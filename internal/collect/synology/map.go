package synology

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

const vendor = "Synology"

// result is what build reports besides the objects of the batch.
type result struct {
	version  string   // the DSM version
	warnings []string // reported, not a fault
}

// builder is the state of one build.
type builder struct {
	c    *collector
	b    *sdk.Batch
	s    *snapshot
	res  result
	rels map[string]bool
}

const devRef = "dev"

// build turns a snapshot into the objects and relations of b: the NAS and its
// interfaces, addresses, drives and software, and the UPS it watches. Storage
// pools, volumes and SSD caches have no type in the schema: they only feed the
// health rollup.
func (c *collector) build(b *sdk.Batch, s *snapshot) result {
	x := &builder{c: c, b: b, s: s, rels: map[string]bool{}}
	serial := string(firstOf(s.sys.Serial, s.dsm.Serial))
	name := x.name()
	osVersion := osVersion(string(firstOf(s.sys.FirmwareVer, s.dsm.VersionString)))
	x.res.version = osVersion
	if serial == "" {
		x.warn("DSM reports no serial number: the NAS is identified by its name")
	}

	d := b.Object(devRef, "device").
		Identify("serial", serial).
		Identify("name", name).
		Set("vendor", vendor).
		Set("model", string(firstOf(s.sys.Model, s.dsm.Model))).
		Set("os_name", "DSM").
		Set("os_version", osVersion).
		Set("role", c.role)
	if ram, ok := firstNum(s.sys.RAMSize, s.dsm.RAM); ok {
		d.Set("memory_bytes", ram<<20)
	}
	if n, ok := s.sys.CPUCores.Int(); ok && n > 0 {
		d.Set("logical_cpus", n)
	}
	if up, ok := uptime(string(s.sys.UpTime)); ok {
		d.Set("uptime_s", up)
	} else if n, ok := s.dsm.Uptime.Int(); ok && n > 0 {
		d.Set("uptime_s", n)
	}
	if ip := usableIP(string(s.netInfo.GatewayInfo.IP)); ip != "" {
		d.Set("primary_ip", ip)
	}
	if ip := x.mgmtIP(); ip != "" {
		d.Set("mgmt_ip", ip)
	}
	d.Set("base_mac", baseMAC(s.nics))
	d.Set("health", x.health())

	x.interfaces(usableIP(string(s.netInfo.GatewayInfo.IP)))
	x.disks()
	x.software(osVersion)
	x.upsDevice(name)
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

// name is the NAS's name: the server name, else the hostname, else the target's.
func (x *builder) name() string {
	return string(firstOf(x.s.netInfo.ServerName, str(x.s.hostname), str(x.c.t.Name)))
}

// mgmtIP is the address trica reaches the NAS at, when it is an IP literal.
func (x *builder) mgmtIP() string {
	u, err := url.Parse(x.c.base.String())
	if err != nil {
		return ""
	}
	return usableIP(u.Hostname())
}

// health is the rollup of the NAS: critical when a drive, pool, volume or
// SSD cache is crashed, failed or broken; warning when one is in any other
// state than normal, or when DSM raises its temperature warning; ok
// otherwise. When the storage could not be read the rollup cannot say ok: it
// is unknown unless the temperature warning says warning.
func (x *builder) health() string {
	s := x.s
	rank := 0
	note := func(status string) {
		switch statusLevel(status) {
		case "critical":
			rank = max(rank, 2)
		case "warning":
			rank = max(rank, 1)
		}
	}
	for _, d := range s.storage.Disks {
		note(string(d.Status))
	}
	for _, list := range [][]status{s.storage.StoragePools, s.storage.Volumes, s.storage.SSDCaches} {
		for _, e := range list {
			note(string(e.Status))
		}
	}
	tempWarn := bool(s.sys.SysTempWarn || s.sys.TemperatureWarning || s.dsm.TempWarn)
	switch {
	case rank == 2:
		return "critical"
	case rank == 1 || tempWarn:
		return "warning"
	case !s.haveStorage:
		return "unknown"
	}
	return "ok"
}

// statusLevel classifies a DSM status text: critical, warning (anything that
// is not normal) or ok. An empty status says nothing and is ok.
func statusLevel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "normal":
		return "ok"
	case "crashed", "failed", "broken":
		return "critical"
	}
	return "warning"
}

// interfaces emits the physical interfaces (eth*) and their usable addresses.
func (x *builder) interfaces(primary string) {
	for _, n := range x.s.nics {
		id := strings.TrimSpace(n.ID)
		if !strings.HasPrefix(strings.ToLower(id), "eth") {
			continue // pppoe, lo, bridges and bonds are not physical ports
		}
		ref := devRef + "/if:" + id
		o := x.b.Object(ref, "interface").Identify("name", id).In(devRef).
			Set("kind", "physical").
			Set("mac", normMAC(n.MAC)).
			Set("oper_status", map[bool]string{true: "up", false: "down"}[strings.EqualFold(n.Status, "connected")])
		if v, ok := n.MTU.Int(); ok && v > 0 {
			o.Set("mtu", v)
		}
		if v, ok := n.Speed.Int(); ok && v > 0 {
			o.Set("speed_mbps", v)
		}
		ip := usableIP(n.Addr)
		if ip == "" {
			continue
		}
		ipRef := "ip:" + ip
		a := x.b.Object(ipRef, "ip_address").Identify("address", ip)
		if p, ok := prefixLength(n.Mask); ok {
			a.Set("prefix_length", p)
		}
		if ip == primary {
			a.Set("role", "primary")
		}
		x.relate("assigned_to", ipRef, ref)
	}
}

// disks emits one disk per drive of the storage, child of the NAS.
func (x *builder) disks() {
	for _, d := range x.s.storage.Disks {
		id := string(d.ID)
		if id == "" {
			x.warn("a drive has no id: skipped")
			continue
		}
		o := x.b.Object(devRef+"/disk:"+id, "disk").In(devRef).
			Identify("serial", string(d.Serial)).
			Identify("name", id).
			Set("slot", string(d.LongName)).
			Set("model", string(d.Model)).
			Set("firmware_version", string(d.Firm)).
			Set("media", mediaOf(d)).
			Set("protocol", protocolOf(d)).
			Set("health", diskHealth(string(d.Status)))
		if v, ok := d.SizeTotal.Int(); ok && v > 0 {
			o.Set("capacity_bytes", v)
		}
		if d.Temp.OK {
			o.Set("temperature_c", d.Temp.V)
		}
		if bool(d.RemainLife.Trustable) && d.RemainLife.Value.OK && d.RemainLife.Value.V >= 0 {
			o.Set("wear_pct", 100-d.RemainLife.Value.V)
		}
	}
}

func mediaOf(d disk) string {
	switch {
	case strings.Contains(strings.ToLower(string(d.DiskType)), "nvme"):
		return "nvme"
	case bool(d.IsSSD):
		return "ssd"
	}
	return "hdd"
}

func protocolOf(d disk) string {
	t := strings.ToLower(string(d.DiskType))
	switch {
	case strings.Contains(t, "nvme"):
		return "NVMe"
	case strings.Contains(t, "sas"):
		return "SAS"
	case strings.Contains(t, "sata"):
		return "SATA"
	}
	return ""
}

func diskHealth(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "normal":
		return "ok"
	case "warning", "attention":
		return "warning"
	case "crashed", "failed", "broken":
		return "critical"
	}
	return "unknown"
}

// software emits DSM, the installed packages and the running container
// images, each run by the NAS.
func (x *builder) software(dsmVersion string) {
	sw := func(kind, name, version, vendor string) {
		if name == "" {
			return
		}
		key := kind + "/" + name + "/" + version
		ref := "sw:" + key
		x.b.Object(ref, "software").
			Identify("key", key).
			Set("kind", kind).
			Set("name", name).
			Set("version", version).
			Set("vendor", vendor)
		x.relate("runs", devRef, ref)
	}
	sw("os", "DSM", dsmVersion, vendor)
	for _, p := range x.s.packages {
		sw("package", string(firstOf(p.Name, p.ID)), string(p.Version), string(firstOf(p.Additional.Maintainer, vendor)))
	}
	for _, c := range x.s.containers {
		if !bool(c.State.Running) && !strings.EqualFold(string(c.Status), "running") && !strings.EqualFold(string(c.State.Status), "running") {
			continue
		}
		name, version := splitImage(string(c.Image))
		sw("container_image", name, version, "")
	}
}

// splitImage splits "registry/repo/name:tag@sha256:..." into the repository
// and the tag; an image with no tag is "latest", and a digest is dropped.
func splitImage(image string) (name, version string) {
	image, _, _ = strings.Cut(strings.TrimSpace(image), "@")
	slash := strings.LastIndex(image, "/")
	if colon := strings.LastIndex(image, ":"); colon > slash {
		return image[:colon], image[colon+1:]
	}
	if image == "" {
		return "", ""
	}
	return image, "latest"
}

// upsDevice emits the UPS the NAS watches through USB, and the powered_by
// relation. DSM gives no serial: the UPS is named after its NAS.
func (x *builder) upsDevice(nasName string) {
	u := x.s.ups
	if !x.s.haveUPS || !bool(u.Enable) || !bool(u.USBUPSConnect) {
		return
	}
	const ref = "ups"
	x.b.Object(ref, "device").
		Identify("name", nasName+"/ups").
		Set("role", "ups").
		Set("vendor", string(u.Manufacture)).
		Set("model", string(u.Model)).
		Set("health", upsHealth(string(u.Status)))
	x.relate("powered_by", devRef, ref)
}

func upsHealth(status string) string {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "low"):
		return "critical"
	case strings.Contains(s, "batt"), strings.Contains(s, "replace"):
		return "warning"
	case strings.Contains(s, "online"):
		return "ok"
	}
	return "unknown"
}

// ---------------------------------------------------------------- helpers

// osVersion drops the "DSM " prefix of DSM's firmware string.
func osVersion(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 4 && strings.EqualFold(s[:4], "DSM ") {
		s = s[4:]
	}
	return strings.TrimSpace(s)
}

// uptime reads DSM's "H:MM:SS", where H may exceed 24.
func uptime(s string) (int64, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, false
	}
	var total int64
	for i, p := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || n < 0 || (i > 0 && n > 59) {
			return 0, false
		}
		total = total*60 + n
	}
	return total, true
}

// firstNum returns the first positive value among vals.
func firstNum(vals ...num) (int64, bool) {
	for _, v := range vals {
		if n, ok := v.Int(); ok && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func firstOf(vals ...str) str {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// baseMAC is eth0's MAC address, else the lowest one of the physical ports.
func baseMAC(nics []nic) string {
	var macs []string
	for _, n := range nics {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(n.ID)), "eth") {
			continue
		}
		m := normMAC(n.MAC)
		if m == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(n.ID), "eth0") {
			return m
		}
		macs = append(macs, m)
	}
	if len(macs) == 0 {
		return ""
	}
	return slices.Min(macs)
}

// normMAC returns the colon form of a MAC address written with or without
// separators (DSM writes 02-11-32-00-00-01), "" when it is not one.
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
// object: not empty, not link-local, loopback, unspecified or multicast.
func usableIP(s string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() {
		return ""
	}
	return a.Unmap().String()
}

// prefixLength reads a netmask (255.255.255.0) or a prefix length ("64").
func prefixLength(mask string) (int, bool) {
	mask = strings.TrimSpace(mask)
	if n, err := strconv.Atoi(mask); err == nil {
		return n, n >= 0 && n <= 128
	}
	a, err := netip.ParseAddr(mask)
	if err != nil || !a.Is4() {
		return 0, false
	}
	ones, bits := net.IPMask(a.AsSlice()).Size()
	if bits != 32 {
		return 0, false
	}
	return ones, true
}
