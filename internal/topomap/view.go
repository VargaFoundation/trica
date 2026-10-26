package topomap

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/live"
)

// The errors View answers with; the API maps them to 400, 404 and 403.
var (
	ErrLevel     = errors.New("topomap: unknown level")
	ErrMismatch  = errors.New("topomap: the id does not fit the level")
	ErrNotFound  = errors.New("topomap: no such place on the map")
	ErrForbidden = errors.New("topomap: the place is not readable")
)

// Options are what a view needs besides the snapshot.
type Options struct {
	// Live are the measurements by interface id; nil without a live source.
	Live map[string]live.Link
	// Allow says whether the caller reads objects of a type in a site ("" for
	// an object in no site).
	Allow func(objType, site string) bool
}

// view is one computation of a level.
type view struct {
	ix   *Index
	opt  Options
	snap *graph.Snapshot

	level     string
	focus     string
	container []string
	siteFloor bool

	can   map[string]bool
	nodes map[string]*MapNode
	edges map[string]*acc
}

func (v *view) typeOf(id string) string {
	if n := v.snap.Nodes[id]; n != nil {
		return n.Type
	}
	return id
}

func (v *view) display(id string) string {
	switch id {
	case InternetID:
		return "Internet"
	case UnplacedID:
		return "Unplaced"
	}
	if n := v.snap.Nodes[id]; n != nil {
		return n.Display
	}
	return id
}

// readable reports whether the caller reads object id where the rights see it.
func (v *view) readable(id string) bool {
	if id == InternetID || id == UnplacedID {
		return true
	}
	if r, ok := v.can[id]; ok {
		return r
	}
	n := v.snap.Nodes[id]
	r := n != nil && v.opt.Allow(n.Type, v.ix.AuthSite(id))
	v.can[id] = r
	return r
}

// View computes one level of the map. level is global, site, room, rack or
// unplaced; id is the focus (a site, a room or a site, a rack) and must be
// empty for the other two.
func (ix *Index) View(level, id string, opt Options) (*TopologyMap, error) {
	v := &view{ix: ix, opt: opt, snap: ix.Snap, level: level, focus: id,
		can: map[string]bool{}, nodes: map[string]*MapNode{}, edges: map[string]*acc{}}
	if v.opt.Allow == nil {
		v.opt.Allow = func(string, string) bool { return true }
	}
	out := &TopologyMap{Level: level, Trail: []MapPlace{}, InventoryAt: ix.Snap.Built, Nodes: []MapNode{}, Edges: []MapEdge{}}
	switch level {
	case LevelGlobal, LevelUnplaced:
		if id != "" {
			return nil, ErrMismatch
		}
		if level == LevelUnplaced {
			v.container = []string{UnplacedID}
		}
	case LevelSite, LevelRoom, LevelRack:
		if id == "" {
			return nil, ErrMismatch
		}
		n := ix.Snap.Nodes[id]
		if n == nil || n.Status == graph.StatusAbsent {
			return nil, ErrNotFound
		}
		okType := n.Type == level || (level == LevelRoom && n.Type == "site")
		if !okType {
			return nil, ErrMismatch
		}
		p := ix.path[id]
		if p == nil {
			return nil, ErrNotFound
		}
		if !v.readable(id) {
			return nil, ErrForbidden
		}
		v.container = p
		v.siteFloor = level == LevelRoom && n.Type == "site"
		out.Focus = v.place(id)
		if level == LevelRoom && !v.siteFloor {
			if x, ok := attrNum(n, "width_mm"); ok {
				out.Focus.WidthMM = int(x)
			}
			if x, ok := attrNum(n, "depth_mm"); ok {
				out.Focus.DepthMM = int(x)
			}
			out.Focus.TileMM = 600
			if x, ok := attrNum(n, "tile_mm"); ok && x > 0 {
				out.Focus.TileMM = int(x)
			}
		}
		for _, a := range p[:len(p)-1] {
			if t := v.typeOf(a); (t == "site" || t == "room") && v.readable(a) {
				out.Trail = append(out.Trail, *v.place(a))
			}
		}
	default:
		return nil, ErrLevel
	}

	v.buildNodes()
	v.buildEdges()
	v.finish(out)
	return out, nil
}

func (v *view) place(id string) *MapPlace {
	n := v.snap.Nodes[id]
	return &MapPlace{ID: id, Type: n.Type, Display: n.Display}
}

// ---------------------------------------------------------------- classify

// end says where one end of a link falls at the level.
type end struct {
	self   bool   // the end is the focus itself: nothing to draw
	inside bool   // the end is in a node of the level
	node   string // that node's id, or the portal's
	elem   string // the object (or pseudo place) the node stands for
}

func (v *view) under(p []string) bool {
	c := v.container
	if len(p) <= len(c) {
		return false
	}
	for i := range c {
		if p[i] != c[i] {
			return false
		}
	}
	return true
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v *view) firstNonGroup(p []string, from int) int {
	for i := from; i < len(p); i++ {
		if !isGroup(v.typeOf(p[i])) {
			return i
		}
	}
	return -1
}

func (v *view) classify(p []string) end {
	if v.level == LevelGlobal {
		return end{inside: true, node: p[0], elem: p[0]}
	}
	if equal(p, v.container) {
		return end{self: true}
	}
	if v.under(p) {
		i := v.firstNonGroup(p, len(v.container))
		if i >= 0 {
			t := v.typeOf(p[i])
			switch {
			case v.level == LevelSite && t != "room":
				return end{inside: true, node: "area:" + v.container[0], elem: ""}
			case v.siteFloor && t == "room":
				// a room is outside the floor of the site: a portal
			default:
				return end{inside: true, node: p[i], elem: p[i]}
			}
		}
	}
	return v.portal(p)
}

// portal returns the place the end is seen as from outside the level: the
// object one level below the lowest common ancestor of the focus and the end,
// groups (buildings, rows) and devices set aside.
func (v *view) portal(p []string) end {
	c := 0
	for c < len(p) && c < len(v.container) && p[c] == v.container[c] {
		c++
	}
	i := c
	if i >= len(p) {
		i = len(p) - 1
	}
	if c > 0 {
		// skip groups going down, then settle on a place, not a device
		for i < len(p)-1 && isGroup(v.typeOf(p[i])) {
			i++
		}
	}
	for i > 0 && (isGroup(v.typeOf(p[i])) || v.typeOf(p[i]) == "device") {
		i--
	}
	e := p[i]
	return end{node: "portal:" + e, elem: e}
}

// ---------------------------------------------------------------- nodes

func (v *view) buildNodes() {
	ids := make([]string, 0, len(v.ix.path))
	for id := range v.ix.path {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := v.ix.path[id]
		e := v.classify(p)
		if !e.inside {
			continue
		}
		t := v.typeOf(id)
		if t == "building" || t == "row" || (t == "site" && v.level != LevelGlobal) {
			continue
		}
		if !v.readable(id) {
			continue
		}
		if e.elem != "" && !v.readable(e.elem) {
			continue
		}
		nd := v.nodes[e.node]
		if nd == nil {
			nd = v.newNode(e)
			v.nodes[e.node] = nd
		}
		self := e.elem == id
		switch t {
		case "room":
			if !self {
				nd.Counts.Rooms++
			}
		case "rack":
			if !self {
				nd.Counts.Racks++
			}
		case "device":
			if !self {
				nd.Counts.Devices++
			}
			v.health(nd, id)
		}
	}
}

func (v *view) newNode(e end) *MapNode {
	nd := &MapNode{ID: e.node}
	switch {
	case e.elem == "" && strings.HasPrefix(e.node, "area:"):
		site := strings.TrimPrefix(e.node, "area:")
		nd.Kind, nd.Display = KindArea, "Site floor"
		nd.Drill = &MapDrill{Level: LevelRoom, ID: site}
	case e.elem == UnplacedID:
		nd.Kind, nd.Display = KindUnplaced, "Unplaced"
		nd.Drill = &MapDrill{Level: LevelUnplaced}
	case e.elem == InternetID:
		nd.Kind, nd.Display = KindInternet, "Internet"
	default:
		n := v.snap.Nodes[e.elem]
		nd.Display = n.Display
		switch n.Type {
		case "site":
			nd.Kind = KindSite
			nd.Drill = &MapDrill{Level: LevelSite, ID: n.ID}
		case "room":
			nd.Kind = KindRoom
			nd.Drill = &MapDrill{Level: LevelRoom, ID: n.ID}
			if x, ok := attrNum(n, "width_mm"); ok {
				nd.Size = &MapSize{WidthMM: int(x)}
			}
			if x, ok := attrNum(n, "depth_mm"); ok {
				if nd.Size == nil {
					nd.Size = &MapSize{}
				}
				nd.Size.DepthMM = int(x)
			}
			nd.Group = v.groupOf(n.ID, "building")
		case "rack":
			nd.Kind = KindRack
			nd.Drill = &MapDrill{Level: LevelRack, ID: n.ID}
			nd.Group = v.groupOf(n.ID, "row")
			sz := &MapSize{}
			if x, ok := attrNum(n, "width_mm"); ok {
				sz.WidthMM = int(x)
			}
			if x, ok := attrNum(n, "depth_mm"); ok {
				sz.DepthMM = int(x)
			}
			if x, ok := attrNum(n, "u_height"); ok {
				sz.UHeight = int(x)
			}
			nd.Size = sz
			fx, okx := attrNum(n, "floor_x")
			fy, oky := attrNum(n, "floor_y")
			if okx && oky {
				nd.Floor = &MapFloor{X: int(fx), Y: int(fy), Facing: attrStr(n, "facing")}
			}
		case "device":
			nd.Kind = KindDevice
			nd.Role = attrStr(n, "role")
			nd.Model = attrStr(n, "model")
			if v.level == LevelRack {
				nd.Mount = v.mount(n)
			}
		}
	}
	return nd
}

// groupOf finds the building or row a place sits on.
func (v *view) groupOf(id, kind string) *MapGroup {
	for _, a := range v.ix.path[id] {
		if v.typeOf(a) == kind && v.readable(a) {
			return &MapGroup{ID: a, Kind: kind, Display: v.display(a)}
		}
	}
	return nil
}

func (v *view) mount(d *graph.Node) *MapMount {
	m := &MapMount{}
	for _, e := range v.snap.Out[d.ID] {
		if e.Type == "mounted_in" && e.To == v.container[len(v.container)-1] {
			m.Position, _ = edgeNum(e, "position")
			m.Face, _ = e.Attrs["face"].(string)
		}
	}
	h, ok := attrNum(d, "u_height")
	var dt *graph.Node
	for _, e := range v.snap.Out[d.ID] {
		if e.Type == "instance_of" {
			if t := v.snap.Nodes[e.To]; t != nil && t.Type == "device_type" {
				dt = t
			}
		}
	}
	if !ok {
		h, _ = attrNum(dt, "u_height")
	}
	m.UHeight = int(h)
	if dt != nil {
		m.FullDepth, _ = dt.Attrs["full_depth"].(bool)
	}
	return m
}

func (v *view) health(nd *MapNode, id string) {
	n := v.snap.Nodes[id]
	switch attrStr(n, "health") {
	case "ok":
		nd.Health.OK++
	case "warning":
		nd.Health.Warning++
	case "critical":
		nd.Health.Critical++
	default:
		nd.Health.Unknown++
	}
	if x, ok := attrNum(n, "log_critical_count"); ok {
		nd.Health.CriticalEvents += int(x)
	}
	if x, ok := attrNum(n, "power_w"); ok {
		if nd.PowerW == nil {
			z := 0.0
			nd.PowerW = &z
		}
		*nd.PowerW += x
	}
}

func worst(h MapHealth) string {
	switch {
	case h.Critical > 0:
		return "critical"
	case h.Warning > 0:
		return "warning"
	case h.Unknown > 0:
		return "unknown"
	case h.OK > 0:
		return "ok"
	}
	return "unknown"
}

// ---------------------------------------------------------------- edges

// acc accumulates the links of one edge.
type acc struct {
	e         MapEdge
	rank      int // weakest link
	ab, ba    *float64
	capacity  float64
	oper      [3]int // up, down, unknown
	kindsSeen map[string]bool
	status    map[string]int
	peer      string
	resolved  bool
	members   []MapMember
	providers map[string]bool
	accesses  map[string]bool
	gwStatus  map[string]bool
	// towardA and towardB say which ends the uplinked_to devices are on.
	towardA, towardB bool
}

// item is one link to draw, from one place to another.
type item struct {
	a, b     []string
	medium   string
	conf     int
	capMbps  float64
	capKnown bool
	ab, ba   *float64
	measured bool
	oper     string
	kind     string
	member   *MapMember
	tunnel   *tunnelItem
	uplink   *uplinkItem
}

// devPair orders two device ids.
func devPair(a, b string) [2]string {
	if b < a {
		a, b = b, a
	}
	return [2]string{a, b}
}

func (v *view) buildEdges() {
	lv := v.opt.Live
	// joined are the device pairs a port-level link the caller can read
	// joins: an uplinked_to between them is covered (ADR 0062).
	joined := map[[2]string]bool{}
	for _, l := range v.ix.links {
		if !v.readable(l.a) || !v.readable(l.b) || !v.readable(l.da) || !v.readable(l.db) {
			continue
		}
		joined[devPair(l.da, l.db)] = true
		ab, ba, m := rates(l, lv)
		it := &item{a: v.ix.path[l.da], b: v.ix.path[l.db], medium: MediumPhysical, conf: l.rank,
			capMbps: l.capMbps, capKnown: l.capKnown, ab: ab, ba: ba, measured: m, oper: v.ix.operOf(l, lv), kind: l.kind}
		it.member = &MapMember{ADevice: *v.place(l.da), APort: *v.place(l.a), BDevice: *v.place(l.db), BPort: *v.place(l.b),
			Kind: l.kind, Confidence: confOf(l.rank)}
		v.add(it)
	}
	for _, u := range v.ix.devUplinks {
		if !v.readable(u.from) || !v.readable(u.to) || joined[devPair(u.from, u.to)] {
			continue
		}
		it := &item{a: v.ix.path[u.from], b: v.ix.path[u.to], medium: MediumUplinkedTo, conf: u.rank, kind: "uplinked_to", oper: "unknown"}
		it.member = &MapMember{ADevice: *v.place(u.from), BDevice: *v.place(u.to), Kind: "uplinked_to", Confidence: confOf(u.rank), Toward: "b"}
		v.add(it)
	}
	for _, t := range v.ix.tunnels {
		if !v.readable(t.id) || !v.readable(t.owner) {
			continue
		}
		v.add(&item{a: v.ix.path[t.owner], b: t.target, medium: MediumTunnel, conf: t.conf, tunnel: t, oper: tunnelOper(t.status)})
	}
	for _, u := range v.ix.uplinks {
		if !v.readable(u.device) {
			continue
		}
		ok := true
		for _, g := range u.gateways {
			ok = ok && v.readable(g.ID)
		}
		if u.access != nil {
			ok = ok && v.readable(u.access.ID)
		}
		if !ok {
			continue
		}
		it := &item{a: v.ix.path[u.device], b: []string{InternetID}, medium: MediumUplink, conf: u.conf, uplink: u, oper: uplinkOper(u)}
		if u.access != nil {
			if bw, ok := attrNum(u.access, "bandwidth_mbps"); ok && bw > 0 {
				it.capMbps, it.capKnown = bw, true
			}
		}
		if u.wan != "" {
			if x, ok := lv[u.wan]; ok && (x.TX != nil || x.RX != nil) {
				it.ab, it.ba, it.measured = x.TX, x.RX, true
			}
		}
		v.add(it)
	}
}

func tunnelOper(s string) string {
	switch s {
	case "up", "down":
		return s
	}
	return "unknown"
}

func uplinkStatus(u *uplinkItem) string {
	for _, g := range u.gateways {
		if s := attrStr(g, "status"); s != "" && s != "unknown" {
			return s
		}
	}
	if s := attrStr(u.access, "status"); s != "" {
		return s
	}
	return "unknown"
}

func uplinkOper(u *uplinkItem) string {
	switch uplinkStatus(u) {
	case "online":
		return "up"
	case "offline":
		return "down"
	case "degraded":
		return "partial"
	}
	return "unknown"
}

// add places one item on the edges of the level.
func (v *view) add(it *item) {
	ea, eb := v.classify(it.a), v.classify(it.b)
	if ea.self || eb.self || (!ea.inside && !eb.inside) {
		return
	}
	na, nb := v.endNode(ea), v.endNode(eb)
	if na == nil || nb == nil {
		return
	}
	if na.ID == nb.ID {
		na.Counts.InternalLinks++
		return
	}
	ab, ba := it.ab, it.ba
	if na.ID > nb.ID {
		na, nb = nb, na
		ab, ba = ba, ab
	}
	id := na.ID + "~" + nb.ID + "~" + it.medium
	a := v.edges[id]
	if a == nil {
		a = &acc{e: MapEdge{ID: id, A: na.ID, B: nb.ID, Medium: it.medium, Members: []MapMember{}},
			rank: 4, kindsSeen: map[string]bool{}, status: map[string]int{}, providers: map[string]bool{},
			accesses: map[string]bool{}, gwStatus: map[string]bool{}}
		v.edges[id] = a
	}
	a.e.Links++
	if it.conf < a.rank {
		a.rank = it.conf
	}
	switch it.kind {
	case "cable":
		a.e.Kinds.Cable++
	case "cabled_to":
		a.e.Kinds.CabledTo++
	case "neighbor_of":
		a.e.Kinds.NeighborOf++
	case "uplinked_to":
		a.e.Kinds.UplinkedTo++
	}
	if it.medium == MediumUplinkedTo {
		if na.ID == ea.node {
			a.towardB = true
		} else {
			a.towardA = true
		}
	}
	if it.medium == MediumPhysical || it.medium == MediumUplinkedTo {
		if len(a.members) < MaxMembers {
			m := *it.member
			if na.ID != ea.node {
				m.ADevice, m.APort, m.BDevice, m.BPort = m.BDevice, m.BPort, m.ADevice, m.APort
				switch m.Toward {
				case "a":
					m.Toward = "b"
				case "b":
					m.Toward = "a"
				}
			}
			a.members = append(a.members, m)
		} else {
			a.e.MembersTruncated = true
		}
	}
	shared := it.uplink != nil && it.uplink.access != nil && a.accesses[it.uplink.access.ID]
	switch {
	case shared: // an access routed by two devices is counted once
	case it.capKnown:
		a.capacity += it.capMbps
	case it.medium != MediumTunnel && it.medium != MediumUplinkedTo:
		a.e.CapacityPartial = true
	}
	if it.measured {
		a.e.Measured = true
		addRate(&a.ab, ab)
		addRate(&a.ba, ba)
	}
	switch it.oper {
	case "up":
		a.oper[0]++
	case "down":
		a.oper[1]++
	case "partial":
		a.oper[0]++
		a.oper[1]++
	default:
		a.oper[2]++
	}
	if t := it.tunnel; t != nil {
		a.e.Kinds.Tunnel++
		a.kindsSeen[t.kind] = true
		a.status[tunnelOper(t.status)]++
		if !t.resolved && a.peer == "" {
			a.peer = t.peer
		}
		a.resolved = a.resolved || t.resolved
	}
	if u := it.uplink; u != nil {
		a.e.Kinds.Uplink++
		a.gwStatus[uplinkStatus(u)] = true
		if u.access != nil && !a.accesses[u.access.ID] {
			a.accesses[u.access.ID] = true
			if x, ok := attrNum(u.access, "bandwidth_mbps"); ok {
				a.e.Uplink = ensureUplink(a.e.Uplink)
				a.e.Uplink.BandwidthMbps += x
			}
			if p := attrStr(u.access, "provider"); p != "" {
				a.providers[p] = true
			}
		}
		for _, g := range u.gateways {
			a.e.Uplink = ensureUplink(a.e.Uplink)
			if x, ok := attrNum(g, "latency_ms"); ok && x > a.e.Uplink.LatencyMS {
				a.e.Uplink.LatencyMS = x
			}
			if x, ok := attrNum(g, "loss_pct"); ok && x > a.e.Uplink.LossPct {
				a.e.Uplink.LossPct = x
			}
		}
		a.e.Uplink = ensureUplink(a.e.Uplink)
	}
}

func ensureUplink(u *MapUplink) *MapUplink {
	if u == nil {
		return &MapUplink{}
	}
	return u
}

// endNode finds, or for a portal creates, the node an end falls in; nil when
// the caller cannot see it.
func (v *view) endNode(e end) *MapNode {
	if nd := v.nodes[e.node]; nd != nil {
		return nd
	}
	if e.inside {
		if e.elem == InternetID && v.level == LevelGlobal {
			nd := v.newNode(e)
			v.nodes[e.node] = nd
			return nd
		}
		return nil // inside a place the caller does not read
	}
	if !v.readable(e.elem) {
		return nil
	}
	nd := &MapNode{ID: e.node, Kind: KindPortal, PlaceType: v.typeOf(e.elem), Display: v.display(e.elem)}
	switch nd.PlaceType {
	case "site":
		nd.Drill = &MapDrill{Level: LevelSite, ID: e.elem}
		for _, c := range v.container {
			if c == e.elem {
				nd.Drill = &MapDrill{Level: LevelRoom, ID: e.elem}
			}
		}
	case "room":
		nd.Drill = &MapDrill{Level: LevelRoom, ID: e.elem}
	case "rack":
		nd.Drill = &MapDrill{Level: LevelRack, ID: e.elem}
	case UnplacedID:
		nd.Drill = &MapDrill{Level: LevelUnplaced}
	}
	v.nodes[e.node] = nd
	return nd
}

// ---------------------------------------------------------------- finish

func (v *view) finish(out *TopologyMap) {
	ids := make([]string, 0, len(v.edges))
	for id := range v.edges {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := v.edges[id]
		e := a.e
		e.Confidence = confOf(a.rank)
		e.CapacityMbps = a.capacity
		e.AToBBps, e.BToABps = a.ab, a.ba
		if a.ab != nil || a.ba != nil {
			peak := 0.0
			if a.ab != nil {
				peak = *a.ab
			}
			if a.ba != nil && *a.ba > peak {
				peak = *a.ba
			}
			if a.capacity > 0 {
				u := peak / (a.capacity * 1e6)
				e.Util = &u
			}
		}
		e.Oper = edgeOper(a.oper)
		switch {
		case a.towardA && a.towardB:
			e.Toward = "both"
		case a.towardA:
			e.Toward = "a"
		case a.towardB:
			e.Toward = "b"
		}
		e.Members = a.members
		if e.Members == nil {
			e.Members = []MapMember{}
		}
		if e.Kinds.Tunnel > 0 {
			t := &MapTunnel{Kind: "mixed", Resolved: a.resolved, Peer: a.peer}
			if len(a.kindsSeen) == 1 {
				for k := range a.kindsSeen {
					t.Kind = k
				}
			}
			if t.Resolved {
				t.Peer = ""
			}
			t.Status = edgeOper([3]int{a.status["up"], a.status["down"], a.status["unknown"]})
			e.Tunnel = t
		}
		if e.Uplink != nil {
			e.Uplink.Status = "degraded"
			if len(a.gwStatus) == 1 {
				for s := range a.gwStatus {
					e.Uplink.Status = s
				}
			}
			ps := make([]string, 0, len(a.providers))
			for p := range a.providers {
				ps = append(ps, p)
			}
			sort.Strings(ps)
			e.Uplink.Provider = strings.Join(ps, ", ")
		}
		if v.nodes[e.A] != nil && v.nodes[e.B] != nil {
			out.Edges = append(out.Edges, e)
		}
	}
	for _, nd := range v.nodes {
		nd.Health.Worst = worst(nd.Health)
		out.Nodes = append(out.Nodes, *nd)
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		x, y := out.Nodes[i], out.Nodes[j]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		if x.Display != y.Display {
			return x.Display < y.Display
		}
		return x.ID < y.ID
	})
	if len(out.Nodes) > MaxNodes {
		out.Nodes, out.Truncated = out.Nodes[:MaxNodes], true
		keep := map[string]bool{}
		for _, n := range out.Nodes {
			keep[n.ID] = true
		}
		edges := out.Edges[:0]
		for _, e := range out.Edges {
			if keep[e.A] && keep[e.B] {
				edges = append(edges, e)
			}
		}
		out.Edges = edges
	}
	if len(out.Edges) > MaxEdges {
		out.Edges, out.Truncated = out.Edges[:MaxEdges], true
	}

	// notes
	for d := range v.ix.ambiguous {
		if v.readable(d) {
			out.Notes.AmbiguousPlacements++
		}
	}
	for _, p := range v.ix.noPlace {
		if v.readable(p) {
			out.Notes.LinksWithoutPlace++
		}
	}
	for _, t := range v.ix.tunnels {
		if !t.resolved && v.readable(t.id) {
			out.Notes.UnresolvedTunnels++
		}
	}
	for id, p := range v.ix.path {
		if len(p) > 0 && p[0] == UnplacedID && v.readable(id) {
			out.Notes.UnplacedDevices++
		}
	}
}

func edgeOper(c [3]int) string {
	up, down := c[0], c[1]
	switch {
	case down > 0 && up > 0:
		return "partial"
	case down > 0:
		return "down"
	case up > 0:
		return "up"
	}
	return "unknown"
}

// String helps tests print a map compactly.
func (m *TopologyMap) String() string {
	return fmt.Sprintf("%s: %d nodes, %d edges", m.Level, len(m.Nodes), len(m.Edges))
}
