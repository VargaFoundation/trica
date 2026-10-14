package pfsense

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// build turns a snapshot into objects and relations of b. It returns the
// firewall's OS version, which the health record carries, and the warnings
// found while mapping.
//
// Objects that the schema cannot scope under the device (gateways, tunnels,
// VLANs, which have no parent types) are named "<target>/<name>": two
// firewalls both have a "WAN_DHCP" gateway and a VLAN 20, and the schema
// identifies them across the whole inventory. The target name is the
// operator's own, stable label of the firewall, so a hostname change does not
// rename them.
func (c *collector) build(b *sdk.Batch, s *snapshot) (string, []string) {
	ns := c.t.Name
	osVersion := versionString(s.version)

	// ---- device
	serial := usableSerial(string(s.system.Serial))
	_, hostErr := netip.ParseAddr(c.host)
	name := string(s.hostname.Hostname)
	if name == "" && serial == "" && hostErr != nil {
		// Nothing else identifies the device: the operator's label for the
		// target is better than an object nobody can recognise.
		name = ns
	}
	fw := b.Object("fw", "device").
		Identify("name", name).
		Identify("serial", serial).
		Set("role", "firewall").
		Set("os_name", "pfSense").
		Set("os_version", osVersion).
		Set("model", string(s.system.Platform))
	if ip, err := netip.ParseAddr(c.host); err == nil {
		fw.Identify("mgmt_ip", ip.String())
		fw.Set("mgmt_ip", ip.String())
	}
	if n, ok := atoi(s.system.CPUCount); ok && n > 0 {
		fw.Set("cpu_count", n)
	}
	if sec, ok := parseUptime(s.system.Uptime); ok {
		fw.Set("uptime_s", sec)
	}

	c.buildInterfaces(b, s, ns)
	gw := c.buildGateways(b, s, ns)
	c.buildRoutes(b, s, gw)
	warnings := c.buildRules(b, s)
	c.buildTunnels(b, s, ns)
	return osVersion, warnings
}

// ---------------------------------------------------------------- interfaces

type ifaceInfo struct {
	name string
	cfg  *ifaceConfig
	st   *ifaceStatus
	vlan *vlanConfig
}

func (c *collector) buildInterfaces(b *sdk.Batch, s *snapshot, ns string) {
	var order []string
	byName := map[string]*ifaceInfo{}
	get := func(name string) *ifaceInfo {
		if i, ok := byName[name]; ok {
			return i
		}
		i := &ifaceInfo{name: name}
		byName[name] = i
		order = append(order, name)
		return i
	}
	idToName := map[string]string{}
	for i := range s.ifaces {
		cfg := &s.ifaces[i]
		if cfg.If == "" {
			continue
		}
		get(string(cfg.If)).cfg = cfg
		idToName[strings.ToLower(string(cfg.ID))] = string(cfg.If)
		if cfg.Descr != "" {
			idToName[strings.ToLower(string(cfg.Descr))] = string(cfg.If)
		}
	}
	for i := range s.ifStatus {
		st := &s.ifStatus[i]
		name := string(st.HWIf)
		if name == "" {
			name = string(st.If)
		}
		if name == "" {
			name = idToName[strings.ToLower(string(st.Name))]
		}
		if name == "" {
			name = string(st.Name)
		}
		if name == "" {
			continue
		}
		get(name).st = st
	}
	for i := range s.vlans {
		v := &s.vlans[i]
		name := string(v.VLANIf)
		if name == "" && v.If != "" && v.Tag != "" {
			name = string(v.If) + "." + string(v.Tag)
		}
		if name == "" {
			continue
		}
		get(name).vlan = v
		if v.If != "" {
			get(string(v.If)) // the parent port exists even when not assigned
		}
	}

	for _, name := range order {
		in := byName[name]
		ref := "if:" + name
		o := b.Object(ref, "interface").Identify("name", name).In("fw")
		o.Set("kind", ifaceKind(name, in.vlan != nil))
		descr := ""
		if in.cfg != nil {
			descr = string(in.cfg.Descr)
			if in.cfg.Enable != nil {
				o.Set("admin_status", map[bool]string{true: "up", false: "down"}[bool(*in.cfg.Enable)])
			}
		}
		if descr == "" && in.st != nil {
			descr = string(in.st.Descr)
		}
		if descr == "" && in.vlan != nil {
			descr = string(in.vlan.Descr)
		}
		o.Set("description", descr)
		if in.st != nil {
			if mac, err := schema.NormalizeMAC(string(in.st.MACAddr)); err == nil {
				o.Set("mac", mac)
			}
			o.Set("oper_status", operStatus(string(in.st.Status)))
			if sp, du := parseMedia(string(in.st.Media)); sp > 0 || du != "" {
				if sp > 0 {
					o.Set("speed_mbps", sp)
				}
				o.Set("duplex", du)
			}
		}
		mtu := str("")
		if in.st != nil {
			mtu = in.st.MTU
		}
		if mtu == "" && in.cfg != nil {
			mtu = in.cfg.MTU
		}
		if n, ok := atoi(mtu); ok && n > 0 {
			o.Set("mtu", n)
		}
		c.addAddresses(b, in, ref)

		if v := in.vlan; v != nil {
			if tag, ok := atoi(v.Tag); ok && tag >= 1 && tag <= 4094 {
				vref := "vlan:" + strconv.Itoa(tag)
				b.Object(vref, "vlan").
					Identify("key", ns+":"+strconv.Itoa(tag)).
					Set("vid", tag).
					Set("domain", ns).
					Set("name", string(v.Descr))
				b.Relate("member_of", ref, vref).With("tagged", true)
			}
		}
	}
}

// addAddresses reports the IPv4 and IPv6 addresses of an interface, the live
// ones when the status is known (a DHCP address is only there), the
// configured ones otherwise, and the prefix each one sits in.
func (c *collector) addAddresses(b *sdk.Batch, in *ifaceInfo, ifRef string) {
	type pair struct{ addr, plen str }
	var cand []pair
	var cfg4, cfg6, st4, st6 pair
	if in.cfg != nil {
		cfg4, cfg6 = pair{in.cfg.IPAddr, in.cfg.Subnet}, pair{in.cfg.IPAddrV6, in.cfg.SubnetV6}
	}
	if in.st != nil {
		st4, st6 = pair{in.st.IPAddr, in.st.Subnet}, pair{in.st.IPAddrV6, in.st.SubnetV6}
	}
	for _, p := range [][2]pair{{st4, cfg4}, {st6, cfg6}} {
		pick := p[0]
		if _, err := netip.ParseAddr(string(pick.addr)); err != nil {
			pick = p[1]
		}
		cand = append(cand, pick)
	}
	for _, p := range cand {
		a, err := netip.ParseAddr(strings.SplitN(string(p.addr), "%", 2)[0])
		if err != nil || a.IsLinkLocalUnicast() || a.IsUnspecified() {
			continue // "dhcp", "track6", empty, link-local
		}
		ipRef := "ip:" + a.String()
		ip := b.Object(ipRef, "ip_address").Identify("address", a.String())
		b.Relate("assigned_to", ipRef, ifRef)
		plen, ok := atoi(p.plen)
		if !ok || plen < 0 || plen > a.BitLen() {
			continue
		}
		ip.Set("prefix_length", plen)
		if plen < a.BitLen() {
			pfx := netip.PrefixFrom(a, plen).Masked().String()
			pRef := "prefix:" + pfx
			b.Object(pRef, "prefix").Identify("prefix", pfx)
			b.Relate("assigned_to", pRef, ifRef)
		}
	}
}

func ifaceKind(name string, vlan bool) string {
	switch {
	case vlan:
		return "vlan"
	case strings.HasPrefix(name, "lo"):
		return "loopback"
	case strings.HasPrefix(name, "lagg"):
		return "lag"
	case strings.HasPrefix(name, "ovpn"), strings.HasPrefix(name, "tun"), strings.HasPrefix(name, "wg"),
		strings.HasPrefix(name, "gif"), strings.HasPrefix(name, "gre"), strings.HasPrefix(name, "ipsec"):
		return "tunnel"
	case strings.HasPrefix(name, "bridge"), strings.HasPrefix(name, "enc"), strings.HasPrefix(name, "pflog"),
		strings.HasPrefix(name, "pfsync"):
		return "virtual"
	}
	return "physical"
}

func operStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "up", "active", "associated":
		return "up"
	case "down", "no carrier":
		return "down"
	}
	return "unknown"
}

var mediaSpeed = regexp.MustCompile(`(?i)(\d+)\s*(g)?base`)

// parseMedia reads "1000baseT <full-duplex>" or "10Gbase-Twinax <full-duplex>".
func parseMedia(media string) (speedMbps int, duplex string) {
	if m := mediaSpeed.FindStringSubmatch(media); m != nil {
		n, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			n *= 1000
		}
		speedMbps = n
	}
	l := strings.ToLower(media)
	switch {
	case strings.Contains(l, "full-duplex"):
		duplex = "full"
	case strings.Contains(l, "half-duplex"):
		duplex = "half"
	}
	return speedMbps, duplex
}

// ------------------------------------------------------------------ gateways

func (c *collector) buildGateways(b *sdk.Batch, s *snapshot, ns string) map[string]string {
	refs := map[string]string{} // gateway name -> ref
	emit := func(name string) (*sdk.Object, string) {
		ref := "gw:" + name
		o := b.Object(ref, "gateway").Identify("name", ns+"/"+name).Set("name", ns+"/"+name)
		refs[name] = ref
		b.Relate("contains", "fw", ref)
		return o, ref
	}
	for i := range s.gateways {
		g := &s.gateways[i]
		if g.Name == "" {
			continue
		}
		o, ref := emit(string(g.Name))
		if a, err := netip.ParseAddr(string(g.Gateway)); err == nil {
			o.Set("address", a.String())
		}
		if g.DefaultGW {
			b.Relate("routes_via", "fw", ref)
		}
	}
	for i := range s.gwStatus {
		gs := &s.gwStatus[i]
		name := string(gs.Name)
		if name == "" {
			continue
		}
		var o *sdk.Object
		if ref, known := refs[name]; known {
			o, _ = b.Lookup(ref)
		} else {
			// Dynamic gateways (WAN_DHCP) exist only in the live state.
			o, _ = emit(name)
		}
		if _, set := o.Attributes["address"]; !set {
			if a, err := netip.ParseAddr(string(gs.MonitorIP)); err == nil {
				o.Set("address", a.String())
			}
		}
		o.Set("status", gatewayStatusName(string(gs.Status)))
		if v, ok := parseMeasure(string(gs.Delay), "ms"); ok {
			o.Set("latency_ms", v)
		}
		if v, ok := parseMeasure(string(gs.Loss), "%"); ok {
			o.Set("loss_pct", v)
		}
	}
	return refs
}

func gatewayStatusName(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	switch {
	case l == "":
		return ""
	case l == "online" || l == "none" || l == "up":
		return "online"
	case l == "down" || l == "offline" || l == "force_down":
		return "offline"
	case strings.Contains(l, "delay") || strings.Contains(l, "loss"):
		return "degraded"
	}
	return "unknown"
}

// parseMeasure reads "12.345ms" or "0.0%".
func parseMeasure(s, unit string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), unit))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func (c *collector) buildRoutes(b *sdk.Batch, s *snapshot, gws map[string]string) {
	for _, r := range s.routes {
		if r.Disabled {
			continue
		}
		p, err := netip.ParsePrefix(string(r.Network))
		if err != nil {
			continue // an alias name: nothing to report as a prefix
		}
		gref, ok := gws[string(r.Gateway)]
		if !ok {
			continue
		}
		pfx := p.Masked().String()
		pRef := "prefix:" + pfx
		b.Object(pRef, "prefix").Identify("prefix", pfx)
		b.Relate("routes_via", pRef, gref)
	}
}

// --------------------------------------------------------------------- rules

func (c *collector) buildRules(b *sdk.Batch, s *snapshot) []string {
	positional := 0
	add := func(kind string, list []rule, f func(*rule, *sdk.Object)) {
		for i := range list {
			r := &list[i]
			order := i + 1
			key, stable := ruleKey(kind, r)
			if !stable {
				key = fmt.Sprintf("%s:#%d", kind, order)
				positional++
			}
			ref := "rule:" + key
			if _, dup := b.Lookup(ref); dup {
				ref = fmt.Sprintf("%s:dup%d", ref, order)
				key = fmt.Sprintf("%s#dup%d", key, order)
			}
			o := b.Object(ref, "firewall_rule").Identify("key", key).In("fw")
			o.Set("kind", kind).
				Set("order", order).
				Set("enabled", !bool(r.Disabled)).
				Set("description", string(r.Descr)).
				Set("interface", strings.Join(r.Interface, ",")).
				Set("protocol", string(r.Protocol)).
				Set("source", string(r.Source)).
				Set("destination", string(r.Destination)).
				Set("port", string(r.DestinationPort))
			f(r, o)
		}
	}
	add("filter", s.filter, func(r *rule, o *sdk.Object) { o.Set("action", string(r.Type)) })
	add("nat_port_forward", s.portForward, func(r *rule, o *sdk.Object) {
		t := string(r.Target)
		if t != "" && r.LocalPort != "" {
			t += ":" + string(r.LocalPort)
		}
		o.Set("target", t)
	})
	add("nat_outbound", s.outbound, func(r *rule, o *sdk.Object) {
		t := string(r.Target)
		if t != "" && r.TargetSubnet != "" {
			t += "/" + string(r.TargetSubnet)
		}
		o.Set("target", t)
		if r.NoNAT {
			o.Set("action", "nonat")
		}
	})
	add("nat_one_to_one", s.oneToOne, func(r *rule, o *sdk.Object) { o.Set("target", string(r.External)) })
	if positional > 0 {
		return []string{fmt.Sprintf("%d rules carry no stable id (tracker, associated rule id, creation time) and are keyed by position: reordering them creates new objects", positional)}
	}
	return nil
}

// ruleKey picks the stable identity of a rule on its firewall: the tracker id
// of a filter rule, the associated rule id or the creation time of a NAT rule.
// stable is false when only the position is left.
func ruleKey(kind string, r *rule) (string, bool) {
	if r.Tracker != "" {
		return kind + ":" + string(r.Tracker), true
	}
	switch r.AssociatedRule {
	case "", "pass", "add-associated", "add-unassociated":
	default:
		return kind + ":" + string(r.AssociatedRule), true
	}
	if r.CreatedTime != "" && r.CreatedTime != "0" {
		return kind + ":t" + string(r.CreatedTime), true
	}
	return "", false
}

// ------------------------------------------------------------------- tunnels

func (c *collector) buildTunnels(b *sdk.Batch, s *snapshot, ns string) {
	addOVPN := func(role string, cfgs []openvpnConfig, stats []openvpnStatus, statusEP string) {
		for _, v := range cfgs {
			if v.VPNID == "" {
				continue
			}
			name := fmt.Sprintf("%s/openvpn-%s-%s", ns, role, v.VPNID)
			ref := "tun:" + name
			o := b.Object(ref, "tunnel").Identify("name", name).Set("name", name).Set("kind", "openvpn")
			if role == "client" && v.ServerAddr != "" {
				peer := string(v.ServerAddr)
				if v.ServerPort != "" {
					peer += ":" + string(v.ServerPort)
				}
				o.Set("peer", peer)
			}
			var nets []string
			for _, l := range []strs{v.TunnelNetwork, v.TunnelNetwork6, v.LocalNetwork, v.RemoteNetwork} {
				for _, n := range l {
					for _, part := range strings.Split(n, ",") {
						if part = strings.TrimSpace(part); part != "" {
							nets = append(nets, part)
						}
					}
				}
			}
			if len(nets) > 0 {
				slices.Sort(nets)
				o.Set("networks", nets)
			}
			state, found := "", false
			for _, st := range stats {
				if st.VPNID == v.VPNID {
					state, found = openvpnState(string(st.Status)), true
					break
				}
			}
			o.Set("status", tunnelStatus(bool(v.Disabled), s.have[statusEP], state, found))
			b.Relate("contains", "fw", ref)
		}
	}
	addOVPN("server", s.ovpnServers, s.ovpnSrvStat, epOvpnSrvStat)
	addOVPN("client", s.ovpnClients, s.ovpnCliStat, epOvpnCliStat)

	for _, p := range s.ipsec {
		if p.IKEID == "" {
			continue
		}
		name := fmt.Sprintf("%s/ipsec-%s", ns, p.IKEID)
		ref := "tun:" + name
		o := b.Object(ref, "tunnel").Identify("name", name).Set("name", name).Set("kind", "ipsec")
		if !bool(p.Mobile) {
			o.Set("peer", string(p.RemoteGateway))
		}
		established := false
		for _, sa := range s.ipsecSAs {
			id := string(sa.IKEID)
			if id == "" {
				id = strings.TrimPrefix(string(sa.ConID), "con")
			}
			if id == string(p.IKEID) && strings.EqualFold(string(sa.State), "established") {
				established = true
			}
		}
		state := ""
		if established {
			state = "up"
		}
		o.Set("status", tunnelStatus(bool(p.Disabled), s.have[epIPsecSAs], state, established))
		b.Relate("contains", "fw", ref)
	}
}

// tunnelStatus: a disabled tunnel is down; when the status endpoint could not
// be read the state is unknown (no value), not down; a tunnel the status does
// not list is down.
func tunnelStatus(disabled, statusRead bool, state string, found bool) string {
	switch {
	case disabled:
		return "down"
	case !statusRead:
		return ""
	case !found:
		return "down"
	}
	return state
}

// openvpnState maps the status text of an OpenVPN instance.
func openvpnState(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	switch {
	case l == "up", l == "running", l == "connected", strings.Contains(l, "listening"):
		return "up"
	case strings.Contains(l, "down"), strings.Contains(l, "reconnect"), strings.Contains(l, "stopped"), strings.Contains(l, "fail"):
		return "down"
	}
	return "unknown"
}

// ------------------------------------------------------------------- helpers

// versionString renders "2.7.2-RELEASE" + patch "1" as "2.7.2-p1".
func versionString(v versionInfo) string {
	s := strings.TrimSuffix(string(v.Version), "-RELEASE")
	if s != "" && v.Patch != "" && v.Patch != "0" {
		s += "-p" + string(v.Patch)
	}
	return s
}

// usableSerial drops the placeholder values firmware reports on machines
// without a serial number (virtual machines, white boxes).
func usableSerial(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "none", "unknown", "n/a", "0", "default string", "not specified", "to be filled by o.e.m.", "system serial number":
		return ""
	}
	return s
}

var uptimePart = regexp.MustCompile(`(?i)(\d+)\s*(day|hour|min|sec)`)

// parseUptime reads the uptime as seconds (a number) or as the text pfSense
// shows ("3 Days 04 Hours 12 Minutes 40 Seconds").
func parseUptime(raw []byte) (int, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		if n < 0 {
			return 0, false
		}
		return int(n), true
	}
	total, found := 0, false
	for _, m := range uptimePart.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		found = true
		switch strings.ToLower(m[2]) {
		case "day":
			total += n * 86400
		case "hour":
			total += n * 3600
		case "min":
			total += n * 60
		case "sec":
			total += n
		}
	}
	return total, found
}
