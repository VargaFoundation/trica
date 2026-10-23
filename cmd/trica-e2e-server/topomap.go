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
// mock inventory, so the browser tests exercise the real aggregation. Three
// things are added to what the inventory declares, only for the map: a VPN
// tunnel from lyon-1 to paris-1, an Internet access with a bandwidth behind a
// gateway of paris-1, and a NAS with no place. The 200-device synthetic
// fabric is declared in paris-1, outside any room.
const (
	idTunnel  = 0x7001
	idGateway = 0x7002
	idAccess  = 0x7003
	idNAS     = 0x7004
)

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
		add(object(idTunnel, "tunnel", "vpn-lyon-paris", idSwL1, idSiteL, obj{"kind": "ipsec", "status": "up", "peer": "192.0.2.10"}))
		add(object(idGateway, "gateway", "gw-paris", idSwA1, idSite, obj{"address": "203.0.113.1"}))
		add(object(idAccess, "internet_access", "fibre-paris", 0, idSite, obj{"bandwidth_mbps": 1000, "public_ips": []string{"203.0.113.2"}}))
		add(object(idNAS, "device", "nas-1", 0, 0, obj{"hostname": "nas-1", "vendor": "synology", "mgmt_ip": "192.0.2.77"}))

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
		rel("routes_via", oid(idSwA1), oid(idGateway), "declared", nil)
		rel("routes_via", oid(idGateway), oid(idAccess), "declared", nil)
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
		rx, tx := m.rx, m.tx
		u := max(rx, tx) / (m.speed * 1e6)
		speed := m.speed
		out[id] = live.Link{RX: &rx, TX: &tx, Oper: m.oper, Util: &u, SpeedMbps: &speed, Source: "e2e-fake"}
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
