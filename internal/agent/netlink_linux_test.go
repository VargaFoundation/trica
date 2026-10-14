//go:build linux

package agent

import (
	"encoding/binary"
	"net/netip"
	"syscall"
	"testing"
)

// nlAttr encodes one rtattr (length, type, value, padded to 4 bytes).
func nlAttr(typ uint16, val []byte) []byte {
	b := make([]byte, 4, 4+len(val)+3)
	binary.NativeEndian.PutUint16(b[0:], uint16(4+len(val)))
	binary.NativeEndian.PutUint16(b[2:], typ)
	b = append(b, val...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// nlAddrMsg encodes one RTM_NEWADDR message: nlmsghdr, ifaddrmsg, attributes.
func nlAddrMsg(typ uint16, family, prefix, flags byte, index uint32, attrs ...[]byte) []byte {
	body := []byte{family, prefix, flags, 0, 0, 0, 0, 0}
	binary.NativeEndian.PutUint32(body[4:], index)
	for _, a := range attrs {
		body = append(body, a...)
	}
	hdr := make([]byte, syscall.NLMSG_HDRLEN)
	binary.NativeEndian.PutUint32(hdr[0:], uint32(len(hdr)+len(body)))
	binary.NativeEndian.PutUint16(hdr[4:], typ)
	return append(hdr, body...)
}

func u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, v)
	return b
}

func TestParseNetlinkAddrs(t *testing.T) {
	v4 := netip.MustParseAddr("192.0.2.10").AsSlice()
	peer := netip.MustParseAddr("198.51.100.1").AsSlice()
	v6 := netip.MustParseAddr("2001:db8::10").AsSlice()
	names := map[int]string{2: "eno1", 3: "wg0"}

	var raw []byte
	// 0: plain IPv4 address, the 8-bit flags field only.
	raw = append(raw, nlAddrMsg(syscall.RTM_NEWADDR, syscall.AF_INET, 24, 0x01, 2,
		nlAttr(syscall.IFA_ADDRESS, v4), nlAttr(syscall.IFA_LOCAL, v4))...)
	// 1: point-to-point: IFA_ADDRESS is the peer, IFA_LOCAL is ours.
	raw = append(raw, nlAddrMsg(syscall.RTM_NEWADDR, syscall.AF_INET, 32, 0, 3,
		nlAttr(syscall.IFA_ADDRESS, peer), nlAttr(syscall.IFA_LOCAL, netip.MustParseAddr("192.0.2.77").AsSlice()))...)
	// 2: IPv6 with IFA_FLAGS (32-bit) overriding the 8-bit field: temporary.
	raw = append(raw, nlAddrMsg(syscall.RTM_NEWADDR, syscall.AF_INET6, 64, 0, 2,
		nlAttr(syscall.IFA_ADDRESS, v6), nlAttr(ifaFlags, u32(ifaTemporary|0x100)))...)
	// 3: an interface the host does not list: dropped.
	raw = append(raw, nlAddrMsg(syscall.RTM_NEWADDR, syscall.AF_INET, 24, 0, 99, nlAttr(syscall.IFA_ADDRESS, v4))...)
	// 4: not an address message: ignored.
	raw = append(raw, nlAddrMsg(syscall.RTM_DELADDR, syscall.AF_INET, 24, 0, 2, nlAttr(syscall.IFA_ADDRESS, v4))...)
	// 5: no address attribute: dropped.
	raw = append(raw, nlAddrMsg(syscall.RTM_NEWADDR, syscall.AF_INET, 24, 0, 2)...)

	got, err := parseNetlinkAddrs(raw, names)
	if err != nil {
		t.Fatal(err)
	}
	want := []Addr{
		{Iface: "eno1", IP: netip.MustParseAddr("192.0.2.10"), PrefixLen: 24, Flags: 0x01},
		{Iface: "wg0", IP: netip.MustParseAddr("192.0.2.77"), PrefixLen: 32},
		{Iface: "eno1", IP: netip.MustParseAddr("2001:db8::10"), PrefixLen: 64, Flags: ifaTemporary | 0x100},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("address %d\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	// The IFA_FLAGS attribute is what marks the privacy address: through
	// usableAddr it must be dropped although the header flags said nothing.
	if usableAddr(got[2]) {
		t.Error("the temporary address from IFA_FLAGS was kept")
	}
}

func TestParseNetlinkAddrsRejectsTruncatedInput(t *testing.T) {
	raw := nlAddrMsg(syscall.RTM_NEWADDR, syscall.AF_INET, 24, 0, 2, nlAttr(syscall.IFA_ADDRESS, netip.MustParseAddr("192.0.2.1").AsSlice()))
	if _, err := parseNetlinkAddrs(raw[:len(raw)-6], map[int]string{2: "eno1"}); err == nil {
		t.Fatal("a truncated dump was accepted")
	}
	if got, err := parseNetlinkAddrs(nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty dump: %v %v", got, err)
	}
}

func TestNetlinkAddrsNeedsSysfs(t *testing.T) {
	// No /sys/class/net in the root: the source reports it without touching netlink.
	_, err := NetlinkAddrs{}.Addrs(t.Context(), tmpRoot(t, map[string]string{"etc/": ""}))
	if err == nil {
		t.Fatal("expected an error without /sys/class/net")
	}
}
