package agent

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestParseHexAddr(t *testing.T) {
	tests := []struct {
		in      string
		ip      string
		port    uint16
		wantErr bool
	}{
		{"0A0200C0:1538", "192.0.2.10", 5432, false},
		{"0100007F:0019", "127.0.0.1", 25, false},
		{"00000000:0000", "0.0.0.0", 0, false},
		{"B80D0120000000000000000010000000:238C", "2001:db8::10", 9100, false},
		{"00000000000000000000000001000000:0143", "::1", 323, false},
		{"0000000000000000FFFF00000A0200C0:01BB", "::ffff:192.0.2.10", 443, false},
		{"0A0200C0", "", 0, true},
		{"0A0200C0:ZZZZ", "", 0, true},
		{"0A0200C0:10000", "", 0, true},
		{"0A02:0016", "", 0, true},
		{"0A0200CG:0016", "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			ip, port, err := parseHexAddr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("accepted %s:%d", ip, port)
				}
				return
			}
			if err != nil || ip != netip.MustParseAddr(tc.ip) || port != tc.port {
				t.Fatalf("got %v:%d err %v, want %s:%d", ip, port, err, tc.ip, tc.port)
			}
		})
	}
}

type listenerWant struct {
	proto, addr string
	port        uint16
	process     string
}

func listenerRows(inv *Inventory) []listenerWant {
	var out []listenerWant
	for _, l := range inv.Listeners {
		out = append(out, listenerWant{l.Proto, l.Addr.String(), l.Port, l.Process})
	}
	return out
}

func TestProbeSocketsDebian(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "debian12"), nil, nil, "interfaces", "addresses", "sockets")
	if err != nil {
		t.Fatal(err)
	}
	wantL := []listenerWant{
		{"tcp", "0.0.0.0", 22, "sshd"},
		{"tcp", "::", 22, "sshd"},
		{"tcp", "127.0.0.1", 25, "exim4"},
		{"tcp", "192.0.2.10", 5432, "postgres"},
		{"tcp", "2001:db8::10", 9100, "node_exporter"},
		{"udp", "0.0.0.0", 123, "chronyd"},
		{"udp", "127.0.0.1", 323, "chronyd"},
		{"udp", "::1", 323, "chronyd"},
	}
	gotL := listenerRows(inv)
	if len(gotL) != len(wantL) {
		t.Fatalf("listeners\n got %+v\nwant %+v", gotL, wantL)
	}
	for i := range wantL {
		if gotL[i] != wantL[i] {
			t.Errorf("listener %d = %+v, want %+v", i, gotL[i], wantL[i])
		}
	}
	// One outbound connection: 198.51.100.20 (two sockets, ports 443 and 8443,
	// fold to the smallest). Dropped: the inbound ssh session from
	// 192.0.2.50, the connection to 127.0.0.1, the TIME_WAIT one and the UDP rows.
	if len(inv.Conns) != 1 {
		t.Fatalf("conns %+v", inv.Conns)
	}
	if c := inv.Conns[0]; c.Remote != netip.MustParseAddr("198.51.100.20") || c.Port != 443 || c.Proto != "tcp" || c.Process != "backup-agent" {
		t.Fatalf("conn %+v", c)
	}
	if len(inv.Skipped) != 0 {
		t.Fatalf("skipped %v", inv.Skipped)
	}
}

func TestProbeSocketsRHEL(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "rhel9"), nil, nil, "interfaces", "addresses", "sockets")
	if err != nil {
		t.Fatal(err)
	}
	if got := listenerRows(inv); len(got) != 2 || got[0] != (listenerWant{"tcp", "0.0.0.0", 22, "sshd"}) || got[1] != (listenerWant{"tcp", "::", 22, "sshd"}) {
		t.Fatalf("listeners %+v", got)
	}
	if len(inv.Conns) != 1 || inv.Conns[0].Remote != netip.MustParseAddr("192.0.2.60") || inv.Conns[0].Port != 514 || inv.Conns[0].Process != "rsyslogd" {
		t.Fatalf("conns %+v", inv.Conns)
	}
}

func TestInboundConnectionsAreNotReported(t *testing.T) {
	// 192.0.2.50 is an ssh client of this host: its own agent reports it.
	inv, _ := probeAfter(t, openRoot(t, "debian12"), nil, nil, "interfaces", "addresses", "sockets")
	for _, c := range inv.Conns {
		if c.Remote == netip.MustParseAddr("192.0.2.50") {
			t.Fatalf("inbound connection reported as talks_to: %+v", c)
		}
	}
}

func TestEphemeralUDPSocketsAreNotListeners(t *testing.T) {
	inv, _ := probeAfter(t, openRoot(t, "debian12"), nil, nil, "interfaces", "addresses", "sockets")
	for _, l := range inv.Listeners {
		if l.Proto == "udp" && l.Port == 50000 {
			t.Fatalf("an outgoing-query socket on port %d is a service", l.Port)
		}
	}
	// With a lower ephemeral range the same socket is below it and becomes a service.
	root := t.TempDir()
	copyTree(t, "testdata/debian12/root", root)
	r, err := NewRootFS(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	writeFile(t, root, "proc/sys/net/ipv4/ip_local_port_range", "55000\t60999\n")
	inv, _ = probeAfter(t, r, nil, nil, "interfaces", "addresses", "sockets")
	found := false
	for _, l := range inv.Listeners {
		found = found || (l.Proto == "udp" && l.Port == 50000)
	}
	if !found {
		t.Fatal("the ephemeral range is not read from /proc/sys/net/ipv4/ip_local_port_range")
	}
}

func TestEphemeralLow(t *testing.T) {
	tests := map[string]uint16{"": 32768, "32768\t60999\n": 32768, "1024 65000": 1024, "garbage": 32768, "0 100": 32768, "70000 80000": 32768, "100": 32768}
	for in, want := range tests {
		files := map[string]string{}
		if in != "" {
			files["proc/sys/net/ipv4/ip_local_port_range"] = in
		} else {
			files["proc/"] = ""
		}
		c := &collector{fs: tmpRoot(t, files)}
		if got := c.ephemeralLow(); got != want {
			t.Errorf("range %q -> %d, want %d", in, got, want)
		}
	}
}

func TestUsableRemote(t *testing.T) {
	tests := map[string]bool{
		"198.51.100.20": true, "2001:db8::1": true, "::ffff:198.51.100.20": true,
		"127.0.0.1": false, "::1": false, "::ffff:127.0.0.1": false, "0.0.0.0": false, "::": false,
		"169.254.0.5": false, "fe80::1": false, "224.0.0.251": false, "ff02::1": false,
	}
	for in, want := range tests {
		if got := usableRemote(netip.MustParseAddr(in)); got != want {
			t.Errorf("usableRemote(%s) = %v, want %v", in, got, want)
		}
	}
	if usableRemote(netip.Addr{}) {
		t.Error("the zero address is usable")
	}
}

func TestSocketsOfUnreadableProcessesAreNamelessAndNoted(t *testing.T) {
	// Without root /proc/<pid>/fd of other users' processes is refused.
	fsys := denyFS{FS: openRoot(t, "debian12"), prefixes: []string{"/proc/411/fd"}}
	inv, err := probeAfter(t, fsys, nil, nil, "interfaces", "addresses", "sockets")
	if err != nil {
		t.Fatal(err)
	}
	var sshd, exim int
	for _, l := range inv.Listeners {
		switch l.Port {
		case 22:
			if l.Process != "" {
				t.Errorf("sshd socket named %q despite unreadable descriptors", l.Process)
			}
			sshd++
		case 25:
			if l.Process != "exim4" {
				t.Errorf("exim4 lost its name: %q", l.Process)
			}
			exim++
		}
	}
	if sshd != 2 || exim != 1 {
		t.Fatalf("listeners %+v", inv.Listeners)
	}
	if len(inv.Skipped) != 1 || !strings.Contains(inv.Skipped[0], "1 processes' descriptors not readable") {
		t.Fatalf("skipped %v", inv.Skipped)
	}
}

func TestProcessNameComesFromCommOnly(t *testing.T) {
	// A process's command line and environment hold secrets: they are never opened.
	root := t.TempDir()
	copyTree(t, "testdata/debian12/root", root)
	writeFile(t, root, "proc/411/cmdline", "/usr/sbin/sshd\x00-o\x00PasswordAuthentication=hunter2\x00")
	writeFile(t, root, "proc/411/environ", "DB_PASSWORD=hunter2\x00")
	base, err := NewRootFS(root)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	rec := &recordFS{FS: base}
	inv := observe(t.Context(), rec, newFakeRunner("debian12"), fsAddrs{path: "/addrs.txt"})
	for _, name := range rec.opened() {
		if strings.HasSuffix(name, "/cmdline") || strings.HasSuffix(name, "/environ") {
			t.Fatalf("the agent opened %s", name)
		}
	}
	b, err := inv.Batch(epoch, "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if js := mustJSON(t, b); strings.Contains(js, "hunter2") {
		t.Fatal("a secret reached the batch")
	}
}

func TestProbeSocketsCaps(t *testing.T) {
	const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	t.Run("listeners", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(header)
		for i := 1; i <= maxListeners+10; i++ {
			fmt.Fprintf(&sb, "%4d: 00000000:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 %d 1\n", i, i, 100000+i)
		}
		inv, err := probeAfter(t, tmpRoot(t, map[string]string{"proc/net/tcp": sb.String()}), nil, nil, "sockets")
		if err != nil {
			t.Fatal(err)
		}
		if len(inv.Listeners) != maxListeners || inv.Listeners[0].Port != 1 {
			t.Fatalf("%d listeners kept", len(inv.Listeners))
		}
		if !containsSub(inv.Skipped, fmt.Sprintf("first %d kept", maxListeners)) {
			t.Fatalf("the truncation was not reported: %v", inv.Skipped)
		}
	})
	t.Run("remote hosts", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(header)
		for i := 1; i <= maxConns+5; i++ {
			// remote 10.0.<i/256>.<i%256>, hex in network order as the kernel prints it on little-endian.
			fmt.Fprintf(&sb, "%4d: 0A0200C0:9C40 %02X%02X000A:01BB 01 00000000:00000000 00:00000000 00000000 0 0 %d 1\n", i, i%256, i/256, 200000+i)
		}
		inv, err := probeAfter(t, tmpRoot(t, map[string]string{"proc/net/tcp": sb.String()}), nil, nil, "sockets")
		if err != nil {
			t.Fatal(err)
		}
		if len(inv.Conns) != maxConns {
			t.Fatalf("%d connections kept", len(inv.Conns))
		}
		if !containsSub(inv.Skipped, fmt.Sprintf("first %d kept", maxConns)) {
			t.Fatalf("the truncation was not reported: %v", inv.Skipped)
		}
	})
}

func TestProbeSocketsMissingAndMalformed(t *testing.T) {
	inv := observe(t.Context(), tmpRoot(t, map[string]string{"etc/hostname": "h\n"}), noRunner{}, fsAddrs{path: "/addrs.txt"})
	if !containsPrefix(inv.Skipped, "sockets: ") {
		t.Fatalf("skipped %v failed %v", inv.Skipped, inv.Failed)
	}
	bad := tmpRoot(t, map[string]string{"proc/net/tcp": "header\n  0: ZZZZ:0016 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n"})
	if _, err := probeAfter(t, bad, nil, nil, "sockets"); err == nil {
		t.Fatal("a malformed socket table was accepted")
	}
	short := tmpRoot(t, map[string]string{"proc/net/tcp": "header\ntoo short\n"})
	inv2, err := probeAfter(t, short, nil, nil, "sockets")
	if err != nil || len(inv2.Listeners) != 0 {
		t.Fatalf("a short row: %+v %v", inv2.Listeners, err)
	}
}

func TestConnectionsToOwnAddressesAreDropped(t *testing.T) {
	// A connection from this host to one of its own addresses is not a dependency.
	r := tmpRoot(t, map[string]string{
		"sys/class/net/eth0/type": "1",
		"addrs.txt":               "eth0 192.0.2.10/24\n",
		"proc/net/tcp": "header\n" +
			"  0: 0A0200C0:9C40 0A0200C0:1F90 01 0:0 0:0 0 0 0 1 1\n" +
			"  1: 0A0200C0:9C41 3C0200C0:0202 01 0:0 0:0 0 0 0 2 1\n",
	})
	inv, err := probeAfter(t, r, nil, nil, "interfaces", "addresses", "sockets")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Conns) != 1 || inv.Conns[0].Remote != netip.MustParseAddr("192.0.2.60") {
		t.Fatalf("%+v", inv.Conns)
	}
}

func TestMappedIPv6RemotesFoldWithIPv4(t *testing.T) {
	r := tmpRoot(t, map[string]string{
		"proc/net/tcp":  "header\n  0: 0A0200C0:9C40 3C0200C0:0202 01 0:0 0:0 0 0 0 1 1\n",
		"proc/net/tcp6": "header\n  0: 0000000000000000FFFF00000A0200C0:9C41 0000000000000000FFFF00003C0200C0:01BB 01 0:0 0:0 0 0 0 2 1\n",
	})
	inv, err := probeAfter(t, r, nil, nil, "sockets")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Conns) != 1 || inv.Conns[0].Remote != netip.MustParseAddr("192.0.2.60") || inv.Conns[0].Port != 443 {
		t.Fatalf("%+v", inv.Conns)
	}
}
