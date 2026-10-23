package api_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const tokSiteLyon = "t-site-lyon" // user hana: read limited to the site lyon-1 (added by the tests that need it)

// placeEnv adds an access point in no rack and a room in each site to the
// harness inventory, and a Lyon-limited reader.
func placeEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, reconcile.Policy{})
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("paris", "site").Identify("code", "PAR1")
	b.Object("lyon", "site").Identify("code", "LYO1")
	b.Object("room-p", "room").Identify("name", "salle-1").In("paris")
	b.Object("room-l", "room").Identify("name", "salle-l").In("lyon")
	b.Object("ap", "device").Identify("name", "ap-1").Set("role", "access_point")
	e.apply(b)
	e.auth.Tokens[tokSiteLyon] = api.Grant{Principal: api.Principal{Subject: "hana", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Sites: []string{e.ids["lyon"]}}
	return e
}

// placesOf lists the places a device is located in now: id -> source.
func (e *env) placesOf(device string) map[string]string {
	e.t.Helper()
	out := map[string]string{}
	for _, l := range links(e.t, e.page("/api/v1/objects/"+device+"/relations", tokReader)) {
		if l.Type == "located_in" && l.FromID == device {
			out[l.ToID] = l.Source
		}
	}
	return out
}

func (e *env) searchIDs(q string) []string {
	e.t.Helper()
	return ids(e.t, e.page("/api/v1/search?q="+url.QueryEscape(q), tokReader), "id")
}

func contains(l []string, id string) bool {
	for _, v := range l {
		if v == id {
			return true
		}
	}
	return false
}

func TestPlacingAnUnrackedDevice(t *testing.T) {
	e := placeEnv(t)
	set := loadUIBatches(t)
	ap, paris := e.ids["ap"], e.ids["paris"]

	// In no place, a device has no site: a site-limited reader is refused.
	wantProblem(t, e.get("/api/v1/objects/"+ap, tokSiteParis), 403, "forbidden")
	wantProblem(t, e.get("/api/v1/objects/"+ap, tokSiteLyon), 403, "forbidden")
	if got, ok := e.get("/api/v1/objects/"+ap, tokReader).obj(t)["site_id"]; ok && got != "" && got != nil {
		t.Fatalf("an unplaced device claims the site %v", got)
	}

	// A person places it in a Paris room.
	if p := e.previewUI(set, "locateApInParisRoom", tokProposer); !p.relates() {
		t.Fatalf("preview of a placement lists no relation: %+v", p.Result.Changes)
	}
	out := e.submitUI(set, "locateApInParisRoom", tokProposer, 200)
	if out.Status != "applied" || out.Result.RelationsOpened != 1 {
		t.Fatalf("placement: %+v", out)
	}
	if got := e.placesOf(ap); len(got) != 1 || got[e.ids["room-p"]] != "user:carol" {
		t.Fatalf("places %v, want the Paris room by user:carol", got)
	}
	before := e.clk.now().Format(time.RFC3339)

	r := e.get("/api/v1/objects/"+ap, tokSiteParis)
	wantStatus(t, r, 200)
	if got := r.obj(t)["site_id"]; got != paris {
		t.Fatalf("site_id %v, want Paris", got)
	}
	wantProblem(t, e.get("/api/v1/objects/"+ap, tokSiteLyon), 403, "forbidden")

	// The site: query finds it in Paris and not in Lyon.
	if got := e.searchIDs("type:device site:PAR1"); !contains(got, ap) {
		t.Fatalf("site:PAR1 misses the access point: %v", got)
	}
	if got := e.searchIDs("type:device site:LYO1"); contains(got, ap) {
		t.Fatalf("site:LYO1 finds the access point: %v", got)
	}

	// A device mounted in a Paris rack stays in Paris whatever a place says.
	b := sdk.NewBatch("user:carol", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.ObservedAt = e.clk.now()
	b.RelateEndpoints("located_in", sdk.ByID("device", e.ids["sw"]), sdk.ByID("room", e.ids["room-l"]))
	e.clk.advance(time.Minute)
	b.ObservedAt = e.clk.now()
	if _, err := e.eng.Apply(t.Context(), b, "", ""); err != nil {
		t.Fatal(err)
	}
	r = e.get("/api/v1/objects/"+e.ids["sw"], tokSiteParis)
	wantStatus(t, r, 200)
	if got := r.obj(t)["site_id"]; got != paris {
		t.Fatalf("a racked device placed in Lyon is in %v, want Paris (the rack wins)", got)
	}

	// Moving to a Lyon room closes the Paris relation, and the preview says so.
	e.clk.advance(time.Hour)
	p := e.previewUI(set, "locateApInLyonRoom", tokProposer)
	unrelates := 0
	for _, c := range p.Result.Changes {
		if c.Op == "unrelate" {
			unrelates++
		}
	}
	if unrelates != 1 || p.Result.RelationsClosed != 1 {
		t.Fatalf("preview of a move: %+v", p.Result)
	}
	out = e.submitUI(set, "locateApInLyonRoom", tokProposer, 200)
	if out.Result.RelationsClosed != 1 || out.Result.RelationsOpened != 1 {
		t.Fatalf("move: %+v", out.Result)
	}
	if got := e.placesOf(ap); len(got) != 1 || got[e.ids["room-l"]] != "user:carol" {
		t.Fatalf("places after the move %v", got)
	}
	wantProblem(t, e.get("/api/v1/objects/"+ap, tokSiteParis), 403, "forbidden")
	wantStatus(t, e.get("/api/v1/objects/"+ap, tokSiteLyon), 200)

	// The history keeps the Paris room.
	var was []string
	for _, l := range links(t, e.page("/api/v1/objects/"+ap+"/relations?at="+url.QueryEscape(before), tokReader)) {
		if l.Type == "located_in" {
			was = append(was, l.ToID)
		}
	}
	if len(was) != 1 || was[0] != e.ids["room-p"] {
		t.Fatalf("relations as of %s: %v, want the Paris room", before, was)
	}
	if e.page("/api/v1/objects/"+ap+"/timeline", tokReader).Total < 2 {
		t.Fatal("the timeline lost a placement")
	}

	// Taking the place away leaves no site.
	e.clk.advance(time.Minute)
	e.submitUI(set, "unlocateApInLyon", tokProposer, 200)
	if got := e.placesOf(ap); len(got) != 0 {
		t.Fatalf("places after unlocating: %v", got)
	}
	wantProblem(t, e.get("/api/v1/objects/"+ap, tokSiteLyon), 403, "forbidden")
}

// A collector does not place devices (ADR 0058): its located_in is dropped
// with a warning.
func TestCollectorsCannotPlaceDevices(t *testing.T) {
	e := placeEnv(t)
	b := userBatch("", func(b *sdk.Batch) {
		b.RelateEndpoints("located_in", sdk.ByID("device", e.ids["ap"]), sdk.ByID("room", e.ids["room-p"]))
	})
	b.ObservedAt = e.clk.now()
	r := e.do("POST", "/api/v1/batches", tokCollector, batchJSON(t, b))
	wantStatus(t, r, 200)
	var out uiOutcome
	r.json(t, &out)
	if len(out.Result.Warnings) != 1 {
		t.Fatalf("warnings %v, want one", out.Result.Warnings)
	}
	if got := e.placesOf(e.ids["ap"]); len(got) != 0 {
		t.Fatalf("a collector placed a device: %v", got)
	}
}
