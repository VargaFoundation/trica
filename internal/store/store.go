// Package store is the PostgreSQL persistence layer: connection, migrations,
// and the read model (current state, provenance, history, time travel).
// Writes to the inventory go through package reconcile, never directly.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNotFound is returned when a looked-up row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps a connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Open connects to url and checks the connection.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("store: parse url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.Pool.Close() }

// migrateLock is the advisory lock key serialising migrations.
const migrateLock = 7426202

// Migrate applies every pending migration. Several replicas starting at once
// take turns: the first migrates, the others find nothing left to apply.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateLock); err != nil {
		return fmt.Errorf("store: migration lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrateLock) }()
	return s.goose(func(p *goose.Provider) error {
		_, err := p.Up(ctx)
		return err
	})
}

// MigrateDown rolls every migration back. Used by the reversibility test.
func (s *Store) MigrateDown(ctx context.Context) error {
	return s.goose(func(p *goose.Provider) error {
		_, err := p.DownTo(ctx, 0)
		return err
	})
}

// MigrateDownOne rolls the latest migration back, and only that one.
func (s *Store) MigrateDownOne(ctx context.Context) error {
	return s.goose(func(p *goose.Provider) error {
		_, err := p.Down(ctx)
		return err
	})
}

// SchemaVersion returns the version of the latest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.goose(func(p *goose.Provider) (err error) {
		v, err = p.GetDBVersion(ctx)
		return err
	})
	return v, err
}

// KnownVersion is the version of the newest migration this binary embeds: the
// schema it knows how to run against.
func KnownVersion() int64 {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return 0
	}
	var newest int64
	for _, e := range entries {
		n, _, _ := strings.Cut(e.Name(), "_")
		if v, err := strconv.ParseInt(n, 10, 64); err == nil && v > newest {
			newest = v
		}
	}
	return newest
}

// goose runs fn with a migration provider of its own: goose's package-level
// API keeps global state and races when two stores migrate in one process.
func (s *Store) goose(fn func(*goose.Provider) error) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	db := stdlib.OpenDBFromPool(s.Pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return fmt.Errorf("store: migrations: %w", err)
	}
	return fn(p)
}

// InTx runs fn in a transaction, committing on success.
func (s *Store) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, fn)
}

// Object is the current state of an inventory object.
type Object struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	ParentID  *string        `json:"parent_id,omitempty"`
	Display   string         `json:"display"`
	Status    string         `json:"status"`
	Attrs     map[string]any `json:"attributes"`
	CreatedAt time.Time      `json:"created_at"`
	FirstSeen *time.Time     `json:"first_seen,omitempty"`
	LastSeen  *time.Time     `json:"last_seen,omitempty"`
	Observed  bool           `json:"observed"`
}

const objectCols = `id, type, parent_id, display, status, attrs, created_at, first_seen, last_seen, observed`

// ObjectColumns is the select list CollectObjects expects, for read-only
// queries built elsewhere (package search).
const ObjectColumns = objectCols

// CollectObjects scans rows selected with ObjectColumns and closes them.
func CollectObjects(rows pgx.Rows) ([]*Object, error) { return collectObjects(rows) }

func scanObject(row pgx.Row) (*Object, error) {
	var o Object
	var attrs []byte
	if err := row.Scan(&o.ID, &o.Type, &o.ParentID, &o.Display, &o.Status, &attrs, &o.CreatedAt, &o.FirstSeen, &o.LastSeen, &o.Observed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(attrs, &o.Attrs); err != nil {
		return nil, err
	}
	return &o, nil
}

// Querier is satisfied by a pool and a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Object returns one object by id.
func (s *Store) Object(ctx context.Context, id string) (*Object, error) {
	return scanObject(s.Pool.QueryRow(ctx, `SELECT `+objectCols+` FROM objects WHERE id = $1`, id))
}

// Objects returns objects by id, in no particular order; unknown ids are skipped.
func (s *Store) Objects(ctx context.Context, ids []string) ([]*Object, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+objectCols+` FROM objects WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	return collectObjects(rows)
}

// ListFilter selects objects.
type ListFilter struct {
	Type     string
	ParentID string
	Status   string
	// Attrs: exact matches on current attribute values (JSON containment).
	Attrs  map[string]any
	Limit  int
	Offset int
}

// List returns objects matching f, ordered by display, and the total count.
func (s *Store) List(ctx context.Context, f ListFilter) ([]*Object, int, error) {
	where, args := "TRUE", []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.Type != "" {
		add("type = $%d", f.Type)
	}
	if f.ParentID != "" {
		add("parent_id = $%d", f.ParentID)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if len(f.Attrs) > 0 {
		b, _ := json.Marshal(f.Attrs)
		add("attrs @> $%d::jsonb", string(b))
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM objects WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	args = append(args, limit, f.Offset)
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`SELECT `+objectCols+` FROM objects WHERE %s ORDER BY type, display, id LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	objs, err := collectObjects(rows)
	return objs, total, err
}

func collectObjects(rows pgx.Rows) ([]*Object, error) {
	defer rows.Close()
	var out []*Object
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Value is one resolved attribute value with its provenance.
type Value struct {
	Key         string     `json:"key"`
	Value       any        `json:"value"`
	Source      string     `json:"source"`
	Confidence  string     `json:"confidence"`
	Author      string     `json:"author,omitempty"`
	ValidFrom   time.Time  `json:"valid_from"`
	ValidTo     *time.Time `json:"valid_to,omitempty"`
	ChangesetID int64      `json:"changeset_id"`
	// LastSeen is when the winning source last confirmed the value (current values only).
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

// Provenance returns the current value of every attribute of an object with
// its source, confidence, first and last confirmation.
func (s *Store) Provenance(ctx context.Context, id string) ([]Value, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT h.key, h.value, h.source, h.confidence, h.author, lower(h.valid), h.changeset_id, c.last_seen
		FROM attr_history h
		LEFT JOIN claims c ON c.object_id = h.object_id AND c.key = h.key AND c.source = h.source
		WHERE h.object_id = $1 AND upper_inf(h.valid)
		ORDER BY h.key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Value
	for rows.Next() {
		var v Value
		var raw []byte
		if err := rows.Scan(&v.Key, &raw, &v.Source, &v.Confidence, &v.Author, &v.ValidFrom, &v.ChangesetID, &v.LastSeen); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &v.Value)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Claim is what one source currently says about one attribute.
type Claim struct {
	Key        string    `json:"key"`
	Source     string    `json:"source"`
	Value      any       `json:"value"`
	Confidence string    `json:"confidence"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
}

// Claims returns every source's current claims on an object.
func (s *Store) Claims(ctx context.Context, id string) ([]Claim, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key, source, value, confidence, first_seen, last_seen FROM claims WHERE object_id = $1 ORDER BY key, source`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		var c Claim
		var raw []byte
		if err := rows.Scan(&c.Key, &c.Source, &raw, &c.Confidence, &c.FirstSeen, &c.LastSeen); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &c.Value)
		out = append(out, c)
	}
	return out, rows.Err()
}

// RelationClaim is what one source currently says about one relation.
type RelationClaim struct {
	Type       string    `json:"type"`
	FromID     string    `json:"from_id"`
	ToID       string    `json:"to_id"`
	Source     string    `json:"source"`
	Confidence string    `json:"confidence"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
}

// RelationClaims returns every source's current claims on relations of one
// type, from the objects fromIDs, or from any object when fromIDs is nil.
// Ordered by from, last seen (newest first), then source.
func (s *Store) RelationClaims(ctx context.Context, typ string, fromIDs []string) ([]RelationClaim, error) {
	q := `SELECT type, from_id::text, to_id::text, source, confidence, first_seen, last_seen FROM rel_claims WHERE type = $1`
	args := []any{typ}
	if fromIDs != nil {
		if len(fromIDs) == 0 {
			return nil, nil
		}
		q += ` AND from_id = ANY($2::uuid[])`
		args = append(args, fromIDs)
	}
	rows, err := s.Pool.Query(ctx, q+` ORDER BY from_id, last_seen DESC, source`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelationClaim
	for rows.Next() {
		var c RelationClaim
		if err := rows.Scan(&c.Type, &c.FromID, &c.ToID, &c.Source, &c.Confidence, &c.FirstSeen, &c.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// History returns every value an object's attributes ever had, newest first.
// key filters on one attribute when not empty.
func (s *Store) History(ctx context.Context, id, key string, limit int) ([]Value, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT key, value, source, confidence, author, lower(valid), upper(valid), changeset_id
		FROM attr_history WHERE object_id = $1 AND ($2 = '' OR key = $2)
		ORDER BY lower(valid) DESC, key LIMIT $3`, id, key, limit)
	if err != nil {
		return nil, err
	}
	return scanValues(rows)
}

// HistoryBetween returns the values of one attribute that were valid at some
// point of [from, to), oldest first, at most limit of them; truncated says
// whether more were left out (the newest ones).
func (s *Store) HistoryBetween(ctx context.Context, id, key string, from, to time.Time, limit int) (vals []Value, truncated bool, err error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT key, value, source, confidence, author, lower(valid), upper(valid), changeset_id
		FROM attr_history WHERE object_id = $1 AND key = $2 AND valid && tstzrange($3, $4)
		ORDER BY lower(valid) LIMIT $5`, id, key, from, to, limit+1)
	if err != nil {
		return nil, false, err
	}
	vals, err = scanValues(rows)
	if err != nil {
		return nil, false, err
	}
	if len(vals) > limit {
		return vals[:limit], true, nil
	}
	return vals, false, nil
}

// KeysWithHistory returns those of keys for which the object has at least one value
// valid at some point of [from, to), current or closed, sorted; never nil.
func (s *Store) KeysWithHistory(ctx context.Context, id string, keys []string, from, to time.Time) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT k FROM unnest($2::text[]) AS k
		WHERE EXISTS (SELECT 1 FROM attr_history h
		              WHERE h.object_id = $1 AND h.key = k AND h.valid && tstzrange($3, $4))
		ORDER BY k`, id, keys, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func scanValues(rows pgx.Rows) ([]Value, error) {
	defer rows.Close()
	var out []Value
	for rows.Next() {
		var v Value
		var raw []byte
		if err := rows.Scan(&v.Key, &raw, &v.Source, &v.Confidence, &v.Author, &v.ValidFrom, &v.ValidTo, &v.ChangesetID); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &v.Value)
		out = append(out, v)
	}
	return out, rows.Err()
}

// AsOf returns the attribute values an object had at time t. The result is
// empty when the object did not exist yet.
func (s *Store) AsOf(ctx context.Context, id string, t time.Time) (map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key, value FROM attr_history WHERE object_id = $1 AND valid @> $2::timestamptz`, id, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var k string
		var raw []byte
		if err := rows.Scan(&k, &raw); err != nil {
			return nil, err
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		out[k] = v
	}
	return out, rows.Err()
}

// Relation is one relation, current or past.
type Relation struct {
	ID         int64          `json:"id"`
	Type       string         `json:"type"`
	FromID     string         `json:"from_id"`
	ToID       string         `json:"to_id"`
	Attrs      map[string]any `json:"attributes,omitempty"`
	Source     string         `json:"source"`
	Confidence string         `json:"confidence"`
	ValidFrom  time.Time      `json:"valid_from"`
	ValidTo    *time.Time     `json:"valid_to,omitempty"`
}

const relationCols = `id, type, from_id, to_id, attrs, source, confidence, lower(valid), upper(valid)`

func scanRelations(rows pgx.Rows) ([]Relation, error) {
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var r Relation
		var raw []byte
		if err := rows.Scan(&r.ID, &r.Type, &r.FromID, &r.ToID, &raw, &r.Source, &r.Confidence, &r.ValidFrom, &r.ValidTo); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &r.Attrs)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RelationsOf returns the relations touching an object, current when at is
// nil, else valid at *at.
func (s *Store) RelationsOf(ctx context.Context, id string, at *time.Time) ([]Relation, error) {
	q := `SELECT ` + relationCols + ` FROM relations WHERE (from_id = $1 OR to_id = $1) AND upper_inf(valid) ORDER BY type, id`
	args := []any{id}
	if at != nil {
		q = `SELECT ` + relationCols + ` FROM relations WHERE (from_id = $1 OR to_id = $1) AND valid @> $2::timestamptz ORDER BY type, id`
		args = append(args, *at)
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// CurrentRelationsOfTypes returns the current relations of the given types,
// oldest id first, at most limit of them. It is the bulk read behind the
// topology views: one query instead of one per object.
func (s *Store) CurrentRelationsOfTypes(ctx context.Context, types []string, limit int) ([]Relation, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+relationCols+` FROM relations WHERE type = ANY($1::text[]) AND upper_inf(valid) ORDER BY id LIMIT $2`, types, limit)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// CurrentRelationsFrom returns the current relations of one type that start
// at one of these objects, oldest id first: the models of the devices of a
// rack in one query instead of one per device.
func (s *Store) CurrentRelationsFrom(ctx context.Context, typ string, fromIDs []string) ([]Relation, error) {
	if len(fromIDs) == 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+relationCols+` FROM relations WHERE type = $1 AND from_id = ANY($2::uuid[]) AND upper_inf(valid) ORDER BY id`, typ, fromIDs)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// DescendantValue is one current attribute value of an object below a root:
// a component of a device (a power supply, a fan, a disk) and its own parts.
type DescendantValue struct {
	// Root is the id of the ancestor the walk started from.
	Root    string
	ID      string
	Type    string
	Display string
	Status  string
	// Value is the attribute as text; empty when the object does not have it.
	Value string
}

// DescendantValues returns, for every object at most depth levels below one
// of roots (through parent_id), its type, status and the current value of
// one attribute: the health of the parts of a rack's devices in one query
// instead of one per device.
func (s *Store) DescendantValues(ctx context.Context, roots []string, key string, depth int) ([]DescendantValue, error) {
	if len(roots) == 0 || depth <= 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `
WITH RECURSIVE d AS (
  SELECT o.parent_id AS root, o.id, 1 AS lvl FROM objects o WHERE o.parent_id = ANY($1::uuid[])
  UNION ALL
  SELECT d.root, o.id, d.lvl + 1 FROM objects o JOIN d ON o.parent_id = d.id WHERE d.lvl < $3
)
SELECT d.root, o.id, o.type, o.display, o.status, COALESCE(o.attrs->>$2, '') FROM d JOIN objects o ON o.id = d.id ORDER BY d.root, o.id`, roots, key, depth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DescendantValue
	for rows.Next() {
		var v DescendantValue
		if err := rows.Scan(&v.Root, &v.ID, &v.Type, &v.Display, &v.Status, &v.Value); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Descendants returns the objects below root, depth levels down at most (1:
// its children), absent ones included, ordered by type then display. At
// most limit objects are returned; the second result says whether there
// were more.
func (s *Store) Descendants(ctx context.Context, root string, depth, limit int) ([]*Object, bool, error) {
	if depth <= 0 || limit <= 0 {
		return nil, false, nil
	}
	rows, err := s.Pool.Query(ctx, `
WITH RECURSIVE d AS (
  SELECT id, 1 AS lvl FROM objects WHERE parent_id = $1
  UNION ALL
  SELECT o.id, d.lvl + 1 FROM objects o JOIN d ON o.parent_id = d.id WHERE d.lvl < $2
)
SELECT `+objectCols+` FROM objects WHERE id IN (SELECT id FROM d) ORDER BY type, display, id LIMIT $3`, root, depth, limit+1)
	if err != nil {
		return nil, false, err
	}
	out, err := collectObjects(rows)
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// RelationHistory returns every relation that ever touched an object, newest first.
func (s *Store) RelationHistory(ctx context.Context, id string, limit int) ([]Relation, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+relationCols+` FROM relations WHERE from_id = $1 OR to_id = $1 ORDER BY lower(valid) DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// MountHistory returns the mounted_in relations a device ever had, current
// ones first, then the closed ones, the most recently closed first. It is the
// read behind "where was it before" (ADR 0053).
func (s *Store) MountHistory(ctx context.Context, deviceID string, limit int) ([]Relation, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+relationCols+` FROM relations
WHERE type = 'mounted_in' AND from_id = $1
ORDER BY upper_inf(valid) DESC, upper(valid) DESC, id DESC LIMIT $2`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// RemovedFrom returns, for the devices taken out of a rack, the mounted_in
// relation that ended there: a device is listed when its most recent mount
// (across every rack) ended in this rack and it has no current mount. A
// device moved to another rack, or put back, is not listed. Newest first;
// since, when given, keeps what ended at or after it.
func (s *Store) RemovedFrom(ctx context.Context, rackID string, since *time.Time, limit int) ([]Relation, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+relationCols+` FROM (
  SELECT DISTINCT ON (r.from_id) r.* FROM relations r
  WHERE r.type = 'mounted_in'
    AND r.from_id IN (SELECT from_id FROM relations WHERE type = 'mounted_in' AND to_id = $1 AND NOT upper_inf(valid))
    AND NOT EXISTS (SELECT 1 FROM relations c WHERE c.type = 'mounted_in' AND c.from_id = r.from_id AND upper_inf(c.valid))
  ORDER BY r.from_id, upper(r.valid) DESC, r.id DESC
) lastm
WHERE lastm.to_id = $1 AND ($2::timestamptz IS NULL OR upper(lastm.valid) >= $2)
ORDER BY upper(lastm.valid) DESC, lastm.id DESC LIMIT $3`, rackID, since, limit)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// CurrentRelations streams every current relation; used to build the graph cache.
func (s *Store) CurrentRelations(ctx context.Context) ([]Relation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+relationCols+` FROM relations WHERE upper_inf(valid)`)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// ObjectsOfTypes returns the objects of these types that are not absent: the
// read behind the live overlay's join, which needs the interfaces and their
// owners only.
func (s *Store) ObjectsOfTypes(ctx context.Context, types []string) ([]*Object, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+objectCols+` FROM objects WHERE type = ANY($1::text[]) AND status <> 'absent'`, types)
	if err != nil {
		return nil, err
	}
	return collectObjects(rows)
}

// CurrentRelationsBetween returns the current relations of one type whose
// ends have these object types (member_of from an interface to an
// interface: the aggregates).
func (s *Store) CurrentRelationsBetween(ctx context.Context, typ, fromType, toType string) ([]Relation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.id, r.type, r.from_id, r.to_id, r.attrs, r.source, r.confidence, lower(r.valid), upper(r.valid) FROM relations r
		JOIN objects f ON f.id = r.from_id JOIN objects t ON t.id = r.to_id
		WHERE r.type = $1 AND upper_inf(r.valid) AND f.type = $2 AND t.type = $3`, typ, fromType, toType)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

// AllObjects returns every object; used to build the graph cache.
func (s *Store) AllObjects(ctx context.Context) ([]*Object, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+objectCols+` FROM objects`)
	if err != nil {
		return nil, err
	}
	return collectObjects(rows)
}

// Change is one entry of a diff or a timeline.
type Change struct {
	At          time.Time `json:"at"`
	ObjectID    string    `json:"object_id"`
	Type        string    `json:"type"`
	Display     string    `json:"display"`
	Key         string    `json:"key,omitempty"`
	Relation    string    `json:"relation,omitempty"`
	OtherID     string    `json:"other_id,omitempty"`
	Before      any       `json:"before,omitempty"`
	After       any       `json:"after,omitempty"`
	Source      string    `json:"source"`
	ChangesetID int64     `json:"changeset_id"`
}

// Changes lists attribute and relation changes between from and to, newest
// first. objectID restricts to one object when not empty.
func (s *Store) Changes(ctx context.Context, from, to time.Time, objectID string, limit int) ([]Change, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.Pool.Query(ctx, `
		WITH attr AS (
			SELECT lower(h.valid) AS at, h.object_id, h.key, '' AS relation, NULL::uuid AS other_id,
			       p.value AS before, h.value AS after, h.source, h.changeset_id
			FROM attr_history h
			LEFT JOIN attr_history p ON p.object_id = h.object_id AND p.key = h.key AND upper(p.valid) = lower(h.valid)
			WHERE lower(h.valid) > $1 AND lower(h.valid) <= $2 AND ($3 = '' OR h.object_id = $3::uuid)
		), gone AS (
			SELECT upper(h.valid) AS at, h.object_id, h.key, '' AS relation, NULL::uuid AS other_id,
			       h.value AS before, NULL::jsonb AS after, h.source, h.changeset_id
			FROM attr_history h
			WHERE upper(h.valid) > $1 AND upper(h.valid) <= $2 AND ($3 = '' OR h.object_id = $3::uuid)
			  AND NOT EXISTS (SELECT 1 FROM attr_history n WHERE n.object_id = h.object_id AND n.key = h.key AND lower(n.valid) = upper(h.valid))
		), rel_new AS (
			SELECT lower(valid) AS at, from_id AS object_id, '' AS key, type AS relation, to_id AS other_id,
			       NULL::jsonb AS before, attrs AS after, source, changeset_id
			FROM relations WHERE lower(valid) > $1 AND lower(valid) <= $2 AND ($3 = '' OR from_id = $3::uuid OR to_id = $3::uuid)
		), rel_gone AS (
			SELECT upper(valid) AS at, from_id AS object_id, '' AS key, type AS relation, to_id AS other_id,
			       attrs AS before, NULL::jsonb AS after, source, coalesce(closed_by, changeset_id)
			FROM relations WHERE upper(valid) > $1 AND upper(valid) <= $2 AND ($3 = '' OR from_id = $3::uuid OR to_id = $3::uuid)
		)
		SELECT c.at, c.object_id, o.type, o.display, c.key, c.relation, coalesce(c.other_id::text, ''), c.before, c.after, c.source, c.changeset_id
		FROM (SELECT * FROM attr UNION ALL SELECT * FROM gone UNION ALL SELECT * FROM rel_new UNION ALL SELECT * FROM rel_gone) c
		JOIN objects o ON o.id = c.object_id
		ORDER BY c.at DESC, c.object_id, c.key, c.relation LIMIT $4`, from, to, objectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var before, after []byte
		if err := rows.Scan(&c.At, &c.ObjectID, &c.Type, &c.Display, &c.Key, &c.Relation, &c.OtherID, &before, &after, &c.Source, &c.ChangesetID); err != nil {
			return nil, err
		}
		if before != nil {
			_ = json.Unmarshal(before, &c.Before)
		}
		if after != nil {
			_ = json.Unmarshal(after, &c.After)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WhoHad answers "which object had attribute key = value at time t" (or at
// any time when t is nil), e.g. which device had an IP on 3 March.
func (s *Store) WhoHad(ctx context.Context, key, value string, t *time.Time) ([]Value, []string, error) {
	q := `SELECT h.key, h.value, h.source, h.confidence, h.author, lower(h.valid), upper(h.valid), h.changeset_id, h.object_id
		FROM attr_history h WHERE ($1 = '' OR h.key = $1) AND h.value #>> '{}' = $2`
	args := []any{key, value}
	if t != nil {
		q += ` AND h.valid @> $3::timestamptz`
		args = append(args, *t)
	}
	q += ` ORDER BY lower(h.valid) DESC LIMIT 200`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var vals []Value
	var ids []string
	for rows.Next() {
		var v Value
		var raw []byte
		var id string
		if err := rows.Scan(&v.Key, &raw, &v.Source, &v.Confidence, &v.Author, &v.ValidFrom, &v.ValidTo, &v.ChangesetID, &id); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(raw, &v.Value)
		vals = append(vals, v)
		ids = append(ids, id)
	}
	return vals, ids, rows.Err()
}
