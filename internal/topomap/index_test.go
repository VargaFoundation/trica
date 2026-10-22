package topomap

import (
	"reflect"
	"testing"
)

func TestPlacementFollowsParentsAndMounts(t *testing.T) {
	ix := newIndex(lab().snap())
	for id, want := range map[string][]string{
		"S1":     {"S1"},
		"A1":     {"S1", "B1", "A1"},
		"R2":     {"S1", "B1", "A1", "RW1", "R2"},
		"R3":     {"S2", "P1", "R3"},
		"sw1":    {"S1", "B1", "A1", "RW1", "R1", "sw1"},
		"fw-p":   {"S2", "P1", "R3", "fw-p"},
		"orphan": {UnplacedID, "orphan"},
	} {
		if got := ix.path[id]; !reflect.DeepEqual(got, want) {
			t.Errorf("path[%s] = %v, want %v", id, got, want)
		}
	}
}

func TestDeviceInSiteByContains(t *testing.T) {
	ix := newIndex(lab().snap())
	if got, want := ix.path["nas"], []string{"S2", "nas"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nas path = %v, want %v", got, want)
	}
	w := lab()
	w.dev("pdu", "", nil)
	w.rel("contains", "A1", "pdu")
	if got, want := newIndex(w.snap()).path["pdu"], []string{"S1", "B1", "A1", "pdu"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a device a room contains: path = %v, want %v", got, want)
	}
}

func TestRackWithoutSiteAndAbsentObjectsAreUnplaced(t *testing.T) {
	w := lab()
	w.add("R9", "rack", "", nil) // no parent: in no site
	w.dev("lost", "R9", nil)
	w.dev("gone", "R1", nil)
	w.absent("gone")
	ix := newIndex(w.snap())
	if got := ix.path["lost"]; !reflect.DeepEqual(got, []string{UnplacedID, "lost"}) {
		t.Errorf("a device in a rack outside any site: %v", got)
	}
	if ix.path["gone"] != nil {
		t.Errorf("an absent device must not be placed: %v", ix.path["gone"])
	}
}

func TestDoubleMountPrefersDeclared(t *testing.T) {
	for _, tc := range []struct {
		name         string
		c1, c2, want string
	}{
		{"declared beats observed", "observed", "declared", "R2"},
		{"observed beats deduced", "deduced", "observed", "R2"},
		{"declared first", "declared", "deduced", "R1"},
		{"a tie goes to the lowest id", "observed", "observed", "R1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := lab()
			w.add("dd", "device", "", nil)
			w.relc("mounted_in", "dd", "R1", tc.c1, nil).relc("mounted_in", "dd", "R2", tc.c2, nil)
			ix := newIndex(w.snap())
			p := ix.path["dd"]
			if got := p[len(p)-2]; got != tc.want {
				t.Errorf("rack = %s, want %s", got, tc.want)
			}
			if !ix.ambiguous["dd"] {
				t.Errorf("a double mount must be counted")
			}
		})
	}
	m := run(t, func() *world {
		w := lab()
		w.add("dd", "device", "", nil)
		w.rel("mounted_in", "dd", "R1").rel("mounted_in", "dd", "R2")
		return w
	}(), LevelGlobal, "", nil)
	if m.Notes.AmbiguousPlacements != 1 {
		t.Errorf("notes.ambiguous_placements = %d, want 1", m.Notes.AmbiguousPlacements)
	}
}

func TestTunnelAndGatewayPlacedThroughContains(t *testing.T) {
	w := lab()
	w.add("tun", "tunnel", "", map[string]any{"kind": "wireguard", "peer": "10.9.0.1", "status": "up"})
	w.rel("contains", "fw-a", "tun")
	w.add("gw", "gateway", "", map[string]any{"address": "198.51.100.1", "status": "online"})
	w.rel("contains", "fw-a", "gw")
	ix := newIndex(w.snap())
	if len(ix.tunnels) != 1 || ix.tunnels[0].owner != "fw-a" {
		t.Fatalf("tunnels = %+v, want one owned by fw-a", ix.tunnels)
	}
	if len(ix.uplinks) != 1 || ix.uplinks[0].device != "fw-a" {
		t.Fatalf("uplinks = %+v, want one from fw-a (a gateway it contains)", ix.uplinks)
	}
	// An orphan tunnel nobody contains has no place: it is not drawn.
	w.add("lonely", "tunnel", "", map[string]any{"peer": "10.9.0.1"})
	if got := len(newIndex(w.snap()).tunnels); got != 1 {
		t.Errorf("a tunnel without an owner must be skipped, got %d tunnels", got)
	}
}

func TestAuthSiteFollowsParentsAndMountsAndFailsClosed(t *testing.T) {
	w := lab()
	w.add("both", "device", "", nil)
	w.rel("mounted_in", "both", "R1").rel("mounted_in", "both", "R3")
	w.add("deep", "interface", "both", nil)
	ix := newIndex(w.snap())
	for id, want := range map[string]string{
		"S1": "S1", "R2": "S1", "sw1": "S1", "sw1/e1": "S1", "fw-p/wan": "S2", "R3": "S2",
		"nas":  "", // contained by the site, but rights follow parents and mounts only
		"both": "", // two sites: ambiguous, fails closed
		"deep": "", "orphan": "", "missing": "",
	} {
		if got := ix.AuthSite(id); got != want {
			t.Errorf("AuthSite(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestAuthSiteStopsAtDepth32(t *testing.T) {
	w := &world{}
	w.add("S", "site", "", nil)
	prev := "S"
	// A chain of 33 buildings is deeper than the rights query walks.
	for i := 0; i < 40; i++ {
		id := string(rune('a'+i/26)) + string(rune('a'+i%26))
		w.add(id, "building", prev, nil)
		prev = id
	}
	ix := newIndex(w.snap())
	if got := ix.AuthSite("aa"); got != "S" {
		t.Errorf("a shallow object is in its site, got %q", got)
	}
	if got := ix.AuthSite(prev); got != "" {
		t.Errorf("an object 40 levels down is out of reach of the rights query, got %q", got)
	}
}
