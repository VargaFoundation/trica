package api_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/place"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func (e *env) placeAs(source, device, room string) {
	e.t.Helper()
	e.clk.advance(time.Minute)
	b := sdk.NewBatch(source, sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.RelateEndpoints("located_in", sdk.ByID("device", e.ids[device]), sdk.ByID("room", e.ids[room]))
	e.apply(b)
}

// Two places in two sites open a location_conflict that the API lists with the
// places and their sources, to the readers who may see the device.
func TestLocationConflictThroughTheAPI(t *testing.T) {
	e := placeEnv(t)
	e.placeAs("user:a", "ap", "room-p")
	e.placeAs("user:b", "ap", "room-l")

	path := "/api/v1/discrepancies?status=open&kind=location_conflict&object=" + e.ids["ap"]
	page := e.page(path, tokReader)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("conflicts %d, want 1", page.Total)
	}
	var d struct {
		Kind    string         `json:"kind"`
		Key     string         `json:"key"`
		Details place.Conflict `json:"details"`
	}
	if err := json.Unmarshal(page.Items[0], &d); err != nil {
		t.Fatal(err)
	}
	if d.Kind != "location_conflict" || d.Key != "located_in" || d.Details.Reason != place.ReasonSites || len(d.Details.Places) != 2 {
		t.Fatalf("conflict %+v", d)
	}
	// With no site, the device is invisible to a site-limited reader: so is its conflict.
	if got := e.page(path, tokSiteLyon); got.Total != 0 {
		t.Fatalf("a Lyon-limited reader sees %d conflicts of a device that has no site", got.Total)
	}
}
