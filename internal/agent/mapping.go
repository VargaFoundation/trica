package agent

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// SourcePrefix starts the source name of every batch the agent emits:
// "agent:<hostname>". The engine owns claims per source, so a host's claims
// are the ones its own agent makes and nobody else's.
const SourcePrefix = "agent:"

// CollectorName is the Health.Collector of the agent.
const CollectorName = "agent"

// ErrNoIdentity means the probes found nothing to identify the host by: no
// batch is built, since an object nobody can recognise would be created anew
// at every run.
var ErrNoIdentity = errors.New("no host identity (hostname, machine-id, serial or uuid)")

func (inv *Inventory) hasIdentity() bool {
	h := inv.Host
	return h.Hostname != "" || h.MachineID != "" || h.Serial != "" || h.UUID != ""
}

// Source is the batch source of this host.
func (inv *Inventory) Source() string { return SourcePrefix + inv.Host.Hostname }

// Batch maps the inventory to one full-scope sdk.Batch.
//
// What is mapped to what:
//
//   - the host: a device (a vm when the probes found a hypervisor), identified
//     by serial, hardware UUID, machine-id and hostname, the first two strong and
//     the other two weak (a cloned VM image shares its machine-id);
//   - interfaces, CPU sockets, disks, services, listeners: children of the host
//     (the schema has no relation for containment, the parent link is it);
//   - ip_address objects assigned_to the interface that carries them;
//   - the OS and the installed packages: software objects the host runs;
//   - outbound connections: talks_to relations from the host to the remote
//     ip_address, which is the only thing the host knows about its peer;
//   - LLDP neighbours: neighbor_of from the local interface to the peer's
//     interface, found by chassis MAC and port name like the network collectors
//     identify it from the other side.
//
// Health carries the probes that failed (Error, OK=false: the engine then
// withdraws nothing, so a probe that broke for one run does not close the
// claims it would have renewed) and the ones that could not look (Warnings).
func (inv *Inventory) Batch(now time.Time, version string, took time.Duration) (*sdk.Batch, error) {
	if !inv.hasIdentity() {
		return nil, ErrNoIdentity
	}
	h := inv.Host
	b := sdk.NewBatch(inv.Source(), sdk.ScopeFull)
	b.ObservedAt = now.UTC()

	vm := h.Virt.Kind == VirtVM
	var host *sdk.Object
	if vm {
		host = b.Object("host", "vm").Identify("uuid", h.UUID)
	} else {
		host = b.Object("host", "device").Identify("serial", h.Serial).Identify("hw_uuid", h.UUID)
	}
	host.Identify("machine_id", h.MachineID).Identify("name", h.Hostname)
	host.Set("memory_bytes", nonZero(h.MemoryBytes)).Set("os_name", h.OSName).Set("kernel", h.Kernel)
	if h.PrimaryIP.IsValid() {
		host.Set("primary_ip", h.PrimaryIP.String())
	}
	if vm {
		host.Set("power_state", "running").Set("vcpus", nonZero(int64(h.Threads)))
	} else {
		host.Set("role", "server").Set("vendor", h.Vendor).Set("model", h.Model).
			Set("os_version", h.OSVersion).Set("bios_version", h.BIOSVersion).
			Set("cpu_count", nonZero(int64(len(h.Sockets))))
		for _, s := range h.Sockets {
			b.Object("cpu:"+s.ID, "cpu").Identify("name", "cpu"+s.ID).In("host").
				Set("model", s.Model).Set("cores", nonZero(int64(s.Cores))).Set("threads", nonZero(int64(s.Threads)))
		}
	}

	if h.OSName != "" {
		key := "os/" + h.OSName + "/" + h.OSVersion
		b.Object("sw:"+key, "software").Identify("key", key).
			Set("kind", "os").Set("name", h.OSName).Set("version", h.OSVersion)
		b.Relate("runs", "host", "sw:"+key)
	}
	for _, p := range uniquePackageKeys(inv.Packages) {
		b.Object("sw:"+p.key, "software").Identify("key", p.key).
			Set("kind", "package").Set("name", p.Name).Set("version", p.Version)
		b.Relate("runs", "host", "sw:"+p.key)
	}

	for _, i := range inv.Interfaces {
		o := b.Object("if:"+i.Name, "interface").Identify("name", i.Name).In("host").
			Set("kind", i.Kind).Set("mac", i.MAC).Set("oper_status", i.Oper).Set("admin_status", i.Admin).Set("duplex", i.Duplex)
		o.Set("mtu", nonZero(int64(i.MTU))).Set("speed_mbps", nonZero(int64(i.SpeedMbps)))
	}
	for _, a := range inv.Addrs {
		ref := "ip:" + a.IP.String()
		b.Object(ref, "ip_address").Identify("address", a.IP.String()).Set("prefix_length", int64(a.PrefixLen))
		b.Relate("assigned_to", ref, "if:"+a.Iface)
	}

	for _, s := range inv.Services {
		o := b.Object("svc:"+s.Name, "service").Identify("name", s.Name).In("host").Set("state", s.State)
		if s.Enabled { // false is not a claim: a unit can be enabled by a vendor preset this probe does not read
			o.Set("enabled", true)
		}
	}
	for _, l := range inv.Listeners {
		key := listenerKey(l)
		b.Object("lst:"+key, "listener").Identify("key", key).In("host").
			Set("protocol", l.Proto).Set("address", l.Addr.String()).Set("port", int64(l.Port)).Set("process", l.Process)
	}
	for _, c := range inv.Conns {
		// A remote address is never one of the host's own (the probe drops those), so its ref is free.
		ref := "rip:" + c.Remote.String()
		b.Object(ref, "ip_address").Identify("address", c.Remote.String())
		b.Relate("talks_to", "host", ref).With("port", int64(c.Port)).With("protocol", c.Proto).With("process", nilIfEmpty(c.Process))
	}

	var notes []string
	if !vm {
		for _, d := range inv.Disks {
			o := b.Object("disk:"+d.Name, "disk").Identify("name", d.Name).Identify("serial", d.Serial).Identify("wwn", d.WWN).In("host").
				Set("model", d.Model).Set("media", d.Media).Set("protocol", d.Protocol).
				Set("capacity_bytes", nonZero(d.CapacityBytes)).Set("firmware_version", d.Firmware)
			if s := d.Smart; s != nil {
				o.Set("health", s.Health).Set("wear_pct", ptr(s.WearPct)).Set("spare_pct", ptr(s.SparePct)).
					Set("power_on_hours", ptr(s.PowerOnHours)).Set("reallocated_sectors", ptr(s.ReallocatedSectors)).
					Set("pending_sectors", ptr(s.PendingSectors)).Set("media_errors", ptr(s.MediaErrors))
			}
		}
	} else if len(inv.Disks) > 0 {
		notes = append(notes, "disks: virtual machine, its disks are not reported (the schema's disk type has no vm parent)")
	}
	if h.Virt.Kind == VirtContainer {
		notes = append(notes, "virt: running in a "+h.Virt.Tech+" container, reported as a device")
	}

	for _, n := range inv.Neighbors {
		if _, ok := b.Lookup("if:" + n.Local); !ok {
			notes = append(notes, fmt.Sprintf("lldp: neighbour on %s, which is not a reported interface, ignored", n.Local))
			continue
		}
		peer := sdk.Endpoint{Type: "device", Identity: map[string]string{}}
		if n.ChassisIDType == "mac" {
			peer.Identity["base_mac"] = n.ChassisID
		}
		if n.SysName != "" {
			peer.Identity["name"] = n.SysName
		}
		b.RelateEndpoints("neighbor_of", sdk.Ref("if:"+n.Local), sdk.Endpoint{
			Type: "interface", Identity: map[string]string{"name": n.peerPort()}, Parent: &peer,
		}).With("protocol", n.Protocol)
	}

	hl := &sdk.Health{
		Target:    h.Hostname,
		OK:        len(inv.Failed) == 0,
		Error:     strings.Join(inv.Failed, "; "),
		Duration:  took,
		Items:     len(b.Objects),
		Collector: CollectorName,
		Version:   version,
	}
	for _, f := range inv.Failed {
		hl.Warnings = append(hl.Warnings, "probe failed: "+f)
	}
	hl.Warnings = append(hl.Warnings, inv.Skipped...)
	hl.Warnings = append(hl.Warnings, notes...)
	b.Health = hl
	return b, nil
}

func listenerKey(l Listener) string {
	return l.Proto + "/" + netip.AddrPortFrom(l.Addr, l.Port).String()
}

type keyedPackage struct {
	Package
	key string
}

// uniquePackageKeys gives each installed package its software key,
// "package/<name>/<version>". Two architectures of one version (libc6 amd64 and
// i386) would collide: the first in architecture order keeps the plain key,
// the others get ":<arch>".
func uniquePackageKeys(pkgs []Package) []keyedPackage {
	out := make([]keyedPackage, 0, len(pkgs))
	seen := make(map[string]bool, len(pkgs))
	seenArch := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		key := "package/" + p.Name + "/" + p.Version
		if seenArch[key+":"+p.Arch] {
			continue // same name, version and arch twice: one package
		}
		seenArch[key+":"+p.Arch] = true
		if seen[key] {
			key += ":" + p.Arch
		}
		seen[key] = true
		out = append(out, keyedPackage{p, key})
	}
	return out
}

// nonZero turns a zero reading into "no reading": the probes use zero for
// "not found", and Set ignores nil.
func nonZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ptr dereferences a reading that may be absent: a nil pointer inside an
// interface would defeat Set's nil check.
func ptr[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
