package graph

import (
	"errors"
	"testing"
	"time"
)

// states flattens a result to id -> "state/rule".
func states(r *ImpactResult) map[string]string {
	m := map[string]string{}
	for _, a := range r.Affected {
		m[a.ID] = a.State + "/" + a.Rule
	}
	return m
}

func TestImpactPropagation(t *testing.T) {
	cases := []struct {
		name   string
		w      *world
		failed []string
		opt    ImpactOptions
		want   map[string]string // must be present, with that state/rule
		spared []string          // must not be affected
	}{
		{
			name:   "a parent takes its children down",
			w:      lab(),
			failed: []string{"sw2"},
			want:   map[string]string{"sw2/e1": "down/parent", "sw2/e2": "down/parent"},
			spared: []string{"srv-dual", "sw1"},
		},
		{
			name:   "hosts, runs_on and depends_on carry the failure, calls only degrades",
			w:      lab(),
			failed: []string{"srv-single"},
			want: map[string]string{
				"srv-single/e1": "down/parent",
				"vm1":           "down/hosts",
				"db":            "down/runs_on",
				"app":           "down/depends_on",
				"front":         "degraded/calls",
			},
			spared: []string{"report", "srv-dual", "sw2"},
		},
		{
			name:   "one feed of a redundant supply is not enough",
			w:      lab(),
			failed: []string{"pdu-a"},
			want:   map[string]string{"pdu-a/o1": "down/parent", "srv-dual/psu1": "down/powered_by"},
			spared: []string{"srv-dual", "srv-dual/psu2"},
		},
		{
			name:   "both feeds down take the device down through its power supplies",
			w:      lab(),
			failed: []string{"pdu-a", "pdu-b"},
			want:   map[string]string{"srv-dual": "down/power", "srv-dual/e1": "down/parent"},
		},
		{
			name:   "a server cut from every anchor is unreachable; a dual-homed one is not",
			w:      lab(),
			failed: []string{"sw2"},
			want:   map[string]string{"srv-single": "down/unreachable", "srv-single/e2": "down/parent", "vm1": "down/hosts", "app": "down/depends_on"},
			spared: []string{"srv-dual", "srv-lonely"},
		},
		{
			name:   "an uncabled device is never counted as cut",
			w:      lab(),
			failed: []string{"fw1"},
			want:   map[string]string{"sw1": "down/unreachable", "srv-dual": "down/unreachable"},
			spared: []string{"srv-lonely", "pdu-a"},
		},
		{
			name:   "explicit anchors replace the default ones",
			w:      lab(),
			failed: []string{"sw1"},
			opt:    ImpactOptions{Anchors: []string{"srv-single", "missing"}},
			want:   map[string]string{"fw1": "down/unreachable", "fw1/e1": "down/unreachable"},
			spared: []string{"srv-dual", "srv-dual/e1", "sw2"},
		},
		{
			name:   "absent objects are never reported",
			w:      lab().absent("vm1"),
			failed: []string{"srv-single"},
			want:   map[string]string{"srv-single/e1": "down/parent"},
			spared: []string{"vm1", "db", "app"},
		},
		{
			name: "down beats degraded when both apply",
			w: lab().
				add("both", "application", "", nil).
				rel("calls", "both", "db").rel("depends_on", "both", "db"),
			failed: []string{"db"},
			want:   map[string]string{"both": "down/depends_on"},
		},
		{
			name: "a supply is lost only when every supplier of that relation type is",
			w: lab().
				add("pod-a", "pod", "", nil).add("pod-b", "pod", "", nil).add("wl", "workload", "", nil).
				rel("served_by", "wl", "pod-a").rel("served_by", "wl", "pod-b"),
			failed: []string{"pod-a"},
			spared: []string{"wl"},
		},
		{
			name: "every replica gone takes the workload down",
			w: lab().
				add("pod-a", "pod", "", nil).add("pod-b", "pod", "", nil).add("wl", "workload", "", nil).
				rel("served_by", "wl", "pod-a").rel("served_by", "wl", "pod-b"),
			failed: []string{"pod-a", "pod-b"},
			want:   map[string]string{"wl": "down/served_by"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tc.w.snapshot().Impact(tc.failed, tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			got := states(r)
			for id, want := range tc.want {
				if got[id] != want {
					t.Errorf("%s: got %q, want %q", id, got[id], want)
				}
			}
			for _, id := range tc.spared {
				if s, ok := got[id]; ok {
					t.Errorf("%s: should be spared, got %q", id, s)
				}
			}
			for _, id := range tc.failed {
				if _, ok := got[id]; ok {
					t.Errorf("%s: a failed object is not listed as affected", id)
				}
			}
		})
	}
}

func TestImpactResultShape(t *testing.T) {
	r, err := lab().snapshot().Impact([]string{"srv-single", "db"}, ImpactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Failed) != 2 || r.Failed[0] != "db" {
		t.Errorf("failed list: %v", r.Failed)
	}
	if r.Down["vm"] != 1 || r.Down["application"] != 1 || r.Degraded["application"] != 1 {
		t.Errorf("counts: down %v degraded %v", r.Down, r.Degraded)
	}
	for i := 1; i < len(r.Affected); i++ {
		if r.Affected[i].Depth < r.Affected[i-1].Depth {
			t.Fatalf("not sorted by depth: %+v", r.Affected)
		}
	}
	byID := map[string]Affected{}
	for _, a := range r.Affected {
		byID[a.ID] = a
	}
	if a := byID["vm1"]; a.Cause != "srv-single" || a.Depth != 1 {
		t.Errorf("vm1: %+v", a)
	}
	if a := byID["app"]; a.Cause != "db" || a.Depth != 1 {
		t.Errorf("app: %+v", a)
	}
}

func TestImpactUnreachableComesAfterDirectEffects(t *testing.T) {
	r, err := lab().snapshot().Impact([]string{"sw2"}, ImpactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var parent, cut int
	for _, a := range r.Affected {
		switch a.ID {
		case "sw2/e1":
			parent = a.Depth
		case "srv-single":
			cut = a.Depth
		}
	}
	if cut <= parent {
		t.Errorf("unreachable depth %d should follow the direct effects (%d)", cut, parent)
	}
}

func TestImpactWithoutAnchorsSkipsReachability(t *testing.T) {
	w := &world{}
	w.device("sw", "switch", "e1").device("srv", "server", "e1").cable("sw/e1", "srv/e1")
	r, err := w.snapshot().Impact([]string{"sw"}, ImpactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := states(r)["srv"]; ok {
		t.Error("with no anchor, nothing can be declared unreachable")
	}
}

func TestImpactUnknownObject(t *testing.T) {
	_, err := lab().snapshot().Impact([]string{"nope"}, ImpactOptions{})
	if !errors.Is(err, ErrUnknownObject) {
		t.Fatalf("got %v", err)
	}
}

// TestImpactOnLargeSite checks the acceptance target of the specification:
// impact on 2 000 servers in 50 racks answers in under 2 seconds.
func TestImpactOnLargeSite(t *testing.T) {
	s := site(50, 40).snapshot()
	start := time.Now()
	r, err := s.Impact([]string{"core1", "r3-tor-a", "r7-pdu-a"}, ImpactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("impact took %s, the target is 2s", d)
	}
	got := states(r)
	if _, ok := got["r3-s0"]; ok {
		t.Error("a dual-homed, dual-fed server survives one switch and one PDU")
	}
	if got["r7-s0/psu1"] != "down/powered_by" {
		t.Errorf("psu on the failed PDU: %q", got["r7-s0/psu1"])
	}

	r, err = s.Impact([]string{"core1", "core2"}, ImpactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Down["vm"] != 2000 {
		t.Errorf("losing both cores cuts every server: %d VMs down", r.Down["vm"])
	}
}

func BenchmarkImpact(b *testing.B) {
	s := site(50, 40).snapshot()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Impact([]string{"core1", "r3-tor-a"}, ImpactOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
