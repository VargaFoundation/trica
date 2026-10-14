package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/agent/update"
)

// stage puts version on probation in a fresh state directory, as the updater does.
func stage(t *testing.T, version string) (dir string, s *update.Store) {
	t.Helper()
	dir = t.TempDir()
	tmp := filepath.Join(dir, ".dl")
	if err := os.WriteFile(tmp, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = update.NewStore(dir)
	if err := s.Install(version, tmp); err != nil {
		t.Fatal(err)
	}
	return dir, s
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func stubExec(t *testing.T, fn func(path string, args, env []string) error) {
	t.Helper()
	old := execFn
	execFn = fn
	t.Cleanup(func() { execFn = old })
}

func TestStagedVersionReplacesThePackagedBinary(t *testing.T) {
	dir, s := stage(t, "2.0.0")
	var gotPath string
	var gotArgs []string
	stubExec(t, func(path string, args, _ []string) error { gotPath, gotArgs = path, args; return nil })
	runArgs(t, "--root", fixture, "--once", "--state-dir", dir)
	if gotPath != s.BinaryPath("2.0.0") || len(gotArgs) < 2 || gotArgs[0] != gotPath || gotArgs[1] != "--root" {
		t.Fatalf("exec %q %v", gotPath, gotArgs)
	}
}

func TestBootedBinaryDoesNotBootAgain(t *testing.T) {
	dir, _ := stage(t, "2.0.0")
	t.Setenv(update.BootedEnv, "1")
	stubExec(t, func(string, []string, []string) error {
		t.Error("exec called by a binary that was already booted")
		return nil
	})
	if code, _, errs := runArgs(t, "--root", fixture, "--once", "--state-dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

func TestNoStateDirMeansNoUpdateMachinery(t *testing.T) {
	stubExec(t, func(string, []string, []string) error { t.Error("exec called without --state-dir"); return nil })
	if code, _, errs := runArgs(t, "--root", fixture, "--once"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

func TestVersionOnProbationIsConfirmedByItsFirstBatch(t *testing.T) {
	dir, s := stage(t, "2.0.0")
	setVersion(t, "2.0.0")
	t.Setenv(update.BootedEnv, "1")
	if code, _, errs := runArgs(t, "--root", fixture, "--once", "--state-dir", dir, "--output", filepath.Join(dir, "batch.json")); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	st, _ := s.Load()
	if st.Pending != nil || st.Current != "2.0.0" {
		t.Fatalf("state %+v: the first healthy batch did not confirm the version", st)
	}
}

// A batch that cannot be delivered is not a healthy collection.
func TestVersionOnProbationIsNotConfirmedWhenTheBatchIsNotEmitted(t *testing.T) {
	dir, s := stage(t, "2.0.0")
	setVersion(t, "2.0.0")
	t.Setenv(update.BootedEnv, "1")
	code, _, _ := runArgs(t, "--root", fixture, "--once", "--state-dir", dir, "--output", filepath.Join(dir, "missing", "batch.json"))
	if code == 0 {
		t.Fatal("exit 0 although the batch was not written")
	}
	if st, _ := s.Load(); st.Pending == nil {
		t.Fatal("confirmed without a delivered batch")
	}
}

// Past the health timeout without a confirmed batch the agent stops with a
// failure, which is what makes the service manager restart it into the rollback.
func TestVersionOnProbationStopsWhenItStaysUnhealthy(t *testing.T) {
	dir, s := stage(t, "2.0.0")
	setVersion(t, "2.0.0")
	t.Setenv(update.BootedEnv, "1")
	type result struct {
		code int
		errs string
	}
	done := make(chan result, 1)
	go func() {
		code, _, errs := runArgs(t, "--root", fixture, "--state-dir", dir, "--health-timeout", "300ms", "--interval", "1h",
			"--output", filepath.Join(dir, "missing", "batch.json"))
		done <- result{code, errs}
	}()
	select {
	case r := <-done:
		if r.code != 1 || !strings.Contains(r.errs, "health timeout") {
			t.Fatalf("exit %d: %s", r.code, r.errs)
		}
	case <-time.After(30 * time.Second):
		// Without the watchdog the agent would wait for its next run, an hour away.
		t.Fatal("did not stop at the health timeout")
	}
	if st, _ := s.Load(); st.Pending == nil {
		t.Fatal("pending cleared without a healthy batch")
	}
}

func TestPackagedBinaryIsNotOnProbation(t *testing.T) {
	// version is "dev" and nothing is pending for it: no watchdog, no state change.
	dir := t.TempDir()
	if code, _, errs := runArgs(t, "--root", fixture, "--once", "--state-dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err == nil {
		t.Fatal("a state file appeared for a binary that is not updating")
	}
}
