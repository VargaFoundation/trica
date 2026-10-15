package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/VargaFoundation/trica/internal/agent"
	"github.com/VargaFoundation/trica/internal/transport/uplink"
)

// envMetricsFile stands for --metrics-file.
const envMetricsFile = "TRICA_AGENT_METRICS_FILE"

// metricsText renders the transport state in the Prometheus text format, for
// the node exporter's textfile collector (ADR 0033): the agent opens no port.
// Counters start at zero with each process, as a /metrics endpoint's would.
func metricsText(version string, st uplink.Stats, now time.Time) []byte {
	var b bytes.Buffer
	metric := func(name, typ, help string, value float64, labels string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n%s%s %s\n", name, help, name, typ, name, labels,
			strconv.FormatFloat(value, 'g', -1, 64))
	}
	unix := func(t time.Time) float64 {
		if t.IsZero() {
			return 0
		}
		return float64(t.UnixNano()) / 1e9
	}
	age := 0.0
	if !st.OldestSpooled.IsZero() {
		age = max(0, now.Sub(st.OldestSpooled).Seconds())
	}
	metric("trica_agent_build_info", "gauge", "Version of the running agent.", 1,
		`{version="`+version+`"}`)
	metric("trica_agent_spooled_batches", "gauge", "Batches waiting in the spool for the server.",
		float64(st.Spooled), "")
	metric("trica_agent_spool_bytes", "gauge", "Size of the batches waiting in the spool.",
		float64(st.SpoolBytes), "")
	metric("trica_agent_spool_oldest_age_seconds", "gauge",
		"Age of the oldest batch waiting in the spool: how long the server has been out of reach. 0 when the spool is empty.",
		age, "")
	metric("trica_agent_last_delivery_timestamp_seconds", "gauge",
		"Unix time the server last accepted a batch from this process. 0 if never.", unix(st.LastSent), "")
	metric("trica_agent_sent_batches_total", "counter", "Batches the server accepted.", float64(st.Sent), "")
	metric("trica_agent_dropped_batches_total", "counter",
		"Batches lost: evicted from a full spool or too large for it.", float64(st.Dropped), "")
	metric("trica_agent_rejected_batches_total", "counter",
		"Spooled batches the server refused on resend, discarded.", float64(st.Rejected), "")
	metric("trica_agent_metrics_timestamp_seconds", "gauge",
		"Unix time this file was written: a stale file means the agent stopped.", unix(now), "")
	return b.Bytes()
}

// writeMetrics replaces path with the current metrics. The node exporter reads
// the file at any time: it must see the old content or the new, never half.
//
// The directory is created when missing (mode 0755: the exporter must be able
// to enter it), so that a fresh node needs no setup step.
func writeMetrics(path, version string, st uplink.Stats, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 G703 -- the operator names the path; read by the node exporter, holds no secret
		return fmt.Errorf("metrics file: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("metrics file: %w", err)
	}
	_, werr := tmp.Write(metricsText(version, st, now))
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644) // #nosec G302 G703 -- the operator names the path; read by the node exporter, holds no secret
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path) // #nosec G703 -- the operator names the path
	}
	if werr != nil {
		_ = os.Remove(tmp.Name()) // #nosec G703 -- our own temporary file
		return fmt.Errorf("metrics file: %w", werr)
	}
	return nil
}

// metricsWriter writes the metrics of stats to path every interval until ctx
// ends, and once more at the end. A failed write is logged; the next one
// tries again.
func metricsWriter(ctx context.Context, path string, interval time.Duration, stats func() uplink.Stats, logger *slog.Logger) {
	write := func() {
		if err := writeMetrics(path, version, stats(), time.Now()); err != nil {
			logger.Warn("cannot write the metrics file", "collector", agent.CollectorName, "target", path, "error", err)
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		write()
		select {
		case <-ctx.Done():
			write()
			return
		case <-t.C:
		}
	}
}
