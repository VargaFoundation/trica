package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/transport/uplink"
)

func TestMetricsTextAgeAndTimestamps(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "trica-agent.prom")
	st := uplink.Stats{
		Spooled: 3, SpoolBytes: 1200, OldestSpooled: now.Add(-90 * time.Second),
		LastSent: now.Add(-2 * time.Minute), Sent: 7, Dropped: 1, Rejected: 2,
	}
	if err := writeMetrics(path, version, st, now); err != nil {
		t.Fatal(err)
	}
	m := readMetrics(t, path)
	for name, want := range map[string]float64{
		"trica_agent_spooled_batches":                 3,
		"trica_agent_spool_bytes":                     1200,
		"trica_agent_spool_oldest_age_seconds":        90,
		"trica_agent_last_delivery_timestamp_seconds": float64(now.Add(-2 * time.Minute).Unix()),
		"trica_agent_sent_batches_total":              7,
		"trica_agent_dropped_batches_total":           1,
		"trica_agent_rejected_batches_total":          2,
		"trica_agent_metrics_timestamp_seconds":       float64(now.Unix()),
		"trica_agent_build_info":                      1,
	} {
		if m[name] != want {
			t.Errorf("%s = %v, want %v", name, m[name], want)
		}
	}
	if !strings.Contains(string(metricsText("1.2.3", st, now)), `trica_agent_build_info{version="1.2.3"} 1`) {
		t.Error("the version is not a label of trica_agent_build_info")
	}
	// A spool stamped in the future (a clock set back) is not a negative age.
	st.OldestSpooled = now.Add(time.Minute)
	if err := writeMetrics(path, version, st, now); err != nil {
		t.Fatal(err)
	}
	if age := readMetrics(t, path)["trica_agent_spool_oldest_age_seconds"]; age != 0 {
		t.Errorf("age = %v, want 0", age)
	}
}

func TestWriteMetricsCreatesItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics", "trica-agent.prom")
	if err := writeMetrics(path, version, uplink.Stats{}, time.Now()); err != nil {
		t.Fatalf("a missing directory must be created: %v", err)
	}
	if st, err := os.Stat(filepath.Dir(path)); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("directory: %v, %v", st, err)
	}
	// Below a regular file there is no directory to create: an error, not a panic.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeMetrics(filepath.Join(file, "trica-agent.prom"), version, uplink.Stats{}, time.Now()); err == nil {
		t.Fatal("writing below a regular file must fail")
	}
}
