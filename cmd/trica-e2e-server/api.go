package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
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
	idSpare = 0xd9   // a device in no rack
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
	relations []relation       // declared in the lab file
	links     []link           // the network: declared, observed and deduced
	places    map[string]place // where a part sits on its device's faces, by part id
	modelIDs  map[string]int   // the R330 models of r22, by slug
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

	inv.add(object(idSwA1, "device", "sw-a1", idRack, idSite, obj{"hostname": "sw-a1", "vendor": "arista", "os_version": "4.28.3M", "mgmt_ip": "192.0.2.10", "power_w": 120, "mlag_domain": "mlag-a",
		"role": "switch", "health": "ok", "model": "ex-switch-48", "serial": "EXSW0001", "os_name": "ex-os", "uptime_s": 4060800, "inlet_temp_c": 24.5, "outlet_temp_c": 31}))
	inv.add(object(idSwA2, "device", "sw-a2", idRack, idSite, obj{"hostname": "sw-a2", "vendor": "arista", "os_version": "4.28.3M", "mgmt_ip": "192.0.2.11", "mlag_domain": "mlag-a",
		"role": "switch", "health": "warning", "power_w": 135, "inlet_temp_c": 26, "outlet_temp_c": 41.5}))
	inv.add(object(idSrv01, "device", "srv-01", idRack, idSite, obj{"hostname": "srv-01", "vendor": "dell", "mgmt_ip": "198.51.100.21",
		"role": "server", "health": "critical", "model": "ex-server", "serial": "EXSV0001", "os_name": "linux", "os_version": "6.8", "power_w": 412.5,
		"inlet_temp_c": 23, "outlet_temp_c": 38.5, "uptime_s": 1303200, "memory_bytes": 274877906944, "cpu_count": 2, "log_critical_count": 2}))
	inv.add(object(idPDU, "device", "pdu-a", idRack, idSite, obj{"hostname": "pdu-a", "vendor": "example", "role": "pdu", "health": "ok", "power_w": 1840}))
	// A device in no rack: what the placement dialog offers (ADR 0052).
	inv.add(object(idSpare, "device", "spare-01", idSite, idSite, obj{"hostname": "spare-01", "vendor": "example", "role": "server", "health": "ok"}))
	inv.mount(idSwA1, idRack, 40, "front", idT1U)
	inv.mount(idSwA2, idRack, 39, "front", idT1U)
	inv.mount(idSrv01, idRack, 10, "front", idTFull)
	inv.mount(idPDU, idRack, 2, "rear", idT2U)

	// r13: two devices overlap, one is mounted without a position.
	inv.add(object(0x120, "device", "srv-02", idRack2, idSite, obj{"hostname": "srv-02", "role": "server", "health": "ok", "power_w": 288}))
	inv.add(object(0x121, "device", "srv-03", idRack2, idSite, obj{"hostname": "srv-03", "role": "nas", "health": "warning", "power_w": 96.5}))
	inv.add(object(0x122, "device", "srv-04", idRack2, idSite, obj{"hostname": "srv-04", "role": "firewall"}))
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
			inv.add(object(n, "device", name, idRack3, idSite, obj{"hostname": name, "role": "server", "health": []string{"ok", "ok", "ok", "ok", "ok", "ok", "warning"}[n%7], "power_w": 180 + n%40}))
			inv.mount(n, idRack3, float64(u), face, idT1U)
		}
	}
	buildNetwork(inv)
	buildComponents(inv)
	buildFaces(inv)
	buildParts(inv)
	buildStorageNode(inv)
	buildVariants(inv)
	buildAisles(inv)
	return inv
}

// buildComponents gives srv-01 and sw-a1 what a BMC or a switch reports
// inside them: power supplies (one of srv-01's has failed), fans, disks,
// memory and processors.
func buildComponents(inv *inventory) {
	n := 0x3000
	part := func(parent int, typ, name string, attrs obj) {
		attrs["name"] = name
		inv.add(object(n, typ, name, parent, idSite, attrs))
		n++
	}
	part(idSrv01, "psu", "PSU 1", obj{"capacity_w": 800, "input_w": 210.4, "health": "ok", "feed": "A", "model": "ex-psu-800"})
	part(idSrv01, "psu", "PSU 2", obj{"capacity_w": 800, "input_w": 0, "health": "critical", "feed": "B", "model": "ex-psu-800"})
	for i := 1; i <= 4; i++ {
		part(idSrv01, "fan", fmt.Sprintf("Fan %d", i), obj{"speed_rpm": 7800 + 120*i, "speed_pct": 38.5, "health": "ok"})
	}
	part(idSrv01, "disk", "nvme0n1", obj{"media": "nvme", "capacity_bytes": 960197124096, "wear_pct": 4, "temperature_c": 31, "health": "ok", "model": "ex-nvme-960"})
	part(idSrv01, "disk", "nvme1n1", obj{"media": "nvme", "capacity_bytes": 960197124096, "wear_pct": 72, "temperature_c": 44, "health": "warning", "model": "ex-nvme-960"})
	for i := 0; i < 4; i++ {
		part(idSrv01, "memory", fmt.Sprintf("DIMM A%d", i+1), obj{"size_bytes": 68719476736, "speed_mhz": 4800, "health": "ok", "corrected_errors": []int{0, 0, 12, 0}[i]})
	}
	for i := 0; i < 2; i++ {
		part(idSrv01, "cpu", fmt.Sprintf("CPU %d", i+1), obj{"model": "ex-cpu-32c", "cores": 32, "threads": 64, "health": "ok"})
	}
	part(idSwA1, "psu", "PSU 1", obj{"capacity_w": 500, "input_w": 61, "health": "ok", "feed": "A"})
	part(idSwA1, "psu", "PSU 2", obj{"capacity_w": 500, "input_w": 59, "health": "ok", "feed": "B"})
	for i := 1; i <= 3; i++ {
		part(idSwA1, "fan", fmt.Sprintf("Fan %d", i), obj{"speed_pct": 42.5, "health": "ok"})
	}
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

// named adds an interface with its own name (an aggregate), outside the EthernetN count.
func (p *ports) named(device int, name string) int {
	id := p.next
	p.next++
	d := p.inv.byID(oid(device))
	var site int
	if s, ok := d["site_id"].(string); ok {
		site = int(mustHex(s))
	}
	p.inv.add(object(id, "interface", name, device, site, obj{"name": name, "kind": "lag"}))
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
	// An aggregate between the MLAG peers: Ethernet5 and Ethernet6 of each side
	// in a Port-Channel11, the live overlay draws them as one line.
	a1e5, a1e6, a2e5, a2e6 := p.of(idSwA1), p.of(idSwA1), p.of(idSwA2), p.of(idSwA2)
	inv.wire("cabled_to", a1e5, a2e5, lldp, "observed", obj{})
	inv.wire("cabled_to", a1e6, a2e6, lldp, "observed", obj{})
	a1po, a2po := p.named(idSwA1, "Port-Channel11"), p.named(idSwA2, "Port-Channel11")
	for _, m := range [][2]int{{a1e5, a1po}, {a1e6, a1po}, {a2e5, a2po}, {a2e6, a2po}} {
		inv.wire("member_of", m[0], m[1], lldp, "observed", obj{})
	}
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
		role := map[string]string{"spine": "switch", "leaf": "switch", "srv": "server"}[name[:strings.IndexByte(name, '-')]]
		inv.add(object(id, "device", name, 0, idSite, obj{"hostname": name, "role": role, "health": []string{"ok", "ok", "ok", "ok", "ok", "unknown", "ok", "ok", "ok", "ok", "ok", "warning"}[id%12]}))
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
	var firstSrv int
	for i := 0; i < servers; i++ {
		srv := mk(fmt.Sprintf("srv-f%03d", i+1))
		if i == 0 {
			firstSrv = srv
		}
		inv.wire("cabled_to", sp2.of(srv), sp2.of(lf[i%leaves]), lldp, "observed", obj{})
		inv.wire("neighbor_of", sp2.of(srv), sp2.of(lf[(i+1)%leaves]), lldp, "observed", obj{"protocol": "lldp"})
	}
	// Uplinks a controller reports without any port (ADR 0060, 0062): the first
	// server to a spine no cable reaches it through (drawn as a line of its own),
	// and the first leaf to a spine its cable already joins it to (carried by
	// that cable's line).
	inv.wire("uplinked_to", firstSrv, sp[0], "unifi:e2e", "observed", obj{})
	inv.wire("uplinked_to", lf[0], sp[0], "unifi:e2e", "observed", obj{})
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
	inv     *inventory
	valid   func(string) bool
	choices *choices
	aisles  *aisleDecl
}

func newAPI(valid func(string) bool) http.Handler {
	return &api{inv: buildInventory(), valid: valid, choices: &choices{slug: map[string]string{}, by: map[string]string{}}, aisles: &aisleDecl{front: map[string]string{}}}
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !a.valid(token) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		problem(w, http.StatusUnauthorized, "unauthenticated", "A valid bearer token is required")
		return
	}
	path := r.URL.Path
	if r.Method == http.MethodPost && (path == "/batches" || path == "/batches/preview") {
		a.batch(w, r, path == "/batches/preview")
		return
	}
	if r.Method != http.MethodGet {
		problem(w, http.StatusMethodNotAllowed, "read-only", "The e2e mock API writes nothing")
		return
	}
	switch {
	case path == "/me":
		writeJSON(w, http.StatusOK, obj{"subject": "e2e-user", "kind": "token", "source": "e2e", "actions": []string{"read", "propose", "approve"}})
	case path == "/objects":
		writeJSON(w, http.StatusOK, a.list(r.URL.Query()))
	case path == "/relations":
		a.relationLinks(w, r.URL.Query().Get("type"))
	case path == "/search":
		a.search(w, r.URL.Query().Get("q"))
	case strings.HasPrefix(path, "/objects/"):
		a.one(w, strings.TrimPrefix(path, "/objects/"))
	case strings.HasPrefix(path, "/device-types/"):
		image(w, r, strings.TrimPrefix(path, "/device-types/"))
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
		eos, snmp, sw2, host := healthRows()
		writeJSON(w, http.StatusOK, page([]obj{eos, snmp, sw2, host}))
	case path == "/collectors" || strings.HasPrefix(path, "/collectors/"):
		a.collectors(w, path)
	case path == "/schema":
		writeJSON(w, http.StatusOK, schemaDoc())
	case path == "/topology/map":
		a.topologyMap(w, r)
	case path == "/topology/live":
		writeJSON(w, http.StatusOK, a.live())
	default:
		problem(w, http.StatusNotFound, "not-found", "No such endpoint")
	}
}

// batchRel is a relation of a batch body, its ends as sent.
type batchRel struct {
	Type string `json:"type"`
	From struct {
		Ref      string            `json:"ref"`
		Identity map[string]string `json:"identity"`
	} `json:"from"`
	To struct {
		Ref      string            `json:"ref"`
		Identity map[string]string `json:"identity"`
	} `json:"to"`
}

// batch answers POST /batches and /batches/preview without writing anything,
// so that the browser tests stay independent of each other (they run in
// parallel against one server). The preview lists what the batch says, as the
// engine's preview would for objects it does not know: one create per object
// that is not designated by id, one relate or unrelate per relation. A submit
// is always queued as proposal 12, the pending proposal of the mock: the page
// shows "sent for approval" and links to it. The one exception is the
// chassis chooser's declaration (variants.go): it is applied and kept, so
// that the rack shows the chosen face.
func (a *api) batch(w http.ResponseWriter, r *http.Request, preview bool) {
	var b struct {
		Objects     []batchObject           `json:"objects"`
		Relations   []batchRel              `json:"relations"`
		Unrelations []struct{ Type string } `json:"unrelations"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || json.Unmarshal(raw, &b) != nil {
		problem(w, http.StatusBadRequest, "invalid-batch", "The body is not a batch")
		return
	}
	if a.declaredAisle(w, b.Objects, len(b.Relations), preview) {
		return
	}
	identities := map[string]map[string]string{}
	for _, o := range b.Objects {
		identities[o.Ref] = o.Identity
	}
	if a.declaredChoice(identities, b.Relations, preview) {
		rel := b.Relations[0]
		model := oid(a.inv.modelIDs[identities[rel.To.Ref]["slug"]])
		result := obj{"created": 0, "values_changed": 0, "relations_opened": 1, "relations_closed": len(b.Unrelations), "objects": obj{rel.To.Ref: model},
			"changes": []obj{{"op": "relate", "object_id": rel.From.Identity["id"], "type": "device", "key": "instance_of", "other_id": model}}}
		if preview {
			writeJSON(w, http.StatusOK, obj{"source": "e2e", "result": result})
		} else {
			writeJSON(w, http.StatusOK, obj{"source": "e2e", "status": "applied", "result": result})
		}
		return
	}
	changes := []obj{}
	objects := obj{}
	for i, o := range b.Objects {
		if o.Identity["id"] != "" {
			objects[o.Ref] = o.Identity["id"]
			continue
		}
		id := oid(0xf000 + i)
		objects[o.Ref] = id
		changes = append(changes, obj{"op": "create", "object_id": id, "type": o.Type})
	}
	for _, rel := range b.Relations {
		changes = append(changes, obj{"op": "relate", "object_id": oid(0xf000), "type": "", "key": rel.Type, "other_id": oid(idRack)})
	}
	for _, rel := range b.Unrelations {
		changes = append(changes, obj{"op": "unrelate", "object_id": oid(0xf000), "type": "", "key": rel.Type, "other_id": oid(idRack)})
	}
	result := obj{"created": len(changes), "values_changed": 0, "relations_opened": len(b.Relations), "relations_closed": len(b.Unrelations), "objects": objects, "changes": changes}
	if preview {
		writeJSON(w, http.StatusOK, obj{"source": "e2e", "result": result})
		return
	}
	result["proposal_id"] = 12
	writeJSON(w, http.StatusAccepted, obj{"source": "e2e", "status": "proposed", "result": result})
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

func (a *api) list(q url.Values) obj {
	typ := q.Get("type")
	// The picker's text, without its status clause: a substring of the display name.
	text := strings.ToLower(strings.TrimSpace(strings.SplitN(q.Get("q"), "(status:", 2)[0]))
	mounted := map[string]bool{}
	for _, r := range a.inv.relations {
		if r.typ == "mounted_in" {
			mounted[oid(r.from)] = true
		}
	}
	var items []obj
	for _, o := range a.inv.objects {
		if typ != "" && o["type"] != typ {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(o["display"].(string)), text) {
			continue
		}
		if q.Get("mounted") == "false" && mounted[o["id"].(string)] {
			continue
		}
		items = append(items, a.withAisle(o))
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i]["display"].(string) < items[j]["display"].(string) })
	return page(items)
}

func (a *api) search(w http.ResponseWriter, q string) {
	query := q
	var items []obj
	// parent:"<id>": what sits inside an object (the object page's components).
	if parent, ok := strings.CutPrefix(q, "parent:"); ok {
		parent = strings.Trim(parent, `"`)
		for _, o := range a.inv.objects {
			if o["parent_id"] == parent && o["type"] != "interface" {
				items = append(items, o)
			}
		}
		for _, o := range a.inv.objects {
			if o["parent_id"] == parent && o["type"] == "interface" {
				items = append(items, o)
			}
		}
		q = "" // the loop below adds nothing
	}
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
		"summary": obj{"query": query, "total": len(items), "by_type": byType, "fuzzy": false, "took_ms": 1},
		"total":   len(items), "limit": 100, "offset": 0, "items": items,
	})
}

func (a *api) one(w http.ResponseWriter, rest string) {
	id, sub, _ := strings.Cut(rest, "/")
	o := a.withAisle(a.inv.byID(id))
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
	case "faces":
		a.faces(w, o)
	case "device-type-candidates":
		a.candidates(w, o)
	case "airflow":
		a.objectAirflow(w, o)
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
		m := a.modelOf(oid(r.from))
		if m != nil {
			it["model"] = obj{"id": m["id"], "type": m["type"], "display": m["display"]}
			attrs, _ := m["attributes"].(obj)
			if h, ok := attrs["u_height"]; ok {
				it["u_height"] = h
			}
			it["full_depth"] = attrs["full_depth"] == true
		}
		a.faceFields(it, d, m)
		if u, n := a.uncertain(d); u {
			it["variant_uncertain"], it["variants"] = true, n
		}
		it["airflow"] = a.rackAirflow(d)
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
				attr("name", "string", "Name", "Nom", obj{"identity": "weak"}),
				attr("role", "enum", "Role", "Rôle", obj{"values": []string{"server", "switch", "router", "firewall", "storage", "pdu", "ups", "patch_panel", "sensor", "nas", "kvm", "other"}}),
				attr("u_height", "float", "Height", "Hauteur", obj{"unit": "U"}),
				attr("model", "string", "Model", "Modèle", nil),
				attr("serial", "string", "Serial number", "Numéro de série", obj{"identity": "strong"}),
				attr("os_name", "string", "OS", "Système", nil),
				attr("health", "enum", "Health", "Santé", obj{"values": []string{"ok", "warning", "critical", "unknown"}}),
				attr("inlet_temp_c", "float", "Inlet temperature", "Température d'entrée", obj{"unit": "°C"}),
				attr("outlet_temp_c", "float", "Outlet temperature", "Température de sortie", obj{"unit": "°C"}),
				attr("uptime_s", "int", "Uptime", "Durée de fonctionnement", obj{"unit": "s"}),
				attr("memory_bytes", "int", "Memory", "Mémoire", obj{"unit": "B"}),
				attr("cpu_count", "int", "CPUs", "Sockets CPU", nil),
				attr("log_critical_count", "int", "Critical events", "Événements critiques", nil),
			}),
			typeDoc("psu", "Power supply", "Alimentation", []string{"device"}, []obj{
				attr("name", "string", "Name", "Nom", obj{"identity": "weak"}),
				attr("model", "string", "Model", "Modèle", nil),
				attr("capacity_w", "int", "Capacity", "Capacité", obj{"unit": "W"}),
				attr("input_w", "float", "Input power", "Puissance d'entrée", obj{"unit": "W"}),
				attr("health", "enum", "Health", "Santé", obj{"values": []string{"ok", "warning", "critical", "unknown", "absent"}}),
				attr("feed", "string", "Feed", "Voie", nil),
			}),
			typeDoc("fan", "Fan", "Ventilateur", []string{"device"}, []obj{
				attr("name", "string", "Name", "Nom", obj{"identity": "weak"}),
				attr("speed_rpm", "int", "Speed", "Vitesse", obj{"unit": "rpm"}),
				attr("speed_pct", "float", "Speed", "Vitesse", obj{"unit": "%"}),
				attr("health", "enum", "Health", "Santé", obj{"values": []string{"ok", "warning", "critical", "unknown", "absent"}}),
			}),
			typeDoc("disk", "Disk", "Disque", []string{"device"}, []obj{
				attr("name", "string", "Name", "Nom", obj{"identity": "weak"}),
				attr("model", "string", "Model", "Modèle", nil),
				attr("media", "enum", "Media", "Technologie", obj{"values": []string{"ssd", "nvme", "hdd", "unknown"}}),
				attr("capacity_bytes", "int", "Capacity", "Capacité", obj{"unit": "B"}),
				attr("wear_pct", "float", "Wear", "Usure", obj{"unit": "%"}),
				attr("temperature_c", "float", "Temperature", "Température", obj{"unit": "°C"}),
				attr("health", "enum", "Health", "Santé", obj{"values": []string{"ok", "warning", "critical", "unknown", "absent"}}),
			}),
			typeDoc("memory", "Memory module", "Barrette mémoire", []string{"device"}, []obj{
				attr("name", "string", "Slot", "Emplacement", obj{"identity": "weak"}),
				attr("size_bytes", "int", "Size", "Taille", obj{"unit": "B"}),
				attr("speed_mhz", "int", "Speed", "Fréquence", obj{"unit": "MHz"}),
				attr("health", "enum", "Health", "Santé", obj{"values": []string{"ok", "warning", "critical", "unknown", "absent"}}),
				attr("corrected_errors", "int", "Corrected errors", "Erreurs corrigées", nil),
			}),
			typeDoc("cpu", "Processor", "Processeur", []string{"device"}, []obj{
				attr("name", "string", "Socket", "Socket", obj{"identity": "weak"}),
				attr("model", "string", "Model", "Modèle", nil),
				attr("cores", "int", "Cores", "Cœurs", nil),
				attr("threads", "int", "Threads", "Threads", nil),
				attr("health", "enum", "Health", "Santé", obj{"values": []string{"ok", "warning", "critical", "unknown", "absent"}}),
			}),
			typeDoc("rack", "Rack", "Baie", []string{"room", "row", "site"}, []obj{
				attr("name", "string", "Name", "Nom", obj{"required": true, "identity": "weak"}),
				attr("u_height", "int", "Height", "Hauteur", obj{"unit": "U"}),
				attr("floor_x", "int", "Floor X", "Position X", nil),
				attr("floor_y", "int", "Floor Y", "Position Y", nil),
				attr("facing", "enum", "Front faces", "Face avant vers", obj{"values": []string{"north", "east", "south", "west"}}),
				attr("front_aisle", "enum", "Front aisle", "Couloir côté avant", obj{"values": []string{"cold", "hot"}}),
			}),
			typeDoc("row", "Row", "Rangée", []string{"room"}, nil),
			typeDoc("room", "Room", "Salle", []string{"site"}, []obj{
				attr("name", "string", "Name", "Nom", obj{"required": true, "identity": "weak"}),
				attr("width_mm", "int", "Width", "Largeur", obj{"unit": "mm"}),
				attr("depth_mm", "int", "Depth", "Profondeur", obj{"unit": "mm"}),
				attr("tile_mm", "int", "Floor tile", "Carreau", obj{"unit": "mm"}),
			}),
			typeDoc("site", "Site", "Site", []string{}, []obj{attr("name", "string", "Name", "Nom", obj{"required": true, "identity": "weak"})}),
			typeDoc("device_type", "Device model", "Modèle d'équipement", []string{}, nil),
			typeDoc("interface", "Interface", "Interface", []string{"device"}, nil),
			typeDoc("vlan", "VLAN", "VLAN", []string{}, nil),
			typeDoc("cable", "Cable", "Câble", []string{}, nil),
		},
		"relations": []obj{
			{"name": "mounted_in", "label": obj{"en": "mounted in", "fr": "monté dans"}, "inverse": obj{"en": "hosts", "fr": "héberge"}, "description": "", "from": []string{"device"}, "to": []string{"rack"}, "attributes": []obj{
				attr("position", "float", "Position", "Position", obj{"unit": "U"}),
				attr("face", "enum", "Face", "Face", obj{"values": []string{"front", "rear"}}),
			}, "custom": false},
			{"name": "instance_of", "label": obj{"en": "instance of", "fr": "instance de"}, "inverse": obj{"en": "instances", "fr": "instances"}, "description": "", "from": []string{"device"}, "to": []string{"device_type"}, "attributes": []obj{}, "custom": false},
			{"name": "cabled_to", "label": obj{"en": "cabled to", "fr": "câblé à"}, "inverse": obj{"en": "cabled to", "fr": "câblé à"}, "description": "", "from": []string{"interface"}, "to": []string{"interface"}, "attributes": []obj{}, "custom": false},
			{"name": "connects", "label": obj{"en": "connects", "fr": "relie"}, "inverse": obj{"en": "connected by", "fr": "relié par"}, "description": "", "from": []string{"cable"}, "to": []string{"interface"}, "attributes": []obj{}, "custom": false},
			{"name": "neighbor_of", "label": obj{"en": "neighbor of", "fr": "voisin de"}, "inverse": obj{"en": "neighbor of", "fr": "voisin de"}, "description": "", "from": []string{"interface"}, "to": []string{"interface"}, "attributes": []obj{}, "custom": false},
			{"name": "member_of", "label": obj{"en": "member of", "fr": "membre de"}, "inverse": obj{"en": "members", "fr": "membres"}, "description": "", "from": []string{"interface"}, "to": []string{"vlan"}, "attributes": []obj{}, "custom": false},
		},
	}
}

// live answers GET /topology/live as a server with a live block would: a
// fake source measuring every physical interface of the small network and of
// the fabric, with invented, stable figures that put some links under 50 %,
// some between 50 and 80 %, some above, one down, and some not measured at
// all. The aggregates have no series of their own, as on a switch that only
// exports physical ports. generated_at is a few seconds ago.
func (a *api) live() obj {
	links := obj{}
	for id, m := range a.measures() {
		l := obj{"rx_bps": m.rx, "tx_bps": m.tx, "util": max(m.rx, m.tx) / (m.speed * 1e6), "oper": m.oper, "speed_mbps": m.speed, "source": "e2e-fake"}
		links[id] = l
	}
	at := time.Now().Add(-4 * time.Second).UTC().Format(time.RFC3339)
	return obj{"enabled": true, "generated_at": at, "stale": false, "refresh_seconds": 30, "links": links,
		"sources": []obj{{"name": "e2e-fake", "kind": "generic", "series": len(links) * 3, "matched": len(links) * 3, "stale": false}}}
}

// measure is the invented traffic of one interface, in bits per second.
type measure struct {
	rx, tx, speed float64
	oper          string
}

// measures gives every measured physical interface its figures, by id: the
// same for /topology/live and for the map, so that both tell one story.
func (a *api) measures() map[string]measure {
	out := map[string]measure{}
	for _, o := range a.inv.objects {
		if o["type"] != "interface" {
			continue
		}
		id, _ := o["id"].(string)
		attrs, _ := o["attributes"].(obj)
		if attrs["kind"] == "lag" {
			continue
		}
		n := mustHex(id)
		if n%11 == 3 {
			continue // not measured
		}
		const speed = 10000.0
		u := float64((n*7919)%100) / 100 // 0 to 0.99
		tx := u * speed * 1e6
		rx := tx * float64((n*31)%60+20) / 100
		oper := "up"
		if n%37 == 5 {
			oper, tx, rx = "down", 0, 0
		}
		out[id] = measure{rx: rx, tx: tx, speed: speed, oper: oper}
	}
	return out
}
