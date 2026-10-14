package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/testdb"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(context.Background(), args, nil, &out, &errOut, os.Getenv)
	return out.String() + errOut.String(), err
}

func TestCommands(t *testing.T) {
	if out, err := runCmd(t, "version"); err != nil || strings.TrimSpace(out) != version {
		t.Fatalf("version: %q %v", out, err)
	}
	if out, err := runCmd(t, "help"); err != nil || !strings.Contains(out, "migrate") {
		t.Fatalf("help: %q %v", out, err)
	}
	for _, args := range [][]string{nil, {"frobnicate"}, {"server", "--no-such-flag"}, {"server", "--config", "/nonexistent.yaml"}} {
		if _, err := runCmd(t, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestMigrate(t *testing.T) {
	t.Setenv(server.EnvDatabaseURL, testdb.NewURL(t))
	t.Setenv("TRICA_CONFIG", "")
	out, err := runCmd(t, "migrate", "status")
	if err != nil || !strings.Contains(out, "schema version 0") {
		t.Fatalf("status on an empty database: %q %v", out, err)
	}
	if out, err = runCmd(t, "migrate", "up"); err != nil {
		t.Fatalf("up: %q %v", out, err)
	}
	var up int
	if _, err := fmtSscan(out, &up); err != nil || up < 1 {
		t.Fatalf("up: %q", out)
	}
	out, err = runCmd(t, "migrate", "down")
	var down int
	if _, scanErr := fmtSscan(out, &down); err != nil || scanErr != nil || down >= up {
		t.Fatalf("down rolled back nothing: %q %v", out, err)
	}
	if down == 0 && up > 1 {
		t.Fatalf("down rolled back every migration, want one: %d -> %d", up, down)
	}
	for _, args := range [][]string{{"migrate"}, {"migrate", "sideways"}} {
		if _, err := runCmd(t, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	t.Setenv(server.EnvDatabaseURL, "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if _, err := runCmd(t, "migrate", "status"); err == nil {
		t.Fatal("unreachable database accepted")
	}
}

// syncBuffer is a bytes.Buffer safe for the server's logger and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestServerCommandStopsOnCancel(t *testing.T) {
	t.Setenv(server.EnvDatabaseURL, testdb.NewURL(t))
	t.Setenv("TRICA_CONFIG", "")
	t.Setenv("TRICA_HTTP_ADDR", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var out syncBuffer
	go func() { done <- run(ctx, []string{"server"}, nil, &out, &out, os.Getenv) }()
	deadline := time.Now().Add(90 * time.Second)
	for !strings.Contains(out.String(), `"msg":"trica server started"`) {
		select {
		case err := <-done:
			t.Fatalf("server exited before starting: %v\n%s", err, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never started:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server: %v\n%s", err, out.String())
		}
	case <-time.After(90 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestNewLogger(t *testing.T) {
	var b bytes.Buffer
	newLogger(&b, "warn").Info("hidden")
	newLogger(&b, "warn").Warn("shown")
	if s := b.String(); strings.Contains(s, "hidden") || !strings.Contains(s, `"msg":"shown"`) {
		t.Fatalf("log level not applied: %s", s)
	}
}

// fmtSscan reads the version out of "schema version N".
func fmtSscan(out string, v *int) (int, error) {
	return fmt.Sscanf(strings.TrimSpace(out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:]), "schema version %d", v)
}

func TestBackupRestoreCommands(t *testing.T) {
	if os.Getenv("TRICA_PG_DUMP") == "" || os.Getenv("TRICA_PG_RESTORE") == "" {
		for _, bin := range []string{"pg_dump", "pg_restore"} {
			if _, err := exec.LookPath(bin); err != nil {
				if os.Getenv("TRICA_REQUIRE_DB") == "1" {
					t.Fatalf("TRICA_REQUIRE_DB=1 but %s is not available", bin)
				}
				t.Skipf("%s not available", bin)
			}
		}
	}
	t.Setenv(server.EnvDatabaseURL, testdb.NewURL(t))
	t.Setenv("TRICA_CONFIG", "")
	if _, err := runCmd(t, "migrate", "up"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"backup"}, {"backup", "--file", dir + "/a", "--dir", dir}, {"restore"}} {
		if _, err := runCmd(t, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	out, err := runCmd(t, "backup", "--dir", dir)
	var v int
	_, after, found := strings.Cut(out, "schema version ")
	if err != nil || !found {
		t.Fatalf("backup: %q %v", out, err)
	}
	if _, err := fmt.Sscanf(after, "%d", &v); err != nil || v < 1 {
		t.Fatalf("backup: no schema version in %q", out)
	}
	files, _ := filepath.Glob(dir + "/trica-*.dump")
	if len(files) != 1 {
		t.Fatalf("timestamped archive not written: %v", files)
	}
	t.Setenv(server.EnvDatabaseURL, testdb.NewURL(t))
	if out, err = runCmd(t, "restore", "--file", files[0]); err != nil || !strings.Contains(out, fmt.Sprintf("now %d", v)) {
		t.Fatalf("restore: %q %v", out, err)
	}
	if _, err = runCmd(t, "restore", "--file", files[0]); err == nil {
		t.Fatal("restore into a populated database accepted without --replace")
	}
}
