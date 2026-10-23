package topomap

import (
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/place"
)

// maxDepth bounds every walk up the parents, like the rights query does.
const maxDepth = 32

// locationTypes are the objects a device can be in.
var locationTypes = map[string]bool{"site": true, "building": true, "room": true, "row": true, "rack": true}

// groupTypes are the locations that only group: they are never a node.
func isGroup(t string) bool { return t == "building" || t == "row" }

// confidence ranks, strongest first.
func rankOf(c string) int {
	switch c {
	case "declared":
		return 3
	case "observed":
		return 2
	}
	return 1
}

func confOf(rank int) string {
	switch rank {
	case 3:
		return "declared"
	case 2:
		return "observed"
	}
	return "deduced"
}

// Index is everything about a snapshot the views share: where each place and
// device is, and the links between devices. It is immutable once built, except
// for the memoised rights lookups.
type Index struct {
	Snap *graph.Snapshot

	// path maps a placed location or device to the ids from its site down to
	// itself. A device with no site is [unplaced, device].
	path map[string][]string
	// ambiguous lists the devices mounted in several racks.
	ambiguous map[string]bool

	links   []*link
	noPlace []string
	tunnels []*tunnelItem
	uplinks []*uplinkItem

	auth sync.Map // id -> string, the memoised AuthSite
}

var memo atomic.Pointer[Index]

// IndexOf returns the index of snap, built once: a request on the snapshot a
// previous one used costs nothing.
func IndexOf(snap *graph.Snapshot) *Index {
	if ix := memo.Load(); ix != nil && ix.Snap == snap {
		return ix
	}
	ix := newIndex(snap)
	memo.Store(ix)
	return ix
}

func attrStr(n *graph.Node, key string) string {
	if n == nil {
		return ""
	}
	s, _ := n.Attrs[key].(string)
	return s
}

func attrNum(n *graph.Node, key string) (float64, bool) {
	if n == nil {
		return 0, false
	}
	switch v := n.Attrs[key].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	}
	return 0, false
}

func edgeNum(e graph.Edge, key string) (float64, bool) {
	n := &graph.Node{Attrs: e.Attrs}
	return attrNum(n, key)
}

// AuthSite returns the site the rights see id in: the same answer as the
// rights query (internal/api siteIDs), computed from the snapshot by the same
// rule (place.Graph, ADR 0058): the parents, the current mounted_in relations
// and, for a device in no rack, its current located_in. The object is in the
// one site it reaches; none or two different sites give "", which fails closed.
// The wider placement used to draw the map is never used for rights.
func (ix *Index) AuthSite(id string) string {
	if v, ok := ix.auth.Load(id); ok {
		return v.(string)
	}
	site := ix.authSite(id)
	ix.auth.Store(id, site)
	return site
}

func (ix *Index) authSite(id string) string {
	return place.Graph{
		Type: func(id string) string {
			if n := ix.Snap.Nodes[id]; n != nil {
				return n.Type
			}
			return ""
		},
		Parent: func(id string) string {
			if n := ix.Snap.Nodes[id]; n != nil {
				return n.ParentID
			}
			return ""
		},
		Targets: func(id, relType string) []string {
			var out []string
			for _, e := range ix.Snap.Out[id] {
				if e.Type == relType {
					out = append(out, e.To)
				}
			}
			return out
		},
	}.Site(id)
}

// locPath returns the ids from the site down to location id, nil when the
// chain does not reach a site.
func (ix *Index) locPath(id string) []string {
	var rev []string
	cur := id
	for i := 0; i < maxDepth; i++ {
		n := ix.Snap.Nodes[cur]
		if n == nil || !locationTypes[n.Type] || n.Status == graph.StatusAbsent {
			return nil
		}
		rev = append(rev, cur)
		if n.Type == "site" {
			out := make([]string, len(rev))
			for j, v := range rev {
				out[len(rev)-1-j] = v
			}
			return out
		}
		cur = n.ParentID
	}
	return nil
}

func newIndex(snap *graph.Snapshot) *Index {
	ix := &Index{Snap: snap, path: map[string][]string{}, ambiguous: map[string]bool{}}
	ids := make([]string, 0, len(snap.Nodes))
	for id := range snap.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if n := snap.Nodes[id]; locationTypes[n.Type] {
			if p := ix.locPath(id); p != nil {
				ix.path[id] = p
			}
		}
	}
	for _, id := range ids {
		if n := snap.Nodes[id]; n.Type == "device" && n.Status != graph.StatusAbsent {
			ix.path[id] = ix.placeDevice(n)
		}
	}
	ix.collectLinks(ids)
	ix.collectTunnels(ids)
	ix.collectUplinks(ids)
	return ix
}

// placeDevice finds where a device is: the rack it is mounted in (the most
// trusted claim, then the lowest id, when several); when it is mounted in no
// rack, the site, building or room it is located_in (ADR 0058: a rack always
// wins), else the location that contains it, else its parent, else nowhere.
func (ix *Index) placeDevice(d *graph.Node) []string {
	best, bestRank, n := "", 0, 0
	racked := false
	for _, e := range ix.Snap.Out[d.ID] {
		if e.Type != "mounted_in" {
			continue
		}
		racked = true
		if ix.path[e.To] == nil {
			continue
		}
		n++
		r := rankOf(e.Confidence)
		if best == "" || r > bestRank || (r == bestRank && e.To < best) {
			best, bestRank = e.To, r
		}
	}
	if n > 1 {
		ix.ambiguous[d.ID] = true
	}
	if best != "" {
		return appendPath(ix.path[best], d.ID)
	}
	var in string
	if !racked {
		for _, e := range ix.Snap.Out[d.ID] {
			if e.Type == place.LocatedIn && ix.path[e.To] != nil && (in == "" || e.To < in) {
				in = e.To
			}
		}
	}
	for _, e := range ix.Snap.In[d.ID] {
		if in != "" {
			break
		}
		if e.Type == "contains" && ix.path[e.From] != nil && locationTypes[ix.Snap.Nodes[e.From].Type] {
			if in == "" || e.From < in {
				in = e.From
			}
		}
	}
	if in == "" && d.ParentID != "" && ix.path[d.ParentID] != nil {
		in = d.ParentID
	}
	if in != "" {
		return appendPath(ix.path[in], d.ID)
	}
	return []string{UnplacedID, d.ID}
}

func appendPath(p []string, id string) []string {
	out := make([]string, len(p)+1)
	copy(out, p)
	out[len(p)] = id
	return out
}

// owner returns the device, VM or node a port belongs to, going through its
// modules.
func (ix *Index) owner(id string) *graph.Node {
	cur := ix.Snap.Nodes[id]
	for i := 0; cur != nil && i < maxDepth; i++ {
		switch cur.Type {
		case "device", "vm", "k8s_node":
			return cur
		}
		cur = ix.Snap.Nodes[cur.ParentID]
	}
	return nil
}

// ---------------------------------------------------------------- links

type link struct {
	a, b      string // port ids, a < b
	da, db    string // owner devices
	kind      string // cable, cabled_to, neighbor_of
	rank      int    // strongest relation asserting the pair
	aggregate bool   // both ends are aggregate interfaces
	capMbps   float64
	capKnown  bool
}

var kindPriority = map[string]int{"cable": 3, "cabled_to": 2, "neighbor_of": 1}

func isPort(t string) bool { return t == "interface" || t == "front_port" || t == "rear_port" }

func (ix *Index) collectLinks(ids []string) {
	pairs := map[[2]string]*link{}
	add := func(x, y, kind, conf string) {
		nx, ny := ix.Snap.Nodes[x], ix.Snap.Nodes[y]
		if nx == nil || ny == nil || x == y || !isPort(nx.Type) || !isPort(ny.Type) {
			return
		}
		if nx.Status == graph.StatusAbsent || ny.Status == graph.StatusAbsent {
			return
		}
		if y < x {
			x, y = y, x
		}
		ox, oy := ix.owner(x), ix.owner(y)
		if ox == nil || oy == nil {
			ix.noPlace = append(ix.noPlace, x)
			return
		}
		if ox.Type != "device" || oy.Type != "device" {
			ix.noPlace = append(ix.noPlace, x)
			return
		}
		if ox.ID == oy.ID || ox.Status == graph.StatusAbsent || oy.Status == graph.StatusAbsent {
			return
		}
		k := [2]string{x, y}
		l := pairs[k]
		if l == nil {
			l = &link{a: x, b: y, da: ox.ID, db: oy.ID, kind: kind}
			pairs[k] = l
		}
		if kindPriority[kind] > kindPriority[l.kind] {
			l.kind = kind
		}
		if r := rankOf(conf); r > l.rank {
			l.rank = r
		}
	}
	for _, id := range ids {
		n := ix.Snap.Nodes[id]
		var ends []graph.Edge
		for _, e := range ix.Snap.Out[id] {
			switch e.Type {
			case "cabled_to":
				add(e.From, e.To, "cabled_to", e.Confidence)
			case "neighbor_of":
				add(e.From, e.To, "neighbor_of", e.Confidence)
			case "connects":
				ends = append(ends, e)
			}
		}
		if n.Type == "cable" && n.Status != graph.StatusAbsent && len(ends) == 2 {
			conf := ends[0].Confidence
			if rankOf(ends[1].Confidence) < rankOf(conf) {
				conf = ends[1].Confidence
			}
			add(ends[0].To, ends[1].To, "cable", conf)
		}
	}
	// The LAG guard: an aggregate interface carries the sum of its members, so
	// a link between two aggregates is dropped when a link between members of
	// those aggregates is already counted (its traffic would be counted twice).
	lagOf := map[string]string{}
	for _, id := range ids {
		for _, e := range ix.Snap.Out[id] {
			if e.Type == "member_of" {
				if t := ix.Snap.Nodes[e.To]; t != nil && attrStr(t, "kind") == "lag" {
					lagOf[e.From] = e.To
				}
			}
		}
	}
	memberPairs := map[[2]string]bool{}
	list := make([]*link, 0, len(pairs))
	for _, l := range pairs {
		list = append(list, l)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].a != list[j].a {
			return list[i].a < list[j].a
		}
		return list[i].b < list[j].b
	})
	for _, l := range list {
		la, lb := lagOf[l.a], lagOf[l.b]
		if la != "" && lb != "" {
			if la > lb {
				la, lb = lb, la
			}
			memberPairs[[2]string{la, lb}] = true
		}
	}
	for _, l := range list {
		na, nb := ix.Snap.Nodes[l.a], ix.Snap.Nodes[l.b]
		if attrStr(na, "kind") == "lag" && attrStr(nb, "kind") == "lag" {
			l.aggregate = true
			x, y := l.a, l.b
			if x > y {
				x, y = y, x
			}
			if memberPairs[[2]string{x, y}] {
				continue
			}
		}
		sa, oka := attrNum(na, "speed_mbps")
		sb, okb := attrNum(nb, "speed_mbps")
		switch {
		case oka && okb:
			l.capMbps, l.capKnown = min(sa, sb), true
		case oka:
			l.capMbps, l.capKnown = sa, true
		case okb:
			l.capMbps, l.capKnown = sb, true
		}
		if l.capMbps <= 0 {
			l.capKnown = false
		}
		ix.links = append(ix.links, l)
	}
}

// ---------------------------------------------------------------- tunnels

type tunnelItem struct {
	id       string
	owner    string
	kind     string
	status   string
	peer     string
	conf     int
	target   []string // path of the peer, [internet] when unresolved
	resolved bool
}

// normKey makes a peer or an address comparable: no brackets, port, prefix
// length or case.
func normKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			s = s[1:i]
		}
	} else if host, _, ok := strings.Cut(s, ":"); ok && strings.Count(s, ":") == 1 {
		s = host
	}
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return s
}

type resolver struct {
	keys map[string]string // key -> target path joined, "" when ambiguous
	path map[string][]string
}

func (r *resolver) add(key string, target []string) {
	if key == "" || len(target) == 0 {
		return
	}
	joined := strings.Join(target, "/")
	if cur, ok := r.keys[key]; ok && cur != joined {
		r.keys[key] = ""
		return
	}
	r.keys[key] = joined
	r.path[joined] = target
}

func (ix *Index) peerResolver() *resolver {
	r := &resolver{keys: map[string]string{}, path: map[string][]string{}}
	deviceOf := func(id string) []string {
		if o := ix.owner(id); o != nil && o.Type == "device" && ix.path[o.ID] != nil {
			return ix.path[o.ID]
		}
		return nil
	}
	ids := make([]string, 0, len(ix.Snap.Nodes))
	for id := range ix.Snap.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := ix.Snap.Nodes[id]
		if n.Status == graph.StatusAbsent {
			continue
		}
		switch n.Type {
		case "device":
			p := ix.path[id]
			r.add(normKey(attrStr(n, "mgmt_ip")), p)
			r.add(normKey(attrStr(n, "primary_ip")), p)
			name := strings.ToLower(attrStr(n, "name"))
			r.add(name, p)
			if i := strings.Index(name, "."); i > 0 {
				r.add(name[:i], p)
			}
		case "ip_address":
			for _, e := range ix.Snap.Out[id] {
				if e.Type != "assigned_to" {
					continue
				}
				if p := deviceOf(e.To); p != nil {
					r.add(normKey(attrStr(n, "address")), p)
				}
			}
		case "internet_access":
			site := ix.accessSite(id)
			if site == "" {
				continue
			}
			ips, _ := n.Attrs["public_ips"].([]any)
			for _, v := range ips {
				if s, ok := v.(string); ok {
					r.add(normKey(s), []string{site})
				}
			}
		}
	}
	return r
}

// accessSite is the site of the device routing through an Internet access.
func (ix *Index) accessSite(id string) string {
	var site string
	var visit func(id string, depth int)
	visit = func(id string, depth int) {
		for _, e := range ix.Snap.In[id] {
			if e.Type != "routes_via" {
				continue
			}
			src := ix.Snap.Nodes[e.From]
			switch {
			case src.Type == "device":
				if p := ix.path[src.ID]; len(p) > 0 && p[0] != UnplacedID && (site == "" || p[0] < site) {
					site = p[0]
				}
			case src.Type == "gateway" && depth < 2:
				visit(src.ID, depth+1)
				if o := ix.gatewayOwner(src.ID); o != "" {
					if p := ix.path[o]; len(p) > 0 && p[0] != UnplacedID && (site == "" || p[0] < site) {
						site = p[0]
					}
				}
			}
		}
	}
	visit(id, 0)
	return site
}

// gatewayOwner is the device that holds a gateway or a tunnel.
func (ix *Index) gatewayOwner(id string) string {
	owner := ""
	for _, e := range ix.Snap.In[id] {
		if e.Type == "contains" {
			if n := ix.Snap.Nodes[e.From]; n != nil && n.Type == "device" && n.Status != graph.StatusAbsent {
				if owner == "" || e.From < owner {
					owner = e.From
				}
			}
		}
	}
	if owner == "" {
		if n := ix.Snap.Nodes[id]; n != nil && n.ParentID != "" {
			if p := ix.Snap.Nodes[n.ParentID]; p != nil && p.Type == "device" && p.Status != graph.StatusAbsent {
				owner = p.ID
			}
		}
	}
	return owner
}

func (ix *Index) containsConf(id string) int {
	best := 0
	for _, e := range ix.Snap.In[id] {
		if e.Type == "contains" {
			if r := rankOf(e.Confidence); r > best {
				best = r
			}
		}
	}
	if best == 0 {
		best = 1
	}
	return best
}

func (ix *Index) collectTunnels(ids []string) {
	var res *resolver
	for _, id := range ids {
		n := ix.Snap.Nodes[id]
		if n.Type != "tunnel" || n.Status == graph.StatusAbsent {
			continue
		}
		owner := ix.gatewayOwner(id)
		if owner == "" {
			continue
		}
		t := &tunnelItem{id: id, owner: owner, kind: attrStr(n, "kind"), status: attrStr(n, "status"), peer: attrStr(n, "peer"), conf: ix.containsConf(id)}
		if t.kind == "" {
			t.kind = "other"
		}
		if t.status == "" {
			t.status = "unknown"
		}
		t.target = []string{InternetID}
		if key := normKey(t.peer); key != "" {
			if res == nil {
				res = ix.peerResolver()
			}
			joined := res.keys[key]
			if _, err := netip.ParseAddr(key); joined == "" && err != nil {
				// a host name: its short name is what a device is known by
				if i := strings.Index(key, "."); i > 0 {
					joined = res.keys[key[:i]]
				}
			}
			if joined != "" {
				p := res.path[joined]
				if len(p) > 0 && p[len(p)-1] != owner {
					t.target, t.resolved = p, true
				} else {
					continue // the peer is the owner itself
				}
			}
		}
		ix.tunnels = append(ix.tunnels, t)
	}
}

// ---------------------------------------------------------------- uplinks

type uplinkItem struct {
	device   string
	gateways []*graph.Node
	access   *graph.Node
	conf     int
	// wan is the routing device's interface facing the gateway, "" when it
	// cannot be told.
	wan string
}

type wanIface struct {
	iface  string
	prefix netip.Prefix
}

func (ix *Index) wanIndex() map[string][]wanIface {
	out := map[string][]wanIface{}
	for id, n := range ix.Snap.Nodes {
		if n.Type != "ip_address" || n.Status == graph.StatusAbsent {
			continue
		}
		pfx, ok := parsePrefix(attrStr(n, "address"), n)
		if !ok {
			continue
		}
		for _, e := range ix.Snap.Out[id] {
			if e.Type != "assigned_to" {
				continue
			}
			if t := ix.Snap.Nodes[e.To]; t != nil && t.Type == "interface" {
				if o := ix.owner(t.ID); o != nil {
					out[o.ID] = append(out[o.ID], wanIface{t.ID, pfx})
				}
			}
		}
	}
	return out
}

func parsePrefix(addr string, n *graph.Node) (netip.Prefix, bool) {
	addr = strings.TrimSpace(addr)
	if p, err := netip.ParsePrefix(addr); err == nil {
		return p, true
	}
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return netip.Prefix{}, false
	}
	bits, ok := attrNum(n, "prefix_length")
	if !ok {
		return netip.Prefix{}, false
	}
	p, err := a.Prefix(int(bits))
	return p, err == nil
}

func (ix *Index) collectUplinks(ids []string) {
	byKey := map[string]*uplinkItem{}
	var order []string
	add := func(device string, gw, ia *graph.Node, conf string) {
		key := device + "|"
		switch {
		case ia != nil:
			key += ia.ID
		case gw != nil:
			key += gw.ID
		default:
			return
		}
		u := byKey[key]
		if u == nil {
			u = &uplinkItem{device: device, access: ia}
			byKey[key] = u
			order = append(order, key)
		}
		if gw != nil {
			dup := false
			for _, g := range u.gateways {
				dup = dup || g.ID == gw.ID
			}
			if !dup {
				u.gateways = append(u.gateways, gw)
			}
		}
		if r := rankOf(conf); r > u.conf {
			u.conf = r
		}
	}
	accessVia := func(gw *graph.Node) *graph.Node {
		var best *graph.Node
		for _, e := range ix.Snap.Out[gw.ID] {
			if t := ix.Snap.Nodes[e.To]; e.Type == "routes_via" && t != nil && t.Type == "internet_access" && t.Status != graph.StatusAbsent {
				if best == nil || t.ID < best.ID {
					best = t
				}
			}
		}
		return best
	}
	for _, id := range ids {
		n := ix.Snap.Nodes[id]
		if n.Status == graph.StatusAbsent {
			continue
		}
		switch n.Type {
		case "device":
			if ix.path[id] == nil {
				continue
			}
			for _, e := range ix.Snap.Out[id] {
				t := ix.Snap.Nodes[e.To]
				if e.Type != "routes_via" || t == nil || t.Status == graph.StatusAbsent {
					continue
				}
				switch t.Type {
				case "gateway":
					add(id, t, accessVia(t), e.Confidence)
				case "internet_access":
					add(id, nil, t, e.Confidence)
				}
			}
		case "gateway":
			if o := ix.gatewayOwner(id); o != "" && ix.path[o] != nil {
				add(o, n, accessVia(n), confOf(ix.containsConf(id)))
			}
		}
	}
	var wan map[string][]wanIface
	for _, key := range order {
		u := byKey[key]
		for _, g := range u.gateways {
			if wan == nil {
				wan = ix.wanIndex()
			}
			addr, err := netip.ParseAddr(normKey(attrStr(g, "address")))
			if err != nil {
				continue
			}
			matches := map[string]bool{}
			for _, w := range wan[u.device] {
				if w.prefix.Contains(addr) {
					matches[w.iface] = true
				}
			}
			if len(matches) == 1 && u.wan == "" {
				for m := range matches {
					u.wan = m
				}
			} else if len(matches) > 1 {
				u.wan = ""
				break
			}
		}
		ix.uplinks = append(ix.uplinks, u)
	}
}
