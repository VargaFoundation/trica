package main

import (
	"testing"

	"github.com/VargaFoundation/trica/internal/topomap"
)

// The browser test of the device uplink (web/e2e/topology-map.spec.ts) counts
// on this: one uplinked_to edge between the sites, and one on the paris-1
// floor (srv-f001 to spine-01; leaf-01 to spine-01 is covered by its cable).
func TestMockMapDrawsTheDeviceUplinks(t *testing.T) {
	a := newAPI(func(string) bool { return true }).(*api)
	ix := topomap.IndexOf(a.mapSnapshot())
	for _, tc := range []struct{ level, id string }{
		{topomap.LevelGlobal, ""},
		{topomap.LevelRoom, oid(idSite)},
	} {
		m, err := ix.View(tc.level, tc.id, topomap.Options{})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range m.Edges {
			if e.Medium == topomap.MediumUplinkedTo {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s %s: %d uplinked_to edges, want 1", tc.level, tc.id, n)
		}
	}
}

// The rack level of r12 is the fixture of the UniFi-like look (web/e2e/topology-look.spec.ts):
// a firewall with a model at the top, two switches, two servers, an aggregate
// of two 40G links with live traffic, and speeds of several classes.
func TestMockMapRackHasATreeWithAnAggregateAndLiveTraffic(t *testing.T) {
	a := newAPI(func(string) bool { return true }).(*api)
	m, err := topomap.IndexOf(a.mapSnapshot()).View(topomap.LevelRack, oid(idRack), topomap.Options{Live: a.mapLive()})
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, n := range m.Nodes {
		if n.Kind == topomap.KindDevice {
			roles[n.Display] = n.Role + "/" + n.Model
		}
	}
	if roles["fw-paris"] != "firewall/ex-fw-100" || roles["sw-a1"] != "switch/ex-switch-48" || roles["srv-05"] != "server/ex-server" {
		t.Errorf("devices of r12 = %v", roles)
	}
	var fw *topomap.MapEdge
	for i, e := range m.Edges {
		if e.Medium == topomap.MediumPhysical && e.Links == 2 && e.CapacityMbps == 80000 {
			fw = &m.Edges[i]
		}
	}
	if fw == nil || !fw.Measured || fw.AToBBps == nil || fw.Util == nil || len(fw.Members) != 2 {
		t.Fatalf("no measured 2 x 40G aggregate in %+v", m.Edges)
	}
	speeds := map[float64]bool{}
	for _, e := range m.Edges {
		if e.Medium == topomap.MediumPhysical {
			speeds[e.CapacityMbps] = true
		}
	}
	if len(speeds) < 3 {
		t.Errorf("the rack shows %d capacities, the look needs several: %v", len(speeds), speeds)
	}
}
