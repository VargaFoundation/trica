package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// These tests run the agent over a fixture host and apply what it emits through
// the real reconcile engine on PostgreSQL: the agent's batches are only worth
// something if the engine accepts them as they are.

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func newTestEngine(t *testing.T) (*reconcile.Engine, *testClock) {
	t.Helper()
	c := &testClock{t: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}
	reg := schema.Builtin()
	return &reconcile.Engine{Store: testdb.New(t), Schema: func() *schema.Registry { return reg }, Now: c.now}, c
}

func fixtureRoot(host string) string { return filepath.Join("testdata", host, "root") }

// collectRoot runs the agent over a tree (a fixture host, or an edited copy
// of one) with the recorded command output of that fixture host.
func collectRoot(t *testing.T, root, host string, c *testClock) *sdk.Batch {
	t.Helper()
	fsys, err := NewRootFS(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	a, err := New(Config{FS: fsys, Runner: newFakeRunner(host), Now: c.now, Version: "test", Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	return collectOne(t, a)
}

func applyBatch(t *testing.T, e *reconcile.Engine, b *sdk.Batch) *reconcile.Result {
	t.Helper()
	res, err := e.Apply(context.Background(), b, "", "")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return res
}

// notLLDP returns the engine warnings that are not about LLDP peers: the
// switch the agent's host is plugged into is only known once its own collector
// has run, until then the neighbour is reported as unresolved.
func notLLDP(ws []string) []string {
	var out []string
	for _, w := range ws {
		if !strings.Contains(w, "neighbor") {
			out = append(out, w)
		}
	}
	return out
}

func TestEngineAcceptsTheAgentBatch(t *testing.T) {
	for _, host := range fakeRoots {
		t.Run(host, func(t *testing.T) {
			e, c := newTestEngine(t)
			b := collectRoot(t, fixtureRoot(host), host, c)
			res := applyBatch(t, e, b)
			if w := notLLDP(res.Warnings); len(w) != 0 {
				// An attribute the schema does not know is dropped with a warning: each one is a gap between the agent and the schema.
				t.Fatalf("engine warnings: %v", w)
			}
			if res.Created < len(b.Objects) {
				t.Fatalf("created %d of %d objects", res.Created, len(b.Objects))
			}
			if res.RelationsOpened == 0 {
				t.Fatal("no relation opened")
			}
		})
	}
}

func TestSecondRunChangesNothing(t *testing.T) {
	for _, host := range fakeRoots {
		t.Run(host, func(t *testing.T) {
			e, c := newTestEngine(t)
			first := applyBatch(t, e, collectRoot(t, fixtureRoot(host), host, c))
			if first.Created == 0 {
				t.Fatal("nothing created on the first run")
			}
			c.t = c.t.Add(5 * time.Minute)
			second := applyBatch(t, e, collectRoot(t, fixtureRoot(host), host, c))
			if second.Created != 0 || second.ValuesChanged != 0 || second.RelationsOpened != 0 || second.RelationsClosed != 0 ||
				len(second.Changes) != 0 || len(second.Touched) != 0 {
				t.Fatalf("an unchanged host changed the inventory: %+v", second)
			}
			if w := notLLDP(second.Warnings); len(w) != 0 {
				t.Fatalf("warnings %v", w)
			}
		})
	}
}

// editedRoot copies the Debian fixture and rewrites one file of the copy.
func editedRoot(t *testing.T, rel, old, repl string) string {
	t.Helper()
	dst := t.TempDir()
	copyTree(t, fixtureRoot("debian12"), dst)
	p := filepath.Join(dst, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestUpgradedPackageIsANewSoftwareAndTheOldRunsIsClosed(t *testing.T) {
	e, c := newTestEngine(t)
	applyBatch(t, e, collectRoot(t, fixtureRoot("debian12"), "debian12", c))

	// The software key carries the version (see docs/reference/agent.md): an
	// upgrade is a new software object the host runs, and the old version stops
	// being run.
	c.t = c.t.Add(24 * time.Hour)
	upgraded := editedRoot(t, "var/lib/dpkg/status", "Version: 5.2.15-2+b7", "Version: 5.2.15-2+b8")
	res := applyBatch(t, e, collectRoot(t, upgraded, "debian12", c))
	if res.Created != 1 || res.RelationsOpened != 1 || res.RelationsClosed != 1 {
		t.Fatalf("one package upgrade must be one new software, one runs opened, one closed: %+v", res)
	}
	// The only values written are the four attributes (key, kind, name, version) of the new software.
	created := ""
	for _, ch := range res.Changes {
		if ch.Op == "create" {
			created = ch.ObjectID
		}
	}
	sets := 0
	for _, ch := range res.Changes {
		if ch.Op == "set" {
			sets++
			if ch.ObjectID != created {
				t.Errorf("a value of an existing object changed: %+v", ch)
			}
		}
	}
	if sets != 4 || res.ValuesChanged != 4 {
		t.Fatalf("%d sets, %d values changed", sets, res.ValuesChanged)
	}
	if w := notLLDP(res.Warnings); len(w) != 0 {
		t.Fatalf("warnings %v", w)
	}

	// And it settles: the next run over the upgraded host changes nothing.
	c.t = c.t.Add(5 * time.Minute)
	again := applyBatch(t, e, collectRoot(t, upgraded, "debian12", c))
	if again.Created != 0 || again.ValuesChanged != 0 || again.RelationsOpened != 0 || again.RelationsClosed != 0 {
		t.Fatalf("%+v", again)
	}
}

func TestKernelChangeIsOneValueChange(t *testing.T) {
	e, c := newTestEngine(t)
	applyBatch(t, e, collectRoot(t, fixtureRoot("debian12"), "debian12", c))

	c.t = c.t.Add(24 * time.Hour)
	rebooted := editedRoot(t, "proc/sys/kernel/osrelease", "6.1.0-28-amd64", "6.1.0-29-amd64")
	res := applyBatch(t, e, collectRoot(t, rebooted, "debian12", c))
	if res.ValuesChanged != 1 || res.Created != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
		t.Fatalf("a new kernel must change exactly the kernel attribute: %+v", res)
	}
}

func TestBrokenProbeWithdrawsNothing(t *testing.T) {
	// A run whose sockets probe broke reports its listeners and connections as
	// absent. The agent flags the run not OK, so the engine keeps what the
	// previous run claimed instead of closing what this one could not renew.
	e, c := newTestEngine(t)
	applyBatch(t, e, collectRoot(t, fixtureRoot("debian12"), "debian12", c))

	c.t = c.t.Add(5 * time.Minute)
	a, err := New(Config{FS: panicFS{FS: openRoot(t, "debian12"), on: "/proc/net/tcp"}, Runner: newFakeRunner("debian12"), Now: c.now, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	b := collectOne(t, a)
	if b.Health.OK || len(relations(b, "talks_to")) != 0 {
		t.Fatalf("setup: health %+v", b.Health)
	}
	res := applyBatch(t, e, b)
	if res.RelationsClosed != 0 || len(res.Touched) != 0 {
		t.Fatalf("a broken probe withdrew claims: %+v", res)
	}
}
