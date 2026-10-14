package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// fixture is a recorded host tree of the agent package: the tests never look at the machine they run on.
const fixture = "../../internal/agent/testdata/debian12/root"

func runArgs(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestOnceWritesOneJSONLineToStdout(t *testing.T) {
	code, out, errs := runArgs(t, "--root", fixture, "--once")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("%d lines on stdout, want one batch", len(lines))
	}
	var b sdk.Batch
	if err := json.Unmarshal([]byte(lines[0]), &b); err != nil {
		t.Fatal(err)
	}
	if b.Source != "agent:host-01" || b.Scope != sdk.ScopeFull || len(b.Objects) == 0 || b.Health == nil || b.Health.Collector != "agent" {
		t.Fatalf("batch %s/%s, %d objects, health %+v", b.Source, b.Scope, len(b.Objects), b.Health)
	}
	// Logs go to stderr as JSON, never to stdout.
	var log map[string]any
	first, _, _ := strings.Cut(errs, "\n")
	if err := json.Unmarshal([]byte(first), &log); err != nil || log["source"] != "agent:host-01" || log["collector"] != "agent" {
		t.Fatalf("stderr %q", errs)
	}
}

func TestOnceWritesTheLatestBatchToAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "batch.json")
	for i := 0; i < 2; i++ { // the second run replaces the first
		if code, out, errs := runArgs(t, "--root", fixture, "--once", "--output", path); code != 0 || out != "" {
			t.Fatalf("exit %d, stdout %q, stderr %s", code, out, errs)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v: an inventory is not for everyone", st.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var b sdk.Batch
	if err := json.Unmarshal(raw, &b); err != nil || b.Source != "agent:host-01" {
		t.Fatalf("%v %s", err, raw)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("%d files left in the output directory, want the batch alone", len(ents))
	}
}

func TestOutputInAMissingDirectoryFails(t *testing.T) {
	code, _, _ := runArgs(t, "--root", fixture, "--once", "--output", filepath.Join(t.TempDir(), "no", "batch.json"))
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
}

func TestIntervalBelowTheMinimumIsRefused(t *testing.T) {
	code, _, errs := runArgs(t, "--root", fixture, "--interval", "1s")
	if code != 2 || !strings.Contains(errs, "below the minimum") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	// --once has no interval to protect.
	if code, _, errs := runArgs(t, "--root", fixture, "--interval", "1s", "--once"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

func TestBadInvocations(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"--interval", "soon"}, {"extra"}, {"--once", "extra"}} {
		if code, _, _ := runArgs(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, _, _ := runArgs(t, "--root", filepath.Join(t.TempDir(), "missing"), "--once"); code != 1 {
		t.Errorf("a missing root: exit %d, want 1", code)
	}
}

func TestHelpDescribesTheTransport(t *testing.T) {
	code, out, errs := runArgs(t, "--help")
	if code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	for _, want := range []string{"mTLS", "-server", "-ca-file", "-enrol-token-file", "-state-dir", "-root", "-interval", "-once", "-output", "read-only"} {
		if !strings.Contains(errs, want) {
			t.Errorf("--help does not mention %q:\n%s", want, errs)
		}
	}
}

func TestRunsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	var out bytes.Buffer
	var code int
	go func() {
		code = run(ctx, []string{"--root", fixture, "--interval", "1m"}, &out, &bytes.Buffer{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop when cancelled")
	}
	if code != 0 || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("exit %d after %q", code, out.String())
	}
}
