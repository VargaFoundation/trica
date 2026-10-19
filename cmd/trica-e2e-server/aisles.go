package main

import (
	"net/http"
	"sort"
	"sync"

	"github.com/VargaFoundation/trica/internal/airflow"
)

// The aisles room (ADR 0049): room-b in paris-1, four racks around a cold
// aisle. r30 and r31 face each other across row 2, both fronts on the cold
// aisle; r32 says its front is on the hot aisle and r33, facing it, says the
// same tile is cold, so the floor plan flags them. r30 holds the airflow
// cases: a server mounted the right way, an Arista -F switch mounted on the
// rear (port-side intake on the hot aisle: reversed), its -R twin on the rear
// (correct), a -R switch whose collector reports forward airflow (its model
// is contradicted), a -F switch whose fan trays are -R, and a PDU nobody knows
// the airflow of. r34, r35 and r36 (lyon-1) say nothing yet: the browser test
// declares the aisle of one each, one per browser engine.
const (
	idRoomB    = 0x800
	idR30      = 0x801
	idR31      = 0x802
	idR32      = 0x803
	idR33      = 0x804
	idR34      = 0x805
	idR35      = 0x806
	idR36      = 0x807
	firstAisle = 0x810 // the devices, their models and parts
)

// aisleDecl is a rack's front_aisle declared through POST /batches: the mock
// otherwise writes nothing; this is the one declaration it keeps (with the
// chassis choice), so that the page shows the rack once its aisle is said.
type aisleDecl struct {
	mu    sync.Mutex
	front map[string]string // rack id -> cold, hot, or "" when withdrawn
}

func (d *aisleDecl) get(rack string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.front[rack]
	return v, ok
}

func (d *aisleDecl) set(rack, v string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.front[rack] = v
}

func buildAisles(inv *inventory) {
	inv.add(object(idRoomB, "room", "room-b", idSite, idSite, obj{"name": "room-b", "width_mm": 3000, "depth_mm": 3000, "tile_mm": 600}))
	rack := func(id int, name string, x, y int, facing, front string) {
		a := obj{"name": name, "u_height": 42, "floor_x": x, "floor_y": y, "facing": facing}
		if front != "" {
			a["front_aisle"] = front
		}
		inv.add(object(id, "rack", name, idRoomB, idSite, a))
	}
	rack(idR30, "r30", 1, 1, "south", "cold")
	rack(idR31, "r31", 1, 3, "north", "cold")
	rack(idR32, "r32", 2, 1, "south", "hot")
	rack(idR33, "r33", 2, 3, "north", "cold")
	for i, name := range []string{"r34", "r35", "r36"} {
		inv.add(object(idR34+i, "rack", name, idSiteL, idSiteL, obj{"name": name, "u_height": 12}))
	}

	n := firstAisle
	models := map[string]int{}
	model := func(slug string) int {
		if id, ok := models[slug]; ok {
			return id
		}
		m := library.BySlug(slug)
		if m == nil {
			panic("no model " + slug)
		}
		n++
		af := m.Airflow
		if af == "" {
			af = "front_to_rear" // a 1U server: the library's file does not say
		}
		inv.add(object(n, "device_type", m.Model, 0, 0, obj{"slug": m.Slug, "vendor": m.Vendor, "model": m.Model, "u_height": m.UHeight, "full_depth": m.FullDepth, "airflow": af}))
		models[slug] = n
		return n
	}
	device := func(name, role, site string, rack int, attrs obj) int {
		n++
		attrs["hostname"], attrs["role"], attrs["health"] = name, role, "ok"
		s := idSite
		if site == "lyon" {
			s = idSiteL
		}
		inv.add(object(n, "device", name, rack, s, attrs))
		return n
	}
	part := func(parent int, typ, name, af string) {
		n++
		inv.add(object(n, typ, name, parent, idSite, obj{"name": name, "health": "ok", "airflow": af}))
	}

	srv := device("srv-30", "server", "", idR30, obj{"vendor": "Dell Inc."})
	inv.mount(srv, idR30, 20, "front", model("dell-poweredge-r630-10sff"))
	swF := device("sw-30f", "switch", "", idR30, obj{"vendor": "Arista"})
	inv.mount(swF, idR30, 40, "rear", model("arista-dcs-7050sx-64-f"))
	swR := device("sw-30r", "switch", "", idR30, obj{"vendor": "Arista"})
	inv.mount(swR, idR30, 39, "rear", model("arista-dcs-7050sx-64-r"))
	// Declared -R, but the switch reports forward airflow (EOS airflowDirection).
	swX := device("sw-30x", "switch", "", idR30, obj{"vendor": "Arista", "airflow": "front_to_rear"})
	inv.mount(swX, idR30, 38, "rear", model("arista-dcs-7050sx-64-r"))
	// -F mounted on the front, the right way; but its fan trays are -R.
	swP := device("sw-30p", "switch", "", idR30, obj{"vendor": "Arista", "airflow": "front_to_rear"})
	inv.mount(swP, idR30, 37, "front", model("arista-dcs-7050sx-64-f"))
	part(swP, "psu", "PowerSupply1", "front_to_rear")
	part(swP, "psu", "PowerSupply2", "front_to_rear")
	part(swP, "fan", "FanTray1", "rear_to_front")
	part(swP, "fan", "FanTray2", "rear_to_front")
	pdu := device("pdu-30", "pdu", "", idR30, obj{"u_height": 1})
	inv.relations = append(inv.relations, relation{"mounted_in", pdu, idR30, obj{"position": float64(2), "face": "rear"}})

	srv31 := device("srv-31", "server", "", idR31, obj{"vendor": "Dell Inc."})
	inv.mount(srv31, idR31, 20, "front", model("dell-poweredge-r630-10sff"))

	for i, name := range []string{"srv-34", "srv-35", "srv-36"} {
		d := device(name, "server", "lyon", idR34+i, obj{"vendor": "Dell Inc."})
		inv.mount(d, idR34+i, 6, "front", model("dell-poweredge-r630-10sff"))
	}
}

// withAisle is the object as the server would answer it once a front_aisle
// was declared or withdrawn through the mock.
func (a *api) withAisle(o obj) obj {
	if o == nil || o["type"] != "rack" {
		return o
	}
	v, ok := a.aisles.get(o["id"].(string))
	if !ok {
		return o
	}
	out := obj{}
	for k, x := range o {
		out[k] = x
	}
	attrs := obj{}
	for k, x := range o["attributes"].(obj) {
		attrs[k] = x
	}
	if v == "" {
		delete(attrs, "front_aisle")
	} else {
		attrs["front_aisle"] = v
	}
	out["attributes"] = attrs
	return out
}

// airflowInput gathers what the server's check reads about a device.
func (a *api) airflowInput(d obj) (airflow.Input, obj, obj, string) {
	id := d["id"].(string)
	in := airflow.Input{Device: attrStr(d, "airflow")}
	var rack obj
	var face string
	for _, r := range a.inv.relations {
		if r.typ == "mounted_in" && oid(r.from) == id {
			rack = a.withAisle(a.inv.byID(oid(r.to)))
			face, _ = r.attrs["face"].(string)
			break
		}
	}
	if rack != nil {
		in.Mounted, in.Face, in.FrontAisle = true, face, attrStr(rack, "front_aisle")
	}
	m := a.modelOf(id)
	if m != nil {
		in.Model = attrStr(m, "airflow")
	}
	for _, o := range a.inv.objects {
		if o["parent_id"] == id && (o["type"] == "psu" || o["type"] == "fan") {
			if af := attrStr(o, "airflow"); af != "" {
				in.Parts = append(in.Parts, airflow.Part{ID: o["id"].(string), Name: o["display"].(string), Type: o["type"].(string), Airflow: af})
			}
		}
	}
	sort.SliceStable(in.Parts, func(i, j int) bool { return in.Parts[i].Name < in.Parts[j].Name })
	return in, rack, m, face
}

func partsJSON(ps []airflow.Part) []obj {
	out := []obj{}
	for _, p := range ps {
		out = append(out, obj{"part": obj{"id": p.ID, "type": p.Type, "display": p.Name}, "airflow": p.Airflow})
	}
	return out
}

// checkJSON is the AirflowCheck of the API, from the real check.
func checkJSON(r airflow.Result) obj {
	c := obj{"status": r.Status, "reason": r.Reason}
	for k, v := range map[string]string{"airflow": r.Airflow, "airflow_from": r.From, "intake": r.Intake, "cold_face": r.ColdFace} {
		if v != "" {
			c[k] = v
		}
	}
	var ws []obj
	for _, w := range r.Warnings {
		o := obj{"code": w.Code}
		for k, v := range map[string]string{"airflow": w.Airflow, "from": w.From, "model": w.Model} {
			if v != "" {
				o[k] = v
			}
		}
		if w.ModelDeclared {
			o["model_declared"] = true
		}
		if len(w.Parts) > 0 {
			o["parts"] = partsJSON(w.Parts)
		}
		ws = append(ws, o)
	}
	if ws != nil {
		c["warnings"] = ws
	}
	return c
}

// rackAirflow is a rack item's airflow, as the server computes it.
func (a *api) rackAirflow(d obj) obj {
	in, _, _, _ := a.airflowInput(d)
	return checkJSON(airflow.Check(in))
}

// objectAirflow answers GET /objects/{id}/airflow like the server.
func (a *api) objectAirflow(w http.ResponseWriter, d obj) {
	if d["type"] != "device" {
		problem(w, http.StatusNotFound, "not-found", "Not a device")
		return
	}
	in, rack, m, face := a.airflowInput(d)
	out := obj{"device": obj{"id": d["id"], "type": "device", "display": d["display"]}, "parts": partsJSON(in.Parts), "check": checkJSON(airflow.Check(in))}
	if rack != nil {
		out["rack"] = obj{"id": rack["id"], "type": "rack", "display": rack["display"]}
		if face == "front" || face == "rear" {
			out["face"] = face
		}
		if in.FrontAisle != "" {
			out["front_aisle"] = in.FrontAisle
		}
	}
	if m != nil {
		out["model"] = obj{"id": m["id"], "type": "device_type", "display": m["display"]}
		if in.Model != "" {
			out["model_airflow"] = in.Model
		}
	}
	if in.Device != "" {
		out["device_airflow"] = in.Device
	}
	writeJSON(w, http.StatusOK, out)
}

// batchObject is an object of a batch body, as the aisle chooser sends it.
type batchObject struct {
	Ref        string            `json:"ref"`
	Type       string            `json:"type"`
	Identity   map[string]string `json:"identity"`
	Attributes map[string]any    `json:"attributes"`
	Withdraw   []string          `json:"withdraw"`
}

// declaredAisle recognises the batch the aisle chooser sends (a rack by id,
// front_aisle said or withdrawn) and keeps it on submit. It answers the
// preview or the submit itself and reports whether the batch was one.
func (a *api) declaredAisle(w http.ResponseWriter, objects []batchObject, rels int, preview bool) bool {
	if len(objects) != 1 || rels != 0 || objects[0].Type != "rack" {
		return false
	}
	o := objects[0]
	rack := a.withAisle(a.inv.byID(o.Identity["id"]))
	if rack == nil {
		return false
	}
	v, said := o.Attributes["front_aisle"].(string)
	withdrawn := len(o.Withdraw) == 1 && o.Withdraw[0] == "front_aisle"
	if !said && !withdrawn {
		return false
	}
	if withdrawn {
		v = ""
	}
	change := obj{"op": "set_attr", "object_id": rack["id"], "type": "rack", "key": "front_aisle"}
	if before := attrStr(rack, "front_aisle"); before != "" {
		change["before"] = before
	}
	if v != "" {
		change["after"] = v
	}
	result := obj{"created": 0, "values_changed": 1, "relations_opened": 0, "relations_closed": 0, "objects": obj{o.Ref: rack["id"]}, "changes": []obj{change}}
	if preview {
		writeJSON(w, http.StatusOK, obj{"source": "e2e", "result": result})
		return true
	}
	a.aisles.set(rack["id"].(string), v)
	writeJSON(w, http.StatusOK, obj{"source": "e2e", "status": "applied", "result": result})
	return true
}
