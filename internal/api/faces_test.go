package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The faces of the server model: bays named by box on the front; power
// supplies, the BMC port, two on-board ports and a PCIe slot on the rear.
var (
	srvFront = &api.FaceLayout{Width: 482.6, Height: 44.45, Origin: "trica", Spots: []api.FaceSpot{
		{Kind: "bay", Name: "1:1", X: 10, Y: 5, W: 20, H: 10},
		{Kind: "bay", Name: "1:2", X: 31, Y: 5, W: 20, H: 10},
	}}
	srvRear = &api.FaceLayout{Width: 482.6, Height: 44.45, Origin: "trica", Spots: []api.FaceSpot{
		{Kind: "psu", Name: "1", X: 400, Y: 2, W: 40, H: 40},
		{Kind: "psu", Name: "2", X: 441, Y: 2, W: 40, H: 40},
		{Kind: "port", Name: "bmc", X: 100, Y: 20, W: 10, H: 10},
		{Kind: "port", Name: "nic1", X: 112, Y: 20, W: 10, H: 10},
		{Kind: "port", Name: "nic2", X: 124, Y: 20, W: 10, H: 10},
		{Kind: "slot", Name: "1", X: 20, Y: 2, W: 100, H: 15},
		{Kind: "fan", Name: "1", X: 200, Y: 2, W: 20, H: 20},
	}}
)

type facesAnswer struct {
	Model     *api.ObjectRef `json:"model"`
	ModelName string         `json:"model_name"`
	Faces     []struct {
		Face         string         `json:"face"`
		Image        string         `json:"image"`
		Width        float64        `json:"width"`
		LayoutOrigin string         `json:"layout_origin"`
		Spots        []api.FaceSpot `json:"spots"`
	} `json:"faces"`
	Parts []struct {
		Part     api.ObjectView  `json:"part"`
		Kind     string          `json:"kind"`
		Face     string          `json:"face"`
		Spot     string          `json:"spot"`
		Unplaced string          `json:"unplaced"`
		Also     []api.ObjectRef `json:"also"`
	} `json:"parts"`
	Truncated bool `json:"truncated"`
}

// srvWithParts declares an HPE-like server in rack-b (lyon-1) with the parts
// and the names its BMC and its OS give them, then lets one power supply go
// absent.
func (e *env) srvWithParts() string {
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("dt", "device_type").Identify("slug", "acme-srv").Set("model", "SRV 360").Set("u_height", 1)
	e.apply(b)
	observe := func(withOld bool) {
		c := sdk.NewBatch("redfish:srv-f", sdk.ScopePartial)
		c.Object("srv", "device").Identify("serial", "SRV-F-SN").Identify("name", "srv-f").Set("role", "server")
		c.Object("ctl", "module").Identify("name", "Smart Array P440ar").In("srv").Set("kind", "raid_controller")
		// iLO 4 writes the bay as port:box:bay; iLO 5 as Port=1I:Box=1:Bay=2.
		c.Object("d1", "disk").Identify("serial", "D1").In("ctl").Set("name", "Disk 1").Set("slot", "1I:1:1").Set("health", "ok")
		c.Object("d2", "disk").Identify("serial", "D2").In("ctl").Set("name", "Disk 2").Set("slot", "Port=1I:Box=1:Bay=2").Set("health", "critical")
		// A bay the faces do not have, and the OS's name of a disk: not placed.
		c.Object("d3", "disk").Identify("serial", "D3").In("ctl").Set("name", "Disk 3").Set("slot", "1I:1:23")
		c.Object("sda", "disk").Identify("serial", "D4").In("srv").Set("name", "sda")
		// iLO 5 numbers power supplies from 0.
		c.Object("p0", "psu").Identify("name", "HpeServerPowerSupply 0").In("srv").Set("health", "ok").Set("input_w", 120.5)
		c.Object("p1", "psu").Identify("name", "HpeServerPowerSupply 1").In("srv").Set("health", "warning")
		if withOld {
			c.Object("p7", "psu").Identify("name", "HpeServerPowerSupply 7").In("srv")
		}
		// The fan of the chassis has its spot; the fan of a power supply
		// turns with it and is not listed.
		c.Object("fan", "fan").Identify("name", "Fan 1").In("srv")
		c.Object("pfan", "fan").Identify("name", "Fan 2").In("p0")
		c.Object("ilo", "interface").Identify("name", "iLO").In("srv").Set("kind", "bmc")
		c.Object("eno1", "interface").Identify("name", "eno1").In("srv").Set("kind", "physical").Set("oper_status", "up").Set("mac", "02:00:00:00:00:01")
		c.Object("ens1", "interface").Identify("name", "ens1d1").In("srv").Set("kind", "physical")
		c.Object("eno49", "interface").Identify("name", "eno49").In("srv").Set("mac", "02:00:00:00:00:49")
		// The BMC's names of the same ports (iLO numbers them): eno1's MAC
		// address shares its spot, eno49's is listed with it, once; a number
		// without a MAC address says nothing.
		c.Object("hpe1", "interface").Identify("name", "1").In("srv").Set("kind", "physical").Set("mac", "02:00:00:00:00:01")
		c.Object("hpe6", "interface").Identify("name", "6").In("srv").Set("kind", "physical").Set("mac", "02:00:00:00:00:49")
		c.Object("hpe5", "interface").Identify("name", "5").In("srv").Set("kind", "physical")
		c.Object("bond", "interface").Identify("name", "bond0").In("srv").Set("kind", "lag")
		// A processor has no spot on a face.
		c.Object("cpu", "cpu").Identify("name", "CPU 1").In("srv")
		c.Relations = append(c.Relations,
			&sdk.Relation{Type: "instance_of", From: sdk.Ref("srv"), To: sdk.Endpoint{Type: "device_type", Identity: map[string]string{"slug": "acme-srv"}}},
			&sdk.Relation{Type: "mounted_in", From: sdk.Ref("srv"), To: sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: e.ids["rack-l"]}}, Attributes: map[string]any{"position": 3}})
		e.apply(c)
	}
	observe(true)
	e.clk.advance(time.Hour)
	observe(false)
	if _, err := e.eng.SweepAbsent(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return e.ids["srv"]
}

func (e *env) faces(id, tok string) facesAnswer {
	e.t.Helper()
	r := e.get("/api/v1/objects/"+id+"/faces", tok)
	wantStatus(e.t, r, 200)
	var a facesAnswer
	r.json(e.t, &a)
	return a
}

// Each part goes in the spot its name says: a disk in its bay (by box), a
// power supply counted from 1, the BMC port, an on-board port, a card's port
// in its slot. A name the rules do not read leaves the part unplaced, never
// guessed; kinds the faces have no spot for, aggregates and absent parts are
// left out.
func TestObjectFacesPlaceTheParts(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	id := e.srvWithParts()
	a := e.faces(id, tokReader)
	if a.Model == nil || a.Model.Display != "SRV 360" || a.ModelName != "SRV 360" {
		t.Fatalf("model %+v %q, want SRV 360", a.Model, a.ModelName)
	}
	if len(a.Faces) != 2 || a.Faces[0].Face != "front" || a.Faces[0].Image != "/api/v1/device-types/acme-srv/front" ||
		len(a.Faces[0].Spots) != 2 || a.Faces[1].Face != "rear" || a.Faces[1].Width != 482.6 || a.Faces[1].LayoutOrigin != "trica" {
		t.Fatalf("faces %+v, want front then rear with their spots", a.Faces)
	}
	got := map[string]string{}
	for _, p := range a.Parts {
		name, _ := p.Part.Attributes["name"].(string)
		got[name] = p.Kind + " " + p.Face + " " + p.Spot + " " + p.Unplaced
		for _, o := range p.Also {
			got[name] += " also:" + o.Display
		}
	}
	want := map[string]string{
		"Disk 1":                 "bay front 1:1 ",
		"Disk 2":                 "bay front 1:2 ",
		"Disk 3":                 "bay   no_spot",
		"sda":                    "bay   os",
		"HpeServerPowerSupply 0": "psu rear 1 ",
		"HpeServerPowerSupply 1": "psu rear 2 ",
		"iLO":                    "port rear bmc ",
		"eno1":                   "port rear nic1 ",
		"1":                      "port rear nic1 ",
		"ens1d1":                 "slot rear 1 ",
		"eno49":                  "port   os also:6",
		"5":                      "port   unread",
		"Fan 1":                  "fan rear 1 ",
	}
	if len(got) != len(want) {
		t.Errorf("parts %v, want %d", got, len(want))
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: %q, want %q", k, got[k], w)
		}
	}
	for _, p := range a.Parts {
		if p.Part.Attributes["name"] == "HpeServerPowerSupply 0" && p.Part.Attributes["input_w"] != 120.5 {
			t.Errorf("a part carries its attributes for the page: %v", p.Part.Attributes)
		}
	}
	if a.Truncated {
		t.Error("truncated with a dozen parts")
	}
}

// A caller sees the faces of what it may read: no part it may not read, no
// model it may not read, no device of a site it is not granted.
func TestObjectFacesFollowTheGrants(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	id := e.srvWithParts()
	if a := e.faces(id, tokRackView); len(a.Faces) != 2 || len(a.Parts) != 0 {
		t.Errorf("racks, devices and models only: %d faces, %d parts, want the faces and no part", len(a.Faces), len(a.Parts))
	}
	if a := e.faces(id, tokDevices); a.Model != nil || len(a.Faces) != 0 || len(a.Parts) != 0 {
		t.Errorf("devices only: %+v, want no model, so no face", a)
	}
	wantProblem(t, e.get("/api/v1/objects/"+id+"/faces", tokSiteParis), 403, "forbidden")
	wantProblem(t, e.get("/api/v1/objects/"+id+"/faces", ""), 401, "unauthenticated")
	wantProblem(t, e.get("/api/v1/objects/00000000-0000-0000-0000-000000000000/faces", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/objects/nope/faces", tokReader), 400, "invalid-request")
}

// A model with an image and no layout shows its face and places nothing; a
// device without a model has no face.
func TestObjectFacesWithoutLayoutOrModel(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.furnish()
	a := e.faces(e.ids["sw2"], tokReader)
	if len(a.Faces) != 1 || a.Faces[0].Face != "front" || len(a.Faces[0].Spots) != 0 || a.Faces[0].LayoutOrigin != "" || len(a.Parts) != 0 {
		t.Fatalf("sw-a2: %+v, want the front image alone", a)
	}
	a = e.faces(e.ids["sw"], tokReader)
	if a.Model != nil || len(a.Faces) != 0 || len(a.Parts) != 0 || !strings.HasPrefix(string(e.get("/api/v1/objects/"+e.ids["sw"]+"/faces", tokReader).Body), `{"faces":[],"parts":[]`) {
		t.Fatalf("sw-a1 without a model: %+v, want empty lists, not null", a)
	}
}
