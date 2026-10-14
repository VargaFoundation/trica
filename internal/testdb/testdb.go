// Package testdb gives each integration test its own freshly migrated
// PostgreSQL database.
//
// Tests need TRICA_TEST_DATABASE_URL pointing at a server where the user may
// create databases (`make db` starts one in Docker). Without it the tests are
// skipped, unless TRICA_REQUIRE_DB=1 (set in CI), in which case they fail:
// a green CI run must have exercised the database.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
)

// New returns a store on a new, migrated database dropped at test end.
func New(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), NewURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("testdb: migrate: %v", err)
	}
	return s
}

// NewURL creates a new, empty database dropped at test end and returns its
// URL, for tests that open their own connections (the server, migrations).
func NewURL(t testing.TB) string {
	t.Helper()
	base := os.Getenv("TRICA_TEST_DATABASE_URL")
	if base == "" {
		if os.Getenv("TRICA_REQUIRE_DB") == "1" {
			t.Fatal("TRICA_REQUIRE_DB=1 but TRICA_TEST_DATABASE_URL is not set")
		}
		t.Skip("TRICA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	defer admin.Close(ctx)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "trica_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("testdb: create: %v", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	// Registered first, so it runs after the cleanups that close connections.
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return u.String()
}
