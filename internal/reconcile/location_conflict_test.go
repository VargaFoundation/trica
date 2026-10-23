package reconcile_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/place"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// twoSites loads sites X (rooms x1 and x2, a rack) and Y (a room), an access
// point in no place, and a switch mounted in X's rack.
func twoSites(t *testing.T) (*reconcile.Engine, *store.Store, *clock, map[string]string) {
	t.Helper()
	e, s, c := newEngine(t, reconcile.Policy{})
	b := batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("x", "site").Identify("code", "X").Set("name", "site-x")
	b.Object("y", "site").Identify("code", "Y").Set("name", "site-y")
	b.Object("x1", "room").Identify("name", "salle-1").In("x")
	b.Object("x2", "room").Identify("name", "salle-2").In("x")
	b.Object("y1", "room").Identify("name", "salle-y").In("y")
	b.Object("rack", "rack").Identify("name", "rack-x").In("x")
	b.Object("ap", "device").Identify("name", "ap-1").Set("role", "access_point")
	b.Object("sw", "device").Identify("name", "sw-1").Set("role", "switch")
	ids := apply(t, e, b).Objects
	c.advance(time.Minute)
	b = batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.RelateEndpoints("mounted_in", sdk.ByID("device", ids["sw"]), sdk.ByID("rack", ids["rack"]))
	apply(t, e, b)
	return e, s, c, ids
}

func place1(c *clock, source string, ids map[string]string, device, room string) *sdk.Batch {
	c.advance(time.Minute)
	b := batch(c, source, sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.RelateEndpoints("located_in", sdk.ByID("device", ids[device]), sdk.ByID("room", ids[room]))
	return b
}

type locRow struct {
	ID          int64
	Fingerprint string
	Status      string
	ClosedBy    string
	Key         string
	Details     place.Conflict
}

func locRows(t *testing.T, s *store.Store, device string) []locRow {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `SELECT id, fingerprint, status, coalesce(closed_by, ''), key, details
		FROM discrepancies WHERE kind = 'location_conflict' AND object_id = $1 ORDER BY id`, device)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []locRow
	for rows.Next() {
		var r locRow
		var det []byte
		if err := rows.Scan(&r.ID, &r.Fingerprint, &r.Status, &r.ClosedBy, &r.Key, &det); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(det, &r.Details); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func openLoc(t *testing.T, s *store.Store, device string) []locRow {
	t.Helper()
	var out []locRow
	for _, r := range locRows(t, s, device) {
		if r.Status == "open" {
			out = append(out, r)
		}
	}
	return out
}

func TestLocationConflictOpensOnTwoSites(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	if got := openLoc(t, s, ids["ap"]); len(got) != 0 {
		t.Fatalf("one place: %+v", got)
	}
	res := apply(t, e, place1(c, "user:b", ids, "ap", "y1"))
	got := openLoc(t, s, ids["ap"])
	if len(got) != 1 {
		t.Fatalf("open conflicts %+v, want one", got)
	}
	d := got[0]
	if d.Key != "located_in" || d.Details.Reason != place.ReasonSites || len(d.Details.Places) != 2 || len(d.Details.Sites) != 2 {
		t.Fatalf("conflict %+v", d)
	}
	for _, p := range d.Details.Places {
		want := map[string]string{ids["x1"]: "user:a", ids["y1"]: "user:b"}[p.PlaceID]
		if len(p.Sources) != 1 || p.Sources[0] != want {
			t.Fatalf("place %+v, want source %s", p, want)
		}
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings %v", res.Warnings)
	}
}

func TestLocationConflictResolvesOnMove(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	apply(t, e, place1(c, "user:b", ids, "ap", "y1"))
	apply(t, e, place1(c, "user:b", ids, "ap", "x2"))
	if got := openLoc(t, s, ids["ap"]); len(got) != 0 {
		t.Fatalf("still open: %+v", got)
	}
	rows := locRows(t, s, ids["ap"])
	if len(rows) != 1 || rows[0].Status != "resolved" || rows[0].ClosedBy != "reconcile" {
		t.Fatalf("rows %+v", rows)
	}
}

func TestLocationConflictResolvesOnWithdrawal(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	apply(t, e, place1(c, "user:b", ids, "ap", "y1"))
	if got := openLoc(t, s, ids["ap"]); len(got) != 1 {
		t.Fatalf("before the withdrawal: %+v", got)
	}
	c.advance(time.Minute)
	b := batch(c, "user:b", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Unrelate("located_in", sdk.ByID("device", ids["ap"]), sdk.ByID("room", ids["y1"]))
	apply(t, e, b)
	if got := openLoc(t, s, ids["ap"]); len(got) != 0 {
		t.Fatalf("still open after withdrawal: %+v", got)
	}
}

func TestLocationConflictIsIdempotent(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	b := place1(c, "user:b", ids, "ap", "y1")
	apply(t, e, b)
	first := openLoc(t, s, ids["ap"])
	c.advance(time.Minute)
	apply(t, e, b)
	again := openLoc(t, s, ids["ap"])
	if len(first) != 1 || len(again) != 1 || first[0].ID != again[0].ID {
		t.Fatalf("first %+v, again %+v", first, again)
	}
	// Another person in the same sites: the same row, with their name.
	apply(t, e, place1(c, "user:c", ids, "ap", "x2"))
	third := openLoc(t, s, ids["ap"])
	if len(third) != 1 || third[0].ID != first[0].ID || len(third[0].Details.Places) != 3 {
		t.Fatalf("a place in the same sites must refresh the same row: %+v", third)
	}
	if n := len(locRows(t, s, ids["ap"])); n != 1 {
		t.Fatalf("%d rows ever, want 1", n)
	}
}

func TestLocationConflictReasonChange(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	apply(t, e, place1(c, "user:b", ids, "ap", "y1"))
	if got := openLoc(t, s, ids["ap"]); len(got) != 1 || got[0].Details.Reason != place.ReasonSites {
		t.Fatalf("%+v", got)
	}
	c.advance(time.Minute)
	b := batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.RelateEndpoints("mounted_in", sdk.ByID("device", ids["ap"]), sdk.ByID("rack", ids["rack"]))
	apply(t, e, b)
	got := openLoc(t, s, ids["ap"])
	if len(got) != 1 || got[0].Details.Reason != place.ReasonRack || got[0].Details.RackID != ids["rack"] {
		t.Fatalf("after mounting: %+v, want exactly one, reason rack", got)
	}
	c.advance(time.Minute)
	b = batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Unrelate("mounted_in", sdk.ByID("device", ids["ap"]), sdk.ByID("rack", ids["rack"]))
	apply(t, e, b)
	got = openLoc(t, s, ids["ap"])
	if len(got) != 1 || got[0].Details.Reason != place.ReasonSites {
		t.Fatalf("after unmounting: %+v, want one, reason sites", got)
	}
}

func TestRackWinsButSaysSo(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "sw", "x2"))
	if got := openLoc(t, s, ids["sw"]); len(got) != 0 {
		t.Fatalf("a place in the rack's site: %+v", got)
	}
	apply(t, e, place1(c, "user:a", ids, "sw", "y1"))
	got := openLoc(t, s, ids["sw"])
	if len(got) != 1 || got[0].Details.Reason != place.ReasonRack || got[0].Details.RackSiteID != ids["x"] {
		t.Fatalf("a place in another site than the rack: %+v", got)
	}
	res, err := e.Preview(context.Background(), place1(c, "user:b", ids, "sw", "y1"))
	if err != nil || len(res.Warnings) != 1 {
		t.Fatalf("warnings %v, %v", res, err)
	}
	apply(t, e, place1(c, "user:a", ids, "sw", "x2"))
	if got := openLoc(t, s, ids["sw"]); len(got) != 0 {
		t.Fatalf("moved back to the rack's site: %+v", got)
	}
}

func TestMovingARoomChangesTheConflict(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	apply(t, e, place1(c, "user:b", ids, "ap", "y1"))
	if got := openLoc(t, s, ids["ap"]); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	// Room Y1 is re-parented under X: the two places now agree.
	c.advance(time.Minute)
	b := batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("x", "site").Identify("code", "X")
	b.Object("y1", "room").Identify(sdk.IDKey, ids["y1"]).In("x")
	apply(t, e, b)
	if got := openLoc(t, s, ids["ap"]); len(got) != 0 {
		t.Fatalf("a room moved under the same site leaves the conflict open: %+v", got)
	}
	// And the other way: room X2 goes to Y while the AP is in x1 and x2.
	apply(t, e, place1(c, "user:c", ids, "ap", "x2"))
	if got := openLoc(t, s, ids["ap"]); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	c.advance(time.Minute)
	b = batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("y", "site").Identify("code", "Y")
	b.Object("x2", "room").Identify(sdk.IDKey, ids["x2"]).In("y")
	apply(t, e, b)
	if got := openLoc(t, s, ids["ap"]); len(got) != 1 || got[0].Details.Reason != place.ReasonSites {
		t.Fatalf("a room moved to another site must open the conflict: %+v", got)
	}
}

func TestAcceptedLocationConflictStaysAccepted(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	apply(t, e, place1(c, "user:b", ids, "ap", "y1"))
	open := openLoc(t, s, ids["ap"])
	if err := reconcile.AcceptDiscrepancy(context.Background(), s, open[0].ID, "user:a", "known"); err != nil {
		t.Fatal(err)
	}
	apply(t, e, place1(c, "user:c", ids, "ap", "x2"))
	if got := openLoc(t, s, ids["ap"]); len(got) != 0 {
		t.Fatalf("an accepted conflict with the same sites reopened: %+v", got)
	}
	// A third site is a new conflict.
	c.advance(time.Minute)
	b := batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("z", "site").Identify("code", "Z").Set("name", "site-z")
	b.Object("z1", "room").Identify("name", "salle-z").In("z")
	ids["z1"] = apply(t, e, b).Objects["z1"]
	apply(t, e, place1(c, "user:d", ids, "ap", "z1"))
	got := openLoc(t, s, ids["ap"])
	if len(got) != 1 || len(got[0].Details.Sites) != 3 {
		t.Fatalf("a third site: %+v", got)
	}
}

func TestPreviewWarnsOfALocationConflict(t *testing.T) {
	e, s, c, ids := twoSites(t)
	apply(t, e, place1(c, "user:a", ids, "ap", "x1"))
	res, err := e.Preview(context.Background(), place1(c, "user:b", ids, "ap", "y1"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "location_conflict") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings %v", res.Warnings)
	}
	if n := len(locRows(t, s, ids["ap"])); n != 0 {
		t.Fatalf("a preview left %d rows", n)
	}
}
