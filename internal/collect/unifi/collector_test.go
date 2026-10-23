package unifi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// recordedNow is a moment 10 000 s after the recorded console booted, so that
// uptimes are exact.
var recordedNow = time.Date(2026, 10, 22, 17, 27, 17, 0, time.UTC).Add(10000 * time.Second)

// runAt runs the collector against f with its clock fixed.
func runAt(t *testing.T, f *FakeSiteManager, tg collect.Target, now time.Time) *sdk.Batch {
	t.Helper()
	c, err := New(tg)
	if err != nil {
		t.Fatal(err)
	}
	c.(*collector).now = func() time.Time { return now }
	bs := collecttest.Run(t, c)
	if len(bs) != 1 {
		t.Fatalf("got %d batches", len(bs))
	}
	if err := f.OnlyReads(); err != nil {
		t.Fatal(err)
	}
	return bs[0]
}

func TestRecordedOffice(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runAt(t, f, f.Target(office), recordedNow)

	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Collector != Kind || b.Health.Version != "5.1.33" || len(b.Health.Warnings) != 0 {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	if !b.ObservedAt.Equal(recordedNow) {
		t.Errorf("observed at %v", b.ObservedAt)
	}

	if n := count(b, "device"); n != 10 {
		t.Fatalf("%d devices, want 10", n)
	}
	roles := map[string]int{}
	for _, o := range collecttest.Objects([]*sdk.Batch{b}, "device") {
		roles[o.Attributes["role"].(string)]++
		attrs(t, o, map[string]any{"vendor": "Ubiquiti", "health": "ok"})
	}
	want := map[string]int{"firewall": 1, "access_point": 6, "switch": 1, "access_control": 2}
	for r, n := range want {
		if roles[r] != n {
			t.Errorf("role %s: %d devices, want %d (all: %v)", r, roles[r], n, roles)
		}
	}

	// Model, part number and version per device: the library match key.
	for mac, w := range map[string]map[string]any{
		"02:00:00:00:00:02": {"name": "ap-01", "model": "U7 Pro", "part_number": "U7PRO", "os_version": "8.7.11", "role": "access_point", "mgmt_ip": "192.0.2.11"},
		"02:00:00:00:00:08": {"name": "door-hub-01", "model": "UA Hub Door", "part_number": "UAH-DOOR", "os_version": "4.13.9.0", "role": "access_control"},
		"02:00:00:00:00:09": {"name": "reader-01", "model": "UA G2 Pro", "part_number": "UA-G2-PRO", "os_version": "1.14.17.0", "role": "access_control"},
		"02:00:00:00:00:0a": {"name": "sw-01", "model": "USW Pro Max 24 PoE", "part_number": "USPM24P", "os_version": "7.5.15", "role": "switch", "mgmt_ip": "192.0.2.19"},
	} {
		o := obj(t, b, "device", "base_mac", mac)
		rest := map[string]any{}
		for k, v := range w {
			if k != "name" {
				rest[k] = v
			}
		}
		attrs(t, o, rest)
		if o.Identity["name"] != w["name"] {
			t.Errorf("%s: name identity %q", mac, o.Identity["name"])
		}
	}
	ap := obj(t, b, "device", "base_mac", "02:00:00:00:00:02")
	fw, _ := ap.Attributes["firmware"].([]map[string]any)
	if len(fw) != 1 || fw[0]["name"] != "firmware" || fw[0]["version"] != "8.7.11" {
		t.Errorf("firmware records %v", ap.Attributes["firmware"])
	}
	if _, ok := fw[0]["update_available"]; ok {
		t.Error("an update is offered on an up-to-date device")
	}
	// ap-01 started at 17:28:47, 10000 s after the console's 17:27:17 less 90 s.
	attrs(t, ap, map[string]any{"uptime_s": 10000 - 90})
	absent(t, ap, "description", "serial", "os_name")

	con := obj(t, b, "device", "base_mac", "02:00:00:00:00:01")
	if con.Identity["name"] != "Office A" {
		t.Errorf("console name identity %q", con.Identity["name"])
	}
	attrs(t, con, map[string]any{
		"model": "UDM Pro Max", "part_number": "UDMPROMAX", "role": "firewall",
		"os_name": "UniFi OS", "os_version": "5.1.33", "mgmt_ip": "198.51.100.2", "uptime_s": 10000,
	})
	if con.Identity["serial"] != "020000000001" {
		t.Errorf("console serial identity %q", con.Identity["serial"])
	}

	// WAN ports.
	if n := count(b, "interface"); n != 2 {
		t.Fatalf("%d interfaces, want 2", n)
	}
	eth9 := obj(t, b, "interface", "name", "eth9")
	attrs(t, eth9, map[string]any{"kind": "physical", "description": "WAN", "mac": "02:00:00:00:00:0c", "admin_status": "up", "oper_status": "up", "speed_mbps": 10000})
	if eth9.Parent != con.Ref {
		t.Errorf("eth9 parent %q, want the console %q", eth9.Parent, con.Ref)
	}
	eth8 := obj(t, b, "interface", "name", "eth8")
	attrs(t, eth8, map[string]any{"description": "WAN2", "admin_status": "up", "oper_status": "down", "speed_mbps": 2500})
	if !hasRel(b, "assigned_to", "ip:198.51.100.2", eth9.Ref) {
		t.Error("the WAN address is not on eth9")
	}

	// Addresses: 9 devices, the console's WAN and LAN; link-local left out.
	if n := count(b, "ip_address"); n != 11 {
		t.Errorf("%d ip_address, want 11", n)
	}
	for _, o := range collecttest.Objects([]*sdk.Batch{b}, "ip_address") {
		if strings.HasPrefix(o.Identity["address"], "fe80") {
			t.Errorf("link-local address %s emitted", o.Identity["address"])
		}
	}
	if !hasRel(b, "assigned_to", "ip:192.0.2.1", con.Ref) {
		t.Error("the LAN address is not on the console")
	}
	if hasRel(b, "assigned_to", "ip:198.51.100.2", con.Ref) {
		t.Error("the WAN address is also assigned to the device")
	}
	if !hasRel(b, "assigned_to", "ip:192.0.2.11", ap.Ref) {
		t.Error("the AP's address is not assigned to it")
	}

	// Internet accesses: the public address is an attribute, not an object.
	if n := count(b, "internet_access"); n != 2 {
		t.Fatalf("%d internet accesses, want 2", n)
	}
	wan := obj(t, b, "internet_access", "name", "office/office-a/WAN")
	attrs(t, wan, map[string]any{"provider": "Example ISP", "status": "online", "public_ips": []string{"203.0.113.10"}})
	wan2 := obj(t, b, "internet_access", "name", "office/office-a/WAN2")
	attrs(t, wan2, map[string]any{"status": "offline", "provider": "Example ISP"})
	absent(t, wan2, "public_ips")
	for _, o := range collecttest.Objects([]*sdk.Batch{b}, "ip_address") {
		if o.Identity["address"] == "203.0.113.10" {
			t.Error("the public address became an ip_address (it would be stamped with the office domain)")
		}
	}
	if relCount(b, "routes_via") != 2 || !hasRel(b, "routes_via", con.Ref, wan.Ref) || !hasRel(b, "routes_via", con.Ref, wan2.Ref) {
		t.Errorf("routes_via: %d", relCount(b, "routes_via"))
	}

	// Applications and management.
	if n := count(b, "software"); n != 3 {
		t.Errorf("%d software, want 3", n)
	}
	attrs(t, obj(t, b, "software", "key", "application/UniFi Network/10.6.106"),
		map[string]any{"kind": "application", "name": "UniFi Network", "version": "10.6.106", "vendor": "Ubiquiti"})
	obj(t, b, "software", "key", "application/UniFi Protect/7.3.70")
	obj(t, b, "software", "key", "application/UniFi Access/4.3.22")
	if relCount(b, "runs") != 3 {
		t.Errorf("runs: %d", relCount(b, "runs"))
	}
	if relCount(b, "managed_by") != 9 {
		t.Errorf("managed_by: %d, want 9", relCount(b, "managed_by"))
	}
	if !hasRel(b, "managed_by", ap.Ref, con.Ref) {
		t.Error("ap-01 is not managed by the console")
	}
	if hasRel(b, "managed_by", con.Ref, con.Ref) {
		t.Error("the console manages itself")
	}
}

// The batch carries no object but those the descriptor lists, the engine-side
// schema accepts everything (collecttest.Run), and nothing else is emitted.
func TestRecordedObjectTypes(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runOne(t, f)
	types := map[string]bool{}
	for _, o := range b.Objects {
		types[o.Type] = true
	}
	for _, typ := range Descriptor.Collects {
		if !types[typ] {
			t.Errorf("the descriptor lists %s but the recorded run emits none", typ)
		}
	}
}

func TestSynthesisedAccount(t *testing.T) {
	f := newFake(t, SetSynthesised)
	b := runAt(t, f, f.Target(office), time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC))

	// Disconnected console: partial, a warning, health OK (nothing is broken
	// on our side), no state for its devices.
	if b.Scope != sdk.ScopePartial || !b.Health.OK {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	if b.Health.Version != "" {
		t.Errorf("version %q with two consoles", b.Health.Version)
	}
	warnings := strings.Join(b.Health.Warnings, "|")
	for _, w := range []string{"console Office B is disconnected", `unknown UniFi model "XYZ-9000" (XYZ9000)`, "host Self-hosted Network"} {
		if !strings.Contains(warnings, w) {
			t.Errorf("no warning %q in %v", w, b.Health.Warnings)
		}
	}
	ap := obj(t, b, "device", "base_mac", "02:00:00:01:00:02")
	absent(t, obj(t, b, "device", "base_mac", "02:00:00:02:00:02"), "health", "uptime_s")
	obj(t, b, "device", "base_mac", "02:00:00:02:00:01")
	attrs(t, obj(t, b, "device", "base_mac", "02:00:00:02:00:01"), map[string]any{"role": "firewall", "model": "UCG Max"})
	attrs(t, obj(t, b, "device", "base_mac", "02:00:00:02:00:02"), map[string]any{"role": "access_point", "os_version": "8.7.11"})

	// Statuses and roles.
	attrs(t, ap, map[string]any{"health": "critical", "role": "access_point", "description": "lobby ceiling"})
	sw := obj(t, b, "device", "base_mac", "02:00:00:01:00:03")
	attrs(t, sw, map[string]any{"role": "switch", "os_version": "7"})
	fw, _ := sw.Attributes["firmware"].([]map[string]any)
	if len(fw) != 1 || fw[0]["update_available"] != "7.5.20" {
		t.Errorf("firmware %v: the offered update is missing", sw.Attributes["firmware"])
	}
	attrs(t, obj(t, b, "device", "base_mac", "02:00:00:01:00:04"), map[string]any{"role": "camera"})
	mystery := obj(t, b, "device", "base_mac", "02:00:00:01:00:05")
	attrs(t, mystery, map[string]any{"role": "other", "health": "warning"})
	spare := obj(t, b, "device", "base_mac", "02:00:00:01:00:06")
	attrs(t, spare, map[string]any{"role": "access_point", "health": "warning"})
	attrs(t, obj(t, b, "device", "base_mac", "02:00:00:01:00:51"), map[string]any{"role": "switch"})

	// Management: an adopted device has a controller, the others do not.
	conA := "dev:02:00:00:01:00:01"
	if !hasRel(b, "managed_by", ap.Ref, conA) {
		t.Error("ap-lobby is not managed by its console")
	}
	if hasRel(b, "managed_by", spare.Ref, conA) {
		t.Error("a device that is not adopted is managed")
	}
	for _, r := range b.Relations {
		if r.Type == "managed_by" && r.From.Ref == "dev:02:00:00:01:00:51" {
			t.Error("a device of a self-hosted server is managed by a console")
		}
	}
	// WAN2 of office-a is down, a WAN without a public address has none.
	attrs(t, obj(t, b, "internet_access", "name", "office/office-a/WAN"), map[string]any{"provider": "Example ISP A", "public_ips": []string{"203.0.113.20"}, "status": "online"})
	attrs(t, obj(t, b, "internet_access", "name", "office/office-b/WAN"), map[string]any{"provider": "Example ISP B", "status": "online", "public_ips": []string{"203.0.113.40"}})
}

func TestPaginationFollowsNextToken(t *testing.T) {
	f := newFake(t, SetSynthesised)
	b := runOne(t, f)
	// Three hosts over two pages, all devices of both pages.
	if n := count(b, "device"); n != 9 {
		t.Errorf("%d devices, want 9 (6 + 1 + 2)", n)
	}
	var pages []string
	for _, r := range f.Requests() {
		if strings.Contains(r.Query, "nextToken=page-2") {
			pages = append(pages, r.Path)
		}
	}
	if len(pages) != 2 || pages[0] == pages[1] {
		t.Errorf("the second page was asked for %v, want hosts and devices", pages)
	}
	if b.Health.Items == 0 {
		t.Error("no items in the health record")
	}
}

func TestPaginationStopsOnRepeatedToken(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.SetBody("/v1/devices", `{"data":[],"nextToken":"again"}`)
	b := runOne(t, f)
	if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, "not paging") {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	if n := len(f.Requests()); n > 10 {
		t.Errorf("%d requests: the loop was not stopped", n)
	}
}

func TestPaginationStopsAtMaxPages(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n.Add(1)
		_, _ = w.Write([]byte(`{"data":[],"nextToken":"t` + strconv.Itoa(int(i)) + `"}`))
	}))
	defer srv.Close()
	f := newFake(t, SetRecorded)
	tg := f.Target(office)
	tg.Address = srv.URL
	b := collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK || !strings.Contains(b.Health.Error, "more than 50 pages") {
		t.Fatalf("health %+v", b.Health)
	}
	if n.Load() != maxPages {
		t.Errorf("%d requests, want %d", n.Load(), maxPages)
	}
}

func TestHostsFilter(t *testing.T) {
	f := newFake(t, SetSynthesised)
	tg := withOptions(t, f.Target(office), "hosts: [OFFICE-B]\n")
	b := runTarget(t, f, tg)
	if n := count(b, "device"); n != 2 {
		t.Errorf("%d devices, want office-b's 2", n)
	}
	for _, o := range collecttest.Objects([]*sdk.Batch{b}, "device") {
		if !strings.HasPrefix(o.Identity["base_mac"], "02:00:00:02:") {
			t.Errorf("device %v of another console", o.Identity)
		}
	}
	var asked bool
	for _, r := range f.Requests() {
		if r.Path == "/v1/devices" {
			asked = strings.Contains(r.Query, "hostIds%5B%5D=host-office-b%3A1")
			if strings.Contains(r.Query, "host-office-a") {
				t.Errorf("devices asked for office-a: %s", r.Query)
			}
		}
	}
	if !asked {
		t.Error("the devices call does not carry hostIds[]")
	}
	// By name and by id, and a name that matches nothing is a warning.
	tg = withOptions(t, f.Target(office), "hosts: [Office A, \"host-office-b:1\", nothing]\n")
	b = runTarget(t, f, tg)
	if n := count(b, "device"); n != 8 {
		t.Errorf("%d devices, want 8", n)
	}
	if !strings.Contains(strings.Join(b.Health.Warnings, "|"), `"nothing" matches no console`) {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
}

func TestHostsFilterMatchingNothingFails(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runTarget(t, f, withOptions(t, f.Target(office), "hosts: [elsewhere]\n"))
	if b.Health.OK || b.Scope != sdk.ScopePartial || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, "matches no console") {
		t.Fatalf("health %+v", b.Health)
	}
	for _, r := range f.Requests() {
		if r.Path == "/v1/devices" {
			t.Error("an unfiltered devices call was made: every console of the account would be read")
		}
	}
}

func TestRoleOverride(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runTarget(t, f, withOptions(t, f.Target(office), "roles: [\"UAH-DOOR=sensor\", \"u7 pro=other\"]\n"))
	attrs(t, obj(t, b, "device", "base_mac", "02:00:00:00:00:08"), map[string]any{"role": "sensor"})
	attrs(t, obj(t, b, "device", "base_mac", "02:00:00:00:00:02"), map[string]any{"role": "other"})
	if len(b.Health.Warnings) != 0 {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
}

func TestReadsOnlyTheThreeEndpoints(t *testing.T) {
	for _, set := range []string{SetRecorded, SetSynthesised} {
		f := newFake(t, set)
		runOne(t, f)
		seen := map[string]bool{}
		for _, r := range f.Requests() {
			if r.Method != http.MethodGet {
				t.Errorf("%s %s", r.Method, r.Path)
			}
			seen[r.Path] = true
		}
		want := map[string]bool{"/v1/hosts": true, "/v1/sites": true, "/v1/devices": true}
		if len(seen) != 3 || !seen["/v1/hosts"] || !seen["/v1/sites"] || !seen["/v1/devices"] {
			t.Errorf("%s: endpoints %v, want %v", set, seen, want)
		}
	}
}

func TestFakeRejectsWrites(t *testing.T) {
	f := newFake(t, SetRecorded)
	c := f.server.Client()
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(m, f.URL()+"/v1/hosts", nil)
		req.Header.Set("X-API-KEY", APIKey)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d", m, resp.StatusCode)
		}
	}
	if err := f.OnlyReads(); err == nil {
		t.Error("the writes were not recorded")
	}
	if n := len(f.Requests()); n != 4 {
		t.Errorf("%d requests recorded", n)
	}
}

func TestKeyOnlyInHeader(t *testing.T) {
	for _, set := range []string{SetRecorded, SetSynthesised} {
		f := newFake(t, set)
		runOne(t, f)
		for _, r := range f.Requests() {
			if strings.Contains(r.Path, APIKey) || strings.Contains(r.Query, APIKey) {
				t.Errorf("the key is in the URL: %+v", r)
			}
			if !r.HasKey {
				t.Errorf("a request without the key in X-API-KEY: %+v", r)
			}
		}
	}
}

func TestAuthRefused(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.SetKey("another-key")
	b := runTarget(t, f, f.Target(office))
	if b.Health.OK || b.Scope != sdk.ScopePartial || len(b.Objects) != 0 {
		t.Fatalf("scope %s, %d objects, health %+v", b.Scope, len(b.Objects), b.Health)
	}
	if !strings.Contains(b.Health.Error, "authentication refused") {
		t.Errorf("error %q", b.Health.Error)
	}
	if n := len(f.Requests()); n != 1 {
		t.Errorf("%d requests after a refused key, want exactly 1", n)
	}
}

func TestPartialFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		set      func(*FakeSiteManager)
		timeout  time.Duration
		want     string
		wantObjs map[string]int
	}{
		{"sites 500", func(f *FakeSiteManager) { f.Fail("/v1/sites", 500) }, 0, "GET /v1/sites: HTTP 500", map[string]int{"device": 10, "internet_access": 0, "interface": 2}},
		{"sites 429", func(f *FakeSiteManager) { f.Fail("/v1/sites", 429) }, 0, "rate limited (HTTP 429), retry after 30", map[string]int{"device": 10, "internet_access": 0}},
		{"sites malformed", func(f *FakeSiteManager) { f.Malformed("/v1/sites") }, 0, "malformed response", map[string]int{"device": 10, "internet_access": 0}},
		{"devices 403", func(f *FakeSiteManager) { f.Fail("/v1/devices", 403) }, 0, "forbidden (HTTP 403)", map[string]int{"device": 1, "internet_access": 2, "interface": 2, "software": 3}},
		{"devices malformed", func(f *FakeSiteManager) { f.Malformed("/v1/devices") }, 0, "malformed response", map[string]int{"device": 1, "internet_access": 2}},
		{"devices too slow", func(f *FakeSiteManager) { f.Slow("/v1/devices", 3*time.Second) }, 400 * time.Millisecond, "GET /v1/devices: transport error", map[string]int{"device": 1, "internet_access": 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			tc.set(f)
			tg := f.Target(office)
			if tc.timeout > 0 {
				tg.Timeout = tc.timeout
			}
			b := runTarget(t, f, tg)
			if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, tc.want) {
				t.Fatalf("scope %s, health %+v, want error containing %q", b.Scope, b.Health, tc.want)
			}
			for typ, n := range tc.wantObjs {
				if got := count(b, typ); got != n {
					t.Errorf("%d %s, want %d", got, typ, n)
				}
			}
		})
	}
}

func TestNotTheSiteManager(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.Malformed("")
	b := runOne(t, f)
	if b.Health.OK || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, "malformed response") {
		t.Fatalf("health %+v", b.Health)
	}
}

func TestHostsServerError(t *testing.T) {
	for _, status := range []int{403, 429, 500} {
		f := newFake(t, SetRecorded)
		f.Fail("/v1/hosts", status)
		b := runOne(t, f)
		if b.Health.OK || len(b.Objects) != 0 || b.Scope != sdk.ScopePartial {
			t.Errorf("%d: health %+v", status, b.Health)
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("%d: %d requests, want 1", status, n)
		}
	}
}

func TestUnreachable(t *testing.T) {
	f := newFake(t, SetRecorded)
	tg := f.Target(office)
	f.server.Close() // the key file stays: only the network is gone
	b := collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, "transport error") {
		t.Fatalf("health %+v", b.Health)
	}
}

func TestSecretFailure(t *testing.T) {
	f := newFake(t, SetRecorded)
	for _, tc := range []struct{ ref, want string }{
		{"env:TRICA_UNIFI_TEST_UNSET_KEY", "TRICA_UNIFI_TEST_UNSET_KEY"},
		{"file:/nonexistent/unifi-key", "/nonexistent/unifi-key"},
		{"", "no secret reference"},
		{"plain-text-key", "file: or env:"},
	} {
		tg := f.Target(office)
		tg.Password = collect.Secret(tc.ref)
		b := collecttest.Run(t, newCollector(t, tg))[0]
		if b.Health.OK || !strings.Contains(b.Health.Error, tc.want) || strings.Contains(b.Health.Error, "plain-text-key") {
			t.Errorf("%q: health %+v", tc.ref, b.Health)
		}
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("%d requests without a key", n)
	}
	t.Setenv("TRICA_UNIFI_TEST_EMPTY_KEY", "")
	tg := f.Target(office)
	tg.Password = "env:TRICA_UNIFI_TEST_EMPTY_KEY"
	if b := collecttest.Run(t, newCollector(t, tg))[0]; b.Health.OK || !strings.Contains(b.Health.Error, "empty") {
		t.Errorf("empty key: health %+v", b.Health)
	}
}

func TestBadTLSConfigAtRun(t *testing.T) {
	f := newFake(t, SetRecorded)
	tg := f.Target(office)
	tg.TLS.Insecure = false
	tg.TLS.CAFile = ""
	// The fake's certificate is not trusted: a transport failure, with no
	// way to fall back to an unverified connection.
	b := collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK || !strings.Contains(b.Health.Error, "transport error") {
		t.Fatalf("health %+v", b.Health)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer other.Close()
	f := newFake(t, SetRecorded)
	f.Redirect("/v1/hosts", other.URL+"/v1/hosts")
	b := runOne(t, f)
	if b.Health.OK || len(b.Objects) != 0 {
		t.Fatalf("health %+v", b.Health)
	}
	if hits.Load() != 0 {
		t.Fatal("the redirect was followed: the key went to another origin")
	}
	if reqs := f.Requests(); len(reqs) != 1 || !reqs[0].HasKey {
		t.Fatalf("requests %+v: the key was not sent to the configured address", reqs)
	}
}

func TestNewValidation(t *testing.T) {
	f := newFake(t, SetRecorded)
	good := f.Target(office)
	cases := []struct {
		name   string
		mutate func(*collect.Target)
		want   string
	}{
		{"http address", func(tg *collect.Target) { tg.Address = "http://api.example.test" }, "must be https"},
		{"bad address", func(tg *collect.Target) { tg.Address = "https://" }, "invalid address"},
		{"unknown option", func(tg *collect.Target) { tg.Options = optionsNode(t, "host: [a]\n") }, "host"},
		{"empty host", func(tg *collect.Target) { tg.Options = optionsNode(t, "hosts: [\"\"]\n") }, "empty host name"},
		{"role not in the schema", func(tg *collect.Target) { tg.Options = optionsNode(t, "roles: [\"X=spine\"]\n") }, `"spine" is not a device role`},
		{"role without equal", func(tg *collect.Target) { tg.Options = optionsNode(t, "roles: [UAH-DOOR]\n") }, "shortname-or-model=role"},
		{"bad tls", func(tg *collect.Target) { tg.TLS.MinVersion = "0.9" }, "min_version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg := good
			tc.mutate(&tg)
			if _, err := New(tg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
	// An empty address is the Site Manager; a host or a URL is accepted.
	tg := good
	tg.Address = ""
	c, err := New(tg)
	if err != nil || c.(*collector).base.String() != DefaultAddress {
		t.Errorf("empty address: %v, %v", err, c)
	}
	for addr, want := range map[string]string{"proxy.example.test:8443": "https://proxy.example.test:8443", "https://api.example.test/": "https://api.example.test"} {
		tg.Address = addr
		c, err := New(tg)
		if err != nil || c.(*collector).base.String() != want {
			t.Errorf("address %q: %v", addr, err)
		}
	}
}

func TestRegistered(t *testing.T) {
	d, ok := collect.Lookup(Kind)
	if !ok {
		t.Fatal("not registered")
	}
	if err := d.Validate(true); err != nil {
		t.Fatal(err)
	}
	if d.Evidence != collect.EvidenceRecorded || d.Secret.Use != collect.UseRequired || d.Username.Use != collect.UseUnused {
		t.Errorf("descriptor %+v", d)
	}
	opts, err := d.OptionFields()
	if err != nil || len(opts) != 2 || opts[0].Name != "hosts" || opts[1].Name != "roles" {
		t.Errorf("options %v, %v", opts, err)
	}
}

func TestNewFakeUnknownSet(t *testing.T) {
	if _, err := NewFake("nope"); err == nil {
		t.Error("an unknown fixture set was accepted")
	}
}

func TestContextCancelled(t *testing.T) {
	f := newFake(t, SetRecorded)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got *sdk.Batch
	err := newCollector(t, f.Target(office)).Collect(ctx, sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
		got = b
		return nil
	}))
	if err != nil || got == nil || got.Health.OK {
		t.Fatalf("err %v, batch %+v", err, got)
	}
}

func TestEmitterErrorIsReturned(t *testing.T) {
	f := newFake(t, SetRecorded)
	want := context.DeadlineExceeded
	err := newCollector(t, f.Target(office)).Collect(context.Background(), sdk.EmitterFunc(func(context.Context, *sdk.Batch) error { return want }))
	if !errors.Is(err, want) {
		t.Errorf("got %v", err)
	}
}

func TestBlockedAndStateless(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.SetBody("/v1/hosts", `{"data":[
	  {"id":"h1","type":"console","isBlocked":true,"reportedState":{"name":"Blocked","mac":"02:00:00:00:09:01"}},
	  {"id":"h2","type":"console"},
	  {"id":"h3","type":"console","reportedState":{"name":"No mac"}}]}`)
	b := runOne(t, f)
	if count(b, "device") != 0 {
		t.Errorf("%d devices from unusable hosts", count(b, "device"))
	}
	w := strings.Join(b.Health.Warnings, "|")
	for _, want := range []string{"console Blocked is blocked", "console h2 has no reported state", "console No mac has no MAC address"} {
		if !strings.Contains(w, want) {
			t.Errorf("no warning %q in %v", want, b.Health.Warnings)
		}
	}
}

func TestConsoleFromHostsWhenDevicesAreMissing(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.SetBody("/v1/devices", `{"data":[]}`)
	b := runOne(t, f)
	if b.Scope != sdk.ScopeFull {
		t.Errorf("scope %s", b.Scope)
	}
	con := obj(t, b, "device", "base_mac", "02:00:00:00:00:01")
	attrs(t, con, map[string]any{"role": "firewall", "part_number": "UDMPROMAX", "os_name": "UniFi OS"})
	// The model and versions come from the device list only: never flip.
	absent(t, con, "model", "os_version", "health", "uptime_s", "firmware")
	if count(b, "device") != 1 || count(b, "interface") != 2 {
		t.Errorf("%d devices, %d interfaces", count(b, "device"), count(b, "interface"))
	}
}
