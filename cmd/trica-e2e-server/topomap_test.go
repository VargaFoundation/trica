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
