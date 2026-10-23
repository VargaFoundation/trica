package unifi

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// snapshot holds what one run read.
type snapshot struct {
	hosts  []host // the consoles selected by options.hosts, or all of them
	sites  []site
	groups []deviceGroup
	// have records which calls answered, so that "no WAN statistics" can be
	// told from "statistics not read".
	haveSites   bool
	haveDevices bool
	// The ISP metrics of the window [metricsFrom, metricsTo].
	metrics                []metricSite
	haveMetrics            bool
	metricsFrom, metricsTo time.Time
}

// result is what build reports besides the objects of the batch.
type result struct {
	version  string   // the console's version, when the target covers exactly one
	warnings []string // reported, not a fault
	partial  bool     // some claim could not be refreshed: nothing is withdrawn
}

const vendor = "Ubiquiti"

// builder is the state of one build: dedup sets for the objects and relations
// that several parts of the answer produce.
type builder struct {
	c    *collector
	b    *sdk.Batch
	s    *snapshot
	res  result
	rels map[string]bool
	// access maps host id + NUL + WAN type to the ref of the internet access
	// made for that WAN, where the ISP metrics are attached.
	access map[string]string
}

// build turns a snapshot into objects and relations of b.
//
// A UniFi site is a namespace inside a console, not a place, so no site
// object is made (ADR 0057). Devices are identified by their base MAC, which
// every entry has; the console is the same physical device in /v1/hosts and
// in /v1/devices and is merged by MAC.
func (c *collector) build(b *sdk.Batch, s *snapshot) result {
	x := &builder{c: c, b: b, s: s, rels: map[string]bool{}, access: map[string]string{}}

	devices := map[string][]device{} // host id -> its devices
	for _, g := range s.groups {
		devices[string(g.HostID)] = append(devices[string(g.HostID)], g.Devices...)
	}
	consoles, version := 0, ""
	for i := range s.hosts {
		h := &s.hosts[i]
		label := hostLabel(h)
		rs := h.ReportedState
		switch {
		case bool(h.IsBlocked):
			x.warn("console %s is blocked: skipped", label)
			continue
		case rs == nil:
			x.warn("console %s has no reported state: skipped", label)
			continue
		}
		disconnected := rs.State != "" && !strings.EqualFold(string(rs.State), "connected")
		if disconnected {
			x.res.partial = true
			x.warn("console %s is %s from the cloud: device states are not refreshed", label, rs.State)
		}
		hid := string(h.ID)
		list := devices[hid]
		consoleRef := ""
		if strings.EqualFold(string(h.Type), "console") {
			var v string
			consoleRef, v, list = x.console(h, list, disconnected)
			if consoleRef != "" {
				consoles++
				version = v
			}
		} else {
			x.warn("host %s is a %q, not a console: its devices are collected without a controller", label, h.Type)
		}
		for _, d := range list {
			x.device(h, d, disconnected, consoleRef)
		}
	}
	if consoles == 1 {
		x.res.version = version
	}
	x.ispMetrics()
	return x.res
}

func (x *builder) warn(format string, a ...any) {
	x.res.warnings = append(x.res.warnings, fmt.Sprintf(format, a...))
}

// relate adds a relation once.
func (x *builder) relate(typ, from, to string) {
	k := typ + "|" + from + "|" + to
	if x.rels[k] {
		return
	}
	x.rels[k] = true
	x.b.Relate(typ, from, to)
}

func hostLabel(h *host) string {
	if rs := h.ReportedState; rs != nil {
		if rs.Name != "" {
			return string(rs.Name)
		}
		if rs.Hostname != "" {
			return string(rs.Hostname)
		}
	}
	return string(h.ID)
}

// console emits the host's console device and everything that hangs off it
// (WAN ports, addresses, Internet accesses, applications). It returns the
// console's ref, its version and the devices of the host without the console.
func (x *builder) console(h *host, list []device, disconnected bool) (ref, version string, rest []device) {
	rs := h.ReportedState
	mac := normMAC(string(rs.MAC))
	if mac == "" {
		mac = normMAC(string(rs.Hardware.MAC))
	}
	idx := -1
	for i, d := range list {
		if bool(d.IsConsole) && (mac == "" || normMAC(string(d.MAC)) == mac) {
			idx = i
			break
		}
	}
	var d device
	fromHost := idx < 0
	if fromHost {
		// The device list did not answer (or does not list the console): what
		// the host says is enough to keep the console and its WAN ports. The
		// model and versions come only from the device list, so that they
		// never flip between the two sources.
		d = device{MAC: str(mac), Name: firstOf(rs.Name, rs.Hostname), Shortname: rs.Hardware.Shortname, IsConsole: true, IsManaged: true}
		rest = list
	} else {
		d = list[idx]
		rest = append(append([]device(nil), list[:idx]...), list[idx+1:]...)
	}
	if mac == "" {
		mac = normMAC(string(d.MAC))
	}
	if mac == "" {
		x.warn("console %s has no MAC address: skipped", hostLabel(h))
		return "", "", list
	}
	ref = "dev:" + mac
	o := x.deviceObject(ref, d, mac, bool(rs.Features.HasGateway), disconnected, fromHost)
	o.Identify("serial", string(rs.Hardware.Serialno))
	o.Set("os_name", "UniFi OS")
	if fromHost {
		o.Identify("name", string(firstOf(rs.Name, rs.Hostname)))
	}
	x.wans(h, ref)
	x.addresses(h, d, ref)
	x.applications(rs, ref)
	if !fromHost {
		version = string(d.Version)
	}
	return ref, version, rest
}

// device emits one non-console device of host h.
func (x *builder) device(h *host, d device, disconnected bool, consoleRef string) {
	mac := normMAC(string(d.MAC))
	if mac == "" {
		mac = normMAC(string(d.ID))
	}
	if mac == "" {
		x.warn("device %q of console %s has no MAC address: skipped", d.Name, hostLabel(h))
		return
	}
	ref := "dev:" + mac
	if _, dup := x.b.Lookup(ref); dup {
		return
	}
	x.deviceObject(ref, d, mac, false, disconnected, false)
	if ip := usableIP(string(d.IP)); ip != "" {
		x.assign(ip, ref, rolePrimary)
	}
	if bool(d.IsManaged) && consoleRef != "" {
		x.relate("managed_by", ref, consoleRef)
	}
}

const rolePrimary = "primary"

// deviceObject writes the attributes every UniFi device has, console or not.
func (x *builder) deviceObject(ref string, d device, mac string, hasGateway, disconnected, fromHost bool) *sdk.Object {
	o := x.b.Object(ref, "device").
		Identify("base_mac", mac).
		Identify("name", string(d.Name)).
		Set("vendor", vendor).
		Set("model", string(d.Model)).
		Set("part_number", string(d.Shortname))
	role, known := roleOf(d, hasGateway, x.c.roles)
	o.Set("role", role)
	if !known {
		x.warn("unknown UniFi model %q (%s): role other; set options.roles", d.Model, d.Shortname)
	}
	if fromHost {
		return o
	}
	if v := string(d.Version); v != "" {
		o.Set("os_version", v)
		rec := map[string]any{"name": "firmware", "version": v}
		if u := updateOf(d); u != "" {
			rec["update_available"] = u
		}
		o.Set("firmware", []map[string]any{rec})
	}
	if ip := usableIP(string(d.IP)); ip != "" {
		o.Set("mgmt_ip", ip)
	}
	o.Set("description", string(d.Note))
	if !disconnected {
		o.Set("health", healthOf(string(d.Status)))
		if up, ok := x.uptime(d); ok {
			o.Set("uptime_s", up)
		}
	}
	return o
}

func (x *builder) uptime(d device) (int, bool) {
	t, err := time.Parse(time.RFC3339, string(d.StartupTime))
	if err != nil {
		return 0, false
	}
	sec := int(x.c.now().Sub(t) / time.Second)
	return sec, sec > 0
}

// updateOf is the version a device could update to, "" when none is offered.
func updateOf(d device) string {
	switch strings.ToLower(string(d.UpdateAvailable)) {
	case "", "false", "0", "null":
		return ""
	}
	return string(d.UpdateAvailable)
}

// wans emits the console's WAN ports as interfaces and, for each enabled WAN,
// an Internet access reached through the console.
func (x *builder) wans(h *host, consoleRef string) {
	rs := h.ReportedState
	host := string(firstOf(rs.Hostname, rs.Name, h.ID))
	fallbackUsed := false
	for _, w := range rs.WANs {
		name := string(w.Interface)
		if name == "" {
			continue
		}
		ifRef := consoleRef + "/if:" + name
		in := x.b.Object(ifRef, "interface").Identify("name", name).In(consoleRef)
		in.Set("kind", "physical")
		in.Set("description", string(w.Type))
		if m := normMAC(string(w.MAC)); m != "" {
			in.Set("mac", m)
		}
		in.Set("admin_status", map[bool]string{true: "up", false: "down"}[bool(w.Enabled)])
		in.Set("oper_status", map[bool]string{true: "up", false: "down"}[bool(w.Plugged)])
		if sp := speedMbps(string(w.SpeedType)); sp > 0 {
			in.Set("speed_mbps", sp)
		}
		if ip := usableIP(string(w.IPv4)); ip != "" {
			x.assign(ip, ifRef, "")
		}
		if ip := usableIP(string(w.IPv6)); ip != "" {
			x.assign(ip, ifRef, "")
		}
		if !bool(w.Enabled) || !x.s.haveSites {
			// Without the site statistics an Internet access would be a
			// name with no provider, status or address.
			continue
		}
		typ := string(firstOf(w.Type, w.Interface))
		stat, siteISP, found := x.wanStat(string(h.ID), string(w.Type))
		ia := x.b.Object(consoleRef+"/wan:"+typ, "internet_access").
			Identify("name", fmt.Sprintf("%s/%s/%s", x.c.t.Name, host, typ))
		ia.Set("provider", string(firstOf(stat.ISPInfo.Name, siteISP)))
		if found && stat.PortUp != nil {
			ia.Set("status", map[bool]string{true: "online", false: "offline"}[bool(*stat.PortUp)])
		}
		if ip := usableIP(string(stat.ExternalIP)); ip != "" {
			ia.Set("public_ips", []string{ip})
		} else if !fallbackUsed && bool(w.Plugged) && w.IPv4 != "" {
			// The host's own address is the one the cloud sees: the WAN that
			// is up carries it when the site statistics did not say.
			if ip := usableIP(string(h.IPAddress)); ip != "" {
				ia.Set("public_ips", []string{ip})
				fallbackUsed = true
			}
		}
		x.access[string(h.ID)+"\x00"+string(w.Type)] = ia.Ref
		x.relate("routes_via", consoleRef, ia.Ref)
	}
}

// wanStat finds the statistics of one WAN among the host's sites.
func (x *builder) wanStat(hostID, wanType string) (stat wanStat, siteISP str, found bool) {
	for _, st := range x.s.sites {
		if string(st.HostID) != hostID {
			continue
		}
		if s, ok := st.Statistics.WANs[wanType]; ok {
			return s, st.Statistics.ISPInfo.Name, true
		}
		if siteISP == "" {
			siteISP = st.Statistics.ISPInfo.Name
		}
	}
	return wanStat{}, siteISP, false
}

// addresses emits the console's addresses that are not on a WAN port: its
// own (the one the device list gives) and the LAN ones the host reports.
func (x *builder) addresses(h *host, d device, consoleRef string) {
	rs := h.ReportedState
	onWAN := map[string]bool{}
	for _, w := range rs.WANs {
		for _, a := range []string{string(w.IPv4), string(w.IPv6)} {
			if ip := usableIP(a); ip != "" {
				onWAN[ip] = true
			}
		}
	}
	if ip := usableIP(string(d.IP)); ip != "" && !onWAN[ip] {
		x.assign(ip, consoleRef, rolePrimary)
	}
	for _, a := range rs.IPAddrs {
		if ip := usableIP(string(a)); ip != "" && !onWAN[ip] {
			x.assign(ip, consoleRef, "")
		}
	}
}

// assign makes the ip_address object of ip (once) and assigns it to ref.
func (x *builder) assign(ip, ref, role string) {
	ipRef := "ip:" + ip
	o := x.b.Object(ipRef, "ip_address").Identify("address", ip)
	if role != "" {
		o.Set("role", role)
	}
	x.relate("assigned_to", ipRef, ref)
}

// applications emits the UniFi applications that run on the console.
func (x *builder) applications(rs *reportedState, consoleRef string) {
	for _, ctl := range rs.Controllers {
		if !bool(ctl.IsInstalled) || ctl.Version == "" || ctl.Name == "" {
			continue
		}
		name := softwareName(string(ctl.Name))
		ref := "sw:" + name + "/" + string(ctl.Version)
		x.b.Object(ref, "software").
			Identify("key", "application/"+name+"/"+string(ctl.Version)).
			Set("kind", "application").
			Set("name", name).
			Set("version", string(ctl.Version)).
			Set("vendor", vendor)
		x.relate("runs", consoleRef, ref)
	}
}

// ---------------------------------------------------------------- helpers

// roleOf names the role of a device. Shortnames are not a clean taxonomy
// (USP... is both a switch and a PDU), so the display model is read first.
// known is false for a model no rule recognises: the role is "other" and the
// run says so, so that the operator can set options.roles.
func roleOf(d device, hasGateway bool, overrides map[string]string) (role string, known bool) {
	model, short := key(string(d.Model)), key(string(d.Shortname))
	for _, k := range []string{short, model} {
		if r, ok := overrides[k]; ok && k != "" {
			return r, true
		}
	}
	has := func(s string, prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(s, p) {
				return true
			}
		}
		return false
	}
	line := strings.ToLower(string(d.ProductLine))
	switch {
	case bool(d.IsConsole) && hasGateway, has(model, "UDM", "UCG", "UXG", "UDR", "USG"):
		return "firewall", true
	case has(model, "USPPDU", "USPSTRIP", "USPPLUG"):
		return "pdu", true
	case has(model, "UNVR", "ENVR"):
		return "nas", true
	case line == "access":
		return "access_control", true
	case line == "protect":
		return "camera", true
	case has(model, "U6", "U7", "UAP", "AC", "NANOHD", "FLEX", "UAL"):
		return "access_point", true
	case has(model, "USW") || (strings.HasPrefix(model, "US") && len(model) > 2 && model[2] >= '0' && model[2] <= '9'), has(short, "USPM", "USL", "USW"):
		return "switch", true
	case bool(d.IsConsole):
		// A console without a gateway (a Cloud Key) is a server of UniFi
		// applications; no schema role fits it better than "other".
		return "other", true
	}
	return "other", false
}

// key normalises a model or shortname for comparison: upper case, letters
// and digits only ("UA Hub Door" and "UA-Hub-Door" are the same key).
func key(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// healthOf maps a device status to the schema's health values.
func healthOf(status string) string {
	switch key(status) {
	case "ONLINE":
		return "ok"
	case "OFFLINE", "DISCONNECTED":
		return "critical"
	case "UPDATING", "ADOPTING", "PENDING", "PENDINGADOPTION", "PROVISIONING", "GETTINGREADY", "RESTARTING", "CONNECTIONINTERRUPTED", "ISOLATED":
		return "warning"
	}
	return "unknown"
}

var speedPattern = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*([gm])`)

// speedMbps reads the port speed of a WAN's speedType: "10G SFP+" is 10000,
// "2.5GbE" 2500, "GbE" 1000, "100MbE" 100. 0 when it says nothing.
func speedMbps(speedType string) int {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(speedType)), "gbe") {
		return 1000
	}
	m := speedPattern.FindStringSubmatch(speedType)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	if strings.EqualFold(m[2], "g") {
		n *= 1000
	}
	return int(n + 0.5)
}

// softwareName is the product name of a UniFi application: "network" is
// "UniFi Network".
func softwareName(controller string) string {
	c := strings.TrimSpace(controller)
	if c == "" {
		return "UniFi"
	}
	return "UniFi " + strings.ToUpper(c[:1]) + strings.ToLower(c[1:])
}

// normMAC returns the colon form of a MAC address written with or without
// separators, "" when it is not one.
func normMAC(s string) string {
	var hex strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'):
			hex.WriteRune(r)
		case r == ':' || r == '-' || r == '.':
		default:
			return ""
		}
	}
	h := hex.String()
	if len(h) != 12 {
		return ""
	}
	var parts []string
	for i := 0; i < 12; i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// usableIP returns s in canonical form when it is an address worth an
// ip_address object: not empty, not link-local, loopback or unspecified.
func usableIP(s string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() {
		return ""
	}
	return a.Unmap().String()
}

func firstOf(vals ...str) str {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
