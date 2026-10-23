package unifi

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// netBuilder is the state of one api: network build.
type netBuilder struct {
	c    *collector
	b    *sdk.Batch
	res  result
	rels map[string]bool
	// byID maps a device id of the console to its ref, across the selected
	// sites, to resolve an uplink.
	byID map[string]string
}

// buildNetwork turns a snapshot of the Network Integration API into objects
// and relations of b.
//
// The devices are the ones the Site Manager collector makes, merged by base
// MAC (ADR 0057). Two sources writing the same attribute with different values
// would make the resolved value flip from one run to the next, so this one sets
// only attributes whose value the other gives too, or that the other does not
// give at all (ADR 0060). It does not set the display model, the firmware
// record, the uptime, the serial or the console relation.
//
// No cable and no neighbor_of is made: the API says which device a device is
// uplinked to, not through which port, and a port pairing guessed from speeds
// or PoE would be an invention (ADR 0010).
func (c *collector) buildNetwork(b *sdk.Batch, s *netSnapshot) result {
	x := &netBuilder{c: c, b: b, rels: map[string]bool{}, byID: map[string]string{}}
	x.res.version = s.version
	x.res.partial = s.partial
	for i := range s.sites {
		if !s.sites[i].haveDevices {
			x.res.partial = true
		}
	}
	// Devices first, so that an uplink may name a device of another site.
	type placed struct {
		ref string
		det *netDeviceDetails
	}
	var uplinked []placed
	for i := range s.sites {
		sd := &s.sites[i]
		for _, d := range sd.devices {
			ref := x.device(d)
			if ref == "" {
				continue
			}
			x.byID[string(d.ID)] = ref
			if det := sd.details[string(d.ID)]; det != nil {
				x.ports(ref, det.Interfaces.Ports)
				uplinked = append(uplinked, placed{ref, det})
			}
		}
	}
	for _, p := range uplinked {
		x.uplink(p.ref, p.det)
	}
	return x.res
}

func (x *netBuilder) warn(format string, a ...any) {
	x.res.warnings = append(x.res.warnings, fmt.Sprintf(format, a...))
}

func (x *netBuilder) relate(typ, from, to string) {
	k := typ + "|" + from + "|" + to
	if x.rels[k] {
		return
	}
	x.rels[k] = true
	x.b.Relate(typ, from, to)
}

// device emits one device and returns its ref, "" when it has no usable MAC.
func (x *netBuilder) device(d netDeviceOverview) string {
	mac := normMAC(string(d.MACAddress))
	if mac == "" {
		x.warn("device %q has no usable MAC address: skipped", d.Name)
		return ""
	}
	ref := "dev:" + mac
	if _, dup := x.b.Lookup(ref); dup {
		return ref
	}
	o := x.b.Object(ref, "device").Identify("base_mac", mac).Set("vendor", vendor)
	if d.Name != "" {
		o.Identify("name", string(d.Name))
	}
	if d.Model != "" {
		o.Set("part_number", string(d.Model))
	}
	role, known := networkRole(d, x.c.roles)
	if known {
		o.Set("role", role)
	} else {
		x.warn("unknown UniFi model %q (%s): no role set; set options.roles", d.Model, d.Name)
	}
	if v := string(d.FirmwareVersion); v != "" {
		o.Set("os_version", v)
	}
	o.Set("health", healthOf(string(d.State)))
	// On a gateway the local address is its LAN one, and the Site Manager API
	// reports the WAN side as the device's address: the two would flip, so the
	// console's management address is the cloud's alone. Its LAN address is
	// still an address of the device.
	gateway := slices.Contains(d.Features, "gateway")
	if ip := usableIP(string(d.IPAddress)); ip != "" {
		if !gateway {
			o.Set("mgmt_ip", ip)
		}
		x.assign(ip, ref, map[bool]string{true: "", false: rolePrimary}[gateway])
	}
	return ref
}

// assign makes the ip_address object of ip (once) and assigns it to ref.
func (x *netBuilder) assign(ip, ref, role string) {
	ipRef := "ip:" + ip
	o := x.b.Object(ipRef, "ip_address").Identify("address", ip)
	if role != "" {
		o.Set("role", role)
	}
	x.relate("assigned_to", ipRef, ref)
}

// networkRole names the role of a device from what the Network Integration API
// says of it: options.roles, then the features the console lists (gateway,
// accessPoint, switching), then the model rules of the Site Manager collector,
// then the key prefix of an Access product (UA..., not the UAP and UAL access
// points). known is false when nothing says: the role is then left to the
// other source.
func networkRole(d netDeviceOverview, overrides map[string]string) (role string, known bool) {
	if r, ok := overrides[key(string(d.Model))]; ok {
		return r, true
	}
	switch {
	case slices.Contains(d.Features, "gateway"):
		return "firewall", true
	case slices.Contains(d.Features, "accessPoint"):
		return "access_point", true
	case slices.Contains(d.Features, "switching"):
		return "switch", true
	}
	if r, ok := roleOf(device{Model: d.Model, Shortname: d.Model}, false, nil); ok {
		return r, true
	}
	if k := key(string(d.Model)); strings.HasPrefix(k, "UA") && !strings.HasPrefix(k, "UAP") && !strings.HasPrefix(k, "UAL") {
		return "access_control", true
	}
	return "", false
}

// ports emits one interface per port of a device.
func (x *netBuilder) ports(devRef string, ports []netPort) {
	seen := map[int]bool{}
	for _, p := range ports {
		idx, ok := atoi(p.Idx)
		if !ok || idx < 1 {
			x.warn("a port of %s has index %q: skipped", devRef, p.Idx)
			continue
		}
		if seen[idx] {
			continue
		}
		seen[idx] = true
		name := "Port " + strconv.Itoa(idx)
		in := x.b.Object(devRef+"/if:"+name, "interface").Identify("name", name).In(devRef)
		in.Set("kind", "physical")
		state := portStatus(string(p.State))
		in.Set("oper_status", state)
		if sp, ok := atoi(p.SpeedMbps); ok && sp > 0 && state == "up" {
			in.Set("speed_mbps", sp)
		}
		if mx, ok := atoi(p.MaxSpeedMbps); ok && mx > 0 {
			in.Set("max_speed_mbps", mx)
		}
		if c := connectorName(string(p.Connector)); c != "" {
			in.Set("connector", c)
		}
		if p.PoE != nil {
			in.Set("poe_status", poeStatus(p.PoE))
			if std := string(p.PoE.Standard); std != "" {
				in.Set("poe_standard", std)
			}
		}
	}
}

// uplink relates a device to the device it is uplinked to. An unknown device
// (another console's site that is not selected, a device that was not listed)
// is a warning and no relation; a device that names itself is ignored.
func (x *netBuilder) uplink(ref string, det *netDeviceDetails) {
	if det.Uplink == nil || det.Uplink.DeviceID == "" {
		return
	}
	to, ok := x.byID[string(det.Uplink.DeviceID)]
	switch {
	case !ok:
		x.warn("the uplink of %s is device %q, which is not among the devices read: no uplinked_to", ref, det.Uplink.DeviceID)
	case to != ref:
		x.relate("uplinked_to", ref, to)
	}
}

// portStatus maps a port's state to the schema's oper_status.
func portStatus(s string) string {
	switch strings.ToUpper(s) {
	case "UP":
		return "up"
	case "DOWN":
		return "down"
	}
	return "unknown"
}

// connectorName writes a connector as the hardware names it.
func connectorName(s string) string {
	switch strings.ToUpper(s) {
	case "":
		return ""
	case "SFPPLUS":
		return "SFP+"
	}
	return strings.ToUpper(s)
}

// poeStatus maps a port's PoE to the schema's poe_status: a port whose PoE is
// off is disabled whatever its state says; an enabled one is delivering when
// power flows, idle when nothing draws any, limited by the power budget.
func poeStatus(p *netPoE) string {
	if !bool(p.Enabled) {
		return "disabled"
	}
	switch strings.ToUpper(string(p.State)) {
	case "UP":
		return "delivering"
	case "DOWN":
		return "idle"
	case "LIMITED":
		return "limited"
	}
	return "unknown"
}

// atoi reads a number the API wrote as a number or as text.
func atoi(s str) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(string(s)))
	return n, err == nil
}
