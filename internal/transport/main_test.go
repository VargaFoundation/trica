package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VargaFoundation/trica/internal/store"
)

// sharedStore is one migrated database for the whole package: creating a
// database per test (internal/testdb) costs seconds each on a busy server,
// and the transport tables are emptied between tests instead.
var sharedStore *store.Store

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "transport tests:", err)
		code = 1
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	base := os.Getenv("TRICA_TEST_DATABASE_URL")
	if base == "" {
		if os.Getenv("TRICA_REQUIRE_DB") == "1" {
			return 1, fmt.Errorf("TRICA_REQUIRE_DB=1 but TRICA_TEST_DATABASE_URL is not set")
		}
		return m.Run(), nil // tests that need the database skip themselves
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return 1, err
	}
	defer admin.Close(ctx)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "trica_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return 1, err
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	}()
	u, err := url.Parse(base)
	if err != nil {
		return 1, err
	}
	u.Path = "/" + name
	sharedStore, err = store.Open(ctx, u.String())
	if err != nil {
		return 1, err
	}
	defer sharedStore.Close()
	if err := sharedStore.Migrate(ctx); err != nil {
		return 1, err
	}
	return m.Run(), nil
}

// testPool returns the shared pool with the transport tables emptied.
func testPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if sharedStore == nil {
		t.Skip("TRICA_TEST_DATABASE_URL not set")
	}
	if _, err := sharedStore.Pool.Exec(context.Background(), `TRUNCATE enrol_tokens, issued_certs, revoked_certs`); err != nil {
		t.Fatal(err)
	}
	return sharedStore.Pool
}
