package wiring

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/deduce"
	"github.com/VargaFoundation/trica/internal/server"
)

// deduceLockKey is the advisory lock that makes the deducer a singleton across
// replicas (ADR 0014). It differs from the engine's and the audit log's.
const deduceLockKey = 7426203

type deduceMetrics struct {
	runs, errors, skipped prometheus.Counter
	cables, findings      prometheus.Gauge
	lastSuccess           prometheus.Gauge
	duration              prometheus.Histogram
}

func newDeduceMetrics(reg prometheus.Registerer) *deduceMetrics {
	m := &deduceMetrics{
		runs:        prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_deduce_runs_total", Help: "Cabling deduction passes run."}),
		errors:      prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_deduce_errors_total", Help: "Cabling deduction passes that failed."}),
		skipped:     prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_deduce_skipped_total", Help: "Passes skipped because another replica held the lock."}),
		cables:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "trica_deduce_cables", Help: "Cables deduced by the last successful pass."}),
		findings:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "trica_deduce_findings", Help: "Declared-versus-observed cabling findings of the last successful pass."}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: "trica_deduce_last_success_timestamp_seconds", Help: "Unix time of the last successful pass."}),
		duration:    prometheus.NewHistogram(prometheus.HistogramOpts{Name: "trica_deduce_duration_seconds", Help: "Duration of a deduction pass.", Buckets: prometheus.ExponentialBuckets(0.01, 4, 8)}),
	}
	reg.MustRegister(m.runs, m.errors, m.skipped, m.cables, m.findings, m.lastSuccess, m.duration)
	return m
}

// runDeduce deduces cabling from LLDP and forwarding tables every
// deduce.interval, starting at once. A failed pass is logged, counted and
// retried at the next tick: it does not stop the server. Only one replica
// deduces at a time.
func (w *Wiring) runDeduce(ctx context.Context, d server.Deps) error {
	m := newDeduceMetrics(d.Metrics)
	dd := &deduce.Deducer{Engine: d.Engine, Store: d.Store, Log: d.Log}
	t := time.NewTicker(w.cfg.Deduce.Interval)
	defer t.Stop()
	for {
		w.deducePass(ctx, d, dd, m)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (w *Wiring) deducePass(ctx context.Context, d server.Deps, dd *deduce.Deducer, m *deduceMetrics) {
	// The lock lives on one connection for the pass; releasing it on that
	// connection (or closing the connection) frees it.
	conn, err := d.Store.Pool.Acquire(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.errors.Inc()
			logFailure(d.Log, "deduction: no database connection", err)
		}
		return
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, deduceLockKey).Scan(&got); err != nil {
		if ctx.Err() == nil {
			m.errors.Inc()
			logFailure(d.Log, "deduction: lock", err)
		}
		return
	}
	if !got {
		m.skipped.Inc()
		return
	}
	defer func() {
		// Not the pass's context: the lock must be released on shutdown too.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, deduceLockKey)
	}()

	start := time.Now()
	rep, err := dd.Run(ctx)
	m.runs.Inc()
	m.duration.Observe(time.Since(start).Seconds())
	if err != nil {
		if ctx.Err() == nil {
			m.errors.Inc()
			d.Log.Error("deduction failed", "source", deduce.Source, "error", err.Error())
		}
		return
	}
	m.cables.Set(float64(len(rep.Plan.Cables)))
	m.findings.Set(float64(len(rep.Plan.Findings)))
	m.lastSuccess.SetToCurrentTime()
}
