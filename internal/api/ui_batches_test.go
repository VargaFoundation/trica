package api_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
)

// The web UI declares the physical layout (rooms, racks, placements, devices
// no collector sees) by sending batches. Its builders (web/src/edit/batch.ts)
// write the exact batches they make to a file; these tests replay that file
// against the real API and engine, as a person, so a builder whose batch the
// engine would refuse or misread fails here and not in front of a user.
const uiBatchesFile = "../../web/src/edit/testdata/ui-batches.json"

type uiBatchSet map[string]json.RawMessage

func loadUIBatches(t *testing.T) uiBatchSet {
	t.Helper()
	raw, err := os.ReadFile(uiBatchesFile)
	if err != nil {
		t.Fatalf("%v: run `TRICA_UPDATE_UI_BATCHES=1 npx vitest run src/edit` in web/", err)
	}
	var set uiBatchSet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	return set
}

// body returns the named batch with its "@name" placeholders replaced by ids
// and observed now (the engine refuses a batch observed too long ago).
func (s uiBatchSet) body(t *testing.T, e *env, name string) map[string]any {
	t.Helper()
	text, ok := s[name]
	if !ok {
		t.Fatalf("no batch %q in %s", name, uiBatchesFile)
	}
	out := string(text)
	for k, id := range e.ids {
		out = strings.ReplaceAll(out, `"@`+k+`"`, `"`+id+`"`)
	}
	if i := strings.Index(out, `"@`); i >= 0 {
		t.Fatalf("batch %s: placeholder left unreplaced near %q", name, out[i:min(len(out), i+20)])
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	b["observed_at"] = e.clk.now().Format(time.RFC3339)
	return b
}

type uiOutcome struct {
	Source string `json:"source"`
	Status string `json:"status"`
	Result struct {
		ProposalID      int64             `json:"proposal_id"`
		Objects         map[string]string `json:"objects"`
		Created         int               `json:"created"`
		RelationsOpened int               `json:"relations_opened"`
		RelationsClosed int               `json:"relations_closed"`
		Changes         []struct {
			Op   string `json:"op"`
			Type string `json:"type"`
		} `json:"changes"`
		Warnings []string `json:"warnings"`
	} `json:"result"`
}

func (e *env) submitUI(set uiBatchSet, name, token string, want int) uiOutcome {
	e.t.Helper()
	r := e.do("POST", "/api/v1/batches", token, set.body(e.t, e, name))
	wantStatus(e.t, r, want)
	var out uiOutcome
	r.json(e.t, &out)
	return out
}

func (e *env) previewUI(set uiBatchSet, name, token string) uiOutcome {
	e.t.Helper()
	r := e.do("POST", "/api/v1/batches/preview", token, set.body(e.t, e, name))
	wantStatus(e.t, r, 200)
	var out uiOutcome
	r.json(e.t, &out)
	return out
}

func (o uiOutcome) relates() bool {
	for _, c := range o.Result.Changes {
		if c.Op == "relate" {
			return true
		}
	}
	return false
}

func (o uiOutcome) creates(typ string) bool {
	for _, c := range o.Result.Changes {
		if c.Op == "create" && c.Type == typ {
			return true
		}
	}
	return false
}

// mountsOf lists the racks a device is mounted in now, with position and source.
func (e *env) mountsOf(device string) map[string]map[string]any {
	e.t.Helper()
	out := map[string]map[string]any{}
	for _, raw := range e.page("/api/v1/objects/"+device+"/relations", tokReader).Items {
		var r struct {
			Type      string         `json:"type"`
			Direction string         `json:"direction"`
			ToID      string         `json:"to_id"`
			Source    string         `json:"source"`
			Attrs     map[string]any `json:"attributes"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			e.t.Fatal(err)
		}
		if r.Type == "mounted_in" && r.Direction == "out" {
			if r.Attrs == nil {
				r.Attrs = map[string]any{}
			}
			r.Attrs["_source"] = r.Source
			out[r.ToID] = r.Attrs
		}
	}
	return out
}

func TestUIBatchesDeclareTheRackLayout(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	set := loadUIBatches(t)

	room := e.submitUI(set, "createRoom", tokProposer, 200)
	if room.Status != "applied" || room.Source != "user:carol" || room.Result.Created != 1 || room.Result.Objects["new"] == "" {
		t.Fatalf("create room: %+v", room)
	}
	e.ids["room"] = room.Result.Objects["new"]

	rack := e.submitUI(set, "createRack", tokProposer, 200)
	e.ids["rack"] = rack.Result.Objects["new"]
	r := e.get("/api/v1/objects/"+e.ids["rack"], tokReader).obj(t)
	if r["parent_id"] != e.ids["room"] || r["display"] != "r14" {
		t.Fatalf("rack %+v, want r14 in the new room", r)
	}
	if a := r["attributes"].(map[string]any); a["u_height"] != float64(42) || a["floor_x"] != float64(2) || a["floor_y"] != float64(1) || a["facing"] != "south" {
		t.Fatalf("rack attributes %+v", a)
	}

	// A device no collector sees: the preview says it is new, the submit creates and mounts it.
	if p := e.previewUI(set, "placeNewPdu", tokProposer); !p.creates("device") {
		t.Fatalf("preview of a new PDU does not create a device: %+v", p.Result.Changes)
	}
	pdu := e.submitUI(set, "placeNewPdu", tokProposer, 200)
	e.ids["pdu"] = pdu.Result.Objects["device"]
	items := rackItems(t, e.page("/api/v1/racks/"+e.ids["rack"]+"/contents", tokReader))
	if len(items) != 1 || items[0].Device.Display != "pdu-r14-a" || *items[0].Position != 1.5 || items[0].Face != "rear" || items[0].UHeight == nil || *items[0].UHeight != 1 {
		t.Fatalf("rack contents %+v, want pdu-r14-a at U 1.5 on the rear, 1 U from its own height", items)
	}

	// Another U of the same rack: the relation stays, its attributes change, and the preview says so.
	if p := e.previewUI(set, "moveWithin", tokProposer); !p.relates() {
		t.Fatalf("preview of a move within the rack lists no change: %+v", p.Result.Changes)
	}

	// A "new" device named like one already there is recognised, not created: the preview shows it
	// (no create), which is what the form warns about.
	if p := e.previewUI(set, "newNamedLikeExisting", tokProposer); p.creates("device") {
		t.Fatalf("a new device named sw-a1 would be created next to the existing one: %+v", p.Result.Changes)
	}

	// Moving a device whose placement a collector declared: the person's claim is added, the
	// collector's stays (a person withdraws only their own claims).
	e.submitUI(set, "moveObserved", tokProposer, 200)
	m := e.mountsOf(e.ids["sw"])
	if len(m) != 2 || m[e.ids["rack"]]["position"] != float64(40) || m[e.ids["rack-p"]]["_source"] != "arista:sw-a1" {
		t.Fatalf("sw-a1 mounts %+v, want the new one at U 40 and the collector's in rack-a", m)
	}

	// Moving a device the person placed: the old placement is withdrawn.
	e.submitUI(set, "moveOwn", tokProposer, 200)
	m = e.mountsOf(e.ids["pdu"])
	if len(m) != 1 || m[e.ids["rack-p"]]["position"] != float64(3) || m[e.ids["rack-p"]]["face"] != "front" {
		t.Fatalf("pdu mounts %+v, want only rack-a at U 3 front", m)
	}
	items = rackItems(t, e.page("/api/v1/racks/"+e.ids["rack-p"]+"/contents", tokReader))
	for _, it := range items {
		if it.Device.Display == "pdu-r14-a" && (it.UHeight == nil || *it.UHeight != 2) {
			t.Fatalf("pdu height %v, want the 2 U said with the move", it.UHeight)
		}
	}

	e.clk.advance(time.Hour) // a placement that ends at the instant it began has no history to read
	e.submitUI(set, "unplace", tokProposer, 200)
	if m := e.mountsOf(e.ids["pdu"]); len(m) != 0 {
		t.Fatalf("pdu still mounted after unplace: %+v", m)
	}

	// Put it back where it was: the tray lists it as taken out of rack-a, and the same partial
	// placement batch (no withdrawal) mounts it again there, so the tray is empty.
	e.clk.advance(time.Hour)
	if got := e.removedFrom("rack-p"); len(got) != 1 || got[0].Device.ID != e.ids["pdu"] || got[0].Position == nil || *got[0].Position != 3 {
		t.Fatalf("removed from rack-a %+v, want pdu-r14-a, last at U 3", got)
	}
	if p := e.previewUI(set, "putBack", tokProposer); !p.relates() {
		t.Fatalf("preview of a put back lists no change: %+v", p.Result.Changes)
	}
	put := e.submitUI(set, "putBack", tokProposer, 200)
	if put.Result.RelationsClosed != 0 {
		t.Fatalf("put back closed %d relations, want none: it withdraws nothing", put.Result.RelationsClosed)
	}
	m = e.mountsOf(e.ids["pdu"])
	if len(m) != 1 || m[e.ids["rack-p"]]["position"] != float64(3) || m[e.ids["rack-p"]]["face"] != "front" {
		t.Fatalf("pdu mounts %+v after put back, want rack-a at U 3 front", m)
	}
	if got := e.removedFrom("rack-p"); len(got) != 0 {
		t.Fatalf("removed from rack-a after the put back: %+v, want none", got)
	}

	e.submitUI(set, "renameRack", tokProposer, 200)
	r = e.get("/api/v1/objects/"+e.ids["rack"], tokReader).obj(t)
	if r["display"] != "r14b" || r["attributes"].(map[string]any)["u_height"] != float64(48) {
		t.Fatalf("renamed rack %+v", r)
	}
}

// The outcomes the form shows, each from the real API.
func TestUIBatchesOutcomes(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"user:*"}})
	set := loadUIBatches(t)
	// 202: the policy queues a person's batch; nothing changes until an approver decides.
	out := e.submitUI(set, "createRoom", tokProposer, 202)
	if out.Status != "proposed" || out.Result.ProposalID == 0 {
		t.Fatalf("outcome %+v, want proposed with a proposal id", out)
	}
	if p := e.page("/api/v1/objects?type=room", tokReader); p.Total != 0 {
		t.Fatalf("%d rooms before approval, want 0", p.Total)
	}
	// 403: a person who may propose devices only cannot declare a room.
	wantProblem(t, e.do("POST", "/api/v1/batches", tokDevProp, set.body(t, e, "createRoom")), 403, "forbidden")
	// 403: no propose action at all.
	wantStatus(t, e.do("POST", "/api/v1/batches", tokReader, set.body(t, e, "createRoom")), 403)
}

func TestUIBatchesRefusedByTheSchema(t *testing.T) {
	f := newEnv(t, reconcile.Policy{})
	set := loadUIBatches(t)
	f.ids["pdu"] = f.ids["sw"]
	f.ids["rack"] = f.ids["rack-l"]
	// 422: a face the schema does not have is refused from a person, nothing applied.
	wantProblem(t, f.do("POST", "/api/v1/batches", tokProposer, set.body(t, f, "badFace")), 422, "rejected")
	if m := f.mountsOf(f.ids["sw"]); len(m) != 1 {
		t.Fatalf("sw-a1 mounts %+v after a refused batch, want only its first", m)
	}
}

func (e *env) removedFrom(rack string) []removedItem {
	e.t.Helper()
	return decodeItems[removedItem](e.t, e.page("/api/v1/racks/"+e.ids[rack]+"/removed", tokReader))
}
