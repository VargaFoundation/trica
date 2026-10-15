package api_test

import (
	"encoding/json"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

type rackItem struct {
	Device struct {
		ID      string `json:"id"`
		Display string `json:"display"`
	} `json:"device"`
	Position *float64 `json:"position"`
	Face     string   `json:"face"`
	Model    *struct {
		Display string `json:"display"`
	} `json:"model"`
	UHeight   *float64 `json:"u_height"`
	FullDepth bool     `json:"full_depth"`
}

func rackItems(t *testing.T, p page) []rackItem {
	t.Helper()
	out := make([]rackItem, 0, len(p.Items))
	for _, raw := range p.Items {
		var it rackItem
		if err := json.Unmarshal(raw, &it); err != nil {
			t.Fatal(err)
		}
		out = append(out, it)
	}
	return out
}

// furnish mounts, in rack-a (Paris, where sw-a1 already is, position
// unknown), sw-a2 at U 10 on the front as a 1 U full-depth model, and a patch
// panel with neither position nor model. The rack is also tracked in a
// ticket: a relation of the rack that is not a mount.
func (e *env) furnish() {
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("dt", "device_type").Identify("slug", "acme-sw48").Set("model", "SW48").Set("u_height", 1).Set("full_depth", true)
	b.Object("sw2", "device").Identify("name", "sw-a2").Set("role", "switch")
	b.Object("pp", "device").Identify("name", "pp-a1").Set("role", "patch-panel")
	b.Object("tk", "ticket").Identify("key", "OPS-1")
	rack := sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: e.ids["rack-p"]}}
	b.Relations = append(b.Relations,
		&sdk.Relation{Type: "instance_of", From: sdk.Ref("sw2"), To: sdk.Ref("dt")},
		&sdk.Relation{Type: "tracked_in", From: rack, To: sdk.Ref("tk")},
		&sdk.Relation{Type: "mounted_in", From: sdk.Ref("sw2"), To: rack, Attributes: map[string]any{"position": 10, "face": "front"}},
		&sdk.Relation{Type: "mounted_in", From: sdk.Ref("pp"), To: rack, Attributes: map[string]any{}})
	e.apply(b)
}

// One call returns what an elevation needs: before it, the UI made one
// request per mounted device to learn its model (finding #42).
func TestRackContentsGiveAnElevationInOneCall(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.furnish()
	p := e.page("/api/v1/racks/"+e.ids["rack-p"]+"/contents", tokReader)
	items := rackItems(t, p)
	if p.Total != 3 || len(items) != 3 {
		t.Fatalf("total %d, %d items, want 3: %+v", p.Total, len(items), items)
	}
	sw, pp := items[0], items[1]
	if sw.Device.Display != "sw-a2" || sw.Position == nil || *sw.Position != 10 || sw.Face != "front" {
		t.Fatalf("first item %+v, want sw-a2 at U 10 on the front", sw)
	}
	if sw.Model == nil || sw.Model.Display != "SW48" || sw.UHeight == nil || *sw.UHeight != 1 || !sw.FullDepth {
		t.Fatalf("sw-a2 model %+v height %v full depth %v, want SW48, 1 U, full depth", sw.Model, sw.UHeight, sw.FullDepth)
	}
	if pp.Device.Display != "pp-a1" || pp.Position != nil || pp.Model != nil || pp.UHeight != nil || pp.Face != "" {
		t.Fatalf("second item %+v, want pp-a1 with nothing known (unknown positions last, then by name)", pp)
	}
	if items[2].Device.Display != "sw-a1" {
		t.Fatalf("third item %s, want sw-a1", items[2].Device.Display)
	}
	if other := e.page("/api/v1/racks/"+e.ids["rack-l"]+"/contents", tokReader); other.Total != 1 {
		t.Fatalf("rack-b holds %d devices, want 1 (srv-01)", other.Total)
	}
}

func TestRackContentsOfWhatIsNotARack(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	wantProblem(t, e.get("/api/v1/racks/"+e.ids["sw"]+"/contents", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/racks/00000000-0000-4000-8000-000000000000/contents", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/racks/nope/contents", tokReader), 400, "invalid-request")
}

// The rack's contents say no more than the objects one by one: a device or a
// model the caller may not read is left out.
func TestRackContentsKeepToWhatTheCallerMayRead(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.furnish()
	wantStatus(t, e.get("/api/v1/racks/"+e.ids["rack-p"]+"/contents", tokDevices), 403)
	items := rackItems(t, e.page("/api/v1/racks/"+e.ids["rack-p"]+"/contents", tokSiteParis))
	if len(items) != 3 {
		t.Fatalf("%d items, want the three Paris devices", len(items))
	}
	for _, it := range items {
		if it.Model != nil || it.UHeight != nil {
			t.Fatalf("%s: model %+v shown to a caller limited to a site (models have no site)", it.Device.Display, it.Model)
		}
	}
	wantStatus(t, e.get("/api/v1/racks/"+e.ids["rack-l"]+"/contents", tokSiteParis), 403)
}
