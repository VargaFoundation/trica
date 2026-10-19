package main

import (
	"net/http"
	"sync"

	"github.com/VargaFoundation/trica/internal/devicetypes"
)

// The variant rack: r22 in lyon-1, four Dell PowerEdge R330 whose chassis
// the library cannot tell (Dell reports no chassis part number), matched to
// the generic R330 by the library (ADR 0048). srv-v1, -v2 and -v3 are the
// ones the browser test chooses the chassis of, one per browser engine (they
// share one server); srv-v4 stays undecided, for the pictures.
const (
	idRackV  = 9     // r22
	firstVar = 0x500 // its devices and the R330 models
)

// r330 are the library's R330 models: the generic one and the three chassis.
var r330 = []string{"dell-poweredge-r330", "dell-poweredge-r330-4lff-cabled", "dell-poweredge-r330-4lff-hot-plug", "dell-poweredge-r330-8sff"}

// choices are the models declared through POST /batches, by device id. The
// mock otherwise writes nothing; this is the one declaration it keeps, so
// that the page can show the rack once a chassis is chosen. Each of srv-v1
// to -v3 is chosen by one test only.
type choices struct {
	mu   sync.Mutex
	slug map[string]string // device id -> declared slug
	by   map[string]string // device id -> source
}

func (c *choices) get(device string) (slug, by string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slug[device], c.by[device]
}

func (c *choices) set(device, slug, by string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slug[device], c.by[device] = slug, by
}

func buildVariants(inv *inventory) {
	inv.add(object(idRackV, "rack", "r22", idSiteL, idSiteL, obj{"name": "r22", "u_height": 12}))
	n := firstVar
	inv.modelIDs = map[string]int{}
	for _, slug := range r330 {
		m := library.BySlug(slug)
		if m == nil {
			panic("no model " + slug)
		}
		n++
		inv.add(object(n, "device_type", m.Model, 0, 0, obj{"slug": m.Slug, "vendor": m.Vendor, "model": m.Model, "u_height": m.UHeight, "full_depth": m.FullDepth}))
		inv.modelIDs[slug] = n
	}
	for i, name := range []string{"srv-v1", "srv-v2", "srv-v3", "srv-v4"} {
		n++
		inv.add(object(n, "device", name, idRackV, idSiteL, obj{"hostname": name, "role": "server", "health": "ok", "vendor": "Dell Inc.", "model": "PowerEdge R330"}))
		inv.relations = append(inv.relations, relation{"mounted_in", n, idRackV, obj{"position": float64(11 - 2*i), "face": "front"}})
		// The library's match by name, not a lab-file declaration.
		inv.links = append(inv.links, link{relation{"instance_of", n, inv.modelIDs["dell-poweredge-r330"], obj{}}, devicetypes.Source, "declared"})
	}
}

// modelOf is the device's model as the server decides it: a declared choice
// wins over the library's match and the lab file's relation.
func (a *api) modelOf(device string) obj {
	if slug, _ := a.choices.get(device); slug != "" {
		return a.inv.byID(oid(a.inv.modelIDs[slug]))
	}
	for _, r := range a.inv.all() {
		if r.typ == "instance_of" && oid(r.from) == device {
			return a.inv.byID(oid(r.to))
		}
	}
	return nil
}

func attrStr(o obj, k string) string {
	a, _ := o["attributes"].(obj)
	s, _ := a[k].(string)
	return s
}

// uncertain is the rack item's variant_uncertain, from the real matcher.
func (a *api) uncertain(d obj) (bool, int) {
	if slug, _ := a.choices.get(d["id"].(string)); slug != "" {
		return false, 0
	}
	ch := library.Choices(attrStr(d, "vendor"), attrStr(d, "model"), attrStr(d, "part_number"))
	if ch.Certain || len(ch.Candidates) == 0 {
		return false, 0
	}
	return true, len(ch.Candidates)
}

// candidates answers GET /objects/{id}/device-type-candidates like the
// server, from the real matcher over the embedded library.
func (a *api) candidates(w http.ResponseWriter, d obj) {
	if d["type"] != "device" {
		problem(w, http.StatusNotFound, "not-found", "Not a device")
		return
	}
	id := d["id"].(string)
	vendor, model, pn := attrStr(d, "vendor"), attrStr(d, "model"), attrStr(d, "part_number")
	out := obj{"device": obj{"id": id, "type": "device", "display": d["display"]}, "declarations": []obj{}, "candidates": []obj{}, "can_choose": true}
	for k, v := range map[string]string{"vendor": vendor, "model": model, "part_number": pn} {
		if v != "" {
			out[k] = v
		}
	}
	chosen, by := a.choices.get(id)
	source := "declared:lab-file"
	for _, r := range a.inv.all() {
		if r.typ == "instance_of" && oid(r.from) == id {
			source = r.source
		}
	}
	curSlug := ""
	if m := a.modelOf(id); m != nil {
		curSlug = attrStr(m, "slug")
		if chosen != "" {
			source = by
		}
		out["current"] = obj{"model": obj{"id": m["id"], "type": "device_type", "display": m["display"]}, "slug": curSlug, "model_name": attrStr(m, "model"), "source": source}
	}
	ch := library.Choices(vendor, model, pn)
	if ch.Match != nil {
		out["library_match"] = obj{"slug": ch.Match.Model.Slug, "model": ch.Match.Model.Model, "how": ch.Match.How}
	}
	if chosen != "" {
		m := a.inv.byID(oid(a.inv.modelIDs[chosen]))
		out["declarations"] = []obj{{"model": obj{"id": m["id"], "type": "device_type", "display": m["display"]}, "slug": chosen, "source": by, "last_seen": t1}}
		out["certain"], out["reason"], out["declared_by"] = true, devicetypes.ByDeclared, by
	} else {
		out["certain"], out["reason"] = ch.Certain, ch.Reason
	}
	var cands []obj
	for _, c := range ch.Candidates {
		m := c.Model
		it := obj{"slug": m.Slug, "model": m.Model, "vendor": m.Vendor, "u_height": m.UHeight, "full_depth": m.FullDepth, "how": c.How,
			"current": m.Slug == curSlug, "library_match": ch.Match != nil && ch.Match.Model.Slug == m.Slug}
		if m.Airflow != "" {
			it["airflow"] = m.Airflow
		}
		for face, img := range map[string]*devicetypes.Image{"front_image": m.FrontImage, "rear_image": m.RearImage} {
			if img != nil {
				it[face] = "/api/v1/device-types/" + m.Slug + "/" + face[:len(face)-len("_image")]
			}
		}
		cands = append(cands, it)
	}
	if cands != nil {
		out["candidates"] = cands
	}
	writeJSON(w, http.StatusOK, out)
}

// declaredChoice recognises the batch the chooser sends (a device by id, a
// device_type by slug, instance_of between them) and keeps it. It reports
// whether the batch was one.
func (a *api) declaredChoice(objects map[string]map[string]string, rels []batchRel, preview bool) bool {
	if len(rels) != 1 || rels[0].Type != "instance_of" {
		return false
	}
	device := rels[0].From.Identity["id"]
	slug := objects[rels[0].To.Ref]["slug"]
	if device == "" || a.inv.modelIDs[slug] == 0 || a.inv.byID(device) == nil {
		return false
	}
	if !preview {
		a.choices.set(device, slug, "e2e")
	}
	return true
}
