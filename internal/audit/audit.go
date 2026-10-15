// Package audit is the append-only, hash-chained audit log: who did what, to what, with what
// outcome. Each entry stores the SHA-256 of the previous entry's hash followed by its own
// canonical content, so changing, removing or reordering an entry breaks the chain from that
// point on and Verify reports where. See docs/adr/0013-authentication-and-audit.md.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
)

// Outcomes of an audited action.
const (
	OutcomeSuccess = "success"
	OutcomeDenied  = "denied"
	OutcomeFailure = "failure"
)

// chainLockKey is the advisory lock that serialises appends. It differs from the
// reconciliation engine's (7426201): the two never wait for each other.
const chainLockKey = 7426202

// Entry is one audit record. ID, PrevHash and Hash are set by Append.
type Entry struct {
	ID        int64
	At        time.Time
	Actor     string // "user:<sub>", "token:<id>", or "anonymous"
	Action    string
	Target    string
	Outcome   string
	RequestID string
	// Details is free-form JSON; secrets are redacted before it is stored (see Redact).
	Details  map[string]any
	PrevHash []byte
	Hash     []byte
}

// Log appends to and verifies the audit chain in PostgreSQL.
type Log struct {
	Store *store.Store
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

func (l *Log) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// genesis is the previous hash of the first entry.
var genesis = make([]byte, sha256.Size)

// canonical is what an entry's hash covers, besides the previous hash. A fixed-order struct
// serialised by encoding/json is unambiguous (every string is quoted and escaped) and needs
// no separator convention. Details is re-serialised from the decoded value, so that it is
// the same bytes whether it comes from the caller or back from PostgreSQL's jsonb (which
// reorders keys and normalises whitespace).
type canonical struct {
	At        string          `json:"at"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Outcome   string          `json:"outcome"`
	RequestID string          `json:"request_id"`
	Details   json.RawMessage `json:"details"`
}

func computeHash(prev []byte, e *Entry) ([]byte, error) {
	details, err := json.Marshal(e.Details)
	if err != nil {
		return nil, err
	}
	if len(e.Details) == 0 {
		details = []byte("{}")
	}
	body, err := json.Marshal(canonical{
		At: e.At.UTC().Format(time.RFC3339Nano), Actor: e.Actor, Action: e.Action, Target: e.Target,
		Outcome: e.Outcome, RequestID: e.RequestID, Details: details,
	})
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(prev)
	h.Write(body)
	return h.Sum(nil), nil
}

// normalise passes details through JSON, so that its in-memory form is the one a read back
// from the database gives (numbers become float64, structs become maps), and redacts it.
func normalise(details map[string]any) (map[string]any, error) {
	if details == nil {
		return map[string]any{}, nil
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return nil, fmt.Errorf("audit: details are not JSON: %w", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		return nil, err
	}
	out, _ := Redact(back).(map[string]any)
	return out, nil
}

// Append records an entry at the end of the chain and returns it with its id and hashes.
// Appends are serialised by a transaction-scoped advisory lock taken before the head is
// read, so concurrent appenders queue and the chain never forks. Appending with the context
// of a request NewMiddleware watches tells it the request has its entry.
func (l *Log) Append(ctx context.Context, e Entry) (*Entry, error) {
	if e.Actor == "" || e.Action == "" {
		return nil, errors.New("audit: an entry needs an actor and an action")
	}
	if e.Outcome == "" {
		e.Outcome = OutcomeSuccess
	}
	if !slices.Contains([]string{OutcomeSuccess, OutcomeDenied, OutcomeFailure}, e.Outcome) {
		return nil, fmt.Errorf("audit: unknown outcome %q", e.Outcome)
	}
	details, err := normalise(e.Details)
	if err != nil {
		return nil, err
	}
	e.Details = details
	e.Target = secretValues.ReplaceAllString(e.Target, Redacted)
	// PostgreSQL stores microseconds; hash what it will give back.
	e.At = l.now().UTC().Truncate(time.Microsecond)
	err = l.Store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, chainLockKey); err != nil {
			return err
		}
		prev := genesis
		err := tx.QueryRow(ctx, `SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		hash, err := computeHash(prev, &e)
		if err != nil {
			return err
		}
		e.PrevHash, e.Hash = prev, hash
		return tx.QueryRow(ctx, `
			INSERT INTO audit_log (at, actor, action, target, outcome, request_id, details, prev_hash, hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			e.At, e.Actor, e.Action, e.Target, e.Outcome, e.RequestID, e.Details, e.PrevHash, e.Hash).Scan(&e.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("audit: append: %w", err)
	}
	markRecorded(ctx)
	return &e, nil
}

// Head returns the id and hash of the last entry of the chain, or 0 and nil when it is
// empty. It is what an anchor records outside the table (ADR 0034): the chain alone cannot
// show that its last entries were removed.
func (l *Log) Head(ctx context.Context) (int64, []byte, error) {
	var id int64
	var hash []byte
	err := l.Store.Pool.QueryRow(ctx, `SELECT id, hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("audit: head: %w", err)
	}
	return id, hash, nil
}

// HashOf returns the hash of entry id, and false when there is no such entry: what an anchor
// compares with the head it recorded earlier, since entries appended after a truncation hide
// it from the head alone.
func (l *Log) HashOf(ctx context.Context, id int64) ([]byte, bool, error) {
	var hash []byte
	err := l.Store.Pool.QueryRow(ctx, `SELECT hash FROM audit_log WHERE id = $1`, id).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("audit: entry %d: %w", id, err)
	}
	return hash, true, nil
}

// Break describes the first entry where the chain does not hold.
type Break struct {
	ID     int64
	Reason string
}

// Result is the outcome of Verify.
type Result struct {
	// Entries is how many entries were checked, including the broken one if any.
	Entries int
	// First is nil when the whole chain is intact.
	First *Break
	// HeadID and Head identify the last entry seen. Record them somewhere the audit table's
	// writers cannot reach: truncating the end of the chain is only detectable against it.
	HeadID int64
	Head   []byte
}

// Intact reports whether no break was found.
func (r *Result) Intact() bool { return r.First == nil }

// Verify walks the whole chain in order, in one consistent snapshot, and reports the first
// entry whose content does not match its hash, or whose previous hash is not the hash of the
// entry before it (an entry was removed, inserted or reordered).
func (l *Log) Verify(ctx context.Context) (*Result, error) {
	res := &Result{}
	err := pgx.BeginTxFunc(ctx, l.Store.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, at, actor, action, target, outcome, request_id, details, prev_hash, hash
			FROM audit_log ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		expectedPrev := genesis
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.Outcome, &e.RequestID, &e.Details, &e.PrevHash, &e.Hash); err != nil {
				return err
			}
			res.Entries++
			res.HeadID, res.Head = e.ID, e.Hash
			// Recompute from the recorded previous hash first: a modified entry is reported
			// at itself even when its previous hash is also wrong.
			want, err := computeHash(e.PrevHash, &e)
			if err != nil {
				return err
			}
			switch {
			case !bytes.Equal(want, e.Hash):
				res.First = &Break{ID: e.ID, Reason: "content does not match its hash"}
			case !bytes.Equal(e.PrevHash, expectedPrev):
				res.First = &Break{ID: e.ID, Reason: "previous hash does not match the preceding entry (entry removed, inserted or reordered)"}
			}
			if res.First != nil {
				return nil
			}
			expectedPrev = e.Hash
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("audit: verify: %w", err)
	}
	return res, nil
}

// List returns the most recent entries, newest first, for display. limit is capped at 1000.
func (l *Log) List(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := l.Store.Pool.Query(ctx, `
		SELECT id, at, actor, action, target, outcome, request_id, details, prev_hash, hash
		FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.Outcome, &e.RequestID, &e.Details, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		e.At = e.At.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
