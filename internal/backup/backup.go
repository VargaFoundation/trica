// Package backup is trica's logical backup and restore (ADR 0021).
//
// trica's state is PostgreSQL and nothing else, so a backup is one consistent
// pg_dump in custom format (a single snapshot, compressed). The archive
// carries the schema migration version in its own copy of the goose table:
// no side file can drift from it. Restore refuses an archive newer than the
// running binary, refuses a non-empty database unless told to replace it, and
// migrates an older archive up after loading it.
//
// pg_dump and pg_restore (PostgreSQL 17 client tools) are executed, not
// reimplemented: they are the tools an operator already trusts. The password
// never travels on a command line: the connection is passed in PG* variables.
package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
)

// Errors an operator can act on.
var (
	ErrNewerArchive = errors.New("archive is from a newer schema than this binary knows")
	ErrNotEmpty     = errors.New("target database is not empty")
	ErrInUse        = errors.New("target database has other connections open")
	ErrExists       = errors.New("backup file already exists")
	ErrNoVersion    = errors.New("archive has no schema version: not a trica backup")
)

// Tools names the PostgreSQL client programs. Empty means the PATH lookup of
// pg_dump and pg_restore; TRICA_PG_DUMP and TRICA_PG_RESTORE override it.
type Tools struct{ Dump, Restore string }

func (t Tools) dump() string    { return pick(t.Dump, "TRICA_PG_DUMP", "pg_dump") }
func (t Tools) restore() string { return pick(t.Restore, "TRICA_PG_RESTORE", "pg_restore") }

func pick(v, env, def string) string {
	if v != "" {
		return v
	}
	if e := os.Getenv(env); e != "" {
		return e
	}
	return def
}

// Info reports a backup or a restore.
type Info struct {
	File string
	// Version is the schema version recorded in the archive.
	Version int64
	// Migrated is the schema version after a restore ran migrations up.
	Migrated int64
	Bytes    int64
	Duration time.Duration
}

// connEnv turns a PostgreSQL URL into libpq environment variables and returns
// the database name.
func connEnv(dbURL string) (env []string, dbname string, err error) {
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, "", errors.New("backup: database URL is not a postgres:// URL")
	}
	env = os.Environ()
	set := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	dbname = strings.TrimPrefix(u.Path, "/")
	set("PGHOST", u.Hostname())
	set("PGPORT", u.Port())
	set("PGDATABASE", dbname)
	set("PGUSER", u.User.Username())
	pw, _ := u.User.Password()
	set("PGPASSWORD", pw)
	q := u.Query()
	set("PGSSLMODE", q.Get("sslmode"))
	set("PGSSLROOTCERT", q.Get("sslrootcert"))
	set("PGSSLCERT", q.Get("sslcert"))
	set("PGSSLKEY", q.Get("sslkey"))
	return env, dbname, nil
}

func run(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if errors.Is(err, exec.ErrNotFound) {
			msg = "not found in PATH (the trica ops image ships the PostgreSQL 17 client tools)"
		}
		return fmt.Errorf("%s: %w: %s", filepath.Base(cmd.Path), err, msg)
	}
	return nil
}

// Backup writes a consistent archive of the database to file. It never
// overwrites: an existing file is an error. The archive is written under a
// temporary name and renamed once pg_dump succeeded and the archive was read
// back, so a crash leaves no truncated backup under the final name.
func Backup(ctx context.Context, dbURL, file string, tools Tools, log *slog.Logger) (*Info, error) {
	start := time.Now()
	env, _, err := connEnv(dbURL)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(file); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, file)
	}
	tmp := file + ".partial"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the operator names the archive on purpose
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	// --no-owner and --no-privileges: the archive restores under whichever
	// role the operator restores with.
	cmd := exec.CommandContext(ctx, tools.dump(), "--format=custom", "--no-owner", "--no-privileges") //nolint:gosec // fixed PostgreSQL tools, names overridable by the operator environment only
	cmd.Env, cmd.Stdout = env, out
	err = run(cmd)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	var v int64
	if err == nil {
		// Read the archive back: a dump that cannot be read is not a backup.
		if v, err = ArchiveVersion(ctx, tmp, tools); err != nil {
			err = fmt.Errorf("backup: verifying the archive: %w", err)
		}
	}
	if err == nil {
		err = os.Rename(tmp, file)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	info := &Info{File: file, Version: v, Bytes: st.Size(), Duration: time.Since(start)}
	log.Info("backup written", "file", file, "schema_version", v, "bytes", info.Bytes, "seconds", info.Duration.Seconds())
	return info, nil
}

// ArchiveVersion returns the schema migration version recorded in a trica
// archive, read from the archive's copy of the goose version table.
func ArchiveVersion(ctx context.Context, file string, tools Tools) (int64, error) {
	in, err := os.Open(file) //nolint:gosec // the operator names the archive on purpose
	if err != nil {
		return 0, err
	}
	defer in.Close()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, tools.restore(), "--data-only", "--table=goose_db_version", "--file=-") //nolint:gosec // fixed PostgreSQL tools, names overridable by the operator environment only
	cmd.Stdin, cmd.Stdout = in, &out
	if err := run(cmd); err != nil {
		return 0, err
	}
	return parseVersion(&out)
}

// parseVersion reads the COPY block of goose_db_version: rows are in id order
// and the last row of a version says whether it is applied or rolled back.
func parseVersion(r io.Reader) (int64, error) {
	sc := bufio.NewScanner(r)
	state := map[int64]bool{}
	vCol, aCol := -1, -1
	in := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "COPY ") && strings.Contains(line, "goose_db_version"):
			in = true
			open, end := strings.Index(line, "("), strings.Index(line, ")")
			if open < 0 || end < open {
				return 0, ErrNoVersion
			}
			for i, c := range strings.Split(line[open+1:end], ",") {
				switch strings.TrimSpace(c) {
				case "version_id":
					vCol = i
				case "is_applied":
					aCol = i
				}
			}
		case in && line == `\.`:
			in = false
		case in:
			f := strings.Split(line, "\t")
			if vCol < 0 || aCol < 0 || len(f) <= vCol || len(f) <= aCol {
				return 0, ErrNoVersion
			}
			v, err := strconv.ParseInt(f[vCol], 10, 64)
			if err != nil {
				return 0, ErrNoVersion
			}
			state[v] = f[aCol] == "t"
		}
	}
	newest := int64(-1)
	for v, applied := range state {
		if applied && v > newest {
			newest = v
		}
	}
	if newest < 0 {
		return 0, ErrNoVersion
	}
	return newest, nil
}

// RestoreOptions tune Restore.
type RestoreOptions struct {
	// Replace drops what the target database holds before loading. Without
	// it a non-empty database is refused.
	Replace bool
	Tools   Tools
}

// Restore loads file into the database at dbURL, then migrates it up when the
// archive predates this binary. The server must be stopped: with Replace,
// other open connections to the database are refused.
func Restore(ctx context.Context, dbURL, file string, opts RestoreOptions, log *slog.Logger) (*Info, error) {
	start := time.Now()
	env, dbname, err := connEnv(dbURL)
	if err != nil {
		return nil, err
	}
	v, err := ArchiveVersion(ctx, file, opts.Tools)
	if err != nil {
		return nil, err
	}
	if known := store.KnownVersion(); v > known {
		return nil, fmt.Errorf("%w: archive is at version %d, this binary knows %d (use the release that wrote it)", ErrNewerArchive, v, known)
	}
	if err := checkTarget(ctx, dbURL, opts.Replace); err != nil {
		return nil, err
	}
	args := []string{"--single-transaction", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname=" + dbname}
	if opts.Replace {
		args = append(args, "--clean", "--if-exists")
	}
	in, err := os.Open(file) //nolint:gosec // the operator names the archive on purpose
	if err != nil {
		return nil, err
	}
	defer in.Close()
	cmd := exec.CommandContext(ctx, opts.Tools.restore(), args...) //nolint:gosec // fixed PostgreSQL tools, names overridable by the operator environment only
	cmd.Env, cmd.Stdin = env, in
	if err := run(cmd); err != nil {
		return nil, err
	}
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("restore: migrating up from version %d: %w", v, err)
	}
	after, err := s.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	info := &Info{File: file, Version: v, Migrated: after, Duration: time.Since(start)}
	log.Info("restore done", "file", file, "archive_version", v, "schema_version", after, "seconds", info.Duration.Seconds())
	return info, nil
}

// checkTarget refuses to load into a database that holds tables, unless
// replacing, and refuses to replace one that is in use.
func checkTarget(ctx context.Context, dbURL string, replace bool) error {
	c, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("restore: connect: %w", err)
	}
	defer c.Close(context.WithoutCancel(ctx))
	var tables, others int
	if err := c.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`).Scan(&tables); err != nil {
		return err
	}
	if tables > 0 && !replace {
		return fmt.Errorf("%w (%d tables): restore into an empty database, or pass --replace to drop them first", ErrNotEmpty, tables)
	}
	if replace {
		if err := c.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'`).Scan(&others); err != nil {
			return err
		}
		if others > 0 {
			return fmt.Errorf("%w (%d): stop the trica server first", ErrInUse, others)
		}
	}
	return nil
}
