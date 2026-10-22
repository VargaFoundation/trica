package topomap

import (
	"testing"

	"github.com/VargaFoundation/trica/internal/live"
)

// tunnelLab is lab() with a WireGuard tunnel on fw-a toward peer.
func tunnelLab(peer string) *world {
	w := lab()
	w.objs[idx(w, "fw-p")].Attrs["name"] = "fw-p"
	w.add("tun", "tunnel", "", map[string]any{"kind": "wireguard", "peer": peer, "status": "up"})
	w.rel("contains", "fw-a", "tun")
	return w
}

func TestTunnelPeerResolution(t *testing.T) {
	for _, tc := range []struct {
		name     string
		peer     string
		tweak    func(*world)
		to       string // the node the tunnel edge goes to at the global level, "" for none
		resolved bool
	}{
		{name: "the management address", peer: "10.9.0.1", to: "S2", resolved: true},
		{name: "address and port", peer: "10.9.0.1:51820", to: "S2", resolved: true},
		{name: "bracketed with a prefix", peer: "[10.9.0.1]:51820", to: "S2", resolved: true},
		{name: "a host name by its short name", peer: "FW-P.example.net", to: "S2", resolved: true},
		{name: "an address assigned to an interface", peer: "203.0.113.7", resolved: true, to: "S2", tweak: func(w *world) {
			w.add("ip1", "ip_address", "", map[string]any{"address": "203.0.113.7"})
			w.rel("assigned_to", "ip1", "fw-p/wan")
		}},
		{name: "a public address of an Internet access", peer: "198.51.100.9", resolved: true, to: "S2", tweak: func(w *world) {
			w.add("ia", "internet_access", "", map[string]any{"public_ips": []any{"198.51.100.9"}})
			w.rel("routes_via", "fw-p", "ia")
		}},
		{name: "two devices share the address", peer: "10.9.0.1", to: UnplacedID, tweak: func(w *world) {
			w.dev("twin", "R2", map[string]any{"mgmt_ip": "10.9.0.1"})
		}, resolved: false},
		{name: "unknown peer", peer: "vpn.example.org", to: InternetID},
		{name: "no peer", peer: "", to: InternetID},
		{name: "the peer is the owner itself", peer: "10.1.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := tunnelLab(tc.peer)
			if tc.tweak != nil {
				tc.tweak(w)
			}
			to := tc.to
			if to == UnplacedID { // ambiguity falls back to the Internet
				to = InternetID
			}
			m := run(t, w, LevelGlobal, "", nil)
			var tun *MapEdge
			for i := range m.Edges {
				if m.Edges[i].Medium == MediumTunnel {
					tun = &m.Edges[i]
				}
			}
			if to == "" {
				if tun != nil {
					t.Fatalf("a tunnel to itself must not be drawn: %+v", tun)
				}
				return
			}
			if tun == nil {
				t.Fatalf("no tunnel edge in %+v", m.Edges)
			}
			if got := tun.A + "|" + tun.B; got != canon("S1", to) {
				t.Errorf("tunnel joins %s, want %s", got, canon("S1", to))
			}
			if tun.Tunnel.Resolved != tc.resolved || tun.Tunnel.Kind != "wireguard" || tun.Tunnel.Status != "up" {
				t.Errorf("tunnel = %+v, want resolved=%v wireguard up", tun.Tunnel, tc.resolved)
			}
			if !tc.resolved && tun.Tunnel.Peer != tc.peer {
				t.Errorf("an unresolved tunnel states its peer %q, got %q", tc.peer, tun.Tunnel.Peer)
			}
			if tc.resolved && tun.Tunnel.Peer != "" {
				t.Errorf("a resolved tunnel does not repeat its peer, got %q", tun.Tunnel.Peer)
			}
			if got := m.Notes.UnresolvedTunnels; (got == 1) == tc.resolved {
				t.Errorf("notes.unresolved_tunnels = %d, resolved=%v", got, tc.resolved)
			}
			if tun.Measured || tun.AToBBps != nil {
				t.Errorf("a tunnel's rate is not measured in v1: %+v", tun)
			}
		})
	}
}

func canon(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func TestSeveralTunnelsOnOnePairAreOneEdge(t *testing.T) {
	w := tunnelLab("10.9.0.1")
	w.add("tun2", "tunnel", "", map[string]any{"kind": "ipsec", "peer": "fw-p", "status": "down"})
	w.rel("contains", "fw-a", "tun2")
	e := mustEdge(t, run(t, w, LevelGlobal, "", nil), "S1", "S2", MediumTunnel)
	if e.Links != 2 || e.Kinds.Tunnel != 2 || e.Tunnel.Kind != "mixed" || e.Tunnel.Status != "partial" || e.Oper != "partial" {
		t.Errorf("edge = %+v tunnel = %+v", e, e.Tunnel)
	}
}

// uplinkLab: fw-a routes via a gateway that itself routes via an access.
func uplinkLab() *world {
	w := lab()
	w.add("gw", "gateway", "", map[string]any{"address": "198.51.100.1", "status": "online", "latency_ms": 12.0, "loss_pct": 0.5})
	w.add("ia", "internet_access", "", map[string]any{"provider": "Orange", "bandwidth_mbps": 1000.0, "status": "online"})
	w.rel("contains", "fw-a", "gw").rel("routes_via", "fw-a", "gw").rel("routes_via", "gw", "ia")
	return w
}

func TestUplinkFromRoutesVia(t *testing.T) {
	m := run(t, uplinkLab(), LevelGlobal, "", nil)
	e := mustEdge(t, m, "S1", InternetID, MediumUplink)
	if e.Links != 1 || e.Kinds.Uplink != 1 {
		t.Fatalf("a gateway that routes via an access folds into it: links = %d, want 1", e.Links)
	}
	if e.CapacityMbps != 1000 || e.CapacityPartial {
		t.Errorf("capacity = %v partial = %v", e.CapacityMbps, e.CapacityPartial)
	}
	u := e.Uplink
	if u == nil || u.Status != "online" || u.LatencyMS != 12 || u.LossPct != 0.5 || u.Provider != "Orange" || u.BandwidthMbps != 1000 {
		t.Errorf("uplink = %+v", u)
	}
	if e.Oper != "up" || e.Confidence != "declared" {
		t.Errorf("oper = %s confidence = %s", e.Oper, e.Confidence)
	}
	if n := mustNode(t, m, InternetID); n.Kind != KindInternet {
		t.Errorf("internet = %+v", n)
	}

	t.Run("seen from a room the Internet is a portal", func(t *testing.T) {
		m := run(t, uplinkLab(), LevelRoom, "A1", nil)
		mustEdge(t, m, "R1", "portal:internet", MediumUplink)
		if p := mustNode(t, m, "portal:internet"); p.Kind != KindPortal || p.PlaceType != InternetID || p.Drill != nil {
			t.Errorf("portal = %+v", p)
		}
	})
	t.Run("a gateway only contained by the firewall", func(t *testing.T) {
		w := lab()
		w.add("gw", "gateway", "", map[string]any{"status": "offline"})
		w.rel("contains", "fw-a", "gw")
		e := mustEdge(t, run(t, w, LevelGlobal, "", nil), "S1", InternetID, MediumUplink)
		if e.Oper != "down" || e.Uplink.Status != "offline" || !e.CapacityPartial {
			t.Errorf("edge = %+v uplink = %+v", e, e.Uplink)
		}
	})
	t.Run("an access routed directly", func(t *testing.T) {
		w := lab()
		w.add("ia", "internet_access", "", map[string]any{"bandwidth_mbps": 300.0})
		w.rel("routes_via", "fw-a", "ia")
		e := mustEdge(t, run(t, w, LevelGlobal, "", nil), "S1", InternetID, MediumUplink)
		if e.CapacityMbps != 300 || e.Uplink.Status != "unknown" || e.Oper != "unknown" {
			t.Errorf("edge = %+v uplink = %+v", e, e.Uplink)
		}
	})
	t.Run("several uplinks aggregate on one edge", func(t *testing.T) {
		w := uplinkLab()
		w.add("gw2", "gateway", "", map[string]any{"status": "degraded", "latency_ms": 40.0, "loss_pct": 2.0})
		w.add("ia2", "internet_access", "", map[string]any{"provider": "Free", "bandwidth_mbps": 500.0})
		w.rel("routes_via", "fw-a", "gw2").rel("routes_via", "gw2", "ia2")
		// the same access routed by a second device is counted once
		w.rel("routes_via", "srv", "ia")
		e := mustEdge(t, run(t, w, LevelGlobal, "", nil), "S1", InternetID, MediumUplink)
		if e.Links != 3 || e.CapacityMbps != 1500 {
			t.Errorf("links = %d capacity = %v, want 3 links and 1500 (ia counted once)", e.Links, e.CapacityMbps)
		}
		if u := e.Uplink; u.Status != "degraded" || u.LatencyMS != 40 || u.LossPct != 2 || u.Provider != "Free, Orange" || u.BandwidthMbps != 1500 {
			t.Errorf("uplink = %+v", u)
		}
	})
}

func TestWanInterfaceByPrefix(t *testing.T) {
	wan := func(w *world) {
		w.add("ip-wan", "ip_address", "", map[string]any{"address": "198.51.100.2", "prefix_length": 29.0})
		w.rel("assigned_to", "ip-wan", "fw-a/wan")
		w.add("ip-lan", "ip_address", "", map[string]any{"address": "10.1.0.1/24"})
		w.rel("assigned_to", "ip-lan", "fw-a/lan")
	}
	lv := map[string]live.Link{
		"fw-a/wan": {TX: rate(2e8), RX: rate(8e8), Source: "x"},
		"fw-a/lan": {TX: rate(5e9), RX: rate(5e9), Source: "x"},
	}
	w := uplinkLab()
	wan(w)
	e := mustEdge(t, run(t, w, LevelGlobal, "", lv), "S1", InternetID, MediumUplink)
	if !e.Measured || e.AToBBps == nil || *e.AToBBps != 2e8 || *e.BToABps != 8e8 {
		t.Fatalf("uplink rate = %v / %v, want the wan interface's tx and rx", e.AToBBps, e.BToABps)
	}
	if e.Util == nil || *e.Util != 0.8 {
		t.Errorf("util = %v, want 0.8 of the contracted 1000 Mb/s", e.Util)
	}

	t.Run("two interfaces in the prefix is not measured", func(t *testing.T) {
		w := uplinkLab()
		wan(w)
		w.add("ip-dup", "ip_address", "", map[string]any{"address": "198.51.100.3", "prefix_length": 29.0})
		w.rel("assigned_to", "ip-dup", "fw-a/lan")
		if e := mustEdge(t, run(t, w, LevelGlobal, "", lv), "S1", InternetID, MediumUplink); e.Measured {
			t.Errorf("an ambiguous WAN interface must not be measured: %+v", e)
		}
	})
	t.Run("no interface in the prefix is not measured", func(t *testing.T) {
		w := uplinkLab()
		if e := mustEdge(t, run(t, w, LevelGlobal, "", lv), "S1", InternetID, MediumUplink); e.Measured {
			t.Errorf("no address data: %+v", e)
		}
	})
	t.Run("rates follow the canonical edge orientation", func(t *testing.T) {
		w := &world{}
		w.add("zsite", "site", "", nil).add("zr", "rack", "zsite", nil)
		w.dev("zfw", "zr", nil, "wan")
		w.add("gw", "gateway", "", map[string]any{"address": "198.51.100.1"})
		w.rel("contains", "zfw", "gw").rel("routes_via", "zfw", "gw")
		w.add("ip", "ip_address", "", map[string]any{"address": "198.51.100.2", "prefix_length": 29.0})
		w.rel("assigned_to", "ip", "zfw/wan")
		e := mustEdge(t, run(t, w, LevelGlobal, "", map[string]live.Link{"zfw/wan": {TX: rate(1e6), RX: rate(9e6)}}), "zsite", InternetID, MediumUplink)
		if e.A != InternetID || *e.AToBBps != 9e6 || *e.BToABps != 1e6 {
			t.Errorf("edge %s~%s a_to_b %v b_to_a %v: internet (a) to the site is what the site receives", e.A, e.B, *e.AToBBps, *e.BToABps)
		}
	})
}

// lagLab adds a LAG po1 on sw1 and sw2 over the two cabled ports.
func lagLab() *world {
	w := lab()
	for _, d := range []string{"sw1", "sw2"} {
		w.add(d+"/po1", "interface", d, map[string]any{"kind": "lag", "speed_mbps": 20000.0})
		w.rel("member_of", d+"/e1", d+"/po1").rel("member_of", d+"/e2", d+"/po1")
	}
	return w
}

func TestLiveSumsPhysicalLinksNotAggregates(t *testing.T) {
	lv := map[string]live.Link{
		"sw1/e1":  {TX: rate(4e9), RX: rate(1e9), Oper: "up"},
		"sw1/e2":  {TX: rate(2e9), RX: rate(3e9), Oper: "up"},
		"sw1/po1": {TX: rate(6e9), RX: rate(4e9), Aggregated: true, Members: 2},
		"sw2/po1": {TX: rate(4e9), RX: rate(6e9), Aggregated: true, Members: 2},
	}
	w := lagLab()
	w.relc("neighbor_of", "sw1/po1", "sw2/po1", "observed", nil) // LLDP also sees the aggregate
	e := mustEdge(t, run(t, w, LevelRoom, "A1", lv), "R1", "R2", MediumPhysical)
	if e.Links != 2 {
		t.Fatalf("links = %d, want 2: the aggregate adjacency is the same traffic as its members", e.Links)
	}
	if *e.AToBBps != 6e9 || *e.BToABps != 4e9 {
		t.Errorf("rate = %v / %v, want 6e9 / 4e9 (the members' sum, once)", *e.AToBBps, *e.BToABps)
	}
	if e.Util == nil || *e.Util != 0.3 {
		t.Errorf("util = %v, want 6e9 over 20 Gb/s", e.Util)
	}

	t.Run("an aggregate adjacency alone is kept, with its aggregated rate", func(t *testing.T) {
		w := lagLab()
		w.rels = nil
		w.rel("mounted_in", "sw1", "R1").rel("mounted_in", "sw2", "R2")
		w.rel("member_of", "sw1/e1", "sw1/po1").rel("member_of", "sw2/e1", "sw2/po1")
		w.relc("neighbor_of", "sw1/po1", "sw2/po1", "observed", nil)
		e := mustEdge(t, run(t, w, LevelRoom, "A1", lv), "R1", "R2", MediumPhysical)
		if e.Links != 1 || *e.AToBBps != 6e9 {
			t.Errorf("links = %d rate = %v, want 1 link at the aggregated 6e9", e.Links, *e.AToBBps)
		}
	})
}

func TestLiveDirectionFollowsEdgeEnds(t *testing.T) {
	// Port ids sort the other way round than the racks: the link's a end is in
	// R2, the edge's a node is R1.
	build := func() *world {
		w := &world{}
		w.add("S", "site", "", nil).add("R1", "rack", "S", nil).add("R2", "rack", "S", nil)
		w.dev("zz", "R1", nil, "p").dev("aa", "R2", nil, "p")
		w.cable("zz/p", "aa/p")
		return w
	}
	for _, tc := range []struct {
		name string
		lv   map[string]live.Link
	}{
		{"measured at the R1 end", map[string]live.Link{"zz/p": {TX: rate(5e9), RX: rate(1e9)}}},
		{"measured at the R2 end only", map[string]live.Link{"aa/p": {TX: rate(1e9), RX: rate(5e9)}}},
		{"both ends: the first is used, not both summed", map[string]live.Link{"zz/p": {TX: rate(5e9), RX: rate(1e9)}, "aa/p": {TX: rate(1e9), RX: rate(5e9)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := mustEdge(t, run(t, build(), LevelRoom, "S", tc.lv), "R1", "R2", MediumPhysical)
			if e.A != "R1" || *e.AToBBps != 5e9 || *e.BToABps != 1e9 {
				t.Errorf("R1 to R2 = %v, R2 to R1 = %v, want 5e9 and 1e9", *e.AToBBps, *e.BToABps)
			}
		})
	}
	t.Run("unmeasured", func(t *testing.T) {
		e := mustEdge(t, run(t, build(), LevelRoom, "S", nil), "R1", "R2", MediumPhysical)
		if e.Measured || e.AToBBps != nil || e.Util != nil {
			t.Errorf("no live values, no rate: %+v", e)
		}
	})
}

func TestOperFromLiveThenInventory(t *testing.T) {
	w := lab()
	w.objs[idx(w, "sw1/e2")].Attrs["oper_status"] = "down"
	lv := map[string]live.Link{"sw1/e1": {Oper: "up"}}
	e := mustEdge(t, run(t, w, LevelRoom, "A1", lv), "R1", "R2", MediumPhysical)
	if e.Oper != "partial" {
		t.Errorf("one link up (live), one down (inventory): oper = %s, want partial", e.Oper)
	}
	lv = map[string]live.Link{"sw1/e1": {Oper: "down"}, "sw1/e2": {Oper: "down"}}
	if e := mustEdge(t, run(t, lab(), LevelRoom, "A1", lv), "R1", "R2", MediumPhysical); e.Oper != "down" {
		t.Errorf("oper = %s, want down", e.Oper)
	}
	if e := mustEdge(t, run(t, lab(), LevelRoom, "A1", nil), "R1", "R2", MediumPhysical); e.Oper != "unknown" {
		t.Errorf("oper = %s, want unknown", e.Oper)
	}
}
