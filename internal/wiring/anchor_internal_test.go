package wiring

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/audit"
	"github.com/VargaFoundation/trica/internal/testdb"
)

func gathered(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		m := mf.GetMetric()[0]
		if c := m.GetCounter(); c != nil {
			return c.GetValue()
		}
		return m.GetGauge().GetValue()
	}
	t.Fatalf("no metric %s", name)
	return 0
}

// Removing the last entries of the chain leaves it intact (ADR 0013); the
// anchor sees the head go back and says so, in its log and its metrics.
func TestAnchorReportsATruncatedChain(t *testing.T) {
	ctx := context.Background()
	st := testdb.New(t)
	l := &audit.Log{Store: st}
	for _, target := range []string{"proposal/1", "proposal/2", "proposal/3"} {
		if _, err := l.Append(ctx, audit.Entry{Actor: "user:alice", Action: "proposal.approve", Target: target}); err != nil {
			t.Fatal(err)
		}
	}
	var buf strings.Builder
	reg := prometheus.NewRegistry()
	a := &anchor{log: l, logger: slog.New(slog.NewTextHandler(&buf, nil)), m: newAnchorMetrics(reg), now: time.Now}
	a.pass(ctx)
	if got := gathered(t, reg, "trica_audit_head_id"); got != 3 {
		t.Fatalf("head id = %v, want 3", got)
	}
	if !strings.Contains(buf.String(), "audit chain head") || !strings.Contains(buf.String(), "head_id=3") {
		t.Fatalf("log = %q", buf.String())
	}
	a.pass(ctx)
	if got := gathered(t, reg, "trica_audit_head_regressions_total"); got != 0 {
		t.Fatalf("an unchanged head counted as a regression: %v", got)
	}

	err := st.InTx(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`ALTER TABLE audit_log DISABLE TRIGGER audit_log_no_change`,
			`DELETE FROM audit_log WHERE id = 3`,
			`ALTER TABLE audit_log ENABLE TRIGGER audit_log_no_change`,
		} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := l.Verify(ctx); err != nil || !res.Intact() {
		t.Fatalf("verify = %+v, %v; the truncation must be invisible to the chain for this test to mean anything", res, err)
	}
	a.pass(ctx)
	if got := gathered(t, reg, "trica_audit_head_regressions_total"); got != 1 {
		t.Fatalf("regressions = %v, want 1", got)
	}
	if !strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "previous_head_id=3") {
		t.Fatalf("log = %q", buf.String())
	}
}

// Entries appended after a truncation push the head forward again: the
// anchor still sees that the entry it anchored is gone.
func TestAnchorSeesATruncationHiddenByNewEntries(t *testing.T) {
	ctx := context.Background()
	st := testdb.New(t)
	l := &audit.Log{Store: st}
	add := func(target string) {
		t.Helper()
		if _, err := l.Append(ctx, audit.Entry{Actor: "user:alice", Action: "proposal.approve", Target: target}); err != nil {
			t.Fatal(err)
		}
	}
	add("proposal/1")
	add("proposal/2")
	reg := prometheus.NewRegistry()
	a := &anchor{log: l, logger: slog.New(slog.DiscardHandler), m: newAnchorMetrics(reg), now: time.Now}
	a.pass(ctx)
	if err := st.InTx(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`ALTER TABLE audit_log DISABLE TRIGGER audit_log_no_change`,
			`DELETE FROM audit_log WHERE id = 2`,
			`ALTER TABLE audit_log ENABLE TRIGGER audit_log_no_change`,
		} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	add("proposal/3")
	add("proposal/4")
	a.pass(ctx)
	if got := gathered(t, reg, "trica_audit_head_id"); got != 4 {
		t.Fatalf("head id = %v, want 4: the head went forward", got)
	}
	if got := gathered(t, reg, "trica_audit_head_regressions_total"); got != 1 {
		t.Fatalf("regressions = %v, want 1", got)
	}
}

// A head that cannot be read is counted, not fatal.
func TestAnchorCountsAFailedRead(t *testing.T) {
	st := testdb.New(t)
	st.Close()
	reg := prometheus.NewRegistry()
	a := &anchor{log: &audit.Log{Store: st}, logger: slog.New(slog.DiscardHandler), m: newAnchorMetrics(reg), now: time.Now}
	a.pass(context.Background())
	if got := gathered(t, reg, "trica_audit_anchor_errors_total"); got != 1 {
		t.Fatalf("errors = %v, want 1", got)
	}
}
