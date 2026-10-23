package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/topomap"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func fp(v float64) *float64 { return &v }

// mapFixture adds to the base fixtures a map worth drawing: in Paris a room
// holding two racks, two switches joined by two 10G cables that are also a
// LAG, a firewall with a WireGuard tunnel to a firewall in Lyon and an
// Internet uplink, and a NAS in no rack. Documentation addresses only.
func (e *env) mapFixture() {
	b := sdk.NewBatch("declaration:map", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("paris", "site").Identify("code", "PAR1")
	b.Object("lyon", "site").Identify("code", "LYO1")
	b.Object("rack-l", "rack").Identify("name", "rack-b").In("lyon")
	b.Object("room", "room").Identify("name", "A1").In("paris").Set("width_mm", 6000).Set("depth_mm", 4800)
	b.Object("r1", "rack").Identify("name", "R1").In("room").Set("floor_x", 1).Set("floor_y", 1).Set("facing", "south")
	b.Object("r2", "rack").Identify("name", "R2").In("room").Set("floor_x", 3).Set("floor_y", 1).Set("facing", "south")
	for _, d := range []struct{ ref, name, ip, rack string }{
		{"swa", "sw-r1", "192.0.2.31", "r1"}, {"swb", "sw-r2", "192.0.2.32", "r2"}, {"fwa", "fw-a", "192.0.2.50", "r1"},
	} {
		b.Object(d.ref, "device").Identify("name", d.name).Set("role", "switch").Set("mgmt_ip", d.ip)
		b.Relate("mounted_in", d.ref, d.rack).Attributes["position"] = 10
	}
	b.Object("fwp", "device").Identify("name", "fw-p").Set("role", "firewall").Set("mgmt_ip", "198.51.100.9")
	b.Relate("mounted_in", "fwp", "rack-l")
	b.Object("nas", "device").Identify("name", "nas-1").Set("role", "storage")
	for _, s := range []string{"swa", "swb"} {
		for _, p := range []string{"Ethernet1", "Ethernet2"} {
			b.Object(s+p, "interface").Identify("name", p).In(s).Set("kind", "physical").Set("speed_mbps", 10000)
			b.Relate("member_of", s+p, s+"Po1")
		}
		b.Object(s+"Po1", "interface").Identify("name", "Port-Channel1").In(s).Set("kind", "lag").Set("speed_mbps", 20000)
	}
	b.Relate("cabled_to", "swaEthernet1", "swbEthernet1")
	b.Relate("cabled_to", "swaEthernet2", "swbEthernet2")
	b.Relate("cabled_to", "swaPo1", "swbPo1") // the same two cables seen as the aggregate
	b.Object("wg", "tunnel").Identify("name", "wg-paris-lyon").Set("kind", "wireguard").Set("peer", "198.51.100.9").Set("status", "up")
	b.Relate("contains", "fwa", "wg")
	b.Object("gw", "gateway").Identify("name", "gw-1").Set("address", "192.0.2.1").Set("status", "online")
	b.Object("isp", "internet_access").Identify("name", "isp-1").Set("provider", "Example ISP").Set("bandwidth_mbps", 1000)
	b.Relate("contains", "fwa", "gw")
	b.Relate("routes_via", "fwa", "gw")
	b.Relate("routes_via", "gw", "isp")
	// Device uplinks (ADR 0062): Lyon's firewall reaches the network through
	// Paris's second switch, no cable between them, so the map draws it; the
	// second switch through the first is covered by their cables, so it is not.
	b.Relate("uplinked_to", "fwp", "swb")
	b.Relate("uplinked_to", "swb", "swa")
	e.apply(b)
}

func (e *env) liveMap() fakeLive {
	m := map[string]live.Link{}
	set := func(name string, tx, rx float64) {
		m[e.ids[name]] = live.Link{TX: fp(tx), RX: fp(rx), Oper: "up", Source: "prom"}
	}
	set("swaEthernet1", 100e6, 10e6)
	set("swaEthernet2", 50e6, 5e6)
	set("swbEthernet1", 10e6, 100e6)
	set("swbEthernet2", 5e6, 50e6)
	// The aggregate repeats its members: it must never be added to them.
	m[e.ids["swaPo1"]] = live.Link{TX: fp(150e6), RX: fp(15e6), Oper: "up", Source: "prom", Aggregated: true, Members: 2}
	m[e.ids["swbPo1"]] = live.Link{TX: fp(15e6), RX: fp(150e6), Oper: "up", Source: "prom", Aggregated: true, Members: 2}
	g := t0
	return fakeLive{&live.Snapshot{GeneratedAt: &g, Refresh: 15 * time.Second, Links: m}}
}

func (e *env) topo(token, query string) (topomap.TopologyMap, resp) {
	e.t.Helper()
	r := e.get("/api/v1/topology/map?"+query, token)
	var m topomap.TopologyMap
	if r.Status == 200 {
		r.json(e.t, &m)
	}
	return m, r
}

func nodeIDs(m topomap.TopologyMap) []string {
	out := []string{}
	for _, n := range m.Nodes {
		out = append(out, n.ID)
	}
	sort.Strings(out)
	return out
}

func edgeByMedium(t *testing.T, m topomap.TopologyMap, medium string) topomap.MapEdge {
	t.Helper()
	for _, ed := range m.Edges {
		if ed.Medium == medium {
			return ed
		}
	}
	t.Fatalf("no %s edge in %v", medium, m.Edges)
	return topomap.MapEdge{}
}

func TestTopologyMapLevels(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.mapFixture()
	e.withLive(e.liveMap())
	ids := e.ids

	// Global: the sites, the Internet and what is in no site.
	m, _ := e.topo(tokAdmin, "level=global")
	want := []string{ids["lyon"], ids["paris"], topomap.InternetID, topomap.UnplacedID}
	sort.Strings(want)
	if got := nodeIDs(m); !equal(got, want) {
		t.Fatalf("global nodes = %v, want %v", got, want)
	}
	if !m.Live.Enabled || m.Live.RefreshSeconds != 15 {
		t.Fatalf("live = %+v", m.Live)
	}
	tun := edgeByMedium(t, m, topomap.MediumTunnel)
	if tun.Tunnel == nil || !tun.Tunnel.Resolved || tun.Tunnel.Status != "up" || tun.Measured {
		t.Fatalf("tunnel edge = %+v", tun)
	}
	if up := edgeByMedium(t, m, topomap.MediumUplink); up.Uplink == nil || up.Uplink.BandwidthMbps != 1000 {
		t.Fatalf("uplink edge = %+v", up)
	}
	ul := edgeByMedium(t, m, topomap.MediumUplinkedTo)
	towardParis := "b"
	if ul.A == ids["paris"] {
		towardParis = "a"
	}
	if ul.Links != 1 || ul.Kinds.UplinkedTo != 1 || ul.Toward != towardParis || ul.Measured || ul.Oper != "unknown" || len(ul.Members) != 1 || ul.Members[0].Toward != towardParis {
		t.Fatalf("uplinked_to edge = %+v, want one link toward Paris (%s), never measured", ul, towardParis)
	}

	// Site Paris: the room, the area outside rooms, a portal to Lyon and
	// the two cables of the room's inside are not drawn as links to itself.
	// The room's internal links stay 2: the uplink between its two racks'
	// switches is covered by those cables and must not be counted.
	m, _ = e.topo(tokAdmin, "level=site&id="+ids["paris"])
	if m.Focus == nil || m.Focus.ID != ids["paris"] {
		t.Fatalf("focus = %+v", m.Focus)
	}
	var sawRoom bool
	for _, n := range m.Nodes {
		if n.ID == ids["room"] && n.Kind == "room" && n.Counts.Racks == 2 && n.Counts.InternalLinks == 2 {
			sawRoom = true
		}
	}
	if !sawRoom {
		t.Fatalf("site nodes = %+v", m.Nodes)
	}

	// Room: two racks on the floor, one edge between them.
	m, _ = e.topo(tokAdmin, "level=room&id="+ids["room"])
	if got := nodeIDs(m); !equalSubset(got, ids["r1"], ids["r2"]) {
		t.Fatalf("room nodes = %v", got)
	}
	if m.Focus.WidthMM != 6000 || m.Focus.TileMM != 600 {
		t.Fatalf("room focus = %+v", m.Focus)
	}
	ed := edgeByMedium(t, m, topomap.MediumPhysical)
	// Three adjacencies (two cables, one aggregate) are two links, and the
	// rates are those of the two ports, not of the ports plus the aggregate.
	if ed.Links != 2 || ed.CapacityMbps != 20000 || !ed.Measured {
		t.Fatalf("rack edge = %+v", ed)
	}
	// The edge's a end is the lower id; R1's switch sends 150 Mb/s to R2's.
	r1ToR2, r2ToR1 := ed.AToBBps, ed.BToABps
	if ed.A == ids["r2"] {
		r1ToR2, r2ToR1 = r2ToR1, r1ToR2
	}
	if r1ToR2 == nil || *r1ToR2 != 150e6 || r2ToR1 == nil || *r2ToR1 != 15e6 {
		t.Fatalf("rack edge rates r1->r2 %v, r2->r1 %v", deref(r1ToR2), deref(r2ToR1))
	}
	// The covered uplink adds no edge between the racks, and the one to Lyon
	// is a portal edge.
	for _, ed := range m.Edges {
		if ed.Medium == topomap.MediumUplinkedTo && (ed.A != "portal:"+ids["lyon"] && ed.B != "portal:"+ids["lyon"]) {
			t.Fatalf("a covered uplink is drawn between the racks: %+v", ed)
		}
	}
	if pe := edgeByMedium(t, m, topomap.MediumUplinkedTo); pe.Links != 1 {
		t.Fatalf("room uplinked_to edge = %+v", pe)
	}

	// Rack R1: its devices, with the mount, and the switch link out as a portal.
	m, _ = e.topo(tokAdmin, "level=rack&id="+ids["r1"])
	var mounted bool
	for _, n := range m.Nodes {
		if n.ID == ids["swa"] && n.Mount != nil && n.Mount.Position == 10 {
			mounted = true
		}
	}
	if !mounted {
		t.Fatalf("rack nodes = %+v", m.Nodes)
	}

	// Unplaced: the NAS.
	m, _ = e.topo(tokAdmin, "level=unplaced")
	if got := nodeIDs(m); !equalSubset(got, ids["nas"]) {
		t.Fatalf("unplaced nodes = %v", got)
	}
}

func TestTopologyMapErrors(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.mapFixture()
	ids := e.ids
	for _, c := range []struct {
		name, q string
		status  int
	}{
		{"no level", "", 400},
		{"unknown level", "level=planet", 400},
		{"id not a uuid", "level=rack&id=nope", 400},
		{"global with an id", "level=global&id=" + ids["paris"], 400},
		{"site level on a rack", "level=site&id=" + ids["r1"], 400},
		{"site level without id", "level=site", 400},
		{"unknown place", "level=rack&id=00000000-0000-4000-8000-000000000000", 404},
	} {
		t.Run(c.name, func(t *testing.T) { wantStatus(t, e.get("/api/v1/topology/map?"+c.q, tokAdmin), c.status) })
	}
	wantProblem(t, e.get("/api/v1/topology/map?level=global", ""), 401, "unauthenticated")
	// A reader of devices only cannot open a rack.
	wantProblem(t, e.get("/api/v1/topology/map?level=rack&id="+ids["r1"], tokDevices), 403, "forbidden")
}

// A caller limited to one site sees that site and nothing of the others: no
// node, no portal to them, no Internet, no tunnel.
func TestTopologyMapSiteLimitedCaller(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.mapFixture()
	e.withLive(e.liveMap())
	ids := e.ids

	m, _ := e.topo(tokSiteParis, "level=global")
	for _, n := range m.Nodes {
		if n.ID == ids["lyon"] || n.ID == topomap.InternetID {
			t.Fatalf("site-limited caller sees %s", n.ID)
		}
	}
	if len(m.Edges) != 0 {
		t.Fatalf("site-limited caller sees edges %+v", m.Edges)
	}
	m, _ = e.topo(tokSiteParis, "level=site&id="+ids["paris"])
	for _, n := range m.Nodes {
		if n.Kind == "portal" || n.ID == topomap.InternetID {
			t.Fatalf("site-limited caller sees %+v", n)
		}
	}
	wantProblem(t, e.get("/api/v1/topology/map?level=site&id="+ids["lyon"], tokSiteParis), 403, "forbidden")
}

// The in-memory rights walk the map uses is the SQL one, object by object.
func TestAuthSiteEqualsSiteIDs(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.mapFixture()
	// Devices placed by a person (located_in, ADR 0058): an access point in a
	// room, a racked device also placed in another site (the rack wins), a
	// device placed in a building of one site and in another site by a second
	// source (two sites: none), and the NAS placed in Lyon.
	b := sdk.NewBatch("declaration:places", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("paris", "site").Identify("code", "PAR1")
	b.Object("room", "room").Identify("name", "A1").In("paris")
	b.Object("lyon", "site").Identify("code", "LYO1")
	b.Object("ap", "device").Identify("name", "ap-1").Set("role", "access_point")
	b.Relate("located_in", "ap", "room")
	b.Object("swa", "device").Identify("name", "sw-r1").Set("role", "switch")
	b.Relate("located_in", "swa", "lyon")
	b.Object("nas", "device").Identify("name", "nas-1").Set("role", "storage")
	b.Relate("located_in", "nas", "lyon")
	b.Object("cam", "device").Identify("name", "cam-1").Set("role", "camera")
	b.Relate("located_in", "cam", "room")
	e.apply(b)
	b = sdk.NewBatch("declaration:places-2", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("lyon", "site").Identify("code", "LYO1")
	b.Object("cam", "device").Identify("name", "cam-1").Set("role", "camera")
	b.Relate("located_in", "cam", "lyon")
	e.apply(b)
	snap, err := e.eng.Store.AllObjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rels, err := e.eng.Store.CurrentRelations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ix := topomap.IndexOf(graph.Build(snap, rels, e.reg.get()))
	all := make([]string, 0, len(snap))
	for _, o := range snap {
		all = append(all, o.ID)
	}
	sql, err := e.srv.SiteIDs(context.Background(), all)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range all {
		if got := ix.AuthSite(id); got != sql[id] {
			t.Errorf("AuthSite(%s) = %q, SQL = %q", id, got, sql[id])
		}
	}
	// The worlds above must exercise the rule, not agree on emptiness.
	for ref, want := range map[string]string{"ap": "paris", "swa": "paris", "nas": "lyon", "cam": ""} {
		if sql[e.ids[ref]] != e.ids[want] {
			t.Errorf("site of %s = %q, want %q (%s)", ref, sql[e.ids[ref]], e.ids[want], want)
		}
	}
}

// flipToward swaps a and b of a toward, "both" and "" staying as they are.
func flipToward(s string) string {
	switch s {
	case "a":
		return "b"
	case "b":
		return "a"
	}
	return s
}

// TestTopologyMapRecordedForTheUI writes (TRICA_UPDATE_TOPOLOGY=1) and checks
// the answers the UI's tests replay, so the web and the server cannot drift.
func TestTopologyMapRecordedForTheUI(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.mapFixture()
	e.withLive(e.liveMap())
	ids := e.ids
	// Ids are random: name them by what they are, then put the edges in an
	// order and an orientation that do not depend on them.
	pairs := []string{}
	for ref, id := range ids {
		pairs = append(pairs, id, "<"+ref+">")
	}
	ren := strings.NewReplacer(pairs...)
	rec := map[string]topomap.TopologyMap{}
	for name, q := range map[string]string{
		"global": "level=global", "site": "level=site&id=" + ids["paris"], "room": "level=room&id=" + ids["room"],
		"rack": "level=rack&id=" + ids["r1"], "unplaced": "level=unplaced",
	} {
		r := e.get("/api/v1/topology/map?"+q, tokAdmin)
		wantStatus(t, r, 200)
		var m topomap.TopologyMap
		if err := json.Unmarshal([]byte(ren.Replace(string(r.Body))), &m); err != nil {
			t.Fatal(err)
		}
		m.InventoryAt = t0
		sort.Slice(m.Nodes, func(i, j int) bool {
			x, y := m.Nodes[i], m.Nodes[j]
			if x.Kind != y.Kind {
				return x.Kind < y.Kind
			}
			if x.Display != y.Display {
				return x.Display < y.Display
			}
			return x.ID < y.ID
		})
		for i := range m.Edges {
			ed := &m.Edges[i]
			if ed.A > ed.B {
				ed.A, ed.B = ed.B, ed.A
				ed.AToBBps, ed.BToABps = ed.BToABps, ed.AToBBps
				ed.Toward = flipToward(ed.Toward)
				for j := range ed.Members {
					mb := &ed.Members[j]
					mb.ADevice, mb.BDevice, mb.APort, mb.BPort = mb.BDevice, mb.ADevice, mb.BPort, mb.APort
					mb.Toward = flipToward(mb.Toward)
				}
			}
			ed.ID = ed.A + "~" + ed.B + "~" + ed.Medium
			sort.Slice(ed.Members, func(x, y int) bool { return ed.Members[x].APort.ID < ed.Members[y].APort.ID })
		}
		sort.Slice(m.Edges, func(i, j int) bool { return m.Edges[i].ID < m.Edges[j].ID })
		rec[name] = m
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	path := filepath.Join("..", "..", "web", "src", "topology", "map", "testdata", "map-api.json")
	if os.Getenv("TRICA_UPDATE_TOPOLOGY") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (record it with TRICA_UPDATE_TOPOLOGY=1)", err)
	}
	if string(have) != string(out) {
		t.Fatalf("%s drifted from the API: run the test with TRICA_UPDATE_TOPOLOGY=1 and review the diff", path)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalSubset: every wanted id is among got.
func equalSubset(got []string, want ...string) bool {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
