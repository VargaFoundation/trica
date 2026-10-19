package api_test

import (
	"encoding/json"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

type airflowCheck struct {
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	Airflow     string `json:"airflow"`
	AirflowFrom string `json:"airflow_from"`
	Intake      string `json:"intake"`
	ColdFace    string `json:"cold_face"`
	Warnings    []struct {
		Code    string `json:"code"`
		Airflow string `json:"airflow"`
		From    string `json:"from"`
		Model   string `json:"model"`
		Parts   []struct {
			Part struct {
				Display string `json:"display"`
				Type    string `json:"type"`
			} `json:"part"`
			Airflow string `json:"airflow"`
		} `json:"parts"`
	} `json:"warnings"`
}

// cool furnishes rack-a (Paris) with what the airflow check reads: a
// front-to-rear server on the front, a port-side intake switch (-F) and a
// port-side exhaust switch (-R) both mounted on the rear, a switch whose
// collector reports the airflow its model contradicts, a server with one
// power supply turned the other way, and a PDU nobody knows the airflow of.
func (e *env) cool() {
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("srv-t", "device_type").Identify("slug", "acme-srv1").Set("model", "SRV1").Set("u_height", 1).Set("airflow", "front_to_rear")
	b.Object("swf-t", "device_type").Identify("slug", "acme-sw48-f").Set("model", "SW48-F").Set("u_height", 1).Set("airflow", "front_to_rear")
	b.Object("swr-t", "device_type").Identify("slug", "acme-sw48-r").Set("model", "SW48-R").Set("u_height", 1).Set("airflow", "rear_to_front")
	b.Object("srv1", "device").Identify("name", "srv-cool").Set("role", "server")
	b.Object("swf", "device").Identify("name", "sw-port-intake").Set("role", "switch")
	b.Object("swr", "device").Identify("name", "sw-port-exhaust").Set("role", "switch")
	b.Object("swx", "device").Identify("name", "sw-swapped").Set("role", "switch")
	b.Object("srv2", "device").Identify("name", "srv-psu").Set("role", "server")
	b.Object("psu1", "psu").Identify("name", "PSU1").In("srv2").Set("airflow", "front_to_rear")
	b.Object("psu2", "psu").Identify("name", "PSU2").In("srv2").Set("airflow", "rear_to_front")
	b.Object("pdu", "device").Identify("name", "pdu-x").Set("role", "pdu")
	rack := sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: e.ids["rack-p"]}}
	mount := func(ref string, u int, face string) {
		b.Relations = append(b.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref(ref), To: rack, Attributes: map[string]any{"position": u, "face": face}})
	}
	model := func(ref, t string) {
		b.Relations = append(b.Relations, &sdk.Relation{Type: "instance_of", From: sdk.Ref(ref), To: sdk.Ref(t)})
	}
	mount("srv1", 10, "front")
	model("srv1", "srv-t")
	mount("swf", 40, "rear")
	model("swf", "swf-t")
	mount("swr", 41, "rear")
	model("swr", "swr-t")
	mount("swx", 42, "rear")
	model("swx", "swr-t")
	mount("srv2", 12, "front")
	model("srv2", "srv-t")
	mount("pdu", 2, "rear")
	e.apply(b)
	// The switch's collector reports the airflow its fans blow: not its model's.
	c := sdk.NewBatch("arista:sw-swapped", sdk.ScopePartial)
	c.Object("swx", "device").Identify("name", "sw-swapped").Set("airflow", "front_to_rear")
	e.apply(c)
}

func (e *env) airflowByDevice() map[string]airflowCheck {
	e.t.Helper()
	out := map[string]airflowCheck{}
	for _, raw := range e.page("/api/v1/racks/"+e.ids["rack-p"]+"/contents", tokReader).Items {
		var it struct {
			Device struct {
				Display string `json:"display"`
			} `json:"device"`
			Airflow *airflowCheck `json:"airflow"`
		}
		if err := json.Unmarshal(raw, &it); err != nil {
			e.t.Fatal(err)
		}
		if it.Airflow == nil {
			e.t.Fatalf("%s: no airflow check in the rack contents", it.Device.Display)
		}
		out[it.Device.Display] = *it.Airflow
	}
	return out
}

// The front of the rack faces the cold aisle once someone says so from the
// rack page; every mounted device is then checked against it, and the
// declaration is the person's, kept, changed and withdrawn like any other.
func TestAisleDeclarationDrivesTheAirflowCheck(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cool()
	set := loadUIBatches(t)
	e.ids["rack"] = e.ids["rack-p"]

	// Nothing said yet: every check is unknown for want of the aisle.
	before := e.airflowByDevice()
	if c := before["srv-cool"]; c.Status != "unknown" || c.Reason != "aisle_not_set" || c.Intake != "front" {
		t.Fatalf("srv-cool before the aisle is said: %+v", c)
	}

	out := e.submitUI(set, "frontAisleCold", tokProposer, 200)
	if out.Status != "applied" || out.Source != "user:carol" {
		t.Fatalf("declare the cold aisle: %+v", out)
	}
	r := e.get("/api/v1/objects/"+e.ids["rack-p"], tokReader).obj(t)
	if a := r["attributes"].(map[string]any); a["front_aisle"] != "cold" {
		t.Fatalf("rack attributes %+v, want front_aisle cold", a)
	}
	var prov struct {
		Attributes []struct {
			Key    string `json:"key"`
			Source string `json:"source"`
		} `json:"attributes"`
	}
	e.get("/api/v1/objects/"+e.ids["rack-p"]+"/provenance", tokReader).json(t, &prov)
	said := false
	for _, a := range prov.Attributes {
		said = said || (a.Key == "front_aisle" && a.Source == "user:carol")
	}
	if !said {
		t.Fatalf("provenance %+v, want front_aisle said by user:carol", prov.Attributes)
	}

	got := e.airflowByDevice()
	want := map[string][2]string{
		"srv-cool":        {"ok", "intake_on_cold_aisle"},
		"sw-port-intake":  {"mismatch", "intake_on_hot_aisle"}, // -F on the rear: ports, and intake, on the hot aisle
		"sw-port-exhaust": {"ok", "intake_on_cold_aisle"},      // -R on the rear: takes its air in at the rack's front
		"sw-swapped":      {"mismatch", "intake_on_hot_aisle"}, // its model says -R, its fans blow -F
		"srv-psu":         {"mismatch", "part_intake_on_hot_aisle"},
		"pdu-x":           {"unknown", "airflow_unknown"},
	}
	for name, w := range want {
		if c := got[name]; c.Status != w[0] || c.Reason != w[1] || (c.Status != "unknown" && c.ColdFace != "front") {
			t.Errorf("%s: %s/%s cold face %q, want %s/%s on the front", name, c.Status, c.Reason, c.ColdFace, w[0], w[1])
		}
	}
	if c := got["sw-swapped"]; c.AirflowFrom != "device" || len(c.Warnings) != 1 || c.Warnings[0].Code != "airflow_contradicts_model" || c.Warnings[0].Model != "rear_to_front" {
		t.Errorf("sw-swapped: %+v, want the device's own airflow used and the contradiction with its model said", c)
	}
	if c := got["srv-psu"]; len(c.Warnings) != 1 || c.Warnings[0].Code != "parts_disagree" || len(c.Warnings[0].Parts) != 1 || c.Warnings[0].Parts[0].Part.Display != "PSU2" {
		t.Errorf("srv-psu: %+v, want PSU2 named as turned the other way", c)
	}

	// The rack turned around: the front faces the hot aisle.
	e.submitUI(set, "frontAisleHot", tokProposer, 200)
	got = e.airflowByDevice()
	if got["srv-cool"].Status != "mismatch" || got["sw-port-intake"].Status != "ok" || got["sw-port-intake"].ColdFace != "rear" {
		t.Fatalf("after turning the rack: %+v", got)
	}

	// Not set: the person withdraws their own claim, nothing is left.
	e.submitUI(set, "frontAisleUnset", tokProposer, 200)
	r = e.get("/api/v1/objects/"+e.ids["rack-p"], tokReader).obj(t)
	if a, _ := r["attributes"].(map[string]any); a["front_aisle"] != nil {
		t.Fatalf("front_aisle %v after withdrawal, want none", a["front_aisle"])
	}
	if c := e.airflowByDevice()["srv-cool"]; c.Reason != "aisle_not_set" {
		t.Fatalf("srv-cool after withdrawal: %+v", c)
	}
}

// A value the schema does not know is refused: the aisle is cold or hot.
func TestAisleRefusesAnotherValue(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := map[string]any{"source": "ui", "scope": "partial", "confidence": "declared", "observed_at": e.clk.now().Format("2006-01-02T15:04:05Z07:00"),
		"objects": []any{map[string]any{"ref": "obj", "type": "rack", "identity": map[string]any{"id": e.ids["rack-p"]}, "attributes": map[string]any{"front_aisle": "lukewarm"}}}}
	wantProblem(t, e.do("POST", "/api/v1/batches", tokProposer, b), 422, "rejected")
}

func TestObjectAirflow(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cool()
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("rack", "rack").Identify(sdk.IDKey, e.ids["rack-p"]).Set("front_aisle", "cold")
	e.apply(b)

	var d struct {
		Device        ObjectRefT  `json:"device"`
		Rack          *ObjectRefT `json:"rack"`
		Face          string      `json:"face"`
		FrontAisle    string      `json:"front_aisle"`
		Model         *ObjectRefT `json:"model"`
		ModelAirflow  string      `json:"model_airflow"`
		DeviceAirflow string      `json:"device_airflow"`
		Parts         []struct {
			Airflow string `json:"airflow"`
		} `json:"parts"`
		Check airflowCheck `json:"check"`
	}
	e.get("/api/v1/objects/"+e.ids["swx"]+"/airflow", tokReader).json(t, &d)
	if d.Rack == nil || d.Rack.Display != "rack-a" || d.Face != "rear" || d.FrontAisle != "cold" || d.Model == nil || d.Model.Display != "SW48-R" ||
		d.ModelAirflow != "rear_to_front" || d.DeviceAirflow != "front_to_rear" || d.Check.Status != "mismatch" || len(d.Check.Warnings) != 1 {
		t.Fatalf("airflow of sw-swapped: %+v", d)
	}
	e.get("/api/v1/objects/"+e.ids["srv2"]+"/airflow", tokReader).json(t, &d)
	if len(d.Parts) != 2 || d.Check.Reason != "part_intake_on_hot_aisle" {
		t.Fatalf("airflow of srv-psu: %+v", d)
	}
	e.get("/api/v1/objects/"+e.ids["srv"]+"/airflow", tokReader).json(t, &d)
	if d.Rack == nil || d.Check.Status != "unknown" || d.Check.Reason != "airflow_unknown" {
		t.Fatalf("airflow of srv-01 (rack-b, no airflow known): %+v", d.Check)
	}
	// A device in no rack: unknown, said why.
	c := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	c.Confidence = sdk.Declared
	c.Object("loose", "device").Identify("name", "loose-1").Set("airflow", "front_to_rear")
	e.apply(c)
	var loose struct {
		Rack  *ObjectRefT  `json:"rack"`
		Check airflowCheck `json:"check"`
	}
	e.get("/api/v1/objects/"+e.ids["loose"]+"/airflow", tokReader).json(t, &loose)
	if loose.Rack != nil || loose.Check.Reason != "not_mounted" || loose.Check.Airflow != "front_to_rear" {
		t.Fatalf("airflow of a device in no rack: %+v", loose)
	}

	wantProblem(t, e.get("/api/v1/objects/"+e.ids["rack-p"]+"/airflow", tokReader), 404, "not-found")
	wantStatus(t, e.get("/api/v1/objects/"+e.ids["swx"]+"/airflow", tokSiteParis), 200)
	wantStatus(t, e.get("/api/v1/objects/"+e.ids["srv"]+"/airflow", tokSiteParis), 403)
}

// ObjectRefT is an object reference as the API writes it.
type ObjectRefT struct {
	ID      string `json:"id"`
	Display string `json:"display"`
}
