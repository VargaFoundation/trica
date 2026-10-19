package api_test

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestProbesArePublic(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	for path, want := range map[string]string{"/api/v1/healthz": "ok", "/api/v1/readyz": "ready"} {
		r := e.get(path, "") // no credential
		wantStatus(t, r, 200)
		if got := r.obj(t)["status"]; got != want {
			t.Errorf("%s: status %v, want %s", path, got, want)
		}
	}
}

func TestReadyzFailsWhenTheDatabaseIsGone(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.st.Pool.Close()
	wantProblem(t, e.get("/api/v1/readyz", ""), 503, "not-ready")
}

func TestSchemaEndpoints(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	r := e.get("/api/v1/schema", tokReader)
	wantStatus(t, r, 200)
	var doc struct {
		Types []struct {
			Name       string `json:"name"`
			Attributes []struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			} `json:"attributes"`
		} `json:"types"`
		Relations []struct {
			Name string `json:"name"`
		} `json:"relations"`
	}
	r.json(t, &doc)
	names := map[string]bool{}
	for _, ty := range doc.Types {
		names[ty.Name] = true
	}
	for _, want := range []string{"site", "device", "interface", "rack"} {
		if !names[want] {
			t.Errorf("type %s missing from /schema", want)
		}
	}
	if len(doc.Relations) == 0 {
		t.Error("no relation types in /schema")
	}

	r = e.get("/api/v1/schema/types/device", tokReader)
	wantStatus(t, r, 200)
	var ty struct {
		Name       string `json:"name"`
		Attributes []struct {
			Name   string `json:"name"`
			Kind   string `json:"kind"`
			Series bool   `json:"series"`
		} `json:"attributes"`
		Identity struct {
			Strong []string `json:"strong"`
		} `json:"identity"`
	}
	r.json(t, &ty)
	// The measurements are marked, so that the UI draws them as curves.
	series := map[string]bool{}
	for _, a := range ty.Attributes {
		series[a.Name] = a.Series
	}
	if !series["power_w"] || !series["inlet_temp_c"] || series["os_version"] || series["u_height"] {
		t.Errorf("series flags of device: %v", series)
	}
	kinds := map[string]string{}
	for _, a := range ty.Attributes {
		kinds[a.Name] = a.Kind
	}
	if ty.Name != "device" || kinds["mgmt_ip"] != "ip" || kinds["role"] != "enum" || !slices.Contains(ty.Identity.Strong, "serial") {
		t.Fatalf("device type doc: %+v", ty)
	}
	wantProblem(t, e.get("/api/v1/schema/types/starship", tokReader), 404, "not-found")
}

func TestObjectsListAndFilter(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	all := e.get("/api/v1/objects?limit=1000", tokReader)
	wantStatus(t, all, 200)
	var sp struct {
		Summary struct {
			Query  string         `json:"query"`
			Total  int            `json:"total"`
			ByType map[string]int `json:"by_type"`
		} `json:"summary"`
		Total int `json:"total"`
		Items []struct {
			ID         string         `json:"id"`
			Type       string         `json:"type"`
			Display    string         `json:"display"`
			Attributes map[string]any `json:"attributes"`
		} `json:"items"`
	}
	all.json(t, &sp)
	// 2 sites + 2 racks + 2 devices + 2 interfaces
	if sp.Total != 8 || len(sp.Items) != 8 || sp.Summary.Total != 8 {
		t.Fatalf("objects: total %d items %d summary %d: %s", sp.Total, len(sp.Items), sp.Summary.Total, all.Body)
	}
	if sp.Summary.ByType["device"] != 2 || sp.Summary.ByType["interface"] != 2 {
		t.Fatalf("by_type %v", sp.Summary.ByType)
	}

	p := e.page("/api/v1/objects?type=device", tokReader)
	if p.Total != 2 {
		t.Fatalf("type=device: %d", p.Total)
	}
	p = e.page("/api/v1/objects?q="+url.QueryEscape("type:device role:switch"), tokReader)
	if p.Total != 1 || field(t, p.Items[0], "display") != "sw-a1" {
		t.Fatalf("TQL filter: %d %s", p.Total, p.Items)
	}
	p = e.page("/api/v1/objects?type=device&sort=-name", tokReader)
	if got := ids(t, p, "display"); !slices.Equal(got, []string{"sw-a1", "srv-01"}) {
		t.Fatalf("sort=-name: %v", got)
	}
	p = e.page("/api/v1/objects?type=device&sort=name", tokReader)
	if got := ids(t, p, "display"); !slices.Equal(got, []string{"srv-01", "sw-a1"}) {
		t.Fatalf("sort=name: %v", got)
	}
	// the type parameter and a text query combine
	p = e.page("/api/v1/objects?type=device&q=sw-a1", tokReader)
	if p.Total != 1 {
		t.Fatalf("type + q: %d", p.Total)
	}
	wantProblem(t, e.get("/api/v1/objects?type=starship", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/objects?q="+url.QueryEscape("role:("), tokReader), 400, "invalid-query")
}

func TestSearchSummaryThenPage(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	r := e.get("/api/v1/search?q=sw-a1", tokReader)
	wantStatus(t, r, 200)
	// The summary comes first in the body: counted before listed.
	body := string(r.Body)
	if !strings.Contains(body, `"summary"`) || strings.Index(body, `"summary"`) > strings.Index(body, `"items"`) {
		t.Fatalf("summary must precede items: %s", body)
	}
	var sp struct {
		Summary struct {
			Total  int            `json:"total"`
			ByType map[string]int `json:"by_type"`
		} `json:"summary"`
		Items []map[string]any `json:"items"`
	}
	r.json(t, &sp)
	if sp.Summary.Total < 1 || len(sp.Items) < 1 || sp.Items[0]["display"] != "sw-a1" {
		t.Fatalf("search sw-a1: %s", r.Body)
	}
	wantProblem(t, e.get("/api/v1/search", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/search?q="+url.QueryEscape(`role:"`), tokReader), 400, "invalid-query")
}

func TestObjectProvenanceHistoryRelationsTimeline(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw := e.ids["sw"]

	r := e.get("/api/v1/objects/"+sw, tokReader)
	wantStatus(t, r, 200)
	o := r.obj(t)
	attrs := o["attributes"].(map[string]any)
	if o["type"] != "device" || o["display"] != "sw-a1" || attrs["mgmt_ip"] != "192.0.2.10" {
		t.Fatalf("object: %s", r.Body)
	}

	r = e.get("/api/v1/objects/"+sw+"/provenance", tokReader)
	wantStatus(t, r, 200)
	var prov struct {
		ObjectID   string `json:"object_id"`
		Attributes []struct {
			Key        string `json:"key"`
			Source     string `json:"source"`
			Confidence string `json:"confidence"`
		} `json:"attributes"`
	}
	r.json(t, &prov)
	found := false
	for _, a := range prov.Attributes {
		if a.Key == "mgmt_ip" {
			found = a.Source == "arista:sw-a1" && a.Confidence == "observed"
		}
	}
	if prov.ObjectID != sw || !found {
		t.Fatalf("provenance: %s", r.Body)
	}

	// Change the version later, so there is a history.
	e.clk.advance(24 * time.Hour)
	b := sdk.NewBatch("arista:sw-a1", sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1").Set("os_version", "4.28.0F")
	e.apply(b)

	p := e.page("/api/v1/objects/"+sw+"/history?key=os_version", tokReader)
	if p.Total != 2 {
		t.Fatalf("history of os_version: %d entries: %s", p.Total, p.Items)
	}
	p = e.page("/api/v1/objects/"+sw+"/history", tokReader)
	if p.Total < 5 {
		t.Fatalf("full history: %d", p.Total)
	}

	p = e.page("/api/v1/objects/"+sw+"/relations", tokReader)
	// mounted_in the rack, contains... at least the rack and the two interfaces' parent link are not relations
	if p.Total < 1 {
		t.Fatalf("relations: %d", p.Total)
	}
	var rel struct {
		Type      string `json:"type"`
		Direction string `json:"direction"`
		Other     struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Display string `json:"display"`
		} `json:"other"`
	}
	for _, it := range p.Items {
		_ = json.Unmarshal(it, &rel)
		if rel.Type == "mounted_in" {
			break
		}
	}
	if rel.Type != "mounted_in" || rel.Direction != "out" || rel.Other.ID != e.ids["rack-p"] || rel.Other.Type != "rack" || rel.Other.Display != "rack-a" {
		t.Fatalf("mounted_in relation: %+v", rel)
	}
	// Before the switch existed there was nothing.
	if p := e.page("/api/v1/objects/"+sw+"/relations?at=2026-01-01", tokReader); p.Total != 0 {
		t.Fatalf("relations before creation: %d", p.Total)
	}

	p = e.page("/api/v1/objects/"+sw+"/timeline", tokReader)
	if p.Total < 1 {
		t.Fatalf("timeline: %d", p.Total)
	}
	p = e.page("/api/v1/objects/"+sw+"/timeline?from=2026-03-02&to=2026-03-03", tokReader)
	if p.Total != 1 {
		t.Fatalf("timeline window of the change: %d: %s", p.Total, p.Items)
	}
	wantProblem(t, e.get("/api/v1/objects/"+sw+"/timeline?from=2026-03-03&to=2026-03-02", tokReader), 400, "invalid-request")
}

func TestObjectAtADate(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw := e.ids["sw"]
	e.clk.advance(48 * time.Hour)
	b := sdk.NewBatch("arista:sw-a1", sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1").Set("os_version", "4.28.0F").Set("mgmt_ip", "192.0.2.11")
	e.apply(b)

	attrs := func(path string) map[string]any {
		r := e.get(path, tokReader)
		wantStatus(t, r, 200)
		return r.obj(t)["attributes"].(map[string]any)
	}
	now := attrs("/api/v1/objects/" + sw)
	then := attrs("/api/v1/objects/" + sw + "?at=2026-03-02")
	if now["os_version"] != "4.28.0F" || now["mgmt_ip"] != "192.0.2.11" {
		t.Fatalf("now: %v", now)
	}
	if then["os_version"] != "4.20.7M" || then["mgmt_ip"] != "192.0.2.10" {
		t.Fatalf("state at 2026-03-02 shows the current values: %v", then)
	}
	r := e.get("/api/v1/objects/"+sw+"?at=2026-03-02", tokReader)
	if o := r.obj(t); o["as_of"] == nil || o["display"] != "sw-a1" {
		t.Fatalf("as_of view: %s", r.Body)
	}
	// Before the object existed: no state.
	wantProblem(t, e.get("/api/v1/objects/"+sw+"?at=2026-01-01", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/objects/"+sw+"?at=yesterday", tokReader), 400, "invalid-request")
}

func TestObjectIDValidationAndUnknown(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	wantProblem(t, e.get("/api/v1/objects/not-a-uuid", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/objects/00000000-0000-4000-8000-000000000000", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/objects/00000000-0000-4000-8000-000000000000/history", tokReader), 404, "not-found")
}

func TestWhoHad(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw := e.ids["sw"]
	e.clk.advance(48 * time.Hour)
	// The switch changes address; the old one goes to another device later.
	b := sdk.NewBatch("arista:sw-a1", sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1").Set("mgmt_ip", "192.0.2.99")
	e.apply(b)

	now := e.page("/api/v1/whohad?key=mgmt_ip&value=192.0.2.99", tokReader)
	if now.Total != 1 || !strings.Contains(string(now.Items[0]), sw) {
		t.Fatalf("who has .99 now: %s", now.Items)
	}
	if p := e.page("/api/v1/whohad?key=mgmt_ip&value=192.0.2.10&at=2026-03-02", tokReader); p.Total != 1 {
		t.Fatalf("who had .10 on 2026-03-02: %s", p.Items)
	}
	if p := e.page("/api/v1/whohad?key=mgmt_ip&value=192.0.2.10&at=2026-03-04", tokReader); p.Total != 0 {
		t.Fatalf("who had .10 on 2026-03-04: %s", p.Items)
	}
	// The item says who.
	var it struct {
		Object struct {
			ID      string `json:"id"`
			Display string `json:"display"`
		} `json:"object"`
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(now.Items[0], &it); err != nil || it.Object.ID != sw || it.Object.Display != "sw-a1" || it.Key != "mgmt_ip" {
		t.Fatalf("whohad item: %+v %v", it, err)
	}
	// An address is found whatever its spelling.
	if p := e.page("/api/v1/whohad?key=base_mac&value=AA-BB-CC-00-00-01", tokReader); p.Total != 0 {
		t.Fatalf("unknown mac: %d", p.Total)
	}
	wantProblem(t, e.get("/api/v1/whohad?key=mgmt_ip", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/whohad?value=x", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/whohad?key=Bad%20Key&value=x", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/whohad?key=mgmt_ip&value=x&at=nope", tokReader), 400, "invalid-request")
}

func TestChangesFeed(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.clk.advance(24 * time.Hour)
	b := sdk.NewBatch("arista:sw-a1", sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1").Set("os_version", "4.28.0F")
	e.apply(b)

	all := e.page("/api/v1/changes?from=2026-02-28", tokReader)
	day2 := e.page("/api/v1/changes?from=2026-03-02&to=2026-03-03", tokReader)
	if all.Total <= day2.Total || day2.Total != 1 {
		t.Fatalf("changes: all %d, day 2 %d", all.Total, day2.Total)
	}
	one := e.page("/api/v1/changes?from=2026-02-28&object="+e.ids["srv"], tokReader)
	if one.Total == 0 || one.Total >= all.Total {
		t.Fatalf("changes of one object: %d of %d", one.Total, all.Total)
	}
	wantProblem(t, e.get("/api/v1/changes", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/changes?from=2026-02-28&object=zzz", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/changes?from=bad", tokReader), 400, "invalid-request")
}

func TestCollectionHealth(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	p := e.page("/api/v1/health/collection", tokReader)
	if p.Total != 1 {
		t.Fatalf("health rows: %d", p.Total)
	}
	var h struct {
		Source     string   `json:"source"`
		Collector  string   `json:"collector"`
		Target     string   `json:"target"`
		OK         bool     `json:"ok"`
		Items      int      `json:"items"`
		DurationMS int64    `json:"duration_ms"`
		Warnings   []string `json:"warnings"`
	}
	if err := json.Unmarshal(p.Items[0], &h); err != nil {
		t.Fatal(err)
	}
	if h.Source != "arista:sw-a1" || h.Collector != "arista" || h.Target != "192.0.2.10" || !h.OK || h.Items != 3 || h.DurationMS != 1500 || h.Warnings == nil {
		t.Fatalf("health row: %+v", h)
	}
}

func TestCollectionHealthScrubsCredentialsInErrors(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.clk.advance(time.Hour)
	b := sdk.NewBatch("redfish:srv-01", sdk.ScopePartial)
	b.Health = &sdk.Health{Target: "198.51.100.21", OK: false, Collector: "redfish",
		Error:    "GET https://admin:hunter2@198.51.100.21/redfish/v1: 401",
		Warnings: []string{"retry via ssh://root:pw0rd@198.51.100.21:22"}}
	e.apply(b)
	r := e.get("/api/v1/health/collection", tokReader)
	wantStatus(t, r, 200)
	for _, leak := range []string{"hunter2", "pw0rd", "admin:", "root:"} {
		if strings.Contains(string(r.Body), leak) {
			t.Fatalf("health leaks %q: %s", leak, r.Body)
		}
	}
	if !strings.Contains(string(r.Body), "https://***@198.51.100.21/redfish/v1: 401") {
		t.Fatalf("error text lost: %s", r.Body)
	}
}

// A listed object says which site it is in, as the object itself does: the
// room list and the topology read it from the list (#100).
func TestObjectsListCarriesTheSite(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	want := map[string]string{
		e.ids["paris"]:  e.ids["paris"], // a site is in itself
		e.ids["rack-l"]: e.ids["lyon"],  // through its parent
		e.ids["sw"]:     e.ids["paris"], // through mounted_in
		e.ids["srv"]:    e.ids["lyon"],
		e.ids["e1"]:     e.ids["paris"], // through its device
	}
	for _, path := range []string{"/api/v1/objects?limit=1000", "/api/v1/search?q=" + url.QueryEscape("sw-a1")} {
		var sp struct {
			Items []struct {
				ID     string `json:"id"`
				SiteID string `json:"site_id"`
			} `json:"items"`
		}
		r := e.get(path, tokReader)
		wantStatus(t, r, 200)
		r.json(t, &sp)
		if len(sp.Items) == 0 {
			t.Fatalf("%s: no items", path)
		}
		for _, it := range sp.Items {
			site, ok := want[it.ID]
			if !ok {
				continue
			}
			if it.SiteID != site {
				t.Errorf("%s: %s has site %q, want %q", path, it.ID, it.SiteID, site)
			}
			// The list and the object agree.
			var one struct {
				SiteID string `json:"site_id"`
			}
			e.get("/api/v1/objects/"+it.ID, tokReader).json(t, &one)
			if one.SiteID != it.SiteID {
				t.Errorf("%s: list says %q, object says %q", it.ID, it.SiteID, one.SiteID)
			}
		}
	}
}
