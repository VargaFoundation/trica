package topomap

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/live"
)

func TestAllowFiltersNodesLinksAndPortals(t *testing.T) {
	w := uplinkLab()
	w.cable("fw-a/wan", "fw-p/wan")
	w.add("tun", "tunnel", "", map[string]any{"peer": "10.9.0.1"})
	w.rel("contains", "fw-a", "tun")
	siteOne := func(typ, site string) bool { return site == "S1" }

	t.Run("a caller of one site sees nothing of the other", func(t *testing.T) {
		m := runAllow(t, w, LevelGlobal, "", nil, siteOne)
		if got := fmt.Sprint(ids(m)); got != "[S1]" {
			t.Errorf("nodes = %s, want only S1 (not S2, not unplaced, not Internet)", got)
		}
		if len(m.Edges) != 0 {
			t.Errorf("edges = %+v: a link to a site the caller cannot read, a tunnel and an uplink must be hidden", m.Edges)
		}
		if m.Notes.UnplacedDevices != 0 || m.Notes.UnresolvedTunnels != 0 {
			t.Errorf("notes leak what the caller cannot read: %+v", m.Notes)
		}
		if n := mustNode(t, m, "S1"); n.Counts.Devices != 4 {
			t.Errorf("S1 devices = %d, want 4", n.Counts.Devices)
		}
	})
	t.Run("no portal to an unreadable place", func(t *testing.T) {
		for _, lvl := range [][2]string{{LevelRoom, "A1"}, {LevelRack, "R1"}, {LevelSite, "S1"}} {
			m := runAllow(t, w, lvl[0], lvl[1], nil, siteOne)
			for _, n := range m.Nodes {
				if n.Kind == KindPortal && (n.ID == "portal:S2" || n.ID == "portal:internet" || n.ID == "portal:unplaced") {
					t.Errorf("%s %s: portal %s shown to a caller of S1 only", lvl[0], lvl[1], n.ID)
				}
			}
		}
	})
	t.Run("everything readable shows it all", func(t *testing.T) {
		m := run(t, w, LevelGlobal, "", nil)
		mustEdge(t, m, "S1", "S2", MediumPhysical)
		mustEdge(t, m, "S1", "S2", MediumTunnel)
		mustEdge(t, m, "S1", InternetID, MediumUplink)
	})
	t.Run("a link needs both interfaces readable", func(t *testing.T) {
		noIfaces := func(typ, site string) bool { return typ != "interface" }
		m := runAllow(t, w, LevelRoom, "A1", nil, noIfaces)
		for _, e := range m.Edges {
			if e.Medium == MediumPhysical {
				t.Errorf("a physical edge between unreadable interfaces: %+v", e)
			}
		}
		if mustNode(t, m, "R1").Counts.InternalLinks != 0 {
			t.Errorf("internal links must not count unreadable ones")
		}
	})
	t.Run("devices the caller cannot read are not counted", func(t *testing.T) {
		noDevices := func(typ, site string) bool { return typ != "device" }
		m := runAllow(t, w, LevelRoom, "A1", nil, noDevices)
		if r := mustNode(t, m, "R1"); r.Counts.Devices != 0 || r.Health.Worst != "unknown" {
			t.Errorf("R1 = %+v", r)
		}
		if len(m.Edges) != 0 {
			t.Errorf("a link needs its devices readable: %+v", m.Edges)
		}
	})
	t.Run("the focus must be readable", func(t *testing.T) {
		_, err := newIndex(w.snap()).View(LevelSite, "S2", Options{Allow: siteOne})
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("err = %v, want forbidden", err)
		}
	})
}

func TestViewErrors(t *testing.T) {
	ix := newIndex(lab().snap())
	for _, tc := range []struct {
		level, id string
		want      error
	}{
		{"galaxy", "", ErrLevel},
		{LevelGlobal, "S1", ErrMismatch},
		{LevelUnplaced, "S1", ErrMismatch},
		{LevelSite, "", ErrMismatch},
		{LevelSite, "A1", ErrMismatch},
		{LevelRoom, "R1", ErrMismatch},
		{LevelRack, "S1", ErrMismatch},
		{LevelSite, "nowhere", ErrNotFound},
		{LevelRack, "R1", nil},
		{LevelRoom, "S1", nil},
	} {
		if _, err := ix.View(tc.level, tc.id, Options{}); !errors.Is(err, tc.want) {
			t.Errorf("View(%q, %q) = %v, want %v", tc.level, tc.id, err, tc.want)
		}
	}
	w := lab()
	w.absent("R1")
	if _, err := newIndex(w.snap()).View(LevelRack, "R1", Options{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("an absent rack is not on the map: %v", err)
	}
	w = lab()
	w.add("R9", "rack", "", nil)
	if _, err := newIndex(w.snap()).View(LevelRack, "R9", Options{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a rack in no site is not on the map: %v", err)
	}
}

func TestCaps(t *testing.T) {
	t.Run("nodes", func(t *testing.T) {
		w := &world{}
		w.add("S", "site", "", nil).add("A", "room", "S", nil)
		for i := 0; i < MaxNodes+20; i++ {
			w.add(fmt.Sprintf("k%04d", i), "rack", "A", nil)
		}
		m := run(t, w, LevelRoom, "A", nil)
		if len(m.Nodes) != MaxNodes || !m.Truncated {
			t.Errorf("nodes = %d truncated = %v, want %d and truncated", len(m.Nodes), m.Truncated, MaxNodes)
		}
	})
	t.Run("edges", func(t *testing.T) {
		const n = 70 // 2415 pairs
		w := &world{}
		w.add("S", "site", "", nil).add("K", "rack", "S", nil)
		for i := 0; i < n; i++ {
			w.dev(fmt.Sprintf("d%02d", i), "K", nil)
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				a, b := fmt.Sprintf("d%02d/to%02d", i, j), fmt.Sprintf("d%02d/to%02d", j, i)
				w.add(a, "interface", fmt.Sprintf("d%02d", i), nil).add(b, "interface", fmt.Sprintf("d%02d", j), nil)
				w.cable(a, b)
			}
		}
		m := run(t, w, LevelRack, "K", nil)
		if len(m.Edges) != MaxEdges || !m.Truncated {
			t.Errorf("edges = %d truncated = %v, want %d and truncated", len(m.Edges), m.Truncated, MaxEdges)
		}
	})
	t.Run("a small map is not truncated", func(t *testing.T) {
		if m := run(t, lab(), LevelGlobal, "", nil); m.Truncated {
			t.Errorf("truncated")
		}
	})
}

func TestIndexIsBuiltOncePerSnapshot(t *testing.T) {
	s := lab().snap()
	a, b := IndexOf(s), IndexOf(s)
	if a != b {
		t.Errorf("the same snapshot must share its index")
	}
	if c := IndexOf(lab().snap()); c == a {
		t.Errorf("a new snapshot must get a new index")
	}
}

// TestMapOn2000Devices is the size check: 50 racks of 40 devices in 5 rooms,
// about 3,900 cables, live values on every port. Each level must answer in
// well under a second; the figures are logged for STATUS.md.
func TestMapOn2000Devices(t *testing.T) {
	w := &world{}
	w.add("S", "site", "", nil)
	lv := map[string]live.Link{}
	const rooms, racksPerRoom, perRack = 5, 10, 40
	dev := func(r, k, i int) string { return fmt.Sprintf("d-%d-%02d-%02d", r, k, i) }
	for r := 0; r < rooms; r++ {
		room := fmt.Sprintf("room%d", r)
		w.add(room, "room", "S", nil)
		for k := 0; k < racksPerRoom; k++ {
			rack := fmt.Sprintf("rack-%d-%02d", r, k)
			w.add(rack, "rack", room, map[string]any{"floor_x": float64(k), "floor_y": 0.0, "u_height": 42.0})
			for i := 0; i < perRack; i++ {
				w.dev(dev(r, k, i), rack, map[string]any{"role": "server", "health": "ok"}, "up", "down", "side", "next")
			}
		}
	}
	cables := 0
	link := func(a, b string) {
		w.cable(a, b)
		lv[a] = live.Link{TX: rate(1e6), RX: rate(2e6), Oper: "up"}
		cables++
	}
	for r := 0; r < rooms; r++ {
		for k := 0; k < racksPerRoom; k++ {
			for i := 0; i < perRack; i++ {
				if i+1 < perRack { // down to the next device in the rack
					link(dev(r, k, i)+"/down", dev(r, k, i+1)+"/up")
				}
				if k == 0 && r+1 < rooms { // the same slot in the next room
					link(dev(r, k, i)+"/up", dev(r+1, k, i)+"/next")
				}
				if k+1 < racksPerRoom { // the same slot in the next rack
					link(dev(r, k, i)+"/side", dev(r, k+1, i)+"/next")
				}
			}
		}
	}
	snap := w.snap()
	start := time.Now()
	ix := IndexOf(snap)
	built := time.Since(start)
	t.Logf("%d devices, %d cables; index built in %v", rooms*racksPerRoom*perRack, cables, built.Round(time.Millisecond))
	allow := func(string, string) bool { return true }
	for _, tc := range []struct{ level, id string }{
		{LevelGlobal, ""}, {LevelSite, "S"}, {LevelRoom, "room0"}, {LevelRack, "rack-0-00"},
	} {
		start := time.Now()
		m, err := ix.View(tc.level, tc.id, Options{Live: lv, Allow: allow})
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%-6s %4d nodes %4d edges in %v", tc.level, len(m.Nodes), len(m.Edges), took.Round(100*time.Microsecond))
		if took > time.Second {
			t.Errorf("%s took %v, want under a second", tc.level, took)
		}
	}
}
