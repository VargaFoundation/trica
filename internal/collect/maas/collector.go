// Package maas collects the machine inventory of a Canonical MAAS region
// controller (REST API 2.0): machines with their hardware identity, network
// interfaces, addresses and disks, plus the subnets and VLANs they sit on.
//
// The collector is read-only: it only issues GET requests, authenticated with
// an OAuth 1.0a PLAINTEXT header built from the target's API key.
package maas

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector kind to use in a target.
const Kind = "maas"

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "MAAS",
	Vendors:    []string{"Canonical"},
	Protocol:   "MAAS REST API 2.0 over HTTP(S), OAuth 1.0a PLAINTEXT",
	Summary:    schema.Label{EN: "Bare-metal machines managed by a MAAS region controller: interfaces, addresses, disks, subnets and VLANs.", FR: "Machines bare-metal gérées par un contrôleur de région MAAS : interfaces, adresses, disques, sous-réseaux et VLAN."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"device", "disk", "interface", "ip_address", "prefix", "vlan"},
	ReadOnly:   "The client has no code path that sends anything but GET.",
	Address:    "http(s)://host[:port] of the region controller, without /MAAS.",
	Username:   collect.Credential{Use: collect.UseUnused},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "API key consumer_key:token_key:token_secret"},
	Options:    Options{},
	Tested:     []string{"MAAS 3.5"},
	Evidence:   collect.EvidenceSynthesised,
	Docs:       "internal/collect/maas/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Options are the MAAS-specific settings of a target (the "options" block).
type Options struct {
	// AssumeFullVisibility declares that the API key sees every machine even
	// though it does not belong to a MAAS administrator (for instance through
	// an RBAC "view all" role on every resource pool). Without it, a non
	// administrator's batch is partial, so that machines it cannot see are not
	// reported as gone.
	AssumeFullVisibility bool `yaml:"assume_full_visibility" default:"false" doc:"Declares that a non-administrator API key sees every machine; without it such a batch is partial, so machines it cannot see are not reported as gone."`
	// VLANDomains maps a MAAS fabric name to the layer-2 domain its VLANs are
	// filed under ("<domain>:<vid>"). A fabric that is not listed is its own
	// domain, named after the fabric. Map a fabric to "default" when it is the
	// network the switches and the firewall report under that name.
	VLANDomains map[string]string `yaml:"vlan_domains" doc:"Maps a MAAS fabric name to the layer-2 domain its VLANs are filed under; a fabric not listed is its own domain."`
	// UntaggedVIDs maps a MAAS fabric name to the VLAN ID the switches use for
	// that fabric's untagged VLAN (MAAS reports it as VID 0).
	UntaggedVIDs map[string]int `yaml:"untagged_vids" doc:"Maps a MAAS fabric name to the VLAN ID its untagged (VID 0) VLAN carries on the switches; that VLAN is then reported as <domain>:<vid>, the switches' object. A fabric not listed keeps <domain>:0."`
}

// Collector reads one MAAS region controller.
type Collector struct {
	target collect.Target
	opts   Options
}

// New builds the collector of a target. It validates the address and the
// options; credentials are read at each run.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	u, err := url.Parse(t.Address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("target %s: address must be an http(s) URL such as http://maas.example.net:5240 (got %q)", t.Source(), t.Address)
	}
	if opts.UntaggedVIDs, err = checkUntaggedVIDs(opts.UntaggedVIDs); err != nil {
		return nil, fmt.Errorf("target %s: options.untagged_vids: %w", t.Source(), err)
	}
	return &Collector{target: t, opts: opts}, nil
}

// checkUntaggedVIDs trims the fabric names and refuses a VLAN ID the switches
// cannot carry.
func checkUntaggedVIDs(m map[string]int) (map[string]int, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]int, len(m))
	for fabric, vid := range m {
		name := strings.TrimSpace(fabric)
		if name == "" {
			return nil, errors.New("a fabric name is empty")
		}
		if vid < 1 || vid > 4094 {
			return nil, fmt.Errorf("fabric %q: VLAN ID %d is not between 1 and 4094", name, vid)
		}
		out[name] = vid
	}
	return out, nil
}

// untaggedVIDs resolves options.untagged_vids against fabrics/: a fabric that
// already has the VID as a tagged VLAN is not remapped (it would merge two of
// MAAS's layer-2 networks), and a fabric MAAS does not report is warned about,
// only when fabrics/ was read (fabricsKnown).
func untaggedVIDs(opt map[string]int, fabrics []fabric, fabricsKnown bool) (map[string]int, []string) {
	if len(opt) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(opt))
	for n := range opt {
		names = append(names, n)
	}
	sort.Strings(names)
	byName := map[string]fabric{}
	for _, f := range fabrics {
		byName[strings.TrimSpace(f.Name)] = f
	}
	out := map[string]int{}
	var warnings []string
	for _, n := range names {
		vid := opt[n]
		if fabricsKnown {
			f, ok := byName[n]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("options.untagged_vids: fabric %q is not in MAAS, ignored", n))
				continue
			}
			clash := false
			for _, v := range f.VLANs {
				clash = clash || int(v.VID) == vid
			}
			if clash {
				warnings = append(warnings, fmt.Sprintf("options.untagged_vids: fabric %q already has a tagged VLAN %d, so its untagged VLAN is not reported as VLAN %d", n, vid, vid))
				continue
			}
		}
		out[n] = vid
	}
	return out, warnings
}

// Kind implements sdk.Collector.
func (c *Collector) Kind() string { return Kind }

// Collect implements sdk.Collector: one run is one batch.
func (c *Collector) Collect(parent context.Context, emit sdk.Emitter) error {
	started := time.Now()
	fail := func(version string, err error) error {
		return emit.Emit(parent, collect.Failed(c.target, Kind, version, started, err))
	}

	key, err := c.target.Password.Resolve()
	if err != nil {
		return fail("", fmt.Errorf("API key: %w", err))
	}
	creds, err := parseAPIKey(key)
	if err != nil {
		return fail("", err)
	}
	hc, err := c.target.HTTPClient()
	if err != nil {
		return fail("", err)
	}
	cl := newClient(c.target.Address, hc, creds)

	ctx, cancel := collect.Context(parent, c.target)
	defer cancel()

	var failures, warnings []string

	// The first authenticated request doubles as the connectivity and
	// credentials probe: if it gets no answer or is refused, nothing else is
	// worth trying. It also says whether the key sees every machine.
	var who whoami
	whoKnown := true
	if err := cl.get(ctx, "users/", url.Values{"op": {"whoami"}}, &who); err != nil {
		if fatal(err) {
			return fail("", err)
		}
		whoKnown = false
		failures = append(failures, "whoami: "+err.Error())
	}

	// The version only feeds the health board: its failure is a warning.
	var ver versionInfo
	if err := cl.get(ctx, "version/", nil, &ver); err != nil {
		warnings = append(warnings, "version: "+err.Error())
	}

	var machines []machine
	if err := cl.get(ctx, "machines/", nil, &machines); err != nil {
		machines = nil
		failures = append(failures, "machines: "+err.Error())
	}
	var subnets []subnet
	if err := cl.get(ctx, "subnets/", nil, &subnets); err != nil {
		subnets = nil
		failures = append(failures, "subnets: "+err.Error())
	}
	var fabrics []fabric
	fabricsOK := true
	if err := cl.get(ctx, "fabrics/", nil, &fabrics); err != nil {
		fabrics, fabricsOK = nil, false
		failures = append(failures, "fabrics: "+err.Error())
	}

	complete := c.opts.AssumeFullVisibility || (whoKnown && who.IsSuperuser)
	if whoKnown && !who.IsSuperuser && !c.opts.AssumeFullVisibility {
		warnings = append(warnings, fmt.Sprintf("API user %q is not a MAAS administrator: it only sees unallocated machines and the ones it owns, "+
			"so this batch is partial and withdraws nothing (set options.assume_full_visibility if the key does see every machine)", who.Username))
	}

	untagged, uw := untaggedVIDs(c.opts.UntaggedVIDs, fabrics, fabricsOK)
	warnings = append(warnings, uw...)
	bd := &builder{b: sdk.NewBatch(c.target.Source(), sdk.ScopeFull), domains: c.opts.VLANDomains, untagged: untagged}
	for _, f := range fabrics {
		for i := range f.VLANs {
			v := f.VLANs[i]
			if v.VID == 0 {
				continue // the untagged VLAN is reported when an interface or a subnet is on it (#148)
			}
			if v.Fabric == "" {
				v.Fabric = f.Name
			}
			bd.vlan(&v)
		}
	}
	for _, s := range subnets {
		bd.subnet(s)
	}
	sort.SliceStable(machines, func(i, j int) bool { return machines[i].SystemID < machines[j].SystemID })
	for i := range machines {
		bd.machine(&machines[i])
	}

	if len(failures) > 0 || !complete {
		bd.b.Scope = sdk.ScopePartial
	}
	collect.Succeeded(bd.b, c.target, Kind, ver.Version, started, append(warnings, bd.warnings...))
	if len(failures) > 0 {
		bd.b.Health.OK = false
		bd.b.Health.Error = strings.Join(failures, "; ")
	}
	return emit.Emit(parent, bd.b)
}

// builder turns MAAS documents into batch objects and relations.
type builder struct {
	b        *sdk.Batch
	warnings []string
	domains  map[string]string // Options.VLANDomains
	untagged map[string]int    // Options.UntaggedVIDs, once resolved against fabrics/
}

func (bd *builder) warn(format string, args ...any) {
	bd.warnings = append(bd.warnings, fmt.Sprintf(format, args...))
}

// vlanDomain is the layer-2 domain of a fabric: the operator's mapping
// (Options.VLANDomains) when it names one, the fabric's own name otherwise.
func (bd *builder) vlanDomain(fabric string) string {
	fabric = strings.TrimSpace(fabric)
	if d := strings.TrimSpace(bd.domains[fabric]); d != "" {
		return d
	}
	return fabric
}

// vlanKey is the identity of a VLAN: its layer-2 domain (the fabric, unless
// mapped) and the VLAN ID, which is only unique inside its fabric. VID 0 is
// MAAS's "untagged" VLAN of the fabric.
func (bd *builder) vlanKey(v *vlanInfo) (domain, key string, ok bool) {
	if v == nil || strings.TrimSpace(v.Fabric) == "" {
		return "", "", false
	}
	domain = bd.vlanDomain(v.Fabric)
	return domain, collect.VLANKey(domain, bd.vid(v)), true
}

// vid is the VLAN ID v is reported under: its own, or, for the untagged VLAN
// (VID 0) of a fabric in untagged, the VID the switches use for it.
func (bd *builder) vid(v *vlanInfo) int {
	if v.VID == 0 {
		if n, ok := bd.untagged[strings.TrimSpace(v.Fabric)]; ok {
			return n
		}
	}
	return int(v.VID)
}

// vlan returns the ref of the VLAN object, creating it on first sight.
func (bd *builder) vlan(v *vlanInfo) string {
	domain, key, ok := bd.vlanKey(v)
	if !ok {
		return ""
	}
	ref := "vlan/" + key
	if _, exists := bd.b.Lookup(ref); exists {
		return ref
	}
	name := strings.TrimSpace(v.Name)
	if v.VID == 0 && bd.vid(v) != 0 && strings.EqualFold(name, "untagged") {
		name = "" // MAAS's default name, which would compete with the switches' own
	}
	bd.b.Object(ref, "vlan").
		Identify("key", key).
		Set("vid", int64(bd.vid(v))).
		Set("name", name).
		Set("domain", domain).
		Set("mtu", positive(v.MTU)).
		Set("dhcp_on", v.DHCPOn)
	return ref
}

// positive returns n as an int64, or nil when it is not a usable reading.
func positive(n num) any {
	if n <= 0 {
		return nil
	}
	return int64(n)
}

func (bd *builder) subnet(s subnet) {
	cidr := strings.TrimSpace(s.CIDR)
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		bd.warn("subnet %q: ignored, %q is not a CIDR", s.Name, s.CIDR)
		return
	}
	ref := "prefix/" + p.Masked().String()
	o := bd.b.Object(ref, "prefix").Identify("prefix", cidr)

	desc := strings.TrimSpace(s.Description)
	if name := strings.TrimSpace(s.Name); name != "" && name != cidr {
		if desc == "" {
			desc = name
		} else {
			desc = name + " - " + desc
		}
	}
	o.Set("description", desc)
	if gw := strings.TrimSpace(s.GatewayIP); validIP(gw) {
		o.Set("gateway", gw)
	}
	if s.VLAN != nil {
		if n := bd.vid(s.VLAN); n != 0 {
			o.Set("vlan", int64(n))
		}
		if vref := bd.vlan(s.VLAN); vref != "" {
			bd.b.Relate("assigned_to", ref, vref)
		}
	}
}

func validIP(s string) bool {
	_, err := netip.ParseAddr(strings.TrimSpace(s))
	return err == nil
}

// placeholders are the values firmware and MAAS report when a field has no
// real content. They must never become identifiers: two unrelated machines
// would merge on "Unknown".
var placeholders = map[string]bool{
	"unknown": true, "not specified": true, "not available": true, "not applicable": true,
	"n/a": true, "na": true, "none": true, "null": true, "default string": true,
	"to be filled by o.e.m.": true, "to be filled by oem": true, "system serial number": true,
	"system product name": true, "system manufacturer": true, "system version": true,
	"serial number": true, "0123456789": true, "123456789": true, "1234567890": true,
	"03000200-0400-0500-0006-000700080009": true, // AMI default UUID
}

// clean trims s and returns "" for a placeholder value.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || placeholders[strings.ToLower(s)] {
		return ""
	}
	// All-zero or all-F strings ("00000000", "FFFFFFFF-FFFF-...").
	rest := strings.ReplaceAll(strings.ToLower(s), "-", "")
	if rest != "" && strings.Trim(rest, rest[:1]) == "" && strings.ContainsAny(rest[:1], "0f") {
		return ""
	}
	return s
}

func (m *machine) hw(key string) string {
	v, _ := m.HardwareInfo[key].(string)
	return clean(v)
}

func (bd *builder) machine(m *machine) {
	sid := strings.TrimSpace(m.SystemID)
	if sid == "" {
		bd.warn("a machine without system_id was ignored")
		return
	}
	ref := "machine/" + sid

	dev := bd.b.Object(ref, "device").
		Identify("provisioning_id", sid).
		Identify("serial", m.hw("system_serial")).
		Identify("hw_uuid", clean(m.HardwareUUID)).
		Identify("name", strings.TrimSpace(m.Hostname))
	dev.Set("role", "server").
		Set("provisioning_status", strings.TrimSpace(m.StatusName)).
		Set("vendor", m.hw("system_vendor")).
		Set("model", m.hw("system_product")).
		Set("part_number", m.hw("system_sku")).
		Set("bios_version", m.hw("mainboard_firmware_version")).
		Set("os_name", strings.TrimSpace(m.OSystem)).
		Set("os_version", strings.TrimSpace(m.DistroSeries)).
		Set("logical_cpus", positive(m.CPUCount)).
		Set("provisioning_owner", strings.TrimSpace(m.Owner)).
		Set("provisioning_zone", strings.TrimSpace(m.Zone.Name)).
		Set("provisioning_pool", strings.TrimSpace(m.Pool.Name)).
		Set("provisioning_kernel", strings.TrimSpace(m.HWEKernel)).
		Set("provisioning_power_type", strings.TrimSpace(m.PowerType))
	if m.Memory > 0 {
		dev.Set("memory_bytes", int64(m.Memory)<<20) // MAAS reports MiB
	}
	switch ps := strings.ToLower(strings.TrimSpace(m.PowerState)); ps {
	case "on", "off":
		dev.Set("power_state", ps)
	case "":
	default: // "unknown", "error"
		dev.Set("power_state", "unknown")
	}
	if tags := nonEmpty(m.TagNames); len(tags) > 0 {
		dev.Set("provisioning_tags", tags)
	}

	ifs := m.InterfaceSet
	if bi := m.BootInterface; bi != nil && bi.Name != "" {
		found := false
		for i := range ifs {
			found = found || ifs[i].Name == bi.Name
		}
		if !found {
			ifs = append(append([]iface(nil), ifs...), *bi)
		}
	}
	for i := range ifs {
		bd.iface(ref, sid, &ifs[i])
	}
	bd.addresses(ref, sid, m, ifs)
	for i := range m.BlockDeviceSet {
		bd.disk(ref, sid, &m.BlockDeviceSet[i])
	}
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// interfaceKinds maps MAAS interface types to the schema's interface kinds.
var interfaceKinds = map[string]string{
	"physical": "physical",
	"bond":     "lag",
	"vlan":     "vlan",
	"bridge":   "virtual",
}

func (bd *builder) iface(devRef, sid string, i *iface) {
	name := strings.TrimSpace(i.Name)
	if name == "" {
		bd.warn("machine %s: an interface without a name was ignored", sid)
		return
	}
	ref := devRef + "/if/" + name
	kind, ok := interfaceKinds[strings.ToLower(i.Type)]
	if !ok {
		kind = "other" // "alias", "unknown" (discovered but unconfigured)
	}
	o := bd.b.Object(ref, "interface").In(devRef).Identify("name", name)
	o.Set("kind", kind).Set("mac", strings.TrimSpace(i.MACAddress))
	mtu := i.EffectiveMTU
	if mtu <= 0 && i.VLAN != nil {
		mtu = i.VLAN.MTU
	}
	o.Set("mtu", positive(mtu))
	if i.Enabled != nil {
		o.Set("admin_status", map[bool]string{true: "up", false: "down"}[*i.Enabled])
	}
	if kind == "physical" {
		// link_connected is only meaningful on physical ports.
		if i.LinkConnected != nil {
			o.Set("oper_status", map[bool]string{true: "up", false: "down"}[*i.LinkConnected])
		}
		o.Set("speed_mbps", positive(i.LinkSpeed))
	}

	// Aggregates and bridges: each parent is a member of this interface.
	if kind == "lag" || (kind == "virtual" && strings.EqualFold(i.Type, "bridge")) {
		for _, p := range i.Parents {
			if p = strings.TrimSpace(p); p != "" {
				bd.b.Relate("member_of", devRef+"/if/"+p, ref)
			}
		}
	}
	if vref := bd.vlan(i.VLAN); vref != "" {
		// tagged follows the MAAS VID on purpose: a mapped untagged VLAN stays untagged.
		bd.b.Relate("member_of", ref, vref).With("tagged", i.VLAN.VID != 0)
	}

	for _, l := range i.Links {
		addr := strings.TrimSpace(l.IPAddress)
		if addr == "" {
			continue // mode auto/link_up without an address yet
		}
		if !validIP(addr) {
			bd.warn("machine %s: interface %s: %q is not an IP address", sid, name, addr)
			continue
		}
		ip := bd.ip(addr, l.Subnet)
		bd.b.Relate("assigned_to", ip, ref)
	}
}

// ip returns the ref of the address object, creating it on first sight.
func (bd *builder) ip(addr string, sub *subnet) string {
	ref := "ip/" + addr
	if _, exists := bd.b.Lookup(ref); exists {
		return ref
	}
	o := bd.b.Object(ref, "ip_address").Identify("address", addr)
	if sub != nil {
		if p, err := netip.ParsePrefix(strings.TrimSpace(sub.CIDR)); err == nil {
			o.Set("prefix_length", int64(p.Bits()))
		}
	}
	return ref
}

// addresses handles what the interfaces do not carry: the primary address of
// the device, the DNS name it resolves from, and the machine addresses MAAS
// reports that sit on no known interface.
func (bd *builder) addresses(devRef, sid string, m *machine, ifs []iface) {
	onInterface := map[string]bool{}
	for i := range ifs {
		for _, l := range ifs[i].Links {
			if a := strings.TrimSpace(l.IPAddress); a != "" {
				onInterface[a] = true
			}
		}
	}

	primary := ""
	if bi := m.BootInterface; bi != nil {
		for _, l := range bi.Links {
			if a := strings.TrimSpace(l.IPAddress); validIP(a) {
				primary = a
				break
			}
		}
	}
	for _, raw := range m.IPAddresses {
		a := strings.TrimSpace(raw)
		if !validIP(a) {
			bd.warn("machine %s: %q in ip_addresses is not an IP address", sid, a)
			continue
		}
		if primary == "" {
			primary = a
		}
		if !onInterface[a] {
			bd.b.Relate("assigned_to", bd.ip(a, nil), devRef)
		}
	}
	if primary == "" {
		return
	}
	dev, _ := bd.b.Lookup(devRef)
	dev.Set("primary_ip", primary)
	if fqdn := strings.TrimSpace(m.FQDN); fqdn != "" {
		bd.b.Object("ip/"+primary, "ip_address").Identify("address", primary).Set("dns_name", fqdn)
	}
}

func (bd *builder) disk(devRef, sid string, d *blockDevice) {
	// Virtual block devices (RAID, LVM, bcache) are layers over the physical
	// drives, which are what the inventory tracks.
	if t := strings.ToLower(strings.TrimSpace(d.Type)); t != "" && t != "physical" {
		return
	}
	name := strings.TrimSpace(d.Name)
	if name == "" {
		bd.warn("machine %s: a disk without a name was ignored", sid)
		return
	}
	// A unit an HPE Smart Array presents to the system is not a drive: the
	// serial the operating system reads is the identifier the controller gave
	// the unit, so the drive behind it would be stored a second time beside
	// the one the BMC reports with its real serial (issue #128).
	if smartArrayUnit(d.Serial) {
		return
	}
	o := bd.b.Object(devRef+"/disk/"+name, "disk").In(devRef).
		Identify("name", name).
		Identify("serial", clean(d.Serial))
	o.Set("model", clean(d.Model)).
		Set("capacity_bytes", positive(d.Size)).
		Set("firmware_version", clean(d.FirmwareVersion))
	media, proto := diskMedia(name, d.Tags)
	o.Set("media", media).Set("protocol", proto)
}

// smartArrayUnit reports whether serial is the identifier an HPE Smart Array
// gives a unit it presents (logical drive, or a drive in HBA mode): a locally
// assigned NAA name (first digit 3) carrying HPE's OUI 00-14-38, 16 hex
// digits, the unit's index in the last ones. MAAS reads it where a drive's
// serial would be.
func smartArrayUnit(serial string) bool {
	s := strings.ToLower(strings.TrimSpace(serial))
	if len(s) != 16 || !strings.HasPrefix(s, "3001438") {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// diskMedia reads the technology off the tags MAAS sets at commissioning
// ("ssd", "rotary", "nvme", "sata", "sas"), or off the device name.
func diskMedia(name string, tags []string) (media, protocol string) {
	set := map[string]bool{}
	for _, t := range tags {
		set[strings.ToLower(strings.TrimSpace(t))] = true
	}
	switch {
	case set["nvme"] || strings.HasPrefix(name, "nvme"):
		return "nvme", "NVMe"
	case set["sata"]:
		protocol = "SATA"
	case set["sas"]:
		protocol = "SAS"
	}
	switch {
	case set["ssd"]:
		media = "ssd"
	case set["rotary"] || set["hdd"]:
		media = "hdd"
	}
	return media, protocol
}
