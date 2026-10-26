package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/topomap"
)

// The map is served by the real topomap package over a graph built from the
// mock inventory, so the browser tests exercise the real aggregation. Four
// things are added to what the inventory declares, only for the map: a VPN
// tunnel from lyon-1 to paris-1, a device uplink (uplinked_to) from a
// lyon-1 switch to a paris-1 one, an Internet access with a bandwidth behind a
// gateway of paris-1, and a NAS with no place. The 200-device synthetic
// fabric is declared in paris-1, outside any room.
const (
	idTunnel   = 0x7001
	idGateway  = 0x7002
	idAccess   = 0x7003
	idNAS      = 0x7004
	idFw       = 0x7005 // fw-paris, the firewall of r12 (the top of its tree)
	idSrv05    = 0x7006 // srv-05, a second server of r12
	firstMapIf = 0x7100 // the interfaces added here
)

// mapSpeeds are the speeds (Mb/s) the map gives to the interfaces of the small
// network, so that every speed class of the legend is drawn somewhere. The
// live figures of an interface are scaled to its speed (see mapLive).
var mapSpeeds = map[int]float64{
	0x200: 10000, 0x201: 10000, // C-0001
	0x202: 1000, 0x203: 1000, // sw-a1 to srv-01
	0x206: 2500, 0x207: 2500, // sw-a2 to srv-01
	0x204: 25000, 0x205: 25000, // paris to lyon
	0x208: 10000, 0x20b: 10000, 0x20c: 10000, 0x20d: 10000, 0x20e: 10000, // the MLAG aggregate
	0x209: 1000, 0x20a: 1000, // lyon
}

var (
	mapOnce sync.Once
	mapSnap *graph.Snapshot
)

func (a *api) mapSnapshot() *graph.Snapshot {
	mapOnce.Do(func() {
		var objs []*store.Object
		created, _ := time.Parse(time.RFC3339, t0)
		add := func(o obj) {
			// Attributes as the store returns them: decoded from JSON.
			var attrs map[string]any
			if raw, err := json.Marshal(o["attributes"]); err == nil {
				_ = json.Unmarshal(raw, &attrs)
			}
			so := &store.Object{ID: o["id"].(string), Type: o["type"].(string), Display: o["display"].(string), Status: "active", Attrs: attrs, CreatedAt: created}
			if p, ok := o["parent_id"].(string); ok {
				so.ParentID = &p
			}
			objs = append(objs, so)
		}
		for _, o := range a.inv.objects {
			add(o)
		}
		for _, o := range objs {
			if o.Type != "interface" {
				continue
			}
			if v, ok := mapSpeeds[int(mustHex(o.ID))]; ok {
				if o.Attrs == nil {
					o.Attrs = map[string]any{}
				}
				o.Attrs["speed_mbps"] = v
			}
		}
		add(object(idTunnel, "tunnel", "vpn-lyon-paris", idSwL1, idSiteL, obj{"kind": "ipsec", "status": "up", "peer": "192.0.2.10"}))
		add(object(idGateway, "gateway", "gw-paris", idSwA1, idSite, obj{"address": "203.0.113.1"}))
		add(object(idAccess, "internet_access", "fibre-paris", 0, idSite, obj{"bandwidth_mbps": 1000, "public_ips": []string{"203.0.113.2"}}))
		add(object(idNAS, "device", "nas-1", 0, 0, obj{"hostname": "nas-1", "vendor": "synology", "mgmt_ip": "192.0.2.77"}))

		// r12 gets the top of a tree: a firewall (with a model) cabled to sw-a1 by two
		// 40G links, and a second server cabled to sw-a2.
		add(object(idFw, "device", "fw-paris", idRack, idSite, obj{"hostname": "fw-paris", "vendor": "example", "role": "firewall", "health": "ok", "model": "ex-fw-100"}))
		add(object(idSrv05, "device", "srv-05", idRack, idSite, obj{"hostname": "srv-05", "vendor": "dell", "role": "server", "health": "warning", "model": "ex-server"}))
		for i, d := range []struct {
			dev  int
			name string
			mbps float64
		}{{idFw, "Ethernet1", 40000}, {idFw, "Ethernet2", 40000}, {idSwA1, "Ethernet7", 40000}, {idSwA1, "Ethernet8", 40000}, {idSrv05, "eno1", 1000}, {idSwA2, "Ethernet7", 1000}} {
			add(object(firstMapIf+i, "interface", d.name, d.dev, idSite, obj{"name": d.name, "speed_mbps": d.mbps}))
		}

		var rels []store.Relation
		n := int64(0)
		rel := func(typ, from, to, conf string, attrs obj) {
			n++
			var at map[string]any
			if raw, err := json.Marshal(attrs); err == nil {
				_ = json.Unmarshal(raw, &at)
			}
			rels = append(rels, store.Relation{ID: n, Type: typ, FromID: from, ToID: to, Attrs: at, Source: "e2e", Confidence: conf, ValidFrom: created})
		}
		for _, l := range a.inv.all() {
			rel(l.typ, oid(l.from), oid(l.to), l.confidence, l.attrs)
		}
		for _, m := range []struct {
			dev, pos int
			t        int
		}{{idFw, 41, idT1U}, {idSrv05, 12, idT2U}} {
			rel("mounted_in", oid(m.dev), oid(idRack), "declared", obj{"position": float64(m.pos), "face": "front"})
			rel("instance_of", oid(m.dev), oid(m.t), "declared", nil)
		}
		rel("cabled_to", oid(firstMapIf), oid(firstMapIf+2), "observed", nil)
		rel("cabled_to", oid(firstMapIf+1), oid(firstMapIf+3), "observed", nil)
		rel("cabled_to", oid(firstMapIf+4), oid(firstMapIf+5), "observed", nil)
		rel("routes_via", oid(idFw), oid(idGateway), "declared", nil)
		rel("routes_via", oid(idSwA1), oid(idGateway), "declared", nil)
		rel("routes_via", oid(idGateway), oid(idAccess), "declared", nil)
		// A device uplink between the two sites (ADR 0062), with no cable
		// between the two switches: the map draws it with an arrow.
		rel("uplinked_to", oid(idSwL2), oid(idSwA1), "observed", nil)
		for _, o := range a.inv.objects {
			id, _ := o["id"].(string)
			if o["type"] == "device" && mustHex(id) >= firstSyn && o["parent_id"] == nil {
				rel("contains", oid(idSite), id, "declared", nil)
			}
		}
		mapSnap = graph.Build(objs, rels, schema.Builtin())
	})
	return mapSnap
}

// mapLive is the live overlay of the map: the figures of /topology/live.
func (a *api) mapLive() map[string]live.Link {
	out := map[string]live.Link{}
	for id, m := range a.measures() {
		if v, ok := mapSpeeds[int(mustHex(id))]; ok {
			// The figures follow the speed the map gives the port.
			m.rx, m.tx, m.speed = m.rx*v/m.speed, m.tx*v/m.speed, v
		}
		rx, tx := m.rx, m.tx
		u := max(rx, tx) / (m.speed * 1e6)
		speed := m.speed
		out[id] = live.Link{RX: &rx, TX: &tx, Oper: m.oper, Util: &u, SpeedMbps: &speed, Source: "e2e-fake"}
	}
	for i := 0; i < 6; i++ {
		rx, tx, u, speed := 4e9+float64(i)*3e9, 11e9+float64(i)*5e9, 0.0, 40000.0
		if i >= 4 {
			rx, tx, speed = 2.1e8, 6.4e8, 1000
		}
		u = max(rx, tx) / (speed * 1e6)
		out[oid(firstMapIf+i)] = live.Link{RX: &rx, TX: &tx, Oper: "up", Util: &u, SpeedMbps: &speed, Source: "e2e-fake"}
	}
	return out
}

// topologyMap answers GET /topology/map as the real handler does, with the
// same error mapping.
func (a *api) topologyMap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	level, id := q.Get("level"), q.Get("id")
	if level == "" {
		problem(w, http.StatusBadRequest, "bad-request", "level is required (global, site, room, rack or unplaced)")
		return
	}
	m, err := topomap.IndexOf(a.mapSnapshot()).View(level, id, topomap.Options{
		Allow: func(string, string) bool { return true },
		Live:  a.mapLive(),
	})
	switch {
	case errors.Is(err, topomap.ErrLevel), errors.Is(err, topomap.ErrMismatch):
		problem(w, http.StatusBadRequest, "bad-request", "The level and the place do not agree")
		return
	case errors.Is(err, topomap.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", "no such place on the map")
		return
	case err != nil:
		problem(w, http.StatusInternalServerError, "internal", "The map could not be computed")
		return
	}
	at := time.Now().Add(-4 * time.Second).UTC()
	m.Live = topomap.MapLive{Enabled: true, GeneratedAt: &at, RefreshSeconds: 30}
	writeJSON(w, http.StatusOK, m)
}
