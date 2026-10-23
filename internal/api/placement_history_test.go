package api_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

type placementRecord struct {
	Rack struct {
		ID      string `json:"id"`
		Display string `json:"display"`
		Status  string `json:"status"`
	} `json:"rack"`
	Position *float64   `json:"position"`
	Face     string     `json:"face"`
	Since    time.Time  `json:"since"`
	Until    *time.Time `json:"until"`
	Source   string     `json:"source"`
}

type removedItem struct {
	rackItem
	RemovedAt time.Time `json:"removed_at"`
	RemovedBy string    `json:"removed_by"`
	Status    string    `json:"status"`
}

func decodeItems[T any](t *testing.T, p page) []T {
	t.Helper()
	out := make([]T, 0, len(p.Items))
	for _, raw := range p.Items {
		var it T
		if err := json.Unmarshal(raw, &it); err != nil {
			t.Fatal(err)
		}
		out = append(out, it)
	}
	return out
}

func (e *env) endpoint(typ, key string) sdk.Endpoint {
	return sdk.Endpoint{Type: typ, Identity: map[string]string{sdk.IDKey: e.ids[key]}}
}

// mount, unmount and moveTo declare, as a person, what the rack page does, one
// hour after the previous step so that the history has an order.
func (e *env) mount(dev, rack string, pos float64, face string) {
	e.clk.advance(time.Hour)
	b := sdk.NewBatch("user:carol", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.RelateEndpoints("mounted_in", e.endpoint("device", dev), e.endpoint("rack", rack)).Attributes = map[string]any{"position": pos, "face": face}
	e.apply(b)
}

func (e *env) unmount(dev, rack string) {
	e.clk.advance(time.Hour)
	b := sdk.NewBatch("user:carol", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Unrelate("mounted_in", e.endpoint("device", dev), e.endpoint("rack", rack))
	e.apply(b)
}

func (e *env) newDevices(names ...string) {
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	for _, n := range names {
		b.Object(n, "device").Identify("name", n).Set("role", "pdu")
	}
	e.apply(b)
}

// "Where was it" reads the closed placements: the current one first, then the
// others, the most recently ended first, each with the position and face it had.
func TestPlacementHistoryNewestFirst(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.newDevices("pdu-1")
	e.mount("pdu-1", "rack-p", 3, "front")
	e.mount("pdu-1", "rack-l", 5, "rear") // a second mount: the user's claim in another rack
	e.unmount("pdu-1", "rack-l")
	e.unmount("pdu-1", "rack-p")
	e.mount("pdu-1", "rack-p", 7, "front")

	url := "/api/v1/objects/" + e.ids["pdu-1"] + "/placement-history"
	got := decodeItems[placementRecord](t, e.page(url, tokReader))
	if len(got) != 3 {
		t.Fatalf("%d placements, want 3: %+v", len(got), got)
	}
	cur, last, first := got[0], got[1], got[2]
	if cur.Until != nil || cur.Rack.Display != "rack-a" || cur.Position == nil || *cur.Position != 7 {
		t.Fatalf("first %+v, want the current placement (rack-a, U7) with no end", cur)
	}
	if last.Until == nil || first.Until == nil || !last.Until.After(*first.Until) {
		t.Fatalf("closed placements %+v then %+v, want the most recently ended first", last, first)
	}
	if last.Rack.Display != "rack-a" || *last.Position != 3 || last.Face != "front" || last.Source != "user:carol" {
		t.Fatalf("second %+v, want rack-a U3 front by user:carol", last)
	}
	if first.Rack.Display != "rack-b" || *first.Position != 5 || first.Face != "rear" {
		t.Fatalf("third %+v, want rack-b U5 rear", first)
	}
	// A page of one keeps the order.
	if one := decodeItems[placementRecord](t, e.page(url+"?limit=1", tokReader)); len(one) != 1 || one[0].Until != nil {
		t.Fatalf("limit=1: %+v, want the current placement", one)
	}
	// A rack the caller may not read is left out, the device is still readable.
	if d := decodeItems[placementRecord](t, e.page(url, tokDevices)); len(d) != 0 {
		t.Fatalf("a caller who reads no rack sees %+v, want none", d)
	}
	wantProblem(t, e.get("/api/v1/objects/"+e.ids["rack-p"]+"/placement-history", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/objects/nope/placement-history", tokReader), 400, "invalid-request")
}

func TestRackRemovedListsWhatWasTakenOut(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.newDevices("out-1", "moved-1", "movedout-1", "back-1")
	e.mount("out-1", "rack-p", 3, "front")
	e.mount("moved-1", "rack-p", 5, "front")
	e.mount("movedout-1", "rack-p", 8, "rear")
	e.mount("back-1", "rack-p", 12, "front")
	e.clk.advance(time.Hour)
	cut := e.clk.now() // what follows is "recent"
	e.unmount("out-1", "rack-p")
	// moved to another rack: the old mount closes, the new one is current
	e.unmount("moved-1", "rack-p")
	e.mount("moved-1", "rack-l", 2, "front")
	e.unmount("movedout-1", "rack-p")
	e.mount("movedout-1", "rack-l", 4, "front")
	e.unmount("movedout-1", "rack-l")
	e.unmount("back-1", "rack-p")
	e.clk.advance(time.Minute)
	e.mount("back-1", "rack-p", 12, "front") // put back: it is mounted, not removed
	// a device a collector mounts and a person also placed, then withdrew: still mounted
	e.mount("sw", "rack-p", 20, "front")
	e.unmount("sw", "rack-p")

	removed := func(rack, query, token string) []removedItem {
		return decodeItems[removedItem](t, e.page("/api/v1/racks/"+e.ids[rack]+"/removed"+query, token))
	}
	namesOf := func(items []removedItem) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it.Device.Display)
		}
		return out
	}
	inA := removed("rack-p", "", tokReader)
	sameNames(t, "rack-a", namesOf(inA), "out-1")
	o := inA[0]
	if o.Position == nil || *o.Position != 3 || o.Face != "front" || o.RemovedBy != "user:carol" || o.Status != "active" || o.RemovedAt.IsZero() {
		t.Fatalf("out-1 %+v, want U3 front, removed by user:carol, active", o)
	}
	// moved then taken out: only under the rack it left last
	sameNames(t, "rack-b", namesOf(removed("rack-l", "", tokReader)), "movedout-1")
	// since keeps what ended at or after it
	sameNames(t, "since in the future", namesOf(removed("rack-p", "?since="+e.clk.now().Add(time.Hour).Format(time.RFC3339), tokReader)))
	sameNames(t, "since before", namesOf(removed("rack-p", "?since="+cut.Format(time.RFC3339), tokReader)), "out-1")
	// a device the caller may not read is left out; the rack is still readable
	if got := removed("rack-p", "", tokRackOnly); len(got) != 0 {
		t.Fatalf("a caller who reads no device sees %+v", got)
	}
	// same refusal as the contents
	wantStatus(t, e.get("/api/v1/racks/"+e.ids["rack-p"]+"/removed", tokDevices), 403)
	wantProblem(t, e.get("/api/v1/racks/"+e.ids["sw"]+"/removed", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/racks/nope/removed", tokReader), 400, "invalid-request")
}
