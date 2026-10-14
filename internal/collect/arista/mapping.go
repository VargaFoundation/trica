package arista

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// ifaceInfo is what the interface commands say about one interface, merged.
type ifaceInfo struct {
	name, kind, mac, desc string
	mtu, bandwidth        int64
	admin, oper, duplex   string
	mode                  string
	native                int64
	hasNative             bool
	mediaType             string
	ips                   []ipInfo
}

type ipInfo struct {
	addr, role string
	maskLen    int64
}

// builder turns the gathered command results into one batch.
type builder struct {
	c        *Collector
	b        *sdk.Batch
	d        *data
	warnings []string
	ifaces   map[string]*ifaceInfo
	ifRef    map[string]string
	psuRef   map[string]string
	vlanRef  map[string]string
}

// build maps the results to a batch. The caller has checked that show version
// worked; every other command is optional and its absence only leaves data out.
func (c *Collector) build(d *data, ver *showVersion) (*sdk.Batch, []string) {
	bd := &builder{
		c: c, d: d, b: sdk.NewBatch(c.target.Source(), sdk.ScopeFull),
		ifRef: map[string]string{}, psuRef: map[string]string{}, vlanRef: map[string]string{},
	}
	dev := bd.device(ver)
	bd.interfaces()
	bd.addresses(dev)
	bd.vlans()
	bd.portChannels()
	bd.learnedMACs()
	bd.lldp()
	bd.power()
	bd.cooling()
	bd.temperature(dev)
	bd.transceivers()
	return bd.b, bd.warnings
}

// device creates the switch itself.
func (bd *builder) device(ver *showVersion) *sdk.Object {
	inv := decode[showInventory](bd.d, cmdInventory)
	host := decode[showHostname](bd.d, cmdHostname)
	up := decode[showUptime](bd.d, cmdUptime)
	local := decode[showLLDPLocal](bd.d, cmdLLDPLocal)

	serial := strings.TrimSpace(ver.SerialNumber)
	if serial == "" && inv != nil {
		serial = strings.TrimSpace(inv.SystemInformation.SerialNum)
	}
	name := ""
	switch {
	case host != nil && strings.TrimSpace(host.Hostname) != "":
		name = strings.TrimSpace(host.Hostname)
	case host != nil && host.FQDN != "":
		name = shortName(host.FQDN)
	case local != nil:
		name = shortName(local.SystemName)
	}
	vendor := strings.TrimSpace(ver.MfgName)
	if vendor == "" {
		vendor = "Arista"
	}

	dev := bd.b.Object("dev", "device").
		Identify("serial", serial).
		Identify("name", name).
		Set("role", "switch").
		Set("vendor", vendor).
		Set("model", strings.TrimSpace(ver.ModelName)).
		Set("os_name", "EOS").
		Set("os_version", strings.TrimSpace(ver.Version))
	if mac, ok := validMAC(ver.SystemMacAddress); ok {
		dev.Identify("base_mac", mac)
	}
	if ver.MemTotal.OK && ver.MemTotal.V > 0 {
		// memTotal is in kibibytes.
		dev.Set("memory_bytes", int64(ver.MemTotal.V)*1024)
	}
	if up != nil && up.UpTime.OK && up.UpTime.V >= 0 {
		dev.Set("uptime_s", int64(up.UpTime.V))
	}
	if inv != nil && ver.ModelName == "" {
		dev.Set("model", strings.TrimSpace(inv.SystemInformation.Name))
	}
	if mlag := decode[showMLAG](bd.d, cmdMLAG); mlag != nil {
		dev.Set("mlag_state", strings.TrimSpace(mlag.State)).
			Set("mlag_domain", strings.TrimSpace(mlag.DomainID))
		if ip, ok := validIP(mlag.PeerAddress); ok && ip != "0.0.0.0" {
			dev.Set("mlag_peer", ip)
		}
	}
	return dev
}

// ---------------------------------------------------------------- interfaces

func (bd *builder) interfaces() {
	bd.ifaces = map[string]*ifaceInfo{}
	get := func(raw string) *ifaceInfo {
		name := canonicalInterface(raw)
		if name == "" {
			return nil
		}
		if in, ok := bd.ifaces[name]; ok {
			return in
		}
		in := &ifaceInfo{name: name, kind: interfaceKind(name)}
		bd.ifaces[name] = in
		return in
	}

	if full := decode[showInterfaces](bd.d, cmdInterfaces); full != nil {
		for raw, r := range full.Interfaces {
			in := get(raw)
			if in == nil {
				continue
			}
			if in.kind == "other" {
				in.kind = kindFromHardware(r.Hardware)
			}
			in.desc = strings.TrimSpace(r.Description)
			in.mtu = int64(r.MTU.V)
			in.bandwidth = int64(r.Bandwidth.V)
			if m, ok := validMAC(r.PhysicalAddress); ok {
				in.mac = m
			}
			in.admin = adminStatus(r.InterfaceStatus)
			in.oper = operStatus(r.LineProtocol)
			in.duplex = duplexOf(r.Duplex)
			in.mediaType = mediaOf(r.InterfaceType)
			in.ips = append(in.ips, addressesOf(r.InterfaceAddress)...)
		}
	}
	if st := decode[showInterfacesStatus](bd.d, cmdStatus); st != nil {
		for raw, r := range st.InterfaceStatuses {
			in := get(raw)
			if in == nil {
				continue
			}
			if in.desc == "" {
				in.desc = strings.TrimSpace(r.Description)
			}
			if in.oper == "" {
				in.oper = operFromLink(r.LinkStatus)
			}
			if in.admin == "" && r.LinkStatus != "" {
				in.admin = adminStatus(r.LinkStatus)
			}
			if in.duplex == "" {
				in.duplex = duplexOf(r.Duplex)
			}
			if in.bandwidth == 0 && r.Bandwidth.OK {
				in.bandwidth = int64(r.Bandwidth.V)
			}
			if in.mediaType == "" {
				in.mediaType = mediaOf(r.Type)
			}
			switch r.VLAN.Mode {
			case "bridged":
				if r.VLAN.ID.OK {
					in.mode = "access"
				}
			case "trunk":
				in.mode = "trunk"
			case "routed":
				in.mode = "routed"
			}
			if r.VLAN.ID.OK && (in.mode == "access" || in.mode == "trunk") {
				in.native, in.hasNative = int64(r.VLAN.ID.V), true
			}
		}
	}
	if ipi := decode[showIPInterface](bd.d, cmdIPInterface); ipi != nil {
		for raw, r := range ipi.Interfaces {
			in := get(raw)
			if in == nil {
				continue
			}
			// "show ip interface" is the authority on addresses: it replaces
			// what the interface listing said.
			if addrs := addressesOf(r.InterfaceAddress); len(addrs) > 0 {
				in.ips = addrs
			}
		}
	}

	for _, name := range sdk.SortedKeys(bd.ifaces) {
		in := bd.ifaces[name]
		ref := "if/" + name
		bd.ifRef[name] = ref
		o := bd.b.Object(ref, "interface").Identify("name", name).In("dev").
			Set("kind", in.kind).
			Set("mac", in.mac).
			Set("description", in.desc).
			Set("admin_status", in.admin).
			Set("oper_status", in.oper)
		if in.mtu > 0 {
			o.Set("mtu", in.mtu)
		}
		if in.kind == "physical" || in.kind == "lag" {
			o.Set("duplex", in.duplex).Set("mode", in.mode)
			if in.oper == "up" && in.bandwidth >= 1_000_000 {
				o.Set("speed_mbps", in.bandwidth/1_000_000)
			}
			if in.hasNative {
				o.Set("native_vlan", in.native)
			}
		}
	}
}

// addresses creates the IP addresses and attaches them to their interface;
// the management address also becomes the device's mgmt_ip.
func (bd *builder) addresses(dev *sdk.Object) {
	mgmt := ""
	for _, name := range sdk.SortedKeys(bd.ifaces) {
		in := bd.ifaces[name]
		for _, a := range in.ips {
			ref := "ip/" + a.addr
			if _, seen := bd.b.Lookup(ref); seen {
				continue
			}
			o := bd.b.Object(ref, "ip_address").Identify("address", a.addr).Set("role", a.role)
			if a.maskLen > 0 {
				o.Set("prefix_length", a.maskLen)
			}
			bd.b.Relate("assigned_to", ref, bd.ifRef[name])
			if mgmt == "" && strings.HasPrefix(name, "Management") && a.role == "primary" {
				mgmt = a.addr
			}
		}
	}
	if mgmt == "" {
		if local := decode[showLLDPLocal](bd.d, cmdLLDPLocal); local != nil {
			for _, m := range local.ManagementAddresses {
				if ip, ok := validIP(m.Address); ok && strings.EqualFold(m.AddressType, "ipv4") {
					mgmt = ip
					break
				}
			}
		}
	}
	if mgmt == "" {
		mgmt = hostIP(bd.c.target.Address)
	}
	dev.Identify("mgmt_ip", mgmt)
}

// ---------------------------------------------------------- VLANs, LAGs, MACs

func (bd *builder) vlans() {
	v := decode[showVLAN](bd.d, cmdVLAN)
	if v == nil {
		return
	}
	for _, key := range sdk.SortedKeys(v.VLANs) {
		vid, err := strconv.Atoi(key)
		if err != nil || vid < 1 || vid > 4094 {
			continue
		}
		rec := v.VLANs[key]
		ref := "vlan/" + key
		bd.vlanRef[key] = ref
		bd.b.Object(ref, "vlan").
			Identify("key", fmt.Sprintf("%s:%d", bd.c.domain, vid)).
			Set("vid", int64(vid)).
			Set("name", strings.TrimSpace(rec.Name)).
			Set("domain", bd.c.domain)
		for _, raw := range sdk.SortedKeys(rec.Interfaces) {
			name := canonicalInterface(raw)
			in, ok := bd.ifaces[name]
			if !ok || (in.kind != "physical" && in.kind != "lag") {
				continue
			}
			r := bd.b.Relate("member_of", bd.ifRef[name], ref)
			if tagged, known := in.tagged(int64(vid)); known {
				r.With("tagged", tagged)
			}
		}
	}
	// A VLAN interface (SVI) is the layer-3 face of its VLAN.
	for _, name := range sdk.SortedKeys(bd.ifaces) {
		in := bd.ifaces[name]
		if in.kind != "vlan" {
			continue
		}
		if ref, ok := bd.vlanRef[strings.TrimPrefix(name, "Vlan")]; ok {
			bd.b.Relate("member_of", bd.ifRef[name], ref)
		}
	}
}

// tagged says whether frames of vid leave this port tagged. An access port is
// untagged; a trunk tags every VLAN but its native one, which is VLAN 1 when
// the switch did not report it (the EOS default).
func (in *ifaceInfo) tagged(vid int64) (tagged, known bool) {
	switch in.mode {
	case "access":
		return false, true
	case "trunk":
		native := int64(1)
		if in.hasNative {
			native = in.native
		}
		return vid != native, true
	}
	return false, false
}

func (bd *builder) portChannels() {
	pc := decode[showPortChannel](bd.d, cmdPortChannel)
	if pc == nil {
		return
	}
	for _, raw := range sdk.SortedKeys(pc.PortChannels) {
		lag := canonicalInterface(raw)
		if _, ok := bd.ifRef[lag]; !ok {
			continue
		}
		rec := pc.PortChannels[raw]
		members := map[string]bool{}
		for m := range rec.ActivePorts {
			members[canonicalInterface(m)] = true
		}
		for m := range rec.InactivePorts {
			members[canonicalInterface(m)] = true
		}
		for _, m := range sdk.SortedKeys(members) {
			// Peer ports of an MLAG pair ("PeerEthernet1") are not ours.
			if ref, ok := bd.ifRef[m]; ok {
				bd.b.Relate("member_of", ref, bd.ifRef[lag])
			}
		}
	}
}

func (bd *builder) learnedMACs() {
	t := decode[showMACTable](bd.d, cmdMACTable)
	if t == nil || bd.c.maxEdge == 0 {
		return
	}
	perPort := map[string]map[string]bool{}
	for _, e := range t.Unicast.Entries {
		name := canonicalInterface(e.Interface)
		in, ok := bd.ifaces[name]
		if !ok || (in.kind != "physical" && in.kind != "lag") || !e.VLAN.OK {
			continue
		}
		// Static and configured entries (the switch's own MACs, pinned hosts)
		// say nothing about what is plugged in; learned ones do.
		if e.Type != "" && !strings.Contains(strings.ToLower(e.Type), "dynamic") {
			continue
		}
		mac, ok := validMAC(e.MAC)
		if !ok {
			continue
		}
		if perPort[name] == nil {
			perPort[name] = map[string]bool{}
		}
		perPort[name][fmt.Sprintf("%d/%s", int64(e.VLAN.V), mac)] = true
	}
	for name, set := range perPort {
		if len(set) > bd.c.maxEdge {
			continue
		}
		list := make([]string, 0, len(set))
		for k := range set {
			list = append(list, k)
		}
		sort.Strings(list)
		bd.b.Object(bd.ifRef[name], "interface").Set("learned_macs", list)
	}
}

func (bd *builder) lldp() {
	n := decode[showLLDPNeighbors](bd.d, cmdLLDPNeighbors)
	if n == nil {
		return
	}
	seen := map[string]bool{}
	for _, raw := range sdk.SortedKeys(n.Neighbors) {
		local := canonicalInterface(raw)
		ref, ok := bd.ifRef[local]
		if !ok {
			bd.warnings = append(bd.warnings, fmt.Sprintf("lldp: neighbor on unknown interface %q ignored", raw))
			continue
		}
		for _, nb := range n.Neighbors[raw].Info {
			dev := map[string]string{}
			if nb.ChassisIDType == "" || strings.EqualFold(nb.ChassisIDType, "macAddress") {
				if m, ok := validMAC(nb.ChassisID); ok {
					dev["base_mac"] = m
				}
			}
			if name := shortName(nb.SystemName); name != "" {
				dev["name"] = name
			}
			port := remotePort(nb)
			if len(dev) == 0 || port == "" {
				continue
			}
			key := local + "|" + dev["base_mac"] + "|" + dev["name"] + "|" + port
			if seen[key] {
				continue
			}
			seen[key] = true
			bd.b.RelateEndpoints("neighbor_of", sdk.Ref(ref), sdk.Endpoint{
				Type:     "interface",
				Identity: map[string]string{"name": port},
				Parent:   &sdk.Endpoint{Type: "device", Identity: dev},
			}).With("protocol", "lldp")
		}
	}
}

// remotePort is the name of the neighbour's port. A port ID of type
// interfaceName or local is the name; when the neighbour identifies the port
// by MAC address (Linux hosts), the port description is the name.
func remotePort(nb lldpNeighbor) string {
	id := nb.Port.IDV2
	if id == "" {
		// EOS 4.14 wraps the identifier in literal double quotes.
		id = nb.Port.ID
	}
	id = strings.Trim(strings.TrimSpace(id), `"`)
	desc := strings.TrimSpace(nb.Port.Description)
	switch strings.ToLower(nb.Port.IDType) {
	case "interfacename", "local", "interfacealias", "":
		if id != "" {
			return id
		}
	}
	return desc
}

// ------------------------------------------------------------------ hardware

func (bd *builder) ensurePSU(slot string) string {
	if ref, ok := bd.psuRef[slot]; ok {
		return ref
	}
	ref := "psu/" + slot
	bd.b.Object(ref, "psu").Identify("name", "PowerSupply"+slot).In("dev")
	bd.psuRef[slot] = ref
	return ref
}

func (bd *builder) power() {
	inv := decode[showInventory](bd.d, cmdInventory)
	pw := decode[showPower](bd.d, cmdPower)
	slotsSeen := map[string]bool{}
	if inv != nil {
		for s := range inv.PowerSupplySlots {
			slotsSeen[s] = true
		}
	}
	if pw != nil {
		for s := range pw.PowerSupplies {
			slotsSeen[s] = true
		}
	}
	total, haveTotal := 0.0, false
	for _, slot := range sdk.SortedKeys(slotsSeen) {
		o := bd.b.Object(bd.ensurePSU(slot), "psu")
		if inv != nil {
			it := inv.PowerSupplySlots[slot]
			o.Identify("serial", strings.TrimSpace(it.SerialNum)).Set("model", strings.TrimSpace(it.Name))
		}
		if pw != nil {
			p := pw.PowerSupplies[slot]
			if o.Attributes["model"] == nil {
				o.Set("model", strings.TrimSpace(p.ModelName))
			}
			o.Set("health", psuHealth(p.State))
			if p.Capacity.OK && p.Capacity.V > 0 {
				o.Set("capacity_w", int64(p.Capacity.V))
			}
			if p.InputPower.OK {
				o.Set("input_w", p.InputPower.V)
			}
			if p.OutputPower.OK {
				total, haveTotal = total+p.OutputPower.V, true
			}
		}
	}
	if haveTotal {
		bd.b.Object("dev", "device").Set("power_w", math.Round(total*10)/10)
	}
}

func (bd *builder) cooling() {
	c := decode[showCooling](bd.d, cmdCooling)
	if c == nil {
		return
	}
	for _, tray := range c.FanTraySlots {
		if len(tray.Fans) == 0 {
			// Some trains report a tray as one unit, without its fans.
			o := bd.b.Object("fan/tray"+tray.Label, "fan").Identify("name", "FanTray"+tray.Label).In("dev").
				Set("health", fanHealth(tray.Status))
			if tray.Speed.OK {
				o.Set("speed_pct", tray.Speed.V)
			}
			continue
		}
		for _, f := range tray.Fans {
			name := f.Name
			if name == "" {
				name = f.Label
			}
			if name == "" {
				continue
			}
			if name[0] >= '0' && name[0] <= '9' {
				name = "FanTray" + name
			}
			o := bd.b.Object("fan/"+name, "fan").Identify("name", name).In("dev").Set("health", fanHealth(f.Status))
			if f.ActualSpeed.OK {
				o.Set("speed_pct", f.ActualSpeed.V)
			}
		}
	}
	for _, ps := range c.PowerSupply {
		parent := bd.ensurePSU(ps.Label)
		for _, f := range ps.Fans {
			name := f.Name
			if name == "" {
				name = f.Label
			}
			if name == "" {
				continue
			}
			o := bd.b.Object("fan/"+name, "fan").Identify("name", name).In(parent).Set("health", fanHealth(f.Status))
			if f.ActualSpeed.OK {
				o.Set("speed_pct", f.ActualSpeed.V)
			}
		}
	}
	bd.rollup(c.SystemStatus)
}

func (bd *builder) temperature(dev *sdk.Object) {
	t := decode[showTemperature](bd.d, cmdTemperature)
	if t == nil {
		return
	}
	sensors := func(parent, prefix string, list []tempSensor) {
		for _, s := range list {
			name := strings.TrimSpace(s.Name)
			if name == "" {
				continue
			}
			name = prefix + name
			o := bd.b.Object("sensor/"+parent+"/"+name, "sensor").Identify("name", name).In(parent).
				Set("kind", "temperature").Set("unit", "°C").
				Set("placement", placement(s.Name+" "+s.Description)).
				Set("health", sensorHealth(s))
			if s.Current.OK {
				o.Set("reading", s.Current.V)
			}
			if s.Overheat.OK {
				o.Set("warning_threshold", s.Overheat.V)
			}
			if s.Critical.OK {
				o.Set("critical_threshold", s.Critical.V)
			}
			if s.Current.OK {
				switch placement(s.Name + " " + s.Description) {
				case "inlet":
					dev.Set("inlet_temp_c", s.Current.V)
				case "outlet":
					dev.Set("outlet_temp_c", s.Current.V)
				}
			}
		}
	}
	sensors("dev", "", t.TempSensors)
	for _, ps := range t.PowerSupply {
		if ps.RelPos == "" {
			continue
		}
		sensors(bd.ensurePSU(ps.RelPos), "", ps.TempSensors)
	}
	for _, card := range t.Cards {
		sensors("dev", "Card"+card.RelPos+" ", card.TempSensors)
	}
	bd.rollup(t.SystemStatus)
}

// rollup folds a "systemStatus" of the environment commands into the device
// health, keeping the worst.
func (bd *builder) rollup(status string) {
	h := systemHealth(status)
	if h == "" {
		return
	}
	dev := bd.b.Object("dev", "device")
	rank := map[string]int{"ok": 1, "warning": 2, "critical": 3}
	if cur, _ := dev.Attributes["health"].(string); rank[cur] >= rank[h] {
		return
	}
	dev.Set("health", h)
}

func (bd *builder) transceivers() {
	inv := decode[showInventory](bd.d, cmdInventory)
	dom := decode[showTransceiver](bd.d, cmdTransceiver)

	type optic struct {
		vendor, model, serial, media string
		dom                          *domRecord
	}
	optics := map[string]*optic{}
	if inv != nil {
		for slot, it := range inv.XcvrSlots {
			if strings.EqualFold(it.MfgName, "Not Present") || (it.SerialNum == "" && it.ModelName == "") {
				continue
			}
			name := canonicalInterface("Ethernet" + slot)
			optics[name] = &optic{
				vendor: strings.TrimSpace(it.MfgName), model: strings.TrimSpace(it.ModelName),
				serial: strings.TrimSpace(it.SerialNum),
			}
		}
	}
	if dom != nil {
		for raw, rec := range dom.Interfaces {
			name := canonicalInterface(raw)
			rec := rec
			o := optics[name]
			if o == nil {
				if strings.TrimSpace(rec.VendorSN) == "" {
					continue
				}
				o = &optic{}
				optics[name] = o
			}
			o.dom = &rec
			if o.serial == "" {
				o.serial = strings.TrimSpace(rec.VendorSN)
			}
			if o.vendor == "" {
				o.vendor = strings.TrimSpace(rec.VendorName)
			}
			o.media = strings.TrimSpace(rec.MediaType)
		}
	}
	for _, name := range sdk.SortedKeys(optics) {
		o := optics[name]
		parent := "dev"
		if ref, ok := bd.ifRef[name]; ok {
			parent = ref
		}
		if o.media == "" {
			if in := bd.ifaces[name]; in != nil {
				o.media = in.mediaType
			}
		}
		t := bd.b.Object("xcvr/"+name, "transceiver").Identify("name", name).Identify("serial", o.serial).In(parent).
			Set("vendor", o.vendor).Set("model", o.model).Set("media_type", o.media)
		if o.dom != nil {
			if o.dom.Temperature.OK {
				t.Set("temperature_c", o.dom.Temperature.V)
			}
			if o.dom.Voltage.OK {
				t.Set("voltage_v", o.dom.Voltage.V)
			}
			if o.dom.TxPower.OK {
				t.Set("tx_power_dbm", o.dom.TxPower.V)
			}
			if o.dom.RxPower.OK {
				t.Set("rx_power_dbm", o.dom.RxPower.V)
			}
		}
	}
}

// ------------------------------------------------------------------- helpers

// canonicalInterface expands the short names some EOS outputs use.
func canonicalInterface(name string) string {
	name = strings.TrimSpace(name)
	for _, p := range []struct{ short, long string }{
		{"Et", "Ethernet"}, {"Ma", "Management"}, {"Po", "Port-Channel"}, {"Vl", "Vlan"}, {"Lo", "Loopback"},
	} {
		if rest, ok := strings.CutPrefix(name, p.short); ok && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			return p.long + rest
		}
	}
	return name
}

func interfaceKind(name string) string {
	switch {
	case strings.HasPrefix(name, "Ethernet"), strings.HasPrefix(name, "Management"):
		return "physical"
	case strings.HasPrefix(name, "Port-Channel"):
		return "lag"
	case strings.HasPrefix(name, "Vlan"):
		return "vlan"
	case strings.HasPrefix(name, "Loopback"):
		return "loopback"
	case strings.HasPrefix(name, "Vxlan"), strings.HasPrefix(name, "Tunnel"):
		return "virtual"
	}
	return "other"
}

func kindFromHardware(hw string) string {
	switch strings.ToLower(hw) {
	case "ethernet":
		return "physical"
	case "portchannel":
		return "lag"
	case "vlan":
		return "vlan"
	case "loopback":
		return "loopback"
	}
	return "other"
}

func adminStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "disabled", "admindown":
		return "down"
	}
	return "up"
}

func operStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "up":
		return "up"
	case "notpresent":
		return "not_present"
	}
	return "down"
}

func operFromLink(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "connected", "up":
		return "up"
	}
	return "down"
}

func duplexOf(s string) string {
	switch strings.ToLower(s) {
	case "duplexfull", "full":
		return "full"
	case "duplexhalf", "half":
		return "half"
	case "":
		return ""
	}
	return "unknown"
}

// mediaOf keeps an interface type only when it names a medium
// ("10GBASE-SR"); simulators and virtual ports report internal class names.
func mediaOf(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(strings.ToUpper(s), "BASE") {
		return s
	}
	return ""
}

// addressesOf reads an "interfaceAddress", an object or an array of objects.
func addressesOf(raw json.RawMessage) []ipInfo {
	var blocks []addressBlock
	var one addressBlock
	if json.Unmarshal(raw, &one) == nil {
		blocks = []addressBlock{one}
	} else if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []ipInfo
	add := func(m ipMask, role string) {
		if ip, ok := validIP(m.Address); ok && ip != "0.0.0.0" {
			out = append(out, ipInfo{addr: ip, role: role, maskLen: int64(m.MaskLen.V)})
		}
	}
	for _, b := range blocks {
		add(b.Primary, "primary")
		for _, k := range sdk.SortedKeys(b.Secondary) {
			add(b.Secondary[k], "secondary")
		}
		add(b.Virtual, "vip")
	}
	return out
}

func validIP(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr().String(), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", false
	}
	return a.String(), true
}

// validMAC returns s in the form the schema stores, or false when it is not a
// usable MAC (empty, malformed, all zeros).
func validMAC(s string) (string, bool) {
	m, err := schema.NormalizeMAC(s)
	if err != nil || m == "00:00:00:00:00:00" {
		return "", false
	}
	return m, true
}

// shortName is a hostname without its domain: LLDP systemName is often the FQDN.
func shortName(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if _, err := netip.ParseAddr(s); err == nil {
		return s
	}
	if i := strings.IndexByte(s, '.'); i > 0 {
		return s[:i]
	}
	return s
}

func placement(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "inlet"), strings.Contains(s, "intake"):
		return "inlet"
	case strings.Contains(s, "outlet"), strings.Contains(s, "exhaust"):
		return "outlet"
	case strings.Contains(s, "cpu"):
		return "cpu"
	case strings.Contains(s, "power supply"), strings.Contains(s, "powersupply"):
		return "psu"
	case strings.Contains(s, "board"), strings.Contains(s, "system"):
		return "system"
	}
	return "other"
}

func sensorHealth(s tempSensor) string {
	switch {
	case s.InAlert:
		return "warning"
	case strings.EqualFold(s.HwStatus, "ok"):
		return "ok"
	case s.HwStatus == "":
		return ""
	}
	return "critical"
}

func psuHealth(state string) string {
	switch strings.ToLower(state) {
	case "":
		return ""
	case "ok":
		return "ok"
	case "powerloss", "failed":
		return "critical"
	case "notinserted", "absent":
		return "absent"
	}
	return "unknown"
}

func fanHealth(status string) string {
	switch strings.ToLower(status) {
	case "":
		return ""
	case "ok":
		return "ok"
	case "failed":
		return "critical"
	case "notinserted", "absent":
		return "absent"
	}
	return "unknown"
}

// systemHealth reads the "systemStatus" of an environment command
// ("temperatureOk", "coolingOk", "temperatureKo", ...).
func systemHealth(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.HasSuffix(s, "ok"):
		return "ok"
	case strings.HasSuffix(s, "ko"), strings.Contains(s, "fail"), strings.Contains(s, "overheat"):
		return "critical"
	}
	return "warning"
}
