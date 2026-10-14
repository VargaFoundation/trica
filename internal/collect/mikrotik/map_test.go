package mikrotik

import (
	"reflect"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestParseUptime(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"3w2d4h5m6s", 3*604800 + 2*86400 + 4*3600 + 5*60 + 6, true},
		{"45s", 45, true},
		{"1d", 86400, true},
		{"10m", 600, true},
		{"6s500ms", 6, true},
		{"1d02:03:04", 86400 + 2*3600 + 3*60 + 4, true},
		{"02:03:04", 2*3600 + 3*60 + 4, true},
		{"", 0, false},
		{"abc", 0, false},
		{"5", 0, false},
		{"3x", 0, false},
		{"xd02:03:04", 0, false},
		{"02:03", 0, false},
		{"02:xx:04", 0, false},
	} {
		got, ok := parseUptime(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseUptime(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseSpeed(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"10Mbps", 10, true}, {"100Mbps", 100, true}, {"1Gbps", 1000, true}, {"2.5Gbps", 2500, true},
		{"10Gbps", 10000, true}, {"100Gbps", 100000, true}, {"1G", 1000, true},
		{"", 0, false}, {"fast", 0, false}, {"0Mbps", 0, false}, {"Gbps", 0, false}, {"12", 0, false},
	} {
		got, ok := parseSpeed(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseSpeed(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseVIDs(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    []int
		wantErr bool
	}{
		{"10", []int{10}, false},
		{"20-22", []int{20, 21, 22}, false},
		{"10,20-21", []int{10, 20, 21}, false},
		{" 5 , 7 ", []int{5, 7}, false},
		{"", nil, false},
		{"0", nil, true},
		{"4095", nil, true},
		{"5-3", nil, true},
		{"a", nil, true},
		{"1-4094", nil, true}, // over maxVIDsPerRow
	} {
		got, err := parseVIDs(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseVIDs(%q) error = %v, want error %v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseVIDs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if got, err := parseVIDs("1-512"); err != nil || len(got) != 512 || got[0] != 1 || got[511] != 512 {
		t.Errorf("1-512: %d ids, %v", len(got), err)
	}
}

func TestInterfaceKind(t *testing.T) {
	for in, want := range map[string]string{
		"ether": "physical", "wlan": "physical", "wifi": "physical", "lte": "physical",
		"bond": "lag", "vlan": "vlan", "loopback": "loopback", "bridge": "virtual", "veth": "virtual", "vrrp": "virtual",
		"gre-tunnel": "tunnel", "eoip-tunnel": "tunnel", "wg": "tunnel", "ovpn-out": "tunnel", "pppoe-out": "tunnel",
		"l2tp-in": "tunnel", "sstp-out": "tunnel", "ipip-tunnel": "tunnel",
		"mesh": "other", "": "other", "ETHER": "physical",
	} {
		if got := interfaceKind(in); got != want {
			t.Errorf("interfaceKind(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSmallHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"lldp": "lldp", "cdp": "cdp", "mndp": "mndp", "mndp,lldp,cdp": "lldp", "mndp,cdp": "cdp", "CDP,LLDP": "lldp",
		"": "", "unknown": "",
	} {
		if got := bestProtocol(in); got != want {
			t.Errorf("bestProtocol(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"core-1.example.net": "core-1", "srv-01": "srv-01", "192.0.2.7": "192.0.2.7", "": "", "2001:db8::1": "2001:db8::1",
	} {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"ok": "ok", "OK": "ok", "fail": "critical", "failed": "critical", "error": "critical", "absent": "absent",
		"not-present": "absent", "": "unknown", "weird": "unknown",
	} {
		if got := componentHealth(in); got != want {
			t.Errorf("componentHealth(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{"true": true, "yes": true, "TRUE": true, "false": false, "no": false, "": false, "1": false} {
		if got := parseFlag(in); got != want {
			t.Errorf("parseFlag(%q) = %v", in, got)
		}
	}
	if !parseFlag(" yes ") {
		t.Error("parseFlag trims")
	}
	if got := splitList(" a, b ,,c,"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("splitList = %v", got)
	}
	if got := unique([]string{"a", "b", "a"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("unique = %v", got)
	}
	for _, c := range []struct {
		in, prefix, comp, sub string
		ok                    bool
	}{
		{"psu1-voltage", "psu", "psu1", "voltage", true}, {"psu12-state", "psu", "psu12", "state", true},
		{"fan2-speed", "fan", "fan2", "speed", true},
		{"psu-state", "psu", "", "", false}, {"psu1", "psu", "", "", false}, {"psu1voltage", "psu", "", "", false},
		{"cpu-temperature", "psu", "", "", false},
	} {
		comp, sub, ok := component(c.in, c.prefix)
		if comp != c.comp || sub != c.sub || ok != c.ok {
			t.Errorf("component(%q, %q) = %q %q %v", c.in, c.prefix, comp, sub, ok)
		}
	}
}

func TestHealthReadingsShapes(t *testing.T) {
	v7 := healthReadings([]row{{"name": "cpu-temperature", "value": "47", "type": "C"}, {"name": "psu1-state", "value": "ok"}})
	if len(v7) != 2 || v7[0].typ != "C" || v7[0].scale != 0 || v7[1].name != "psu1-state" {
		t.Errorf("v7 shape = %+v", v7)
	}
	flat := healthReadings([]row{{".id": "*0", "voltage": "240", "temperature": "38", "fan1-speed": "5000", "current": "300", "psu1-state": "ok"}})
	byName := map[string]reading{}
	for _, r := range flat {
		byName[r.name] = r
	}
	if len(flat) != 5 || byName["voltage"].scale != 0.1 || byName["voltage"].typ != "V" || byName["temperature"].typ != "C" ||
		byName["fan1-speed"].typ != "RPM" || byName["current"].typ != "mA" || byName["psu1-state"].typ != "" {
		t.Errorf("flat shape = %+v", flat)
	}
	for i := 1; i < len(flat); i++ {
		if flat[i-1].name > flat[i].name {
			t.Errorf("flat readings are not in a stable order: %+v", flat)
		}
	}
}

// build maps synthetic rows through the real collector configuration.
func build(t *testing.T, data map[string][]row, opts ...func(*config)) (*sdk.Batch, *mapper) {
	t.Helper()
	cfg := config{target: collect.Target{Kind: Kind, Name: "t"}, maxEdgeMACs: DefaultMaxEdgeMACs, domain: "default", skip: map[string]bool{}}
	for _, o := range opts {
		o(&cfg)
	}
	if _, ok := data[cmdIdentity]; !ok {
		data[cmdIdentity] = []row{{"name": "sw-t"}}
	}
	m := newMapper(cfg, "192.0.2.1:8728", data)
	b, err := m.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	b.Health = &sdk.Health{OK: true}
	collecttest.CheckSchema(t, b)
	if err := b.Validate(); err != nil {
		t.Fatalf("invalid batch: %v", err)
	}
	return b, m
}

func TestNoIdentity(t *testing.T) {
	m := newMapper(config{target: collect.Target{Kind: Kind, Name: "t"}}, "192.0.2.1:8728", map[string][]row{})
	if _, err := m.build(); err == nil {
		t.Fatal("a device with no identity at all must not be reported")
	}
}

func TestIdentityFallbacks(t *testing.T) {
	cases := []struct {
		name string
		data map[string][]row
		want map[string]string
	}{
		{"orig mac of the first port wins", map[string][]row{
			cmdEthernet: {{"name": "ether1", "mac-address": "02:00:00:00:00:02", "orig-mac-address": "02:00:00:00:00:01"}},
			cmdBridge:   {{"name": "br", "mac-address": "02:00:00:00:00:09"}},
		}, map[string]string{"base_mac": "02:00:00:00:00:01", "name": "sw-t"}},
		{"current mac when no orig", map[string][]row{
			cmdEthernet: {{"name": "ether1", "mac-address": "02:00:00:00:00:02"}},
		}, map[string]string{"base_mac": "02:00:00:00:00:02", "name": "sw-t"}},
		{"first ether of /interface when /interface/ethernet failed", map[string][]row{
			cmdInterface: {{"name": "bridge", "type": "bridge", "mac-address": "02:00:00:00:00:09"}, {"name": "ether1", "type": "ether", "mac-address": "02:00:00:00:00:03"}},
		}, map[string]string{"base_mac": "02:00:00:00:00:03", "name": "sw-t"}},
		{"bridge mac only as a last resort", map[string][]row{
			cmdBridge: {{"name": "br", "mac-address": "02:00:00:00:00:09"}},
		}, map[string]string{"base_mac": "02:00:00:00:00:09", "name": "sw-t"}},
		{"serial from routerboard", map[string][]row{
			cmdRouterboard: {{"serial-number": "FAKE9", "model": "RB5009UG+S+IN", "current-firmware": "7.12"}},
		}, map[string]string{"serial": "FAKE9", "name": "sw-t"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, _ := build(t, c.data)
			id := obj(t, b, devRef).Identity
			delete(id, "mgmt_ip")
			if !reflect.DeepEqual(id, c.want) {
				t.Errorf("identity = %v, want %v", id, c.want)
			}
		})
	}
	// a CHR has no routerboard serial; the model comes from the resource
	b, _ := build(t, map[string][]row{cmdResource: {{"board-name": "CHR", "version": "7.15.2 (stable)"}}})
	wantAttrs(t, b, devRef, map[string]any{"model": "CHR", "role": "router", "os_version": "7.15.2"})
	noAttrs(t, b, devRef, "serial", "uptime_s", "memory_bytes", "bios_version")
}

func TestRoleByModel(t *testing.T) {
	for model, want := range map[string]string{
		"CRS354-48G-4S+2Q+": "switch", "crs326-24g-2s+": "switch", "CSS610-8G-2S+": "switch",
		"RB5009UG+S+IN": "router", "CCR2004-1G-12S+2XS": "router", "CHR": "router", "": "router",
	} {
		b, _ := build(t, map[string][]row{cmdRouterboard: {{"model": model, "serial-number": "FAKE1"}}})
		wantAttrs(t, b, devRef, map[string]any{"role": want})
	}
	b, _ := build(t, map[string][]row{cmdRouterboard: {{"model": "RB5009", "serial-number": "FAKE1"}}}, func(c *config) { c.opts.Role = "switch" })
	wantAttrs(t, b, devRef, map[string]any{"role": "switch"})
}

func TestWarningsAndDegradedInput(t *testing.T) {
	data := map[string][]row{
		cmdInterface: {
			{"name": "ether1", "type": "ether", "running": "true", "disabled": "false", "mtu": "auto"},
			{"name": "ether2", "type": "ether", "running": "false", "disabled": "false", "mtu": "1500"},
			{"name": "bridge", "type": "bridge", "running": "true", "disabled": "false"},
			{"name": "ether1", "type": "ether"}, // duplicate name: ignored
			{"type": "ether"},                   // no name: ignored
		},
		cmdEthernet: {
			{"name": "ether1", "running": "true", "speed": "warp9", "full-duplex": "false"},
			{"name": "ghost", "running": "true", "speed": "1Gbps"},
		},
		cmdBonding:    {{"name": "bond9", "slaves": "ether1"}, {"name": "bridge", "slaves": "ether-gone"}},
		cmdBridgePort: {{"bridge": "bridge", "interface": "ether1", "pvid": "1"}, {"bridge": "bridge", "interface": "nowhere"}},
		cmdBridgeVLAN: {
			{"bridge": "bridge", "vlan-ids": "10", "tagged": "ether1,LIST-UPLINKS", "untagged": "ether2", "comment": "ten"},
			{"bridge": "bridge", "vlan-ids": "1-4094", "tagged": "ether1"},
			{"bridge": "bridge", "vlan-ids": "x", "tagged": "ether1"},
			{"bridge": "bridge", "vlan-ids": "30", "disabled": "true"},
			{"bridge": "bridge", "vlan-ids": "10", "untagged": "ether1"}, // same VLAN again: tagged elsewhere wins
		},
		cmdAddress: {
			{"address": "not-an-ip", "interface": "ether1"},
			{"address": "192.0.2.77", "interface": "missing"}, // bare address, interface unknown: on the device
		},
		cmdHealth: {
			{"name": "mystery", "value": "1", "type": "?"}, {"name": "cpu-temperature", "value": "hot", "type": "C"},
			{"name": "psu1-whatever", "value": "1"}, {"name": "fan1-whatever", "value": "1"},
			{"name": "fan3-state", "value": "ok"}, {"name": "power-consumption", "value": "n/a", "type": "W"},
			{"name": "fan-rear", "value": "4000", "type": "RPM"}, {"name": "board-voltage", "value": "5", "type": "V"},
			{"name": "board-current", "value": "1", "type": "A"},
		},
	}
	b, m := build(t, data)

	want := []string{
		`interface ether1: unparsable speed "warp9"`,
		`member "ether-gone"`,
		`bridge port "nowhere"`,
		`bridge VLAN member "LIST-UPLINKS"`,
		`more than 512 VLAN ids`,
		`invalid VLAN id "x"`,
		`ip address "not-an-ip"`,
		`ip address 192.0.2.77`,
		`health sensor "mystery" not recognised`,
		`health sensor "cpu-temperature": unparsable value "hot"`,
		`health sensor "psu1-whatever"`,
		`health sensor "fan1-whatever"`,
	}
	got := strings.Join(m.warnings(), "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing warning %q in:\n%s", w, got)
		}
	}

	// speed is unparsable: no speed, but the duplex is still reported
	noAttrs(t, b, "if:ether1", "speed_mbps", "mtu") // "auto" is not a number
	wantAttrs(t, b, "if:ether1", map[string]any{"duplex": "half"})
	noObj(t, b, "if:ghost", "vlan:30")
	// the VLAN-10 row: ether1 is tagged in one row and untagged in the other: tagged wins
	if wantRel(t, b, "member_of", "if:ether1", "vlan:10").Attributes["tagged"] != true {
		t.Error("a tagged membership must win over an untagged one")
	}
	wantRel(t, b, "member_of", "if:ether2", "vlan:10")
	noObj(t, b, "vlan:11")
	wantAttrs(t, b, "vlan:10", map[string]any{"name": "ten"})
	// a bare address and an unknown interface: assigned to the device
	wantRel(t, b, "assigned_to", "ip:192.0.2.77", devRef)
	noAttrs(t, b, "ip:192.0.2.77", "prefix_length")
	// the fan seen only through its state
	wantAttrs(t, b, "fan:fan3", map[string]any{"health": "ok"})
	wantAttrs(t, b, "fan:fan-rear", map[string]any{"speed_rpm": 4000})
	wantAttrs(t, b, "sensor:device:board-voltage", map[string]any{"kind": "voltage"})
	wantAttrs(t, b, "sensor:device:board-current", map[string]any{"kind": "current", "unit": "A"})
	noAttrs(t, b, devRef, "power_w")
}

func TestWarningsAreBoundedAndDeduplicated(t *testing.T) {
	m := newMapper(config{}, "", nil)
	for i := 0; i < 50; i++ {
		m.warnf("same")
		m.warnf("warning %d", i)
	}
	w := m.warnings()
	if len(w) != maxWarnings+1 || !strings.Contains(w[len(w)-1], "more warnings") {
		t.Fatalf("%d warnings, last %q", len(w), w[len(w)-1])
	}
	if strings.Count(strings.Join(w, "|"), "same") != 1 {
		t.Error("an identical warning must be reported once")
	}
	if got := w[len(w)-1]; got != "31 more warnings not shown" {
		t.Errorf("last = %q", got)
	}
}

func TestBridgeWithoutVLANFiltering(t *testing.T) {
	data := map[string][]row{
		cmdBridge:     {{"name": "br", "vlan-filtering": "false"}},
		cmdInterface:  {{"name": "br", "type": "bridge"}, {"name": "ether1", "type": "ether"}},
		cmdBridgePort: {{"bridge": "br", "interface": "ether1", "pvid": "1"}},
		cmdBridgeVLAN: {{"bridge": "br", "vlan-ids": "10", "tagged": "ether1"}},
	}
	b, _ := build(t, data)
	wantRel(t, b, "member_of", "if:ether1", "if:br") // still a port of the bridge
	noAttrs(t, b, "if:ether1", "mode", "native_vlan")
	noObj(t, b, "vlan:10") // the table is ignored by the router without filtering
}

func TestRoutedAndVLANInterfaces(t *testing.T) {
	data := map[string][]row{
		cmdInterface: {
			{"name": "ether1", "type": "ether"}, {"name": "bond0", "type": "bond"}, {"name": "vlan5", "type": "vlan"},
			{"name": "ether2", "type": "ether"},
		},
		cmdVLAN:    {{"name": "vlan5", "vlan-id": "5", "interface": "ether2"}, {"name": "vlan5", "vlan-id": "5000"}, {"name": "none", "vlan-id": "6"}},
		cmdAddress: {{"address": "192.0.2.5/30", "interface": "ether1", "actual-interface": "ether1"}, {"address": "198.51.100.5/30", "interface": "bond0"}, {"address": "203.0.113.5/30", "interface": "vlan5"}},
	}
	b, _ := build(t, data)
	wantAttrs(t, b, "if:ether1", map[string]any{"mode": "routed"})
	wantAttrs(t, b, "if:bond0", map[string]any{"mode": "routed"})
	noAttrs(t, b, "if:vlan5", "mode") // a VLAN interface is not a switchport
	noAttrs(t, b, "if:ether2", "mode")
	if wantRel(t, b, "member_of", "if:vlan5", "vlan:5").Attributes["tagged"] != true {
		t.Error("a VLAN interface carries its VLAN tagged")
	}
	noObj(t, b, "vlan:6", "vlan:5000")
}

func TestNeighborEdgeCases(t *testing.T) {
	data := map[string][]row{
		cmdInterface: {{"name": "ether1", "type": "ether"}, {"name": "bond0", "type": "bond"}, {"name": "bridge", "type": "bridge"}},
		cmdNeighbor: {
			{"identity": "a.example", "interface": "bridge,bond0", "interface-name": "p1", "discovered-by": "lldp"}, // only a LAG: used
			{"identity": "b", "interface": "bridge", "interface-name": "p1"},                                        // only a bridge: no port
			{"identity": "c", "interface": "ether1", "interface-name": "p1", "discovered-by": "lldp"},
			{"identity": "c.other.example", "interface": "ether1", "interface-name": "p1", "discovered-by": "cdp"}, // same link twice
			{"identity": "", "interface": "ether1", "interface-name": "p9"},
			{"identity": "d", "interface": "ether1", "interface-name": "p2", "discovered-by": "something"}, // unknown protocol: none
			{"identity": "192.0.2.9", "interface": "ether1", "interface-name": "p3", "discovered-by": "cdp"},
		},
	}
	b, _ := build(t, data)
	if neighbor(b, "if:bond0", "a", "p1") == nil {
		t.Error("a neighbour seen through a LAG is reported on the LAG")
	}
	if neighbor(b, "if:bridge", "b", "p1") != nil {
		t.Error("a bridge is not a cabled port")
	}
	n := 0
	for _, r := range b.Relations {
		if r.Type == "neighbor_of" && r.To.Parent.Identity["name"] == "c" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the same link seen twice gives %d relations, want 1", n)
	}
	if r := neighbor(b, "if:ether1", "d", "p2"); r == nil || r.Attributes["protocol"] != nil {
		t.Errorf("unknown protocol: %+v", r)
	}
	if neighbor(b, "if:ether1", "192.0.2.9", "p3") == nil {
		t.Error("an address as identity is kept whole")
	}
}

func TestLearnedMACsRules(t *testing.T) {
	data := map[string][]row{
		cmdInterface: {{"name": "ether1", "type": "ether"}, {"name": "bond0", "type": "bond"}, {"name": "bridge", "type": "bridge"}, {"name": "ether2", "type": "ether"}},
		cmdBridgeHost: {
			{"on-interface": "ether1", "mac-address": "02:AA:00:00:00:01", "vid": "10"},
			{"on-interface": "ether1", "mac-address": "02:AA:00:00:00:01", "vid": "10"}, // duplicate
			{"on-interface": "ether1", "mac-address": "02:AA:00:00:00:02"},              // no vid: 0
			{"on-interface": "ether1", "mac-address": "02:AA:00:00:00:03", "vid": "9"},
			{"on-interface": "ether1", "mac-address": "02:AA:00:00:00:04", "local": "true"},
			{"on-interface": "ether1", "mac-address": "02:AA:00:00:00:05", "invalid": "true"},
			{"on-interface": "bridge", "mac-address": "02:AA:00:00:00:06", "vid": "1"},
			{"on-interface": "nowhere", "mac-address": "02:AA:00:00:00:07"},
			{"on-interface": "", "mac-address": "02:AA:00:00:00:08"},
			{"on-interface": "ether2", "mac-address": ""},
		},
	}
	b, _ := build(t, data)
	wantAttrs(t, b, "if:ether1", map[string]any{"learned_macs": []string{"0/02:aa:00:00:00:02", "10/02:aa:00:00:00:01", "9/02:aa:00:00:00:03"}})
	noAttrs(t, b, "if:bridge", "learned_macs")
	noAttrs(t, b, "if:ether2", "learned_macs")
}
