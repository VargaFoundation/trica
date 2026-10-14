package runner_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/arista"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

var quiet = slog.New(slog.NewJSONHandler(io.Discard, nil))

// sink keeps what the runner emits.
type sink struct {
	mu      sync.Mutex
	batches []*sdk.Batch
	err     error
}

func (s *sink) Emit(_ context.Context, b *sdk.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.batches = append(s.batches, b)
	return nil
}

func (s *sink) all() []*sdk.Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*sdk.Batch(nil), s.batches...)
}

// from returns the batches of one source.
func (s *sink) from(source string) []*sdk.Batch {
	var out []*sdk.Batch
	for _, b := range s.all() {
		if b.Source == source {
			out = append(out, b)
		}
	}
	return out
}

// stub is a collector whose run is a function.
type stub struct {
	kind string
	run  func(ctx context.Context, t collect.Target, emit sdk.Emitter) error
	t    collect.Target
}

func (s *stub) Kind() string { return s.kind }
func (s *stub) Collect(ctx context.Context, emit sdk.Emitter) error {
	return s.run(ctx, s.t, emit)
}

// healthy emits what a collector emits: one batch with a successful record.
func healthy(_ context.Context, t collect.Target, emit sdk.Emitter) error {
	b := sdk.NewBatch(t.Source(), sdk.ScopeFull)
	b.Object("d", "device").Identify("name", t.Name).Set("role", "switch")
	return emit.Emit(context.Background(), collect.Succeeded(b, t, t.Kind, "test", time.Now(), nil))
}

// builder maps a target name to the behaviour of its stub.
func builder(runs map[string]func(context.Context, collect.Target, sdk.Emitter) error) func(collect.Target) (sdk.Collector, error) {
	return func(t collect.Target) (sdk.Collector, error) {
		fn, ok := runs[t.Name]
		if !ok {
			return nil, errors.New("no stub for " + t.Name)
		}
		return &stub{kind: t.Kind, run: fn, t: t}, nil
	}
}

func target(name string, interval time.Duration) collect.Target {
	return collect.Target{Kind: "stub", Name: name, Interval: interval, Timeout: time.Second}
}

func start(t *testing.T, r *runner.Runner) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Error("the runner did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRunsATargetOnSchedule(t *testing.T) {
	s := &sink{}
	reg := prometheus.NewRegistry()
	r, err := runner.New(runner.Config{
		Targets:    []collect.Target{target("sw-a1", 20*time.Millisecond)},
		Emitter:    s,
		Log:        quiet,
		Registerer: reg,
		Jitter:     -1,
		Build:      builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{"sw-a1": healthy}),
	})
	if err != nil {
		t.Fatal(err)
	}
	start(t, r)
	waitFor(t, "three runs", func() bool { return len(s.from("stub:sw-a1")) >= 3 })

	for _, b := range s.from("stub:sw-a1") {
		if b.Health == nil || !b.Health.OK || b.Health.Target != "sw-a1" {
			t.Fatalf("health: %+v", b.Health)
		}
	}
	waitFor(t, "the run counter", func() bool {
		return value(t, reg, "trica_collect_runs_total", "sw-a1") >= 3
	})
	if got := value(t, reg, "trica_collect_failures_total", "sw-a1"); got != 0 {
		t.Fatalf("failures: %v", got)
	}
	if ts := value(t, reg, "trica_collect_last_success_timestamp_seconds", "sw-a1"); time.Since(time.Unix(int64(ts), 0)) > 5*time.Second {
		t.Fatalf("last success: %v", ts)
	}
}

func TestAFailingTargetIsReportedAndDoesNotStopTheOthers(t *testing.T) {
	s := &sink{}
	reg := prometheus.NewRegistry()
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{target("bad", 20*time.Millisecond), target("good", 20*time.Millisecond)},
		Emitter: s, Log: quiet, Registerer: reg, Jitter: -1,
		Build: builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{
			"bad": func(context.Context, collect.Target, sdk.Emitter) error {
				return errors.New("connection refused")
			},
			"good": healthy,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	start(t, r)
	waitFor(t, "failures and successes", func() bool {
		return len(s.from("stub:bad")) >= 2 && len(s.from("stub:good")) >= 3
	})

	b := s.from("stub:bad")[0]
	if b.Health == nil || b.Health.OK || !strings.Contains(b.Health.Error, "connection refused") || b.Health.Target != "bad" || b.Health.Collector != "stub" {
		t.Fatalf("health: %+v", b.Health)
	}
	if b.Scope != sdk.ScopePartial {
		t.Fatalf("a failed run must not withdraw anything: scope %q", b.Scope)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("the failure batch is invalid: %v", err)
	}
	waitFor(t, "the failure counter", func() bool {
		return value(t, reg, "trica_collect_failures_total", "bad") >= 1
	})
	if got := value(t, reg, "trica_collect_failures_total", "good"); got != 0 {
		t.Fatalf("the healthy target is counted as failing: %v", got)
	}
	if ts := value(t, reg, "trica_collect_last_success_timestamp_seconds", "bad"); ts != 0 {
		t.Fatalf("a target that never succeeded has a last success: %v", ts)
	}
}

func TestAPanickingCollectorIsContained(t *testing.T) {
	s := &sink{}
	var calls atomic.Int32
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{target("boom", 20*time.Millisecond), target("good", 20*time.Millisecond)},
		Emitter: s, Log: quiet, Jitter: -1,
		Build: builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{
			"boom": func(context.Context, collect.Target, sdk.Emitter) error {
				calls.Add(1)
				panic("index out of range [3] with length 3")
			},
			"good": healthy,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	start(t, r)
	waitFor(t, "the panicking target to run again after its panic", func() bool {
		return calls.Load() >= 2 && len(s.from("stub:good")) >= 3 && len(s.from("stub:boom")) >= 2
	})
	h := s.from("stub:boom")[0].Health
	if h == nil || h.OK || !strings.Contains(h.Error, "panicked") || !strings.Contains(h.Error, "index out of range") {
		t.Fatalf("health: %+v", h)
	}
}

func TestRunsOfATargetNeverOverlap(t *testing.T) {
	var current, peak, calls atomic.Int32
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{target("slow", 5*time.Millisecond)},
		Emitter: &sink{}, Log: quiet, Jitter: -1, Registerer: prometheus.NewRegistry(),
		Build: builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{
			"slow": func(ctx context.Context, tg collect.Target, emit sdk.Emitter) error {
				n := current.Add(1)
				defer current.Add(-1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				calls.Add(1)
				select {
				case <-time.After(60 * time.Millisecond):
				case <-ctx.Done():
				}
				return healthy(ctx, tg, emit)
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	stop := start(t, r)
	waitFor(t, "three runs", func() bool { return calls.Load() >= 3 })
	stop()
	if p := peak.Load(); p != 1 {
		t.Fatalf("%d runs of one target at the same time", p)
	}
}

func TestSkippedRunsAreCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	var calls atomic.Int32
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{target("slow", 5*time.Millisecond)},
		Emitter: &sink{}, Log: quiet, Jitter: -1, Registerer: reg,
		Build: builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{
			"slow": func(ctx context.Context, tg collect.Target, emit sdk.Emitter) error {
				calls.Add(1)
				time.Sleep(50 * time.Millisecond)
				return healthy(ctx, tg, emit)
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	start(t, r)
	waitFor(t, "skipped runs", func() bool {
		return value(t, reg, "trica_collect_skipped_total", "slow") >= 2 && calls.Load() >= 1
	})
}

func TestARunIsBoundedByTheTargetTimeout(t *testing.T) {
	s := &sink{}
	tg := target("hang", time.Hour)
	tg.Timeout = 30 * time.Millisecond
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{tg}, Emitter: s, Log: quiet, Jitter: -1,
		Build: builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{
			"hang": func(ctx context.Context, _ collect.Target, _ sdk.Emitter) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(context.Background())
	bs := s.from("stub:hang")
	if len(bs) != 1 || bs[0].Health == nil || bs[0].Health.OK || !strings.Contains(bs[0].Health.Error, "deadline") {
		t.Fatalf("batches: %+v", bs)
	}
}

func TestACollectorThatReportsNothingGetsAHealthyRecord(t *testing.T) {
	s := &sink{}
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{target("quiet", time.Hour)}, Emitter: s, Log: quiet,
		Build: builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{
			"quiet": func(context.Context, collect.Target, sdk.Emitter) error { return nil },
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(context.Background())
	bs := s.from("stub:quiet")
	if len(bs) != 1 || bs[0].Health == nil || !bs[0].Health.OK || bs[0].Scope != sdk.ScopePartial {
		t.Fatalf("batches: %+v", bs)
	}
	if err := bs[0].Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestARefusedBatchIsAFailure(t *testing.T) {
	reg := prometheus.NewRegistry()
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{target("sw-a1", time.Hour)}, Emitter: &sink{err: errors.New("rejected")}, Log: quiet, Registerer: reg,
		Build: builder(map[string]func(context.Context, collect.Target, sdk.Emitter) error{"sw-a1": healthy}),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(context.Background())
	if got := value(t, reg, "trica_collect_failures_total", "sw-a1"); got != 1 {
		t.Fatalf("failures: %v", got)
	}
}

func TestNewRefusesWhatCannotRun(t *testing.T) {
	ok := func(c collect.Target) (sdk.Collector, error) { return &stub{kind: c.Kind, run: healthy, t: c}, nil }
	if _, err := runner.New(runner.Config{Targets: nil}); err == nil {
		t.Error("no emitter accepted")
	}
	if _, err := runner.New(runner.Config{Emitter: &sink{}, Targets: []collect.Target{{Kind: "nope", Name: "x"}}}); err == nil || !strings.Contains(err.Error(), "unknown collector kind") {
		t.Errorf("unknown kind: %v", err)
	}
	dup := []collect.Target{target("a", 0), target("a", 0)}
	if _, err := runner.New(runner.Config{Emitter: &sink{}, Targets: dup, Build: ok}); err == nil {
		t.Error("duplicate target accepted")
	}
}

// The real collector, against its fake, through the registry: what `all`
// registers is what a targets file can name.
func TestRunsARealCollectorAgainstItsFake(t *testing.T) {
	t.Setenv(arista.PasswordEnv, arista.FakePassword)
	good := arista.NewFakeArista(arista.EOS420)
	defer good.Close()
	bad := arista.NewFakeArista(arista.EOS420)
	defer bad.Close()
	bad.RejectCredentials()

	s := &sink{}
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{good.Target("sw-a1"), bad.Target("sw-a2")},
		Emitter: s, Log: quiet, Registerer: prometheus.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(context.Background())

	g := s.from("arista:sw-a1")
	if len(g) != 1 || g[0].Health == nil || !g[0].Health.OK || len(g[0].Objects) == 0 {
		t.Fatalf("good target: %+v", g)
	}
	b := s.from("arista:sw-a2")
	if len(b) != 1 || b[0].Health == nil || b[0].Health.OK || b[0].Scope != sdk.ScopePartial {
		t.Fatalf("a target the collector reported as failed must produce exactly one failed record: %+v", b)
	}
}

// value reads the series of one target from the registry; zero when absent.
func value(t *testing.T, reg *prometheus.Registry, name, target string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "target" && l.GetValue() == target {
					if m.Counter != nil {
						return m.GetCounter().GetValue()
					}
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	return 0
}
