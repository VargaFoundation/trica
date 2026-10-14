// Package store is the PostgreSQL persistence layer: connection, migrations,
// and the read model (current state, provenance, history, time travel).
// Writes to the inventory go through package reconcile, never directly.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
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

// Migrate applies every pending migration.
func (s *Store) Migrate(ctx context.Context) error {
	return s.goose(ctx, func(db *sql.DB) error { return goose.UpContext(ctx, db, "migrations") })
}

// MigrateDown rolls every migration back. Used by the reversibility test.
func (s *Store) MigrateDown(ctx context.Context) error {
	return s.goose(ctx, func(db *sql.DB) error { return goose.DownToContext(ctx, db, "migrations", 0) })
}

func (s *Store) goose(_ context.Context, fn func(*sql.DB) error) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	db := stdlib.OpenDBFromPool(s.Pool)
	defer db.Close()
	return fn(db)
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

// CurrentRelations streams every current relation; used to build the graph cache.
func (s *Store) CurrentRelations(ctx context.Context) ([]Relation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+relationCols+` FROM relations WHERE upper_inf(valid)`)
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
