package reconcile_test

import (
	"context"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func hardwareBatch(c *clock) *sdk.Batch {
	b := batch(c, "redfish:srv1", sdk.ScopeFull)
	b.Object("srv", "device").Identify("serial", "SRV1").Identify("name", "srv1").Set("role", "server")
	b.Object("cpu", "cpu").Identify("name", "Proc 1").In("srv").Set("model", "Intel(R) Xeon(R) CPU E5-2695 v4 @ 2.10GHz")
	b.Object("dimm", "memory").Identify("name", "DIMM A1").Identify("serial", "D1").In("srv").Set("size_bytes", int64(17179869184)).Set("technology", "DDR4")
	b.Object("disk", "disk").Identify("name", "1I:0:1").Identify("serial", "S1").In("srv").Set("media", "ssd").Set("capacity_bytes", int64(500107837440)).Set("model", "CT500MX500SSD1")
	b.Object("psu", "psu").Identify("name", "PS 0").Identify("serial", "P1").In("srv").Set("capacity_w", 800).Set("model", "865414-B21")
	return b
}

func displays(t *testing.T, e *reconcile.Engine) map[string]string {
	t.Helper()
	rows, err := e.Store.Pool.Query(context.Background(), `SELECT type, display FROM objects`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ty, d string
		if err := rows.Scan(&ty, &d); err != nil {
			t.Fatal(err)
		}
		out[ty] = d
	}
	return out
}

// A component is named after its slot and what it is, as soon as it is written.
func TestComponentDisplayNames(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{})
	apply(t, e, hardwareBatch(c))
	got := displays(t, e)
	for typ, want := range map[string]string{
		"device": "srv1",
		"cpu":    "Proc 1 · Intel Xeon E5-2695 v4 @ 2.10GHz",
		"memory": "DIMM A1 · DDR4 16 GB",
		"disk":   "1I:0:1 · SSD 500 GB CT500MX500SSD1",
		"psu":    "PS 0 · 800 W 865414-B21",
	} {
		if got[typ] != want {
			t.Errorf("%s displays %q, want %q", typ, got[typ], want)
		}
	}
}

// Objects stored under the old rule take the new one at start, once.
func TestRecomputeDisplays(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	apply(t, e, hardwareBatch(c))
	// Put the database back as it was before templates: raw names, no rule recorded.
	if _, err := s.Pool.Exec(ctx, `UPDATE objects SET display = attrs->>'name', search_text = '' WHERE type IN ('cpu', 'memory', 'disk', 'psu')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM display_rules`); err != nil {
		t.Fatal(err)
	}
	if got := displays(t, e); got["cpu"] != "Proc 1" {
		t.Fatalf("setup: cpu displays %q", got["cpu"])
	}
	defer reconcile.SetDisplayBatch(1)() // one object per transaction: the paging is exercised
	n, err := e.RecomputeDisplays(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("recomputed %d objects, want 4 (the device keeps its rule)", n)
	}
	got := displays(t, e)
	if got["cpu"] != "Proc 1 · Intel Xeon E5-2695 v4 @ 2.10GHz" || got["memory"] != "DIMM A1 · DDR4 16 GB" || got["device"] != "srv1" {
		t.Errorf("after recompute: %v", got)
	}
	var found int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM objects WHERE type = 'cpu' AND search_text LIKE '%Proc 1%'`).Scan(&found); err != nil || found != 1 {
		t.Errorf("search text not rebuilt: %d, %v", found, err)
	}
	// Second start: nothing to do, even if a display was edited by hand (the rule is recorded).
	if _, err := s.Pool.Exec(ctx, `UPDATE objects SET display = 'kept' WHERE type = 'cpu'`); err != nil {
		t.Fatal(err)
	}
	n, err = e.RecomputeDisplays(ctx, nil)
	if err != nil || n != 0 {
		t.Fatalf("second run: %d, %v", n, err)
	}
	if got := displays(t, e); got["cpu"] != "kept" {
		t.Errorf("the second run rewrote a display: %v", got)
	}
	// A new rule for a type is applied on the next run.
	reg := schema.Builtin()
	reg.Types["cpu"].DisplayTemplate = "{model|cpu}"
	e2 := &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }}
	n, err = e2.RecomputeDisplays(ctx, nil)
	if err != nil || n != 1 {
		t.Fatalf("changed rule: %d, %v", n, err)
	}
	if got := displays(t, e); got["cpu"] != "Intel Xeon E5-2695 v4 @ 2.10GHz" {
		t.Errorf("new rule not applied: %v", got)
	}
}
