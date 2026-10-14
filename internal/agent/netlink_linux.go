//go:build linux

package agent

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"syscall"
)

// ifaFlags is IFA_FLAGS, the 32-bit flags attribute that supersedes the 8-bit
// field of the address header (not exported by package syscall).
const ifaFlags = 8

// NetlinkAddrs reads the addresses with one RTM_GETADDR dump on a netlink
// socket (package syscall, so a CGO_ENABLED=0 build keeps working and no
// dependency is added). Netlink, not /proc/net/fib_trie, because fib_trie
// does not say which interface owns an address, and not `ip addr`, because
// the agent runs no command it does not need. The request is a read-only dump.
type NetlinkAddrs struct{}

// Addrs implements AddrSource. Interface names come from /sys/class/net/*/ifindex.
func (NetlinkAddrs) Addrs(_ context.Context, fsys FS) ([]Addr, error) {
	names, err := ifaceNames(fsys)
	if err != nil {
		return nil, err
	}
	byIndex := map[int]string{}
	for _, n := range names {
		if i, ok := parseInt(readOptional(fsys, sysNet+n+"/ifindex")); ok {
			byIndex[int(i)] = n
		}
	}
	raw, err := syscall.NetlinkRIB(syscall.RTM_GETADDR, syscall.AF_UNSPEC)
	if err != nil {
		return nil, fmt.Errorf("netlink address dump: %w", err)
	}
	return parseNetlinkAddrs(raw, byIndex)
}

// parseNetlinkAddrs decodes the RTM_NEWADDR messages of a dump.
func parseNetlinkAddrs(raw []byte, byIndex map[int]string) ([]Addr, error) {
	msgs, err := syscall.ParseNetlinkMessage(raw)
	if err != nil {
		return nil, fmt.Errorf("netlink parse: %w", err)
	}
	var out []Addr
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWADDR || len(m.Data) < syscall.SizeofIfAddrmsg {
			continue
		}
		// struct ifaddrmsg: family, prefixlen, flags, scope (1 byte each), index (u32).
		attrs, err := syscall.ParseNetlinkRouteAttr(&m)
		if err != nil {
			return nil, fmt.Errorf("netlink attributes: %w", err)
		}
		a := Addr{PrefixLen: int(m.Data[1]), Flags: uint32(m.Data[2])}
		a.Iface = byIndex[int(binary.NativeEndian.Uint32(m.Data[4:8]))]
		var local, address []byte
		for _, at := range attrs {
			switch at.Attr.Type {
			case syscall.IFA_LOCAL:
				local = at.Value
			case syscall.IFA_ADDRESS:
				address = at.Value
			case ifaFlags:
				if len(at.Value) == 4 {
					a.Flags = binary.NativeEndian.Uint32(at.Value)
				}
			}
		}
		// On a point-to-point link IFA_ADDRESS is the peer and IFA_LOCAL is ours.
		src := local
		if src == nil {
			src = address
		}
		ip, ok := netip.AddrFromSlice(src)
		if !ok || a.Iface == "" {
			continue
		}
		a.IP = ip
		out = append(out, a)
	}
	return out, nil
}
