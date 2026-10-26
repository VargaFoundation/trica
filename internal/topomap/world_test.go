package topomap

import (
	"fmt"
	"sort"
	"testing"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// world builds snapshots with readable ids: an object's id is its name.
type world struct {
	objs []*store.Object
	rels []store.Relation
}

func (w *world) add(id, typ, parent string, attrs map[string]any) *world {
	o := &store.Object{ID: id, Type: typ, Display: id, Status: "active", Attrs: attrs}
	if parent != "" {
		o.ParentID = &parent
	}
	w.objs = append(w.objs, o)
	return w
}

func (w *world) absent(id string) *world {
	for _, o := range w.objs {
		if o.ID == id {
			o.Status = graph.StatusAbsent
		}
	}
	return w
}

func (w *world) relc(typ, from, to, conf string, attrs map[string]any) *world {
	w.rels = append(w.rels, store.Relation{Type: typ, FromID: from, ToID: to, Confidence: conf, Attrs: attrs})
	return w
}

func (w *world) rel(typ, from, to string) *world { return w.relc(typ, from, to, "declared", nil) }

// dev adds a device mounted in rack (none when empty) with ports "<dev>/<p>"
// of 10 Gb/s.
func (w *world) dev(id, rack string, attrs map[string]any, ports ...string) *world {
	if attrs == nil {
		attrs = map[string]any{}
	}
	w.add(id, "device", "", attrs)
	if rack != "" {
		w.relc("mounted_in", id, rack, "declared", map[string]any{"position": 4.0, "face": "front"})
	}
	for _, p := range ports {
		w.add(id+"/"+p, "interface", id, map[string]any{"kind": "physical", "speed_mbps": 10000.0})
	}
	return w
}

func (w *world) cable(a, b string) *world { return w.relc("cabled_to", a, b, "declared", nil) }

func (w *world) snap() *graph.Snapshot { return graph.Build(w.objs, w.rels, schema.Builtin()) }

// lab is two sites. Nanterre S1 has building B1, room A1, row RW1 and racks R1
// and R2; Pereire S2 has room P1 and rack R3, and a NAS in the site itself.
func lab() *world {
	w := &world{}
	w.add("S1", "site", "", map[string]any{"name": "Nanterre"})
	w.add("B1", "building", "S1", nil)
	w.add("A1", "room", "B1", map[string]any{"width_mm": 6000.0, "depth_mm": 4800.0})
	w.add("RW1", "row", "A1", nil)
	w.add("R1", "rack", "RW1", map[string]any{"floor_x": 1.0, "floor_y": 1.0, "facing": "south", "u_height": 42.0})
	w.add("R2", "rack", "RW1", map[string]any{"floor_x": 3.0, "floor_y": 1.0, "facing": "south", "u_height": 42.0})
	w.add("S2", "site", "", map[string]any{"name": "Pereire"})
	w.add("P1", "room", "S2", nil)
	w.add("R3", "rack", "P1", nil)

	w.dev("sw1", "R1", map[string]any{"role": "switch", "model": "ex-switch-48", "health": "ok", "power_w": 100.0}, "e1", "e2", "e3")
	w.dev("sw2", "R2", map[string]any{"role": "switch", "health": "warning", "power_w": 120.0}, "e1", "e2")
	w.dev("srv", "R1", map[string]any{"role": "server", "health": "critical", "log_critical_count": 3.0}, "e1")
	w.dev("fw-a", "R1", map[string]any{"role": "firewall", "health": "ok", "mgmt_ip": "10.1.0.1"}, "wan", "lan")
	w.dev("fw-p", "R3", map[string]any{"role": "firewall", "health": "ok", "mgmt_ip": "10.9.0.1"}, "wan")
	w.dev("nas", "", map[string]any{"role": "nas", "health": "ok"}, "e1")
	w.rel("contains", "S2", "nas")
	w.dev("orphan", "", map[string]any{"role": "server"}, "e1")

	w.cable("sw1/e1", "sw2/e1")
	w.cable("sw1/e2", "sw2/e2")
	w.cable("srv/e1", "sw1/e3")
	return w
}

// run computes a level with every right and the given live values.
func run(t *testing.T, w *world, level, id string, lv map[string]live.Link) *TopologyMap {
	t.Helper()
	return runAllow(t, w, level, id, lv, nil)
}

func runAllow(t *testing.T, w *world, level, id string, lv map[string]live.Link, allow func(string, string) bool) *TopologyMap {
	t.Helper()
	m, err := newIndex(w.snap()).View(level, id, Options{Live: lv, Allow: allow})
	if err != nil {
		t.Fatalf("View(%s, %s): %v", level, id, err)
	}
	return m
}

func (m *TopologyMap) node(id string) *MapNode {
	for i := range m.Nodes {
		if m.Nodes[i].ID == id {
			return &m.Nodes[i]
		}
	}
	return nil
}

func (m *TopologyMap) edge(a, b, medium string) *MapEdge {
	if a > b {
		a, b = b, a
	}
	for i := range m.Edges {
		if m.Edges[i].A == a && m.Edges[i].B == b && m.Edges[i].Medium == medium {
			return &m.Edges[i]
		}
	}
	return nil
}

func rate(v float64) *float64 { return &v }

func ids(m *TopologyMap) []string {
	out := make([]string, len(m.Nodes))
	for i, n := range m.Nodes {
		out[i] = n.ID
	}
	sort.Strings(out)
	return out
}

func mustNode(t *testing.T, m *TopologyMap, id string) *MapNode {
	t.Helper()
	n := m.node(id)
	if n == nil {
		t.Fatalf("no node %s in %v", id, ids(m))
	}
	return n
}

func mustEdge(t *testing.T, m *TopologyMap, a, b, medium string) *MapEdge {
	t.Helper()
	e := m.edge(a, b, medium)
	if e == nil {
		t.Fatalf("no %s edge %s-%s in %+v", medium, a, b, m.Edges)
	}
	return e
}

var _ = fmt.Sprintf
