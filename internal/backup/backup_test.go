package backup_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/backup"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// restoreBudget is the operator-facing promise: a restore of a realistic
// inventory takes under 15 minutes.
const restoreBudget = 15 * time.Minute

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// needTools skips (fails under TRICA_REQUIRE_DB=1) without the PostgreSQL
// client tools, so a green CI run has exercised them.
func needTools(t *testing.T) {
	t.Helper()
	for _, p := range []struct{ env, bin string }{{"TRICA_PG_DUMP", "pg_dump"}, {"TRICA_PG_RESTORE", "pg_restore"}} {
		name := os.Getenv(p.env)
		if name == "" {
			name = p.bin
		}
		if _, err := exec.LookPath(name); err != nil {
			if os.Getenv("TRICA_REQUIRE_DB") == "1" {
				t.Fatalf("TRICA_REQUIRE_DB=1 but %s is not available", name)
			}
			t.Skipf("%s not available", name)
		}
	}
}

// admin connects to the server's maintenance database.
func admin(t *testing.T) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), os.Getenv("TRICA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func dbName(t *testing.T, dbURL string) string {
	t.Helper()
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(u.Path, "/")
}

// dropAndCreate is the disaster: the database is gone, and an empty one is
// all the operator has to restore into.
func dropAndCreate(t *testing.T, dbURL string) {
	t.Helper()
	a, name := admin(t), dbName(t, dbURL)
	ctx := context.Background()
	if _, err := a.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
}

const (
	racks          = 50
	devicesPerRack = 40 // 2,000 devices
)

// loadInventory builds the synthetic inventory through the engine, the only
// writer: one site, 50 racks, 2,000 devices, then a second observation that
// changes every device's OS version, so the history holds closed ranges.
func loadInventory(t *testing.T, s *store.Store) (t0 time.Time, sample []string) {
	t.Helper()
	reg := schema.Builtin()
	clock := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	e := &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }, Now: func() time.Time { return clock }}
	pass := func(osVersion string) []*reconcile.Result {
		var out []*reconcile.Result
		for r := 0; r < racks; r++ {
			b := sdk.NewBatch(fmt.Sprintf("decl:rack%02d", r), sdk.ScopeFull)
			b.ObservedAt = clock
			b.Object("site", "site").Identify("name", "par-1")
			b.Object("rack", "rack").Identify("name", fmt.Sprintf("r%02d", r)).In("site").Set("u_height", 42)
			for d := 0; d < devicesPerRack; d++ {
				ref := fmt.Sprintf("d%02d", d)
				b.Object(ref, "device").
					Identify("serial", fmt.Sprintf("SN%02d%02d", r, d)).
					Identify("name", fmt.Sprintf("srv-r%02d-%02d", r, d)).
					In("rack").
					Set("role", "server").Set("os_version", osVersion)
			}
			res, err := e.Apply(context.Background(), b, "", "")
			if err != nil {
				t.Fatalf("load rack %d: %v", r, err)
			}
			out = append(out, res)
		}
		return out
	}
	res := pass("1.0")
	for r := 0; r < racks; r += 10 {
		sample = append(sample, res[r].Objects["d00"], res[r].Objects["d39"])
	}
	t0 = clock.Add(time.Minute)
	clock = clock.Add(time.Hour)
	pass("2.0")
	return t0, sample
}

// fingerprint hashes every row of every table, so "identical" does not rest
// on a hand-picked list of queries.
func fingerprint(t *testing.T, dbURL string) map[string]string {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	rows, err := c.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tb := range tables {
		var n int
		var h *string
		q := fmt.Sprintf(`SELECT count(*), md5(string_agg(x::text, '|' ORDER BY x::text)) FROM %q x`, tb)
		if err := c.QueryRow(ctx, q).Scan(&n, &h); err != nil {
			t.Fatal(err)
		}
		hash := ""
		if h != nil {
			hash = *h
		}
		out[tb] = fmt.Sprintf("%d rows md5 %s", n, hash)
	}
	return out
}

func TestBackupDropRestore(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	dbURL := testdb.NewURL(t)
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t0, sample := loadInventory(t, s)
	before := fingerprint(t, dbURL)
	if before["objects"] == "0 rows md5 " || !strings.HasPrefix(before["objects"], "2051 rows") {
		t.Fatalf("dataset: objects = %q, want 2051 rows (1 site, 50 racks, 2,000 devices)", before["objects"])
	}
	asOf := map[string]map[string]any{}
	for _, id := range sample {
		if asOf[id], err = s.AsOf(ctx, id, t0); err != nil {
			t.Fatal(err)
		}
		if asOf[id]["os_version"] != "1.0" {
			t.Fatalf("sample %s at t0: %v", id, asOf[id])
		}
	}
	ver, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	file := filepath.Join(t.TempDir(), "trica.dump")
	info, err := backup.Backup(ctx, dbURL, file, backup.Tools{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != ver || info.Bytes == 0 {
		t.Fatalf("backup info %+v, database at version %d", info, ver)
	}
	t.Logf("backup: %d bytes in %s", info.Bytes, info.Duration.Round(time.Millisecond))

	dropAndCreate(t, dbURL)
	res, err := backup.Restore(ctx, dbURL, file, backup.RestoreOptions{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("restore: %d objects, %d tables in %s (budget %s)", racks*devicesPerRack+racks+1, len(before), res.Duration.Round(time.Millisecond), restoreBudget)
	if res.Duration > restoreBudget {
		t.Fatalf("restore took %s, budget %s", res.Duration, restoreBudget)
	}

	after := fingerprint(t, dbURL)
	for tb, want := range before {
		if after[tb] != want {
			t.Errorf("table %s differs after restore: %s, was %s", tb, after[tb], want)
		}
	}
	if len(after) != len(before) {
		t.Errorf("tables after restore %d, before %d", len(after), len(before))
	}
	s2, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	for id, want := range asOf {
		got, err := s2.AsOf(ctx, id, t0)
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("history of %s at t0 after restore: %v %v, want %v", id, got, err, want)
		}
	}
}

// archiveAt backs up a database migrated down to version 0 (the goose table
// only) or, with extra, one recording a newer version.
func archiveWithVersion(t *testing.T, mutate func(ctx context.Context, s *store.Store)) (dbURL, file string) {
	t.Helper()
	ctx := context.Background()
	dbURL = testdb.NewURL(t)
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	mutate(ctx, s)
	s.Close()
	file = filepath.Join(t.TempDir(), "a.dump")
	if _, err := backup.Backup(ctx, dbURL, file, backup.Tools{}, quiet); err != nil {
		t.Fatal(err)
	}
	return dbURL, file
}

func TestRestoreRefusesNewerArchive(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	_, file := archiveWithVersion(t, func(ctx context.Context, s *store.Store) {
		// A later release's migration, as its goose table would record it.
		next := store.KnownVersion() + 1
		if _, err := s.Pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, next); err != nil {
			t.Fatal(err)
		}
	})
	target := testdb.NewURL(t)
	_, err := backup.Restore(ctx, target, file, backup.RestoreOptions{}, quiet)
	if !errors.Is(err, backup.ErrNewerArchive) {
		t.Fatalf("restore of a newer archive: %v, want ErrNewerArchive", err)
	}
	if fp := fingerprint(t, target); len(fp) != 0 {
		t.Fatalf("the refused restore touched the target: %v", fp)
	}
}

func TestRestoreMigratesOlderArchiveUp(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	_, file := archiveWithVersion(t, func(ctx context.Context, s *store.Store) {
		if err := s.MigrateDown(ctx); err != nil {
			t.Fatal(err)
		}
	})
	v, err := backup.ArchiveVersion(ctx, file, backup.Tools{})
	if err != nil || v != 0 {
		t.Fatalf("archive version %d %v, want 0", v, err)
	}
	target := testdb.NewURL(t)
	info, err := backup.Restore(ctx, target, file, backup.RestoreOptions{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != 0 || info.Migrated != store.KnownVersion() {
		t.Fatalf("restored version %d, migrated to %d, want 0 -> %d", info.Version, info.Migrated, store.KnownVersion())
	}
	if _, ok := fingerprint(t, target)["objects"]; !ok {
		t.Fatal("migrations did not run after the restore: no objects table")
	}
}

func TestRestoreTargetGuards(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	srcURL, file := archiveWithVersion(t, func(context.Context, *store.Store) {})

	// A database that holds tables is refused, and left as it was.
	before := fingerprint(t, srcURL)
	_, err := backup.Restore(ctx, srcURL, file, backup.RestoreOptions{}, quiet)
	if !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("restore into a populated database: %v, want ErrNotEmpty", err)
	}
	if after := fingerprint(t, srcURL); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatal("the refused restore changed the database")
	}

	// Replacing a database someone is connected to is refused.
	c, err := pgx.Connect(ctx, srcURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backup.Restore(ctx, srcURL, file, backup.RestoreOptions{Replace: true}, quiet)
	_ = c.Close(ctx)
	if !errors.Is(err, backup.ErrInUse) {
		t.Fatalf("replace under an open connection: %v, want ErrInUse", err)
	}

	// With the server stopped, replace works and yields the archive's state.
	if _, err := backup.Restore(ctx, srcURL, file, backup.RestoreOptions{Replace: true}, quiet); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if after := fingerprint(t, srcURL); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("replace result differs: %v vs %v", after, before)
	}
}

func TestBackupFailuresLeaveNothing(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "x.dump")

	// A failing dump (nonexistent database) leaves neither file nor partial.
	bad := testdb.NewURL(t)
	bad = strings.Replace(bad, dbName(t, bad), "no_such_database", 1)
	if _, err := backup.Backup(ctx, bad, file, backup.Tools{}, quiet); err == nil {
		t.Fatal("backup of a missing database succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a failed backup left %d file(s)", len(entries))
	}

	// An existing file is never overwritten.
	if err := os.WriteFile(file, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Backup(ctx, testdb.NewURL(t), file, backup.Tools{}, quiet); !errors.Is(err, backup.ErrExists) {
		t.Fatalf("backup over an existing file: %v, want ErrExists", err)
	}
	if b, _ := os.ReadFile(file); string(b) != "precious" {
		t.Fatal("the existing file was modified")
	}

	// A file that is not an archive is refused before anything is loaded.
	if _, err := backup.Restore(ctx, testdb.NewURL(t), file, backup.RestoreOptions{}, quiet); err == nil {
		t.Fatal("restore of a non-archive succeeded")
	}
	if _, err := backup.Backup(ctx, "mysql://x", filepath.Join(dir, "y"), backup.Tools{}, quiet); err == nil {
		t.Fatal("a non-postgres URL was accepted")
	}
}

func TestMissingToolsExplains(t *testing.T) {
	ctx := context.Background()
	_, err := backup.ArchiveVersion(ctx, os.DevNull, backup.Tools{Restore: "/nonexistent/pg_restore"})
	if err == nil || !strings.Contains(err.Error(), "pg_restore") {
		t.Fatalf("missing tool: %v", err)
	}
}

// fakeDump writes a pg_dump stand-in that records its arguments and
// PGPASSWORD, then emits bytes that are not an archive and exits 0.
func fakeDump(t *testing.T) (tool, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	tool = filepath.Join(dir, "pg_dump")
	script := "#!/bin/sh\necho \"args: $*\" > " + record + "\necho \"password: $PGPASSWORD\" >> " + record + "\necho not-an-archive\n"
	if err := os.WriteFile(tool, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return tool, record
}

func TestBackupVerifiesTheArchiveAndKeepsThePasswordOffTheCommandLine(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "x.dump")
	tool, record := fakeDump(t)
	_, err := backup.Backup(context.Background(), "postgres://trica:s3cret-pw@127.0.0.1:1/trica?sslmode=disable", file, backup.Tools{Dump: tool}, quiet)
	if err == nil {
		t.Fatal("a dump that is not an archive was accepted as a backup")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("an unreadable dump left %d file(s) under the backup name", len(entries))
	}
	rec, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	args, pw, _ := strings.Cut(string(rec), "password: ")
	if strings.Contains(args, "s3cret-pw") || strings.TrimSpace(pw) != "s3cret-pw" {
		t.Fatalf("the password must travel in PGPASSWORD only:\n%s", rec)
	}
}
