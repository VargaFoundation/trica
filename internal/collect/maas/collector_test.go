package maas

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const keyEnv = "MAAS_TEST_API_KEY"

// targetFor builds a target for f. options is a YAML mapping or "".
func targetFor(t *testing.T, f *FakeMAAS, options string) collect.Target {
	t.Helper()
	t.Setenv(keyEnv, f.APIKey())
	tg := collect.Target{
		Kind:     Kind,
		Name:     "maas-a",
		Address:  f.URL(),
		Password: "env:" + keyEnv,
		Timeout:  10 * time.Second,
	}
	if options != "" {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(options), &n); err != nil {
			t.Fatal(err)
		}
		tg.Options = n
	}
	return tg
}

// collectFrom runs the registered collector once against f.
func collectFrom(t *testing.T, tg collect.Target) *sdk.Batch {
	t.Helper()
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	batches := collecttest.Run(t, c)
	if len(batches) != 1 {
		t.Fatalf("want exactly one batch, got %d", len(batches))
	}
	return batches[0]
}

func newFake(t *testing.T, v Version) *FakeMAAS {
	t.Helper()
	f := NewFake(v)
	t.Cleanup(f.Close)
	return f
}

func obj(t *testing.T, b *sdk.Batch, ref string) *sdk.Object {
	t.Helper()
	o, ok := b.Lookup(ref)
	if !ok {
		t.Fatalf("object %q not in batch", ref)
	}
	return o
}

func noObj(t *testing.T, b *sdk.Batch, ref string) {
	t.Helper()
	if _, ok := b.Lookup(ref); ok {
		t.Errorf("object %q should not be in the batch", ref)
	}
}

func rel(b *sdk.Batch, typ, from, to string) *sdk.Relation {
	for _, r := range b.Relations {
		if r.Type == typ && r.From.Ref == from && r.To.Ref == to {
			return r
		}
	}
	return nil
}

func wantAttrs(t *testing.T, o *sdk.Object, want map[string]any) {
	t.Helper()
	for k, v := range want {
		got, ok := o.Attributes[k]
		if !ok {
			t.Errorf("%s: attribute %q missing (attributes: %v)", o.Ref, k, o.Attributes)
			continue
		}
		if !reflect.DeepEqual(got, v) {
			t.Errorf("%s: %s = %#v, want %#v", o.Ref, k, got, v)
		}
	}
}

func wantNoAttrs(t *testing.T, o *sdk.Object, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := o.Attributes[k]; ok {
			t.Errorf("%s: attribute %q = %#v should be absent", o.Ref, k, v)
		}
	}
}

func wantIdentity(t *testing.T, o *sdk.Object, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(o.Identity, want) {
		t.Errorf("%s: identity = %v, want %v", o.Ref, o.Identity, want)
	}
}

func onlyReads(t *testing.T, f *FakeMAAS) {
	t.Helper()
	reqs := f.Requests()
	if len(reqs) == 0 {
		t.Fatal("the fake saw no request")
	}
	for _, r := range reqs {
		if r.Method != http.MethodGet {
			t.Errorf("collector issued %s %s: collectors are read-only", r.Method, r.Path)
		}
	}
}

func TestRegistered(t *testing.T) {
	found := false
	for _, k := range collect.Kinds() {
		found = found || k == Kind
	}
	if !found {
		t.Fatalf("kind %q is not registered (known: %v)", Kind, collect.Kinds())
	}
	f := newFake(t, V35)
	c, err := collect.New(targetFor(t, f, ""))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != Kind {
		t.Errorf("Kind() = %q", c.Kind())
	}
}

func TestCollectV35(t *testing.T) {
	f := newFake(t, V35)
	b := collectFrom(t, targetFor(t, f, ""))

	if b.Source != "maas:maas-a" || b.Scope != sdk.ScopeFull {
		t.Errorf("source %q scope %q", b.Source, b.Scope)
	}
	h := b.Health
	if h == nil || !h.OK || h.Collector != Kind || h.Version != "3.5.2" || h.Target != "maas-a" {
		t.Errorf("health = %+v", h)
	}

	// The exact set of requests: five GETs, no more.
	var got []string
	for _, r := range f.Requests() {
		got = append(got, r.Method+" "+r.Path+"?"+r.Query)
	}
	want := []string{
		"GET /MAAS/api/2.0/users/?op=whoami",
		"GET /MAAS/api/2.0/version/?",
		"GET /MAAS/api/2.0/machines/?",
		"GET /MAAS/api/2.0/subnets/?",
		"GET /MAAS/api/2.0/fabrics/?",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	onlyReads(t, f)

	t.Run("device with real identifiers", func(t *testing.T) {
		d := obj(t, b, "machine/fk0a01")
		wantIdentity(t, d, map[string]string{
			"provisioning_id": "fk0a01",
			"serial":          "SN-FAKE-0001",
			"hw_uuid":         "02a1b2c3-0000-4000-8000-000000000001",
			"name":            "srv-01",
		})
		wantAttrs(t, d, map[string]any{
			"role":                    "server",
			"provisioning_status":     "Deployed",
			"vendor":                  "Example Systems",
			"model":                   "ES-2200",
			"part_number":             "ES2200-SKU",
			"bios_version":            "2.1.0",
			"os_name":                 "ubuntu",
			"os_version":              "jammy",
			"logical_cpus":            int64(32),
			"memory_bytes":            int64(131072) << 20,
			"power_state":             "on",
			"primary_ip":              "192.0.2.11",
			"provisioning_owner":      "admin",
			"provisioning_zone":       "rack-a",
			"provisioning_pool":       "prod",
			"provisioning_kernel":     "ga-22.04",
			"provisioning_power_type": "ipmi",
			"provisioning_tags":       []string{"gpu", "rack-a"},
		})
		wantNoAttrs(t, d, "cpu_count") // MAAS counts logical CPUs, not sockets
	})

	t.Run("placeholders never become identifiers", func(t *testing.T) {
		d := obj(t, b, "machine/fk0a02")
		wantIdentity(t, d, map[string]string{"provisioning_id": "fk0a02", "name": "srv-02"})
		wantAttrs(t, d, map[string]any{
			"provisioning_status": "Ready",
			"vendor":              "Example Systems",
			"power_state":         "off",
			"provisioning_zone":   "rack-b",
			"provisioning_pool":   "default",
			"bios_version":        "2.0.9",
		})
		// "Not Specified" is not a model; an unallocated machine has no OS or owner.
		wantNoAttrs(t, d, "model", "os_name", "os_version", "provisioning_owner", "primary_ip", "provisioning_tags", "provisioning_kernel")

		v := obj(t, b, "machine/fk0a03")
		wantIdentity(t, v, map[string]string{
			"provisioning_id": "fk0a03", "hw_uuid": "02a1b2c3-0000-4000-8000-000000000003", "name": "vm-03",
		})
		wantAttrs(t, v, map[string]any{"vendor": "QEMU", "provisioning_owner": "alice", "provisioning_power_type": "lxd", "os_version": "noble"})
	})

	t.Run("interfaces", func(t *testing.T) {
		eno1 := obj(t, b, "machine/fk0a01/if/eno1")
		if eno1.Parent != "machine/fk0a01" {
			t.Errorf("parent = %q", eno1.Parent)
		}
		wantIdentity(t, eno1, map[string]string{"name": "eno1"})
		wantAttrs(t, eno1, map[string]any{
			"kind": "physical", "mac": "02:00:5e:10:01:01", "mtu": int64(1500),
			"admin_status": "up", "oper_status": "up", "speed_mbps": int64(10000),
		})
		bond := obj(t, b, "machine/fk0a01/if/bond0")
		wantAttrs(t, bond, map[string]any{"kind": "lag", "mtu": int64(9000), "mac": "02:00:5e:10:01:02"})
		wantNoAttrs(t, bond, "oper_status", "speed_mbps") // physical ports only
		wantAttrs(t, obj(t, b, "machine/fk0a01/if/bond0.204"), map[string]any{"kind": "vlan"})
		wantAttrs(t, obj(t, b, "machine/fk0a01/if/br0"), map[string]any{"kind": "virtual"})
		// srv-02's port has no link.
		wantAttrs(t, obj(t, b, "machine/fk0a02/if/eno1"), map[string]any{"oper_status": "down"})
		wantNoAttrs(t, obj(t, b, "machine/fk0a02/if/eno1"), "speed_mbps")
	})

	t.Run("bond and bridge members", func(t *testing.T) {
		for _, m := range []string{"eno2", "eno3"} {
			if rel(b, "member_of", "machine/fk0a01/if/"+m, "machine/fk0a01/if/bond0") == nil {
				t.Errorf("%s is not a member of bond0", m)
			}
		}
		if rel(b, "member_of", "machine/fk0a01/if/eno4", "machine/fk0a01/if/br0") == nil {
			t.Error("eno4 is not a member of br0")
		}
		// A VLAN interface rides on its parent, it is not a member of it.
		if rel(b, "member_of", "machine/fk0a01/if/bond0", "machine/fk0a01/if/bond0.204") != nil {
			t.Error("bond0 must not be a member of bond0.204")
		}
	})

	t.Run("vlans", func(t *testing.T) {
		tagged := obj(t, b, "vlan/fabric-0:204")
		wantIdentity(t, tagged, map[string]string{"key": "fabric-0:204"})
		wantAttrs(t, tagged, map[string]any{"vid": int64(204), "name": "storage", "domain": "fabric-0", "mtu": int64(9000), "dhcp_on": false})
		wantAttrs(t, obj(t, b, "vlan/fabric-0:0"), map[string]any{"vid": int64(0), "name": "untagged", "dhcp_on": true})
		obj(t, b, "vlan/fabric-1:0")
		// An empty fabric's untagged VLAN is not reported (#148); a tagged VLAN
		// nobody is on is an operator's declaration and is kept.
		noObj(t, b, "vlan/fabric-2:0")
		wantAttrs(t, obj(t, b, "vlan/fabric-1:30"), map[string]any{"vid": int64(30), "name": "backup"})

		r := rel(b, "member_of", "machine/fk0a01/if/bond0.204", "vlan/fabric-0:204")
		if r == nil || r.Attributes["tagged"] != true {
			t.Errorf("bond0.204 -> VLAN 204: %+v", r)
		}
		r = rel(b, "member_of", "machine/fk0a01/if/eno1", "vlan/fabric-0:0")
		if r == nil || r.Attributes["tagged"] != false {
			t.Errorf("eno1 -> untagged VLAN: %+v", r)
		}
	})

	t.Run("addresses", func(t *testing.T) {
		ip := obj(t, b, "ip/192.0.2.11")
		wantIdentity(t, ip, map[string]string{"address": "192.0.2.11"})
		wantAttrs(t, ip, map[string]any{"prefix_length": int64(24), "dns_name": "srv-01.maas"})
		if rel(b, "assigned_to", "ip/192.0.2.11", "machine/fk0a01/if/eno1") == nil {
			t.Error("192.0.2.11 is not assigned to eno1")
		}
		if rel(b, "assigned_to", "ip/198.51.100.21", "machine/fk0a01/if/bond0.204") == nil {
			t.Error("198.51.100.21 is not assigned to bond0.204")
		}
		wantNoAttrs(t, obj(t, b, "ip/198.51.100.21"), "dns_name") // only the primary address
		if rel(b, "assigned_to", "ip/203.0.113.30", "machine/fk0a01/if/br0") == nil {
			t.Error("203.0.113.30 is not assigned to br0")
		}
		// A DHCP lease counts; an "auto" link without address does not.
		if rel(b, "assigned_to", "ip/192.0.2.33", "machine/fk0a03/if/eno1") == nil {
			t.Error("the DHCP address of vm-03 is not assigned")
		}
	})

	t.Run("prefixes", func(t *testing.T) {
		p := obj(t, b, "prefix/192.0.2.0/24")
		wantIdentity(t, p, map[string]string{"prefix": "192.0.2.0/24"})
		wantAttrs(t, p, map[string]any{"description": "management", "gateway": "192.0.2.1"})
		wantNoAttrs(t, p, "vlan") // untagged: no VLAN ID to report
		s := obj(t, b, "prefix/198.51.100.0/24")
		wantAttrs(t, s, map[string]any{"description": "storage-net", "vlan": int64(204)})
		wantNoAttrs(t, s, "gateway")
		if rel(b, "assigned_to", "prefix/198.51.100.0/24", "vlan/fabric-0:204") == nil {
			t.Error("storage prefix is not assigned to VLAN 204")
		}
	})

	t.Run("disks", func(t *testing.T) {
		sda := obj(t, b, "machine/fk0a01/disk/sda")
		if sda.Parent != "machine/fk0a01" {
			t.Errorf("parent = %q", sda.Parent)
		}
		wantIdentity(t, sda, map[string]string{"name": "sda", "serial": "DSK-FAKE-0001"})
		wantAttrs(t, sda, map[string]any{
			"model": "EXAMPLE SSD 480", "capacity_bytes": int64(480103981056),
			"media": "ssd", "protocol": "SATA", "firmware_version": "FW01",
		})
		nvme := obj(t, b, "machine/fk0a01/disk/nvme0n1")
		wantIdentity(t, nvme, map[string]string{"name": "nvme0n1", "serial": "DSK-FAKE-0002"})
		wantAttrs(t, nvme, map[string]any{"media": "nvme", "protocol": "NVMe"})
		// "Unknown" is not a serial number.
		sdb := obj(t, b, "machine/fk0a01/disk/sdb")
		wantIdentity(t, sdb, map[string]string{"name": "sdb"})
		wantAttrs(t, sdb, map[string]any{"media": "hdd", "protocol": "SATA"})
		// RAID/LVM layers are not drives.
		noObj(t, b, "machine/fk0a01/disk/md0")
		wantIdentity(t, obj(t, b, "machine/fk0a02/disk/sda"), map[string]string{"name": "sda"})
		// A unit behind an HPE Smart Array is the BMC's to report (#128).
		noObj(t, b, "machine/fk0a02/disk/sdb")
	})
}

func TestSmartArrayUnit(t *testing.T) {
	for _, tc := range []struct {
		serial string
		want   bool
	}{
		{"30014380371aa900", true},
		{" 3001438000000A01 ", true},
		{"DSK-FAKE-0001", false},
		{"", false},
		{"5001438000000a01", false},  // NAA 5: a registered name, not the controller's local one
		{"3001438000000a0", false},   // 15 digits
		{"30014380000000a01", false}, // 17 digits
		{"3001438000000g01", false},  // not hex
		{"30014390000000a1", false},  // another OUI
	} {
		if got := smartArrayUnit(tc.serial); got != tc.want {
			t.Errorf("smartArrayUnit(%q) = %v, want %v", tc.serial, got, tc.want)
		}
	}
}

func TestCollectV33(t *testing.T) {
	f := newFake(t, V33)
	b := collectFrom(t, targetFor(t, f, ""))
	onlyReads(t, f)

	if b.Scope != sdk.ScopeFull || b.Health == nil || !b.Health.OK || b.Health.Version != "3.3.4" {
		t.Fatalf("scope %q health %+v", b.Scope, b.Health)
	}
	// 3.3 does not report system_sku nor the drive firmware.
	d := obj(t, b, "machine/fk0a01")
	wantIdentity(t, d, map[string]string{
		"provisioning_id": "fk0a01", "serial": "SN-FAKE-0001",
		"hw_uuid": "02a1b2c3-0000-4000-8000-000000000001", "name": "srv-01",
	})
	wantAttrs(t, d, map[string]any{"model": "ES-2200", "bios_version": "2.1.0", "primary_ip": "192.0.2.11"})
	wantNoAttrs(t, d, "part_number")
	sda := obj(t, b, "machine/fk0a01/disk/sda")
	wantAttrs(t, sda, map[string]any{"capacity_bytes": int64(480103981056), "media": "ssd"})
	wantNoAttrs(t, sda, "firmware_version")
	wantAttrs(t, obj(t, b, "machine/fk0a01/if/eno1"), map[string]any{"mac": "02:00:5e:10:01:01", "oper_status": "up"})
	if rel(b, "member_of", "machine/fk0a01/if/eno2", "machine/fk0a01/if/bond0") == nil {
		t.Error("bond membership missing")
	}

	// A machine that was never commissioned: no hardware, no interface, no disk.
	n := obj(t, b, "machine/fk0a04")
	wantIdentity(t, n, map[string]string{"provisioning_id": "fk0a04", "name": "srv-04"})
	wantAttrs(t, n, map[string]any{"role": "server", "provisioning_status": "New", "power_state": "unknown", "provisioning_zone": "default"})
	wantNoAttrs(t, n, "vendor", "model", "memory_bytes", "logical_cpus", "primary_ip", "provisioning_power_type")
	for _, o := range b.Objects {
		if o.Parent == "machine/fk0a04" {
			t.Errorf("a never-commissioned machine has no child, got %s", o.Ref)
		}
	}
}

func TestNonAdminKeySeesOnlyPartOfTheMachines(t *testing.T) {
	f := newFake(t, V35)
	f.AsNonAdmin()
	b := collectFrom(t, targetFor(t, f, ""))
	onlyReads(t, f)

	// MAAS hides srv-01, owned by admin: the batch must not claim to be complete.
	noObj(t, b, "machine/fk0a01")
	obj(t, b, "machine/fk0a02") // unallocated
	obj(t, b, "machine/fk0a03") // owned by the key's user
	if b.Scope != sdk.ScopePartial {
		t.Errorf("scope = %q: a non-administrator's view must not withdraw anything", b.Scope)
	}
	if !b.Health.OK {
		t.Errorf("a restricted key is not a failure: %+v", b.Health)
	}
	if len(b.Health.Warnings) != 1 || !strings.Contains(b.Health.Warnings[0], "not a MAAS administrator") {
		t.Errorf("warnings = %v", b.Health.Warnings)
	}

	// The operator may assert the key sees everything (RBAC "view all").
	f2 := newFake(t, V35)
	f2.AsNonAdmin()
	full := collectFrom(t, targetFor(t, f2, "assume_full_visibility: true"))
	if full.Scope != sdk.ScopeFull || len(full.Health.Warnings) != 0 {
		t.Errorf("assume_full_visibility: scope %q warnings %v", full.Scope, full.Health.Warnings)
	}
}

func assertFailed(t *testing.T, b *sdk.Batch, errContains string) {
	t.Helper()
	if b.Scope != sdk.ScopePartial {
		t.Errorf("scope = %q, a failed run must withdraw nothing", b.Scope)
	}
	if b.Health == nil || b.Health.OK {
		t.Fatalf("health = %+v, want a failure", b.Health)
	}
	if !strings.Contains(b.Health.Error, errContains) {
		t.Errorf("health error %q does not mention %q", b.Health.Error, errContains)
	}
	if strings.Contains(b.Health.Error, fakeTokenSecret) || strings.Contains(b.Health.Error, fakeTokenKey) {
		t.Errorf("the health error leaks the API key: %q", b.Health.Error)
	}
}

func TestAuthRefused(t *testing.T) {
	f := newFake(t, V35)
	f.FailAuth()
	b := collectFrom(t, targetFor(t, f, ""))
	assertFailed(t, b, "authentication refused")
	if len(b.Objects) != 0 || len(b.Relations) != 0 {
		t.Errorf("a refused run reports nothing, got %d objects", len(b.Objects))
	}
	// It stops at the first refusal instead of hammering the server.
	if n := len(f.Requests()); n != 1 {
		t.Errorf("%d requests after a refusal, want 1", n)
	}
	onlyReads(t, f)
}

func TestWrongKeyIsRefused(t *testing.T) {
	f := newFake(t, V35)
	tg := targetFor(t, f, "")
	t.Setenv(keyEnv, "fakeconsumer:faketoken:wrongsecret")
	assertFailed(t, collectFrom(t, tg), "authentication refused")
}

func TestUnreachable(t *testing.T) {
	f := NewFake(V35)
	tg := targetFor(t, f, "")
	f.Close()
	assertFailed(t, collectFrom(t, tg), "request failed")
}

func TestTimeout(t *testing.T) {
	f := newFake(t, V35)
	f.SetDelay(3 * time.Second)
	tg := targetFor(t, f, "")
	tg.Timeout = 150 * time.Millisecond
	start := time.Now()
	b := collectFrom(t, tg)
	assertFailed(t, b, "request failed")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the run took %s, the timeout did not bound it", d)
	}
}

func TestPartialFailures(t *testing.T) {
	tests := []struct {
		name     string
		break_   func(*FakeMAAS)
		contains string
		present  []string
		absent   []string
	}{
		{
			name:     "subnets fail",
			break_:   func(f *FakeMAAS) { f.FailEndpoint(EndpointSubnets, http.StatusInternalServerError) },
			contains: "subnets: maas: subnets/: HTTP 500",
			present:  []string{"machine/fk0a01", "vlan/fabric-0:204"},
			absent:   []string{"prefix/192.0.2.0/24"},
		},
		{
			name:     "machines fail",
			break_:   func(f *FakeMAAS) { f.FailEndpoint(EndpointMachines, http.StatusServiceUnavailable) },
			contains: "machines: maas: machines/: HTTP 503",
			present:  []string{"prefix/192.0.2.0/24", "vlan/fabric-0:204"},
			absent:   []string{"machine/fk0a01", "machine/fk0a02"},
		},
		{
			name:     "malformed machines",
			break_:   func(f *FakeMAAS) { f.Malform(EndpointMachines) },
			contains: "machines: maas: machines/: malformed response",
			present:  []string{"prefix/192.0.2.0/24"},
			absent:   []string{"machine/fk0a01"},
		},
		{
			name:     "fabrics fail",
			break_:   func(f *FakeMAAS) { f.FailEndpoint(EndpointFabrics, http.StatusForbidden) },
			contains: "fabrics: maas: fabrics/: HTTP 403",
			// The VLANs are still known through the interfaces and the subnets.
			present: []string{"machine/fk0a01", "vlan/fabric-0:204", "vlan/fabric-0:0"},
		},
		{
			name:     "malformed whoami",
			break_:   func(f *FakeMAAS) { f.Malform(EndpointWhoami) },
			contains: "whoami: maas: users/: malformed response",
			present:  []string{"machine/fk0a01", "machine/fk0a02"},
		},
		{
			name:     "whoami fails",
			break_:   func(f *FakeMAAS) { f.FailEndpoint(EndpointWhoami, http.StatusInternalServerError) },
			contains: "whoami: maas: users/: HTTP 500",
			present:  []string{"machine/fk0a01"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, V35)
			tc.break_(f)
			b := collectFrom(t, targetFor(t, f, ""))
			assertFailed(t, b, tc.contains)
			for _, ref := range tc.present {
				obj(t, b, ref)
			}
			for _, ref := range tc.absent {
				noObj(t, b, ref)
			}
			onlyReads(t, f)
		})
	}
}

func TestVersionFailureIsOnlyAWarning(t *testing.T) {
	f := newFake(t, V35)
	f.FailEndpoint(EndpointVersion, http.StatusNotFound)
	b := collectFrom(t, targetFor(t, f, ""))
	if b.Scope != sdk.ScopeFull || !b.Health.OK {
		t.Errorf("scope %q health %+v", b.Scope, b.Health)
	}
	if b.Health.Version != "" || len(b.Health.Warnings) != 1 || !strings.Contains(b.Health.Warnings[0], "version") {
		t.Errorf("version %q warnings %v", b.Health.Version, b.Health.Warnings)
	}
	obj(t, b, "machine/fk0a01")
}

func TestFailureBatchesForBadCredentials(t *testing.T) {
	f := newFake(t, V35)
	t.Run("no secret reference", func(t *testing.T) {
		tg := targetFor(t, f, "")
		tg.Password = ""
		assertFailed(t, collectFrom(t, tg), "API key")
	})
	t.Run("unset environment variable", func(t *testing.T) {
		tg := targetFor(t, f, "")
		tg.Password = "env:MAAS_TEST_DOES_NOT_EXIST"
		assertFailed(t, collectFrom(t, tg), "API key")
	})
	t.Run("malformed key", func(t *testing.T) {
		tg := targetFor(t, f, "")
		t.Setenv(keyEnv, "only-one-part")
		b := collectFrom(t, tg)
		assertFailed(t, b, "malformed MAAS API key")
		if strings.Contains(b.Health.Error, "only-one-part") {
			t.Error("the malformed key is echoed in the error")
		}
	})
	t.Run("bad TLS settings", func(t *testing.T) {
		tg := targetFor(t, f, "")
		tg.TLS.MinVersion = "9.9"
		assertFailed(t, collectFrom(t, tg), "tls.min_version")
	})
	if n := len(f.Requests()); n != 0 {
		t.Errorf("%d requests sent with unusable credentials", n)
	}
}

func TestNewRejectsBadConfiguration(t *testing.T) {
	f := newFake(t, V35)
	tests := []struct {
		name    string
		mutate  func(*collect.Target)
		options string
		want    string
	}{
		{name: "unknown option", options: "assume_full_visiblity: true", want: "options"},
		{name: "no address", mutate: func(tg *collect.Target) { tg.Address = "" }, want: "address"},
		{name: "not a URL scheme", mutate: func(tg *collect.Target) { tg.Address = "maas.example.net:5240" }, want: "address"},
		{name: "untagged VID 0", options: "untagged_vids: {fabric-0: 0}", want: "untagged_vids"},
		{name: "untagged VID 4095", options: "untagged_vids: {fabric-0: 4095}", want: "untagged_vids"},
		{name: "untagged blank fabric", options: `untagged_vids: {" ": 10}`, want: "untagged_vids"},
		{name: "wrong scheme", mutate: func(tg *collect.Target) { tg.Address = "ftp://maas.example.net" }, want: "address"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := targetFor(t, f, tc.options)
			if tc.mutate != nil {
				tc.mutate(&tg)
			}
			_, err := collect.New(tg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestEmitErrorIsReturned(t *testing.T) {
	f := newFake(t, V35)
	c, err := collect.New(targetFor(t, f, ""))
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	emit := sdk.EmitterFunc(func(context.Context, *sdk.Batch) error { return boom })
	if err := c.Collect(context.Background(), emit); !errors.Is(err, boom) {
		t.Errorf("Collect = %v, want the emitter's error", err)
	}
	f.FailAuth()
	if err := c.Collect(context.Background(), emit); !errors.Is(err, boom) {
		t.Errorf("Collect (failed run) = %v, want the emitter's error", err)
	}
}

func TestOddMachinesAreSkippedWithWarnings(t *testing.T) {
	bd := &builder{b: sdk.NewBatch("maas:x", sdk.ScopeFull)}
	bd.machine(&machine{Hostname: "no-id"})
	bd.subnet(subnet{Name: "bad", CIDR: "not-a-cidr"})
	m := &machine{
		SystemID: "fkodd1",
		InterfaceSet: []iface{
			{Name: ""},
			{Name: "eth0", Type: "alias", Links: []link{{IPAddress: "not-an-ip"}}},
		},
		BlockDeviceSet: []blockDevice{{Name: "", Type: "physical"}},
		IPAddresses:    []string{"bogus", "192.0.2.77"},
		BootInterface:  &iface{Name: "boot0", Type: "physical"},
		PowerState:     "error",
	}
	bd.machine(m)

	if len(bd.warnings) != 6 {
		t.Errorf("warnings = %q", bd.warnings)
	}
	d := obj(t, bd.b, "machine/fkodd1")
	wantAttrs(t, d, map[string]any{"power_state": "unknown", "primary_ip": "192.0.2.77"})
	wantAttrs(t, obj(t, bd.b, "machine/fkodd1/if/eth0"), map[string]any{"kind": "other"})
	// The boot interface is added when interface_set does not list it.
	obj(t, bd.b, "machine/fkodd1/if/boot0")
	// An address on no interface hangs on the device.
	if rel(bd.b, "assigned_to", "ip/192.0.2.77", "machine/fkodd1") == nil {
		t.Error("loose address not assigned to the device")
	}
	noObj(t, bd.b, "machine/fkodd1/if/")
}

// TestVLANDomains: a fabric mapped by options.vlan_domains files its VLANs
// under the named layer-2 domain (so they meet the switches' VLANs), every
// other fabric stays its own domain (ADR 0050).
func TestVLANDomains(t *testing.T) {
	f := newFake(t, V35)
	b := collectFrom(t, targetFor(t, f, "vlan_domains: {fabric-0: default}"))

	mapped := obj(t, b, "vlan/default:204")
	wantIdentity(t, mapped, map[string]string{"key": "default:204"})
	wantAttrs(t, mapped, map[string]any{"vid": int64(204), "domain": "default"})
	noObj(t, b, "vlan/fabric-0:204")
	if r := rel(b, "member_of", "machine/fk0a01/if/bond0.204", "vlan/default:204"); r == nil {
		t.Error("bond0.204 is not a member of the mapped VLAN")
	}
	if rel(b, "assigned_to", "prefix/198.51.100.0/24", "vlan/default:204") == nil {
		t.Error("the subnet is not on the mapped VLAN")
	}
	// An unmapped fabric keeps its own name.
	wantAttrs(t, obj(t, b, "vlan/fabric-1:0"), map[string]any{"domain": "fabric-1"})
}
