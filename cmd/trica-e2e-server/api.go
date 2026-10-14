package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// The mock inventory. Everything is invented: names (sw-a1, srv-01, paris-1),
// documentation addresses (192.0.2.0/24, 198.51.100.0/24). The shapes follow
// web/openapi.json; web/src/api/types.ts types the same documents on the
// browser side, so a field the real server renames fails there, not here.

type obj = map[string]any

func oid(n int) string { return fmt.Sprintf("0a000000-0000-4000-8000-%012x", n) }

const (
	idSite  = 1
	idRack  = 2 // r12, 42 U, the readable rack
	idRack2 = 3 // r13, with a problem on purpose
	idRack3 = 4 // r48, 48 U fully populated on both faces
	idRoom  = 16
	idRow   = 17
	idSwA1  = 0xa1
	idSwA2  = 0xa2
	idSrv01 = 0xb1
	idT1U   = 0xc1
	idT2U   = 0xc2
	idTFull = 0xc3
	idPDU   = 0xd1
	firstR3 = 0x1000 // devices of r48
)

const (
	t0 = "2026-03-01T08:00:00Z"
	t1 = "2026-10-12T06:30:00Z"
)

func object(n int, typ, display string, parent, site int, attrs obj) obj {
	o := obj{"id": oid(n), "type": typ, "display": display, "status": "active", "attributes": attrs, "created_at": t0, "observed": false}
	if parent != 0 {
		o["parent_id"] = oid(parent)
	}
	if site != 0 {
		o["site_id"] = oid(site)
	}
	return o
}

type relation struct {
	typ      string
	from, to int
	attrs    obj
}

type inventory struct {
	objects   []obj
	relations []relation
}

func (inv *inventory) byID(id string) obj {
	for _, o := range inv.objects {
		if o["id"] == id {
			return o
		}
	}
	return nil
}

func (inv *inventory) add(o obj) { inv.objects = append(inv.objects, o) }

func (inv *inventory) mount(device, rack int, position float64, face string, model int) {
	inv.relations = append(inv.relations,
		relation{"mounted_in", device, rack, obj{"position": position, "face": face}},
		relation{"instance_of", device, model, obj{}})
}

func buildInventory() *inventory {
	inv := &inventory{}
	inv.add(object(idSite, "site", "paris-1", 0, 0, obj{"name": "paris-1"}))
	inv.add(object(idRoom, "room", "salle-1", idSite, idSite, obj{"name": "salle-1", "width_mm": 6000, "depth_mm": 3600}))
	inv.add(object(idRow, "row", "rangee-a", idRoom, idSite, obj{"name": "rangee-a"}))
	inv.add(object(idRack, "rack", "r12", idRoom, idSite, obj{"name": "r12", "u_height": 42, "floor_x": 1, "floor_y": 1, "facing": "south", "width_mm": 600, "depth_mm": 1000}))
	inv.add(object(idRack2, "rack", "r13", idRow, idSite, obj{"name": "r13", "u_height": 12, "floor_x": 2, "floor_y": 1, "facing": "east"}))
	inv.add(object(idRack3, "rack", "r48", idRoom, idSite, obj{"name": "r48", "u_height": 48, "floor_x": 4, "floor_y": 2, "facing": "north"}))

	inv.add(object(idT1U, "device_type", "ex-1u", 0, 0, obj{"name": "ex-1u", "u_height": 1}))
	inv.add(object(idT2U, "device_type", "ex-2u", 0, 0, obj{"name": "ex-2u", "u_height": 2}))
	inv.add(object(idTFull, "device_type", "ex-server", 0, 0, obj{"name": "ex-server", "u_height": 2, "full_depth": true}))

	inv.add(object(idSwA1, "device", "sw-a1", idRack, idSite, obj{"hostname": "sw-a1", "vendor": "arista", "os_version": "4.28.3M", "mgmt_ip": "192.0.2.10", "power_w": 120}))
	inv.add(object(idSwA2, "device", "sw-a2", idRack, idSite, obj{"hostname": "sw-a2", "vendor": "arista", "os_version": "4.28.3M", "mgmt_ip": "192.0.2.11"}))
	inv.add(object(idSrv01, "device", "srv-01", idRack, idSite, obj{"hostname": "srv-01", "vendor": "dell", "mgmt_ip": "198.51.100.21"}))
	inv.add(object(idPDU, "device", "pdu-a", idRack, idSite, obj{"hostname": "pdu-a", "vendor": "example"}))
	inv.mount(idSwA1, idRack, 40, "front", idT1U)
	inv.mount(idSwA2, idRack, 39, "front", idT1U)
	inv.mount(idSrv01, idRack, 10, "front", idTFull)
	inv.mount(idPDU, idRack, 2, "rear", idT2U)

	// r13: two devices overlap, one is mounted without a position.
	inv.add(object(0x120, "device", "srv-02", idRack2, idSite, obj{"hostname": "srv-02"}))
	inv.add(object(0x121, "device", "srv-03", idRack2, idSite, obj{"hostname": "srv-03"}))
	inv.add(object(0x122, "device", "srv-04", idRack2, idSite, obj{"hostname": "srv-04"}))
	inv.mount(0x120, idRack2, 5, "front", idT2U)
	inv.mount(0x121, idRack2, 6, "front", idT1U)
	inv.relations = append(inv.relations,
		relation{"mounted_in", 0x122, idRack2, obj{}},
		relation{"instance_of", 0x122, idT1U, obj{}})

	// r48: 48 devices on each face, 96 in all.
	for u := 1; u <= 48; u++ {
		for i, face := range []string{"front", "rear"} {
			n := firstR3 + u*2 + i
			name := fmt.Sprintf("srv-%s%02d", face[:1], u)
			inv.add(object(n, "device", name, idRack3, idSite, obj{"hostname": name}))
			inv.mount(n, idRack3, float64(u), face, idT1U)
		}
	}
	return inv
}

func problem(w http.ResponseWriter, status int, slug, title string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"urn:trica:problem:%s","title":%q,"status":%d,"detail":%q}`, slug, title, status, title)
}

func page(items []obj) obj {
	if items == nil {
		items = []obj{}
	}
	return obj{"total": len(items), "limit": 1000, "offset": 0, "items": items}
}

type api struct {
	inv   *inventory
	valid func(string) bool
}

func newAPI(valid func(string) bool) http.Handler { return &api{inv: buildInventory(), valid: valid} }

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !a.valid(token) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		problem(w, http.StatusUnauthorized, "unauthenticated", "A valid bearer token is required")
		return
	}
	if r.Method != http.MethodGet {
		problem(w, http.StatusMethodNotAllowed, "read-only", "The e2e mock API is read-only")
		return
	}
	path := r.URL.Path
	switch {
	case path == "/me":
		writeJSON(w, http.StatusOK, obj{"subject": "e2e-user", "kind": "token", "source": "e2e", "actions": []string{"read", "propose", "approve"}})
	case path == "/objects":
		writeJSON(w, http.StatusOK, a.list(r.URL.Query().Get("type")))
	case path == "/search":
		a.search(w, r.URL.Query().Get("q"))
	case strings.HasPrefix(path, "/objects/"):
		a.one(w, strings.TrimPrefix(path, "/objects/"))
	case path == "/discrepancies":
		writeJSON(w, http.StatusOK, page([]obj{{
			"id": 5, "kind": "value_mismatch", "fingerprint": "fp-5", "object_id": oid(idSwA1), "key": "os_version",
			"declared": "4.27.0M", "observed": "4.28.3M", "status": "open", "opened_at": "2026-09-20T02:00:00Z", "last_seen": t1,
		}}))
	case path == "/proposals":
		writeJSON(w, http.StatusOK, page([]obj{proposal(12, "pending"), proposal(11, "applied")}))
	case strings.HasPrefix(path, "/proposals/"):
		n, err := strconv.Atoi(strings.TrimPrefix(path, "/proposals/"))
		if err != nil || (n != 11 && n != 12) {
			problem(w, http.StatusNotFound, "not-found", "No such proposal")
			return
		}
		p := proposal(n, map[int]string{11: "applied", 12: "pending"}[n])
		p["preview"] = obj{"created": 0, "objects": obj{}, "relations_closed": 0, "relations_opened": 0, "values_changed": 1,
			"changes": []obj{{"op": "set_attr", "object_id": oid(idSwA1), "type": "device", "key": "rack_unit", "after": "U40"}}}
		writeJSON(w, http.StatusOK, p)
	case path == "/health/collection":
		writeJSON(w, http.StatusOK, page([]obj{
			{"source": "arista:eos", "collector": "arista", "target": "192.0.2.10", "version": "0.3.0", "ok": true, "last_attempt": t1, "last_success": t1, "duration_ms": 840, "items": 124, "warnings": []string{}, "failures": 0},
			{"source": "snmp:core", "collector": "snmp", "target": "198.51.100.1", "ok": false, "last_attempt": "2026-10-12T06:25:00Z", "last_success": "2026-10-11T06:25:00Z", "last_error": "timeout after 30s", "duration_ms": 30000, "items": 0, "warnings": []string{"no ifAlias on 3 interfaces"}, "failures": 4},
		}))
	case path == "/schema":
		writeJSON(w, http.StatusOK, schemaDoc())
	default:
		problem(w, http.StatusNotFound, "not-found", "No such endpoint")
	}
}

func proposal(id int, status string) obj {
	p := obj{"id": id, "created_at": "2026-10-12T07:00:00Z", "source": "mcp:assistant", "author": "agent-1", "reason": "Record the rack unit of sw-a1", "batch": nil, "status": status}
	if status == "applied" {
		p["decided_at"] = "2026-10-12T09:00:00Z"
		p["decided_by"] = "ops-1"
		p["changeset_id"] = 52
	}
	return p
}

func (a *api) list(typ string) obj {
	var items []obj
	for _, o := range a.inv.objects {
		if typ == "" || o["type"] == typ {
			items = append(items, o)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i]["display"].(string) < items[j]["display"].(string) })
	return page(items)
}

func (a *api) search(w http.ResponseWriter, q string) {
	var items []obj
	for _, o := range a.inv.objects {
		if q != "" && strings.Contains(strings.ToLower(o["display"].(string)), strings.ToLower(q)) && o["type"] != "device_type" {
			items = append(items, o)
		}
	}
	if items == nil {
		items = []obj{}
	}
	byType := map[string]int{}
	for _, o := range items {
		byType[o["type"].(string)]++
	}
	writeJSON(w, http.StatusOK, obj{
		"summary": obj{"query": q, "total": len(items), "by_type": byType, "fuzzy": false, "took_ms": 1},
		"total":   len(items), "limit": 100, "offset": 0, "items": items,
	})
}

func (a *api) one(w http.ResponseWriter, rest string) {
	id, sub, _ := strings.Cut(rest, "/")
	o := a.inv.byID(id)
	if o == nil {
		problem(w, http.StatusNotFound, "not-found", "No such object")
		return
	}
	switch sub {
	case "":
		writeJSON(w, http.StatusOK, o)
	case "relations":
		writeJSON(w, http.StatusOK, a.relationsOf(id))
	case "provenance":
		writeJSON(w, http.StatusOK, obj{"object_id": id, "attributes": a.provenanceOf(o)})
	case "timeline":
		writeJSON(w, http.StatusOK, page([]obj{{"at": t0, "object_id": id, "type": o["type"], "display": o["display"], "key": "hostname", "after": o["display"], "source": "declared:lab-file", "changeset_id": 1}}))
	default:
		problem(w, http.StatusNotFound, "not-found", "No such endpoint")
	}
}

func (a *api) provenanceOf(o obj) []obj {
	var out []obj
	attrs, _ := o["attributes"].(obj)
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, obj{"key": k, "value": attrs[k], "source": "declared:lab-file", "confidence": "declared", "valid_from": t0, "first_seen": t0, "last_seen": t1})
	}
	return out
}

func (a *api) relationsOf(id string) obj {
	var items []obj
	n := 0
	for _, rel := range a.inv.relations {
		dir, other := "", ""
		switch {
		case oid(rel.from) == id:
			dir, other = "out", oid(rel.to)
		case oid(rel.to) == id:
			dir, other = "in", oid(rel.from)
		default:
			continue
		}
		n++
		o := a.inv.byID(other)
		items = append(items, obj{
			"id": n, "type": rel.typ, "from_id": oid(rel.from), "to_id": oid(rel.to), "direction": dir,
			"source": "declared:lab-file", "confidence": "declared", "valid_from": t0, "attributes": rel.attrs,
			"other": obj{"id": other, "type": o["type"], "display": o["display"]},
		})
	}
	return page(items)
}

func attr(name, kind, en, fr string, extra obj) obj {
	a := obj{"name": name, "kind": kind, "label": obj{"en": en, "fr": fr}, "description": "", "search": false, "required": false, "custom": false}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func typeDoc(name, label, fr string, parents []string, attrs []obj) obj {
	if attrs == nil {
		attrs = []obj{}
	}
	return obj{"name": name, "layer": "physical", "label": obj{"en": label, "fr": fr}, "description": "", "display": "name", "parents": parents, "custom": false,
		"identity": obj{"strong": []string{}, "weak": []string{"name"}}, "attributes": attrs}
}

func schemaDoc() obj {
	return obj{
		"types": []obj{
			typeDoc("device", "Device", "Équipement", []string{"rack"}, []obj{
				attr("hostname", "string", "Hostname", "Nom d'hôte", obj{"search": true, "required": true}),
				attr("vendor", "string", "Vendor", "Constructeur", obj{"search": true}),
				attr("os_version", "string", "OS version", "Version de l'OS", obj{"search": true}),
				attr("mgmt_ip", "ip", "Management address", "Adresse de gestion", obj{"search": true}),
				attr("power_w", "number", "Power draw", "Consommation", obj{"unit": "W"}),
			}),
			typeDoc("rack", "Rack", "Baie", []string{"room", "row", "site"}, []obj{
				attr("u_height", "integer", "Height in U", "Hauteur en U", nil),
				attr("floor_x", "integer", "Floor column", "Colonne au sol", nil),
				attr("floor_y", "integer", "Floor row", "Rangée au sol", nil),
				attr("facing", "string", "Front faces", "Face avant vers", nil),
			}),
			typeDoc("row", "Row", "Rangée", []string{"room"}, nil),
			typeDoc("room", "Room", "Salle", []string{"site"}, nil),
			typeDoc("site", "Site", "Site", []string{}, nil),
			typeDoc("device_type", "Device model", "Modèle d'équipement", []string{}, nil),
		},
		"relations": []obj{
			{"name": "mounted_in", "label": obj{"en": "mounted in", "fr": "monté dans"}, "inverse": obj{"en": "hosts", "fr": "héberge"}, "description": "", "from": []string{"device"}, "to": []string{"rack"}, "attributes": []obj{}, "custom": false},
			{"name": "instance_of", "label": obj{"en": "instance of", "fr": "instance de"}, "inverse": obj{"en": "instances", "fr": "instances"}, "description": "", "from": []string{"device"}, "to": []string{"device_type"}, "attributes": []obj{}, "custom": false},
		},
	}
}
