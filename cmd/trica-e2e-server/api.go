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

	idSiteL    = 5 // lyon-1
	idRackL    = 6 // rl1, in lyon-1
	idVlan10   = 0x30
	idVlan20   = 0x31
	idCable1   = 0x40
	idSwL1     = 0xe1
	idSwL2     = 0xe2
	firstIf    = 0x200   // interfaces of the small network
	firstSyn   = 0x2000  // the synthetic fabric's devices
	firstSynIf = 0x10000 // and their interfaces
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

// link is a relation with its own provenance: the topology's cables are not
// all declared.
type link struct {
	relation
	source, confidence string
}

type inventory struct {
	objects   []obj
	relations []relation // declared in the lab file
	links     []link     // the network: declared, observed and deduced
}

// all lists every relation with its provenance, plain ones as declared.
func (inv *inventory) all() []link {
	out := make([]link, 0, len(inv.relations)+len(inv.links))
	for _, r := range inv.relations {
		out = append(out, link{r, "declared:lab-file", "declared"})
	}
	return append(out, inv.links...)
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
	buildNetwork(inv)
	return inv
}

// ports hands out interfaces, numbered per device.
type ports struct {
	inv  *inventory
	next int
	n    map[int]int
}

func (p *ports) of(device int) int {
	p.n[device]++
	id := p.next
	p.next++
	name := fmt.Sprintf("Ethernet%d", p.n[device])
	d := p.inv.byID(oid(device))
	var site int
	if s, ok := d["site_id"].(string); ok {
		site = int(mustHex(s))
	}
	p.inv.add(object(id, "interface", name, device, site, obj{"name": name}))
	return id
}

func mustHex(id string) int64 {
	n, err := strconv.ParseInt(id[len(id)-12:], 16, 64)
	if err != nil {
		panic(err)
	}
	return n
}

func (inv *inventory) wire(typ string, from, to int, source, confidence string, attrs obj) {
	inv.links = append(inv.links, link{relation{typ, from, to, attrs}, source, confidence})
}

// buildNetwork adds Lyon, the VLANs and the links: a small network in paris-1
// that shows every kind of link once, then a synthetic leaf-spine fabric that
// brings the topology to 200 linked devices.
func buildNetwork(inv *inventory) {
	inv.add(object(idSiteL, "site", "lyon-1", 0, 0, obj{"name": "lyon-1"}))
	inv.add(object(idRackL, "rack", "rl1", idSiteL, idSiteL, obj{"name": "rl1", "u_height": 42}))
	inv.add(object(idVlan10, "vlan", "vlan-10", 0, 0, obj{"name": "vlan-10", "vid": 10}))
	inv.add(object(idVlan20, "vlan", "vlan-20", 0, 0, obj{"name": "vlan-20", "vid": 20}))
	inv.add(object(idSwL1, "device", "sw-l1", idRackL, idSiteL, obj{"hostname": "sw-l1", "vendor": "arista"}))
	inv.add(object(idSwL2, "device", "sw-l2", idRackL, idSiteL, obj{"hostname": "sw-l2", "vendor": "arista"}))
	inv.mount(idSwL1, idRackL, 40, "front", idT1U)
	inv.mount(idSwL2, idRackL, 39, "front", idT1U)

	p := &ports{inv: inv, next: firstIf, n: map[int]int{}}
	const lldp, ded = "collector:lldp", "deduce:cabling"
	// A documented cable: the cable object, two connects.
	inv.add(object(idCable1, "cable", "C-0001", 0, idSite, obj{"label": "C-0001"}))
	a1e1, a2e1 := p.of(idSwA1), p.of(idSwA2)
	inv.wire("connects", idCable1, a1e1, "declaration:cabling", "declared", obj{"end": "a"})
	inv.wire("connects", idCable1, a2e1, "declaration:cabling", "declared", obj{"end": "b"})
	// Deduced links, one per level, one across the two sites.
	a1e2, srvEno1 := p.of(idSwA1), p.of(idSrv01)
	inv.wire("cabled_to", a1e2, srvEno1, ded, "deduced", obj{"level": "high", "evidence": "lldp"})
	a2e2, l1e1 := p.of(idSwA2), p.of(idSwL1)
	inv.wire("cabled_to", a2e2, l1e1, ded, "deduced", obj{"level": "low", "evidence": "mac_table"})
	a2e3, srvEno2 := p.of(idSwA2), p.of(idSrv01)
	inv.wire("cabled_to", a2e3, srvEno2, ded, "deduced", obj{"level": "medium", "evidence": "lldp_one_sided"})
	// Observed: an LLDP adjacency and an observed cable inside Lyon.
	a1e3 := p.of(idSwA1)
	inv.wire("neighbor_of", a1e3, a2e2, lldp, "observed", obj{"protocol": "lldp"})
	l1e2, l2e1 := p.of(idSwL1), p.of(idSwL2)
	inv.wire("cabled_to", l1e2, l2e1, lldp, "observed", obj{})
	// VLAN membership, for the VLAN filter.
	inv.wire("member_of", a1e1, idVlan10, lldp, "observed", obj{})
	inv.wire("member_of", srvEno1, idVlan10, lldp, "observed", obj{})
	inv.wire("member_of", l1e1, idVlan20, lldp, "observed", obj{})

	// The fabric: 7 spines, 28 leaves, 160 servers = 195 devices, which with the
	// five above make 200 linked devices. Every leaf has an uplink to every
	// spine; every server has two, to two leaves.
	const spines, leaves, servers = 7, 28, 160
	sp := make([]int, spines)
	lf := make([]int, leaves)
	n := firstSyn
	mk := func(name string) int {
		id := n
		n++
		inv.add(object(id, "device", name, 0, idSite, obj{"hostname": name, "role": strings.TrimRight(name[:strings.IndexByte(name, '-')], "0123456789")}))
		return id
	}
	for i := range sp {
		sp[i] = mk(fmt.Sprintf("spine-%02d", i+1))
	}
	for i := range lf {
		lf[i] = mk(fmt.Sprintf("leaf-%02d", i+1))
	}
	sp2 := &ports{inv: inv, next: firstSynIf, n: map[int]int{}}
	levels := []string{"high", "medium", "low"}
	k := 0
	for _, l := range lf {
		for _, s := range sp {
			k++
			switch k % 3 {
			case 0:
				inv.wire("cabled_to", sp2.of(l), sp2.of(s), "declaration:cabling", "declared", obj{})
			default:
				inv.wire("cabled_to", sp2.of(l), sp2.of(s), ded, "deduced", obj{"level": levels[(k/3)%3], "evidence": "lldp"})
			}
		}
	}
	for i := 0; i < servers; i++ {
		srv := mk(fmt.Sprintf("srv-f%03d", i+1))
		inv.wire("cabled_to", sp2.of(srv), sp2.of(lf[i%leaves]), lldp, "observed", obj{})
		inv.wire("neighbor_of", sp2.of(srv), sp2.of(lf[(i+1)%leaves]), lldp, "observed", obj{"protocol": "lldp"})
	}
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
	case path == "/relations":
		a.relationLinks(w, r.URL.Query().Get("type"))
	case path == "/search":
		a.search(w, r.URL.Query().Get("q"))
	case strings.HasPrefix(path, "/objects/"):
		a.one(w, strings.TrimPrefix(path, "/objects/"))
	case strings.HasPrefix(path, "/racks/") && strings.HasSuffix(path, "/contents"):
		a.rackContents(w, strings.TrimSuffix(strings.TrimPrefix(path, "/racks/"), "/contents"))
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

// rackContents answers like the server: the devices mounted in the rack with
// their position, face and model, in one call.
func (a *api) rackContents(w http.ResponseWriter, id string) {
	rack := a.inv.byID(id)
	if rack == nil || rack["type"] != "rack" {
		problem(w, http.StatusNotFound, "not-found", "No such rack")
		return
	}
	model := map[int]int{}
	for _, r := range a.inv.relations {
		if r.typ == "instance_of" {
			model[r.from] = r.to
		}
	}
	var items []obj
	for _, r := range a.inv.relations {
		if r.typ != "mounted_in" || oid(r.to) != id {
			continue
		}
		d := a.inv.byID(oid(r.from))
		it := obj{"device": obj{"id": d["id"], "type": d["type"], "display": d["display"]}, "full_depth": false}
		for _, k := range []string{"position", "face"} {
			if v, ok := r.attrs[k]; ok {
				it[k] = v
			}
		}
		if m := a.inv.byID(oid(model[r.from])); m != nil {
			it["model"] = obj{"id": m["id"], "type": m["type"], "display": m["display"]}
			attrs, _ := m["attributes"].(obj)
			if h, ok := attrs["u_height"]; ok {
				it["u_height"] = h
			}
			it["full_depth"] = attrs["full_depth"] == true
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, page(items))
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
	for _, rel := range a.inv.all() {
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
			"source": rel.source, "confidence": rel.confidence, "valid_from": t0, "attributes": rel.attrs,
			"other": obj{"id": other, "type": o["type"], "display": o["display"]},
		})
	}
	return page(items)
}

// end is the RelationEnd of GET /relations: the object, with the device or
// rack it sits in and its site, so the page can group ports by device.
func (a *api) end(n int) obj {
	o := a.inv.byID(oid(n))
	e := obj{"id": o["id"], "type": o["type"], "display": o["display"]}
	for _, k := range []string{"parent_id", "site_id"} {
		if v, ok := o[k]; ok {
			e[k] = v
		}
	}
	return e
}

// relationLinks answers GET /relations?type=a,b: every current relation of
// those types, both ends described.
func (a *api) relationLinks(w http.ResponseWriter, types string) {
	if types == "" {
		problem(w, http.StatusBadRequest, "invalid-request", "type is required")
		return
	}
	want := map[string]bool{}
	for _, t := range strings.Split(types, ",") {
		want[t] = true
	}
	items := []obj{}
	for i, rel := range a.inv.all() {
		if !want[rel.typ] {
			continue
		}
		items = append(items, obj{
			"id": i + 1, "type": rel.typ, "from_id": oid(rel.from), "to_id": oid(rel.to),
			"from": a.end(rel.from), "to": a.end(rel.to),
			"source": rel.source, "confidence": rel.confidence, "valid_from": t0, "attributes": rel.attrs,
		})
	}
	writeJSON(w, http.StatusOK, obj{"total": len(items), "limit": 1000, "offset": 0, "items": items})
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
			typeDoc("interface", "Interface", "Interface", []string{"device"}, nil),
			typeDoc("vlan", "VLAN", "VLAN", []string{}, nil),
			typeDoc("cable", "Cable", "Câble", []string{}, nil),
		},
		"relations": []obj{
			{"name": "mounted_in", "label": obj{"en": "mounted in", "fr": "monté dans"}, "inverse": obj{"en": "hosts", "fr": "héberge"}, "description": "", "from": []string{"device"}, "to": []string{"rack"}, "attributes": []obj{}, "custom": false},
			{"name": "instance_of", "label": obj{"en": "instance of", "fr": "instance de"}, "inverse": obj{"en": "instances", "fr": "instances"}, "description": "", "from": []string{"device"}, "to": []string{"device_type"}, "attributes": []obj{}, "custom": false},
			{"name": "cabled_to", "label": obj{"en": "cabled to", "fr": "câblé à"}, "inverse": obj{"en": "cabled to", "fr": "câblé à"}, "description": "", "from": []string{"interface"}, "to": []string{"interface"}, "attributes": []obj{}, "custom": false},
			{"name": "connects", "label": obj{"en": "connects", "fr": "relie"}, "inverse": obj{"en": "connected by", "fr": "relié par"}, "description": "", "from": []string{"cable"}, "to": []string{"interface"}, "attributes": []obj{}, "custom": false},
			{"name": "neighbor_of", "label": obj{"en": "neighbor of", "fr": "voisin de"}, "inverse": obj{"en": "neighbor of", "fr": "voisin de"}, "description": "", "from": []string{"interface"}, "to": []string{"interface"}, "attributes": []obj{}, "custom": false},
			{"name": "member_of", "label": obj{"en": "member of", "fr": "membre de"}, "inverse": obj{"en": "members", "fr": "membres"}, "description": "", "from": []string{"interface"}, "to": []string{"vlan"}, "attributes": []obj{}, "custom": false},
		},
	}
}
