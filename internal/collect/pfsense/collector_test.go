package pfsense

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const site = "site-a"

func newFake(t *testing.T, version string) *FakePfSense {
	t.Helper()
	f, err := NewFake(version)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f
}

func newCollector(t *testing.T, tg collect.Target) sdk.Collector {
	t.Helper()
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// runOne runs the collector against f and returns the single batch it emits.
func runOne(t *testing.T, f *FakePfSense) *sdk.Batch {
	t.Helper()
	bs := collecttest.Run(t, newCollector(t, f.Target(site)))
	if len(bs) != 1 {
		t.Fatalf("got %d batches, want 1", len(bs))
	}
	if bs[0].Source != "pfsense:"+site {
		t.Fatalf("source %q", bs[0].Source)
	}
	return bs[0]
}

func obj(t *testing.T, b *sdk.Batch, typ, key, value string) *sdk.Object {
	t.Helper()
	o := collecttest.Find([]*sdk.Batch{b}, typ, key, value)
	if o == nil {
		t.Fatalf("no %s with %s=%q", typ, key, value)
	}
	return o
}

func attrs(t *testing.T, o *sdk.Object, want map[string]any) {
	t.Helper()
	for k, w := range want {
		if got, ok := o.Attributes[k]; !ok || !equal(got, w) {
			t.Errorf("%s %v: attribute %s = %v (present %v), want %v", o.Type, o.Identity, k, got, ok, w)
		}
	}
}

func absent(t *testing.T, o *sdk.Object, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := o.Attributes[k]; ok {
			t.Errorf("%s %v: attribute %s = %v, want none", o.Type, o.Identity, k, v)
		}
	}
}

func equal(a, b any) bool {
	if sa, ok := a.([]string); ok {
		sb, _ := b.([]string)
		return slices.Equal(sa, sb)
	}
	return a == b
}

// hasRel reports whether b holds relation typ between the refs.
func hasRel(b *sdk.Batch, typ, from, to string) *sdk.Relation {
	for _, r := range b.Relations {
		if r.Type == typ && r.From.Ref == from && r.To.Ref == to {
			return r
		}
	}
	return nil
}

func requireRel(t *testing.T, b *sdk.Batch, typ, from, to string) *sdk.Relation {
	t.Helper()
	r := hasRel(b, typ, from, to)
	if r == nil {
		t.Fatalf("no %s relation %s -> %s", typ, from, to)
	}
	return r
}

func TestVersion272(t *testing.T) {
	f := newFake(t, Version272)
	b := runOne(t, f)

	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Collector != Kind || b.Health.Version != "2.7.2" || len(b.Health.Warnings) != 0 {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	if err := f.OnlyReads(); err != nil {
		t.Fatal(err)
	}

	fw := obj(t, b, "device", "name", "fw-a1")
	attrs(t, fw, map[string]any{
		"role": "firewall", "os_name": "pfSense", "os_version": "2.7.2", "model": "Netgate 4100",
		"cpu_count": 2, "uptime_s": 12*86400 + 3*3600 + 4*60 + 5, "mgmt_ip": "127.0.0.1",
	})
	if fw.Identity["serial"] != "FAKE-NG4100-0001" || fw.Identity["mgmt_ip"] != "127.0.0.1" {
		t.Errorf("device identity %v", fw.Identity)
	}

	// Interfaces.
	wan := obj(t, b, "interface", "name", "igb0")
	if wan.Parent != "fw" {
		t.Errorf("igb0 parent %q", wan.Parent)
	}
	attrs(t, wan, map[string]any{
		"kind": "physical", "description": "WAN", "mac": "02:00:00:aa:01:00", "oper_status": "up",
		"admin_status": "up", "speed_mbps": 1000, "duplex": "full", "mtu": 1500,
	})
	attrs(t, obj(t, b, "interface", "name", "igb1"), map[string]any{"speed_mbps": 2500, "description": "LAN"})
	vl := obj(t, b, "interface", "name", "igb1.20")
	attrs(t, vl, map[string]any{"kind": "vlan", "description": "IOT", "mac": "02:00:00:aa:01:01"})
	spare := obj(t, b, "interface", "name", "igb2")
	attrs(t, spare, map[string]any{"admin_status": "down", "oper_status": "down"})

	// Addresses: the live DHCP address, configured static ones, no link-local.
	for addr, want := range map[string]struct {
		plen int
		ifc  string
		pfx  string
	}{
		"203.0.113.18": {28, "if:igb0", "203.0.113.16/28"},
		"192.0.2.1":    {24, "if:igb1", "192.0.2.0/24"},
		"198.51.100.1": {25, "if:igb1.20", "198.51.100.0/25"},
	} {
		ip := obj(t, b, "ip_address", "address", addr)
		attrs(t, ip, map[string]any{"prefix_length": want.plen})
		requireRel(t, b, "assigned_to", ip.Ref, want.ifc)
		p := obj(t, b, "prefix", "prefix", want.pfx)
		requireRel(t, b, "assigned_to", p.Ref, want.ifc)
	}
	if got := len(collecttest.Objects([]*sdk.Batch{b}, "ip_address")); got != 3 {
		t.Errorf("%d ip addresses, want 3 (no link-local)", got)
	}

	// VLAN.
	v := obj(t, b, "vlan", "key", "site-a:20")
	attrs(t, v, map[string]any{"vid": 20, "name": "IoT devices", "domain": site})
	if r := requireRel(t, b, "member_of", vl.Ref, v.Ref); r.Attributes["tagged"] != true {
		t.Errorf("vlan membership %v", r.Attributes)
	}

	// Gateways: a dynamic one only the live state knows, a static one, a disabled one.
	g := obj(t, b, "gateway", "name", "site-a/WAN_DHCP")
	attrs(t, g, map[string]any{"address": "203.0.113.17", "status": "online", "latency_ms": 1.234, "loss_pct": 0.0})
	requireRel(t, b, "contains", "fw", g.Ref)
	bk := obj(t, b, "gateway", "name", "site-a/BACKUP_GW")
	attrs(t, bk, map[string]any{"address": "192.0.2.2", "status": "offline", "loss_pct": 100.0})
	g6 := obj(t, b, "gateway", "name", "site-a/WAN_GW6")
	absent(t, g6, "address", "status", "latency_ms")

	// Static routes: the active one only, through its gateway.
	rp := obj(t, b, "prefix", "prefix", "198.51.100.128/25")
	requireRel(t, b, "routes_via", rp.Ref, bk.Ref)
	if collecttest.Find([]*sdk.Batch{b}, "prefix", "prefix", "203.0.113.128/25") != nil {
		t.Error("disabled static route reported")
	}

	// Rules.
	r1 := obj(t, b, "firewall_rule", "key", "filter:1700000001")
	if r1.Parent != "fw" {
		t.Errorf("rule parent %q", r1.Parent)
	}
	attrs(t, r1, map[string]any{
		"kind": "filter", "order": 1, "enabled": true, "action": "pass", "interface": "lan",
		"source": "192.0.2.0/24", "destination": "any", "description": "Default allow LAN to any",
	})
	absent(t, r1, "protocol", "port")
	attrs(t, obj(t, b, "firewall_rule", "key", "filter:1700000002"), map[string]any{
		"action": "block", "protocol": "tcp", "destination": "(self)", "port": "22", "interface": "wan",
	})
	attrs(t, obj(t, b, "firewall_rule", "key", "filter:1700000003"), map[string]any{
		"enabled": false, "interface": "opt1,lan", "order": 3, "protocol": "udp",
	})
	attrs(t, obj(t, b, "firewall_rule", "key", "nat_port_forward:nat_65a1b2c3d4e5f6a7"), map[string]any{
		"kind": "nat_port_forward", "target": "192.0.2.50:443", "port": "8443", "destination": "wan:ip", "enabled": true,
	})
	attrs(t, obj(t, b, "firewall_rule", "key", "nat_outbound:t1700000200"), map[string]any{
		"kind": "nat_outbound", "target": "wan:ip", "source": "192.0.2.0/24",
	})
	nonat := obj(t, b, "firewall_rule", "key", "nat_outbound:t1700000201")
	attrs(t, nonat, map[string]any{"action": "nonat", "order": 2})
	absent(t, nonat, "target")
	if got := len(collecttest.Objects([]*sdk.Batch{b}, "firewall_rule")); got != 6 {
		t.Errorf("%d rules, want 6", got)
	}

	// Tunnels.
	srv := obj(t, b, "tunnel", "name", "site-a/openvpn-server-1")
	attrs(t, srv, map[string]any{"kind": "openvpn", "status": "up", "networks": []string{"192.0.2.0/24", "198.51.100.192/26"}})
	absent(t, srv, "peer")
	requireRel(t, b, "contains", "fw", srv.Ref)
	cli := obj(t, b, "tunnel", "name", "site-a/openvpn-client-2")
	attrs(t, cli, map[string]any{"kind": "openvpn", "status": "down", "peer": "198.51.100.200:1194", "networks": []string{"203.0.113.128/25"}})
	attrs(t, obj(t, b, "tunnel", "name", "site-a/ipsec-1"), map[string]any{"kind": "ipsec", "status": "up", "peer": "198.51.100.201"})
	mob := obj(t, b, "tunnel", "name", "site-a/ipsec-2")
	attrs(t, mob, map[string]any{"kind": "ipsec", "status": "down"})
	absent(t, mob, "peer")

	// The default gateway is WAN_DHCP in neither list: no routes_via from the
	// firewall in this version (no config entry says defaultgw).
	if hasRel(b, "routes_via", "fw", g.Ref) != nil {
		t.Error("routes_via reported without a defaultgw flag")
	}
}

func TestVersion281(t *testing.T) {
	f := newFake(t, Version281)
	b := runOne(t, f)

	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Version != "2.8.1-p1" {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	if err := f.OnlyReads(); err != nil {
		t.Fatal(err)
	}
	fw := obj(t, b, "device", "name", "fw-b2")
	attrs(t, fw, map[string]any{"os_version": "2.8.1-p1", "model": "pfSense", "cpu_count": 4, "uptime_s": 45*60 + 10})
	if _, ok := fw.Identity["serial"]; ok {
		t.Errorf("placeholder serial used as identity: %v", fw.Identity)
	}

	wan := obj(t, b, "interface", "name", "vtnet0")
	attrs(t, wan, map[string]any{"speed_mbps": 10000, "duplex": "full", "mac": "02:00:00:bb:02:00"})
	lan := obj(t, b, "interface", "name", "vtnet1")
	attrs(t, lan, map[string]any{"mtu": 9000})
	absent(t, lan, "description", "speed_mbps")

	// IPv6 is reported, link-local is not.
	ip6 := obj(t, b, "ip_address", "address", "2001:db8::50")
	attrs(t, ip6, map[string]any{"prefix_length": 64})
	requireRel(t, b, "assigned_to", ip6.Ref, wan.Ref)
	requireRel(t, b, "assigned_to", obj(t, b, "prefix", "prefix", "2001:db8::/64").Ref, wan.Ref)
	obj(t, b, "ip_address", "address", "203.0.113.50")
	if got := len(collecttest.Objects([]*sdk.Batch{b}, "ip_address")); got != 3 {
		t.Errorf("%d addresses, want 3", got)
	}
	if got := len(collecttest.Objects([]*sdk.Batch{b}, "vlan")); got != 0 {
		t.Errorf("%d vlans, want 0", got)
	}

	// A gateway under degradation, flagged as the default one.
	g := obj(t, b, "gateway", "name", "site-a/WAN_GW")
	attrs(t, g, map[string]any{"status": "degraded", "latency_ms": 85.5, "loss_pct": 2.5, "address": "203.0.113.1"})
	requireRel(t, b, "routes_via", "fw", g.Ref)

	// Rules: tracker given as a string, interface as a string or a list.
	attrs(t, obj(t, b, "firewall_rule", "key", "filter:1750000001"), map[string]any{
		"action": "reject", "protocol": "tcp/udp", "port": "25", "interface": "lan",
	})
	obj(t, b, "firewall_rule", "key", "filter:1750000002")
	attrs(t, obj(t, b, "firewall_rule", "key", "nat_one_to_one:t1750000300"), map[string]any{
		"kind": "nat_one_to_one", "target": "203.0.113.60", "source": "192.0.2.60", "description": "Mail server 1:1",
	})

	// An IPsec tunnel the (empty but readable) SA list does not show is down.
	attrs(t, obj(t, b, "tunnel", "name", "site-a/ipsec-5"), map[string]any{"status": "down", "peer": "198.51.100.77"})
	if got := len(collecttest.Objects([]*sdk.Batch{b}, "tunnel")); got != 1 {
		t.Errorf("%d tunnels, want 1", got)
	}
}

func TestReadsOnlyTheDocumentedEndpoints(t *testing.T) {
	f := newFake(t, Version272)
	runOne(t, f)
	want := []string{
		"firewall/nat/one_to_one/mappings", "firewall/nat/outbound/mappings", "firewall/nat/port_forwards", "firewall/rules",
		"interface/vlans", "interfaces", "routing/gateways", "routing/static_routes", "status/gateways",
		"status/interfaces", "status/ipsec/sas", "status/openvpn/clients", "status/openvpn/servers", "status/system",
		"system/hostname", "system/version", "vpn/ipsec/phase1s", "vpn/openvpn/clients", "vpn/openvpn/servers",
	}
	var got []string
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet || r.Query != "" {
			t.Errorf("request %+v", r)
		}
		got = append(got, r.Path)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("endpoints read:\n got %v\nwant %v", got, want)
	}
	// ARP and DHCP leases have no object to feed and are never requested.
	for _, p := range got {
		if strings.Contains(p, "arp") || strings.Contains(p, "dhcp") {
			t.Errorf("unexpected read of %s", p)
		}
	}
}

func TestFakeRejectsWrites(t *testing.T) {
	f := newFake(t, Version272)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(m, f.URL()+"/api/v2/firewall/rules", nil)
		req.Header.Set("X-API-Key", APIKey)
		cl, _ := f.Target("x").HTTPClient()
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d", m, resp.StatusCode)
		}
	}
	if err := f.OnlyReads(); err == nil {
		t.Fatal("the fake did not record the writes it rejected")
	}
}

func TestAuthRefused(t *testing.T) {
	f := newFake(t, Version272)
	f.SetKey("another-key")
	b := runOne(t, f)
	if b.Scope != sdk.ScopePartial || b.Health.OK || len(b.Objects) != 0 {
		t.Fatalf("scope %s, health %+v, %d objects", b.Scope, b.Health, len(b.Objects))
	}
	if !strings.Contains(b.Health.Error, "authentication refused") {
		t.Errorf("error %q", b.Health.Error)
	}
	if strings.Contains(b.Health.Error, APIKey) {
		t.Error("the API key leaked into the health error")
	}
	if n := len(f.Requests()); n != 1 {
		t.Errorf("%d requests after a refused key, want the probe only", n)
	}
}

func TestPartialFailures(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*FakePfSense)
		okHint  bool // health stays OK (version gap, nothing broken)
		errPart string
		warn    string
		gone    string // filter-rule key that must not be reported
		kept    string
	}{
		{"server error", func(f *FakePfSense) { f.Fail("firewall/rules", 500) }, false, "firewall/rules", "", "filter:1700000001", "nat_outbound:t1700000200"},
		{"privilege missing", func(f *FakePfSense) { f.Fail("vpn/ipsec/phase1s", 403) }, false, "lacks the read privilege", "", "", "filter:1700000001"},
		{"malformed", func(f *FakePfSense) { f.Malformed("firewall/rules") }, false, "malformed", "", "filter:1700000001", "nat_outbound:t1700000200"},
		{"json of the wrong shape", func(f *FakePfSense) { f.SetBody("firewall/rules", `{"code":200,"data":{"not":"a list"}}`) }, false, "firewall/rules", "", "filter:1700000001", "nat_outbound:t1700000200"},
		{"error envelope on HTTP 200", func(f *FakePfSense) {
			f.SetBody("interfaces", `{"code":500,"status":"error","message":"boom","data":[]}`)
		}, false, "boom", "", "", "filter:1700000001"},
		{"404 on an object endpoint", func(f *FakePfSense) { f.Missing("firewall/nat/one_to_one/mappings") }, true, "", "firewall/nat/one_to_one/mappings", "", "filter:1700000001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, Version272)
			tc.prepare(f)
			b := runOne(t, f)
			if b.Scope != sdk.ScopePartial {
				t.Errorf("scope %s, want partial: nothing may be withdrawn", b.Scope)
			}
			if b.Health.OK == !tc.okHint {
				t.Errorf("health OK = %v, want %v (%+v)", b.Health.OK, tc.okHint, b.Health)
			}
			if tc.errPart != "" && !strings.Contains(b.Health.Error, tc.errPart) {
				t.Errorf("error %q lacks %q", b.Health.Error, tc.errPart)
			}
			if tc.warn != "" && !strings.Contains(strings.Join(b.Health.Warnings, "|"), tc.warn) {
				t.Errorf("warnings %v lack %q", b.Health.Warnings, tc.warn)
			}
			if tc.gone != "" && collecttest.Find([]*sdk.Batch{b}, "firewall_rule", "key", tc.gone) != nil {
				t.Errorf("rule %s reported though its endpoint failed", tc.gone)
			}
			// What could be read is kept.
			obj(t, b, "device", "name", "fw-a1")
			obj(t, b, "interface", "name", "igb0")
			if tc.kept != "" {
				obj(t, b, "firewall_rule", "key", tc.kept)
			}
		})
	}
}

func TestMissingStatusEndpointKeepsFullScope(t *testing.T) {
	f := newFake(t, Version272)
	f.Missing("status/ipsec/sas")
	f.Missing("status/gateways")
	b := runOne(t, f)
	if b.Scope != sdk.ScopeFull || !b.Health.OK || len(b.Health.Warnings) != 2 {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	// Unknown is not down: the tunnel state is left unset, not claimed down.
	absent(t, obj(t, b, "tunnel", "name", "site-a/ipsec-1"), "status")
	// A status-only gateway (WAN_DHCP) cannot be reported at all.
	if collecttest.Find([]*sdk.Batch{b}, "gateway", "name", "site-a/WAN_DHCP") != nil {
		t.Error("gateway without any source reported")
	}
	absent(t, obj(t, b, "gateway", "name", "site-a/BACKUP_GW"), "status", "latency_ms")
}

func TestNotTheRESTAPI(t *testing.T) {
	// A web server that is not the REST API: HTML for everything.
	f := newFake(t, Version272)
	f.Malformed("")
	b := runOne(t, f)
	if b.Scope != sdk.ScopePartial || b.Health.OK || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, "REST API package") {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	// Same for an install with none of the endpoints.
	g := newFake(t, Version272)
	g.Missing("")
	b = runOne(t, g)
	if b.Health.OK || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, "404") {
		t.Fatalf("health %+v", b.Health)
	}
}

func TestTimeout(t *testing.T) {
	f := newFake(t, Version272)
	f.Slow("", 3*time.Second)
	tg := f.Target(site)
	tg.Timeout = 300 * time.Millisecond
	started := time.Now()
	bs := collecttest.Run(t, newCollector(t, tg))
	if d := time.Since(started); d > 2*time.Second {
		t.Errorf("run took %v with a 300ms timeout", d)
	}
	if len(bs) != 1 || bs[0].Health.OK || len(bs[0].Objects) != 0 || bs[0].Scope != sdk.ScopePartial {
		t.Fatalf("batches %+v", bs)
	}
}

func TestSlowSubRequest(t *testing.T) {
	f := newFake(t, Version272)
	f.Slow("firewall/rules", 3*time.Second)
	tg := f.Target(site)
	tg.Timeout = time.Second
	bs := collecttest.Run(t, newCollector(t, tg))
	b := bs[0]
	// The probe and the first endpoints answered before the deadline; the
	// ones from the slow one on failed: partial, not total.
	if b.Scope != sdk.ScopePartial || b.Health.OK || collecttest.Find(bs, "interface", "name", "igb0") == nil {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
}

func TestUnreachable(t *testing.T) {
	f := newFake(t, Version272)
	tg := f.Target(site)
	f.server.Close()
	bs := collecttest.Run(t, newCollector(t, tg))
	if len(bs) != 1 || bs[0].Health.OK || len(bs[0].Objects) != 0 || !strings.Contains(bs[0].Health.Error, "transport error") {
		t.Fatalf("batches %+v", bs)
	}
}

func TestSecretFailure(t *testing.T) {
	f := newFake(t, Version272)
	tg := f.Target(site)
	tg.Password = "file:/nonexistent/key"
	b := collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK || !strings.Contains(b.Health.Error, "API key") || len(f.Requests()) != 0 {
		t.Fatalf("health %+v, %d requests", b.Health, len(f.Requests()))
	}
	tg.Password = ""
	b = collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK {
		t.Fatalf("health %+v", b.Health)
	}
	tg.Password = "hunter2"
	b = collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK || strings.Contains(b.Health.Error, "hunter2") {
		t.Fatalf("health %+v", b.Health)
	}
}

func TestBadTLSConfigAtRun(t *testing.T) {
	f := newFake(t, Version272)
	tg := f.Target(site)
	c := newCollector(t, tg)
	// The CA file disappears between construction and the run.
	c.(*collector).t.TLS = collect.TLS{CAFile: "/nonexistent/ca.pem"}
	b := collecttest.Run(t, c)[0]
	if b.Health.OK {
		t.Fatalf("health %+v", b.Health)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer other.Close()
	var withKey atomic.Int32
	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "" {
			withKey.Add(1)
		}
		http.Redirect(w, r, other.URL+"/api/v2/system/version", http.StatusFound)
	}))
	defer first.Close()
	f := newFake(t, Version272)
	tg := f.Target(site)
	tg.Address = strings.TrimPrefix(first.URL, "https://")
	b := collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK || len(b.Objects) != 0 {
		t.Fatalf("health %+v", b.Health)
	}
	if hits.Load() != 0 {
		t.Fatal("the redirect was followed: the key went to another origin")
	}
	if withKey.Load() == 0 {
		t.Fatal("the key was not sent to the configured address")
	}
}

func TestOptionsSkipSections(t *testing.T) {
	f := newFake(t, Version272)
	tg := f.Target(site)
	tg.Options = optionsNode(t, "skip: [nat, vpn, routes, firewall_rules]\n")
	b := collecttest.Run(t, newCollector(t, tg))[0]
	if b.Scope != sdk.ScopeFull || !b.Health.OK {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	for _, typ := range []string{"firewall_rule", "tunnel"} {
		if n := len(collecttest.Objects([]*sdk.Batch{b}, typ)); n != 0 {
			t.Errorf("%d %s objects though the section is skipped", n, typ)
		}
	}
	for _, r := range f.Requests() {
		if strings.HasPrefix(r.Path, "firewall/") || strings.HasPrefix(r.Path, "vpn/") || strings.Contains(r.Path, "openvpn") ||
			strings.Contains(r.Path, "ipsec") || r.Path == "routing/static_routes" {
			t.Errorf("skipped section read: %s", r.Path)
		}
	}
	obj(t, b, "interface", "name", "igb0")
}

func TestOptionsBasePath(t *testing.T) {
	f := newFake(t, Version272)
	tg := f.Target(site)
	tg.Options = optionsNode(t, "base_path: /elsewhere/api/v2\n")
	b := collecttest.Run(t, newCollector(t, tg))[0]
	// The fake only serves /api/v2: everything is a 404 there.
	if b.Health.OK || !strings.Contains(b.Health.Error, "404") {
		t.Fatalf("health %+v", b.Health)
	}
}

func optionsNode(t *testing.T, doc string) yaml.Node {
	t.Helper()
	var w struct {
		Options yaml.Node `yaml:"options"`
	}
	indented := "options:\n  " + strings.ReplaceAll(strings.TrimRight(doc, "\n"), "\n", "\n  ") + "\n"
	if err := yaml.Unmarshal([]byte(indented), &w); err != nil {
		t.Fatal(err)
	}
	return w.Options
}

func TestNewValidation(t *testing.T) {
	f := newFake(t, Version272)
	good := f.Target(site)
	cases := []struct {
		name   string
		mutate func(*collect.Target)
		want   string
	}{
		{"no address", func(tg *collect.Target) { tg.Address = "" }, "no address"},
		{"http address", func(tg *collect.Target) { tg.Address = "http://192.0.2.1" }, "must be https"},
		{"bad address", func(tg *collect.Target) { tg.Address = "https://" }, "invalid address"},
		{"unknown option", func(tg *collect.Target) { tg.Options = optionsNode(t, "skpi: [nat]\n") }, "skpi"},
		{"unknown section", func(tg *collect.Target) { tg.Options = optionsNode(t, "skip: [dhcp]\n") }, "unknown section"},
		{"relative base path", func(tg *collect.Target) { tg.Options = optionsNode(t, "base_path: api/v2\n") }, "base_path"},
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
	for _, addr := range []string{"fw.example.test", "fw.example.test:8443", "https://fw.example.test:8443/", "192.0.2.1"} {
		tg := good
		tg.Address = addr
		c, err := New(tg)
		if err != nil || c.Kind() != Kind {
			t.Errorf("address %q: %v", addr, err)
		}
	}
}

func TestNewFakeUnknownVersion(t *testing.T) {
	if _, err := NewFake("1.2.3"); err == nil {
		t.Fatal("unknown fixture version accepted")
	}
}

func TestRegistered(t *testing.T) {
	if !slices.Contains(collect.Kinds(), Kind) {
		t.Fatalf("pfsense not registered: %v", collect.Kinds())
	}
}

func TestContextCancelled(t *testing.T) {
	f := newFake(t, Version272)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got *sdk.Batch
	err := newCollector(t, f.Target(site)).Collect(ctx, sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
		got = b
		return nil
	}))
	if err != nil || got == nil || got.Health.OK {
		t.Fatalf("err %v, batch %+v", err, got)
	}
}

func TestRulesWithoutStableID(t *testing.T) {
	f := newFake(t, Version272)
	f.SetBody("firewall/rules", `{"code":200,"status":"ok","data":[
	  {"type":"pass","interface":["lan"],"source":"any","destination":"any","descr":"a"},
	  {"type":"block","interface":["lan"],"source":"any","destination":"any","descr":"b"},
	  {"tracker":"77","type":"pass","interface":["wan"],"descr":"c"},
	  {"tracker":"77","type":"pass","interface":["wan"],"descr":"duplicate tracker"}]}`)
	b := runOne(t, f)
	a := obj(t, b, "firewall_rule", "key", "filter:#1")
	attrs(t, a, map[string]any{"description": "a", "order": 1})
	obj(t, b, "firewall_rule", "key", "filter:#2")
	obj(t, b, "firewall_rule", "key", "filter:77")
	attrs(t, obj(t, b, "firewall_rule", "key", "filter:77#dup4"), map[string]any{"description": "duplicate tracker"})
	if !strings.Contains(strings.Join(b.Health.Warnings, "|"), "2 rules carry no stable id") {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
	if !b.Health.OK || b.Scope != sdk.ScopeFull {
		t.Errorf("scope %s, health %+v", b.Scope, b.Health)
	}
}

func TestTolerantRepresentations(t *testing.T) {
	// Numbers where strings are usual and the other way round, a numeric
	// uptime, an interface known by name only in the status: all readable.
	f := newFake(t, Version281)
	f.SetBody("status/system", `{"code":200,"data":{"platform":"pfSense","uptime":3600,"cpu_count":"2","serial":"FAKE-1"}}`)
	f.SetBody("status/interfaces", `{"code":200,"data":[
	  {"name":"WAN","status":"up","macaddr":"02-00-00-bb-02-00","ipaddr":"203.0.113.50","subnet":24,"mtu":1500},
	  {"name":"vtnet1","status":"weird","macaddr":"not a mac"},
	  {"status":"up"}]}`)
	b := runOne(t, f)
	fw := obj(t, b, "device", "name", "fw-b2")
	attrs(t, fw, map[string]any{"uptime_s": 3600, "cpu_count": 2})
	if fw.Identity["serial"] != "FAKE-1" {
		t.Errorf("identity %v", fw.Identity)
	}
	// "WAN" matches the configured interface by its description.
	wan := obj(t, b, "interface", "name", "vtnet0")
	attrs(t, wan, map[string]any{"mac": "02:00:00:bb:02:00", "oper_status": "up", "mtu": 1500})
	lan := obj(t, b, "interface", "name", "vtnet1")
	attrs(t, lan, map[string]any{"oper_status": "unknown"})
	absent(t, lan, "mac")
}

func TestVlanWithoutVlanIf(t *testing.T) {
	f := newFake(t, Version281)
	f.SetBody("interface/vlans", `{"code":200,"data":[
	  {"if":"vtnet1","tag":"30","descr":"Guests"},
	  {"if":"vtnet1","tag":99999,"vlanif":"vtnet1.99999"},
	  {"descr":"nothing to identify it"}]}`)
	b := runOne(t, f)
	attrs(t, obj(t, b, "vlan", "key", "site-a:30"), map[string]any{"vid": 30, "name": "Guests"})
	g := obj(t, b, "interface", "name", "vtnet1.30")
	attrs(t, g, map[string]any{"kind": "vlan", "description": "Guests"})
	// The tag is out of range: the interface exists, the VLAN object does not.
	obj(t, b, "interface", "name", "vtnet1.99999")
	if len(collecttest.Objects([]*sdk.Batch{b}, "vlan")) != 1 {
		t.Errorf("vlans: %v", collecttest.Objects([]*sdk.Batch{b}, "vlan"))
	}
}

func TestDeviceIdentityWithoutHostname(t *testing.T) {
	f := newFake(t, Version281)
	f.Missing("system/hostname")

	// The management IP identifies the device: no name is invented.
	b := runOne(t, f)
	fw := collecttest.Objects([]*sdk.Batch{b}, "device")[0]
	if _, ok := fw.Identity["name"]; ok || fw.Identity["mgmt_ip"] != "127.0.0.1" {
		t.Errorf("identity %v", fw.Identity)
	}

	// Reached by name, with no serial: the target name is all that is left.
	tg := f.Target(site)
	tg.Address = "localhost:" + tg.Address[strings.LastIndex(tg.Address, ":")+1:]
	b = collecttest.Run(t, newCollector(t, tg))[0]
	obj(t, b, "device", "name", site)
}

func TestMissingVersionEndpoint(t *testing.T) {
	f := newFake(t, Version272)
	f.Missing("system/version")
	b := runOne(t, f)
	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Version != "" || len(b.Health.Warnings) != 1 {
		t.Fatalf("scope %s, health %+v", b.Scope, b.Health)
	}
	absent(t, obj(t, b, "device", "name", "fw-a1"), "os_version")
}
