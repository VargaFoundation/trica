package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

func collectOne(t testing.TB, a *Agent) *sdk.Batch {
	t.Helper()
	var got []*sdk.Batch
	err := a.Collect(context.Background(), sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
		got = append(got, b)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d batches emitted, want exactly one", len(got))
	}
	return got[0]
}

func TestCollectEmitsOneFullScopeBatch(t *testing.T) {
	for _, root := range fakeRoots {
		t.Run(root, func(t *testing.T) {
			a, _ := newTestAgent(t, root)
			b := collectOne(t, a)
			if b.Scope != sdk.ScopeFull || !strings.HasPrefix(b.Source, SourcePrefix) {
				t.Fatalf("scope %q source %q", b.Scope, b.Source)
			}
			if b.Health == nil || b.Health.Collector != "agent" || b.Health.Version != "test" || !b.Health.OK || b.Health.Items != len(b.Objects) {
				t.Fatalf("health %+v", b.Health)
			}
			if a.Kind() != "agent" {
				t.Fatalf("kind %q", a.Kind())
			}
		})
	}
}

func TestCollectProbeFailureIsReportedNotFatal(t *testing.T) {
	// Reading /proc/net/tcp panics here: the sockets probe fails, the others do not.
	a, err := New(Config{FS: panicFS{FS: openRoot(t, "debian12"), on: "/proc/net/tcp"}, Runner: newFakeRunner("debian12"), Now: fixedNow, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	b := collectOne(t, a)
	if b.Health.OK {
		t.Fatal("a failed probe must make the run not OK so that nothing is withdrawn")
	}
	if !strings.Contains(b.Health.Error, "sockets: panic") || !containsSub(b.Health.Warnings, "probe failed: sockets") {
		t.Fatalf("health %+v", b.Health)
	}
	if _, ok := b.Lookup("host"); !ok || len(objectsOf(b, "software")) == 0 || len(objectsOf(b, "interface")) == 0 {
		t.Fatal("the other probes must still report")
	}
	if len(objectsOf(b, "listener")) != 0 {
		t.Fatal("no listener without the sockets probe")
	}
}

type panicFS struct {
	FS
	on string
}

func (p panicFS) Open(name string) (io.ReadCloser, error) {
	if name == p.on {
		panic("surprising file")
	}
	return p.FS.Open(name)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func TestSafelyRecoversPanics(t *testing.T) {
	err := safely(func(*collector) error { panic("boom") }, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err %v", err)
	}
	if err := safely(func(*collector) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
}

func TestObserveStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inv := observe(ctx, openRoot(t, "debian12"), noRunner{}, fsAddrs{path: "/addrs.txt"})
	if len(inv.Failed) != 1 || !strings.HasPrefix(inv.Failed[0], "run: ") {
		t.Fatalf("failed %v", inv.Failed)
	}
}

func TestCollectWithoutIdentityEmitsNothing(t *testing.T) {
	a, err := New(Config{FS: tmpRoot(t, map[string]string{"etc/": ""}), Now: fixedNow, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	err = a.Collect(context.Background(), sdk.EmitterFunc(func(context.Context, *sdk.Batch) error {
		t.Fatal("a host nobody can recognise must not be emitted")
		return nil
	}))
	if !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("err %v", err)
	}
}

func TestCollectReturnsEmitError(t *testing.T) {
	a, _ := newTestAgent(t, "debian12")
	boom := errors.New("spool full")
	err := a.Collect(context.Background(), sdk.EmitterFunc(func(context.Context, *sdk.Batch) error { return boom }))
	if !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

func TestCollectLogsAreJSONWithSourceAndCollector(t *testing.T) {
	var buf bytes.Buffer
	a, err := New(Config{FS: openRoot(t, "debian12"), Runner: newFakeRunner("debian12"), Now: fixedNow, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	collectOne(t, a)
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(bytes.SplitN(buf.Bytes(), []byte("\n"), 2)[0]), &line); err != nil {
		t.Fatalf("%v: %q", err, buf.String())
	}
	if line["source"] != "agent:host-01" || line["collector"] != "agent" || line["msg"] != "collected" {
		t.Fatalf("%v", line)
	}
}

func TestNewDefaults(t *testing.T) {
	t.Run("a fake root runs no command and asks no netlink", func(t *testing.T) {
		a, err := New(Config{Root: "testdata/debian12/root", Logger: quietLogger()})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		if _, ok := a.run.(noRunner); !ok {
			t.Fatalf("runner %T: a fake root must never run the commands of the machine the tests run on", a.run)
		}
		if _, ok := a.addrs.(fsAddrs); !ok {
			t.Fatalf("addrs %T", a.addrs)
		}
		// End to end on the root alone: no SMART data, no LLDP, everything else.
		b := collectOne(t, a)
		if len(objectsOf(b, "disk")) != 3 || len(relations(b, "neighbor_of")) != 0 || !containsSub(b.Health.Warnings, "smartctl not available") {
			t.Fatalf("disks %d warnings %v", len(objectsOf(b, "disk")), b.Health.Warnings)
		}
		if d := mustObj(t, b, "disk:nvme0n1"); d.Attributes["wear_pct"] != nil {
			t.Fatal("wear without smartctl")
		}
	})
	for _, root := range []string{"", "/"} {
		t.Run("the real root "+root, func(t *testing.T) {
			a, err := New(Config{Root: root, Logger: quietLogger()}) // not collected: that would describe the machine running the tests
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if _, ok := a.run.(ExecRunner); !ok {
				t.Fatalf("runner %T", a.run)
			}
			if _, ok := a.addrs.(NetlinkAddrs); !ok {
				t.Fatalf("addrs %T", a.addrs)
			}
		})
	}
	t.Run("an injected FS is not the host", func(t *testing.T) {
		a, err := New(Config{FS: openRoot(t, "rhel9")})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := a.run.(noRunner); !ok {
			t.Fatalf("runner %T", a.run)
		}
	})
	t.Run("missing root", func(t *testing.T) {
		if _, err := New(Config{Root: "testdata/does-not-exist"}); err == nil {
			t.Fatal("a missing root was accepted")
		}
	})
}

func TestRunRefusesShortIntervals(t *testing.T) {
	a, _ := newTestAgent(t, "debian12")
	err := a.Run(context.Background(), MinInterval-time.Second, sdk.EmitterFunc(func(context.Context, *sdk.Batch) error {
		t.Fatal("collected despite the interval")
		return nil
	}))
	if err == nil {
		t.Fatal("an interval below the minimum was accepted")
	}
}

func TestRunCollectsAtOnceAndStopsOnCancel(t *testing.T) {
	a, _ := newTestAgent(t, "debian12")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx, MinInterval, sdk.EmitterFunc(func(context.Context, *sdk.Batch) error {
			n++
			cancel()
			return nil
		}))
	}()
	select {
	case err := <-done:
		if err != nil || n != 1 {
			t.Fatalf("err %v after %d runs", err, n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}

func TestRunSurvivesAFailingEmitter(t *testing.T) {
	a, err := New(Config{FS: openRoot(t, "debian12"), Runner: newFakeRunner("debian12"), Now: fixedNow, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = a.Run(ctx, MinInterval, sdk.EmitterFunc(func(context.Context, *sdk.Batch) error {
		cancel()
		return errors.New("spool full")
	}))
	if err != nil {
		t.Fatalf("a failed run must be logged, not fatal: %v", err)
	}
}

// allowedReads are the places the probes may read. Everything else, and in
// particular /proc/<pid>/environ and /proc/<pid>/cmdline, is out of bounds.
var allowedReads = []*regexp.Regexp{
	regexp.MustCompile(`^/etc/(hostname|machine-id|os-release|systemd/system/[^/]+\.wants)(/|$)`),
	regexp.MustCompile(`^/proc/(cpuinfo|meminfo|net/[a-z0-9_]+|sys/kernel/(hostname|osrelease)|sys/net/ipv4/ip_local_port_range)$`),
	regexp.MustCompile(`^/proc/[0-9]+/comm$`),
	regexp.MustCompile(`^/sys/(class|block)/`),
	regexp.MustCompile(`^/var/lib/dpkg/status$`),
	regexp.MustCompile(`^/(run/systemd/(system|container)|run/\.containerenv|\.dockerenv)$`),
	regexp.MustCompile(`^/addrs\.txt$`),
	regexp.MustCompile(`^/sys/hypervisor/type$`),
}

func TestProbesOnlyReadTheirWhitelistedFiles(t *testing.T) {
	for _, root := range fakeRoots {
		rec := &recordFS{FS: openRoot(t, root)}
		inv := observe(context.Background(), rec, newFakeRunner(root), fsAddrs{path: "/addrs.txt"})
		if len(inv.Failed) != 0 {
			t.Fatalf("%s: %v", root, inv.Failed)
		}
		opened := rec.opened()
		if len(opened) < 20 {
			t.Fatalf("%s: only %d files opened, the recorder is blind", root, len(opened))
		}
		for _, name := range opened {
			ok := false
			for _, re := range allowedReads {
				if re.MatchString(name) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s: probe read %s, which is not on the read list", root, name)
			}
		}
	}
}
