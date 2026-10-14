// Package runner runs collectors on a schedule: one loop per target, each run
// bounded by the target's timeout, never overlapping with the previous run of
// the same target, and contained: a target that fails, hangs or panics does
// not stop the others. Every run ends with a health record in a batch (the
// collection health board reads them), whatever the collector did.
//
// The runner knows nothing of where batches go: it hands them to an
// sdk.Emitter, which is the reconciliation engine inside the server and a
// transport client in `trica collector`. It never writes the inventory.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// DefaultJitter is the fraction of a target's interval by which each run is
// delayed at random, so that targets with the same interval do not hit the
// network together.
const DefaultJitter = 0.1

// shutdownGrace is how long Run waits for runs in flight once its context is
// cancelled. A collector that ignores its context is abandoned after that.
const shutdownGrace = 10 * time.Second

// Config configures a Runner.
type Config struct {
	Targets []collect.Target
	// Emitter receives every batch the collectors emit.
	Emitter sdk.Emitter
	Log     *slog.Logger
	// Registerer receives the metrics; nil keeps them private.
	Registerer prometheus.Registerer
	// Jitter is the fraction of the interval added at random before each run;
	// DefaultJitter when zero, none when negative.
	Jitter float64
	// Build turns a target into a collector; collect.New when nil. Tests inject
	// their own.
	Build func(collect.Target) (sdk.Collector, error)
}

// Runner runs the collectors of a list of targets.
type Runner struct {
	cfg     Config
	log     *slog.Logger
	jobs    []*job
	metrics *metrics
}

type job struct {
	target    collect.Target
	collector sdk.Collector
	running   atomic.Bool
}

// New builds the collector of every target. A target that cannot be built (an
// unknown kind, a bad option, a missing name) is an error: a configuration
// typo must stop the start, not silently collect less. Nothing touches the
// network or reads a credential yet.
func New(cfg Config) (*Runner, error) {
	if cfg.Emitter == nil {
		return nil, errors.New("runner: no emitter")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	build := cfg.Build
	if build == nil {
		build = collect.New
	}
	r := &Runner{cfg: cfg, log: cfg.Log, metrics: newMetrics(cfg.Registerer)}
	seen := map[string]bool{}
	for _, t := range cfg.Targets {
		if seen[t.Source()] {
			return nil, fmt.Errorf("runner: target %s is declared twice", t.Source())
		}
		seen[t.Source()] = true
		c, err := build(t)
		if err != nil {
			return nil, err
		}
		r.jobs = append(r.jobs, &job{target: t, collector: c})
		r.metrics.init(t)
	}
	return r, nil
}

// Run runs every target until ctx ends. It returns nil then.
func (r *Runner) Run(ctx context.Context) error {
	var loops sync.WaitGroup
	for _, j := range r.jobs {
		loops.Add(1)
		go func() {
			defer loops.Done()
			r.loop(ctx, j)
		}()
	}
	loops.Wait()
	return nil
}

// RunOnce runs every target once, concurrently, and waits for them. It is what
// a test, or an operator checking a configuration, wants.
func (r *Runner) RunOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for _, j := range r.jobs {
		r.tick(ctx, j, &wg)
	}
	wg.Wait()
}

// loop schedules one target: a first run after a random delay of at most one
// jitter, then one every interval (plus jitter). A run still going when the
// next one is due makes that one skipped, never overlapped.
func (r *Runner) loop(ctx context.Context, j *job) {
	var runs sync.WaitGroup
	defer func() {
		done := make(chan struct{})
		go func() { runs.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(shutdownGrace):
			r.log.Warn("collector ignored cancellation, abandoned", "source", j.target.Source(), "collector", j.target.Kind, "target", j.target.Name)
		}
	}()

	interval := j.target.EffectiveInterval()
	timer := time.NewTimer(r.delay(interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		r.tick(ctx, j, &runs)
		timer.Reset(interval + r.delay(interval))
	}
}

// delay is a random duration in [0, jitter*interval).
func (r *Runner) delay(interval time.Duration) time.Duration {
	j := r.cfg.Jitter
	if j == 0 {
		j = DefaultJitter
	}
	if j < 0 {
		return 0
	}
	d := time.Duration(j * float64(interval))
	if d <= 0 {
		return 0
	}
	return rand.N(d) //nolint:gosec // jitter, not a secret
}

// tick starts a run of j unless the previous one is still going.
func (r *Runner) tick(ctx context.Context, j *job, wg *sync.WaitGroup) {
	if !j.running.CompareAndSwap(false, true) {
		r.metrics.skipped(j.target)
		r.log.Warn("run skipped, the previous one is still going", "source", j.target.Source(), "collector", j.target.Kind, "target", j.target.Name)
		return
	}
	run := func() {
		defer j.running.Store(false)
		r.runOne(ctx, j)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		run()
	}()
}

// recorder sits between a collector and the emitter: it notes the health
// record the collector reported and whether the emitter refused a batch.
type recorder struct {
	next    sdk.Emitter
	health  *sdk.Health
	emitErr error
}

func (e *recorder) Emit(ctx context.Context, b *sdk.Batch) error {
	if b != nil && b.Health != nil {
		e.health = b.Health
	}
	err := e.next.Emit(ctx, b)
	if err != nil && e.emitErr == nil {
		e.emitErr = err
	}
	return err
}

// runOne performs one run of j and accounts for it. Whatever the collector
// does, the run ends with one health record emitted: the collector's own, or
// one made here for an error, a panic or a collector that reported none.
func (r *Runner) runOne(ctx context.Context, j *job) {
	t := j.target
	log := r.log.With("source", t.Source(), "collector", t.Kind, "target", t.Name)
	started := time.Now()
	rec := &recorder{next: r.cfg.Emitter}

	err := r.collect(ctx, j, rec)

	switch {
	case err != nil:
		// The collector gave up without saying so (or after a healthy record,
		// or by panicking): make the failure visible on the health board. A
		// collector that already reported its failure is not reported twice.
		var pe panicError
		if errors.As(err, &pe) || rec.health == nil || rec.health.OK {
			r.emitFailed(ctx, log, t, started, err)
		}
	case rec.health == nil && rec.emitErr == nil:
		// A collector that emits no record: report a healthy empty run so
		// the board shows the target is being collected.
		b := collect.Succeeded(sdk.NewBatch(t.Source(), sdk.ScopePartial), t, t.Kind, "", started, nil)
		if e := r.cfg.Emitter.Emit(ctx, b); e != nil {
			err = e
		}
	case rec.health != nil && !rec.health.OK:
		err = errors.New(rec.health.Error)
	case rec.emitErr != nil:
		err = rec.emitErr
	}

	if ctx.Err() != nil && err != nil {
		// Shutting down: an interrupted run is not a failure of the target.
		log.Info("run interrupted by shutdown")
		return
	}
	r.metrics.observe(t, time.Since(started), err)
	if err != nil {
		log.Error("collection failed", "error", err.Error())
		return
	}
	log.Info("collection done", "duration_ms", time.Since(started).Milliseconds())
}

// collect calls the collector, bounded by the target's timeout, and turns a
// panic into an error.
func (r *Runner) collect(ctx context.Context, j *job, rec *recorder) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = panicError{value: fmt.Sprint(p)}
		}
	}()
	cctx, cancel := collect.Context(ctx, j.target)
	defer cancel()
	return j.collector.Collect(cctx, rec)
}

func (r *Runner) emitFailed(ctx context.Context, log *slog.Logger, t collect.Target, started time.Time, err error) {
	// Not the run's context: it may have timed out, and the failure must still
	// be reported (unless the whole runner is stopping).
	ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return
	}
	if e := r.cfg.Emitter.Emit(ectx, collect.Failed(t, t.Kind, "", started, err)); e != nil {
		log.Error("could not report the failure", "error", e.Error())
	}
}

// panicError is a collector panic, kept apart so that it is reported even if
// the collector had emitted a failed record before.
type panicError struct{ value string }

func (p panicError) Error() string {
	v := p.value
	if len(v) > 200 {
		v = v[:200] + "..."
	}
	return "collector panicked: " + v
}

// metrics are per target, labelled by collector kind and target name.
type metrics struct {
	runs, failures, skips *prometheus.CounterVec
	duration              *prometheus.HistogramVec
	lastSuccess           *prometheus.GaugeVec
}

var labels = []string{"collector", "target"}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		runs:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "trica_collect_runs_total", Help: "Collection runs, by target."}, labels),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "trica_collect_failures_total", Help: "Collection runs that failed (error, panic, failed health record, batch refused)."}, labels),
		skips:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "trica_collect_skipped_total", Help: "Runs skipped because the previous run of the target was still going."}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "trica_collect_duration_seconds", Help: "Duration of a collection run.", Buckets: prometheus.ExponentialBuckets(0.05, 3, 9),
		}, labels),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "trica_collect_last_success_timestamp_seconds", Help: "Unix time of the last successful run of the target."}, labels),
	}
	if reg != nil {
		reg.MustRegister(m.runs, m.failures, m.skips, m.duration, m.lastSuccess)
	}
	return m
}

// init makes the series of t exist at zero, so that a target that never ran
// is visible (and alertable) rather than absent.
func (m *metrics) init(t collect.Target) {
	l := prometheus.Labels{"collector": t.Kind, "target": t.Name}
	m.runs.With(l)
	m.failures.With(l)
	m.skips.With(l)
}

func (m *metrics) skipped(t collect.Target) {
	m.skips.WithLabelValues(t.Kind, t.Name).Inc()
}

func (m *metrics) observe(t collect.Target, d time.Duration, err error) {
	m.runs.WithLabelValues(t.Kind, t.Name).Inc()
	m.duration.WithLabelValues(t.Kind, t.Name).Observe(d.Seconds())
	if err != nil {
		m.failures.WithLabelValues(t.Kind, t.Name).Inc()
		return
	}
	m.lastSuccess.WithLabelValues(t.Kind, t.Name).SetToCurrentTime()
}
