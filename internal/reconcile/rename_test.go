package reconcile_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// declared loads a site, a room and a rack, and a device mounted in the rack, the way a declaration file import does.
func declaredPlaces(c *clock) *sdk.Batch {
	b := batch(c, "import:pereire", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("s", "site").Identify("code", "PER").Identify("name", "Pereire")
	b.Object("r", "room").Identify("name", "Salle 1").In("s")
	b.Object("k", "rack").Identify("name", "Baie A").In("r").Set("u_height", 42)
	b.Object("d", "device").Identify("name", "sw-1").Set("role", "switch")
	b.RelateEndpoints("mounted_in", sdk.Ref("d"), sdk.Ref("k")).With("position", 10)
	return b
}

// rename is what the UI sends: the object designated by id, one claim on name, from a person.
func rename(c *clock, typ, id, name string) *sdk.Batch {
	b := batch(c, "user:alice", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("o", typ).Identify(sdk.IDKey, id).Set("name", name)
	return b
}

func count(t *testing.T, s *store.Store, typ string) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM objects WHERE type = $1`, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRenamePlacesKeepsTheObject(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	ids := apply(t, e, declaredPlaces(c)).Objects
	t0 := c.t
	for _, tc := range []struct{ ref, typ, old, new string }{
		{"s", "site", "Pereire", "Paris Pereire"},
		{"r", "room", "Salle 1", "Salle serveurs"},
		{"k", "rack", "Baie A", "Baie 01"},
	} {
		c.advance(time.Hour)
		res := apply(t, e, rename(c, tc.typ, ids[tc.ref], tc.new))
		if res.Created != 0 || res.Objects["o"] != ids[tc.ref] {
			t.Fatalf("%s: rename created or moved to another object: %+v", tc.typ, res)
		}
		if n := count(t, s, tc.typ); n != 1 {
			t.Fatalf("%s: %d objects after the rename, want 1", tc.typ, n)
		}
		o, err := s.Object(ctx, ids[tc.ref])
		if err != nil {
			t.Fatal(err)
		}
		if o.Display != tc.new || o.Attrs["name"] != tc.new {
			t.Fatalf("%s: display %q attrs %v, want %q", tc.typ, o.Display, o.Attrs["name"], tc.new)
		}
		old, err := s.AsOf(ctx, ids[tc.ref], t0.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if old["name"] != tc.old {
			t.Fatalf("%s: history lost the old name: %v", tc.typ, old["name"])
		}
	}
	// Children and relations did not move.
	room, _ := s.Object(ctx, ids["r"])
	rack, _ := s.Object(ctx, ids["k"])
	if room.ParentID == nil || *room.ParentID != ids["s"] || rack.ParentID == nil || *rack.ParentID != ids["r"] {
		t.Fatalf("parents changed: room %v rack %v", room.ParentID, rack.ParentID)
	}
	if rack.Attrs["u_height"] != float64(42) && rack.Attrs["u_height"] != int64(42) {
		t.Fatalf("rack height lost: %v", rack.Attrs["u_height"])
	}
	var mounted int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM relations WHERE type = 'mounted_in' AND to_id = $1 AND upper_inf(valid)`, ids["k"]).Scan(&mounted); err != nil {
		t.Fatal(err)
	}
	if mounted != 1 {
		t.Fatalf("%d devices mounted in the renamed rack, want 1", mounted)
	}
}

// The declaration file still says the old name. Importing it again must neither create the object
// a second time nor take the operator's name back.
func TestReimportAfterRenameDoesNotDuplicate(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	ids := apply(t, e, declaredPlaces(c)).Objects
	c.advance(time.Hour)
	apply(t, e, rename(c, "rack", ids["k"], "Baie 01"))
	c.advance(time.Hour)
	res := apply(t, e, declaredPlaces(c))
	if res.Created != 0 || res.Objects["k"] != ids["k"] || res.Objects["r"] != ids["r"] || res.Objects["s"] != ids["s"] {
		t.Fatalf("re-import created or matched other objects: %+v", res)
	}
	for _, typ := range []string{"site", "room", "rack"} {
		if n := count(t, s, typ); n != 1 {
			t.Fatalf("%s: %d objects after the re-import, want 1", typ, n)
		}
	}
	rack, err := s.Object(ctx, ids["k"])
	if err != nil {
		t.Fatal(err)
	}
	if rack.Display != "Baie 01" {
		t.Fatalf("the re-import took the old name back: %q", rack.Display)
	}
}

// A name another place of the same parent holds is refused, in the preview as in the submission,
// and nothing is written. The same name under another parent, or the object's own name, is fine.
func TestRenameToASiblingsNameIsRefused(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	ids := apply(t, e, declaredPlaces(c)).Objects
	c.advance(time.Minute)
	b := batch(c, "user:alice", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("r", "room").Identify(sdk.IDKey, ids["r"])
	b.Object("s", "site").Identify(sdk.IDKey, ids["s"])
	b.Object("k2", "rack").Identify("name", "Baie B").In("r").Set("u_height", 42)
	b.Object("other", "room").Identify("name", "Salle 2").In("s")
	b.Object("k3", "rack").Identify("name", "Baie A").In("other")
	r2 := apply(t, e, b).Objects
	before := count(t, s, "rack")

	c.advance(time.Minute)
	if _, err := e.Preview(ctx, rename(c, "rack", ids["k"], "Baie B")); !errors.Is(err, reconcile.ErrNameTaken) || !errors.Is(err, reconcile.ErrConflict) {
		t.Fatalf("preview of a duplicate name: %v, want ErrNameTaken", err)
	}
	// Whatever the case or the spacing: the name is compared as an identifier.
	_, err := e.Apply(ctx, rename(c, "rack", ids["k"], "  baie b "), "", "")
	if !errors.Is(err, reconcile.ErrNameTaken) {
		t.Fatalf("apply of a duplicate name: %v, want ErrNameTaken", err)
	}
	o, _ := s.Object(ctx, ids["k"])
	if o.Display != "Baie A" || count(t, s, "rack") != before {
		t.Fatalf("a refused rename changed something: %q, %d racks", o.Display, count(t, s, "rack"))
	}
	// The name of a rack in another room, and the object's own name, are free.
	apply(t, e, rename(c, "rack", r2["k3"], "Baie B"))
	apply(t, e, rename(c, "rack", ids["k"], "Baie A"))
}

// The operator's name is not lost to the declaration file because of the clocks: it ranks above the
// other sources. Withdrawing it gives the name back to the file.
func TestPersonsNameOutranksTheDeclaration(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	ids := apply(t, e, declaredPlaces(c)).Objects
	// The browser's clock is ten minutes behind the server's: the rename was "observed" before the import.
	c.advance(time.Minute)
	b := rename(c, "rack", ids["k"], "Baie 01")
	b.ObservedAt = c.t.Add(-10 * time.Minute)
	apply(t, e, b)
	rack, _ := s.Object(ctx, ids["k"])
	if rack.Display != "Baie 01" {
		t.Fatalf("display %q, want the person's name although the file was applied after it", rack.Display)
	}
	c.advance(time.Hour)
	apply(t, e, declaredPlaces(c))
	rack, _ = s.Object(ctx, ids["k"])
	if rack.Display != "Baie 01" {
		t.Fatalf("display %q after the file was applied again, want the person's name", rack.Display)
	}
	// Withdrawn, the name is the file's again.
	c.advance(time.Hour)
	w := batch(c, "user:alice", sdk.ScopePartial)
	w.Confidence = sdk.Declared
	w.Object("o", "rack").Identify(sdk.IDKey, ids["k"]).Unset("name")
	apply(t, e, w)
	rack, _ = s.Object(ctx, ids["k"])
	if rack.Display != "Baie A" {
		t.Fatalf("display %q after the withdrawal, want the file's name", rack.Display)
	}
}
