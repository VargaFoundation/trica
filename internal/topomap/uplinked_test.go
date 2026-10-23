package topomap

import (
	"testing"

	"github.com/VargaFoundation/trica/internal/live"
)

// uplinkedLab is lab() with three uplinked_to relations (ADR 0062): fw-p
// reaches the network through sw2 (S2 to S1), no port link between them;
// srv through sw1 and sw2 through sw1, both covered by cables.
func uplinkedLab() *world {
	w := lab()
	w.rel("uplinked_to", "fw-p", "sw2")
	w.rel("uplinked_to", "srv", "sw1")
	w.rel("uplinked_to", "sw2", "sw1")
	return w
}

func TestUplinkedToIsAnEdgeBetweenPlaces(t *testing.T) {
	w := uplinkedLab()
	t.Run("global", func(t *testing.T) {
		m := run(t, w, LevelGlobal, "", nil)
		e := mustEdge(t, m, "S1", "S2", MediumUplinkedTo)
		if e.ID != "S1~S2~uplinked_to" || e.Links != 1 || e.Kinds.UplinkedTo != 1 {
			t.Errorf("edge = %+v", e)
		}
		if e.Toward != "a" {
			t.Errorf("toward = %q, want a (S1 holds the uplink device)", e.Toward)
		}
		if e.Confidence != "declared" || e.Measured || e.Oper != "unknown" || e.CapacityMbps != 0 || e.CapacityPartial {
			t.Errorf("an uplink is declared, never measured, with no capacity: %+v", e)
		}
		if len(e.Members) != 1 {
			t.Fatalf("members = %+v", e.Members)
		}
		mb := e.Members[0]
		if mb.Kind != "uplinked_to" || mb.Toward != "a" || mb.ADevice.ID != "sw2" || mb.BDevice.ID != "fw-p" || mb.APort.ID != "" || mb.BPort.ID != "" {
			t.Errorf("member = %+v, want sw2 (a) <- fw-p (b), no port, toward a", mb)
		}
	})
	t.Run("room", func(t *testing.T) {
		m := run(t, w, LevelRoom, "A1", nil)
		e := mustEdge(t, m, "R2", "portal:S2", MediumUplinkedTo)
		if e.Toward != "a" {
			t.Errorf("toward = %q, want a (R2 holds sw2; edge ends are ordered by id)", e.Toward)
		}
	})
	t.Run("rack", func(t *testing.T) {
		m := run(t, w, LevelRack, "R2", nil)
		e := mustEdge(t, m, "sw2", "portal:S2", MediumUplinkedTo)
		if e.Toward != "b" || e.A != "portal:S2" {
			t.Errorf("edge = %+v, want portal:S2 as a and toward b", e)
		}
		if mb := e.Members[0]; mb.Toward != "b" || mb.ADevice.ID != "fw-p" || mb.BDevice.ID != "sw2" {
			t.Errorf("member = %+v must be oriented like the edge", mb)
		}
	})
}

func TestUplinkedToCoveredByAPortLinkIsNotDrawn(t *testing.T) {
	w := uplinkedLab()
	t.Run("room", func(t *testing.T) {
		m := run(t, w, LevelRoom, "A1", nil)
		if e := m.edge("R1", "R2", MediumUplinkedTo); e != nil {
			t.Errorf("an uplink covered by cables must not be drawn: %+v", e)
		}
		e := mustEdge(t, m, "R1", "R2", MediumPhysical)
		if e.Links != 2 || e.Kinds.UplinkedTo != 0 {
			t.Errorf("the cables stay the only links: %+v", e)
		}
	})
	t.Run("site", func(t *testing.T) {
		with := run(t, w, LevelSite, "S1", nil)
		without := run(t, lab(), LevelSite, "S1", nil)
		if a, b := mustNode(t, with, "A1").Counts.InternalLinks, mustNode(t, without, "A1").Counts.InternalLinks; a != b {
			t.Errorf("a covered uplink is counted: internal links %d, want %d", a, b)
		}
	})
	t.Run("rack", func(t *testing.T) {
		m := run(t, w, LevelRack, "R1", nil)
		for _, e := range m.Edges {
			if e.Medium == MediumUplinkedTo {
				t.Errorf("srv to sw1 is cabled: %+v", e)
			}
		}
		if n := mustNode(t, m, "sw1").Counts.InternalLinks; n != 0 {
			t.Errorf("internal links = %d", n)
		}
	})
}

func TestUplinkedToWithinOnePlaceCountsAsInternal(t *testing.T) {
	w := lab()
	w.rel("uplinked_to", "fw-a", "sw1") // same rack R1, no cable
	m := run(t, w, LevelRoom, "A1", nil)
	without := run(t, lab(), LevelRoom, "A1", nil)
	if a, b := mustNode(t, m, "R1").Counts.InternalLinks, mustNode(t, without, "R1").Counts.InternalLinks; a != b+1 {
		t.Errorf("internal links = %d, want %d", a, b+1)
	}
}

func TestUplinkedToBothWaysIsBoth(t *testing.T) {
	w := lab()
	w.rel("uplinked_to", "fw-a", "sw2").rel("uplinked_to", "sw2", "fw-a")
	m := run(t, w, LevelRoom, "A1", nil)
	e := mustEdge(t, m, "R1", "R2", MediumUplinkedTo)
	if e.Links != 2 || e.Toward != "both" || e.Kinds.UplinkedTo != 2 {
		t.Errorf("edge = %+v", e)
	}
	got := map[string]bool{}
	for _, mb := range e.Members {
		got[mb.Toward] = true
	}
	if !got["a"] || !got["b"] {
		t.Errorf("members = %+v, want one toward each end", e.Members)
	}
}

func TestUplinkedToIsNeverMeasured(t *testing.T) {
	lv := map[string]live.Link{}
	for _, id := range []string{"fw-p", "sw2", "fw-p/wan", "sw2/e1", "sw2/e2"} {
		lv[id] = live.Link{TX: rate(1e9), RX: rate(1e9), Oper: "up"}
	}
	m := run(t, uplinkedLab(), LevelGlobal, "", lv)
	e := mustEdge(t, m, "S1", "S2", MediumUplinkedTo)
	if e.Measured || e.AToBBps != nil || e.BToABps != nil || e.Util != nil || e.Oper != "unknown" {
		t.Errorf("an uplink has no live data: %+v", e)
	}
}

func TestUplinkedToSkipsBadEnds(t *testing.T) {
	w := lab()
	w.rel("uplinked_to", "fw-p", "sw2") // the source is absent
	w.rel("uplinked_to", "sw2", "sw1")  // the target is absent
	w.rel("uplinked_to", "srv", "srv")  // itself
	w.rel("uplinked_to", "srv", "srv/e1")
	w.rel("uplinked_to", "srv/e1", "fw-a")
	w.absent("fw-p").absent("sw1")
	ix := newIndex(w.snap())
	if len(ix.devUplinks) != 0 {
		t.Errorf("devUplinks = %+v, want none", ix.devUplinks)
	}
	m, err := ix.View(LevelGlobal, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Edges {
		if e.Medium == MediumUplinkedTo {
			t.Errorf("edge = %+v", e)
		}
	}
}
