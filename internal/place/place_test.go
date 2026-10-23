package place_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/place"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

type world struct {
	t   *testing.T
	st  *store.Store
	eng *reconcile.Engine
	now time.Time
	ids map[string]string
}

// newWorld loads, through the engine, two sites (X with a building and a room,
// a second room and a rack; Y with a room) and three devices in no place yet.
func newWorld(t *testing.T) *world {
	t.Helper()
	st := testdb.New(t)
	reg := schema.Builtin()
	w := &world{t: t, st: st, now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC), ids: map[string]string{}}
	w.eng = &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Now: func() time.Time { return w.now }}
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("x", "site").Identify("code", "X").Set("name", "site-x")
	b.Object("y", "site").Identify("code", "Y").Set("name", "site-y")
	b.Object("b1", "building").Identify("name", "b1").In("x")
	b.Object("room-x1", "room").Identify("name", "salle-1").In("b1")
	b.Object("room-x2", "room").Identify("name", "salle-2").In("x")
	b.Object("room-y", "room").Identify("name", "salle-y").In("y")
	b.Object("rack-x", "rack").Identify("name", "rack-x").In("x")
	b.Object("ap", "device").Identify("name", "ap-1").Set("role", "access_point")
	b.Object("sw", "device").Identify("name", "sw-1").Set("role", "switch")
	w.apply(b)
	b = sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Relations = append(b.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.ByID("device", w.ids["sw"]), To: sdk.ByID("rack", w.ids["rack-x"]), Attributes: map[string]any{}})
	w.apply(b)
	return w
}

func (w *world) apply(b *sdk.Batch) {
	w.t.Helper()
	w.now = w.now.Add(time.Minute)
	b.ObservedAt = w.now
	res, err := w.eng.Apply(context.Background(), b, "", "")
	if err != nil {
		w.t.Fatalf("%s: %v", b.Source, err)
	}
	for ref, id := range res.Objects {
		w.ids[ref] = id
	}
}

// locate places a device with a source; typ is the type of the place.
func (w *world) locate(source, device, placeRef, typ string) {
	w.t.Helper()
	b := sdk.NewBatch(source, sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Relations = append(b.Relations, &sdk.Relation{Type: place.LocatedIn, From: sdk.ByID("device", w.ids[device]), To: sdk.ByID(typ, w.ids[placeRef]), Attributes: map[string]any{}})
	w.apply(b)
}

func (w *world) site(id string) string {
	w.t.Helper()
	m, err := place.Sites(context.Background(), w.st.Pool, []string{w.ids[id]})
	if err != nil {
		w.t.Fatal(err)
	}
	return m[w.ids[id]]
}

func TestSitesFollowsLocatedIn(t *testing.T) {
	w := newWorld(t)
	if got := w.site("ap"); got != "" {
		t.Fatalf("an unplaced device is in site %q", got)
	}
	w.locate("user:a", "ap", "room-x1", "room")
	if got := w.site("ap"); got != w.ids["x"] {
		t.Fatalf("an AP in a room of a building of X is in %q, want X", got)
	}
}

func TestARackWinsOverAPlace(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "sw", "room-y", "room")
	if got := w.site("sw"); got != w.ids["x"] {
		t.Fatalf("a device mounted in a rack of X and located in a room of Y is in %q, want X", got)
	}
}

func TestDisagreeingSourcesLeaveNoSite(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "ap", "room-x1", "room")
	w.locate("user:b", "ap", "room-y", "room")
	if got := w.site("ap"); got != "" {
		t.Fatalf("two sources in two sites: site %q, want none", got)
	}
	w2 := newWorld(t)
	w2.locate("user:a", "ap", "room-x1", "room")
	w2.locate("user:b", "ap", "room-x2", "room")
	if got := w2.site("ap"); got != w2.ids["x"] {
		t.Fatalf("two rooms of the same site: %q, want X", got)
	}
}

func TestAClosedPlaceIsIgnored(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "ap", "room-x1", "room")
	b := sdk.NewBatch("user:a", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Unrelate(place.LocatedIn, sdk.ByID("device", w.ids["ap"]), sdk.ByID("room", w.ids["room-x1"]))
	w.apply(b)
	if got := w.site("ap"); got != "" {
		t.Fatalf("a withdrawn place still gives site %q", got)
	}
}

// The two directions of the rule must agree on every object: the site: query
// reads it downwards, the API upwards.
func TestWithinSitesAgreesWithSites(t *testing.T) {
	w := newWorld(t)
	w.locate("user:a", "ap", "room-x1", "room")
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
	up, err := place.Sites(ctx, w.st.Pool, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"x", "y"} {
		rows, err := w.st.Pool.Query(ctx, `SELECT o.id::text FROM objects o WHERE `+place.WithinSites("s.id = $1"), w.ids[site])
		if err != nil {
			t.Fatal(err)
		}
		var down []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			down = append(down, id)
		}
		rows.Close()
		var want []string
		for id, s := range up {
			if s == w.ids[site] {
				want = append(want, id)
			}
		}
		sort.Strings(down)
		sort.Strings(want)
		if len(down) == 0 || len(down) != len(want) {
			t.Fatalf("site %s: down %v, up %v", site, down, want)
		}
		for i := range down {
			if down[i] != want[i] {
				t.Fatalf("site %s: down %v, up %v", site, down, want)
			}
		}
	}
}

func TestRules(t *testing.T) {
	if !place.DeclaredOnly("located_in") || place.DeclaredOnly("mounted_in") {
		t.Error("DeclaredOnly must hold for located_in only")
	}
	if !place.Exclusive("located_in") || place.Exclusive("contains") {
		t.Error("Exclusive must hold for located_in only")
	}
	if m, err := place.Sites(context.Background(), nil, nil); err != nil || len(m) != 0 {
		t.Errorf("no ids: %v %v", m, err)
	}
}
