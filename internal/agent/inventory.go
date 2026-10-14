package agent

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
)

// VirtKind says what the host runs on.
type VirtKind string

// Virtualisation kinds, in the sense of systemd-detect-virt.
const (
	VirtNone      VirtKind = "none"
	VirtVM        VirtKind = "vm"
	VirtContainer VirtKind = "container"
)

// Virt is the result of the virtualisation probe.
type Virt struct {
	Kind VirtKind
	// Tech is the technology when known: kvm, qemu, vmware, docker, ...
	Tech string
}

// Host is the identity of the machine.
type Host struct {
	Hostname  string
	MachineID string
	// Serial and UUID come from DMI and are usually readable by root only.
	Serial      string
	UUID        string
	Vendor      string
	Model       string
	BIOSVersion string
	OSName      string
	OSVersion   string
	Kernel      string
	MemoryBytes int64
	Virt        Virt
	Sockets     []CPUSocket
	// Threads is the number of logical processors.
	Threads int
	// PrimaryIP is the first IPv4 address of the interface that carries the default route.
	PrimaryIP netip.Addr
}

// CPUSocket is one processor package.
type CPUSocket struct {
	ID      string
	Model   string
	Cores   int
	Threads int
}

// Iface is a network interface. Kind uses the schema's interface kinds.
type Iface struct {
	Name      string
	MAC       string
	Kind      string
	MTU       int
	Oper      string // up, down, unknown, not_present
	Admin     string // up, down
	SpeedMbps int
	Duplex    string
}

// Addr is an address configured on an interface.
type Addr struct {
	Iface     string
	IP        netip.Addr
	PrefixLen int
	// Flags are the IFA_F_* bits (netlink); 0 when the source has none.
	Flags uint32
}

// Listener is a listening socket.
type Listener struct {
	Proto   string // tcp or udp
	Addr    netip.Addr
	Port    uint16
	Process string
}

// Conn is what the host talks to: one outbound established connection per
// remote address (see probeSockets for how several are folded).
type Conn struct {
	Proto   string
	Remote  netip.Addr
	Port    uint16
	Process string
}

// Package is an installed package.
type Package struct {
	Name, Version, Arch string
}

// Service is a systemd service.
type Service struct {
	Name string
	// State is the active state (active, inactive, failed, ...), empty when
	// systemctl could not be asked.
	State   string
	Enabled bool
}

// Disk is a drive of a physical host, with what SMART/NVMe says when it could be asked.
type Disk struct {
	Name          string
	Model         string
	Serial        string
	WWN           string
	Firmware      string
	Protocol      string
	Media         string // ssd, nvme, hdd, unknown
	CapacityBytes int64
	Smart         *Smart
}

// Smart is the health and wear of a drive. Pointers: a reading that the drive
// does not report is absent, not zero.
type Smart struct {
	Health             string // ok, warning, critical
	WearPct            *float64
	SparePct           *float64
	PowerOnHours       *int64
	ReallocatedSectors *int64
	PendingSectors     *int64
	MediaErrors        *int64
}

// Neighbor is an LLDP neighbour seen on a local interface.
type Neighbor struct {
	Local         string
	Protocol      string // lldp, cdp, ...
	ChassisIDType string
	ChassisID     string
	SysName       string
	PortIDType    string
	PortID        string
	PortDescr     string
}

// Inventory is everything the probes found on one run.
type Inventory struct {
	Host       Host
	Interfaces []Iface
	Addrs      []Addr
	Listeners  []Listener
	Conns      []Conn
	Packages   []Package
	Services   []Service
	Disks      []Disk
	Neighbors  []Neighbor

	// Skipped lists probes that could not look (not root, tool absent): reduced
	// coverage, not a failure. Failed lists probes that broke.
	Skipped []string
	Failed  []string
}

// skipError marks a probe that could not look: the host lacks the facility or
// the agent lacks the privilege.
type skipError struct{ msg string }

func (e skipError) Error() string { return e.msg }

func skipf(format string, args ...any) error { return skipError{fmt.Sprintf(format, args...)} }

// collector carries what the probes share.
type collector struct {
	ctx   context.Context
	fs    FS
	run   Runner
	addrs AddrSource
	inv   *Inventory
}

type probe struct {
	name string
	fn   func(*collector) error
}

// probes run in this order: later ones use what earlier ones found.
var probes = []probe{
	{"host", (*collector).probeHost},
	{"os", (*collector).probeOS},
	{"cpu", (*collector).probeCPU},
	{"memory", (*collector).probeMemory},
	{"virt", (*collector).probeVirt},
	{"interfaces", (*collector).probeInterfaces},
	{"addresses", (*collector).probeAddrs},
	{"sockets", (*collector).probeSockets},
	{"packages", (*collector).probePackages},
	{"services", (*collector).probeServices},
	{"disks", (*collector).probeDisks},
	{"lldp", (*collector).probeLLDP},
}

// safely runs a probe and turns a panic into an error: a surprising file
// under /proc must cost one probe, not the agent.
func safely(fn func(*collector) error, c *collector) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(c)
}

// observe runs every probe over fsys and returns what it found. A probe that
// fails or cannot look is recorded, and the next one still runs.
func observe(ctx context.Context, fsys FS, run Runner, addrs AddrSource) *Inventory {
	c := &collector{ctx: ctx, fs: fsys, run: run, addrs: addrs, inv: &Inventory{}}
	for _, p := range probes {
		if ctx.Err() != nil {
			c.inv.Failed = append(c.inv.Failed, "run: "+ctx.Err().Error())
			break
		}
		err := safely(p.fn, c)
		var skip skipError
		switch {
		case err == nil:
		case errors.As(err, &skip):
			c.inv.Skipped = append(c.inv.Skipped, p.name+": "+skip.msg)
		default:
			c.inv.Failed = append(c.inv.Failed, p.name+": "+err.Error())
		}
	}
	return c.inv
}
