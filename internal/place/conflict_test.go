package place_test

import (
	"context"
	"sort"
	"testing"

	"github.com/VargaFoundation/trica/internal/place"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func (w *world) conflict(device string) (place.Conflict, bool) {
	w.t.Helper()
	m, err := place.Conflicts(context.Background(), w.st.Pool, []string{w.ids[device]})
	if err != nil {
		w.t.Fatal(err)
	}
	c, ok := m[w.ids[device]]
	return c, ok
}

func (w *world) withdraw(source, device, placeRef, typ string) {
	w.t.Helper()
	b := sdk.NewBatch(source, sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Unrelate(place.LocatedIn, sdk.ByID("device", w.ids[device]), sdk.ByID(typ, w.ids[placeRef]))
	w.apply(b)
}

func TestConflicts(t *testing.T) {
	type step struct{ source, device, place, typ string }
	tests := []struct {
		name   string
		steps  []step
		device string
		want   string // "" = no conflict, else the reason
		sites  []string
		rack   string
	}{
		{name: "no place", device: "ap"},
		{name: "one place", steps: []step{{"user:a", "ap", "room-x1", "room"}}, device: "ap"},
		{name: "two rooms of one site", steps: []step{{"user:a", "ap", "room-x1", "room"}, {"user:b", "ap", "room-x2", "room"}}, device: "ap"},
		{name: "a room and its site", steps: []step{{"user:a", "ap", "room-x1", "room"}, {"user:b", "ap", "x", "site"}}, device: "ap"},
		{name: "two sites", steps: []step{{"user:a", "ap", "room-x1", "room"}, {"user:b", "ap", "room-y", "room"}}, device: "ap", want: place.ReasonSites, sites: []string{"x", "y"}},
		{name: "a rack agrees with its place", steps: []step{{"user:a", "sw", "room-x2", "room"}}, device: "sw"},
		{name: "a rack against another site", steps: []step{{"user:a", "sw", "room-y", "room"}}, device: "sw", want: place.ReasonRack, sites: []string{"x", "y"}, rack: "x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			for _, s := range tc.steps {
				w.locate(s.source, s.device, s.place, s.typ)
			}
			c, ok := w.conflict(tc.device)
			if tc.want == "" {
				if ok {
					t.Fatalf("unexpected conflict %+v", c)
				}
				return
			}
			if !ok || c.Reason != tc.want {
				t.Fatalf("conflict %+v (%v), want reason %s", c, ok, tc.want)
			}
			var sites []string
			for _, s := range tc.sites {
				sites = append(sites, w.ids[s])
			}
			sort.Strings(sites)
			if len(c.Sites) != len(sites) || c.Sites[0] != sites[0] || c.Sites[1] != sites[1] {
				t.Fatalf("sites %v, want %v", c.Sites, sites)
			}
			if tc.rack != "" && (c.RackSiteID != w.ids[tc.rack] || c.RackID != w.ids["rack-x"]) {
				t.Fatalf("rack %s in site %s, want rack-x in X", c.RackID, c.RackSiteID)
			}
			for _, p := range c.Places {
				if len(p.Sources) == 0 || p.SiteID == "" || p.PlaceType == "" {
					t.Fatalf("incomplete placement %+v", p)
				}
			}
		})
	}
}

func TestConflictsKeepsSources(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "ap", "room-x1", "room")
	w.locate("user:b", "ap", "room-x1", "room")
	w.locate("user:c", "ap", "room-y", "room")
	c, ok := w.conflict("ap")
	if !ok || len(c.Places) != 2 {
		t.Fatalf("conflict %+v", c)
	}
	for _, p := range c.Places {
		if p.PlaceID == w.ids["room-x1"] && (len(p.Sources) != 2 || p.Sources[0] != "user:a" || p.Sources[1] != "user:b") {
			t.Fatalf("sources %v, want user:a and user:b", p.Sources)
		}
	}
}

func TestAWithdrawnPlaceRaisesNoConflict(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "ap", "room-x1", "room")
	w.locate("user:b", "ap", "room-y", "room")
	w.withdraw("user:b", "ap", "room-y", "room")
	if c, ok := w.conflict("ap"); ok {
		t.Fatalf("a closed place still conflicts: %+v", c)
	}
}

// Conflicts and Sites state one rule: a device with a "sites" conflict has no
// site, and a "rack" conflict leaves the site of the rack.
func TestConflictsAgreeWithSites(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "ap", "room-x1", "room")
	w.locate("user:b", "ap", "room-y", "room")
	w.locate("user:a", "sw", "room-y", "room")
	ctx := context.Background()
	all, _, err := w.st.List(ctx, store.ListFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range all {
		ids = append(ids, o.ID)
	}
	sites, err := place.Sites(ctx, w.st.Pool, ids)
	if err != nil {
		t.Fatal(err)
	}
	conflicts, err := place.Conflicts(ctx, w.st.Pool, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 2 {
		t.Fatalf("conflicts %+v, want ap (sites) and sw (rack)", conflicts)
	}
	for dev, c := range conflicts {
		switch c.Reason {
		case place.ReasonSites:
			if s, ok := sites[dev]; ok {
				t.Errorf("%s has conflict sites but Sites gives %s", dev, s)
			}
		case place.ReasonRack:
			if sites[dev] != c.RackSiteID {
				t.Errorf("%s: Sites gives %q, the rack's site is %s", dev, sites[dev], c.RackSiteID)
			}
		default:
			t.Errorf("reason %q", c.Reason)
		}
	}
}

func TestPlacedUnder(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "ap", "room-x1", "room")
	ctx := context.Background()
	tests := []struct {
		name string
		move string
		want []string
	}{
		{"a building reaches the rooms below", "b1", []string{"ap"}},
		{"a site reaches every device below", "x", []string{"ap", "sw"}},
		{"a rack reaches its devices", "rack-x", []string{"sw"}},
		{"an empty room reaches nothing", "room-y", nil},
	}
	for _, tc := range tests {
		got, err := place.PlacedUnder(ctx, w.st.Pool, []string{w.ids[tc.move]})
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, d := range tc.want {
			want = append(want, w.ids[d])
		}
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", tc.name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", tc.name, got, want)
			}
		}
	}
	if got, err := place.PlacedUnder(ctx, nil, nil); err != nil || got != nil {
		t.Errorf("no ids: %v %v", got, err)
	}
	if m, err := place.Conflicts(ctx, nil, nil); err != nil || len(m) != 0 {
		t.Errorf("no ids: %v %v", m, err)
	}
}
