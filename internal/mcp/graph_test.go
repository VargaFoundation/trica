package mcp_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/VargaFoundation/trica/internal/mcp"
)

// impactOf builds a canned graph answer: the failure of sw-a1 takes down two
// servers and one switch in another site, and degrades one more server.
func (e *env) impactOf() *mcp.ImpactResult {
	aff := func(ref, typ, disp, state string, depth int) mcp.Affected {
		return mcp.Affected{ID: e.ids[ref], Type: typ, Display: disp, State: state, Rule: "cabled_to", Cause: e.ids["sw-a1"], Depth: depth}
	}
	return &mcp.ImpactResult{
		Failed: []string{e.ids["sw-a1"]},
		Affected: []mcp.Affected{
			aff("srv-02", "device", "srv-02", mcp.StateDegraded, 1),
			aff("srv-01", "device", "srv-01", mcp.StateDown, 2),
			aff("srv-03", "device", "srv-03", mcp.StateDown, 1),
			aff("sw-b1", "device", "sw-b1", mcp.StateDown, 3),
		},
		Down:     map[string]int{"device": 3},
		Degraded: map[string]int{"device": 1},
	}
}

func TestImpactAnalysis(t *testing.T) {
	e := newEnv(t, proposeMCP, true)
	e.graph.impact = e.impactOf()
	cs := e.connect(e.as(tokReader))

	t.Run("the counts come first, the down objects before the degraded", func(t *testing.T) {
		a := ok(t, cs, "impact_analysis", m{"objects": []string{"sw-a1"}})
		s := dig(t, a.Data, "summary")
		if num(t, s, "affected") != 4 || num(t, s, "down") != 3 || num(t, s, "degraded") != 1 || num(t, s, "down_by_type", "device") != 3 {
			t.Fatalf("summary %v", s)
		}
		if str(t, s, "failed", 0, "display") != "sw-a1" {
			t.Fatalf("failed %v", dig(t, s, "failed"))
		}
		if got := displays(t, a.Data, "items"); !reflect.DeepEqual(got, []string{"srv-03", "srv-01", "sw-b1", "srv-02"}) {
			t.Fatalf("items %v", got)
		}
		if str(t, a.Data, "items", 0, "state") != "down" || str(t, a.Data, "items", 3, "state") != "degraded" {
			t.Fatalf("states %v", dig(t, a.Data, "items"))
		}
	})
	t.Run("the graph is asked about ids, with the anchors", func(t *testing.T) {
		ok(t, cs, "impact_analysis", m{"objects": []string{"192.0.2.10"}, "anchors": []string{"sw-b1"}})
		e.graph.mu.Lock()
		defer e.graph.mu.Unlock()
		if !reflect.DeepEqual(e.graph.failed, []string{e.ids["sw-a1"]}) || !reflect.DeepEqual(e.graph.anchors, []string{e.ids["sw-b1"]}) {
			t.Fatalf("graph asked with %v / %v", e.graph.failed, e.graph.anchors)
		}
	})
	t.Run("the list can be narrowed, the summary still counts everything", func(t *testing.T) {
		a := ok(t, cs, "impact_analysis", m{"objects": []string{"sw-a1"}, "affected_types": []string{"application"}})
		if num(t, a.Data, "summary", "affected") != 4 || num(t, a.Data, "summary", "listed", "total") != 0 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
		b := ok(t, cs, "impact_analysis", m{"objects": []string{"sw-a1"}, "limit": 2})
		if num(t, b.Data, "summary", "listed", "returned") != 2 || dig(t, b.Data, "summary", "listed", "truncated") != true {
			t.Fatalf("page %v", dig(t, b.Data, "summary", "listed"))
		}
	})
	t.Run("a graph that does not know the object says so", func(t *testing.T) {
		e.graph.mu.Lock()
		e.graph.err = mcp.ErrUnknownObject
		e.graph.mu.Unlock()
		denied(t, cs, "impact_analysis", m{"objects": []string{"sw-a1"}}, "not-found")
		e.graph.mu.Lock()
		e.graph.err = errors.New("graph exploded")
		e.graph.mu.Unlock()
		a := call(t, cs, "impact_analysis", m{"objects": []string{"sw-a1"}})
		if !a.IsError {
			t.Fatal("an internal graph error was swallowed")
		}
		e.graph.mu.Lock()
		e.graph.err = nil
		e.graph.mu.Unlock()
	})
	t.Run("bad input is refused", func(t *testing.T) {
		denied(t, cs, "impact_analysis", m{"objects": []string{}}, "invalid-request")
		denied(t, cs, "impact_analysis", m{"objects": []string{"no-such-thing"}}, "not-found")
		denied(t, cs, "impact_analysis", m{"objects": []string{"sw-a1"}, "anchors": []string{"no-such-thing"}}, "not-found")
		tooMany := make([]string, 51)
		for i := range tooMany {
			tooMany[i] = "sw-a1"
		}
		denied(t, cs, "impact_analysis", m{"objects": tooMany}, "invalid-request")
	})
}

// What the caller may not read is not in the answer, nor in its counts.
func TestImpactAnalysisHidesWhatTheCallerCannotRead(t *testing.T) {
	e := newEnv(t, proposeMCP, true)
	e.graph.impact = e.impactOf()
	e.graph.impact.Affected = append(e.graph.impact.Affected, mcp.Affected{ID: e.ids["rack-2"], Type: "rack", Display: "rack-2", State: mcp.StateDown, Rule: "parent", Depth: 1})
	e.graph.impact.Down["rack"] = 1

	// frank reads devices only: the rack is invisible.
	a := ok(t, e.connect(e.as(tokDevices)), "impact_analysis", m{"objects": []string{"sw-a1"}})
	if num(t, a.Data, "summary", "affected") != 4 || num(t, a.Data, "summary", "down") != 3 {
		t.Fatalf("summary %v", dig(t, a.Data, "summary"))
	}
	if dig(t, a.Data, "summary", "down_by_type", "rack") != nil {
		t.Fatalf("a type the caller cannot read is counted: %v", dig(t, a.Data, "summary", "down_by_type"))
	}
	for _, d := range displays(t, a.Data, "items") {
		if d == "rack-2" {
			t.Fatal("an unreadable object is listed")
		}
	}
	// gina reads the site paris-1 only: sw-b1 (lyon) is invisible.
	// (a grant limited to a site designates objects by id: names need a search it may not run).
	b := ok(t, e.connect(e.as(tokSiteParis)), "impact_analysis", m{"objects": []string{e.ids["sw-a1"]}})
	for _, d := range displays(t, b.Data, "items") {
		if d == "sw-b1" {
			t.Fatal("an object of another site is listed")
		}
	}
	if num(t, b.Data, "summary", "affected") != 4 { // srv-01..03 + rack-2, all in paris-1
		t.Fatalf("summary %v", dig(t, b.Data, "summary"))
	}
	// A failed object the caller cannot read is refused before the graph is asked.
	e.graph.mu.Lock()
	e.graph.failed = nil
	e.graph.mu.Unlock()
	denied(t, e.connect(e.as(tokSiteParis)), "impact_analysis", m{"objects": []string{e.ids["sw-b1"]}}, "forbidden")
	e.graph.mu.Lock()
	defer e.graph.mu.Unlock()
	if e.graph.failed != nil {
		t.Fatalf("the graph was asked about an unreadable object: %v", e.graph.failed)
	}
}

func TestNetworkPath(t *testing.T) {
	e := newEnv(t, proposeMCP, true)
	node := func(ref, typ, disp string) *mcp.GraphNode {
		return &mcp.GraphNode{ID: e.ids[ref], Type: typ, Display: disp}
	}
	e.graph.path = []mcp.Hop{
		{Node: node("srv-01", "device", "srv-01")},
		{Node: node("srv-01/eth0", "interface", "eth0"), Depth: 1, Via: &mcp.GraphEdge{Type: "has_interface", From: e.ids["srv-01"], To: e.ids["srv-01/eth0"]}},
		{Node: node("sw-a1/Ethernet2", "interface", "Ethernet2"), Depth: 2, Via: &mcp.GraphEdge{Type: "cabled_to", From: e.ids["srv-01/eth0"], To: e.ids["sw-a1/Ethernet2"]}},
		{Node: node("sw-a1", "device", "sw-a1"), Depth: 3, Via: &mcp.GraphEdge{Type: "has_interface", From: e.ids["sw-a1"], To: e.ids["sw-a1/Ethernet2"]}},
	}
	cs := e.connect(e.as(tokReader))

	t.Run("the chain from one object to the other", func(t *testing.T) {
		a := ok(t, cs, "network_path", m{"from": "srv-01", "to": "192.0.2.10", "relation_types": []string{"cabled_to", "has_interface"}})
		if dig(t, a.Data, "summary", "found") != true || num(t, a.Data, "summary", "hops") != 3 ||
			str(t, a.Data, "summary", "from") != "srv-01" || str(t, a.Data, "summary", "to") != "sw-a1" {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
		if str(t, a.Data, "path", 2, "via", "type") != "cabled_to" || str(t, a.Data, "path", 3, "node", "display") != "sw-a1" {
			t.Fatalf("path %v", dig(t, a.Data, "path"))
		}
		e.graph.mu.Lock()
		defer e.graph.mu.Unlock()
		if e.graph.from != e.ids["srv-01"] || e.graph.to != e.ids["sw-a1"] || !reflect.DeepEqual(e.graph.rels, []string{"cabled_to", "has_interface"}) {
			t.Fatalf("graph asked %s -> %s %v", e.graph.from, e.graph.to, e.graph.rels)
		}
	})
	t.Run("no path is an answer, not an error", func(t *testing.T) {
		e.graph.mu.Lock()
		saved := e.graph.path
		e.graph.path = nil
		e.graph.mu.Unlock()
		defer func() { e.graph.mu.Lock(); e.graph.path = saved; e.graph.mu.Unlock() }()
		a := ok(t, cs, "network_path", m{"from": "srv-01", "to": "sw-b1"})
		if dig(t, a.Data, "summary", "found") != false || num(t, a.Data, "summary", "hops") != 0 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
		if p, ok := a.Data["path"].([]any); !ok || len(p) != 0 {
			t.Fatalf("path %v", a.Data["path"])
		}
	})
	t.Run("an object the graph does not know", func(t *testing.T) {
		e.graph.mu.Lock()
		e.graph.err = mcp.ErrUnknownObject
		e.graph.mu.Unlock()
		defer func() { e.graph.mu.Lock(); e.graph.err = nil; e.graph.mu.Unlock() }()
		denied(t, cs, "network_path", m{"from": "srv-01", "to": "sw-a1"}, "not-found")
	})
	t.Run("bad input is refused", func(t *testing.T) {
		denied(t, cs, "network_path", m{"from": "srv-01", "to": "nothing-here"}, "not-found")
		denied(t, cs, "network_path", m{"from": "srv-01", "to": "sw-a1", "relation_types": []string{"x OR y"}}, "invalid-request")
	})
	t.Run("a path through objects the caller cannot read is not shown, not even in part", func(t *testing.T) {
		// frank reads devices: the interfaces on the path are hidden from him.
		denied(t, e.connect(e.as(tokDevices)), "network_path", m{"from": "srv-01", "to": "sw-a1"}, "forbidden")
	})
}
