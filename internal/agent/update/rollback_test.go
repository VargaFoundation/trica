package update

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The binaries below are real programs built by the test (testdata/fakeagent):
// version 1.0.0 is the "packaged" agent, 2.0.0 the release, and what 2.0.0 does
// is chosen per start by TRICA_TEST_MODE. The test plays the service manager:
// it calls Boot, runs whatever Boot says, kills what hangs, and starts again.
var (
	buildOnce      sync.Once
	buildDir       string
	buildErr       error
	v1Bin, v2Bin   string
	testdataBinary = "./testdata/fakeagent"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

func buildAgents(t *testing.T) {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "trica-update-test-*")
		if buildErr != nil {
			return
		}
		for ver, out := range map[string]*string{"1.0.0": &v1Bin, "2.0.0": &v2Bin} {
			*out = filepath.Join(buildDir, "fakeagent-"+ver)
			cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+ver, "-o", *out, testdataBinary)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if b, err := cmd.CombinedOutput(); err != nil {
				buildErr = &buildError{string(b)}
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
}

type buildError struct{ out string }

func (e *buildError) Error() string { return "building the test agents: " + e.out }

// service is a minimal service manager over one store.
type service struct {
	t       *testing.T
	store   *Store
	dir     string
	self    string // the packaged binary
	now     time.Time
	timeout time.Duration
	up      *rig
}

// start is one service start: Boot, then run what it chose for at most
// runFor. It returns what the process printed and what Boot decided.
func (s *service) start(mode string, runFor time.Duration) (string, Decision) {
	s.t.Helper()
	d, err := s.store.Boot(s.now, s.timeout)
	if err != nil {
		s.t.Fatal(err)
	}
	bin := s.self
	if d.Exec != "" {
		bin = d.Exec
	}
	if bin == s.self {
		mode = "good" // the old binary is the known good one
	}
	ctx, cancel := context.WithTimeout(context.Background(), runFor)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, s.dir)
	cmd.Env = []string{"TRICA_TEST_MODE=" + mode}
	out, _ := cmd.Output() // a crash or a kill is the point
	return strings.TrimSpace(string(out)), d
}

// newService installs the release 2.0.0, signed and verified, as pending, on a
// store whose packaged binary is 1.0.0.
func newService(t *testing.T) *service {
	t.Helper()
	buildAgents(t)
	s := newECDSASigner(t)
	v2, err := os.ReadFile(v2Bin)
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t, s, "1.0.0", nil)
	r.src.Version, r.src.Percent, r.src.Files = "2.0.0", 100, release(s, "2.0.0", v2)
	if res, err := r.up.CheckOnce(context.Background()); err != nil || res.Outcome != Staged {
		t.Fatalf("staging 2.0.0: %+v, %v", res, err)
	}
	svc := &service{t: t, store: r.store, dir: r.dir, self: v1Bin, now: time.Unix(1_800_000_000, 0), timeout: time.Minute}
	svc.up = r
	return svc
}

func (s *service) state() State {
	s.t.Helper()
	st, err := s.store.Load()
	if err != nil {
		s.t.Fatal(err)
	}
	return st
}

func TestHealthyNewBinaryIsConfirmedAndKept(t *testing.T) {
	svc := newService(t)
	out, d := svc.start("good", 20*time.Second)
	if out != "ok 2.0.0" || d.Exec == "" || d.RolledBack {
		t.Fatalf("first start: %q %+v", out, d)
	}
	st := svc.state()
	if st.Pending != nil || st.Current != "2.0.0" || st.Previous != "" || len(st.Bad) != 0 {
		t.Fatalf("state after a healthy start = %+v", st)
	}
	if out, _ := svc.start("good", 20*time.Second); out != "ok 2.0.0" {
		t.Errorf("second start ran %q", out)
	}
}

func TestNewBinaryThatCrashesIsRolledBack(t *testing.T) {
	svc := newService(t)
	var got string
	var rolled Decision
	for i := range 2 * MaxStarts {
		svc.now = svc.now.Add(2 * time.Second) // a crash loop is fast: the deadline is not what ends it
		out, d := svc.start("crash", 20*time.Second)
		if d.RolledBack {
			rolled = d
		}
		if out != "" {
			got = out
			if i < MaxStarts {
				t.Fatalf("start %d printed %q", i+1, out)
			}
			break
		}
	}
	if !rolled.RolledBack || rolled.Reason == "" {
		t.Fatalf("never rolled back: %+v", rolled)
	}
	// The old binary runs, healthy, and nothing is pending.
	if got != "ok 1.0.0" {
		t.Fatalf("after rollback the agent printed %q, want ok 1.0.0", got)
	}
	svc.assertRolledBack(t)
}

func TestNewBinaryThatHangsIsRolledBackAtTheDeadline(t *testing.T) {
	svc := newService(t)
	// Start 1: it hangs, the service manager kills it after the health timeout.
	if out, d := svc.start("hang", 300*time.Millisecond); out != "" || d.RolledBack {
		t.Fatalf("start 1: %q %+v", out, d)
	}
	// Still inside the deadline: it gets another chance.
	svc.now = svc.now.Add(svc.timeout - time.Second)
	if _, d := svc.start("hang", 300*time.Millisecond); d.RolledBack {
		t.Fatalf("rolled back before the deadline: %+v", d)
	}
	// Past the deadline: back to the old binary.
	svc.now = svc.now.Add(2 * time.Second)
	out, d := svc.start("hang", 20*time.Second)
	if !d.RolledBack || out != "ok 1.0.0" {
		t.Fatalf("after the deadline: %q %+v", out, d)
	}
	svc.assertRolledBack(t)
}

func (s *service) assertRolledBack(t *testing.T) {
	t.Helper()
	st := s.state()
	if st.Pending != nil || st.Current != "" || st.Previous != "" || !st.IsBad("2.0.0") {
		t.Fatalf("state after rollback = %+v", st)
	}
	// The next boot stays on the packaged binary.
	if d, err := s.store.Boot(s.now, s.timeout); err != nil || d.Exec != "" || d.RolledBack {
		t.Fatalf("boot after rollback: %+v, %v", d, err)
	}
	// And the same release is not staged again, whatever the server says.
	r := s.up
	r.src.Version = "2.0.0"
	res, err := r.up.CheckOnce(context.Background())
	if err != nil || res.Outcome != KnownBad {
		t.Fatalf("re-offer of the failed release: %+v, %v", res, err)
	}
}

func TestRollbackGoesBackToThePreviousManagedVersion(t *testing.T) {
	svc := newService(t)
	if out, _ := svc.start("good", 20*time.Second); out != "ok 2.0.0" {
		t.Fatal(out)
	}
	// 2.0.0 is confirmed; stage 2.1.0 (a copy of the same bytes: only the state matters here).
	b, _ := os.ReadFile(v2Bin)
	tmp := filepath.Join(svc.dir, ".dl")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.Install("2.1.0", tmp); err != nil {
		t.Fatal(err)
	}
	for range MaxStarts + 1 {
		svc.start("crash", 20*time.Second)
	}
	st := svc.state()
	if st.Current != "2.0.0" || st.Pending != nil || !st.IsBad("2.1.0") {
		t.Fatalf("state = %+v", st)
	}
	if out, _ := svc.start("good", 20*time.Second); out != "ok 2.0.0" {
		t.Errorf("ran %q after rolling back to 2.0.0", out)
	}
}

func TestBootRollsBackWhenTheStagedBinaryIsGone(t *testing.T) {
	svc := newService(t)
	if err := os.Remove(svc.store.BinaryPath("2.0.0")); err != nil {
		t.Fatal(err)
	}
	d, err := svc.store.Boot(svc.now, svc.timeout)
	if err != nil || !d.RolledBack || d.Exec != "" {
		t.Fatalf("= %+v, %v", d, err)
	}
}

func TestConfirmByAnotherVersionChangesNothing(t *testing.T) {
	svc := newService(t)
	if ok, err := svc.store.Confirm("1.0.0"); err != nil || ok {
		t.Fatalf("a different version confirmed the pending one: %v, %v", ok, err)
	}
	if svc.state().Pending == nil {
		t.Fatal("pending was cleared")
	}
}

func TestConfirmPrunesOlderStagedVersions(t *testing.T) {
	svc := newService(t)
	stale := filepath.Join(svc.dir, "versions", "0.5.0")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := svc.store.Confirm("2.0.0"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale version kept: %v", err)
	}
	if _, err := os.Stat(svc.store.BinaryPath("2.0.0")); err != nil {
		t.Errorf("current version removed: %v", err)
	}
}

// A state file can only name versions, never paths: tampering with it cannot
// make Boot execute something that was not staged by this package.
func TestTamperedStateCannotNameAPath(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"../../bin/sh", "/bin/sh", "1.0", "1.0.0/../../x"} {
		b, _ := json.Marshal(State{Current: bad})
		if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(dir).Boot(time.Now(), time.Minute); err == nil {
			t.Errorf("state naming %q was accepted", bad)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir).Load(); err == nil {
		t.Error("a corrupt state file was accepted")
	}
}
