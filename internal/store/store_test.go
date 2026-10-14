package store_test

import (
	"context"
	"testing"

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
