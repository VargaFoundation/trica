package mikrotik

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The system package of RouterOS is named after the OS itself.
const osPkg = "routeros" //nolint:misspell // a RouterOS package name, not prose

const (
	swOS  = "sw:" + osPkg + "/7.8"
	swARM = "sw:" + osPkg + "-arm/6.49.10"
)

// newTarget builds a target for the fake. options is a YAML flow mapping
// ("{tls: true}") or empty; tweak may adjust the target afterwards.
func newTarget(t *testing.T, addr, options string, tweak ...func(*collect.Target)) collect.Target {
	t.Helper()
	t.Setenv("MIKROTIK_TEST_PASSWORD", FakePassword)
	doc := fmt.Sprintf("kind: mikrotik\nname: sw-a1\naddress: %q\nusername: %s\npassword: env:MIKROTIK_TEST_PASSWORD\ntimeout: 5s\n",
		addr, FakeUser)
	if options != "" {
		doc += "options: " + options + "\n"
	}
	var tg collect.Target
	if err := yaml.Unmarshal([]byte(doc), &tg); err != nil {
		t.Fatalf("target yaml: %v", err)
	}
	for _, f := range tweak {
		f(&tg)
	}
	return tg
}

func startFake(t *testing.T, version string, opts ...FakeOption) *FakeMikroTik {
	t.Helper()
	f, err := NewFake(version, opts...)
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// runOnce runs the collector for tg and returns the single batch it emits.
func runOnce(t *testing.T, tg collect.Target) *sdk.Batch {
	t.Helper()
	c, err := New(tg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	batches := collecttest.Run(t, c)
	if len(batches) != 1 {
		t.Fatalf("want exactly one batch, got %d", len(batches))
	}
	b := batches[0]
	if b.Source != "mikrotik:sw-a1" {
		t.Errorf("source = %q", b.Source)
	}
	if b.Health == nil || b.Health.Collector != Kind || b.Health.Target != "sw-a1" {
		t.Errorf("health = %+v", b.Health)
	}
	return b
}

func obj(t *testing.T, b *sdk.Batch, ref string) *sdk.Object {
	t.Helper()
	o, ok := b.Lookup(ref)
	if !ok {
		t.Fatalf("no object %q in the batch", ref)
	}
	return o
}

func wantAttrs(t *testing.T, b *sdk.Batch, ref string, want map[string]any) {
	t.Helper()
	o := obj(t, b, ref)
	for k, w := range want {
		if got, ok := o.Attributes[k]; !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("%s.%s = %#v (present %v), want %#v", ref, k, got, ok, w)
		}
	}
}

func noAttrs(t *testing.T, b *sdk.Batch, ref string, keys ...string) {
	t.Helper()
	o := obj(t, b, ref)
	for _, k := range keys {
		if v, ok := o.Attributes[k]; ok {
			t.Errorf("%s.%s = %#v, want it absent", ref, k, v)
		}
	}
}

func noObj(t *testing.T, b *sdk.Batch, refs ...string) {
	t.Helper()
	for _, r := range refs {
		if _, ok := b.Lookup(r); ok {
			t.Errorf("object %q is in the batch, want it absent", r)
		}
	}
}

// rel returns the relation of type typ between two refs, or nil.
func rel(b *sdk.Batch, typ, from, to string) *sdk.Relation {
	for _, r := range b.Relations {
		if r.Type == typ && r.From.Ref == from && r.To.Ref == to {
			return r
		}
	}
	return nil
}

func wantRel(t *testing.T, b *sdk.Batch, typ, from, to string) *sdk.Relation {
	t.Helper()
	r := rel(b, typ, from, to)
	if r == nil {
		t.Fatalf("no %s relation %s -> %s", typ, from, to)
	}
	return r
}

// neighbor finds the neighbor_of relation from a local interface ref to the
// remote (device name, port name).
func neighbor(b *sdk.Batch, from, device, port string) *sdk.Relation {
	for _, r := range b.Relations {
		if r.Type == "neighbor_of" && r.From.Ref == from && r.To.Identity["name"] == port &&
			r.To.Parent != nil && r.To.Parent.Identity["name"] == device {
			return r
		}
	}
	return nil
}

func TestRegistered(t *testing.T) {
	if !slices.Contains(collect.Kinds(), Kind) {
		t.Fatalf("%q is not registered: %v", Kind, collect.Kinds())
	}
	f := startFake(t, FakeCRS354v78)
	c, err := collect.New(newTarget(t, f.Addr(), ""))
	if err != nil || c.Kind() != Kind {
		t.Fatalf("collect.New: %v, %v", c, err)
	}
}

func TestCRS354RouterOS78(t *testing.T) {
	f := startFake(t, FakeCRS354v78)
	b := runOnce(t, newTarget(t, f.Addr(), ""))

	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Error != "" {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	if b.Health.Version != "7.8" || b.Health.Items != len(b.Objects)+len(b.Relations) {
		t.Errorf("health version/items = %q/%d", b.Health.Version, b.Health.Items)
	}
	if len(b.Health.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", b.Health.Warnings)
	}

	t.Run("device", func(t *testing.T) {
		d := obj(t, b, devRef)
		wantID := map[string]string{"serial": "FAKE000001", "base_mac": "02:11:22:33:44:01", "name": "sw-a1", "mgmt_ip": "127.0.0.1"}
		if !reflect.DeepEqual(d.Identity, wantID) {
			t.Errorf("identity = %v, want %v", d.Identity, wantID)
		}
		wantAttrs(t, b, devRef, map[string]any{
			"role": "switch", "vendor": "MikroTik", "model": "CRS354-48G-4S+2Q+", "os_name": "RouterOS",
			"os_version": "7.8", "bios_version": "7.8", "uptime_s": 3*7*86400 + 2*86400 + 4*3600 + 5*60 + 6,
			"memory_bytes": 1073741824, "cpu_count": 2, "power_w": 58.2,
		})
	})

	t.Run("interfaces", func(t *testing.T) {
		wantAttrs(t, b, "if:ether1", map[string]any{
			"kind": "physical", "mac": "02:11:22:33:44:01", "mtu": 1500, "admin_status": "up", "oper_status": "up",
			"description": "management host", "speed_mbps": 1000, "duplex": "full",
		})
		if o := obj(t, b, "if:ether1"); o.Parent != devRef || o.Identity["name"] != "ether1" {
			t.Errorf("ether1 parent/identity = %q/%v", o.Parent, o.Identity)
		}
		wantAttrs(t, b, "if:ether3", map[string]any{"oper_status": "down", "description": "spare"})
		noAttrs(t, b, "if:ether3", "speed_mbps", "duplex")
		wantAttrs(t, b, "if:sfp-sfpplus4", map[string]any{"admin_status": "down", "oper_status": "down"})
		wantAttrs(t, b, "if:bond0", map[string]any{"kind": "lag"})
		wantAttrs(t, b, "if:bridge", map[string]any{"kind": "virtual"})
		wantAttrs(t, b, "if:vlan99-mgmt", map[string]any{"kind": "vlan"})
		wantAttrs(t, b, "if:lo", map[string]any{"kind": "loopback", "mtu": 65536})
		noAttrs(t, b, "if:lo", "mac")
		wantAttrs(t, b, "if:qsfpplus1-1", map[string]any{"kind": "physical"})
	})

	t.Run("aggregation and bridge membership", func(t *testing.T) {
		wantRel(t, b, "member_of", "if:sfp-sfpplus1", "if:bond0")
		wantRel(t, b, "member_of", "if:sfp-sfpplus2", "if:bond0")
		wantRel(t, b, "member_of", "if:ether1", "if:bridge")
		wantRel(t, b, "member_of", "if:bond0", "if:bridge")
		if rel(b, "member_of", "if:ether48", "if:bridge") != nil {
			t.Error("a disabled bridge port must not be a member")
		}
	})

	t.Run("vlans", func(t *testing.T) {
		for _, vid := range []string{"1", "10", "20", "99", "200", "201", "202"} {
			o := obj(t, b, "vlan:"+vid)
			if o.Identity["key"] != "default:"+vid || fmt.Sprint(o.Attributes["vid"]) != vid || o.Attributes["domain"] != "default" {
				t.Errorf("vlan %s = %v %v", vid, o.Identity, o.Attributes)
			}
		}
		noObj(t, b, "vlan:203", "vlan:30")
		wantAttrs(t, b, "vlan:10", map[string]any{"name": "servers"})
		wantAttrs(t, b, "vlan:99", map[string]any{"name": "mgmt"})
		noAttrs(t, b, "vlan:200", "name") // a range cannot share one name
		tagged := func(from, to string) any {
			return wantRel(t, b, "member_of", from, to).Attributes["tagged"]
		}
		for _, c := range []struct {
			from, to string
			tagged   bool
		}{
			{"if:bond0", "vlan:20", true}, {"if:ether2", "vlan:20", false}, {"if:ether4", "vlan:20", false},
			{"if:ether5", "vlan:10", true}, {"if:sfp-sfpplus3", "vlan:10", true},
			{"if:bridge", "vlan:99", true}, {"if:ether1", "vlan:99", false},
			{"if:bond0", "vlan:201", true}, {"if:ether5", "vlan:1", false},
			{"if:vlan99-mgmt", "vlan:99", true},
		} {
			if got := tagged(c.from, c.to); got != c.tagged {
				t.Errorf("%s in %s: tagged = %v, want %v", c.from, c.to, got, c.tagged)
			}
		}
		if rel(b, "member_of", "if:ether2", "vlan:10") != nil {
			t.Error("ether2 is not in VLAN 10")
		}
	})

	t.Run("port modes", func(t *testing.T) {
		wantAttrs(t, b, "if:ether2", map[string]any{"mode": "access", "native_vlan": 20})
		wantAttrs(t, b, "if:ether1", map[string]any{"mode": "access", "native_vlan": 99})
		wantAttrs(t, b, "if:bond0", map[string]any{"mode": "trunk"})
		noAttrs(t, b, "if:bond0", "native_vlan") // admit-only-vlan-tagged: no untagged VLAN
		wantAttrs(t, b, "if:ether5", map[string]any{"mode": "trunk", "native_vlan": 1})
		noAttrs(t, b, "if:ether48", "mode")
		noAttrs(t, b, "if:sfp-sfpplus1", "mode") // a LAG member, not a bridge port
	})

	t.Run("addresses", func(t *testing.T) {
		a := obj(t, b, "ip:192.0.2.10")
		if a.Identity["address"] != "192.0.2.10" || a.Attributes["prefix_length"] != 24 {
			t.Errorf("address = %v %v", a.Identity, a.Attributes)
		}
		wantRel(t, b, "assigned_to", "ip:192.0.2.10", "if:vlan99-mgmt")
		wantRel(t, b, "assigned_to", "ip:198.51.100.1", "if:lo")
		wantAttrs(t, b, "ip:198.51.100.1", map[string]any{"prefix_length": 32})
		noObj(t, b, "ip:203.0.113.5") // disabled
	})

	t.Run("neighbors", func(t *testing.T) {
		for _, c := range []struct{ from, dev, port, proto string }{
			{"if:sfp-sfpplus1", "core-1", "Ethernet12", "lldp"}, // the cabled port, not bond0 or bridge
			{"if:ether2", "srv-01", "eno1", "lldp"},
			{"if:ether5", "ap-1", "ether1", "lldp"}, // mndp,lldp,cdp: LLDP preferred
			{"if:ether4", "phone-01", "Port 1", "cdp"},
		} {
			r := neighbor(b, c.from, c.dev, c.port)
			if r == nil {
				t.Errorf("no neighbor_of %s -> %s/%s", c.from, c.dev, c.port)
				continue
			}
			if r.Attributes["protocol"] != c.proto {
				t.Errorf("%s: protocol = %v, want %s", c.dev, r.Attributes["protocol"], c.proto)
			}
			if r.To.Type != "interface" || r.To.Parent.Type != "device" || len(r.To.Parent.Identity) != 1 {
				t.Errorf("%s: remote endpoint = %+v / %+v", c.dev, r.To, r.To.Parent)
			}
		}
		n := 0
		for _, r := range b.Relations {
			if r.Type == "neighbor_of" {
				n++
			}
		}
		if n != 4 { // laptop-9 (MNDP, no port) and orphan-x are not links
			t.Errorf("%d neighbor_of relations, want 4", n)
		}
	})

	t.Run("learned macs", func(t *testing.T) {
		wantAttrs(t, b, "if:ether1", map[string]any{"learned_macs": []string{
			"99/02:aa:00:00:63:01", "99/02:aa:00:00:63:02", "99/02:aa:00:00:63:03", "99/02:aa:00:00:63:04",
		}})
		noAttrs(t, b, "if:ether5", "learned_macs") // 5 addresses: over the edge limit of 4
		noAttrs(t, b, "if:bond0", "learned_macs")  // a trunk
		noAttrs(t, b, "if:bridge", "learned_macs")
		// the invalid entry (02:AA:00:00:14:02) of ether2 is dropped, the valid one kept
		wantAttrs(t, b, "if:ether2", map[string]any{"learned_macs": []string{"20/02:aa:00:00:14:01"}})
	})

	t.Run("health sensors", func(t *testing.T) {
		wantAttrs(t, b, "sensor:device:cpu-temperature", map[string]any{
			"kind": "temperature", "placement": "cpu", "reading": 47.0, "unit": "°C"})
		wantAttrs(t, b, "sensor:device:board-temperature1", map[string]any{"placement": "system", "reading": 41.0})
		if o := obj(t, b, "sensor:device:cpu-temperature"); o.Parent != devRef || o.Identity["name"] != "cpu-temperature" {
			t.Errorf("sensor parent/identity = %q/%v", o.Parent, o.Identity)
		}
		wantAttrs(t, b, "fan:fan1", map[string]any{"speed_rpm": 5400})
		wantAttrs(t, b, "fan:fan2", map[string]any{"speed_rpm": 5310})
		wantAttrs(t, b, "psu:psu1", map[string]any{"health": "ok"})
		wantAttrs(t, b, "psu:psu2", map[string]any{"health": "critical"})
		wantAttrs(t, b, "sensor:psu:psu1:voltage", map[string]any{"kind": "voltage", "placement": "psu", "reading": 12.1, "unit": "V"})
		wantAttrs(t, b, "sensor:psu:psu1:current", map[string]any{"kind": "current", "reading": 2.4, "unit": "A"})
		if o := obj(t, b, "sensor:psu:psu1:voltage"); o.Parent != "psu:psu1" {
			t.Errorf("psu sensor parent = %q", o.Parent)
		}
	})

	t.Run("software", func(t *testing.T) {
		wantAttrs(t, b, swOS, map[string]any{"kind": "package", "name": osPkg, "version": "7.8", "vendor": "MikroTik"})
		if o := obj(t, b, swOS); o.Identity["key"] != "package/"+osPkg+"/7.8" {
			t.Errorf("software key = %v", o.Identity)
		}
		wantRel(t, b, "runs", devRef, swOS)
		noObj(t, b, "sw:container/7.8") // disabled package
	})
}

func TestCRS326RouterOS649(t *testing.T) {
	f := startFake(t, FakeCRS326v649)
	b := runOnce(t, newTarget(t, f.Addr(), ""))

	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Version != "6.49.10" {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	d := obj(t, b, devRef)
	if d.Identity["serial"] != "FAKE000002" || d.Identity["name"] != "sw-b1" || d.Identity["base_mac"] != "02:11:22:33:55:01" {
		t.Errorf("identity = %v", d.Identity)
	}
	wantAttrs(t, b, devRef, map[string]any{"role": "switch", "model": "CRS326-24G-2S+", "os_version": "6.49.10", "os_name": "RouterOS"})

	// v6 has no actual-mtu: the configured mtu is used
	wantAttrs(t, b, "if:ether1", map[string]any{"mtu": 1500, "kind": "physical"})
	wantRel(t, b, "member_of", "if:sfp-sfpplus1", "if:bond1")
	wantRel(t, b, "member_of", "if:bond1", "if:bridge1")
	wantAttrs(t, b, "if:ether2", map[string]any{"mode": "access", "native_vlan": 20})
	wantAttrs(t, b, "if:bond1", map[string]any{"mode": "trunk", "kind": "lag"})
	wantAttrs(t, b, "vlan:20", map[string]any{"name": "users"})
	if wantRel(t, b, "member_of", "if:bridge1", "vlan:99").Attributes["tagged"] != true {
		t.Error("the bridge is a tagged member of VLAN 99")
	}
	wantRel(t, b, "assigned_to", "ip:192.0.2.30", "if:vlan99")

	// No discovered-by before 7.7: the link is reported without a protocol.
	r := neighbor(b, "if:sfp-sfpplus1", "core-1", "Ethernet13")
	if r == nil {
		t.Fatal("no neighbor core-1")
	}
	if _, has := r.Attributes["protocol"]; has {
		t.Errorf("protocol = %v on a version that does not report it", r.Attributes["protocol"])
	}
	if neighbor(b, "if:ether2", "srv-02", "eno2") == nil {
		t.Error("no neighbor srv-02")
	}

	// Edge ports only: ether1 (1 address) and ether2 (2) but not the bond (6).
	wantAttrs(t, b, "if:ether1", map[string]any{"learned_macs": []string{"99/02:aa:00:00:63:01"}})
	if got := obj(t, b, "if:ether2").Attributes["learned_macs"].([]string); len(got) != 2 {
		t.Errorf("ether2 learned_macs = %v", got)
	}
	noAttrs(t, b, "if:bond1", "learned_macs")

	// The flat health shape: sensors are properties of one row, voltage in dV.
	wantAttrs(t, b, "sensor:device:voltage", map[string]any{"kind": "voltage", "reading": 24.0, "unit": "V"})
	wantAttrs(t, b, "sensor:device:temperature", map[string]any{"kind": "temperature", "reading": 38.0, "placement": "system"})
	wantAttrs(t, b, "sensor:device:cpu-temperature", map[string]any{"reading": 44.0, "placement": "cpu"})
	wantAttrs(t, b, "psu:psu1", map[string]any{"health": "ok"})
	wantAttrs(t, b, "psu:psu2", map[string]any{"health": "ok"})
	wantAttrs(t, b, swARM, map[string]any{"kind": "package"})
	noObj(t, b, "sw:hotspot/6.49.10")
}

// TestOnlyReads is the guard of the read-only contract: whatever a run does,
// the router receives /login and "print" commands, with no parameter but the
// credentials, and the password never reaches the record.
func TestOnlyReads(t *testing.T) {
	for _, v := range FakeVersions() {
		t.Run(v, func(t *testing.T) {
			f := startFake(t, v)
			runOnce(t, newTarget(t, f.Addr(), ""))
			seen := map[string]bool{}
			logins := 0
			for _, r := range f.Requests() {
				switch {
				case r.Command == "/login":
					logins++
					if !reflect.DeepEqual(r.Args, []string{"=name=" + FakeUser, "=password=***"}) {
						t.Errorf("login args = %v", r.Args)
					}
				case strings.HasSuffix(r.Command, "/print") && len(r.Args) == 0:
					seen[r.Command] = true
				default:
					t.Errorf("not a read-only request: %+v", r)
				}
			}
			if logins != 1 {
				t.Errorf("%d logins, want 1", logins)
			}
			for cmd := range readCommands {
				if !seen[cmd] {
					t.Errorf("%s was never sent", cmd)
				}
			}
			if len(seen) != len(readCommands) {
				t.Errorf("sent %d distinct commands, allowlist has %d", len(seen), len(readCommands))
			}
		})
	}
}

// TestSessionRefusesWrites: the allowlist is enforced before anything is sent.
func TestSessionRefusesWrites(t *testing.T) {
	f := startFake(t, FakeCRS354v78)
	ctx := t.Context()
	s, err := dial(ctx, f.Addr(), FakeUser, FakePassword, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for _, cmd := range []string{
		"/system/reboot", "/system/reset-configuration", "/interface/set", "/user/add",
		"/interface/ethernet/monitor", "/system/identity/set", "", "/interface/print ", "/export",
	} {
		if _, err := s.print(ctx, cmd); err == nil || !strings.Contains(err.Error(), errNotReadOnly.Error()) {
			t.Errorf("print(%q) error = %v, want not-read-only", cmd, err)
		}
	}
	if reqs := f.Requests(); len(reqs) != 1 || reqs[0].Command != "/login" {
		t.Errorf("requests = %+v, want only the login", reqs)
	}
	if rows, err := s.print(ctx, cmdIdentity); err != nil || len(rows) != 1 || rows[0].str("name") != "sw-a1" {
		t.Errorf("an allowed command still works: %v %v", rows, err)
	}
}

func TestFailures(t *testing.T) {
	short := func(tg *collect.Target) { tg.Timeout = 400 * time.Millisecond }
	cases := []struct {
		name    string
		opts    []FakeOption
		options string
		tweak   []func(*collect.Target)
		// wantScope is partial in every failure; failed means no data at all.
		failed   bool
		errPart  string
		warnPart string
		have     []string // refs that must still be reported (partial runs)
		lack     []string
	}{
		{
			name: "wrong password", opts: []FakeOption{WithCredentials(FakeUser, "other")}, failed: true,
			errPart: "invalid user name or password",
		},
		{
			name: "login hangs", opts: []FakeOption{Hang("/login")}, tweak: []func(*collect.Target){short}, failed: true,
			errPart: "login",
		},
		{
			name: "no identity at all",
			opts: []FakeOption{
				FailCommand(cmdIdentity, "not enough permissions (9)"), FailCommand(cmdRouterboard, "x"),
				FailCommand(cmdEthernet, "x"), FailCommand(cmdInterface, "x"), FailCommand(cmdBridge, "x"),
			},
			failed: true, errPart: "no device identity",
		},
		{
			name: "one command refused", opts: []FakeOption{FailCommand(cmdBridgeVLAN, "not enough permissions (9)")},
			errPart: cmdBridgeVLAN, have: []string{devRef, "if:ether1", "if:ether2", "ip:192.0.2.10"}, lack: []string{"vlan:20"},
		},
		{
			name: "command missing on this version", opts: []FakeOption{WithoutCommand(cmdHealth)},
			errPart: "no such command", have: []string{devRef, "vlan:20"}, lack: []string{"fan:fan1"},
		},
		{
			name: "malformed reply", opts: []FakeOption{MalformedReply(cmdBridgeHost)},
			errPart: "connection lost", have: []string{devRef, "vlan:20", "if:ether1"}, lack: []string{"fan:fan1", "ip:192.0.2.10"},
		},
		{
			name: "fatal", opts: []FakeOption{FatalOn(cmdAddress)},
			errPart: "connection lost", have: []string{devRef, "vlan:20"}, lack: []string{"fan:fan1"},
		},
		{
			name: "router stops answering", opts: []FakeOption{Hang(cmdBridgePort)}, tweak: []func(*collect.Target){short},
			errPart: "timeout|deadline exceeded", have: []string{devRef, "if:ether1", swOS}, lack: []string{"vlan:20", "fan:fan1"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := startFake(t, FakeCRS354v78, c.opts...)
			start := time.Now()
			b := runOnce(t, newTarget(t, f.Addr(), c.options, c.tweak...))
			if time.Since(start) > 4*time.Second {
				t.Errorf("run took %s: a timeout is not bounding the run", time.Since(start))
			}
			if b.Scope != sdk.ScopePartial {
				t.Errorf("scope = %s: a failed or incomplete run must not withdraw anything", b.Scope)
			}
			if b.Health.OK || !containsAny(b.Health.Error, strings.Split(c.errPart, "|")) {
				t.Errorf("health = %+v, want not OK with %q", b.Health, c.errPart)
			}
			if c.failed && (len(b.Objects) != 0 || len(b.Relations) != 0) {
				t.Errorf("a failed run reported %d objects", len(b.Objects))
			}
			for _, ref := range c.have {
				obj(t, b, ref)
			}
			noObj(t, b, c.lack...)
			if strings.Contains(b.Health.Error, FakePassword) {
				t.Error("the password leaks in the health error")
			}
		})
	}
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func TestConnectionRefused(t *testing.T) {
	f := startFake(t, FakeCRS354v78)
	addr := f.Addr()
	_ = f.Close()
	b := runOnce(t, newTarget(t, addr, ""))
	if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, "connect") || len(b.Objects) != 0 {
		t.Errorf("scope %s, health %+v", b.Scope, b.Health)
	}
}

func TestSecretFailure(t *testing.T) {
	f := startFake(t, FakeCRS354v78)
	b := runOnce(t, newTarget(t, f.Addr(), "", func(tg *collect.Target) { tg.Password = "env:MIKROTIK_TEST_UNSET_VARIABLE" }))
	if b.Health.OK || !strings.Contains(b.Health.Error, "credentials") || len(f.Requests()) != 0 {
		t.Errorf("health %+v, requests %v", b.Health, f.Requests())
	}
}

func TestTLS(t *testing.T) {
	f := startFake(t, FakeCRS354v78, WithTLS())
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, f.CAPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		tweak func(*collect.Target)
		ok    bool
	}{
		{"trusted CA file", func(tg *collect.Target) { tg.TLS.CAFile = caFile }, true},
		{"insecure", func(tg *collect.Target) { tg.TLS.Insecure = true }, true},
		{"untrusted certificate", func(*collect.Target) {}, false},
		{"minimum version 1.3", func(tg *collect.Target) { tg.TLS.CAFile = caFile; tg.TLS.MinVersion = "1.3" }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := runOnce(t, newTarget(t, f.Addr(), "{tls: true}", c.tweak))
			if c.ok != b.Health.OK {
				t.Fatalf("health = %+v, want ok=%v", b.Health, c.ok)
			}
			if c.ok {
				obj(t, b, devRef)
			} else if !strings.Contains(b.Health.Error, "certificate") {
				t.Errorf("error = %q, want a certificate error", b.Health.Error)
			}
		})
	}
	// TLS asked, plain server: the failure is reported, not hung on.
	plain := startFake(t, FakeCRS354v78)
	b := runOnce(t, newTarget(t, plain.Addr(), "{tls: true}", func(tg *collect.Target) { tg.TLS.Insecure = true; tg.Timeout = time.Second }))
	if b.Health.OK {
		t.Error("TLS against a plain listener must fail")
	}
	// A bad TLS setting is a failed run, not a panic.
	b = runOnce(t, newTarget(t, f.Addr(), "{tls: true}", func(tg *collect.Target) { tg.TLS.MinVersion = "9" }))
	if b.Health.OK || !strings.Contains(b.Health.Error, "min_version") {
		t.Errorf("health = %+v", b.Health)
	}
}

func TestOptions(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		f := startFake(t, FakeCRS354v78, FailCommand(cmdHealth, "x"), FailCommand(cmdNeighbor, "x"))
		b := runOnce(t, newTarget(t, f.Addr(), "{skip: [health, neighbor, host, package]}"))
		if !b.Health.OK || b.Scope != sdk.ScopeFull {
			t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
		}
		for _, r := range f.Requests() {
			switch r.Command {
			case cmdHealth, cmdNeighbor, cmdBridgeHost, cmdPackage:
				t.Errorf("%s was sent although skipped", r.Command)
			}
		}
		noObj(t, b, "fan:fan1", swOS)
		noAttrs(t, b, "if:ether1", "learned_macs")
	})
	t.Run("max_edge_macs", func(t *testing.T) {
		f := startFake(t, FakeCRS354v78)
		b := runOnce(t, newTarget(t, f.Addr(), "{max_edge_macs: 8}"))
		wantAttrs(t, b, "if:ether5", map[string]any{"learned_macs": obj(t, b, "if:ether5").Attributes["learned_macs"]})
		if got := obj(t, b, "if:bond0").Attributes["learned_macs"].([]string); len(got) != 8 {
			t.Errorf("bond0 learned_macs = %v, want 8 entries with a limit of 8", got)
		}
		b = runOnce(t, newTarget(t, f.Addr(), "{max_edge_macs: 0}"))
		noAttrs(t, b, "if:ether1", "learned_macs")
	})
	t.Run("role and vlan domain", func(t *testing.T) {
		f := startFake(t, FakeCRS354v78)
		b := runOnce(t, newTarget(t, f.Addr(), "{role: router, vlan_domain: dc1}"))
		wantAttrs(t, b, devRef, map[string]any{"role": "router"})
		if k := obj(t, b, "vlan:20").Identity["key"]; k != "dc1:20" {
			t.Errorf("vlan key = %q", k)
		}
		wantAttrs(t, b, "vlan:20", map[string]any{"domain": "dc1"})
	})
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name, options string
		tweak         func(*collect.Target)
		want          string
	}{
		{"unknown option", "{tsl: true}", nil, "tsl"},
		{"bad role", "{role: firewall}", nil, "role"},
		{"bad skip", "{skip: [bridge]}", nil, "skip section"},
		{"negative max", "{max_edge_macs: -1}", nil, "max_edge_macs"},
		{"no address", "", func(tg *collect.Target) { tg.Address = " " }, "address"},
		{"no username", "", func(tg *collect.Target) { tg.Username = "" }, "username"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var tweak []func(*collect.Target)
			if c.tweak != nil {
				tweak = append(tweak, c.tweak)
			}
			_, err := New(newTarget(t, "192.0.2.1", c.options, tweak...))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestHostPort(t *testing.T) {
	for _, c := range []struct{ in, port, want string }{
		{"192.0.2.1", defaultPort, "192.0.2.1:8728"},
		{"192.0.2.1", defaultTLSPort, "192.0.2.1:8729"},
		{"192.0.2.1:9000", defaultPort, "192.0.2.1:9000"},
		{" sw-a1.example.net ", defaultPort, "sw-a1.example.net:8728"},
		{"2001:db8::1", defaultPort, "[2001:db8::1]:8728"},
		{"[2001:db8::1]", defaultPort, "[2001:db8::1]:8728"},
		{"[2001:db8::1]:8729", defaultPort, "[2001:db8::1]:8729"},
	} {
		if got := hostPort(c.in, c.port); got != c.want {
			t.Errorf("hostPort(%q, %s) = %q, want %q", c.in, c.port, got, c.want)
		}
	}
}

// TestHostNameIsNotAManagementIP: a name in the target address says nothing
// about the device's address.
func TestHostNameIsNotAManagementIP(t *testing.T) {
	f := startFake(t, FakeCRS354v78)
	_, port, _ := strings.Cut(f.Addr(), ":")
	b := runOnce(t, newTarget(t, "localhost:"+port, ""))
	if _, has := obj(t, b, devRef).Identity["mgmt_ip"]; has {
		t.Errorf("identity = %v", obj(t, b, devRef).Identity)
	}
}
