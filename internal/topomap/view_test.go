package topomap

import (
	"fmt"
	"testing"

	"github.com/VargaFoundation/trica/internal/live"
)

func TestGlobalNodesCountsAndEdges(t *testing.T) {
	// A cable between the two sites, a tunnel to the Internet.
	w := lab()
	w.cable("fw-a/wan", "fw-p/wan")
	m := run(t, w, LevelGlobal, "", nil)
	if got, want := fmt.Sprint(ids(m)), "[S1 S2 unplaced]"; got != want {
		t.Fatalf("nodes = %s, want %s", got, want)
	}
	s1 := mustNode(t, m, "S1")
	if c := s1.Counts; c.Rooms != 1 || c.Racks != 2 || c.Devices != 4 {
		t.Errorf("S1 counts = %+v, want 1 room, 2 racks, 4 devices", c)
	}
	if s1.Health.Worst != "critical" || s1.Health.Critical != 1 || s1.Health.Warning != 1 || s1.Health.CriticalEvents != 3 {
		t.Errorf("S1 health = %+v", s1.Health)
	}
	if s1.PowerW == nil || *s1.PowerW != 220 {
		t.Errorf("S1 power = %v, want 220", s1.PowerW)
	}
	if s1.Drill == nil || s1.Drill.Level != LevelSite || s1.Drill.ID != "S1" {
		t.Errorf("S1 drill = %+v", s1.Drill)
	}
	if u := mustNode(t, m, UnplacedID); u.Counts.Devices != 1 || u.Drill.Level != LevelUnplaced {
		t.Errorf("unplaced = %+v", u)
	}
	if e := mustEdge(t, m, "S1", "S2", MediumPhysical); e.Links != 1 {
		t.Errorf("S1-S2 links = %d, want 1", e.Links)
	}
	if m.Notes.UnplacedDevices != 1 {
		t.Errorf("unplaced devices = %d, want 1", m.Notes.UnplacedDevices)
	}
}

func TestSiteLevelRoomsAndAreaAndPortals(t *testing.T) {
	w := lab()
	w.dev("floor-sw", "", nil, "e1")
	w.rel("contains", "S1", "floor-sw")
	w.cable("floor-sw/e1", "sw1/e1x") // a dangling name: ignored
	w.cable("floor-sw/e1", "sw2/e2")
	w.cable("nas/e1", "sw1/e3")
	m := run(t, w, LevelSite, "S1", nil)
	if got, want := fmt.Sprint(ids(m)), "[A1 area:S1 portal:S2]"; got != want {
		t.Fatalf("nodes = %s, want %s", got, want)
	}
	a1 := mustNode(t, m, "A1")
	if a1.Group == nil || a1.Group.ID != "B1" || a1.Group.Kind != "building" {
		t.Errorf("A1 group = %+v, want building B1", a1.Group)
	}
	if a1.Size == nil || a1.Size.WidthMM != 6000 {
		t.Errorf("A1 size = %+v", a1.Size)
	}
	if ar := mustNode(t, m, "area:S1"); ar.Kind != KindArea || ar.Drill.Level != LevelRoom || ar.Drill.ID != "S1" || ar.Counts.Devices != 1 {
		t.Errorf("area = %+v", ar)
	}
	if e := mustEdge(t, m, "A1", "portal:S2", MediumPhysical); e.Links != 1 {
		t.Errorf("A1-portal:S2 = %+v", e)
	}
	mustEdge(t, m, "A1", "area:S1", MediumPhysical)
	if m.Focus == nil || m.Focus.ID != "S1" || len(m.Trail) != 0 {
		t.Errorf("focus = %+v, trail = %v", m.Focus, m.Trail)
	}
}

func TestRoomLevelRacksFloorAndTrail(t *testing.T) {
	m := run(t, lab(), LevelRoom, "A1", nil)
	if got, want := fmt.Sprint(ids(m)), "[R1 R2]"; got != want {
		t.Fatalf("nodes = %s, want %s", got, want)
	}
	r1 := mustNode(t, m, "R1")
	if r1.Floor == nil || r1.Floor.X != 1 || r1.Floor.Y != 1 || r1.Floor.Facing != "south" {
		t.Errorf("R1 floor = %+v", r1.Floor)
	}
	if r1.Group == nil || r1.Group.ID != "RW1" || r1.Group.Kind != "row" {
		t.Errorf("R1 group = %+v", r1.Group)
	}
	if r1.Size == nil || r1.Size.UHeight != 42 || r1.Counts.Devices != 3 {
		t.Errorf("R1 = %+v", r1)
	}
	if m.Focus.WidthMM != 6000 || m.Focus.DepthMM != 4800 || m.Focus.TileMM != 600 {
		t.Errorf("focus floor plan = %+v", m.Focus)
	}
	if len(m.Trail) != 1 || m.Trail[0].ID != "S1" {
		t.Errorf("trail = %+v, want the site", m.Trail)
	}
	if e := mustEdge(t, m, "R1", "R2", MediumPhysical); e.Links != 2 || e.Kinds.CabledTo != 2 {
		t.Errorf("R1-R2 = %+v, want 2 cabled_to links", e)
	}
}

func TestSiteFloorLevelHoldsRacksAndDevicesOutsideRooms(t *testing.T) {
	w := lab()
	w.add("R4", "rack", "S2", nil)
	w.dev("in-r4", "R4", nil, "e1")
	w.cable("in-r4/e1", "fw-p/wan")
	w.cable("nas/e1", "fw-p/wan")
	m := run(t, w, LevelRoom, "S2", nil)
	if got, want := fmt.Sprint(ids(m)), "[R4 nas portal:P1]"; got != want {
		t.Fatalf("nodes = %s, want %s", got, want)
	}
	if m.Focus.Type != "site" || len(m.Trail) != 0 {
		t.Errorf("focus = %+v trail = %v", m.Focus, m.Trail)
	}
	mustEdge(t, m, "R4", "portal:P1", MediumPhysical)
	mustEdge(t, m, "nas", "portal:P1", MediumPhysical)
}

func TestRackLevelDevicesMountAndTrail(t *testing.T) {
	w := lab()
	w.add("dt", "device_type", "", map[string]any{"u_height": 2.0, "full_depth": true})
	w.rel("instance_of", "sw1", "dt")
	m := run(t, w, LevelRack, "R1", nil)
	if got, want := fmt.Sprint(ids(m)), "[fw-a portal:R2 srv sw1]"; got != want {
		t.Fatalf("nodes = %s, want %s", got, want)
	}
	sw := mustNode(t, m, "sw1")
	if sw.Mount == nil || sw.Mount.Position != 4 || sw.Mount.Face != "front" || sw.Mount.UHeight != 2 || !sw.Mount.FullDepth {
		t.Errorf("sw1 mount = %+v", sw.Mount)
	}
	if sw.Role != "switch" || sw.Health.Worst != "ok" {
		t.Errorf("sw1 = %+v", sw)
	}
	if len(m.Trail) != 2 || m.Trail[0].ID != "S1" || m.Trail[1].ID != "A1" {
		t.Errorf("trail = %+v, want site then room", m.Trail)
	}
	mustEdge(t, m, "srv", "sw1", MediumPhysical)
	e := mustEdge(t, m, "portal:R2", "sw1", MediumPhysical)
	if e.Links != 2 {
		t.Errorf("sw1 to R2 links = %d, want 2", e.Links)
	}
	if mustNode(t, m, "portal:R2").Drill.Level != LevelRack {
		t.Errorf("a rack portal drills to the rack")
	}
}

func TestUnplacedLevel(t *testing.T) {
	w := lab()
	w.cable("orphan/e1", "sw1/e3x")
	w.cable("orphan/e1", "nas/e1")
	m := run(t, w, LevelUnplaced, "", nil)
	if got, want := fmt.Sprint(ids(m)), "[orphan portal:S2]"; got != want {
		t.Fatalf("nodes = %s, want %s", got, want)
	}
	mustEdge(t, m, "orphan", "portal:S2", MediumPhysical)
}

func TestPortalIsLowestCommonAncestorChild(t *testing.T) {
	w := lab()
	w.add("A2", "room", "B1", nil)
	w.add("R5", "rack", "A2", nil)
	w.add("R6", "rack", "S1", nil) // a rack directly in the site
	w.dev("in-r5", "R5", nil, "e1")
	w.dev("in-r6", "R6", nil, "e1")
	w.dev("sitedev", "", nil, "e1")
	w.rel("contains", "S1", "sitedev")
	w.cable("sw1/e1", "in-r5/e1")
	w.cable("sw1/e2", "in-r6/e1")
	w.cable("sw1/e3", "sitedev/e1")
	w.cable("sw2/e1", "fw-p/wan")
	w.cable("sw2/e2", "orphan/e1")
	for _, tc := range []struct {
		level, id string
		portals   map[string]*MapDrill // portal id -> drill
	}{
		{LevelRack, "R1", map[string]*MapDrill{
			"portal:R2": {Level: LevelRack, ID: "R2"}, // same room, same row
			"portal:A2": {Level: LevelRoom, ID: "A2"}, // another room
			"portal:R6": {Level: LevelRack, ID: "R6"}, // a rack on the site floor
			"portal:S1": {Level: LevelRoom, ID: "S1"}, // a device on the site floor
		}},
		{LevelRoom, "A1", map[string]*MapDrill{
			"portal:A2":       {Level: LevelRoom, ID: "A2"},
			"portal:R6":       {Level: LevelRack, ID: "R6"},
			"portal:S1":       {Level: LevelRoom, ID: "S1"},
			"portal:S2":       {Level: LevelSite, ID: "S2"}, // another site
			"portal:unplaced": {Level: LevelUnplaced},
		}},
		{LevelSite, "S1", map[string]*MapDrill{
			"portal:S2":       {Level: LevelSite, ID: "S2"},
			"portal:unplaced": {Level: LevelUnplaced},
		}},
	} {
		t.Run(tc.level+" "+tc.id, func(t *testing.T) {
			m := run(t, w, tc.level, tc.id, nil)
			got := map[string]*MapDrill{}
			for _, n := range m.Nodes {
				if n.Kind == KindPortal {
					got[n.ID] = n.Drill
				}
			}
			for id, want := range tc.portals {
				d, ok := got[id]
				if !ok {
					t.Errorf("no portal %s among %v", id, ids(m))
					continue
				}
				if *d != *want {
					t.Errorf("portal %s drills to %+v, want %+v", id, d, want)
				}
			}
			if len(got) != len(tc.portals) {
				t.Errorf("portals = %v, want %d of them", ids(m), len(tc.portals))
			}
		})
	}
}

func TestInternalLinksNotDrawn(t *testing.T) {
	m := run(t, lab(), LevelRoom, "A1", nil)
	r1 := mustNode(t, m, "R1")
	if r1.Counts.InternalLinks != 1 {
		t.Errorf("R1 internal links = %d, want 1 (srv-sw1)", r1.Counts.InternalLinks)
	}
	for _, e := range m.Edges {
		if e.A == e.B || (e.A == "R1" && e.B == "R1") {
			t.Errorf("a link inside one node was drawn: %+v", e)
		}
	}
	// Both ends outside the level: neither a node nor an edge.
	site := run(t, lab(), LevelSite, "S2", nil)
	for _, e := range site.Edges {
		t.Errorf("S2 has no link leaving a room, got %+v", e)
	}
}

func TestDuplicateAdjacencyCountsOnce(t *testing.T) {
	w := lab()
	w.cable("sw2/e1", "sw1/e1") // the same pair, said from the other side
	w.relc("neighbor_of", "sw1/e1", "sw2/e1", "observed", nil)
	w.relc("neighbor_of", "sw2/e1", "sw1/e1", "observed", nil)
	m := run(t, w, LevelRoom, "A1", nil)
	e := mustEdge(t, m, "R1", "R2", MediumPhysical)
	if e.Links != 2 { // e1 (said four ways) and e2
		t.Fatalf("links = %d, want 2: the same port pair must count once", e.Links)
	}
	if e.Kinds.CabledTo != 2 || e.Kinds.NeighborOf != 0 {
		t.Errorf("kinds = %+v, a cable beats a neighbour", e.Kinds)
	}
	if e.Confidence != "declared" {
		t.Errorf("confidence = %s, want declared (the strongest relation of each pair)", e.Confidence)
	}
}

func TestCableObjectWithTwoEndsIsALink(t *testing.T) {
	w := lab()
	w.add("c1", "cable", "", nil)
	w.relc("connects", "c1", "sw2/e1", "observed", map[string]any{"end": "b"})
	w.relc("connects", "c1", "sw1/e1", "declared", map[string]any{"end": "a"})
	w.add("c3", "cable", "", nil) // a cable with one end is not a link
	w.rel("connects", "c3", "srv/e1")
	w.add("c4", "cable", "", nil) // a cable with three ends is not a link either
	w.rel("connects", "c4", "sw1/e2").rel("connects", "c4", "sw2/e2").rel("connects", "c4", "sw1/e3")
	var cables int
	for _, l := range newIndex(w.snap()).links {
		if l.kind == "cable" {
			cables++
			if l.a != "sw1/e1" || l.b != "sw2/e1" {
				t.Errorf("unexpected cable link %+v", l)
			}
		}
	}
	// sw1/e1-sw2/e1 is also cabled_to: the cable has priority over it.
	if cables != 1 {
		t.Errorf("cable links = %d, want 1", cables)
	}
	m := run(t, w, LevelRoom, "A1", nil)
	if e := mustEdge(t, m, "R1", "R2", MediumPhysical); e.Kinds.Cable != 1 || e.Kinds.CabledTo != 1 || e.Links != 2 {
		t.Errorf("kinds = %+v links = %d", e.Kinds, e.Links)
	}
}

func TestCapacityIsMinOfEnds(t *testing.T) {
	w := lab()
	w.objs[idx(w, "sw1/e1")].Attrs["speed_mbps"] = 1000.0
	m := run(t, w, LevelRoom, "A1", nil)
	e := mustEdge(t, m, "R1", "R2", MediumPhysical)
	if e.CapacityMbps != 11000 { // min(1000,10000) + 10000
		t.Errorf("capacity = %v, want 11000", e.CapacityMbps)
	}
	if e.CapacityPartial {
		t.Errorf("every link has a known speed: not partial")
	}
}

func TestCapacityPartial(t *testing.T) {
	w := lab()
	delete(w.objs[idx(w, "sw1/e1")].Attrs, "speed_mbps")
	delete(w.objs[idx(w, "sw2/e1")].Attrs, "speed_mbps")
	m := run(t, w, LevelRoom, "A1", nil)
	e := mustEdge(t, m, "R1", "R2", MediumPhysical)
	if e.CapacityMbps != 10000 || !e.CapacityPartial {
		t.Errorf("capacity = %v partial = %v, want 10000 and partial", e.CapacityMbps, e.CapacityPartial)
	}
	w2 := lab()
	delete(w2.objs[idx(w2, "sw1/e1")].Attrs, "speed_mbps") // one end only: the other end's speed
	if e := mustEdge(t, run(t, w2, LevelRoom, "A1", nil), "R1", "R2", MediumPhysical); e.CapacityMbps != 20000 || e.CapacityPartial {
		t.Errorf("one known end: capacity = %v partial = %v, want 20000", e.CapacityMbps, e.CapacityPartial)
	}
}

func idx(w *world, id string) int {
	for i, o := range w.objs {
		if o.ID == id {
			return i
		}
	}
	panic("no object " + id)
}

func TestConfidenceIsTheWeakestLink(t *testing.T) {
	w := lab()
	w.rels = w.rels[:0]
	w.relc("cabled_to", "sw1/e1", "sw2/e1", "declared", nil)
	w.relc("cabled_to", "sw1/e2", "sw2/e2", "deduced", nil)
	w.rel("mounted_in", "sw1", "R1").rel("mounted_in", "sw2", "R2")
	e := mustEdge(t, run(t, w, LevelRoom, "A1", nil), "R1", "R2", MediumPhysical)
	if e.Confidence != "deduced" {
		t.Errorf("confidence = %s, want deduced", e.Confidence)
	}
}

func TestMembersAreCappedAndOrientedLikeTheEdge(t *testing.T) {
	w := &world{}
	w.add("S", "site", "", nil).add("K1", "rack", "S", nil).add("K2", "rack", "S", nil)
	w.dev("zz", "K1", nil)
	w.dev("aa", "K2", nil)
	for i := 0; i < MaxMembers+5; i++ {
		p := fmt.Sprintf("p%03d", i)
		w.add("zz/"+p, "interface", "zz", map[string]any{"kind": "physical"})
		w.add("aa/"+p, "interface", "aa", map[string]any{"kind": "physical"})
		w.cable("zz/"+p, "aa/"+p)
	}
	m := run(t, w, LevelRoom, "S", nil)
	e := mustEdge(t, m, "K1", "K2", MediumPhysical)
	if e.Links != MaxMembers+5 || len(e.Members) != MaxMembers || !e.MembersTruncated {
		t.Fatalf("links %d members %d truncated %v", e.Links, len(e.Members), e.MembersTruncated)
	}
	// Edge A is K1 (zz): members read a_device = the K1 side.
	if e.Members[0].ADevice.ID != "zz" || e.Members[0].BDevice.ID != "aa" {
		t.Errorf("member = %+v, want zz first (the edge's a side)", e.Members[0])
	}
	_ = live.Link{}
}
