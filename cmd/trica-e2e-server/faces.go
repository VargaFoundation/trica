package main

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/internal/devicetypes"
)

// The faces rack: r20 in lyon-1, every way a face is drawn. Models from the
// embedded device-type library, three with images (front and rear, front
// only), the others drawn by role; devices without a model; every health.
// Names are invented, like the rest of the mock.
const (
	idRackF   = 7     // r20
	firstFace = 0x400 // its devices, models and parts
	idRackD   = 8     // r21, the faces trica drew
	firstDraw = 0x480 // its devices and models
)

// library is the embedded device-type library, for the images.
var library = func() *devicetypes.Index {
	models, err := devicetypes.Default()
	if err != nil {
		panic(err)
	}
	return devicetypes.NewIndex(models)
}()

func buildFaces(inv *inventory) {
	inv.add(object(idRackF, "rack", "r20", idSiteL, idSiteL, obj{"name": "r20", "u_height": 24}))
	n := firstFace
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
		inv.add(object(n, "device_type", m.Model, 0, 0, obj{"slug": m.Slug, "vendor": m.Vendor, "model": m.Model, "u_height": m.UHeight, "full_depth": m.FullDepth}))
		models[slug] = n
		return n
	}
	device := func(name, role, health string, attrs obj) int {
		n++
		a := obj{"hostname": name, "role": role}
		if health != "" {
			a["health"] = health
		}
		for k, v := range attrs {
			a[k] = v
		}
		inv.add(object(n, "device", name, idRackF, idSiteL, a))
		return n
	}
	for _, d := range []struct {
		name, role, health, slug, face string
		u                              float64
	}{
		{"sw-f1", "switch", "ok", "arista-dcs-7050sx-64-r", "front", 22},
		{"sw-f2", "switch", "warning", "arista-dcs-7280se-64-f", "front", 21},
		{"sw-f3", "switch", "ok", "arista-dcs-7050tx-48", "front", 20},
		{"fw-f1", "firewall", "ok", "generic-1u-pfsense-appliance", "front", 19},
		{"srv-f10", "server", "ok", "hpe-proliant-dl380-gen10", "front", 14},
		{"srv-f11", "server", "critical", "hpe-proliant-dl380-gen9", "front", 12},
		{"srv-f12", "server", "", "hpe-proliant-dl360-gen9", "front", 11},
	} {
		id := device(d.name, d.role, d.health, nil)
		inv.mount(id, idRackF, d.u, d.face, model(d.slug))
		if d.name == "srv-f10" {
			// Healthy itself, a failed power supply: the parts make it critical.
			n++
			inv.add(object(n, "psu", "PSU2", id, idSiteL, obj{"name": "PSU2", "health": "critical"}))
		}
	}
	// Devices nobody knows the model of carry their own height.
	for _, d := range []struct {
		name, role, face string
		u, height        float64
	}{
		{"pp-f1", "patch_panel", "front", 23, 1},
		{"nas-f1", "nas", "front", 8, 2},
		{"ups-f1", "ups", "front", 1, 2},
		{"pdu-f1", "pdu", "rear", 18, 1},
		{"kvm-f1", "kvm", "front", 17, 1},
	} {
		id := device(d.name, d.role, "", obj{"u_height": d.height})
		inv.relations = append(inv.relations, relation{"mounted_in", id, idRackF, obj{"position": d.u, "face": d.face}})
	}
	buildDrawn(inv)
}

// The drawn rack: r21 in lyon-1, one device per face the project drew
// (internal/devicetypes/library/drawings), every one healthy, so that a
// screenshot shows the drawings and nothing else.
func buildDrawn(inv *inventory) {
	inv.add(object(idRackD, "rack", "r21", idSiteL, idSiteL, obj{"name": "r21", "u_height": 24}))
	n := firstDraw
	for _, d := range []struct {
		name, role, slug string
		u                float64
	}{
		{"sw-d1", "switch", "arista-dcs-7280se-64-f", 24},
		{"sw-d2", "switch", "arista-dcs-7050sx-64-r", 23},
		{"sw-d3", "switch", "mikrotik-crs354-48g-4s-plus-2q-plus-rm", 22},
		{"srv-d1", "server", "hpe-proliant-dl360-gen9-8sff", 21},
		{"srv-d2", "server", "dell-poweredge-r330-8sff", 20},
		{"srv-d3", "server", "dell-poweredge-r330-4lff-hot-plug", 19},
		{"srv-d4", "server", "dell-poweredge-r330-4lff-cabled", 18},
		{"srv-d5", "server", "hpe-proliant-dl380-gen9-8sff", 16},
		{"srv-d6", "server", "hpe-proliant-dl380-gen9-24sff", 14},
		{"srv-d7", "server", "hpe-proliant-dl180-gen9-8sff", 12},
		{"srv-d8", "server", "hpe-proliant-dl180-gen9-8lff", 10},
		{"srv-d9", "server", "hpe-proliant-dl180-gen9-12lff", 8},
	} {
		m := library.BySlug(d.slug)
		if m == nil || m.FrontImage == nil {
			panic("no drawn model " + d.slug)
		}
		n++
		model := n
		inv.add(object(model, "device_type", m.Model, 0, 0, obj{"slug": m.Slug, "vendor": m.Vendor, "model": m.Model, "u_height": m.UHeight, "full_depth": m.FullDepth}))
		n++
		inv.add(object(n, "device", d.name, idRackD, idSiteL, obj{"hostname": d.name, "role": d.role, "health": "ok"}))
		inv.mount(n, idRackD, d.u, "front", model)
	}
}

// health ranks of the parts' rollup, as the server counts them.
var healthRank = map[string]int{"ok": 1, "warning": 2, "critical": 3}

// faceFields adds to a rack item what the server adds: role, health, the
// worst health of the parts, the model's name and image paths.
func (a *api) faceFields(it obj, d obj, m obj) {
	attrs, _ := d["attributes"].(obj)
	for _, k := range []string{"role", "health"} {
		if v, ok := attrs[k].(string); ok {
			it[k] = v
		}
	}
	worst := ""
	for _, o := range a.inv.objects {
		if o["parent_id"] != d["id"] {
			continue
		}
		pa, _ := o["attributes"].(obj)
		if h, _ := pa["health"].(string); healthRank[h] > healthRank[worst] {
			worst = h
		}
	}
	if worst != "" {
		it["parts_health"] = worst
	}
	if _, ok := it["u_height"]; !ok {
		if h, ok := attrs["u_height"]; ok {
			it["u_height"] = h
		}
	}
	if m == nil {
		return
	}
	ma, _ := m["attributes"].(obj)
	if name, ok := ma["model"].(string); ok {
		it["model_name"] = name
	}
	slug, _ := ma["slug"].(string)
	if lm := library.BySlug(slug); lm != nil {
		for face, img := range map[string]*devicetypes.Image{"front_image": lm.FrontImage, "rear_image": lm.RearImage} {
			if img != nil {
				it[face] = "/api/v1/device-types/" + slug + "/" + strings.TrimSuffix(face, "_image")
			}
		}
	}
}

// image answers GET /device-types/{slug}/{face} like the server.
func image(w http.ResponseWriter, r *http.Request, rest string) {
	slug, face, _ := strings.Cut(rest, "/")
	m := library.BySlug(slug)
	if m == nil || (face != "front" && face != "rear") || m.Image(face) == nil {
		problem(w, http.StatusNotFound, "not-found", "No such image")
		return
	}
	img := m.Image(face)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", img.ETag)
	if r.Header.Get("If-None-Match") == img.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(img.Data)))
	_, _ = w.Write(img.Data) //nolint:gosec // embedded library bytes, served with their checked image type and nosniff
}
