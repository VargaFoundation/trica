package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const sysNet = "/sys/class/net/"

// Address flags (IFA_F_*) that make an address not worth an inventory entry:
// IPv6 privacy (temporary) addresses rotate, deprecated and tentative ones are
// on their way out or not yet usable, and a failed DAD address is not usable.
const (
	ifaTemporary  = 0x01 // IPv6 temporary; the same bit is "secondary" for IPv4
	ifaDADFailed  = 0x08
	ifaDeprecated = 0x20
	ifaTentative  = 0x40
	ifaSecondary  = 0x01
)

// ephemeralIface matches interfaces that containers, orchestrators and
// hypervisors create and destroy at will: they would open and close objects
// at every churn, and their addresses are not unique across hosts (every
// Docker host has a 172.17.0.1). Their traffic is still seen through the
// sockets of the host.
var ephemeralIface = regexp.MustCompile(`^(veth|cali|lxc|cilium_|flannel|cni|docker0$|kube-|virbr|vnet|tap)|^br-[0-9a-f]{12}$`)

// AddrSource lists the addresses configured on the host's interfaces.
type AddrSource interface {
	Addrs(ctx context.Context, fsys FS) ([]Addr, error)
}

// ifaceNames lists the entries of /sys/class/net.
func ifaceNames(fsys FS) ([]string, error) {
	ents, err := fsys.ReadDir(sysNet)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}

func parseInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 0, 64)
	return n, err == nil
}

// ifaceKind works out the schema kind of an interface from sysfs.
func ifaceKind(fsys FS, name, devtype string) string {
	dir := sysNet + name + "/"
	switch {
	case exists(fsys, dir+"bonding"):
		return "lag"
	case exists(fsys, dir+"bridge"):
		return "virtual"
	}
	switch devtype {
	case "bridge":
		return "virtual"
	case "vlan":
		return "vlan"
	case "bond":
		return "lag"
	case "wlan":
		return "physical"
	case "gre", "gretap", "ipip", "sit", "wireguard", "vxlan", "geneve", "ip6gre", "ip6tnl":
		return "tunnel"
	case "":
	default:
		return "virtual"
	}
	if t, ok := parseInt(readOptional(fsys, dir+"type")); ok {
		switch t {
		case 772:
			return "loopback"
		case 768, 769, 776, 778, 823:
			return "tunnel"
		}
	}
	if exists(fsys, dir+"device") {
		return "physical"
	}
	return "virtual"
}

func ueventValue(fsys FS, name, key string) string {
	b, err := readFile(fsys, sysNet+name+"/uevent")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func validMAC(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if _, err := parseMAC(s); err != nil {
		return ""
	}
	return s
}

// parseMAC accepts the colon-separated 6-byte form sysfs uses, rejecting the
// all-zero address that interfaces without hardware report.
func parseMAC(s string) ([6]byte, error) {
	var out [6]byte
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return out, fmt.Errorf("bad MAC %q", s)
	}
	zero := true
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 16, 8)
		if err != nil || len(p) != 2 {
			return out, fmt.Errorf("bad MAC %q", s)
		}
		out[i] = byte(n)
		if n != 0 {
			zero = false
		}
	}
	if zero {
		return out, fmt.Errorf("empty MAC")
	}
	return out, nil
}

// probeInterfaces reads /sys/class/net. Loopback and the interfaces that
// containers create (see ephemeralIface) are left out.
func (c *collector) probeInterfaces() error {
	names, err := ifaceNames(c.fs)
	if err != nil {
		if skippable(err) {
			return skipf("/sys/class/net not readable")
		}
		return err
	}
	for _, n := range names {
		if n == "lo" || ephemeralIface.MatchString(n) {
			continue
		}
		dir := sysNet + n + "/"
		i := Iface{Name: n, MAC: validMAC(readOptional(c.fs, dir+"address"))}
		i.Kind = ifaceKind(c.fs, n, ueventValue(c.fs, n, "DEVTYPE"))
		if i.Kind == "loopback" {
			continue
		}
		if v, ok := parseInt(readOptional(c.fs, dir+"mtu")); ok && v > 0 {
			i.MTU = int(v)
		}
		switch readOptional(c.fs, dir+"operstate") {
		case "up":
			i.Oper = "up"
		case "down", "lowerlayerdown":
			i.Oper = "down"
		case "notpresent":
			i.Oper = "not_present"
		default:
			i.Oper = "unknown"
		}
		if f, ok := parseInt(readOptional(c.fs, dir+"flags")); ok {
			i.Admin = "down"
			if f&0x1 != 0 { // IFF_UP
				i.Admin = "up"
			}
		}
		// speed is -1 (or an unreadable file) when the link is down or virtual.
		if v, ok := parseInt(readOptional(c.fs, dir+"speed")); ok && v > 0 {
			i.SpeedMbps = int(v)
		}
		if d := readOptional(c.fs, dir+"duplex"); d == "full" || d == "half" {
			i.Duplex = d
		}
		c.inv.Interfaces = append(c.inv.Interfaces, i)
	}
	return nil
}

// usableAddr reports whether an address belongs in the inventory.
func usableAddr(a Addr) bool {
	ip := a.IP
	if !ip.IsValid() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.Is6() && a.Flags&(ifaTemporary|ifaDADFailed|ifaDeprecated|ifaTentative) != 0 {
		return false
	}
	return true
}

// probeAddrs asks the AddrSource and keeps the usable addresses of the
// interfaces the host keeps. It also finds the primary IP.
func (c *collector) probeAddrs() error {
	if c.addrs == nil {
		return skipf("no address source")
	}
	all, err := c.addrs.Addrs(c.ctx, c.fs)
	if err != nil {
		if skippable(err) {
			return skipf("addresses: %v", err)
		}
		return err
	}
	known := map[string]bool{}
	for _, i := range c.inv.Interfaces {
		known[i.Name] = true
	}
	for _, a := range all {
		if known[a.Iface] && usableAddr(a) {
			a.IP = a.IP.Unmap()
			c.inv.Addrs = append(c.inv.Addrs, a)
		}
	}
	sort.Slice(c.inv.Addrs, func(i, j int) bool {
		a, b := c.inv.Addrs[i], c.inv.Addrs[j]
		if a.Iface != b.Iface {
			return a.Iface < b.Iface
		}
		return a.IP.Compare(b.IP) < 0
	})
	c.inv.Host.PrimaryIP = c.primaryIP()
	return nil
}

// primaryIP is the first non-secondary IPv4 address of the interface that
// carries the default route (lowest metric, then name). Without a default
// route there is no primary IP: guessing one would be inventing data.
func (c *collector) primaryIP() netip.Addr {
	r, err := c.fs.Open("/proc/net/route")
	if err != nil {
		return netip.Addr{}
	}
	defer r.Close()
	best, bestMetric := "", int64(-1)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 8 || f[0] == "Iface" || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, ok := parseInt("0x" + f[3])
		if !ok || flags&0x1 == 0 { // RTF_UP
			continue
		}
		metric, ok := parseInt(f[6])
		if !ok {
			continue
		}
		if bestMetric < 0 || metric < bestMetric || (metric == bestMetric && f[0] < best) {
			best, bestMetric = f[0], metric
		}
	}
	if best == "" {
		return netip.Addr{}
	}
	for _, a := range c.inv.Addrs {
		if a.Iface == best && a.IP.Is4() && a.Flags&ifaSecondary == 0 {
			return a.IP
		}
	}
	return netip.Addr{}
}

// fsAddrs reads addresses from a text file inside the observed root, one
// "interface address/prefix [flags]" per line, the shape of `ip -o addr`
// reduced to what the agent needs. It is the AddrSource for a root other than
// "/": netlink would describe the machine the agent runs on, not the tree.
type fsAddrs struct{ path string }

// Addrs implements AddrSource.
func (f fsAddrs) Addrs(_ context.Context, fsys FS) ([]Addr, error) {
	r, err := fsys.Open(f.path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return parseAddrList(r)
}

func parseAddrList(r io.Reader) ([]Addr, error) {
	var out []Addr
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return nil, fmt.Errorf("bad address line %q", line)
		}
		p, err := netip.ParsePrefix(f[1])
		if err != nil {
			return nil, fmt.Errorf("bad address %q: %w", f[1], err)
		}
		a := Addr{Iface: f[0], IP: p.Addr(), PrefixLen: p.Bits()}
		if len(f) > 2 {
			fl, err := strconv.ParseUint(f[2], 0, 32)
			if err != nil {
				return nil, fmt.Errorf("bad flags %q: %w", f[2], err)
			}
			a.Flags = uint32(fl)
		}
		out = append(out, a)
	}
	return out, sc.Err()
}
