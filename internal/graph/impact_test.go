package graph

import (
	"errors"
	"math/rand"
	"reflect"
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
			name:   "hosts, runs_on and depends_on carry the failure, calls only degrades, and what depends on a degraded object is degraded",
			w:      lab(),
			failed: []string{"srv-single"},
			want: map[string]string{
				"srv-single/e1": "down/parent",
				"vm1":           "down/hosts",
				"db":            "down/runs_on",
				"app":           "down/depends_on",
				"front":         "degraded/calls",
				"report":        "degraded/depends_on",
			},
			spared: []string{"srv-dual", "sw2"},
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
			name: "a supply is lost only when every supplier of that relation type is; one lost degrades",
			w: lab().
				add("pod-a", "pod", "", nil).add("pod-b", "pod", "", nil).add("wl", "workload", "", nil).
				rel("served_by", "wl", "pod-a").rel("served_by", "wl", "pod-b"),
			failed: []string{"pod-a"},
			want:   map[string]string{"wl": "degraded/served_by"},
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
	if r.Down["vm"] != 1 || r.Down["application"] != 1 || r.Degraded["application"] != 2 {
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

// kubeApp is what the Kubernetes collector draws for an application: a node
// of a cluster hosts the pods, a workload is served by its pods, a component
// instance runs on the workload and sits under a component of the
// application (ADR 0069).
func kubeApp() *world {
	return (&world{}).
		add("cluster", "k8s_cluster", "", nil).
		add("node-a", "k8s_node", "cluster", nil).add("node-b", "k8s_node", "cluster", nil).
		add("ns", "namespace", "cluster", nil).
		add("wl", "workload", "ns", nil).
		add("pod-a", "pod", "ns", nil).add("pod-b", "pod", "ns", nil).
		rel("hosts", "node-a", "pod-a").rel("hosts", "node-b", "pod-b").
		rel("served_by", "wl", "pod-a").rel("served_by", "wl", "pod-b").
		add("app", "application", "", nil).
		add("comp", "component", "app", nil).
		add("inst", "component_instance", "comp", nil).
		rel("runs_on", "inst", "wl")
}

func TestImpactReachesApplicationInstances(t *testing.T) {
	cases := []struct {
		name   string
		failed []string
		want   map[string]string
		spared []string
	}{
		{
			name:   "a cluster down takes its instances down, and the application whose instances all are",
			failed: []string{"cluster"},
			want:   map[string]string{"node-a": "down/parent", "pod-a": "down/hosts", "wl": "down/parent", "inst": "down/runs_on", "app": "down/instances"},
		},
		{
			name:   "every node down takes the instance and its application down",
			failed: []string{"node-a", "node-b"},
			want:   map[string]string{"wl": "down/served_by", "inst": "down/runs_on", "app": "down/instances"},
		},
		{
			name:   "one node of two: a pod lost, the instance and the application degraded",
			failed: []string{"node-a"},
			want:   map[string]string{"pod-a": "down/hosts", "wl": "degraded/served_by", "inst": "degraded/runs_on", "app": "degraded/instances"},
			spared: []string{"pod-b", "node-b"},
		},
		{
			name:   "every pod lost but a node is up: Kubernetes starts them again, degraded",
			failed: []string{"pod-a", "pod-b"},
			want:   map[string]string{"wl": "degraded/rescheduled", "inst": "degraded/runs_on", "app": "degraded/instances"},
		},
		{
			name:   "the last node failing after the pods turns rescheduled into down",
			failed: []string{"pod-a", "pod-b", "node-a", "node-b"},
			want:   map[string]string{"wl": "down/served_by", "inst": "down/runs_on", "app": "down/instances"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := kubeApp().snapshot().Impact(tc.failed, ImpactOptions{})
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
		})
	}
}

// TestImpactApplicationWithAnInstanceLeft: an application with a healthy
// instance elsewhere (another environment) is degraded, not down, when the
// other one goes down.
func TestImpactApplicationWithAnInstanceLeft(t *testing.T) {
	w := kubeApp().
		add("inst-dev", "component_instance", "comp", nil).
		add("other-wl", "workload", "", nil).
		rel("runs_on", "inst-dev", "other-wl")
	r, err := w.snapshot().Impact([]string{"cluster"}, ImpactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := states(r)
	if got["inst"] != "down/runs_on" || got["app"] != "degraded/instances" {
		t.Errorf("inst %q, app %q; want down/runs_on and degraded/instances", got["inst"], got["app"])
	}
	if _, hit := got["inst-dev"]; hit {
		t.Errorf("the other instance is affected: %q", got["inst-dev"])
	}
}

// capWorld is a Kubernetes cluster whose nodes each have 4 cores and 8 GB
// allocatable, for the capacity rule. Nodes are given by id.
func capWorld(nodes ...string) *world {
	w := (&world{}).add("cluster", "k8s_cluster", "", nil).add("ns", "namespace", "cluster", nil)
	for _, n := range nodes {
		w.add(n, "k8s_node", "cluster", map[string]any{"cpu_capacity": 4.0, "memory_capacity_bytes": int64(8e9)})
	}
	return w
}

// node overrides the attributes of a node of the world.
func (w *world) node(id string, attrs map[string]any) *world {
	for _, o := range w.objs {
		if o.ID == id {
			o.Attrs = attrs
		}
	}
	return w
}

// workload adds a workload of the namespace, optionally of a kind.
func (w *world) workload(id, kind string) *world {
	var attrs map[string]any
	if kind != "" {
		attrs = map[string]any{"kind": kind}
	}
	return w.add(id, "workload", "ns", attrs)
}

// pod adds a pod of a workload, running on a node, with its requests
// (cpu in cores, memory in bytes).
func (w *world) pod(id, workload, node string, cpu float64, mem int64) *world {
	return w.add(id, "pod", "ns", map[string]any{"cpu_request": cpu, "memory_request_bytes": mem}).
		rel("hosts", node, id).rel("served_by", workload, id)
}

// instance adds an application with one component, whose instances run on the
// given workloads.
func (w *world) application(id string, workloads ...string) *world {
	w.add(id, "application", "", nil).add(id+"/c", "component", id, nil)
	for _, wl := range workloads {
		i := id + "/i-" + wl
		w.add(i, "component_instance", id+"/c", nil).rel("runs_on", i, wl)
	}
	return w
}

func impactStates(t *testing.T, w *world, failed ...string) map[string]string {
	t.Helper()
	r, err := w.snapshot().Impact(failed, ImpactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return states(r)
}

func wantStates(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: got %q, want %q", id, got[id], w)
		}
	}
}

// One node of three fails and its pods fit on the other two: Kubernetes
// starts them again, the workloads are degraded.
func TestCapacityRescheduledWhenPodsFit(t *testing.T) {
	w := capWorld("n1", "n2", "n3").
		workload("wl-a", "").pod("a1", "wl-a", "n1", 1, 1e9).pod("a2", "wl-a", "n1", 1, 1e9).
		workload("wl-b", "").pod("b1", "wl-b", "n1", 2, 2e9).
		application("app", "wl-a", "wl-b")
	got := impactStates(t, w, "n1")
	wantStates(t, got, map[string]string{
		"wl-a": "degraded/rescheduled", "wl-b": "degraded/rescheduled",
		"app": "degraded/instances",
	})
}

// Two nodes of three fail and what they ran does not fit on the last one:
// the workloads with no pod placed are down, those with a pod placed stay
// degraded, and the applications follow their instances.
func TestCapacityDownWhenNothingFits(t *testing.T) {
	w := capWorld("n1", "n2", "n3").
		workload("wl-big", "").pod("big1", "wl-big", "n1", 3, 1e9).
		workload("wl-big2", "").pod("big2", "wl-big2", "n2", 3, 1e9).
		workload("wl-small", "").pod("small1", "wl-small", "n1", 1, 1e9).
		workload("wl-stay", "").pod("stay1", "wl-stay", "n3", 2, 1e9).
		application("only-big", "wl-big").
		application("big-and-small", "wl-big", "wl-small").
		application("both-big", "wl-big", "wl-big2")
	// n3 has 2 cores free: the 3-core pods find no room, the 1-core one does.
	got := impactStates(t, w, "n1", "n2")
	wantStates(t, got, map[string]string{
		"wl-big": "down/capacity", "wl-big2": "down/capacity",
		"wl-small":      "degraded/rescheduled",
		"wl-stay":       "",
		"only-big":      "down/instances",
		"both-big":      "down/instances",
		"big-and-small": "degraded/instances",
	})
}

// Memory is counted as CPU is: the pod fits by CPU and not by memory.
func TestCapacityMemoryCounts(t *testing.T) {
	w := capWorld("n1", "n2").
		workload("wl", "").pod("p", "wl", "n1", 1, 9e9)
	wantStates(t, impactStates(t, w, "n1"), map[string]string{"wl": "down/capacity"})
	w = capWorld("n1", "n2").
		workload("wl", "").pod("p", "wl", "n1", 1, 8e9)
	wantStates(t, impactStates(t, w, "n1"), map[string]string{"wl": "degraded/rescheduled"})
}

// A workload with two down pods of which only one fits is degraded, not down.
func TestCapacityPartialFitIsDegraded(t *testing.T) {
	w := capWorld("n1", "n2").
		workload("wl", "").pod("p1", "wl", "n1", 3, 1e9).pod("p2", "wl", "n1", 3, 1e9)
	wantStates(t, impactStates(t, w, "n1"), map[string]string{"wl": "degraded/rescheduled"})
}

// A survivor that does not report its allocatable CPU and memory leaves the
// capacity unknown: the rule does not apply.
func TestCapacityUnknownIsRescheduled(t *testing.T) {
	for name, attrs := range map[string]map[string]any{
		"no attributes": nil,
		"cpu only":      {"cpu_capacity": 4.0},
		"memory only":   {"memory_capacity_bytes": int64(8e9)},
	} {
		t.Run(name, func(t *testing.T) {
			w := capWorld("n1", "n2").node("n2", attrs).
				workload("wl", "").pod("p", "wl", "n1", 100, 1e12)
			wantStates(t, impactStates(t, w, "n1"), map[string]string{"wl": "degraded/rescheduled"})
		})
	}
}

// A cordoned survivor takes no pod, and one that is not ready does not either.
func TestCapacityCordonedSurvivorIsNotCounted(t *testing.T) {
	roomy := map[string]any{"cpu_capacity": 4.0, "memory_capacity_bytes": int64(8e9)}
	cases := []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"schedulable", map[string]any{"unschedulable": false}, "degraded/rescheduled"},
		{"cordoned", map[string]any{"unschedulable": true}, "down/capacity"},
		{"not ready", map[string]any{"ready": false}, "down/capacity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := map[string]any{}
			for k, v := range roomy {
				attrs[k] = v
			}
			for k, v := range tc.extra {
				attrs[k] = v
			}
			// n2 is the one to test; n3 is a survivor with 2 cores left.
			w := capWorld("n1", "n2", "n3").node("n2", attrs).
				workload("wl", "").pod("p", "wl", "n1", 3, 1e9).
				workload("filler", "").pod("f", "filler", "n3", 2, 1e9)
			wantStates(t, impactStates(t, w, "n1"), map[string]string{"wl": tc.want})
		})
	}
}

// The pods of a daemonset are not moved: they take no room from the others,
// and the daemonset itself is left as it was.
func TestCapacityDaemonSetTakesNoRoom(t *testing.T) {
	w := capWorld("n1", "n2").
		workload("agent", "daemonset").pod("ag", "agent", "n1", 4, 1e9).
		workload("web", "deployment").pod("wp", "web", "n1", 3, 1e9)
	wantStates(t, impactStates(t, w, "n1"), map[string]string{
		"agent": "degraded/rescheduled",
		"web":   "degraded/rescheduled",
	})
}

// A pod with no request attributes counts as asking for nothing, down or
// running.
func TestCapacityMissingRequestsCountZero(t *testing.T) {
	w := capWorld("n1", "n2").
		workload("wl", "").
		add("bare", "pod", "ns", nil).rel("hosts", "n1", "bare").rel("served_by", "wl", "bare").
		workload("other", "").
		add("bare2", "pod", "ns", nil).rel("hosts", "n2", "bare2").rel("served_by", "other", "bare2")
	wantStates(t, impactStates(t, w, "n1"), map[string]string{"wl": "degraded/rescheduled"})
}

// The result does not depend on the order objects and relations come in, nor
// on how many times it is computed.
func TestCapacityIsDeterministic(t *testing.T) {
	build := func() *world {
		// Equal pods and equal nodes everywhere: every tie is a tie.
		w := capWorld("n1", "n2", "n3", "n4")
		for i := 0; i < 6; i++ {
			wl := "wl-" + string(rune('a'+i))
			w.workload(wl, "").pod("p"+wl, wl, "n1", 1.5, 1e9).pod("q"+wl, wl, "n2", 1.5, 1e9)
		}
		return w.application("app", "wl-a", "wl-b", "wl-c", "wl-d", "wl-e", "wl-f")
	}
	ref := impactStates(t, build(), "n1", "n2")
	downs := 0
	for _, s := range ref {
		if s == "down/capacity" {
			downs++
		}
	}
	// 12 pods of 1.5 cores on 2 nodes of 4: 4 are placed, 8 are not.
	if downs == 0 || downs == 6 {
		t.Fatalf("the scenario must split the workloads: %d down of 6 (%v)", downs, ref)
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 40; i++ {
		w := build()
		rng.Shuffle(len(w.objs), func(a, b int) { w.objs[a], w.objs[b] = w.objs[b], w.objs[a] })
		rng.Shuffle(len(w.rels), func(a, b int) { w.rels[a], w.rels[b] = w.rels[b], w.rels[a] })
		if got := impactStates(t, w, "n2", "n1"); !reflect.DeepEqual(got, ref) {
			t.Fatalf("run %d differs:\n got %v\nwant %v", i, got, ref)
		}
	}
}
