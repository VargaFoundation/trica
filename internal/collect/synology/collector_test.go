package synology

import (
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestRecordedDevice(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runOne(t, f)
	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Collector != Kind || b.Health.Version != "7.4.1-90080 Update 1" || len(b.Health.Warnings) != 0 {
		t.Fatalf("scope %v, health %+v", b.Scope, b.Health)
	}
	d := obj(t, b, "device", "serial", "0000ABCDE0001")
	attrs(t, d, map[string]any{
		"vendor": "Synology", "model": "DS1821+", "os_name": "DSM",
		"os_version": "7.4.1-90080 Update 1", "role": "nas",
		"memory_bytes": int64(4096) << 20, "logical_cpus": int64(4), "uptime_s": int64(2*3600 + 51*60 + 33),
		"base_mac": "02:11:32:00:00:01", "primary_ip": "192.0.2.57",
		"health": "warning", // pool reuse_1 and volume_1 are "attention"
	})
	absent(t, d, "mgmt_ip") // the fake answers on 127.0.0.1, which is no management address
	if count(b, "device") != 2 {
		t.Errorf("%d devices, want the NAS and its UPS", count(b, "device"))
	}
}

func TestRecordedDisks(t *testing.T) {
	b := runOne(t, newFake(t, SetRecorded))
	if n := count(b, "disk"); n != 8 {
		t.Fatalf("%d disks, want 8", n)
	}
	hdd := obj(t, b, "disk", "serial", "FAKEHDD0001")
	attrs(t, hdd, map[string]any{
		"slot": "Drive 2", "model": "WD4003FFBX-68MU3N0", "firmware_version": "83.00A83",
		"media": "hdd", "protocol": "SATA", "capacity_bytes": int64(4000787030016), "temperature_c": int64(33), "health": "ok",
	})
	absent(t, hdd, "wear_pct") // remain_life -1: the drive has no wear reading
	nvme := obj(t, b, "disk", "serial", "FAKENVME00001")
	attrs(t, nvme, map[string]any{
		"slot": "M.2 Drive 1", "media": "nvme", "protocol": "NVMe", "health": "ok", "wear_pct": int64(0),
		"model": "Samsung SSD 990 PRO 1TB",
	})
	for _, o := range b.Objects {
		if o.Type == "disk" && o.Parent != "dev" {
			t.Errorf("disk %v is not a child of the NAS", o.Identity)
		}
	}
}

func TestRecordedNetwork(t *testing.T) {
	b := runOne(t, newFake(t, SetRecorded))
	if n := count(b, "interface"); n != 6 {
		t.Fatalf("%d interfaces, want 6 (pppoe is not a port)", n)
	}
	up := obj(t, b, "interface", "name", "eth4")
	attrs(t, up, map[string]any{"kind": "physical", "mac": "02:11:32:00:00:05", "mtu": int64(1500), "speed_mbps": int64(10000), "oper_status": "up"})
	down := obj(t, b, "interface", "name", "eth0")
	attrs(t, down, map[string]any{"mac": "02:11:32:00:00:01", "oper_status": "down"})
	absent(t, down, "speed_mbps") // speed -1 is unknown
	if n := count(b, "ip_address"); n != 1 {
		t.Fatalf("%d addresses, want only 192.0.2.57 (the others are link-local)", n)
	}
	ip := obj(t, b, "ip_address", "address", "192.0.2.57")
	attrs(t, ip, map[string]any{"prefix_length": int64(24), "role": "primary"})
	if !hasRel(b, "assigned_to", "ip:192.0.2.57", "dev/if:eth4") {
		t.Error("the address is not assigned to eth4")
	}
}

func TestRecordedSoftwareAndUPS(t *testing.T) {
	b := runOne(t, newFake(t, SetRecorded))
	if n := count(b, "software"); n != 21+1+1 {
		t.Errorf("%d software, want 21 packages + DSM + the netbird image", n)
	}
	attrs(t, obj(t, b, "software", "key", "os/DSM/7.4.1-90080 Update 1"), map[string]any{"kind": "os", "name": "DSM", "version": "7.4.1-90080 Update 1", "vendor": "Synology"})
	attrs(t, obj(t, b, "software", "key", "container_image/netbirdio/netbird/0.78.1"), map[string]any{"kind": "container_image", "name": "netbirdio/netbird", "version": "0.78.1"})
	attrs(t, obj(t, b, "software", "key", "package/Active Insight/3.0.6-24124"), map[string]any{"kind": "package", "vendor": "Synology"})
	if n := relCount(b, "runs"); n != 23 {
		t.Errorf("%d runs relations, want 23", n)
	}
	ups := obj(t, b, "device", "name", "nas-a1/ups")
	attrs(t, ups, map[string]any{"role": "ups", "vendor": "EATON", "model": "Ellipse PRO 650", "health": "ok"})
	if !hasRel(b, "powered_by", "dev", "ups") {
		t.Error("the NAS is not powered_by the UPS")
	}
}

func TestSynthesisedEdgeCases(t *testing.T) {
	f := newFake(t, SetSynthesised)
	b := runOne(t, f)
	if b.Scope != sdk.ScopeFull || !b.Health.OK {
		t.Fatalf("scope %v health %+v: an absent Container Manager (102) is not a fault", b.Scope, b.Health)
	}
	if !hasWarning(b, "containers") {
		t.Errorf("no warning for the missing Container Manager: %v", b.Health.Warnings)
	}
	d := obj(t, b, "device", "serial", "0000ABCDE0002")
	attrs(t, d, map[string]any{
		"model": "DS923+", "os_version": "7.2.2-72806 Update 3",
		"uptime_s": int64(30*3600 + 5*60 + 9), "memory_bytes": int64(8192) << 20,
		"base_mac": "02:11:32:00:00:11", "primary_ip": "192.0.2.10",
		"health": "critical", // crashed pool, volume and drive win over the temperature warning
	})
	if count(b, "device") != 1 {
		t.Error("the UPS is disabled: no UPS device")
	}
	if relCount(b, "powered_by") != 0 {
		t.Error("powered_by without a UPS")
	}
	// Two addresses, the link-local one and the empty pppoe one are not objects.
	if n := count(b, "ip_address"); n != 2 {
		t.Errorf("%d addresses, want 2", n)
	}
	attrs(t, obj(t, b, "ip_address", "address", "192.0.2.11"), map[string]any{"prefix_length": int64(25)})
	absent(t, obj(t, b, "ip_address", "address", "192.0.2.11"), "role")
	attrs(t, obj(t, b, "interface", "name", "eth1"), map[string]any{"mtu": int64(9000), "speed_mbps": int64(2500)})
	if n := count(b, "interface"); n != 3 {
		t.Errorf("%d interfaces, want 3", n)
	}
	// Drives: a crashed one, a wear reading, an untrusted one.
	attrs(t, obj(t, b, "disk", "serial", "FAKEHDD0102"), map[string]any{"health": "critical"})
	ssd := obj(t, b, "disk", "serial", "FAKEHDD0103")
	attrs(t, ssd, map[string]any{"media": "ssd", "protocol": "SATA", "wear_pct": int64(15), "health": "ok"})
	untrusted := obj(t, b, "disk", "serial", "FAKEHDD0104")
	attrs(t, untrusted, map[string]any{"media": "ssd", "health": "warning"})
	absent(t, untrusted, "wear_pct")
	// A third-party package carries its maintainer as vendor.
	attrs(t, obj(t, b, "software", "key", "package/Fake Third-Party Tool/1.2.3-0001"), map[string]any{"vendor": "Example Vendor"})
	attrs(t, obj(t, b, "software", "key", "package/Fake Synology Tool/2.0.0-0002"), map[string]any{"vendor": "Synology"})
}

// A DSM that lists no interface in the system call falls back to the
// interface list (without MAC addresses).
func TestInterfaceListFallback(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.SetBody("SYNO.Core.System", "info", `{"success":true,"data":{"serial":"0000ABCDE0001","model":"DS1821+","firmware_ver":"DSM 7.4.1-90080 Update 1","ram_size":4096,"cpu_cores":"4","up_time":"0:10:00"}}`)
	b := runOne(t, f)
	if n := count(b, "interface"); n != 6 {
		t.Fatalf("%d interfaces, want 6", n)
	}
	if got := f.Calls(); !contains(got, "SYNO.Core.Network.Interface list") {
		t.Errorf("the interface list was not read: %v", got)
	}
	absent(t, obj(t, b, "interface", "name", "eth4"), "mac")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestRecordedCallsAreTheExpectedOnes(t *testing.T) {
	f := newFake(t, SetRecorded)
	runOne(t, f)
	want := []string{
		"SYNO.API.Auth login", "SYNO.Core.System info", "SYNO.DSM.Info getinfo", "SYNO.Core.System info",
		"SYNO.Core.Network get", "SYNO.Storage.CGI.Storage load_info", "SYNO.Core.Package list",
		"SYNO.Core.ExternalDevice.UPS get", "SYNO.Docker.Container list", "SYNO.API.Auth logout",
	}
	if got := f.Calls(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls\n got %v\nwant %v", got, want)
	}
}

func TestManagementAddress(t *testing.T) {
	for addr, want := range map[string]string{
		"https://192.0.2.10:5001":       "192.0.2.10",
		"192.0.2.10:5001":               "192.0.2.10",
		"https://nas.example.test:5001": "",
		"https://[2001:db8::10]:5001":   "2001:db8::10",
		"https://127.0.0.1:5001":        "",
	} {
		c, err := New(collectTarget(addr))
		if err != nil {
			t.Fatal(err)
		}
		x := &builder{c: c.(*collector)}
		if got := x.mgmtIP(); got != want {
			t.Errorf("%s: mgmt_ip %q, want %q", addr, got, want)
		}
	}
}
