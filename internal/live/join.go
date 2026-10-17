package live

import (
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/VargaFoundation/trica/internal/store"
)

// Link is the live state of one interface.
type Link struct {
	// RX and TX are bits per second, averaged over the rate window.
	RX *float64 `json:"rx_bps,omitempty"`
	TX *float64 `json:"tx_bps,omitempty"`
	// Oper is up or down, from the source's link gauge.
	Oper string `json:"oper,omitempty"`
	// Util is max(rx, tx) over the interface speed (speed_mbps), 0 to 1 and
	// beyond on a misreported speed; absent when the speed is unknown.
	Util *float64 `json:"util,omitempty"`
	// Source names the configured source the values come from.
	Source string `json:"source"`
	// Aggregated marks an aggregate (LAG) whose values are the sum of its
	// member ports, because the aggregate itself had no series.
	Aggregated bool `json:"aggregated,omitempty"`
	// Members counts the measured member ports of an aggregated link.
	Members int `json:"members,omitempty"`
}

// Owner types: the objects that hold interfaces.
var ownerTypes = map[string]bool{"device": true, "vm": true, "k8s_node": true}

// InventoryTypes are the object types the join reads.
var InventoryTypes = []string{"device", "vm", "k8s_node", "module", "interface"}

// index finds an interface by its owner's address or name and its own name.
type index struct {
	// owners maps a lower-case key (address, name, short name) to an owner
	// id; a key two owners share is ambiguous and removed.
	owners map[string]string
	// ifaces maps owner id + "\x00" + canonical name to interface id.
	ifaces map[string]string
	// speed is the speed_mbps of each interface, when known.
	speed map[string]float64
	// members maps an aggregate interface to its member interfaces.
	members map[string][]string
}

func newIndex(objs []*store.Object, rels []store.Relation) *index {
	ix := &index{owners: map[string]string{}, ifaces: map[string]string{}, speed: map[string]float64{}, members: map[string][]string{}}
	byID := map[string]*store.Object{}
	for _, o := range objs {
		if o.Status != "absent" {
			byID[o.ID] = o
		}
	}
	ambiguous := map[string]bool{}
	addOwner := func(key, id string) {
		if key == "" {
			return
		}
		if prev, ok := ix.owners[key]; ok && prev != id {
			ambiguous[key] = true
			return
		}
		ix.owners[key] = id
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := byID[id]
		if !ownerTypes[o.Type] {
			continue
		}
		for _, k := range []string{"mgmt_ip", "bmc_ip", "primary_ip"} {
			if v, ok := o.Attrs[k].(string); ok {
				addOwner(normalizeAddr(v), id)
			}
		}
		if n, ok := o.Attrs["name"].(string); ok {
			for _, k := range nameKeys(n) {
				addOwner(k, id)
			}
		}
	}
	for k := range ambiguous {
		delete(ix.owners, k)
	}
	for _, id := range ids {
		o := byID[id]
		if o.Type != "interface" {
			continue
		}
		owner := ownerOf(byID, o)
		name, _ := o.Attrs["name"].(string)
		if owner == "" || name == "" {
			continue
		}
		ix.ifaces[owner+"\x00"+CanonicalInterface(name)] = id
		if sp, ok := number(o.Attrs["speed_mbps"]); ok && sp > 0 {
			ix.speed[id] = sp
		}
	}
	for _, r := range rels {
		if r.Type != "member_of" || r.FromID == r.ToID {
			continue
		}
		from, to := byID[r.FromID], byID[r.ToID]
		if from != nil && to != nil && from.Type == "interface" && to.Type == "interface" {
			ix.members[r.ToID] = append(ix.members[r.ToID], r.FromID)
		}
	}
	return ix
}

// ownerOf walks up through modules to the device, VM or node.
func ownerOf(byID map[string]*store.Object, o *store.Object) string {
	for range 8 {
		if o.ParentID == nil {
			return ""
		}
		p := byID[*o.ParentID]
		if p == nil {
			return ""
		}
		if ownerTypes[p.Type] {
			return p.ID
		}
		if p.Type != "module" {
			return ""
		}
		o = p
	}
	return ""
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// normalizeAddr returns the canonical text of an address, "" when v is not
// one.
func normalizeAddr(v string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	return a.Unmap().String()
}

// nameKeys are the keys of a name: itself and its first label, lower case.
func nameKeys(n string) []string {
	n = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n), "."))
	if n == "" {
		return nil
	}
	keys := []string{n}
	if i := strings.IndexByte(n, '.'); i > 0 && normalizeAddr(n) == "" {
		keys = append(keys, n[:i])
	}
	return keys
}

// lookupOwner resolves a label value: an address (with a port or not), a
// name, or a fully qualified name whose first label is known.
func (ix *index) lookupOwner(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	cands := []string{v}
	if h, _, err := net.SplitHostPort(v); err == nil {
		cands = append(cands, h)
	}
	if strings.Contains(v, "://") {
		if i := strings.Index(v, "://"); i >= 0 {
			rest := v[i+3:]
			if j := strings.IndexAny(rest, "/?"); j >= 0 {
				rest = rest[:j]
			}
			cands = append(cands, rest)
			if h, _, err := net.SplitHostPort(rest); err == nil {
				cands = append(cands, h)
			}
		}
	}
	for _, c := range cands {
		if a := normalizeAddr(strings.Trim(c, "[]")); a != "" {
			if id, ok := ix.owners[a]; ok {
				return id
			}
			continue
		}
		for _, k := range nameKeys(c) {
			if id, ok := ix.owners[k]; ok {
				return id
			}
		}
	}
	return ""
}

// aristaShort expands the abbreviations EOS and its exporters use for port
// names (Et1, Po10, Ma1) so that they meet the names the collector stores.
var aristaShort = regexp.MustCompile(`^(et|po|ma|vl|lo)(\d)`)

var aristaLong = map[string]string{"et": "ethernet", "po": "port-channel", "ma": "management", "vl": "vlan", "lo": "loopback"}

// CanonicalInterface is the form interface names are compared in: lower
// case, trimmed, with the EOS abbreviations expanded ("Et1/1" and
// "Ethernet1/1" meet; "eth0" stays "eth0").
func CanonicalInterface(n string) string {
	n = strings.ToLower(strings.TrimSpace(n))
	if m := aristaShort.FindStringSubmatch(n); m != nil {
		n = aristaLong[m[1]] + n[len(m[1]):]
	}
	return n
}

// measure is what one source says of one interface.
type measure struct {
	rx, tx   *float64
	oper     string
	hasValue bool
}

// sourceResult is the answer to the queries of one source; nil vectors are
// queries that were not configured or failed with no previous value.
type sourceResult struct {
	src          *source
	rx, tx, oper []sample
}

// joinStats counts the series of one source.
type joinStats struct {
	series, matched int
}

// join resolves the series of one source to interfaces.
func (ix *index) join(r sourceResult) (map[string]*measure, joinStats) {
	out := map[string]*measure{}
	var st joinStats
	resolve := func(sm sample) (id string, kept bool) {
		ifName := sm.labels[r.src.ifLabel]
		if ifName == "" || (r.src.exclude != nil && r.src.exclude.MatchString(ifName)) {
			return "", false
		}
		for _, l := range r.src.deviceLabels {
			if owner := ix.lookupOwner(sm.labels[l]); owner != "" {
				return ix.ifaces[owner+"\x00"+CanonicalInterface(ifName)], true
			}
		}
		return "", true
	}
	// Two series for one interface (two scrapers, a duplicate job) are the
	// same traffic counted twice: keep the highest, never the sum.
	maxInto := func(p **float64, v float64) {
		if *p == nil || v > **p {
			x := v
			*p = &x
		}
	}
	const (
		rx = iota
		tx
		oper
	)
	for what, vec := range [][]sample{rx: r.rx, tx: r.tx, oper: r.oper} {
		for _, sm := range vec {
			id, kept := resolve(sm)
			if !kept {
				continue
			}
			st.series++
			if id == "" {
				continue
			}
			st.matched++
			m := out[id]
			if m == nil {
				m = &measure{}
				out[id] = m
			}
			m.hasValue = true
			switch what {
			case rx:
				maxInto(&m.rx, sm.value*r.src.scale)
			case tx:
				maxInto(&m.tx, sm.value*r.src.scale)
			default:
				if sm.value > 0 {
					m.oper = "up"
				} else if m.oper == "" {
					m.oper = "down"
				}
			}
		}
	}
	return out, st
}

// build joins every source and adds the aggregates.
func (ix *index) build(results []sourceResult) (map[string]Link, []joinStats) {
	links := map[string]Link{}
	stats := make([]joinStats, len(results))
	for i, r := range results {
		ms, st := ix.join(r)
		stats[i] = st
		for id, m := range ms {
			if _, taken := links[id]; taken || !m.hasValue {
				continue
			}
			l := Link{RX: m.rx, TX: m.tx, Oper: m.oper, Source: r.src.name}
			l.Util = util(l.RX, l.TX, ix.speed[id])
			links[id] = l
		}
	}
	// An aggregate without series of its own is the sum of its members.
	lags := make([]string, 0, len(ix.members))
	for id := range ix.members {
		lags = append(lags, id)
	}
	sort.Strings(lags)
	for _, lag := range lags {
		if _, has := links[lag]; has {
			continue
		}
		var agg Link
		var rx, tx, speed float64
		var hasRX, hasTX bool
		for _, m := range ix.members[lag] {
			l, ok := links[m]
			if !ok {
				continue
			}
			agg.Members++
			if agg.Source == "" {
				agg.Source = l.Source
			}
			if l.RX != nil {
				rx, hasRX = rx+*l.RX, true
			}
			if l.TX != nil {
				tx, hasTX = tx+*l.TX, true
			}
			switch {
			case l.Oper == "up":
				agg.Oper = "up"
			case l.Oper == "down" && agg.Oper == "":
				agg.Oper = "down"
			}
			speed += ix.speed[m]
		}
		if agg.Members == 0 {
			continue
		}
		agg.Aggregated = true
		if hasRX {
			agg.RX = &rx
		}
		if hasTX {
			agg.TX = &tx
		}
		if sp := ix.speed[lag]; sp > 0 {
			speed = sp
		}
		agg.Util = util(agg.RX, agg.TX, speed)
		links[lag] = agg
	}
	return links, stats
}

func util(rx, tx *float64, speedMbps float64) *float64 {
	if speedMbps <= 0 || (rx == nil && tx == nil) {
		return nil
	}
	m := 0.0
	if rx != nil {
		m = *rx
	}
	if tx != nil && *tx > m {
		m = *tx
	}
	u := m / (speedMbps * 1e6)
	return &u
}
