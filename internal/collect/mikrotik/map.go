package mikrotik

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Object refs inside the batch.
const devRef = "device"

// maxVIDsPerRow bounds the VLAN ids expanded from one /interface/bridge/vlan
// row: a row such as vlan-ids=1-4094 would otherwise create 4094 objects and
// as many memberships for every trunk port.
const maxVIDsPerRow = 512

// maxWarnings bounds Health.Warnings; the rest is counted in the last line.
const maxWarnings = 20

// iface is what the mapper remembers of an interface to build relations.
type iface struct {
	name   string
	kind   string
	ref    string
	obj    *sdk.Object
	hasIP  bool
	member bool // port of a bridge
}

// bridgePort is the part of a /interface/bridge/port row the mapper needs.
type bridgePort struct {
	bridge string
	pvid   int
	frames string
}

// mapper turns the rows of one run into a batch. It is pure: no I/O.
type mapper struct {
	cfg     config
	address string
	data    map[string][]row

	b         *sdk.Batch
	osVersion string

	ifaces  map[string]*iface
	order   []string // interface names in the order the device lists them
	ports   map[string]bridgePort
	tagged  map[string]bool // interface has at least one tagged VLAN membership
	vlanRel map[string]*sdk.Relation
	rels    map[string]bool
	psus    map[string]string
	fans    map[string]string

	warns []string
	seen  map[string]bool
}

func newMapper(cfg config, address string, data map[string][]row) *mapper {
	return &mapper{
		cfg: cfg, address: address, data: data,
		ifaces: make(map[string]*iface), ports: map[string]bridgePort{}, tagged: map[string]bool{},
		vlanRel: map[string]*sdk.Relation{}, rels: map[string]bool{},
		psus: map[string]string{}, fans: map[string]string{}, seen: map[string]bool{},
	}
}

func (m *mapper) warnf(format string, args ...any) {
	w := fmt.Sprintf(format, args...)
	if !m.seen[w] {
		m.seen[w] = true
		m.warns = append(m.warns, w)
	}
}

// warnings returns the non-fatal oddities of the run, bounded.
func (m *mapper) warnings() []string {
	if len(m.warns) <= maxWarnings {
		return m.warns
	}
	out := append([]string(nil), m.warns[:maxWarnings]...)
	return append(out, fmt.Sprintf("%d more warnings not shown", len(m.warns)-maxWarnings))
}

func (m *mapper) first(cmd string) row {
	if rows := m.data[cmd]; len(rows) > 0 {
		return rows[0]
	}
	return row{}
}

var errNoIdentity = errors.New("no device identity: none of identity, serial number or MAC address was returned")

// build maps everything. It fails only when the device cannot be identified.
func (m *mapper) build() (*sdk.Batch, error) {
	m.b = sdk.NewBatch(m.cfg.target.Source(), sdk.ScopeFull)
	if err := m.device(); err != nil {
		return nil, err
	}
	m.interfaces()
	m.ethernet()
	m.bonding()
	m.bridgePorts()
	m.bridgeVLANs()
	m.vlanInterfaces()
	m.portModes()
	m.addresses()
	m.neighbors()
	m.hosts()
	m.packages()
	m.health()
	return m.b, nil
}

// baseMAC is the factory MAC of the first Ethernet port (orig-mac-address,
// burned in and stable; fall back to the current MAC of that port), and only
// then the MAC of the first bridge. The bridge MAC is the one RouterOS
// derives from its ports (auto-mac) or that an operator sets, so it moves
// when ports or configuration change and must not be an identity.
func (m *mapper) baseMAC() string {
	for _, r := range m.data[cmdEthernet] {
		if v := firstNonEmpty(r.str("orig-mac-address"), r.str("mac-address")); v != "" {
			return v
		}
	}
	for _, r := range m.data[cmdInterface] {
		if r.str("type") == "ether" && r.str("mac-address") != "" {
			return r.str("mac-address")
		}
	}
	for _, r := range m.data[cmdBridge] {
		if v := r.str("mac-address"); v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// mgmtIP returns the address the collector talked to when it is an IP
// literal. A host name says nothing about the device address.
func (m *mapper) mgmtIP() string {
	host, _, err := net.SplitHostPort(m.address)
	if err != nil {
		return ""
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	return a.WithZone("").String()
}

func (m *mapper) device() error {
	rb, res := m.first(cmdRouterboard), m.first(cmdResource)
	name, serial, mac := m.first(cmdIdentity).str("name"), rb.str("serial-number"), m.baseMAC()
	if name == "" && serial == "" && mac == "" {
		return errNoIdentity
	}
	model := firstNonEmpty(rb.str("model"), res.str("board-name"))
	m.osVersion = firstToken(res.str("version"))
	d := m.b.Object(devRef, "device").
		Identify("serial", serial).Identify("base_mac", mac).Identify("name", name).Identify("mgmt_ip", m.mgmtIP()).
		Set("role", m.role(model)).Set("vendor", "MikroTik").Set("model", model).
		Set("os_name", "RouterOS").Set("os_version", m.osVersion).
		// RouterBOOT, the boot loader: the schema has no firmware_version on
		// a device and bios_version is the closest, documented as such.
		Set("bios_version", rb.str("current-firmware"))
	if s, ok := parseUptime(res.str("uptime")); ok {
		d.Set("uptime_s", s)
	}
	if n, ok := res.int("total-memory"); ok {
		d.Set("memory_bytes", n)
	}
	if n, ok := res.int("cpu-count"); ok {
		d.Set("cpu_count", n)
	}
	return nil
}

// role: the option wins; CRS and CSS are MikroTik's switch families, every
// other board (RB, CCR, hEX, CHR...) is a router.
func (m *mapper) role(model string) string {
	if r := m.cfg.opts.Role; r != "" {
		return r
	}
	up := strings.ToUpper(model)
	if strings.HasPrefix(up, "CRS") || strings.HasPrefix(up, "CSS") {
		return "switch"
	}
	return "router"
}

func firstToken(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// interfaceKind maps a RouterOS interface type to the schema kind.
func interfaceKind(typ string) string {
	switch t := strings.ToLower(typ); {
	case t == "ether", t == "wlan", t == "wifi", t == "wifiwave2", t == "lte", t == "cap":
		return "physical"
	case t == "bond":
		return "lag"
	case t == "vlan":
		return "vlan"
	case t == "loopback":
		return "loopback"
	case t == "bridge", t == "vrrp", t == "veth":
		return "virtual"
	case strings.Contains(t, "tunnel"), t == "wg", strings.HasPrefix(t, "ovpn"), strings.HasPrefix(t, "l2tp"),
		strings.HasPrefix(t, "sstp"), strings.HasPrefix(t, "pptp"), strings.HasPrefix(t, "pppoe"),
		strings.HasPrefix(t, "ppp"), strings.HasPrefix(t, "gre"), strings.HasPrefix(t, "eoip"), strings.HasPrefix(t, "ipip"):
		return "tunnel"
	}
	return "other"
}

func (m *mapper) interfaces() {
	for _, r := range m.data[cmdInterface] {
		name := r.str("name")
		if name == "" {
			continue
		}
		if _, dup := m.ifaces[name]; dup {
			continue
		}
		kind := interfaceKind(r.str("type"))
		ref := "if:" + name
		o := m.b.Object(ref, "interface").Identify("name", name).In(devRef).
			Set("kind", kind).Set("description", r.str("comment"))
		if mac := r.str("mac-address"); mac != "" && mac != "00:00:00:00:00:00" {
			o.Set("mac", mac)
		}
		if n, ok := r.int("actual-mtu"); ok {
			o.Set("mtu", n)
		} else if n, ok := r.int("mtu"); ok { // "auto" on a bridge: not a number, skipped
			o.Set("mtu", n)
		}
		running := r.flag("running")
		if r.flag("disabled") {
			o.Set("admin_status", "down")
		} else {
			o.Set("admin_status", "up")
		}
		if running {
			o.Set("oper_status", "up")
		} else {
			o.Set("oper_status", "down")
		}
		m.ifaces[name] = &iface{name: name, kind: kind, ref: ref, obj: o}
		m.order = append(m.order, name)
	}
}

// ethernet adds the negotiated speed and duplex, which only mean something
// while the link is up.
func (m *mapper) ethernet() {
	for _, r := range m.data[cmdEthernet] {
		i := m.ifaces[r.str("name")]
		if i == nil || !r.flag("running") {
			continue
		}
		if s := r.str("speed"); s != "" {
			if mbps, ok := parseSpeed(s); ok {
				i.obj.Set("speed_mbps", mbps)
			} else {
				m.warnf("interface %s: unparsable speed %q", i.name, s)
			}
		}
		if _, ok := r["full-duplex"]; ok {
			if r.flag("full-duplex") {
				i.obj.Set("duplex", "full")
			} else {
				i.obj.Set("duplex", "half")
			}
		}
	}
}

// parseSpeed reads "10Mbps", "1Gbps", "2.5Gbps" into Mb/s.
func parseSpeed(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "bps")
	mult := 0.0
	switch {
	case strings.HasSuffix(s, "g"):
		mult = 1000
	case strings.HasSuffix(s, "m"):
		mult = 1
	default:
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(s, "g"), "m"), 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return int(f*mult + 0.5), true
}

// relate adds a relation once.
func (m *mapper) relate(typ, from, to string) *sdk.Relation {
	k := typ + "|" + from + "|" + to
	if m.rels[k] {
		return nil
	}
	m.rels[k] = true
	return m.b.Relate(typ, from, to)
}

func (m *mapper) bonding() {
	for _, r := range m.data[cmdBonding] {
		bond := m.ifaces[r.str("name")]
		if bond == nil {
			continue
		}
		for _, s := range r.list("slaves") {
			if si := m.ifaces[s]; si != nil {
				m.relate("member_of", si.ref, bond.ref)
			} else {
				m.warnf("bond %s: member %q is not an interface the device lists", bond.name, s)
			}
		}
	}
}

// bridgeFiltering reports whether a bridge filters VLANs. Only an explicit
// "false" says no: a bridge missing from the output (its command failed) is
// assumed to filter, which is what the configurations this collector targets
// do. Without filtering the bridge/vlan table has no effect.
func (m *mapper) bridgeFiltering(bridge string) bool {
	for _, r := range m.data[cmdBridge] {
		if r.str("name") == bridge {
			_, has := r["vlan-filtering"]
			return !has || r.flag("vlan-filtering")
		}
	}
	return true
}

// bridgePorts records the ports of each bridge and the member_of relation.
func (m *mapper) bridgePorts() {
	for _, r := range m.data[cmdBridgePort] {
		if r.flag("disabled") {
			continue
		}
		port, br := m.ifaces[r.str("interface")], m.ifaces[r.str("bridge")]
		if port == nil || br == nil {
			if len(m.ifaces) > 0 {
				m.warnf("bridge port %q of bridge %q: interface not listed by the device", r.str("interface"), r.str("bridge"))
			}
			continue
		}
		pvid, _ := r.int("pvid")
		m.ports[port.name] = bridgePort{bridge: br.name, pvid: pvid, frames: r.str("frame-types")}
		port.member = true
		m.relate("member_of", port.ref, br.ref)
	}
}

// parseVIDs expands "10", "20-22", "10,20-22". It refuses ids out of 1..4094
// and rows that would expand to more than maxVIDsPerRow.
func parseVIDs(s string) ([]int, error) {
	var out []int
	for _, part := range splitList(s) {
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			hi = lo
		}
		l, err1 := strconv.Atoi(strings.TrimSpace(lo))
		h, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || l < 1 || h > 4094 || l > h {
			return nil, fmt.Errorf("invalid VLAN id %q", part)
		}
		if len(out)+h-l+1 > maxVIDsPerRow {
			return nil, fmt.Errorf("more than %d VLAN ids in %q", maxVIDsPerRow, s)
		}
		for v := l; v <= h; v++ {
			out = append(out, v)
		}
	}
	return out, nil
}

// vlan returns the ref of the VLAN object for vid, creating it.
func (m *mapper) vlan(vid int, name string) string {
	ref := "vlan:" + strconv.Itoa(vid)
	o := m.b.Object(ref, "vlan").Identify("key", collect.VLANKey(m.cfg.domain, vid)).
		Set("vid", vid).Set("domain", m.cfg.domain)
	if _, has := o.Attributes["name"]; !has {
		o.Set("name", name)
	}
	return ref
}

// vlanMember records that an interface carries a VLAN. A tagged membership
// wins over an untagged one coming from another row.
func (m *mapper) vlanMember(i *iface, vlanRef string, tagged bool) {
	k := i.ref + "|" + vlanRef
	if r, ok := m.vlanRel[k]; ok {
		if tagged {
			r.Attributes["tagged"] = true
		}
		return
	}
	r := m.b.Relate("member_of", i.ref, vlanRef).With("tagged", tagged)
	m.vlanRel[k] = r
}

func (m *mapper) bridgeVLANs() {
	for _, r := range m.data[cmdBridgeVLAN] {
		if r.flag("disabled") || !m.bridgeFiltering(r.str("bridge")) {
			continue
		}
		vids, err := parseVIDs(r.str("vlan-ids"))
		if err != nil {
			m.warnf("bridge VLAN row on %q skipped: %v", r.str("bridge"), err)
			continue
		}
		// The configured sets and the resolved ones (current-*) are united:
		// the first hold the names the operator wrote (possibly interface
		// lists, which are not resolved here), the second what RouterOS
		// resolved them to, plus dynamic entries such as PVID VLANs.
		tagged := unique(append(r.list("tagged"), r.list("current-tagged")...))
		isTagged := map[string]bool{}
		for _, n := range tagged {
			isTagged[n] = true
		}
		var untagged []string
		for _, n := range unique(append(r.list("untagged"), r.list("current-untagged")...)) {
			if !isTagged[n] {
				untagged = append(untagged, n)
			}
		}
		name := ""
		if len(vids) == 1 {
			name = r.str("comment")
		}
		for _, vid := range vids {
			ref := m.vlan(vid, name)
			for _, n := range tagged {
				m.member(n, ref, true)
			}
			for _, n := range untagged {
				m.member(n, ref, false)
			}
		}
	}
}

func (m *mapper) member(name, vlanRef string, tagged bool) {
	i := m.ifaces[name]
	if i == nil {
		if len(m.ifaces) > 0 {
			m.warnf("bridge VLAN member %q is not an interface the device lists (interface list?), skipped", name)
		}
		return
	}
	if tagged {
		m.tagged[name] = true
	}
	m.vlanMember(i, vlanRef, tagged)
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// vlanInterfaces: a VLAN interface carries its VLAN, tagged.
func (m *mapper) vlanInterfaces() {
	for _, r := range m.data[cmdVLAN] {
		i := m.ifaces[r.str("name")]
		vid, ok := r.int("vlan-id")
		if i == nil || !ok || vid < 1 || vid > 4094 {
			continue
		}
		m.vlanMember(i, m.vlan(vid, ""), true)
	}
}

// portModes sets mode and native VLAN on bridge ports of filtering bridges:
// trunk when the port carries a tagged VLAN, access otherwise; the native
// VLAN is the PVID unless the port drops untagged frames.
func (m *mapper) portModes() {
	for _, name := range m.order {
		p, ok := m.ports[name]
		if !ok || !m.bridgeFiltering(p.bridge) {
			continue
		}
		o := m.ifaces[name].obj
		if m.tagged[name] {
			o.Set("mode", "trunk")
		} else {
			o.Set("mode", "access")
		}
		if p.frames != "admit-only-vlan-tagged" && p.pvid > 0 {
			o.Set("native_vlan", p.pvid)
		}
	}
}

func (m *mapper) addresses() {
	for _, r := range m.data[cmdAddress] {
		if r.flag("disabled") {
			continue
		}
		raw := r.str("address")
		var addr netip.Addr
		bits := -1
		if p, err := netip.ParsePrefix(raw); err == nil {
			addr, bits = p.Addr(), p.Bits()
		} else if a, err := netip.ParseAddr(raw); err == nil {
			addr = a
		} else {
			m.warnf("ip address %q: unparsable, skipped", raw)
			continue
		}
		ref := "ip:" + addr.String()
		o := m.b.Object(ref, "ip_address").Identify("address", addr.String())
		if bits >= 0 {
			o.Set("prefix_length", bits)
		}
		to := devRef
		if i := m.ifaces[firstNonEmpty(r.str("actual-interface"), r.str("interface"))]; i != nil {
			to = i.ref
			i.hasIP = true
		} else if len(m.ifaces) > 0 {
			m.warnf("ip address %s: interface %q not listed by the device, assigned to the device", raw, r.str("interface"))
		}
		m.relate("assigned_to", ref, to)
	}
	for _, i := range m.ifaces {
		if i.hasIP && (i.kind == "physical" || i.kind == "lag") {
			if _, has := i.obj.Attributes["mode"]; !has {
				i.obj.Set("mode", "routed")
			}
		}
	}
}

// protocolRank orders discovery protocols: LLDP is the standard one and the
// one that carries the remote port name; MNDP is MikroTik's.
var protocolRank = map[string]int{"lldp": 0, "cdp": 1, "mndp": 2}

func bestProtocol(discoveredBy string) string {
	best := ""
	for _, p := range splitList(strings.ToLower(discoveredBy)) {
		r, ok := protocolRank[p]
		if !ok {
			continue
		}
		if best == "" || r < protocolRank[best] {
			best = p
		}
	}
	return best
}

// shortName is the hostname without its domain, as the neighbour is named on
// its own side, unless it is an address.
func shortName(identity string) string {
	if _, err := netip.ParseAddr(identity); err == nil {
		return identity
	}
	short, _, _ := strings.Cut(identity, ".")
	return short
}

// localPort picks the port a neighbour is seen on among the "interface"
// list, which also names the bridge and the bond the port belongs to
// ("sfp-sfpplus1,bond0,bridge"): the physical port is the one cabled.
func (m *mapper) localPort(names []string) *iface {
	for _, kind := range []string{"physical", "lag"} {
		for _, n := range names {
			if i := m.ifaces[n]; i != nil && i.kind == kind {
				return i
			}
		}
	}
	return nil
}

// neighbors maps the discovery table. The remote device is identified by its
// name only: RouterOS does not expose the chassis ID (the mac-address of the
// row is the source address of the frame, not a chassis MAC), and a wrong
// strong identifier would merge or split devices.
func (m *mapper) neighbors() {
	for _, r := range m.data[cmdNeighbor] {
		name, port := shortName(r.str("identity")), r.str("interface-name")
		if name == "" || port == "" {
			continue // MNDP entries carry no remote port: nothing to link
		}
		local := m.localPort(r.list("interface"))
		if local == nil {
			continue
		}
		k := "neighbor|" + local.ref + "|" + name + "|" + port
		if m.rels[k] {
			continue
		}
		m.rels[k] = true
		rel := m.b.RelateEndpoints("neighbor_of", sdk.Ref(local.ref), sdk.Endpoint{
			Type:     "interface",
			Identity: map[string]string{"name": port},
			Parent:   &sdk.Endpoint{Type: "device", Identity: map[string]string{"name": name}},
		})
		if p := bestProtocol(r.str("discovered-by")); p != "" {
			rel.With("protocol", p)
		}
	}
}

// hosts maps the bridge host (MAC) table to learned_macs, on edge ports only:
// a trunk toward the rest of the network learns thousands of addresses that
// say nothing about what is cabled there.
func (m *mapper) hosts() {
	perPort := map[string]map[string]bool{}
	for _, r := range m.data[cmdBridgeHost] {
		if r.flag("local") || r.flag("invalid") {
			continue
		}
		port, mac := r.str("on-interface"), strings.ToLower(r.str("mac-address"))
		if port == "" || mac == "" {
			continue
		}
		// vid is "0" when the router does not report one (no VLAN filtering).
		vid, _ := r.int("vid")
		if perPort[port] == nil {
			perPort[port] = map[string]bool{}
		}
		perPort[port][fmt.Sprintf("%d/%s", vid, mac)] = true
	}
	for port, set := range perPort {
		i := m.ifaces[port]
		if i == nil || (i.kind != "physical" && i.kind != "lag") || len(set) > m.cfg.maxEdgeMACs {
			continue
		}
		macs := make([]string, 0, len(set))
		for e := range set {
			macs = append(macs, e)
		}
		sort.Strings(macs)
		i.obj.Set("learned_macs", macs)
	}
}

func (m *mapper) packages() {
	for _, r := range m.data[cmdPackage] {
		name, ver := r.str("name"), r.str("version")
		if name == "" || r.flag("disabled") {
			continue
		}
		ref := "sw:" + name + "/" + ver
		m.b.Object(ref, "software").Identify("key", "package/"+name+"/"+ver).
			Set("kind", "package").Set("name", name).Set("version", ver).Set("vendor", "MikroTik")
		m.relate("runs", devRef, ref)
	}
}

// reading is one health value, whatever the shape of the reply.
type reading struct {
	name, typ, value string
	scale            float64 // 0 means 1
}

// healthReadings flattens both shapes of /system/health/print: since 7.x one
// row per sensor (name, value, type); before, one row whose properties are the
// sensors. The shape is detected from the rows, not from the version.
func healthReadings(rows []row) []reading {
	var out []reading
	for _, r := range rows {
		if n := r.str("name"); n != "" {
			out = append(out, reading{name: n, typ: r.str("type"), value: r.str("value")})
			continue
		}
		keys := make([]string, 0, len(r))
		for k := range r {
			if k != ".id" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			rd := reading{name: k, value: strings.TrimSpace(r[k])}
			switch {
			case strings.HasSuffix(k, "voltage"):
				rd.typ, rd.scale = "V", 0.1 // decivolts in the flat shape
			case strings.Contains(k, "temperature"):
				rd.typ = "C"
			case strings.HasSuffix(k, "-speed"):
				rd.typ = "RPM"
			case k == "current":
				rd.typ = "mA"
			}
			out = append(out, rd)
		}
	}
	return out
}

// component splits "psu1-voltage" into ("psu1", "voltage") for prefix "psu".
func component(name, prefix string) (comp, sub string, ok bool) {
	rest, found := strings.CutPrefix(name, prefix)
	if !found {
		return "", "", false
	}
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits == len(rest) || rest[digits] != '-' {
		return "", "", false
	}
	return prefix + rest[:digits], rest[digits+1:], true
}

// componentHealth maps the state word of a PSU or fan.
func componentHealth(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "ok":
		return "ok"
	case "fail", "failed", "error":
		return "critical"
	case "absent", "not-present":
		return "absent"
	}
	return "unknown"
}

func (m *mapper) psu(name string) string {
	if ref, ok := m.psus[name]; ok {
		return ref
	}
	ref := "psu:" + name
	m.b.Object(ref, "psu").Identify("name", name).In(devRef)
	m.psus[name] = ref
	return ref
}

func (m *mapper) fan(name string) string {
	if ref, ok := m.fans[name]; ok {
		return ref
	}
	ref := "fan:" + name
	m.b.Object(ref, "fan").Identify("name", name).In(devRef)
	m.fans[name] = ref
	return ref
}

func (m *mapper) sensor(parent, name, kind, placement, unit string, v float64) {
	m.b.Object("sensor:"+parent+":"+name, "sensor").Identify("name", name).In(parent).
		Set("kind", kind).Set("placement", placement).Set("unit", unit).Set("reading", v)
}

func (m *mapper) health() {
	for _, rd := range healthReadings(m.data[cmdHealth]) {
		m.reading(rd)
	}
}

// reading maps one health value. Names follow the RouterOS manual and CRS3xx
// reports (cpu-temperature, board-temperature1, fan1-speed, psu1-voltage,
// psu1-state, power-consumption); anything else is reported as a warning
// rather than guessed.
func (m *mapper) reading(rd reading) {
	if comp, sub, ok := component(rd.name, "psu"); ok {
		m.psuReading(comp, sub, rd)
		return
	}
	if comp, sub, ok := component(rd.name, "fan"); ok {
		switch sub {
		case "state":
			m.b.Object(m.fan(comp), "fan").Set("health", componentHealth(rd.value))
		case "speed":
			m.fanSpeed(comp, rd)
		default:
			m.warnf("health sensor %q not recognised, skipped", rd.name)
		}
		return
	}
	switch {
	case rd.name == "power-consumption":
		if v, ok := m.number(rd); ok {
			m.b.Object(devRef, "device").Set("power_w", v)
		}
	case rd.typ == "RPM":
		m.fanSpeed(strings.TrimSuffix(rd.name, "-speed"), rd)
	case rd.typ == "C" || strings.Contains(rd.name, "temperature"):
		placement := "system"
		if strings.Contains(rd.name, "cpu") {
			placement = "cpu"
		}
		if v, ok := m.number(rd); ok {
			m.sensor(devRef, rd.name, "temperature", placement, "°C", v)
		}
	case rd.typ == "V":
		if v, ok := m.number(rd); ok {
			m.sensor(devRef, rd.name, "voltage", "system", "V", v)
		}
	case rd.typ == "A" || rd.typ == "mA":
		if v, ok := m.number(rd); ok {
			m.sensor(devRef, rd.name, "current", "system", rd.typ, v)
		}
	default:
		m.warnf("health sensor %q not recognised, skipped", rd.name)
	}
}

func (m *mapper) psuReading(comp, sub string, rd reading) {
	ref := m.psu(comp)
	switch sub {
	case "state":
		m.b.Object(ref, "psu").Set("health", componentHealth(rd.value))
	case "voltage":
		if v, ok := m.number(rd); ok {
			m.sensor(ref, "voltage", "voltage", "psu", "V", v)
		}
	case "current":
		if v, ok := m.number(rd); ok {
			m.sensor(ref, "current", "current", "psu", firstNonEmpty(rd.typ, "A"), v)
		}
	default:
		m.warnf("health sensor %q not recognised, skipped", rd.name)
	}
}

func (m *mapper) fanSpeed(name string, rd reading) {
	if v, ok := m.number(rd); ok {
		m.b.Object(m.fan(name), "fan").Set("speed_rpm", int(v+0.5))
	}
}

// number parses a reading, applying its scale.
func (m *mapper) number(rd reading) (float64, bool) {
	v, err := strconv.ParseFloat(rd.value, 64)
	if err != nil {
		m.warnf("health sensor %q: unparsable value %q", rd.name, rd.value)
		return 0, false
	}
	if rd.scale != 0 {
		v *= rd.scale
	}
	return v, true
}

// parseUptime reads RouterOS durations: "3w2d4h5m6s", "1d02:03:04", "02:03:04".
func parseUptime(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	total := 0
	if d, rest, ok := strings.Cut(s, ":"); ok {
		// [<n>d]hh:mm:ss
		days := 0
		head := d
		if i := strings.IndexByte(d, 'd'); i >= 0 {
			n, err := strconv.Atoi(d[:i])
			if err != nil {
				return 0, false
			}
			days, head = n, d[i+1:]
		}
		parts := strings.Split(head+":"+rest, ":")
		if len(parts) != 3 {
			return 0, false
		}
		mult := []int{3600, 60, 1}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 {
				return 0, false
			}
			total += n * mult[i]
		}
		return total + days*86400, true
	}
	num := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			num += string(c)
			continue
		}
		unit := string(c)
		if c == 'm' && i+1 < len(s) && s[i+1] == 's' { // milliseconds: below a second
			i++
			num = ""
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			return 0, false
		}
		num = ""
		switch unit {
		case "w":
			total += n * 7 * 86400
		case "d":
			total += n * 86400
		case "h":
			total += n * 3600
		case "m":
			total += n * 60
		case "s":
			total += n
		default:
			return 0, false
		}
	}
	if num != "" {
		return 0, false
	}
	return total, true
}
