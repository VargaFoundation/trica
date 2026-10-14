package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Engine applies batches.
type Engine struct {
	Store  *store.Store
	Schema func() *schema.Registry
	Policy Policy
	// Now is the clock used for validity ranges; tests replace it.
	Now func() time.Time
	// OnCommit is called after every committed changeset (graph cache refresh).
	OnCommit func(Result)
}

// Result reports what a batch did.
type Result struct {
	ChangesetID int64 `json:"changeset_id,omitempty"`
	ProposalID  int64 `json:"proposal_id,omitempty"`
	// Objects maps the batch's local refs to inventory ids.
	Objects         map[string]string `json:"objects"`
	Created         int               `json:"created"`
	ValuesChanged   int               `json:"values_changed"`
	RelationsOpened int               `json:"relations_opened"`
	RelationsClosed int               `json:"relations_closed"`
	Warnings        []string          `json:"warnings,omitempty"`
	// Changes describes every change; filled by Preview and kept short otherwise.
	Changes []PlannedChange `json:"changes,omitempty"`
	// Touched lists the ids of objects whose state changed.
	Touched []string `json:"touched,omitempty"`
}

// PlannedChange is one change, as shown in a proposal preview.
type PlannedChange struct {
	Op       string `json:"op"` // create, set, unset, relate, unrelate, move
	ObjectID string `json:"object_id"`
	Type     string `json:"type"`
	Key      string `json:"key,omitempty"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
	OtherID  string `json:"other_id,omitempty"`
}

// ErrInvalid wraps batch validation errors (client errors, HTTP 400).
var ErrInvalid = errors.New("invalid batch")

// ErrRejected wraps batches that are well formed but that the engine refuses
// on policy grounds (HTTP 422 for the API), as opposed to ErrInvalid (400).
var ErrRejected = errors.New("batch rejected")

// checkObservedAge rejects a batch observed longer ago than the policy allows
// for its source kind. A future or zero observed_at is clamped later, not
// rejected. It runs when a batch is submitted (Apply, Propose), not when a
// stored proposal is approved: that one was checked when it was proposed.
func (e *Engine) checkObservedAge(b *sdk.Batch) error {
	if b.ObservedAt.IsZero() {
		return nil
	}
	limit := e.Policy.maxObservedAge(b.Source)
	if age := e.now().Sub(b.ObservedAt); age > limit {
		return fmt.Errorf("%w: observed_at is %s old, the maximum for source %q is %s",
			ErrRejected, age.Round(time.Second), b.Source, limit)
	}
	return nil
}

const maxRecordedChanges = 2000

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

// Submit applies b, or queues it as a proposal when the policy says so.
func (e *Engine) Submit(ctx context.Context, b *sdk.Batch, author, reason string) (*Result, error) {
	if e.Policy.Proposes(b.Source) {
		return e.Propose(ctx, b, author, reason)
	}
	return e.Apply(ctx, b, author, reason)
}

// Apply reconciles b and commits.
func (e *Engine) Apply(ctx context.Context, b *sdk.Batch, author, reason string) (*Result, error) {
	if err := e.checkObservedAge(b); err != nil {
		return nil, err
	}
	var res *Result
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		res, err = e.apply(ctx, tx, b, author, reason, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	if e.OnCommit != nil {
		e.OnCommit(*res)
	}
	return res, nil
}

// Preview reconciles b in a transaction that is rolled back, and returns the
// changes it would make. This is the "preview of the effect" of a proposal.
func (e *Engine) Preview(ctx context.Context, b *sdk.Batch) (*Result, error) {
	tx, err := e.Store.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := e.apply(ctx, tx, b, "", "preview", "")
	if err != nil {
		return nil, err
	}
	res.ChangesetID = 0
	return res, nil
}

// run holds the state of one apply.
type run struct {
	e      *Engine
	tx     pgx.Tx
	reg    *schema.Registry
	b      *sdk.Batch
	now    time.Time
	seen   time.Time
	cs     int64
	author string
	res    *Result
	// ids maps batch refs to object ids.
	ids map[string]string
	// types caches object types by id.
	types map[string]string
	// touchedAttrs: object id -> attribute keys to re-resolve.
	touchedAttrs map[string]map[string]bool
	touchedRels  map[relKey]bool
	touched      map[string]bool
}

type relKey struct{ Type, From, To string }

func (e *Engine) apply(ctx context.Context, tx pgx.Tx, b *sdk.Batch, author, reason, kind string) (*Result, error) {
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if b.Health != nil && !b.Health.OK && b.Scope == sdk.ScopeFull {
		// A failed run never withdraws anything: an unreachable switch has
		// not lost its interfaces.
		c := *b
		c.Scope = sdk.ScopePartial
		b = &c
	}
	reg := e.Schema()
	for _, o := range b.Objects {
		if _, err := reg.Type(o.Type); err != nil {
			return nil, fmt.Errorf("%w: object %s: %w", ErrInvalid, o.Ref, err)
		}
	}
	// One writer at a time: identity resolution reads then writes, and two
	// concurrent batches describing the same new device must not both create it.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7426201)`); err != nil {
		return nil, err
	}
	r := &run{
		e: e, tx: tx, reg: reg, b: b, now: e.now(), author: author,
		res: &Result{Objects: map[string]string{}}, ids: map[string]string{}, types: map[string]string{},
		touchedAttrs: map[string]map[string]bool{}, touchedRels: map[relKey]bool{}, touched: map[string]bool{},
	}
	r.seen = b.ObservedAt.UTC().Truncate(time.Microsecond)
	if r.seen.IsZero() || r.seen.After(r.now) {
		r.seen = r.now
	}
	if kind == "" {
		kind = "collection"
		if b.Confidence == sdk.Declared {
			kind = "declaration"
		}
	}
	if err := tx.QueryRow(ctx, `INSERT INTO changesets (at, source, author, reason, kind) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		r.now, b.Source, author, reason, kind).Scan(&r.cs); err != nil {
		return nil, err
	}
	r.res.ChangesetID = r.cs
	steps := []func(context.Context) error{r.resolveObjects, r.writeClaims, r.writeRelationClaims, r.resolveAttributes, r.resolveRelations, r.writeHealth}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return nil, err
		}
	}
	r.res.Objects = r.ids
	for id := range r.touched {
		r.res.Touched = append(r.res.Touched, id)
	}
	sort.Strings(r.res.Touched)
	stats, _ := json.Marshal(map[string]int{"created": r.res.Created, "values": r.res.ValuesChanged,
		"relations_opened": r.res.RelationsOpened, "relations_closed": r.res.RelationsClosed, "warnings": len(r.res.Warnings)})
	if _, err := tx.Exec(ctx, `UPDATE changesets SET stats = $2 WHERE id = $1`, r.cs, stats); err != nil {
		return nil, err
	}
	return r.res, nil
}

func (r *run) warn(format string, args ...any) {
	if len(r.res.Warnings) < 200 {
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf(format, args...))
	}
}

func (r *run) record(c PlannedChange) {
	if len(r.res.Changes) < maxRecordedChanges {
		r.res.Changes = append(r.res.Changes, c)
	}
	r.touched[c.ObjectID] = true
}

func (r *run) confidence(c sdk.Confidence) sdk.Confidence {
	if c != "" {
		return c
	}
	return r.b.Confidence
}

func (r *run) touchAttr(id, key string) {
	m := r.touchedAttrs[id]
	if m == nil {
		m = map[string]bool{}
		r.touchedAttrs[id] = m
	}
	m[key] = true
}

// ---------------------------------------------------------------- identity

// orderObjects returns objects with parents before children.
func orderObjects(objs []*sdk.Object) []*sdk.Object {
	byRef := map[string]*sdk.Object{}
	for _, o := range objs {
		byRef[o.Ref] = o
	}
	depth := func(o *sdk.Object) int {
		d := 0
		for p := o.Parent; p != ""; p = byRef[p].Parent {
			d++
		}
		return d
	}
	out := append([]*sdk.Object(nil), objs...)
	sort.SliceStable(out, func(i, j int) bool { return depth(out[i]) < depth(out[j]) })
	return out
}

type ident struct {
	Key, Value string
	Strong     bool
}

// identities normalises an object's identifiers against its type.
func identities(t *schema.Type, id map[string]string) ([]ident, error) {
	var out []ident
	for _, k := range sdk.SortedKeys(id) {
		if k == sdk.IDKey {
			continue
		}
		v, err := t.NormalizeIdentity(k, id[k])
		if err != nil {
			return nil, err
		}
		a := t.Attributes[k]
		if a == nil {
			return nil, fmt.Errorf("%s has no attribute %q to identify by", t.Name, k)
		}
		out = append(out, ident{Key: k, Value: v, Strong: a.Identity == schema.Strong})
	}
	return out, nil
}

// find looks up an existing object. parentID scopes weak identifiers ("" =
// no parent). It never creates.
func (r *run) find(ctx context.Context, typ string, idmap map[string]string, parentID string) (string, error) {
	if id := idmap[sdk.IDKey]; id != "" {
		var t string
		err := r.tx.QueryRow(ctx, `SELECT type FROM objects WHERE id = $1::uuid`, id).Scan(&t)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && t != typ) {
			return "", nil
		}
		return id, err
	}
	t, err := r.reg.Type(typ)
	if err != nil {
		return "", err
	}
	ids, err := identities(t, idmap)
	if err != nil {
		return "", err
	}
	var sk, sv, wk, wv []string
	for _, i := range ids {
		if i.Strong {
			sk, sv = append(sk, i.Key), append(sv, i.Value)
		} else {
			wk, wv = append(wk, i.Key), append(wv, i.Value)
		}
	}
	if len(sk) > 0 {
		id, err := r.bestMatch(ctx, typ, sk, sv, true, "", ids)
		if err != nil || id != "" {
			return id, err
		}
	}
	if len(wk) > 0 {
		return r.bestMatch(ctx, typ, wk, wv, false, parentID, ids)
	}
	return "", nil
}

// bestMatch returns the object of type typ sharing the most identifiers in
// (keys, values). Weak candidates whose strong identifiers contradict the
// incoming ones are excluded: a weak match on a reused IP must not merge two
// servers with different serial numbers.
func (r *run) bestMatch(ctx context.Context, typ string, keys, values []string, strong bool, parentID string, all []ident) (string, error) {
	q := `SELECT c.object_id::text, count(DISTINCT c.key) AS n, max(o.last_seen) AS seen
		FROM claims c JOIN objects o ON o.id = c.object_id
		WHERE o.type = $1 AND c.strong = $2 AND (c.key, c.ident) IN (SELECT * FROM unnest($3::text[], $4::text[]))`
	args := []any{typ, strong, keys, values}
	if !strong {
		q += ` AND o.parent_id IS NOT DISTINCT FROM $5::uuid`
		var p any
		if parentID != "" {
			p = parentID
		}
		args = append(args, p)
	}
	q += ` GROUP BY c.object_id ORDER BY n DESC, seen DESC NULLS LAST, c.object_id LIMIT 5`
	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return "", err
	}
	var cands []string
	var counts []int
	for rows.Next() {
		var id string
		var n int
		var seen *time.Time
		if err := rows.Scan(&id, &n, &seen); err != nil {
			rows.Close()
			return "", err
		}
		cands, counts = append(cands, id), append(counts, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	for i, id := range cands {
		// A strong match stands even if another strong identifier changed (a
		// replaced NIC changes the base MAC, not the serial number).
		if !strong {
			ok, err := r.compatible(ctx, id, all)
			if err != nil {
				return "", err
			}
			if !ok {
				continue
			}
		}
		if i+1 < len(cands) && counts[i+1] == counts[i] {
			r.warn("%s %v: several objects match, picked the most recently seen (%s)", typ, values, id)
		}
		return id, nil
	}
	return "", nil
}

// compatible reports whether no strong identifier of object id contradicts the
// incoming ones.
func (r *run) compatible(ctx context.Context, id string, incoming []ident) (bool, error) {
	var keys, values []string
	for _, i := range incoming {
		if i.Strong {
			keys, values = append(keys, i.Key), append(values, i.Value)
		}
	}
	if len(keys) == 0 {
		return true, nil
	}
	var conflict bool
	err := r.tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM claims c JOIN unnest($2::text[], $3::text[]) AS i(k, v) ON c.key = i.k
		               WHERE c.object_id = $1 AND c.strong AND c.ident <> i.v
		               AND NOT EXISTS (SELECT 1 FROM claims d WHERE d.object_id = $1 AND d.key = i.k AND d.ident = i.v))`,
		id, keys, values).Scan(&conflict)
	return !conflict, err
}

func (r *run) resolveObjects(ctx context.Context) error {
	for _, o := range orderObjects(r.b.Objects) {
		parentID := ""
		if o.Parent != "" {
			parentID = r.ids[o.Parent]
		}
		t := r.reg.Types[o.Type]
		if parentID != "" && !t.AllowsParent(r.types[parentID]) {
			r.warn("%s %s: parent type %s not allowed by the schema", o.Type, o.Ref, r.types[parentID])
		}
		id, err := r.find(ctx, o.Type, o.Identity, parentID)
		if err != nil {
			return fmt.Errorf("%w: object %s: %w", ErrInvalid, o.Ref, err)
		}
		conf := r.confidence(o.Confidence)
		observed := conf != sdk.Declared
		if id == "" {
			if o.Identity[sdk.IDKey] != "" {
				return fmt.Errorf("%w: object %s: no %s with id %s", ErrInvalid, o.Ref, o.Type, o.Identity[sdk.IDKey])
			}
			var p any
			if parentID != "" {
				p = parentID
			}
			var first any
			if observed {
				first = r.seen
			}
			if err := r.tx.QueryRow(ctx, `INSERT INTO objects (type, parent_id, created_at, first_seen, last_seen, observed)
				VALUES ($1, $2, $3, $4, $4, $5) RETURNING id::text`, o.Type, p, r.now, first, observed).Scan(&id); err != nil {
				return err
			}
			r.res.Created++
			r.record(PlannedChange{Op: "create", ObjectID: id, Type: o.Type})
			if err := r.setSystem(ctx, id, "_status", "active", conf); err != nil {
				return err
			}
			if parentID != "" {
				if err := r.setSystem(ctx, id, "_parent", parentID, conf); err != nil {
					return err
				}
			}
		} else {
			if err := r.refreshObject(ctx, id, parentID, observed, conf); err != nil {
				return err
			}
		}
		r.ids[o.Ref] = id
		r.types[id] = o.Type
	}
	return nil
}

// refreshObject updates presence and parent of an existing object.
func (r *run) refreshObject(ctx context.Context, id, parentID string, observed bool, conf sdk.Confidence) error {
	var status string
	var curParent *string
	if err := r.tx.QueryRow(ctx, `SELECT status, parent_id::text FROM objects WHERE id = $1`, id).Scan(&status, &curParent); err != nil {
		return err
	}
	if observed {
		if _, err := r.tx.Exec(ctx, `UPDATE objects SET observed = true, first_seen = coalesce(first_seen, $2),
			last_seen = greatest(coalesce(last_seen, $2), $2) WHERE id = $1`, id, r.seen); err != nil {
			return err
		}
		if status == "absent" {
			if _, err := r.tx.Exec(ctx, `UPDATE objects SET status = 'active' WHERE id = $1`, id); err != nil {
				return err
			}
			if err := r.setSystem(ctx, id, "_status", "active", conf); err != nil {
				return err
			}
			r.record(PlannedChange{Op: "set", ObjectID: id, Key: "_status", Before: "absent", After: "active"})
		}
	}
	// A child seen under a new parent moved (a disk to another server).
	// Declared batches may move objects too; a batch that omits the parent
	// leaves it unchanged.
	if parentID != "" && (curParent == nil || *curParent != parentID) {
		if _, err := r.tx.Exec(ctx, `UPDATE objects SET parent_id = $2 WHERE id = $1`, id, parentID); err != nil {
			return err
		}
		before := ""
		if curParent != nil {
			before = *curParent
		}
		r.record(PlannedChange{Op: "move", ObjectID: id, Key: "_parent", Before: before, After: parentID})
		return r.setSystem(ctx, id, "_parent", parentID, conf)
	}
	return nil
}

// setSystem historises a system attribute (_status, _parent).
func (r *run) setSystem(ctx context.Context, id, key string, value any, conf sdk.Confidence) error {
	raw, _ := json.Marshal(value)
	return r.writeHistory(ctx, id, key, &candidate{Source: r.b.Source, Value: raw, Confidence: conf, Author: r.author})
}

// ------------------------------------------------------------------ claims

type claimRow struct {
	value []byte
}

func (r *run) writeClaims(ctx context.Context) error {
	if len(r.ids) == 0 {
		return nil
	}
	ids := make([]string, 0, len(r.ids))
	for _, id := range r.ids {
		ids = append(ids, id)
	}
	existing := map[string]map[string]claimRow{}
	rows, err := r.tx.Query(ctx, `SELECT object_id::text, key, value FROM claims WHERE source = $1 AND object_id = ANY($2::uuid[])`, r.b.Source, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, k string
		var v []byte
		if err := rows.Scan(&id, &k, &v); err != nil {
			rows.Close()
			return err
		}
		if existing[id] == nil {
			existing[id] = map[string]claimRow{}
		}
		existing[id][k] = claimRow{value: canonical(v)}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Several batch objects may resolve to the same inventory object; merge
	// their claims, later ones winning.
	type want struct {
		value  []byte
		ident  *string
		strong bool
		conf   sdk.Confidence
	}
	desired := map[string]map[string]want{}
	withdraw := map[string]map[string]bool{}
	for _, o := range r.b.Objects {
		id := r.ids[o.Ref]
		t := r.reg.Types[o.Type]
		conf := r.confidence(o.Confidence)
		if desired[id] == nil {
			desired[id] = map[string]want{}
		}
		vals := map[string]any{}
		for k, v := range o.Attributes {
			vals[k] = v
		}
		for k, v := range o.Identity {
			if k != sdk.IDKey {
				vals[k] = v
			}
		}
		for _, k := range sdk.SortedKeys(vals) {
			a := t.Attributes[k]
			if a == nil {
				r.warn("%s %s: unknown attribute %q dropped (declare it as a custom field first)", o.Type, o.Ref, k)
				continue
			}
			nv, err := schema.Normalize(a, vals[k])
			if err != nil {
				r.warn("%s %s: %v", o.Type, o.Ref, err)
				continue
			}
			raw, _ := json.Marshal(nv)
			w := want{value: raw, conf: conf}
			if a.Identity != "" {
				s, err := t.NormalizeIdentity(k, fmt.Sprint(vals[k]))
				if err == nil {
					w.ident, w.strong = &s, a.Identity == schema.Strong
				}
			}
			desired[id][k] = w
		}
		for _, k := range o.Withdraw {
			if withdraw[id] == nil {
				withdraw[id] = map[string]bool{}
			}
			withdraw[id][k] = true
		}
	}
	batch := &pgx.Batch{}
	for _, id := range sdk.SortedKeys(desired) {
		for _, k := range sdk.SortedKeys(desired[id]) {
			w := desired[id][k]
			old, had := existing[id][k]
			switch {
			case !had:
				batch.Queue(`INSERT INTO claims (object_id, key, source, value, ident, strong, confidence, first_seen, last_seen, changeset_id)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9)`, id, k, r.b.Source, w.value, w.ident, w.strong, string(w.conf), r.seen, r.cs)
				r.touchAttr(id, k)
			case !bytes.Equal(old.value, canonical(w.value)):
				batch.Queue(`UPDATE claims SET value = $4, ident = $5, strong = $6, confidence = $7, last_seen = $8, changeset_id = $9
					WHERE object_id = $1 AND key = $2 AND source = $3`, id, k, r.b.Source, w.value, w.ident, w.strong, string(w.conf), r.seen, r.cs)
				r.touchAttr(id, k)
			}
		}
		// Unchanged claims: only confirm them, in one statement per object.
		batch.Queue(`UPDATE claims SET last_seen = greatest(last_seen, $3) WHERE object_id = $1 AND source = $2`, id, r.b.Source, r.seen)
	}
	for id, keys := range existing {
		for k := range keys {
			_, still := desired[id][k]
			if (r.b.Scope == sdk.ScopeFull && !still) || withdraw[id][k] {
				batch.Queue(`DELETE FROM claims WHERE object_id = $1 AND key = $2 AND source = $3`, id, k, r.b.Source)
				r.touchAttr(id, k)
			}
		}
	}
	return r.tx.SendBatch(ctx, batch).Close()
}

// canonical re-encodes JSON so that jsonb output and Go output compare equal.
func canonical(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, _ := json.Marshal(v)
	return out
}

// resolveAttributes recomputes the resolved value of every touched attribute.
func (r *run) resolveAttributes(ctx context.Context) error {
	for _, id := range sdk.SortedKeys(r.touchedAttrs) {
		keys := sdk.SortedKeys(r.touchedAttrs[id])
		typ, err := r.typeOf(ctx, id)
		if err != nil {
			return err
		}
		cands := map[string][]candidate{}
		rows, err := r.tx.Query(ctx, `SELECT key, source, value, confidence, last_seen FROM claims WHERE object_id = $1 AND key = ANY($2)`, id, keys)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			var c candidate
			var conf string
			if err := rows.Scan(&k, &c.Source, &c.Value, &conf, &c.LastSeen); err != nil {
				rows.Close()
				return err
			}
			c.Confidence = sdk.Confidence(conf)
			c.Value = canonical(c.Value)
			if c.Source == r.b.Source {
				c.Author = r.author
			}
			cands[k] = append(cands[k], c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, k := range keys {
			var win *candidate
			if cs := cands[k]; len(cs) > 0 {
				w := r.e.Policy.winner(typ, k, cs)
				win = &w
			}
			if err := r.writeHistory(ctx, id, k, win); err != nil {
				return err
			}
			if err := r.checkMismatch(ctx, id, k, cands[k]); err != nil {
				return err
			}
		}
		if err := r.refreshDerived(ctx, id, typ); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) typeOf(ctx context.Context, id string) (string, error) {
	if t, ok := r.types[id]; ok {
		return t, nil
	}
	var t string
	err := r.tx.QueryRow(ctx, `SELECT type FROM objects WHERE id = $1`, id).Scan(&t)
	r.types[id] = t
	return t, err
}

// writeHistory makes win the current history row of (id, key); nil closes it.
func (r *run) writeHistory(ctx context.Context, id, key string, win *candidate) error {
	var curID int64
	var curVal []byte
	var curSource, curConf string
	var curFrom time.Time
	err := r.tx.QueryRow(ctx, `SELECT id, value, source, confidence, lower(valid) FROM attr_history
		WHERE object_id = $1 AND key = $2 AND upper_inf(valid)`, id, key).Scan(&curID, &curVal, &curSource, &curConf, &curFrom)
	had := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if had && win != nil && bytes.Equal(canonical(curVal), canonical(win.Value)) && curSource == win.Source && curConf == string(win.Confidence) {
		return nil
	}
	if !had && win == nil {
		return nil
	}
	var before, after any
	if had {
		_ = json.Unmarshal(curVal, &before)
	}
	if win != nil {
		_ = json.Unmarshal(win.Value, &after)
	}
	if had {
		if !curFrom.Before(r.now) {
			// Same instant (two changes in one transaction): replace in place.
			if win == nil {
				if _, err := r.tx.Exec(ctx, `DELETE FROM attr_history WHERE id = $1`, curID); err != nil {
					return err
				}
			} else if _, err := r.tx.Exec(ctx, `UPDATE attr_history SET value = $2, source = $3, confidence = $4, author = $5, changeset_id = $6 WHERE id = $1`,
				curID, win.Value, win.Source, string(win.Confidence), win.Author, r.cs); err != nil {
				return err
			}
			r.noteValueChange(id, key, before, after)
			return nil
		}
		if _, err := r.tx.Exec(ctx, `UPDATE attr_history SET valid = tstzrange(lower(valid), $2, '[)') WHERE id = $1`, curID, r.now); err != nil {
			return err
		}
	}
	if win != nil {
		if _, err := r.tx.Exec(ctx, `INSERT INTO attr_history (object_id, key, value, source, confidence, author, valid, changeset_id)
			VALUES ($1, $2, $3, $4, $5, $6, tstzrange($7, NULL, '[)'), $8)`,
			id, key, win.Value, win.Source, string(win.Confidence), win.Author, r.now, r.cs); err != nil {
			return err
		}
	}
	r.noteValueChange(id, key, before, after)
	return nil
}

func (r *run) noteValueChange(id, key string, before, after any) {
	if strings.HasPrefix(key, "_") {
		return
	}
	if fmt.Sprint(before) == fmt.Sprint(after) {
		// Provenance changed, not the value.
		r.touched[id] = true
		return
	}
	r.res.ValuesChanged++
	op := "set"
	if after == nil {
		op = "unset"
	}
	r.record(PlannedChange{Op: op, ObjectID: id, Type: r.types[id], Key: key, Before: before, After: after})
}

// checkMismatch opens a discrepancy when a declared value and an observed
// value of the same attribute disagree, and resolves it when they agree.
func (r *run) checkMismatch(ctx context.Context, id, key string, cands []candidate) error {
	var declared, observed *candidate
	for i := range cands {
		c := &cands[i]
		switch c.Confidence {
		case sdk.Declared:
			if declared == nil || c.LastSeen.After(declared.LastSeen) {
				declared = c
			}
		case sdk.Observed:
			if observed == nil || c.LastSeen.After(observed.LastSeen) {
				observed = c
			}
		}
	}
	fp := "value_mismatch:" + id + ":" + key
	if declared != nil && observed != nil && !bytes.Equal(declared.Value, observed.Value) {
		details, _ := json.Marshal(map[string]string{"declared_by": declared.Source, "observed_by": observed.Source})
		return OpenDiscrepancy(ctx, r.tx, Discrepancy{Kind: "value_mismatch", Fingerprint: fp, ObjectID: id, Key: key,
			Declared: declared.Value, Observed: observed.Value, Details: details}, r.now)
	}
	return ResolveDiscrepancy(ctx, r.tx, fp, "reconcile", r.now)
}

// refreshDerived recomputes the materialised current attributes, display
// name and search text of an object.
func (r *run) refreshDerived(ctx context.Context, id, typ string) error {
	attrs := map[string]json.RawMessage{}
	rows, err := r.tx.Query(ctx, `SELECT key, value FROM attr_history WHERE object_id = $1 AND upper_inf(valid) AND key NOT LIKE '\_%'`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return err
		}
		attrs[k] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	t := r.reg.Types[typ]
	display, search := DisplayAndSearch(t, attrs)
	raw, _ := json.Marshal(attrs)
	_, err = r.tx.Exec(ctx, `UPDATE objects SET attrs = $2, display = $3, search_text = $4 WHERE id = $1`, id, raw, display, search)
	return err
}

// DisplayAndSearch derives an object's display name and search text from its
// current attributes.
func DisplayAndSearch(t *schema.Type, attrs map[string]json.RawMessage) (string, string) {
	str := func(raw json.RawMessage) string {
		var v any
		_ = json.Unmarshal(raw, &v)
		switch x := v.(type) {
		case nil:
			return ""
		case string:
			return x
		case []any:
			parts := make([]string, 0, len(x))
			for _, p := range x {
				parts = append(parts, fmt.Sprint(p))
			}
			return strings.Join(parts, " ")
		default:
			return fmt.Sprint(x)
		}
	}
	display := ""
	if t != nil {
		display = str(attrs[t.Display])
	}
	var parts []string
	for _, k := range sdk.SortedKeys(attrs) {
		a := (*schema.Attribute)(nil)
		if t != nil {
			a = t.Attributes[k]
		}
		if a != nil && (a.Search || a.Identity != "") {
			if s := str(attrs[k]); s != "" {
				parts = append(parts, s)
			}
		}
		if display == "" && a != nil && a.Identity != "" {
			display = str(attrs[k])
		}
	}
	return display, strings.Join(parts, " ")
}

// -------------------------------------------------------------- relations

// resolveEndpoint returns the object id an endpoint designates, or "".
func (r *run) resolveEndpoint(ctx context.Context, ep sdk.Endpoint) (string, error) {
	if ep.Ref != "" {
		return r.ids[ep.Ref], nil
	}
	parentID := ""
	if ep.Parent != nil {
		p, err := r.resolveEndpoint(ctx, *ep.Parent)
		if err != nil || p == "" {
			return "", err
		}
		parentID = p
	}
	if _, err := r.reg.Type(ep.Type); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return r.find(ctx, ep.Type, ep.Identity, parentID)
}

func describeEndpoint(ep sdk.Endpoint) string {
	if ep.Ref != "" {
		return ep.Ref
	}
	s := ep.Type
	for _, k := range sdk.SortedKeys(ep.Identity) {
		s += " " + k + "=" + ep.Identity[k]
	}
	if ep.Parent != nil {
		s += " in " + describeEndpoint(*ep.Parent)
	}
	return s
}

func (r *run) writeRelationClaims(ctx context.Context) error {
	type relWant struct {
		attrs []byte
		conf  sdk.Confidence
	}
	desired := map[relKey]relWant{}
	for _, rel := range r.b.Relations {
		rt, err := r.reg.Relation(rel.Type)
		if err != nil {
			r.warn("%v", err)
			continue
		}
		from, err := r.resolveEndpoint(ctx, rel.From)
		if err != nil {
			return err
		}
		to, err := r.resolveEndpoint(ctx, rel.To)
		if err != nil {
			return err
		}
		if from == "" || to == "" {
			if err := r.unresolved(ctx, rel, from, to); err != nil {
				return err
			}
			continue
		}
		if rel.From.Ref == "" || rel.To.Ref == "" {
			// It resolves now: close the gap it may have opened before.
			for _, fp := range []string{unresolvedFingerprint(rel, from, rel.To), unresolvedFingerprint(rel, to, rel.From)} {
				if err := ResolveDiscrepancy(ctx, r.tx, fp, "reconcile", r.now); err != nil {
					return err
				}
			}
		}
		ft, _ := r.typeOf(ctx, from)
		tt, _ := r.typeOf(ctx, to)
		if !rt.Allows(ft, tt) {
			r.warn("relation %s not allowed from %s to %s", rel.Type, ft, tt)
			continue
		}
		attrs := map[string]any{}
		for _, k := range sdk.SortedKeys(rel.Attributes) {
			a := rt.Attributes[k]
			if a == nil {
				r.warn("relation %s: unknown attribute %q dropped", rel.Type, k)
				continue
			}
			v, err := schema.Normalize(a, rel.Attributes[k])
			if err != nil {
				r.warn("relation %s: %v", rel.Type, err)
				continue
			}
			attrs[k] = v
		}
		raw, _ := json.Marshal(attrs)
		desired[relKey{rel.Type, from, to}] = relWant{attrs: raw, conf: r.confidence(rel.Confidence)}
	}
	unrelate := map[relKey]bool{}
	for _, rel := range r.b.Unrelations {
		from, err := r.resolveEndpoint(ctx, rel.From)
		if err != nil {
			return err
		}
		to, err := r.resolveEndpoint(ctx, rel.To)
		if err != nil {
			return err
		}
		if from != "" && to != "" {
			unrelate[relKey{rel.Type, from, to}] = true
		}
	}
	existing := map[relKey][]byte{}
	rows, err := r.tx.Query(ctx, `SELECT type, from_id::text, to_id::text, attrs FROM rel_claims WHERE source = $1`, r.b.Source)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k relKey
		var a []byte
		if err := rows.Scan(&k.Type, &k.From, &k.To, &a); err != nil {
			rows.Close()
			return err
		}
		existing[k] = canonical(a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for k, w := range desired {
		old, had := existing[k]
		switch {
		case !had:
			batch.Queue(`INSERT INTO rel_claims (type, from_id, to_id, source, attrs, confidence, first_seen, last_seen, changeset_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8)`, k.Type, k.From, k.To, r.b.Source, w.attrs, string(w.conf), r.seen, r.cs)
			r.touchedRels[k] = true
		case !bytes.Equal(old, canonical(w.attrs)):
			batch.Queue(`UPDATE rel_claims SET attrs = $5, confidence = $6, last_seen = $7, changeset_id = $8
				WHERE type = $1 AND from_id = $2 AND to_id = $3 AND source = $4`, k.Type, k.From, k.To, r.b.Source, w.attrs, string(w.conf), r.seen, r.cs)
			r.touchedRels[k] = true
		default:
			batch.Queue(`UPDATE rel_claims SET last_seen = greatest(last_seen, $5)
				WHERE type = $1 AND from_id = $2 AND to_id = $3 AND source = $4`, k.Type, k.From, k.To, r.b.Source, r.seen)
		}
	}
	for k := range existing {
		_, still := desired[k]
		if (r.b.Scope == sdk.ScopeFull && !still) || unrelate[k] {
			batch.Queue(`DELETE FROM rel_claims WHERE type = $1 AND from_id = $2 AND to_id = $3 AND source = $4`, k.Type, k.From, k.To, r.b.Source)
			r.touchedRels[k] = true
		}
	}
	return r.tx.SendBatch(ctx, batch).Close()
}

// unresolved records a relation whose far end is not in the inventory yet:
// typically an LLDP neighbour that no collector reports. It is retried on
// every collection and closed as soon as it resolves.
func (r *run) unresolved(ctx context.Context, rel *sdk.Relation, from, to string) error {
	anchor, missing := from, rel.To
	if from == "" {
		anchor, missing = to, rel.From
	}
	kind := "unresolved_reference"
	if rel.Type == "neighbor_of" {
		kind = "unresolved_neighbor"
	}
	desc := describeEndpoint(missing)
	r.warn("%s: %s not found", rel.Type, desc)
	if anchor == "" {
		return nil
	}
	details, _ := json.Marshal(map[string]string{"relation": rel.Type, "missing": desc, "source": r.b.Source})
	return OpenDiscrepancy(ctx, r.tx, Discrepancy{Kind: kind, Fingerprint: unresolvedFingerprint(rel, anchor, missing),
		ObjectID: anchor, Details: details}, r.now)
}

func unresolvedFingerprint(rel *sdk.Relation, anchor string, missing sdk.Endpoint) string {
	kind := "unresolved_reference"
	if rel.Type == "neighbor_of" {
		kind = "unresolved_neighbor"
	}
	sum := sha256.Sum256([]byte(describeEndpoint(missing)))
	return kind + ":" + anchor + ":" + rel.Type + ":" + hex.EncodeToString(sum[:8])
}

func (r *run) resolveRelations(ctx context.Context) error {
	keys := make([]relKey, 0, len(r.touchedRels))
	for k := range r.touchedRels {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		return a.Type+a.From+a.To < b.Type+b.From+b.To
	})
	for _, k := range keys {
		var cands []candidate
		var attrsBy = map[string][]byte{}
		rows, err := r.tx.Query(ctx, `SELECT source, attrs, confidence, last_seen FROM rel_claims WHERE type = $1 AND from_id = $2 AND to_id = $3`, k.Type, k.From, k.To)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c candidate
			var conf string
			var a []byte
			if err := rows.Scan(&c.Source, &a, &conf, &c.LastSeen); err != nil {
				rows.Close()
				return err
			}
			c.Confidence = sdk.Confidence(conf)
			attrsBy[c.Source] = canonical(a)
			cands = append(cands, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var curID int64
		var curAttrs []byte
		var curSource, curConf string
		var curFrom time.Time
		err = r.tx.QueryRow(ctx, `SELECT id, attrs, source, confidence, lower(valid) FROM relations
			WHERE type = $1 AND from_id = $2 AND to_id = $3 AND upper_inf(valid)`, k.Type, k.From, k.To).Scan(&curID, &curAttrs, &curSource, &curConf, &curFrom)
		had := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if len(cands) == 0 {
			if had {
				if err := r.closeRelation(ctx, curID, curFrom); err != nil {
					return err
				}
				r.res.RelationsClosed++
				r.record(PlannedChange{Op: "unrelate", ObjectID: k.From, Key: k.Type, OtherID: k.To})
			}
			continue
		}
		w := r.e.Policy.winner("relation", k.Type, cands)
		wa := attrsBy[w.Source]
		if had && bytes.Equal(canonical(curAttrs), wa) && curSource == w.Source && curConf == string(w.Confidence) {
			continue
		}
		if had {
			if err := r.closeRelation(ctx, curID, curFrom); err != nil {
				return err
			}
		}
		if _, err := r.tx.Exec(ctx, `INSERT INTO relations (type, from_id, to_id, attrs, source, confidence, valid, changeset_id)
			VALUES ($1, $2, $3, $4, $5, $6, tstzrange($7, NULL, '[)'), $8)`, k.Type, k.From, k.To, wa, w.Source, string(w.Confidence), r.now, r.cs); err != nil {
			return err
		}
		if !had {
			r.res.RelationsOpened++
			var a any
			_ = json.Unmarshal(wa, &a)
			r.record(PlannedChange{Op: "relate", ObjectID: k.From, Key: k.Type, OtherID: k.To, After: a})
		} else {
			r.touched[k.From], r.touched[k.To] = true, true
		}
	}
	return nil
}

func (r *run) closeRelation(ctx context.Context, id int64, from time.Time) error {
	if !from.Before(r.now) {
		_, err := r.tx.Exec(ctx, `DELETE FROM relations WHERE id = $1`, id)
		return err
	}
	_, err := r.tx.Exec(ctx, `UPDATE relations SET valid = tstzrange(lower(valid), $2, '[)'), closed_by = $3 WHERE id = $1`, id, r.now, r.cs)
	return err
}

// ------------------------------------------------------------------ health

func (r *run) writeHealth(ctx context.Context) error {
	h := r.b.Health
	if h == nil {
		return nil
	}
	warnings, _ := json.Marshal(append(append([]string{}, h.Warnings...), r.res.Warnings...))
	var success any
	if h.OK {
		success = r.seen
	}
	_, err := r.tx.Exec(ctx, `
		INSERT INTO source_health (source, collector, target, version, ok, last_attempt, last_success, last_error, duration_ms, items, warnings, failures)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, CASE WHEN $5 THEN 0 ELSE 1 END)
		ON CONFLICT (source) DO UPDATE SET collector = $2, target = $3, version = $4, ok = $5, last_attempt = $6,
			last_success = coalesce($7, source_health.last_success), last_error = $8, duration_ms = $9, items = $10,
			warnings = $11, failures = CASE WHEN $5 THEN 0 ELSE source_health.failures + 1 END`,
		r.b.Source, h.Collector, h.Target, h.Version, h.OK, r.seen, success, h.Error, h.Duration.Milliseconds(), h.Items, warnings)
	return err
}
