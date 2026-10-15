package wiring

import (
	"bytes"
	"context"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/audit"
	"github.com/VargaFoundation/trica/internal/server"
)

// anchorMetrics publish the head of the audit chain (ADR 0034). The metrics
// store keeps them where the audit table's writers cannot reach: a head id
// that drops, in its history, is entries removed from the end of the chain.
type anchorMetrics struct {
	headID      prometheus.Gauge
	lastSuccess prometheus.Gauge
	errors      prometheus.Counter
	regressions prometheus.Counter
}

func newAnchorMetrics(reg prometheus.Registerer) *anchorMetrics {
	m := &anchorMetrics{
		headID:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "trica_audit_head_id", Help: "Id of the last entry of the audit chain."}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: "trica_audit_anchor_last_success_timestamp_seconds", Help: "Unix time the audit chain head was last read."}),
		errors:      prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_audit_anchor_errors_total", Help: "Reads of the audit chain head that failed."}),
		regressions: prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_audit_head_regressions_total", Help: "Times the entry anchored at the previous read was gone or had another hash: entries removed or rewritten."}),
	}
	reg.MustRegister(m.headID, m.lastSuccess, m.errors, m.regressions)
	return m
}

// anchor reads the head of the audit chain and publishes it: a log line with
// its id and hash, and the metrics. It remembers the last head it read, so an
// anchored entry that is gone, or has another hash, is reported at once.
type anchor struct {
	log     *audit.Log
	logger  *slog.Logger
	m       *anchorMetrics
	now     func() time.Time
	lastID  int64
	last    []byte
	started bool
}

func (a *anchor) pass(ctx context.Context) {
	id, hash, err := a.log.Head(ctx)
	if err != nil {
		if ctx.Err() == nil {
			a.m.errors.Inc()
			logFailure(a.logger, "audit anchor: head not read", err)
		}
		return
	}
	if a.started && a.lastID > 0 {
		// The head alone misses a truncation followed by new entries (ids
		// are never reused): the entry anchored last must still be there.
		prev, ok, err := a.log.HashOf(ctx, a.lastID)
		if err != nil {
			if ctx.Err() == nil {
				a.m.errors.Inc()
				logFailure(a.logger, "audit anchor: previous head not read", err)
			}
			return
		}
		if !ok || !bytes.Equal(prev, a.last) {
			a.m.regressions.Inc()
			a.logger.Error("audit chain entry anchored earlier is gone or changed: entries were removed or rewritten",
				"previous_head_id", a.lastID, "previous_head_hash", hex.EncodeToString(a.last),
				"head_id", id, "head_hash", hex.EncodeToString(hash))
		}
	}
	a.started, a.lastID, a.last = true, id, hash
	a.m.headID.Set(float64(id))
	a.m.lastSuccess.Set(float64(a.now().Unix()))
	a.logger.Info("audit chain head", "head_id", id, "head_hash", hex.EncodeToString(hash))
}

// runAuditAnchor publishes the head of the audit chain every
// audit.anchor_interval, starting at once. Every replica does: the head is
// the same, and a lost replica must not stop the anchoring. A failed read is
// logged, counted and retried at the next tick.
func (w *Wiring) runAuditAnchor(ctx context.Context, d server.Deps) error {
	c, err := w.shared(d)
	if err != nil {
		return err
	}
	a := &anchor{log: c.auditor.Log, logger: d.Log, m: newAnchorMetrics(d.Metrics), now: time.Now}
	t := time.NewTicker(w.cfg.Audit.AnchorInterval)
	defer t.Stop()
	for {
		a.pass(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
