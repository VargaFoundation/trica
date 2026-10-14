package agent

import (
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestProbeInterfacesDebian(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "debian12"), nil, nil, "interfaces")
	if err != nil {
		t.Fatal(err)
	}
	want := []Iface{
		{Name: "bond0", MAC: "02:00:00:00:01:01", Kind: "lag", MTU: 9000, Oper: "up", Admin: "up", SpeedMbps: 20000, Duplex: "full"},
		{Name: "bond0.100", MAC: "02:00:00:00:01:01", Kind: "vlan", MTU: 9000, Oper: "up", Admin: "up"},
		{Name: "eno1", MAC: "02:00:00:00:01:01", Kind: "physical", MTU: 9000, Oper: "up", Admin: "up", SpeedMbps: 10000, Duplex: "full"},
		{Name: "eno2", MAC: "02:00:00:00:01:02", Kind: "physical", MTU: 9000, Oper: "up", Admin: "up", SpeedMbps: 10000, Duplex: "full"},
		{Name: "eno3", MAC: "02:00:00:00:01:03", Kind: "physical", MTU: 1500, Oper: "down", Admin: "down"},
	}
	if !reflect.DeepEqual(inv.Interfaces, want) {
		t.Fatalf("interfaces\n got %+v\nwant %+v", inv.Interfaces, want)
	}
}

func TestEphemeralInterfacesAreNotInventoried(t *testing.T) {
	// veth, docker0 and loopback exist in the fixture: none must become an object.
	inv, _ := probeAfter(t, openRoot(t, "debian12"), nil, nil, "interfaces")
	for _, i := range inv.Interfaces {
		if i.Name == "lo" || i.Name == "docker0" || strings.HasPrefix(i.Name, "veth") {
			t.Fatalf("%s was inventoried", i.Name)
		}
	}
}

func TestEphemeralIfaceNames(t *testing.T) {
	tests := map[string]bool{
		"veth1a2b3c": true, "cali0123abcd": true, "lxcbr0": true, "lxc1234": true, "cilium_host": true,
		"cilium_vxlan": true, "flannel.1": true, "cni0": true, "docker0": true, "kube-ipvs0": true,
		"virbr0": true, "vnet12": true, "tap0": true, "br-0123456789ab": true,
		"eno1": false, "bond0": false, "bond0.100": false, "br0": false, "br-lan": false, "docker0.100": false,
		"ens3": false, "eth0": false, "wlp3s0": false,
	}
	for n, want := range tests {
		if got := ephemeralIface.MatchString(n); got != want {
			t.Errorf("ephemeralIface(%q) = %v, want %v", n, got, want)
		}
	}
}

func TestIfaceKinds(t *testing.T) {
	// Kinds an inventory cares about, each driven by what sysfs says.
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"bonding dir", map[string]string{"sys/class/net/x/bonding/mode": "x"}, "lag"},
		{"bridge dir", map[string]string{"sys/class/net/x/bridge/stp_state": "0"}, "virtual"},
		{"vlan devtype", map[string]string{"sys/class/net/x/uevent": "DEVTYPE=vlan\n"}, "vlan"},
		{"bond devtype", map[string]string{"sys/class/net/x/uevent": "DEVTYPE=bond\n"}, "lag"},
		{"wlan", map[string]string{"sys/class/net/x/uevent": "DEVTYPE=wlan\n", "sys/class/net/x/address": "02:00:00:00:00:05"}, "physical"},
		{"wireguard", map[string]string{"sys/class/net/x/uevent": "DEVTYPE=wireguard\n"}, "tunnel"},
		{"unknown devtype", map[string]string{"sys/class/net/x/uevent": "DEVTYPE=macvlan\n"}, "virtual"},
		{"gre arphrd type", map[string]string{"sys/class/net/x/type": "778"}, "tunnel"},
		{"sit arphrd type", map[string]string{"sys/class/net/x/type": "776"}, "tunnel"},
		{"loopback type", map[string]string{"sys/class/net/x/type": "772"}, "loopback"},
		{"pci device", map[string]string{"sys/class/net/x/type": "1", "sys/class/net/x/device/vendor": "0x1"}, "physical"},
		{"no device link", map[string]string{"sys/class/net/x/type": "1"}, "virtual"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys := tmpRoot(t, tc.files)
			if got := ifaceKind(fsys, "x", ueventValue(fsys, "x", "DEVTYPE")); got != tc.want {
				t.Fatalf("kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProbeInterfacesLoopbackByTypeIsDropped(t *testing.T) {
	// A loopback not named "lo" is still a loopback.
	r := tmpRoot(t, map[string]string{
		"sys/class/net/lo0/type": "772", "sys/class/net/eth0/type": "1", "sys/class/net/eth0/device/vendor": "0x1",
	})
	inv, err := probeAfter(t, r, nil, nil, "interfaces")
	if err != nil || len(inv.Interfaces) != 1 || inv.Interfaces[0].Name != "eth0" {
		t.Fatalf("%+v %v", inv.Interfaces, err)
	}
}

func TestProbeInterfacesOperState(t *testing.T) {
	tests := map[string]string{"up": "up", "down": "down", "lowerlayerdown": "down", "notpresent": "not_present", "dormant": "unknown", "unknown": "unknown", "": "unknown"}
	for in, want := range tests {
		r := tmpRoot(t, map[string]string{"sys/class/net/eth0/operstate": in, "sys/class/net/eth0/speed": "-1", "sys/class/net/eth0/duplex": "unknown"})
		inv, err := probeAfter(t, r, nil, nil, "interfaces")
		if err != nil || len(inv.Interfaces) != 1 {
			t.Fatalf("%q: %+v %v", in, inv.Interfaces, err)
		}
		if i := inv.Interfaces[0]; i.Oper != want || i.SpeedMbps != 0 || i.Duplex != "" {
			t.Errorf("operstate %q -> %+v, want oper %q, no speed, no duplex", in, i, want)
		}
	}
}

func TestProbeInterfacesNoSysfs(t *testing.T) {
	inv := observe(t.Context(), tmpRoot(t, map[string]string{"etc/hostname": "h\n"}), noRunner{}, fsAddrs{path: "/addrs.txt"})
	if !containsPrefix(inv.Skipped, "interfaces: ") {
		t.Fatalf("skipped %v failed %v", inv.Skipped, inv.Failed)
	}
}

func TestMAC(t *testing.T) {
	tests := [][2]string{
		{"02:00:00:00:01:01", "02:00:00:00:01:01"},
		{" 02:00:00:00:AA:01", "02:00:00:00:aa:01"},
		{"00:00:00:00:00:00", ""},
		{"02:00:00:00:01", ""},
		{"02:00:00:00:01:0g", ""},
		{"2:0:0:0:1:1", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := validMAC(tc[0]); got != tc[1] {
			t.Errorf("validMAC(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestUsableAddr(t *testing.T) {
	ip := netip.MustParseAddr
	tests := []struct {
		name string
		a    Addr
		want bool
	}{
		{"global v4", Addr{IP: ip("192.0.2.10")}, true},
		{"global v6", Addr{IP: ip("2001:db8::10")}, true},
		{"loopback v4", Addr{IP: ip("127.0.0.1")}, false},
		{"loopback v6", Addr{IP: ip("::1")}, false},
		{"link-local v4", Addr{IP: ip("169.254.1.2")}, false},
		{"link-local v6", Addr{IP: ip("fe80::1")}, false},
		{"multicast", Addr{IP: ip("224.0.0.1")}, false},
		{"unspecified", Addr{IP: ip("0.0.0.0")}, false},
		{"invalid", Addr{}, false},
		{"v6 temporary", Addr{IP: ip("2001:db8::dead:beef"), Flags: ifaTemporary}, false},
		{"v6 dad failed", Addr{IP: ip("2001:db8::2"), Flags: ifaDADFailed}, false},
		{"v6 deprecated", Addr{IP: ip("2001:db8::3"), Flags: ifaDeprecated}, false},
		{"v6 tentative", Addr{IP: ip("2001:db8::4"), Flags: ifaTentative}, false},
		{"v6 permanent flag only", Addr{IP: ip("2001:db8::5"), Flags: 0x80}, true},
		{"v4 secondary is kept", Addr{IP: ip("192.0.2.11"), Flags: ifaSecondary}, true},
		{"v4 deprecated flag is not a v6 rule", Addr{IP: ip("192.0.2.12"), Flags: ifaDeprecated}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := usableAddr(tc.a); got != tc.want {
				t.Fatalf("usableAddr = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProbeAddrsDebian(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "debian12"), nil, nil, "interfaces", "addresses")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range inv.Addrs {
		got = append(got, fmt.Sprintf("%s %s/%d", a.Iface, a.IP, a.PrefixLen))
	}
	want := []string{
		"bond0 192.0.2.10/24", "bond0 2001:db8::10/64", "bond0.100 198.51.100.10/24", "eno3 192.0.2.99/24",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addresses\n got %v\nwant %v", got, want)
	}
	// Dropped on purpose: lo (loopback), the fe80 link-local, the temporary
	// 2001:db8::dead:beef, and everything on docker0/veth (interfaces not kept).
	if inv.Host.PrimaryIP != netip.MustParseAddr("192.0.2.10") {
		t.Fatalf("primary ip %v: it is the address of the default route's interface, not of the lowest-metric subnet route", inv.Host.PrimaryIP)
	}
}

func TestProbeAddrsRHEL(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "rhel9"), nil, nil, "interfaces", "addresses")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Addrs) != 2 || inv.Host.PrimaryIP != netip.MustParseAddr("198.51.100.30") {
		t.Fatalf("%+v primary %v", inv.Addrs, inv.Host.PrimaryIP)
	}
}

func TestAddressesOfIgnoredInterfacesAreDropped(t *testing.T) {
	inv, _ := probeAfter(t, openRoot(t, "debian12"), nil, nil, "interfaces", "addresses")
	for _, a := range inv.Addrs {
		if a.Iface == "docker0" || a.Iface == "lo" || strings.HasPrefix(a.Iface, "veth") {
			t.Fatalf("address %v of %s kept: it is not unique to this host", a.IP, a.Iface)
		}
	}
}

func TestPrimaryIP(t *testing.T) {
	const hdr = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
	route := func(iface, dest, flags, metric, mask string) string {
		return fmt.Sprintf("%s\t%s\t010200C0\t%s\t0\t0\t%s\t%s\t0\t0\t0\n", iface, dest, flags, metric, mask)
	}
	addrs := "eth0 192.0.2.5/24 1\neth0 192.0.2.6/24\neth0 2001:db8::5/64\neth1 198.51.100.5/24\n"
	tests := []struct {
		name  string
		route string
		want  string
	}{
		{"default route picks its interface's first non-secondary v4", hdr + route("eth0", "00000000", "0003", "100", "00000000"), "192.0.2.6"},
		{"lowest metric wins", hdr + route("eth0", "00000000", "0003", "200", "00000000") + route("eth1", "00000000", "0003", "100", "00000000"), "198.51.100.5"},
		{"equal metric: name order", hdr + route("eth1", "00000000", "0003", "100", "00000000") + route("eth0", "00000000", "0003", "100", "00000000"), "192.0.2.6"},
		{"subnet route is not a default route", hdr + route("eth0", "006433C6", "0001", "0", "00FFFFFF"), ""},
		{"route that is down is ignored", hdr + route("eth0", "00000000", "0002", "100", "00000000"), ""},
		{"no routes", hdr, ""},
		{"no route file", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"addrs.txt":               addrs,
				"sys/class/net/eth0/type": "1",
				"sys/class/net/eth1/type": "1",
			}
			if tc.route != "" {
				files["proc/net/route"] = tc.route
			}
			inv, err := probeAfter(t, tmpRoot(t, files), nil, nil, "interfaces", "addresses")
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if inv.Host.PrimaryIP.IsValid() {
				got = inv.Host.PrimaryIP.String()
			}
			if got != tc.want {
				t.Fatalf("primary ip %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseAddrList(t *testing.T) {
	good, err := parseAddrList(strings.NewReader("# c\n\neth0 192.0.2.1/24\neth0 2001:db8::1/64 0x41\nlo ::ffff:192.0.2.9/128 33\n"))
	if err != nil || len(good) != 3 {
		t.Fatalf("%+v %v", good, err)
	}
	if good[1].Flags != 0x41 || good[1].PrefixLen != 64 || good[2].Flags != 33 {
		t.Fatalf("%+v", good)
	}
	for _, bad := range []string{"eth0\n", "eth0 not-an-address\n", "eth0 192.0.2.1\n", "eth0 192.0.2.1/24 zz\n"} {
		if _, err := parseAddrList(strings.NewReader(bad)); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAddrsMissingSourceFile(t *testing.T) {
	inv := observe(t.Context(), tmpRoot(t, map[string]string{"etc/hostname": "h\n", "sys/class/net/eth0/type": "1\n"}), noRunner{}, fsAddrs{path: "/addrs.txt"})
	if !containsPrefix(inv.Skipped, "addresses: ") || len(inv.Failed) != 0 {
		t.Fatalf("skipped %v failed %v", inv.Skipped, inv.Failed)
	}
}

func TestAddrsMalformedSourceFails(t *testing.T) {
	inv := observe(t.Context(), tmpRoot(t, map[string]string{"etc/hostname": "h\n", "addrs.txt": "garbage\n"}), noRunner{}, fsAddrs{path: "/addrs.txt"})
	if !containsPrefix(inv.Failed, "addresses: ") {
		t.Fatalf("failed %v", inv.Failed)
	}
}

func TestMappedIPv4AddressesAreUnmapped(t *testing.T) {
	r := tmpRoot(t, map[string]string{"addrs.txt": "eth0 ::ffff:192.0.2.9/128\n", "sys/class/net/eth0/type": "1"})
	inv, _ := probeAfter(t, r, nil, nil, "interfaces", "addresses")
	if len(inv.Addrs) != 1 || !inv.Addrs[0].IP.Is4() {
		t.Fatalf("%+v", inv.Addrs)
	}
}
