package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/internal/collect/hostagent"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func batchOf(t testing.TB, root string) *sdk.Batch {
	t.Helper()
	b, err := observeRoot(t, root).Batch(epoch, "test", 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustObj(t testing.TB, b *sdk.Batch, ref string) *sdk.Object {
	t.Helper()
	o, ok := b.Lookup(ref)
	if !ok {
		t.Fatalf("no object %q in the batch", ref)
	}
	return o
}

func relations(b *sdk.Batch, typ string) []*sdk.Relation {
	var out []*sdk.Relation
	for _, r := range b.Relations {
		if r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

func objectsOf(b *sdk.Batch, typ string) []*sdk.Object {
	var out []*sdk.Object
	for _, o := range b.Objects {
		if o.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

func TestBatchValidatesAndIsFullScope(t *testing.T) {
	for _, root := range fakeRoots {
		t.Run(root, func(t *testing.T) {
			b := batchOf(t, root)
			if err := b.Validate(); err != nil {
				t.Fatal(err)
			}
			if b.Scope != sdk.ScopeFull {
				t.Fatalf("scope %q", b.Scope)
			}
			if !b.ObservedAt.Equal(epoch) {
				t.Fatalf("observed at %v", b.ObservedAt)
			}
		})
	}
}

func TestBatchSource(t *testing.T) {
	if got := batchOf(t, "debian12").Source; got != "agent:host-01" {
		t.Fatalf("source %q", got)
	}
	if got := batchOf(t, "rhel9").Source; got != "agent:host-02.example.invalid" {
		t.Fatalf("source %q", got)
	}
}

func TestBatchDeviceIdentity(t *testing.T) {
	b := batchOf(t, "debian12")
	h := mustObj(t, b, "host")
	if h.Type != "device" {
		t.Fatalf("a bare-metal host is a device, got %q", h.Type)
	}
	wantID := map[string]string{
		"serial": "SN-TEST-HOST01", "hw_uuid": "0a1b2c3d-0000-4000-8000-000000000001",
		"machine_id": "0123456789abcdef0123456789abcd01", "name": "host-01",
	}
	if len(h.Identity) != len(wantID) {
		t.Fatalf("identity %v", h.Identity)
	}
	for k, v := range wantID {
		if h.Identity[k] != v {
			t.Errorf("identity[%s] = %q, want %q", k, h.Identity[k], v)
		}
	}
	wantAttr := map[string]any{
		"role": "server", "vendor": "Example Systems", "model": "ExampleServer X1", "bios_version": "2.4.1",
		"os_name": "Debian GNU/Linux", "os_version": "12", "kernel": "6.1.0-28-amd64",
		"memory_bytes": int64(263921512 * 1024), "cpu_count": int64(2), "primary_ip": "192.0.2.10",
	}
	for k, v := range wantAttr {
		if h.Attributes[k] != v {
			t.Errorf("attribute %s = %#v, want %#v", k, h.Attributes[k], v)
		}
	}
	if _, ok := h.Attributes["uuid"]; ok {
		t.Error("a device has hw_uuid, not uuid")
	}
}

func TestBatchVMIdentity(t *testing.T) {
	b := batchOf(t, "rhel9")
	h := mustObj(t, b, "host")
	if h.Type != "vm" {
		t.Fatalf("a KVM guest is a vm, got %q", h.Type)
	}
	if h.Identity["uuid"] != "5e6f7a8b-0000-4000-8000-000000000002" || h.Identity["machine_id"] != "fedcba9876543210fedcba9876543202" ||
		h.Identity["name"] != "host-02.example.invalid" {
		t.Fatalf("identity %v", h.Identity)
	}
	for _, k := range []string{"serial", "hw_uuid"} {
		if _, ok := h.Identity[k]; ok {
			t.Errorf("a vm has no %s", k)
		}
	}
	if h.Attributes["power_state"] != "running" || h.Attributes["vcpus"] != int64(2) || h.Attributes["kernel"] != "5.14.0-427.el9.x86_64" ||
		h.Attributes["primary_ip"] != "198.51.100.30" || h.Attributes["os_name"] != "Red Hat Enterprise Linux" {
		t.Fatalf("attributes %v", h.Attributes)
	}
	for _, k := range []string{"role", "vendor", "model", "cpu_count", "bios_version"} {
		if _, ok := h.Attributes[k]; ok {
			t.Errorf("attribute %s is not a vm attribute", k)
		}
	}
}

func TestBatchVMHasNoDiskNorCPUObjects(t *testing.T) {
	b := batchOf(t, "rhel9")
	if n := len(objectsOf(b, "disk")) + len(objectsOf(b, "cpu")); n != 0 {
		t.Fatalf("%d disk/cpu objects on a vm: the schema gives them no vm parent", n)
	}
	if !containsSub(b.Health.Warnings, "virtual machine, its disks are not reported") {
		t.Fatalf("the omission is not said: %v", b.Health.Warnings)
	}
}

func TestBatchChildren(t *testing.T) {
	b := batchOf(t, "debian12")
	for _, o := range b.Objects {
		switch o.Type {
		case "interface", "cpu", "disk", "service", "listener":
			if o.Parent != "host" {
				t.Errorf("%s %s has parent %q", o.Type, o.Ref, o.Parent)
			}
		case "device", "software", "ip_address":
			if o.Parent != "" {
				t.Errorf("%s %s has parent %q", o.Type, o.Ref, o.Parent)
			}
		}
	}
	if n := len(objectsOf(b, "cpu")); n != 2 {
		t.Errorf("cpu objects %d", n)
	}
	if n := len(objectsOf(b, "interface")); n != 5 { // bond0, bond0.100, eno1, eno2, eno3
		t.Errorf("interface objects %d", n)
	}
	i := mustObj(t, b, "if:eno1")
	if i.Attributes["kind"] != "physical" || i.Attributes["mac"] != "02:00:00:00:01:01" || i.Attributes["oper_status"] != "up" ||
		i.Attributes["speed_mbps"] != int64(10000) || i.Attributes["mtu"] != int64(9000) {
		t.Errorf("eno1 %v", i.Attributes)
	}
	if mustObj(t, b, "if:bond0").Attributes["kind"] != "lag" || mustObj(t, b, "if:bond0.100").Attributes["kind"] != "vlan" {
		t.Error("bond0 is a lag, bond0.100 a vlan")
	}
}

func TestBatchAddresses(t *testing.T) {
	b := batchOf(t, "debian12")
	got := map[string]string{}
	for _, r := range relations(b, "assigned_to") {
		got[r.From.Ref] = r.To.Ref
	}
	want := map[string]string{
		"ip:192.0.2.10": "if:bond0", "ip:2001:db8::10": "if:bond0", "ip:198.51.100.10": "if:bond0.100", "ip:192.0.2.99": "if:eno3",
	}
	if len(got) != len(want) {
		t.Fatalf("assigned_to %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s assigned to %q, want %q", k, got[k], v)
		}
	}
	if p := mustObj(t, b, "ip:192.0.2.10").Attributes["prefix_length"]; p != int64(24) {
		t.Errorf("prefix_length %#v", p)
	}
	for _, ref := range []string{"ip:127.0.0.1", "ip:::1", "ip:172.17.0.1", "ip:fe80::ff:fe00:a"} {
		if _, ok := b.Lookup(ref); ok {
			t.Errorf("%s must not be reported", ref)
		}
	}
}

func TestBatchSoftware(t *testing.T) {
	b := batchOf(t, "debian12")
	os := mustObj(t, b, "sw:os/Debian GNU/Linux/12")
	if os.Identity["key"] != "os/Debian GNU/Linux/12" || os.Attributes["kind"] != "os" || os.Attributes["version"] != "12" {
		t.Fatalf("os %+v", os)
	}
	bash := mustObj(t, b, "sw:package/bash/5.2.15-2+b7")
	if bash.Attributes["kind"] != "package" || bash.Attributes["name"] != "bash" || bash.Attributes["version"] != "5.2.15-2+b7" {
		t.Fatalf("bash %+v", bash)
	}
	sw := objectsOf(b, "software")
	runs := relations(b, "runs")
	if len(runs) != len(sw) {
		t.Fatalf("%d software objects, %d runs relations", len(sw), len(runs))
	}
	for _, r := range runs {
		if r.From.Ref != "host" {
			t.Errorf("runs from %q", r.From.Ref)
		}
	}
	// libc6 is installed for two architectures at one version.
	if _, ok := b.Lookup("sw:package/libc6/2.36-9+deb12u7"); !ok {
		t.Error("libc6 amd64 missing")
	}
	if _, ok := b.Lookup("sw:package/libc6/2.36-9+deb12u7:i386"); !ok {
		t.Error("libc6 i386 missing: the second architecture must not collide with the first")
	}
}

func TestBatchServicesAndListeners(t *testing.T) {
	b := batchOf(t, "debian12")
	ssh := mustObj(t, b, "svc:ssh.service")
	if ssh.Attributes["state"] != "active" || ssh.Attributes["enabled"] != true {
		t.Errorf("ssh.service %v", ssh.Attributes)
	}
	if _, ok := mustObj(t, b, "svc:cron.service").Attributes["enabled"]; ok {
		t.Error("enabled=false is not claimed")
	}
	l := mustObj(t, b, "lst:tcp/192.0.2.10:5432")
	if l.Identity["key"] != "tcp/192.0.2.10:5432" || l.Attributes["process"] != "postgres" || l.Attributes["port"] != int64(5432) ||
		l.Attributes["address"] != "192.0.2.10" || l.Attributes["protocol"] != "tcp" {
		t.Errorf("listener %+v", l)
	}
	if _, ok := b.Lookup("lst:tcp/[2001:db8::10]:9100"); !ok {
		t.Error("IPv6 listener missing")
	}
}

func TestBatchTalksTo(t *testing.T) {
	b := batchOf(t, "debian12")
	rs := relations(b, "talks_to")
	if len(rs) != 1 {
		t.Fatalf("talks_to %+v", rs)
	}
	r := rs[0]
	if r.From.Ref != "host" || r.To.Ref != "rip:198.51.100.20" || r.Attributes["port"] != int64(443) ||
		r.Attributes["protocol"] != "tcp" || r.Attributes["process"] != "backup-agent" {
		t.Fatalf("%+v", r)
	}
	if o := mustObj(t, b, "rip:198.51.100.20"); o.Type != "ip_address" || o.Identity["address"] != "198.51.100.20" {
		t.Fatalf("%+v", o)
	}
}

func TestBatchTalksToWithoutProcessHasNoProcessAttribute(t *testing.T) {
	inv := observeRoot(t, "debian12")
	inv.Conns = []Conn{{Proto: "tcp", Remote: inv.Addrs[0].IP.Next().Next(), Port: 80}}
	b, err := inv.Batch(epoch, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	r := relations(b, "talks_to")[0]
	if v, ok := r.Attributes["process"]; ok {
		t.Fatalf("process = %#v: an unknown process must not be claimed", v)
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchNeighbors(t *testing.T) {
	b := batchOf(t, "debian12")
	rs := relations(b, "neighbor_of")
	if len(rs) != 2 {
		t.Fatalf("neighbor_of %+v", rs)
	}
	r := rs[0]
	if r.From.Ref != "if:eno1" || r.To.Type != "interface" || r.To.Identity["name"] != "Ethernet12" ||
		r.To.Parent == nil || r.To.Parent.Type != "device" || r.To.Parent.Identity["base_mac"] != "02:00:00:00:aa:01" ||
		r.To.Parent.Identity["name"] != "sw-01" || r.Attributes["protocol"] != "lldp" {
		t.Fatalf("%+v", r)
	}
	// Second neighbour has no system name: identified by chassis MAC alone, port named by its local id.
	r = rs[1]
	if r.From.Ref != "if:eno2" || r.To.Identity["name"] != "Eth1/13" || r.To.Parent.Identity["base_mac"] != "02:00:00:00:aa:02" {
		t.Fatalf("%+v", r)
	}
	if _, ok := r.To.Parent.Identity["name"]; ok {
		t.Fatalf("an absent system name must not become the identity %v", r.To.Parent.Identity)
	}
}

func TestBatchNeighborOnUnreportedInterfaceIsNoted(t *testing.T) {
	inv := observeRoot(t, "debian12")
	inv.Neighbors = []Neighbor{{Local: "veth1a2b3c", Protocol: "lldp", ChassisIDType: "mac", ChassisID: "02:00:00:00:aa:09", PortIDType: "ifname", PortID: "x"}}
	b, err := inv.Batch(epoch, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(relations(b, "neighbor_of")) != 0 || !containsSub(b.Health.Warnings, "neighbour on veth1a2b3c") {
		t.Fatalf("relations %d warnings %v", len(relations(b, "neighbor_of")), b.Health.Warnings)
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchDisks(t *testing.T) {
	b := batchOf(t, "debian12")
	var names []string
	for _, o := range objectsOf(b, "disk") {
		names = append(names, o.Identity["name"])
	}
	if strings.Join(names, ",") != "nvme0n1,sda,sdb" {
		t.Fatalf("disks %v", names)
	}
	n := mustObj(t, b, "disk:nvme0n1")
	if n.Attributes["media"] != "nvme" || n.Attributes["protocol"] != "NVMe" {
		t.Errorf("nvme0n1 %v", n.Attributes)
	}
	w, ok := n.Attributes["wear_pct"].(float64)
	if !ok || w < 0 || w > 100 {
		t.Errorf("wear_pct %#v", n.Attributes["wear_pct"])
	}
	if h, ok := n.Attributes["power_on_hours"].(int64); !ok || h%powerOnQuantum != 0 {
		t.Errorf("power_on_hours %#v must be a multiple of %d", n.Attributes["power_on_hours"], powerOnQuantum)
	}
	if mustObj(t, b, "disk:sda").Attributes["health"] == nil {
		t.Error("sda: SMART health missing")
	}
}

func TestBatchOmitsAbsentReadings(t *testing.T) {
	// A nil *T inside an interface is not nil for sdk.Set: no attribute may carry one.
	for _, root := range fakeRoots {
		b := batchOf(t, root)
		for _, o := range b.Objects {
			for k, v := range o.Attributes {
				if v == nil {
					t.Errorf("%s.%s is nil", o.Ref, k)
				}
				switch p := v.(type) {
				case *float64, *int64, *string:
					t.Errorf("%s.%s is a pointer %T", o.Ref, k, p)
				}
			}
		}
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "null") {
			t.Errorf("%s: a null in the batch JSON: %s", root, firstNull(string(raw)))
		}
	}
}

func firstNull(s string) string {
	i := strings.Index(s, "null")
	lo, hi := max(i-60, 0), min(i+20, len(s))
	return s[lo:hi]
}

func TestBatchHealth(t *testing.T) {
	b := batchOf(t, "debian12")
	h := b.Health
	if h == nil {
		t.Fatal("no health")
	}
	if h.Target != "host-01" || !h.OK || h.Error != "" || h.Collector != "agent" || h.Version != "test" ||
		h.Items != len(b.Objects) || h.Duration != 40*time.Millisecond {
		t.Fatalf("%+v", h)
	}
}

func TestBatchHealthReportsFailedAndSkippedProbes(t *testing.T) {
	inv := observeRoot(t, "debian12")
	inv.Failed = []string{"services: boom"}
	inv.Skipped = []string{"disks: smartctl not available"}
	b, err := inv.Batch(epoch, "v", 0)
	if err != nil {
		t.Fatal(err)
	}
	h := b.Health
	if h.OK {
		t.Error("a failed probe must make the run not OK: the engine then withdraws nothing")
	}
	if h.Error != "services: boom" {
		t.Errorf("error %q", h.Error)
	}
	if !containsSub(h.Warnings, "probe failed: services: boom") || !containsSub(h.Warnings, "disks: smartctl not available") {
		t.Errorf("warnings %v", h.Warnings)
	}
	// A skipped probe alone is reduced coverage, not a failure.
	inv.Failed = nil
	b, _ = inv.Batch(epoch, "v", 0)
	if !b.Health.OK {
		t.Error("a skipped probe is not a failure")
	}
}

func TestBatchWithoutIdentity(t *testing.T) {
	_, err := (&Inventory{}).Batch(epoch, "", 0)
	if !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("err %v", err)
	}
	for _, h := range []Host{{Hostname: "h"}, {MachineID: "m"}, {Serial: "s"}, {UUID: "u"}} {
		if _, err := (&Inventory{Host: h}).Batch(epoch, "", 0); err != nil {
			t.Errorf("%+v: %v", h, err)
		}
	}
}

func TestBatchContainerIsADevice(t *testing.T) {
	inv := observeRoot(t, "debian12")
	inv.Host.Virt = Virt{Kind: VirtContainer, Tech: "docker"}
	b, err := inv.Batch(epoch, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if mustObj(t, b, "host").Type != "device" || !containsSub(b.Health.Warnings, "docker container") {
		t.Fatalf("%s %v", mustObj(t, b, "host").Type, b.Health.Warnings)
	}
}

func TestBatchCarriesNoSecret(t *testing.T) {
	// The agent's own environment and the command lines of the host's processes
	// are secrets in practice: neither may reach the batch.
	t.Setenv("TRICA_AGENT_TEST_SECRET", "s3cr3t-value-do-not-leak")
	for _, root := range fakeRoots {
		raw, err := json.Marshal(batchOf(t, root))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"s3cr3t-value-do-not-leak", "environ", "cmdline", "password", "token"} {
			if strings.Contains(strings.ToLower(string(raw)), bad) {
				t.Errorf("%s: %q in the batch", root, bad)
			}
		}
	}
}

func TestUniquePackageKeysDeduplicates(t *testing.T) {
	got := uniquePackageKeys([]Package{
		{"a", "1", "amd64"}, {"a", "1", "i386"}, {"a", "1", "i386"}, {"a", "1", "amd64"},
	})
	if len(got) != 2 || got[0].key != "package/a/1" || got[1].key != "package/a/1:i386" {
		t.Fatalf("%+v", got)
	}
}

func TestNonZeroAndPtr(t *testing.T) {
	if nonZero(0) != nil || nonZero(3) != int64(3) {
		t.Error("nonZero")
	}
	var np *int64
	if ptr(np) != nil {
		t.Error("ptr(nil) must be an untyped nil")
	}
	v := int64(5)
	if ptr(&v) != int64(5) {
		t.Error("ptr(&5)")
	}
	if nilIfEmpty("") != nil || nilIfEmpty("x") != "x" {
		t.Error("nilIfEmpty")
	}
}

// The agent is a collector module like the others (ADR 0055): what the
// descriptor says it collects covers what it emits.
func TestBatchTypesAreDeclaredByTheDescriptor(t *testing.T) {
	collecttest.CheckCollects(t, hostagent.Descriptor, batchOf(t, "debian12"))
	collecttest.CheckCollects(t, hostagent.Descriptor, batchOf(t, "rhel9"))
}
