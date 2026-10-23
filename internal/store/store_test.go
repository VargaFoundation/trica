package store_test

import (
	"context"
	"sync"
	"testing"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
)

func TestMigrationsAreReversible(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	if err := s.MigrateDown(ctx); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'objects'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("objects table still there after down (%d, %v)", n, err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// Rolling back the class enrolment tokens (ADR 0035) must not leave a class
// token behind: without its column it would read as a one-time token for its
// prefix, and a prefix of two segments ("agent:site-1") is a valid binding.
func TestClassTokensDoNotOutliveTheirMigration(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	insert := `INSERT INTO enrol_tokens (token_hash, binding, created_at, expires_at, class)
		VALUES (decode(repeat($1, 64), 'hex'), $2, now(), now() + interval '1 hour', $3)`
	if _, err := s.Pool.Exec(ctx, insert, "a", "agent:site-1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, insert, "b", "agent:host-01", false); err != nil {
		t.Fatal(err)
	}
	for { // back to before 00006, whatever came after it
		v, err := s.SchemaVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if v < 6 {
			break
		}
		if err := s.MigrateDownOne(ctx); err != nil {
			t.Fatalf("down from %d: %v", v, err)
		}
	}
	var bindings []string
	rows, err := s.Pool.Query(ctx, `SELECT binding FROM enrol_tokens ORDER BY binding`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, b)
	}
	if rows.Err() != nil || len(bindings) != 1 || bindings[0] != "agent:host-01" {
		t.Fatalf("tokens left after down: %v (%v), want the one-time token only", bindings, rows.Err())
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// Two values of one attribute can never be valid at the same instant: the
// exclusion constraint is what makes "the state at a date" single-valued.
func TestHistoryRangesCannotOverlap(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	var cs int64
	var obj string
	if err := s.Pool.QueryRow(ctx, `INSERT INTO changesets (source, kind) VALUES ('test', 'system') RETURNING id`).Scan(&cs); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `INSERT INTO objects (type) VALUES ('device') RETURNING id`).Scan(&obj); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO attr_history (object_id, key, value, source, confidence, valid, changeset_id)
		VALUES ($1, 'os_version', $2, 'test', 'observed', tstzrange($3::timestamptz, $4::timestamptz), $5)`
	if _, err := s.Pool.Exec(ctx, insert, obj, `"4.18"`, "2026-01-01", "2026-02-01", cs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, insert, obj, `"4.20"`, "2026-02-01", nil, cs); err != nil {
		t.Fatalf("adjacent range refused: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, insert, obj, `"4.19"`, "2026-01-15", "2026-01-20", cs); err == nil {
		t.Fatal("overlapping range accepted")
	}
}

// Replicas starting together all migrate on start; they must take turns
// instead of racing on goose's version table.
// Stores of different databases do not share the advisory lock, so their
// migrations really overlap: under -race this catches any process-wide state
// in the migration library (goose's package-level file system, #24).
func TestMigrationsOfSeparateDatabasesRunInParallel(t *testing.T) {
	ctx := context.Background()
	const n = 4
	stores := make([]*store.Store, n)
	for i := range stores {
		s, err := store.Open(ctx, testdb.NewURL(t))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		stores[i] = s
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- s.Migrate(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("parallel migrate: %v", err)
		}
	}
	for _, s := range stores {
		if v, err := s.SchemaVersion(ctx); err != nil || v != store.KnownVersion() {
			t.Fatalf("version %d, %v; want %d", v, err, store.KnownVersion())
		}
	}
}

func TestConcurrentMigrationsTakeTurns(t *testing.T) {
	url := testdb.NewURL(t)
	ctx := context.Background()
	const n = 6
	stores := make([]*store.Store, n)
	for i := range stores {
		s, err := store.Open(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		stores[i] = s
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Migrate(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	v, err := stores[0].SchemaVersion(ctx)
	if err != nil || v < 1 {
		t.Fatalf("version %d, %v", v, err)
	}
	if err := stores[0].MigrateDownOne(ctx); err != nil {
		t.Fatal(err)
	}
	if w, _ := stores[0].SchemaVersion(ctx); w >= v || w == 0 && v > 1 {
		t.Fatalf("down one: %d -> %d", v, w)
	}
}

// The theme tables belong to migration 00007: down drops them, up brings them
// back, and the constraints the code relies on are in place.
func TestThemeTablesGoWithTheirMigration(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	exists := func() (n int) {
		if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('ui_themes', 'ui_theme_active')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if exists() != 2 {
		t.Fatal("the theme tables are not there after the migrations")
	}
	// An active row names a stored archive or a built-in theme, never both or neither.
	for _, q := range []string{
		`INSERT INTO ui_theme_active (builtin, sha256) VALUES ('flat', decode(repeat('aa', 32), 'hex'))`,
		`INSERT INTO ui_theme_active (set_by) VALUES ('x')`,
		`INSERT INTO ui_theme_active (sha256) VALUES (decode(repeat('aa', 32), 'hex'))`, // no such archive
		`INSERT INTO ui_themes (sha256, name, title, version, archive, origin, uploaded_by) VALUES (decode('aa', 'hex'), 'n', '', '1', 'x', 'upload', 'u')`,
		`INSERT INTO ui_themes (sha256, name, title, version, archive, origin, uploaded_by) VALUES (decode(repeat('aa', 32), 'hex'), 'n', '', '1', 'x', 'elsewhere', 'u')`,
	} {
		if _, err := s.Pool.Exec(ctx, q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for ; v >= 7; v-- {
		if err := s.MigrateDownOne(ctx); err != nil {
			t.Fatalf("down from %d: %v", v, err)
		}
	}
	if n := exists(); n != 0 {
		t.Errorf("%d theme table(s) survive their migration", n)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if exists() != 2 {
		t.Error("the theme tables did not come back")
	}
}
