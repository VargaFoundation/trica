package arista

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// collectFrom runs the collector against a fake playing version and returns
// the single batch it emitted.
func collectFrom(t *testing.T, version string, tweak func(*collect.Target)) (*sdk.Batch, *FakeArista) {
	t.Helper()
	t.Setenv(PasswordEnv, FakePassword)
	f := NewFakeArista(version)
	t.Cleanup(f.Close)
	tg := f.Target("sw")
	if tweak != nil {
		tweak(&tg)
	}
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	pinClock(c)
	batches := collecttest.Run(t, c)
	if len(batches) != 1 {
		t.Fatalf("got %d batches, want 1", len(batches))
	}
	return batches[0], f
}

// testNow is the clock of the runs of the tests: the 4.14 fixture booted
// 1234567 s before it.
var testNow = time.Unix(1759234567, 0)

// pinClock makes the uptime a switch derives from its boot time deterministic.
func pinClock(c sdk.Collector) {
	if ac, ok := c.(*Collector); ok {
		ac.now = func() time.Time { return testNow }
	}
}

// checkWarning asserts that one of the health warnings contains every part.
func checkWarning(t *testing.T, b *sdk.Batch, parts ...string) {
	t.Helper()
	for _, w := range b.Health.Warnings {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(w, p)
		}
		if all {
			return
		}
	}
	t.Errorf("no warning contains %q; warnings: %q", parts, b.Health.Warnings)
}

// mustOptions parses a YAML "options:" mapping; the documents are literals of
// this file, so a parse error is a bug in the test.
func mustOptions(doc string) yaml.Node {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		panic(err)
	}
	return *n.Content[0]
}

// obj returns the object of the given type and identity key, failing if absent.
func obj(t *testing.T, b *sdk.Batch, typ, key, value string) *sdk.Object {
	t.Helper()
	o := collecttest.Find([]*sdk.Batch{b}, typ, key, value)
	if o == nil {
		t.Fatalf("no %s with %s=%q", typ, key, value)
	}
	return o
}

// ifObj returns the interface of the device named name.
func ifObj(t *testing.T, b *sdk.Batch, name string) *sdk.Object {
	return obj(t, b, "interface", "name", name)
}

func checkAttrs(t *testing.T, o *sdk.Object, want map[string]any) {
	t.Helper()
	for k, w := range want {
		got, ok := o.Attributes[k]
		if !ok {
			t.Errorf("%s: attribute %s missing", o.Ref, k)
			continue
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s: %s = %#v, want %#v", o.Ref, k, got, w)
		}
	}
}

func checkAbsent(t *testing.T, o *sdk.Object, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := o.Attributes[k]; ok {
			t.Errorf("%s: attribute %s = %#v, want none", o.Ref, k, v)
		}
	}
}

// relAttrs returns the attributes of the relation of typ between two refs.
func relAttrs(b *sdk.Batch, typ, from, to string) (map[string]any, bool) {
	for _, r := range b.Relations {
		if r.Type == typ && r.From.Ref == from && r.To.Ref == to {
			return r.Attributes, true
		}
	}
	return nil, false
}

type wantRel struct {
	from, to string
	attrs    map[string]any
}

func checkRels(t *testing.T, b *sdk.Batch, typ string, want []wantRel) {
	t.Helper()
	n := 0
	for _, r := range b.Relations {
		if r.Type == typ {
			n++
		}
	}
	if n != len(want) {
		t.Errorf("%s relations: got %d, want %d", typ, n, len(want))
	}
	for _, w := range want {
		attrs, ok := relAttrs(b, typ, w.from, w.to)
		if !ok {
			t.Errorf("missing %s %s -> %s", typ, w.from, w.to)
			continue
		}
		for k, v := range w.attrs {
			if !reflect.DeepEqual(attrs[k], v) {
				t.Errorf("%s %s -> %s: %s = %#v, want %#v", typ, w.from, w.to, k, attrs[k], v)
			}
		}
	}
}

func TestEOS414(t *testing.T) {
	b, f := collectFrom(t, EOS414, nil)

	// Four commands have no JSON output on 4.14: that is the release, not a
	// failure, and the run is still full and healthy.
	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Source != "arista:sw" {
		t.Fatalf("scope=%v ok=%v source=%q err=%q", b.Scope, b.Health.OK, b.Source, b.Health.Error)
	}
	checkWarning(t, b, "not available on this EOS release", cmdLLDPLocal, cmdLLDPNeighbors, cmdPortChannel, cmdUptime)
	if b.Health.Collector != Kind || b.Health.Items != len(b.Objects)+len(b.Relations) {
		t.Errorf("health = %+v, %d objects, %d relations", b.Health, len(b.Objects), len(b.Relations))
	}

	dev := obj(t, b, "device", "serial", "FAKE7050A0001")
	if !reflect.DeepEqual(dev.Identity, map[string]string{
		"serial": "FAKE7050A0001", "base_mac": "02:1c:73:00:00:01", "name": "sw-a1", "mgmt_ip": "192.0.2.11",
	}) {
		t.Errorf("device identity = %v", dev.Identity)
	}
	checkAttrs(t, dev, map[string]any{
		"role": "switch", "vendor": "Arista", "model": "DCS-7050SX-64-F", "os_name": "EOS", "os_version": "4.14.2F",
		"uptime_s": int64(1234567), "memory_bytes": int64(3794508 * 1024), "health": "ok", "power_w": 249.5,
		"mlag_domain": "MLAG-A", "mlag_state": "active", "mlag_peer": "203.0.113.2",
	})
	checkAbsent(t, dev, "inlet_temp_c", "outlet_temp_c") // no sensor says inlet or outlet

	t.Run("interfaces", func(t *testing.T) {
		checkAttrs(t, ifObj(t, b, "Ethernet1"), map[string]any{
			"kind": "physical", "mac": "02:1c:73:00:00:01", "description": "srv-01 eno1", "admin_status": "up",
			"oper_status": "up", "mtu": int64(9214), "duplex": "full", "speed_mbps": int64(10000),
			"mode": "access", "native_vlan": int64(100), "learned_macs": []string{"100/02:aa:00:00:01:01"},
		})
		if ifObj(t, b, "Ethernet1").Parent != dev.Ref {
			t.Errorf("Ethernet1 is not a child of the device")
		}
		checkAttrs(t, ifObj(t, b, "Ethernet3"), map[string]any{"oper_status": "not_present", "native_vlan": int64(1)})
		checkAttrs(t, ifObj(t, b, "Ethernet51"), map[string]any{"mode": "trunk", "speed_mbps": int64(40000)})
		checkAttrs(t, ifObj(t, b, "Port-Channel1"), map[string]any{"kind": "lag", "mode": "trunk", "speed_mbps": int64(80000)})
		checkAttrs(t, ifObj(t, b, "Vlan100"), map[string]any{"kind": "vlan"})
		checkAttrs(t, ifObj(t, b, "Loopback0"), map[string]any{"kind": "loopback"})
		checkAttrs(t, ifObj(t, b, "Management1"), map[string]any{"mode": "routed", "mac": "02:1c:73:00:00:ff"})
		// Ethernet49/50 are LAG members: the switch reports no mode for them.
		checkAbsent(t, ifObj(t, b, "Ethernet49"), "mode", "native_vlan")
		// A SVI has no speed, duplex or switchport mode.
		checkAbsent(t, ifObj(t, b, "Vlan100"), "speed_mbps", "duplex", "mode")
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "interface")); got != 11 {
			t.Errorf("%d interfaces, want 11", got)
		}
	})

	t.Run("learned macs only on edge ports", func(t *testing.T) {
		checkAttrs(t, ifObj(t, b, "Ethernet2"), map[string]any{
			"learned_macs": []string{"100/02:aa:00:00:02:01", "100/02:aa:00:00:02:02"},
		})
		// Ethernet3 holds only a static entry; Ethernet51 (5 MACs) and Port-Channel1
		// (5 MACs) are trunks, above max_edge_macs=4; Cpu is not an interface.
		for _, name := range []string{"Ethernet3", "Ethernet51", "Port-Channel1", "Ethernet49", "Management1"} {
			checkAbsent(t, ifObj(t, b, name), "learned_macs")
		}
	})

	t.Run("ip addresses", func(t *testing.T) {
		want := map[string]struct {
			role   string
			prefix int64
			iface  string
		}{
			"192.0.2.11":     {"primary", 24, "if/Management1"},
			"198.51.100.2":   {"primary", 24, "if/Vlan100"},
			"198.51.100.200": {"secondary", 24, "if/Vlan100"},
			"198.51.100.1":   {"vip", 24, "if/Vlan100"},
			"203.0.113.1":    {"primary", 30, "if/Vlan4094"},
			"203.0.113.101":  {"primary", 32, "if/Loopback0"},
		}
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "ip_address")); got != len(want) {
			t.Errorf("%d ip_address objects, want %d", got, len(want))
		}
		var rels []wantRel
		for addr, w := range want {
			ip := obj(t, b, "ip_address", "address", addr)
			checkAttrs(t, ip, map[string]any{"role": w.role, "prefix_length": w.prefix})
			rels = append(rels, wantRel{from: ip.Ref, to: w.iface})
		}
		checkRels(t, b, "assigned_to", rels)
	})

	t.Run("vlans", func(t *testing.T) {
		for key, name := range map[string]string{"default:1": "default", "default:100": "SERVERS", "default:200": "STORAGE", "default:4094": "MLAG"} {
			v := obj(t, b, "vlan", "key", key)
			vid := strings.Split(key, ":")[1]
			if v.Attributes["name"] != name || v.Attributes["domain"] != "default" || v.Attributes["vid"] == nil || !strings.HasSuffix(v.Ref, "/"+vid) {
				t.Errorf("%s: %v", key, v.Attributes)
			}
		}
		checkRels(t, b, "member_of", []wantRel{
			{"if/Ethernet3", "vlan/1", map[string]any{"tagged": false}},
			{"if/Ethernet1", "vlan/100", map[string]any{"tagged": false}},
			{"if/Ethernet2", "vlan/100", map[string]any{"tagged": false}},
			// A trunk with no reported native VLAN carries VLAN 100 tagged (native assumed 1).
			{"if/Ethernet51", "vlan/100", map[string]any{"tagged": true}},
			{"if/Ethernet51", "vlan/200", map[string]any{"tagged": true}},
			{"if/Port-Channel1", "vlan/100", map[string]any{"tagged": true}},
			{"if/Port-Channel1", "vlan/200", map[string]any{"tagged": true}},
			{"if/Port-Channel1", "vlan/4094", map[string]any{"tagged": true}},
			{"if/Vlan100", "vlan/100", nil},
			{"if/Vlan4094", "vlan/4094", nil},
			// LAG membership, from memberInterfaces of show interfaces: 4.14 has
			// no JSON for show port-channel.
			{"if/Ethernet49", "if/Port-Channel1", nil},
			{"if/Ethernet50", "if/Port-Channel1", nil},
		})
	})

	t.Run("lldp", func(t *testing.T) {
		// 4.14 has no JSON for the detail: the brief table names neighbours by
		// name only. Ethernet1's neighbour (a Linux host) gives a MAC address
		// as its port id and no port name: it is left out, and said so.
		checkNeighbors(t, b, map[string]nb{
			"if/Ethernet2":  {map[string]string{"name": "srv-02"}, "eth0"},
			"if/Ethernet49": {map[string]string{"name": "sw-a2"}, "Ethernet49"},
			"if/Ethernet50": {map[string]string{"name": "sw-a2"}, "Ethernet50"},
			"if/Ethernet51": {map[string]string{"name": "sw-b1"}, "Ethernet49"},
		})
		checkWarning(t, b, "1 neighbor(s)", "MAC address only")
	})

	t.Run("hardware", func(t *testing.T) {
		for i, serial := range map[string]string{"1": "FAKEPSU0001", "2": "FAKEPSU0002"} {
			psu := obj(t, b, "psu", "serial", serial)
			checkAttrs(t, psu, map[string]any{"model": "PWR-460AC-F", "capacity_w": int64(460), "health": "ok"})
			if psu.Identity["name"] != "PowerSupply"+i || psu.Parent != dev.Ref {
				t.Errorf("psu %s: identity %v parent %s", serial, psu.Identity, psu.Parent)
			}
			// The PSU's own fan sits under it.
			fan := obj(t, b, "fan", "name", "PowerSupply"+i+"/1")
			if fan.Parent != psu.Ref {
				t.Errorf("fan %s under %s, want %s", fan.Identity["name"], fan.Parent, psu.Ref)
			}
		}
		// Trays that report one speed and no fan list are one fan each.
		checkAttrs(t, obj(t, b, "fan", "name", "FanTray1"), map[string]any{"speed_pct": float64(35), "health": "ok"})
		checkAttrs(t, obj(t, b, "fan", "name", "PowerSupply1/1"), map[string]any{"speed_pct": float64(30)})
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "fan")); got != 4 {
			t.Errorf("%d fans, want 4", got)
		}

		cpu := devSensor(t, b, dev, "TempSensor0")
		checkAttrs(t, cpu, map[string]any{"kind": "temperature", "placement": "cpu", "reading": float64(41), "unit": "°C",
			"warning_threshold": float64(95), "critical_threshold": float64(105), "health": "ok"})
		checkAttrs(t, devSensor(t, b, dev, "TempSensor1"), map[string]any{"placement": "system"})
		// "Front-panel temp sensor" says nothing about airflow: not an inlet.
		checkAttrs(t, devSensor(t, b, dev, "TempSensor2"), map[string]any{"placement": "other"})
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "sensor")); got != 5 {
			t.Errorf("%d sensors, want 5", got)
		}
	})

	t.Run("transceivers", func(t *testing.T) {
		x1 := obj(t, b, "transceiver", "serial", "FAKEXCVR0001")
		checkAttrs(t, x1, map[string]any{
			"model": "SFP-10G-SR", "vendor": "Arista Networks", "media_type": "10GBASE-SR",
			"tx_power_dbm": -2.4, "rx_power_dbm": -3.1, "temperature_c": 31.5, "voltage_v": 3.29,
		})
		if x1.Parent != "if/Ethernet1" || x1.Identity["name"] != "Ethernet1" {
			t.Errorf("transceiver 1: parent %s identity %v", x1.Parent, x1.Identity)
		}
		// A copper cable reports "N/A" for optical power: no reading, not zero.
		x2 := obj(t, b, "transceiver", "serial", "FAKEXCVR0002")
		checkAbsent(t, x2, "tx_power_dbm", "rx_power_dbm")
		checkAttrs(t, x2, map[string]any{"temperature_c": float64(30)})
		// Slot 3 is "Not Present": no transceiver.
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "transceiver")); got != 5 {
			t.Errorf("%d transceivers, want 5", got)
		}
	})

	t.Run("only reads", func(t *testing.T) { checkReadOnly(t, f) })
}

func TestEOS420(t *testing.T) {
	b, f := collectFrom(t, EOS420, nil)

	if b.Scope != sdk.ScopeFull || !b.Health.OK {
		t.Fatalf("scope=%v ok=%v err=%q", b.Scope, b.Health.OK, b.Health.Error)
	}
	// show uptime has no JSON output on 4.20 either: the uptime comes from show version.
	checkWarning(t, b, "not available on this EOS release", cmdUptime)
	for _, w := range b.Health.Warnings {
		if strings.Contains(w, cmdLLDPNeighbors) || strings.Contains(w, cmdPortChannel) {
			t.Errorf("warning %q: 4.20 has JSON for those", w)
		}
	}
	for _, c := range f.Commands() {
		if c == cmdLLDPBrief {
			t.Errorf("%s asked although the detail worked", cmdLLDPBrief)
		}
	}
	dev := obj(t, b, "device", "serial", "FAKE7280B0001")
	if !reflect.DeepEqual(dev.Identity, map[string]string{
		"serial": "FAKE7280B0001", "base_mac": "02:1c:73:00:10:01", "name": "sw-b1", "mgmt_ip": "192.0.2.12",
	}) {
		t.Errorf("device identity = %v", dev.Identity)
	}
	checkAttrs(t, dev, map[string]any{
		"role": "switch", "model": "DCS-7280SE-64-F", "os_version": "4.20.7M", "uptime_s": int64(2234567),
		"memory_bytes": int64(8010656 * 1024), "mlag_state": "disabled",
		// PSU 2 has lost its input: only PSU 1 delivers power.
		"power_w": float64(160), "inlet_temp_c": 27.5, "outlet_temp_c": float64(38), "health": "ok",
	})
	// MLAG is disabled: the empty domain and peer are not facts.
	checkAbsent(t, dev, "mlag_domain", "mlag_peer")

	t.Run("interfaces", func(t *testing.T) {
		checkAttrs(t, ifObj(t, b, "Ethernet2"), map[string]any{"admin_status": "down", "oper_status": "not_present"})
		checkAttrs(t, ifObj(t, b, "Ethernet4"), map[string]any{"admin_status": "up", "oper_status": "down"})
		checkAbsent(t, ifObj(t, b, "Ethernet4"), "speed_mbps") // link down: negotiated speed unknown
		// The uplink reports its native VLAN.
		checkAttrs(t, ifObj(t, b, "Ethernet49"), map[string]any{"mode": "trunk", "native_vlan": int64(99), "speed_mbps": int64(40000)})
		checkAttrs(t, ifObj(t, b, "Port-Channel10"), map[string]any{"kind": "lag", "mode": "access", "native_vlan": int64(100)})
	})

	t.Run("learned macs", func(t *testing.T) {
		checkAttrs(t, ifObj(t, b, "Ethernet1"), map[string]any{"learned_macs": []string{"100/02:aa:00:00:03:01"}})
		// Colon format and "learnedDynamicMac" entries; the LAG carries its hosts' MACs,
		// the member ports none; the uplink (5 MACs) is above the limit.
		checkAttrs(t, ifObj(t, b, "Port-Channel10"), map[string]any{"learned_macs": []string{"100/02:aa:00:00:04:01", "100/02:aa:00:00:04:02"}})
		checkAbsent(t, ifObj(t, b, "Ethernet49"), "learned_macs")
		checkAbsent(t, ifObj(t, b, "Ethernet3"), "learned_macs")
	})

	t.Run("relations", func(t *testing.T) {
		checkRels(t, b, "member_of", []wantRel{
			{"if/Ethernet2", "vlan/1", map[string]any{"tagged": false}},
			{"if/Ethernet1", "vlan/100", map[string]any{"tagged": false}},
			{"if/Port-Channel10", "vlan/100", map[string]any{"tagged": false}},
			{"if/Ethernet49", "vlan/100", map[string]any{"tagged": true}},
			{"if/Ethernet49", "vlan/200", map[string]any{"tagged": true}},
			// VLAN 99 is the uplink's native VLAN: untagged.
			{"if/Ethernet49", "vlan/99", map[string]any{"tagged": false}},
			{"if/Vlan100", "vlan/100", nil},
			// Both members, including the one LACP has not bundled.
			{"if/Ethernet3", "if/Port-Channel10", nil},
			{"if/Ethernet4", "if/Port-Channel10", nil},
		})
		checkNeighbors(t, b, map[string]nb{
			"if/Ethernet1":  {map[string]string{"base_mac": "02:aa:00:00:03:01", "name": "srv-03"}, "eno1"}, // interfaceId_v2, unquoted
			"if/Ethernet3":  {map[string]string{"base_mac": "02:aa:00:00:04:01", "name": "srv-04"}, "eth0"}, // MAC port id: the description names it
			"if/Ethernet49": {map[string]string{"base_mac": "02:1c:73:00:00:01", "name": "sw-a1"}, "Ethernet51"},
		})
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "ip_address")); got != 4 {
			t.Errorf("%d ip_address objects, want 4 (no secondary on this switch)", got)
		}
	})

	t.Run("hardware", func(t *testing.T) {
		checkAttrs(t, obj(t, b, "psu", "serial", "FAKEPSU1001"), map[string]any{"capacity_w": int64(500), "health": "ok", "input_w": 178.5})
		// A PSU that lost its input is critical, and says so.
		checkAttrs(t, obj(t, b, "psu", "serial", "FAKEPSU1002"), map[string]any{"health": "critical", "input_w": float64(0)})
		// Trays with a fan list: one fan object per fan, rpm unit is not claimed as percent.
		checkAttrs(t, obj(t, b, "fan", "name", "FanTray1/2"), map[string]any{"speed_pct": float64(40), "health": "ok"})
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "fan")); got != 6 {
			t.Errorf("%d fans, want 6", got)
		}
		checkAttrs(t, devSensor(t, b, dev, "TempSensor2"), map[string]any{"placement": "inlet", "reading": 27.5})
		checkAttrs(t, devSensor(t, b, dev, "TempSensor3"), map[string]any{"placement": "outlet", "reading": float64(38)})
		// A sensor of a card is prefixed so that it cannot collide with a chassis one.
		checkAttrs(t, devSensor(t, b, dev, "Card1 TempSensor1"), map[string]any{"reading": float64(52)})
		x := obj(t, b, "transceiver", "serial", "FAKEXCVR1003")
		if x.Parent != "if/Ethernet3" {
			t.Errorf("transceiver parent = %s", x.Parent)
		}
		// Long interface names in the DOM table, and "-inf" for a cable without optics.
		checkAbsent(t, x, "tx_power_dbm", "rx_power_dbm")
		checkAttrs(t, obj(t, b, "transceiver", "serial", "FAKEXCVR1049"), map[string]any{"tx_power_dbm": float64(-1), "rx_power_dbm": float64(-2)})
		// Slot 2 is empty.
		if got := len(collecttest.Objects([]*sdk.Batch{b}, "transceiver")); got != 4 {
			t.Errorf("%d transceivers, want 4", got)
		}
	})

	t.Run("only reads", func(t *testing.T) { checkReadOnly(t, f) })
}

// devSensor returns the chassis-level sensor of dev with the given name.
func devSensor(t *testing.T, b *sdk.Batch, dev *sdk.Object, name string) *sdk.Object {
	t.Helper()
	for _, o := range collecttest.Objects([]*sdk.Batch{b}, "sensor") {
		if o.Identity["name"] == name && o.Parent == dev.Ref {
			return o
		}
	}
	t.Fatalf("no chassis sensor %q", name)
	return nil
}

type nb struct {
	device map[string]string
	port   string
}

// checkNeighbors asserts that the batch holds exactly the given lldp
// neighbor_of relations: local interface ref -> remote port under a device.
func checkNeighbors(t *testing.T, b *sdk.Batch, want map[string]nb) {
	t.Helper()
	got := map[string]nb{}
	for _, r := range b.Relations {
		if r.Type != "neighbor_of" {
			continue
		}
		if r.To.Type != "interface" || r.To.Parent == nil || r.To.Parent.Type != "device" {
			t.Errorf("neighbor_of target is not interface-under-device: %+v", r.To)
			continue
		}
		if r.Attributes["protocol"] != "lldp" {
			t.Errorf("neighbor_of protocol = %v", r.Attributes["protocol"])
		}
		if len(r.To.Identity) != 1 {
			t.Errorf("remote port identity = %v, want only the name", r.To.Identity)
		}
		got[r.From.Ref] = nb{r.To.Parent.Identity, r.To.Identity["name"]}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lldp neighbors:\n got  %+v\n want %+v", got, want)
	}
}

// checkReadOnly asserts the fake received nothing but show commands, as
// "POST /command-api", authenticated, in JSON format.
func checkReadOnly(t *testing.T, f *FakeArista) {
	t.Helper()
	if ops := f.NonReadOperations(); len(ops) != 0 {
		t.Errorf("the collector did more than read: %v", ops)
	}
	reqs := f.Requests()
	if len(reqs) == 0 {
		t.Fatal("the fake saw no request")
	}
	for _, r := range reqs {
		if !r.AuthOK || r.User != FakeUsername || r.Format != "json" || r.Version != 1 {
			t.Errorf("request %+v", r)
		}
		for _, c := range r.Commands {
			if !strings.HasPrefix(c, "show ") {
				t.Errorf("command %q is not a show command", c)
			}
		}
	}
	// Every command of the groups is asked, none outside Commands(); a group
	// holding a command the release has no JSON for is replayed one command per
	// request, so a command is asked at most twice (TestCleanRun pins the count
	// of a run with nothing to replay).
	known := map[string]bool{}
	for _, c := range Commands() {
		known[c] = true
	}
	asked := map[string]int{}
	for _, c := range f.Commands() {
		if !known[c] {
			t.Errorf("command %q is not in Commands()", c)
		}
		asked[c]++
	}
	for _, g := range groups {
		for _, c := range g {
			if asked[c] < 1 || asked[c] > 2 {
				t.Errorf("%s asked %d times", c, asked[c])
			}
		}
	}
}

// TestCleanRun pins the request count of a release that has JSON output for
// every command: one request per group, every command asked once, no fallback.
func TestCleanRun(t *testing.T) {
	b, f := runWith(t, EOS420, func(f *FakeArista) { f.ConvertAll() }, nil)
	if b.Scope != sdk.ScopeFull || !b.Health.OK || len(b.Health.Warnings) != 0 {
		t.Fatalf("scope=%v ok=%v warnings=%q", b.Scope, b.Health.OK, b.Health.Warnings)
	}
	reqs, got := f.Requests(), f.Commands()
	want := len(Commands()) - len(fallbacks)
	if len(reqs) != len(groups) || len(got) != want {
		t.Errorf("%d requests, %d commands; want %d and %d", len(reqs), len(got), len(groups), want)
	}
	// show uptime answered: it is the source of the uptime.
	checkAttrs(t, obj(t, b, "device", "serial", "FAKE7280B0001"), map[string]any{"uptime_s": int64(2234567)})
}

// TestUptime pins where the uptime comes from on each release.
func TestUptime(t *testing.T) {
	ver := func(up, boot string) *showVersion {
		var v showVersion
		if err := json.Unmarshal([]byte(`{"uptime": `+up+`, "bootupTimestamp": `+boot+`}`), &v); err != nil {
			t.Fatal(err)
		}
		return &v
	}
	var up showUptime
	_ = json.Unmarshal([]byte(`{"upTime": 10.5}`), &up)
	for _, tc := range []struct {
		name string
		up   *showUptime
		ver  *showVersion
		now  time.Time
		want int64
		ok   bool
	}{
		{"show uptime wins", &up, ver("20", "1000"), time.Unix(5000, 0), 10, true},
		{"show version uptime (4.20)", nil, ver("20", "1000"), time.Unix(5000, 0), 20, true},
		{"boot time (4.14)", nil, ver("null", "1000.9"), time.Unix(5000, 0), 4000, true},
		{"boot time in the future", nil, ver("null", "9000"), time.Unix(5000, 0), 0, false},
		{"nothing", nil, ver("null", "null"), time.Unix(5000, 0), 0, false},
	} {
		got, ok := uptime(tc.up, tc.ver, tc.now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: uptime = %d, %v; want %d, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestOptions(t *testing.T) {
	// max_edge_macs: the number of MACs above which a port is not an edge port.
	tests := []struct {
		name      string
		opts      string
		wantEth2  bool // Ethernet2 holds 2 MACs
		wantEth51 bool // Ethernet51 holds 5 MACs
	}{
		{"default is 4", "", true, false},
		{"0 turns it off", "max_edge_macs: 0", false, false},
		{"1 drops two-MAC ports", "max_edge_macs: 1", false, false},
		{"5 keeps the trunk", "max_edge_macs: 5", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := collectFrom(t, EOS414, func(tg *collect.Target) {
				if tc.opts != "" {
					tg.Options = mustOptions(tc.opts)
				}
			})
			if _, got := ifObj(t, b, "Ethernet2").Attributes["learned_macs"]; got != tc.wantEth2 {
				t.Errorf("Ethernet2 learned_macs present = %v, want %v", got, tc.wantEth2)
			}
			if _, got := ifObj(t, b, "Ethernet51").Attributes["learned_macs"]; got != tc.wantEth51 {
				t.Errorf("Ethernet51 learned_macs present = %v, want %v", got, tc.wantEth51)
			}
		})
	}

	t.Run("vlan_domain", func(t *testing.T) {
		b, _ := collectFrom(t, EOS414, func(tg *collect.Target) { tg.Options = mustOptions("vlan_domain: dc1") })
		v := obj(t, b, "vlan", "key", "dc1:100")
		checkAttrs(t, v, map[string]any{"domain": "dc1", "vid": int64(100), "name": "SERVERS"})
		if collecttest.Find([]*sdk.Batch{b}, "vlan", "key", "default:100") != nil {
			t.Error("a vlan is still filed in the default domain")
		}
	})
}

func TestNewRejects(t *testing.T) {
	base := func() collect.Target {
		return collect.Target{Kind: Kind, Name: "sw", Address: "192.0.2.11", Username: "trica", Password: "env:X"}
	}
	tests := []struct {
		name string
		mod  func(*collect.Target)
		want string
	}{
		{"no address", func(t *collect.Target) { t.Address = "" }, "no address"},
		{"bad scheme", func(t *collect.Target) { t.Address = "ssh://192.0.2.11" }, "scheme"},
		{"no host", func(t *collect.Target) { t.Address = "https://" }, "no host"},
		{"no username", func(t *collect.Target) { t.Username = "" }, "no username"},
		{"unknown option", func(t *collect.Target) { t.Options = mustOptions("max_edge: 3") }, "max_edge"},
		{"negative max_edge_macs", func(t *collect.Target) { t.Options = mustOptions("max_edge_macs: -1") }, "negative"},
		{"bad tls version", func(t *collect.Target) { t.TLS.MinVersion = "9.9" }, "min_version"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := base()
			tc.mod(&tg)
			_, err := New(tg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestRegistration(t *testing.T) {
	t.Setenv(PasswordEnv, FakePassword)
	f := NewFakeArista(EOS414)
	defer f.Close()
	c, err := collect.New(f.Target("sw"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != Kind || Kind != "arista" {
		t.Errorf("kind = %q", c.Kind())
	}
	if _, ok := c.(*Collector); !ok {
		t.Errorf("collect.New returned %T", c)
	}
}

// asked reports whether a run against version sends cmd.
func asked(version, cmd string) bool {
	for orig, fb := range fallbacks {
		if fb == cmd {
			return unconverted[version][orig]
		}
	}
	return true
}

func TestCommandsAreAllShow(t *testing.T) {
	cmds := Commands()
	if len(cmds) != 18 {
		t.Errorf("%d commands: %v", len(cmds), cmds)
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		if !isShow(c) {
			t.Errorf("%q is not a show command", c)
		}
		if seen[c] {
			t.Errorf("%q listed twice", c)
		}
		seen[c] = true
		// Every command a modelled version is asked has a fixture there, or is
		// one that version has no JSON output for. A fallback is asked only
		// where the command it replaces has none.
		for _, v := range []string{EOS414, EOS420} {
			if unconverted[v][c] || !asked(v, c) {
				continue
			}
			if _, err := fixtures.ReadFile(fixturePath(v, c)); err != nil {
				t.Errorf("%s: %v", v, err)
			}
		}
	}
}

// What a switch says about its airflow (ADR 0049): the direction EOS reports
// for the chassis, and the part numbers of its fan trays and power supplies.
// The 4.20 fixture has its fan trays swapped (-R) against its power supplies
// (-RED) and its chassis direction: each fact is reported as it is, the
// airflow check is what says they disagree.
func TestAirflowFacts(t *testing.T) {
	t.Run("4.14: no chassis direction, forward parts", func(t *testing.T) {
		b, _ := collectFrom(t, EOS414, nil)
		if a, ok := collecttest.Objects([]*sdk.Batch{b}, "device")[0].Attributes["airflow"]; ok {
			t.Errorf("device airflow %v on a release that reports none", a)
		}
		checkAttrs(t, obj(t, b, "fan", "name", "FanTray1"), map[string]any{"airflow": "front_to_rear"})
		checkAttrs(t, obj(t, b, "psu", "serial", "FAKEPSU0001"), map[string]any{"airflow": "front_to_rear"})
	})
	t.Run("4.20: chassis direction, fan trays turned the other way", func(t *testing.T) {
		b, _ := collectFrom(t, EOS420, nil)
		dev := collecttest.Objects([]*sdk.Batch{b}, "device")
		if len(dev) != 1 || dev[0].Attributes["airflow"] != "front_to_rear" {
			t.Fatalf("device airflow: %+v", dev)
		}
		checkAttrs(t, obj(t, b, "fan", "name", "FanTray1/1"), map[string]any{"airflow": "rear_to_front"})
		checkAttrs(t, obj(t, b, "fan", "name", "FanTray2/2"), map[string]any{"airflow": "rear_to_front"})
		checkAttrs(t, obj(t, b, "psu", "serial", "FAKEPSU1001"), map[string]any{"airflow": "front_to_rear"})
		if a, ok := obj(t, b, "fan", "name", "PowerSupply1/1").Attributes["airflow"]; ok {
			t.Errorf("a PSU's own fan got airflow %v: only the PSU's part number says it", a)
		}
	})
}

func TestPartAirflow(t *testing.T) {
	for in, want := range map[string]string{
		"FAN-7000-F": "front_to_rear", "fan-7000-r": "rear_to_front", "PWR-511-AC-RED": "front_to_rear", "PWR-511-AC-BLUE": "rear_to_front",
		"PWR-460AC-F": "front_to_rear", "DCS-7280SE-64": "", "": "", "FAN-7000": "",
	} {
		if got := partAirflow(in); got != want {
			t.Errorf("partAirflow(%q) = %q, want %q", in, got, want)
		}
	}
}

// A direction EOS spells in a way not known here is left out, and said.
func TestAirflowDirectionNotUnderstood(t *testing.T) {
	d := &data{raw: map[string]json.RawMessage{cmdCooling: json.RawMessage(`{"systemStatus":"coolingOk","airflowDirection":"sidewaysAirflow","fanTraySlots":[]}`)}, failed: map[string]error{}}
	bd := &builder{b: sdk.NewBatch("t", sdk.ScopeFull), d: d, psuRef: map[string]string{}}
	bd.b.Object("dev", "device")
	bd.cooling()
	if a, ok := bd.b.Object("dev", "device").Attributes["airflow"]; ok {
		t.Errorf("device airflow %v from an unknown spelling", a)
	}
	if len(bd.warnings) != 1 || !strings.Contains(bd.warnings[0], "sidewaysAirflow") {
		t.Errorf("warnings %q, want the unknown direction said", bd.warnings)
	}
}
