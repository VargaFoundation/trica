package unifi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Ids of the fixtures.
const (
	officeSite = "00000000-0000-4000-8000-000000000201"
	edgeSite   = "00000000-0000-4000-8000-000000000301"
	labSite    = "00000000-0000-4000-8000-000000000302"
)

func uidOf(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012x", n) }

func newFakeNet(t *testing.T, set string) *FakeNetwork {
	t.Helper()
	f, err := NewFakeNetwork(set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f
}

// runNet runs the collector against f and returns the single batch it emits.
// Every run is checked against the schema, for GET-only requests and for the
// four endpoint shapes under the console prefix.
func runNet(t *testing.T, f *FakeNetwork) *sdk.Batch {
	t.Helper()
	return runNetTarget(t, f, f.Target(office))
}

func runNetTarget(t *testing.T, f *FakeNetwork, tg collect.Target) *sdk.Batch {
	t.Helper()
	tg = withOptions(t, tg, "api: network\n")
	bs := collecttest.Run(t, newCollector(t, tg))
	if len(bs) != 1 {
		t.Fatalf("got %d batches, want 1", len(bs))
	}
	if bs[0].Source != "unifi:"+tg.Name {
		t.Fatalf("source %q", bs[0].Source)
	}
	checkNetRequests(t, f)
	return bs[0]
}

var netPaths = regexp.MustCompile(`^/v1/(info|sites|sites/[0-9a-f-]{36}/devices|sites/[0-9a-f-]{36}/devices/[0-9a-f-]{36})$`)

// checkNetRequests proves what the collector asked: only GETs, only under the
// console's prefix, only the four endpoint shapes, never a client list.
func checkNetRequests(t *testing.T, f *FakeNetwork) {
	t.Helper()
	if err := f.OnlyReads(); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.Requests() {
		if !r.Prefixed {
			t.Errorf("request outside %s: %+v", networkPrefix, r)
		}
		if !netPaths.MatchString(r.Path) {
			t.Errorf("request to %q is none of the four endpoint shapes", r.Path)
		}
		if strings.Contains(r.Path, "client") {
			t.Errorf("the clients were requested: %+v", r)
		}
	}
}

func byRef(t *testing.T, b *sdk.Batch, ref string) *sdk.Object {
	t.Helper()
	for _, o := range b.Objects {
		if o.Ref == ref {
			return o
		}
	}
	t.Fatalf("no object %s", ref)
	return nil
}

func hasRef(b *sdk.Batch, ref string) bool {
	for _, o := range b.Objects {
		if o.Ref == ref {
			return true
		}
	}
	return false
}

func devRef(n int) string { return fmt.Sprintf("dev:02:00:00:00:00:%02x", n) }

func TestNetworkOffice(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	b := runNet(t, f)

	if !b.Health.OK || b.Health.Error != "" || b.Scope != sdk.ScopeFull || len(b.Health.Warnings) != 0 {
		t.Fatalf("health %+v, scope %s", b.Health, b.Scope)
	}
	if b.Health.Version != "10.6.106" {
		t.Errorf("version %q", b.Health.Version)
	}
	if n := count(b, "device"); n != 10 {
		t.Fatalf("%d devices, want 10", n)
	}
	if n := count(b, "interface"); n != 45 {
		t.Errorf("%d interfaces, want 45 (11 + 26 + 6 + 1 + 1)", n)
	}
	// The uplinks the API names, device to device; and no cable, no adjacency:
	// the API does not say which port carries an uplink.
	if n := relCount(b, "uplinked_to"); n != 9 {
		t.Errorf("%d uplinked_to, want 9", n)
	}
	for n := 2; n <= 9; n++ {
		if !hasRel(b, "uplinked_to", devRef(n), devRef(10)) {
			t.Errorf("device %d is not uplinked to sw-01", n)
		}
	}
	if !hasRel(b, "uplinked_to", devRef(10), devRef(1)) {
		t.Error("sw-01 is not uplinked to the console")
	}
	for _, rel := range []string{"neighbor_of", "cabled_to", "managed_by", "routes_via", "runs"} {
		if n := relCount(b, rel); n != 0 {
			t.Errorf("%d %s relations: this source says none", n, rel)
		}
	}
	// What belongs to the cloud source is not set here, so that the two never
	// make a value flip.
	for _, o := range collecttest.Objects([]*sdk.Batch{b}, "device") {
		absent(t, o, "model", "firmware", "uptime_s", "serial", "description", "os_name")
		attrs(t, o, map[string]any{"vendor": "Ubiquiti", "health": "ok"})
	}
	attrs(t, byRef(t, b, devRef(1)), map[string]any{"role": "firewall", "part_number": "UDMPROMAX", "os_version": "5.1.33"})
	absent(t, byRef(t, b, devRef(1)), "mgmt_ip")
	attrs(t, byRef(t, b, devRef(2)), map[string]any{"role": "access_point", "part_number": "U7PRO", "os_version": "8.7.11", "mgmt_ip": "192.0.2.11"})
	attrs(t, byRef(t, b, devRef(8)), map[string]any{"role": "access_control", "part_number": "UAH-DOOR", "mgmt_ip": "192.0.2.17"})
	attrs(t, byRef(t, b, devRef(9)), map[string]any{"role": "access_control", "part_number": "UA-G2-PRO"})
	attrs(t, byRef(t, b, devRef(10)), map[string]any{"role": "switch", "part_number": "USPM24P", "mgmt_ip": "192.0.2.19"})
}

func TestNetworkPortsOfTheOffice(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	b := runNet(t, f)
	sw := func(idx int) *sdk.Object {
		return byRef(t, b, fmt.Sprintf("%s/if:Port %d", devRef(10), idx))
	}
	for _, tc := range []struct {
		idx  int
		want map[string]any
		none []string
	}{
		{1, map[string]any{"kind": "physical", "oper_status": "up", "speed_mbps": 1000, "max_speed_mbps": 1000, "connector": "RJ45", "poe_status": "delivering", "poe_standard": "802.3at"}, nil},
		{2, map[string]any{"oper_status": "down", "max_speed_mbps": 1000, "poe_status": "idle", "poe_standard": "802.3at"}, []string{"speed_mbps"}},
		{9, map[string]any{"oper_status": "up", "speed_mbps": 1000, "poe_status": "delivering", "poe_standard": "802.3bt"}, nil},
		{17, map[string]any{"oper_status": "up", "speed_mbps": 2500, "max_speed_mbps": 2500, "poe_status": "delivering", "poe_standard": "802.3bt"}, nil},
		{23, map[string]any{"oper_status": "down", "poe_status": "disabled", "poe_standard": "802.3bt"}, []string{"speed_mbps"}},
		{25, map[string]any{"oper_status": "up", "speed_mbps": 10000, "max_speed_mbps": 10000, "connector": "SFP+"}, []string{"poe_status", "poe_standard"}},
		{26, map[string]any{"oper_status": "down", "connector": "SFP+"}, []string{"speed_mbps", "poe_status"}},
	} {
		o := sw(tc.idx)
		attrs(t, o, tc.want)
		absent(t, o, tc.none...)
		if o.Identity["name"] != fmt.Sprintf("Port %d", tc.idx) || o.Parent != devRef(10) {
			t.Errorf("port %d: identity %v, parent %q", tc.idx, o.Identity, o.Parent)
		}
	}
	// An access point's port is a powered device's: no PoE at all.
	ap := byRef(t, b, devRef(2)+"/if:Port 1")
	attrs(t, ap, map[string]any{"oper_status": "up", "speed_mbps": 2500, "max_speed_mbps": 2500, "connector": "RJ45"})
	absent(t, ap, "poe_status", "poe_standard")
	// The console's ports: the unplugged 2.5G port and the two 10G SFP+.
	attrs(t, byRef(t, b, devRef(1)+"/if:Port 9"), map[string]any{"oper_status": "down", "max_speed_mbps": 2500, "connector": "RJ45"})
	attrs(t, byRef(t, b, devRef(1)+"/if:Port 10"), map[string]any{"oper_status": "up", "speed_mbps": 10000, "connector": "SFP+"})
}

// The same device seen through the cloud and through the console must agree on
// every attribute both set and on the role of every address both assign: two
// sources that disagree make the resolved value flip from one run to the next.
func TestBothAPIsAgreeOnSharedAttributes(t *testing.T) {
	cloud := runOne(t, newFake(t, SetRecorded))
	local := runNet(t, newFakeNet(t, SetNetwork))
	compared := map[string]int{}
	for _, o := range collecttest.Objects([]*sdk.Batch{local}, "device") {
		c := collecttest.Find([]*sdk.Batch{cloud}, "device", "base_mac", o.Identity["base_mac"])
		if c == nil {
			t.Errorf("%s: the cloud has no such device", o.Identity["base_mac"])
			continue
		}
		if o.Identity["name"] != c.Identity["name"] {
			t.Errorf("%s: name %q here, %q in the cloud", o.Identity["base_mac"], o.Identity["name"], c.Identity["name"])
		}
		compared["name"]++
		for k, v := range o.Attributes {
			cv, ok := c.Attributes[k]
			if !ok {
				continue
			}
			compared[k]++
			if !equal(v, cv) {
				t.Errorf("%s: %s is %v here and %v in the cloud", o.Identity["base_mac"], k, v, cv)
			}
		}
	}
	for _, k := range []string{"name", "part_number", "role", "os_version", "health", "mgmt_ip", "vendor"} {
		// 9 devices have a mgmt_ip in both sources (not the console).
		min := 10
		if k == "mgmt_ip" {
			min = 9
		}
		if compared[k] < min {
			t.Errorf("%s was compared on %d devices, want %d: the check is vacuous", k, compared[k], min)
		}
	}
	// Addresses: the same object, the same role.
	shared := 0
	for _, ip := range collecttest.Objects([]*sdk.Batch{local}, "ip_address") {
		c := collecttest.Find([]*sdk.Batch{cloud}, "ip_address", "address", ip.Identity["address"])
		if c == nil {
			continue
		}
		shared++
		if ip.Attributes["role"] != c.Attributes["role"] {
			t.Errorf("address %s: role %v here, %v in the cloud", ip.Identity["address"], ip.Attributes["role"], c.Attributes["role"])
		}
	}
	if shared != 10 {
		t.Errorf("%d addresses in both sources, want 10", shared)
	}
	// And each address is assigned to the same device by both.
	for _, r := range local.Relations {
		if r.Type == "assigned_to" && !hasRel(cloud, "assigned_to", r.From.Ref, r.To.Ref) {
			t.Errorf("assigned_to %s -> %s is not in the cloud's batch", r.From.Ref, r.To.Ref)
		}
	}
}

// On a gateway the console's address is the LAN one while the cloud reports
// the WAN side: if both set mgmt_ip the value would flip.
func TestGatewayHasNoMgmtIP(t *testing.T) {
	f := newFakeNet(t, SetNetworkEdge)
	b := runNet(t, f)
	gw := byRef(t, b, "dev:02:00:00:01:00:01")
	attrs(t, gw, map[string]any{"role": "firewall", "part_number": "UXGPRO"})
	absent(t, gw, "mgmt_ip")
	// The address is still the gateway's, with no role (a LAN address of the
	// device, as the cloud assigns it).
	ip := byRef(t, b, "ip:192.0.2.101")
	absent(t, ip, "role")
	if !hasRel(b, "assigned_to", "ip:192.0.2.101", gw.Ref) {
		t.Error("the gateway's address is not assigned to it")
	}
	// A switch keeps both.
	attrs(t, byRef(t, b, "dev:02:00:00:01:00:02"), map[string]any{"mgmt_ip": "192.0.2.102"})
	attrs(t, byRef(t, b, "ip:192.0.2.102"), map[string]any{"role": "primary"})
}

func TestNetworkRole(t *testing.T) {
	for _, tc := range []struct {
		name      string
		model     string
		features  []str
		overrides map[string]string
		want      string
		known     bool
	}{
		{"gateway wins over switching", "UDMPROMAX", []str{"switching", "gateway"}, nil, "firewall", true},
		{"access point", "U7PRO", []str{"accessPoint"}, nil, "access_point", true},
		{"switch", "USPM24P", []str{"switching"}, nil, "switch", true},
		{"unknown model, switching feature", "X-1", []str{"switching"}, nil, "switch", true},
		{"override wins over features", "U7PRO", []str{"accessPoint"}, map[string]string{"U7PRO": "other"}, "other", true},
		{"override by model key", "UAH-DOOR", nil, map[string]string{"UAHDOOR": "other"}, "other", true},
		{"model rule without features", "U6-LR", nil, nil, "access_point", true},
		{"access hub", "UAH-DOOR", nil, nil, "access_control", true},
		{"access reader", "UA-G2-PRO", nil, nil, "access_control", true},
		{"UAP is not Access", "UAP-AC-Pro", nil, nil, "access_point", true},
		{"unknown stays unset", "XYZ-1", nil, nil, "", false},
		{"empty model stays unset", "", nil, nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := networkRole(netDeviceOverview{Model: str(tc.model), Features: tc.features}, tc.overrides)
			if got != tc.want || known != tc.known {
				t.Errorf("got %q, %v; want %q, %v", got, known, tc.want, tc.known)
			}
		})
	}
}

func TestPortMapping(t *testing.T) {
	for in, want := range map[string]string{"UP": "up", "up": "up", "DOWN": "down", "UNKNOWN": "unknown", "": "unknown", "weird": "unknown"} {
		if got := portStatus(in); got != want {
			t.Errorf("portStatus(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"RJ45": "RJ45", "SFP": "SFP", "SFPPLUS": "SFP+", "sfpplus": "SFP+", "SFP28": "SFP28", "QSFP28": "QSFP28", "": "", "fibre": "FIBRE"} {
		if got := connectorName(in); got != want {
			t.Errorf("connectorName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, tc := range []struct {
		poe  netPoE
		want string
	}{
		{netPoE{Enabled: true, State: "UP"}, "delivering"},
		{netPoE{Enabled: true, State: "DOWN"}, "idle"},
		{netPoE{Enabled: true, State: "LIMITED"}, "limited"},
		{netPoE{Enabled: true, State: "UNKNOWN"}, "unknown"},
		{netPoE{Enabled: true, State: ""}, "unknown"},
		{netPoE{Enabled: false, State: "UP"}, "disabled"},
		{netPoE{Enabled: false, State: "DOWN"}, "disabled"},
	} {
		if got := poeStatus(&tc.poe); got != tc.want {
			t.Errorf("poeStatus(%+v) = %q, want %q", tc.poe, got, tc.want)
		}
	}
}

// The edge set: every awkward shape the specification allows.
func TestNetworkEdgeDevicesAndPorts(t *testing.T) {
	f := newFakeNet(t, SetNetworkEdge)
	b := runNet(t, f)

	// A MAC written with dashes is the same colon form; an empty one skips the device.
	dash := byRef(t, b, "dev:02:00:00:01:00:07")
	attrs(t, dash, map[string]any{"role": "switch", "health": "warning"}) // UPDATING
	if hasRef(b, "dev:") || collecttest.Find([]*sdk.Batch{b}, "device", "name", "no-mac") != nil {
		t.Error("a device with no MAC was emitted")
	}
	if !warnedNet(b, `device "no-mac" has no usable MAC`) {
		t.Errorf("no warning for the device without a MAC: %v", b.Health.Warnings)
	}
	// An unknown model with no feature: no role is set (the cloud's stays), a
	// link-local address is no address, DELETING is a warning state.
	odd := byRef(t, b, "dev:02:00:00:01:00:08")
	absent(t, odd, "role", "mgmt_ip")
	attrs(t, odd, map[string]any{"health": "warning", "part_number": "XYZ-1"})
	if !warnedNet(b, `unknown UniFi model "XYZ-1"`) {
		t.Errorf("no warning for the unknown model: %v", b.Health.Warnings)
	}
	if hasRef(b, "ip:169.254.1.1") {
		t.Error("a link-local address became an ip_address")
	}
	// Ports: index 0 is skipped, a repeated index is one port, an unknown
	// connector is kept upper-cased.
	if hasRef(b, odd.Ref+"/if:Port 0") {
		t.Error("port 0 was emitted")
	}
	if n := len(portsOf(b, odd.Ref)); n != 2 {
		t.Errorf("%d ports on the odd device, want 2 (1 and 2)", n)
	}
	attrs(t, byRef(t, b, odd.Ref+"/if:Port 1"), map[string]any{"oper_status": "up", "speed_mbps": 1000}) // the first of the two entries wins
	attrs(t, byRef(t, b, odd.Ref+"/if:Port 2"), map[string]any{"connector": "FIBRE"})
	if !warnedNet(b, `index "0"`) {
		t.Errorf("no warning for port 0: %v", b.Health.Warnings)
	}
	// The switch with every PoE and connector shape.
	swe := "dev:02:00:00:01:00:02"
	attrs(t, byRef(t, b, swe+"/if:Port 1"), map[string]any{"poe_status": "limited", "poe_standard": "802.3bt", "speed_mbps": 1000})
	attrs(t, byRef(t, b, swe+"/if:Port 2"), map[string]any{"oper_status": "unknown", "poe_status": "unknown", "poe_standard": "802.3af"})
	absent(t, byRef(t, b, swe+"/if:Port 2"), "speed_mbps")
	attrs(t, byRef(t, b, swe+"/if:Port 3"), map[string]any{"connector": "SFP28", "speed_mbps": 25000, "max_speed_mbps": 25000})
	attrs(t, byRef(t, b, swe+"/if:Port 4"), map[string]any{"connector": "QSFP28", "oper_status": "down", "max_speed_mbps": 100000})
	absent(t, byRef(t, b, swe+"/if:Port 4"), "speed_mbps", "poe_status")
	attrs(t, byRef(t, b, swe+"/if:Port 5"), map[string]any{"oper_status": "up", "poe_status": "disabled"})
	absent(t, byRef(t, b, swe+"/if:Port 5"), "speed_mbps") // up with no speed reported: none stored
	// The offline AP: kept from its overview, no ports (its detail answered 404).
	off := byRef(t, b, "dev:02:00:00:01:00:03")
	attrs(t, off, map[string]any{"health": "critical", "role": "access_point"})
	if n := len(portsOf(b, off.Ref)); n != 0 {
		t.Errorf("%d ports on a device whose detail failed", n)
	}
}

// Every port the Network collector reports for the owner's office is a
// template of its device's model in the library, so that an observed
// interface and its template are the same string (#173). It fails when a
// template is named otherwise, and when the collector names a port otherwise.
func TestOfficePortsAreTemplatesOfTheirModel(t *testing.T) {
	b := runNet(t, newFakeNet(t, SetNetwork))
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	ix := devicetypes.NewIndex(models)
	checked := map[string]int{}
	for _, d := range collecttest.Objects([]*sdk.Batch{b}, "device") {
		pn, _ := d.Attributes["part_number"].(string)
		if pn == "" {
			continue
		}
		m := ix.Find("Ubiquiti", "", pn)
		if m == nil {
			t.Errorf("%s: no model for part number %q", d.Ref, pn)
			continue
		}
		tmpl := map[string]bool{}
		for _, p := range m.Model.Ports {
			if p.Group == "interface" {
				tmpl[p.Name] = true
			}
		}
		for _, p := range portsOf(b, d.Ref) {
			if name := p.Identity["name"]; !tmpl[name] {
				t.Errorf("%s (%s): port %q is no interface template of %s", d.Ref, pn, name, m.Model.Slug)
			}
			checked[pn]++
		}
	}
	if checked["UDMPROMAX"] != 11 || checked["USPM24P"] != 26 {
		t.Fatalf("ports checked per model %v: the UDM Pro Max has 11, the switch 26", checked)
	}
}

func portsOf(b *sdk.Batch, dev string) []*sdk.Object {
	var out []*sdk.Object
	for _, o := range b.Objects {
		if o.Type == "interface" && o.Parent == dev {
			out = append(out, o)
		}
	}
	return out
}

func warnedNet(b *sdk.Batch, sub string) bool {
	for _, w := range b.Health.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestUplinkResolution(t *testing.T) {
	f := newFakeNet(t, SetNetworkEdge)
	b := runNet(t, f)
	gw, swe, lab, apx := "dev:02:00:00:01:00:01", "dev:02:00:00:01:00:02", "dev:02:00:00:01:00:11", "dev:02:00:00:01:00:09"
	// A switch uplinked to the gateway; a switch of the lab site uplinked to a
	// gateway of the default site; an AP uplinked to the lab switch.
	for _, p := range [][2]string{{swe, gw}, {lab, gw}, {apx, lab}} {
		if !hasRel(b, "uplinked_to", p[0], p[1]) {
			t.Errorf("no uplinked_to %s -> %s", p[0], p[1])
		}
	}
	// An uplink to a device that was not read: a warning, no relation. A device
	// that names itself: no relation.
	if n := relCount(b, "uplinked_to"); n != 3 {
		t.Errorf("%d uplinked_to, want exactly the 3 above", n)
	}
	if !warnedNet(b, `the uplink of dev:02:00:00:01:00:07 is device "00000000-0000-4000-8000-00000000ffff"`) {
		t.Errorf("no warning for the unknown uplink: %v", b.Health.Warnings)
	}
	for _, r := range b.Relations {
		if r.From.Ref == r.To.Ref {
			t.Errorf("a relation from a device to itself: %+v", r)
		}
	}
	// Reading one site only leaves the cross-site uplink unresolved.
	tg := withOptions(t, f.Target(office), "api: network\nsites: [lab]\n")
	only := collecttest.Run(t, newCollector(t, tg))[0]
	if n := relCount(only, "uplinked_to"); n != 0 {
		t.Errorf("%d uplinked_to with only the lab site read", n)
	}
	if !warnedNet(only, "the uplink of dev:02:00:00:01:00:11") {
		t.Errorf("no warning for the cross-site uplink: %v", only.Health.Warnings)
	}
}

// An id the server supplies goes into a path only when it is a UUID.
func TestNetworkIDsAreValidated(t *testing.T) {
	f := newFakeNet(t, SetNetworkEdge)
	b := runNet(t, f)
	for _, r := range f.Requests() {
		if strings.Contains(r.Path, "..") || strings.Contains(r.Path, "not-a-uuid") || strings.Contains(r.Path, "%") {
			t.Errorf("a server-supplied id reached a path: %+v", r)
		}
	}
	// "/v1/info" is asked once, by the probe, not a second time through the id.
	n := 0
	for _, r := range f.Requests() {
		if r.Path == "/v1/info" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("/v1/info asked %d times", n)
	}
	if !warnedNet(b, `site "Broken" has an id that is not a UUID`) || !warnedNet(b, `device "weird-id" has an id that is not a UUID`) {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
	// The device with the bad id is kept from its overview; the run is partial
	// (its ports were not refreshed) but not a fault of the console.
	attrs(t, byRef(t, b, "dev:02:00:00:01:00:04"), map[string]any{"role": "access_point"})
	// The run is partial. What fails it is the offline AP's 404, not the ids.
	if b.Scope != sdk.ScopePartial || strings.Contains(b.Health.Error, "weird-id") || strings.Contains(b.Health.Error, "Broken") {
		t.Errorf("scope %s, health %+v", b.Scope, b.Health)
	}
}

func TestNetworkPaginationFollowsOffset(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	f.MaxLimit(3)
	b := runNet(t, f)
	if n := count(b, "device"); n != 10 {
		t.Fatalf("%d devices, want 10", n)
	}
	var offsets []string
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, "/devices") {
			q, _ := url.ParseQuery(r.Query)
			if q.Get("limit") != strconv.Itoa(netPageSize) {
				t.Errorf("limit %q asked, want %d", q.Get("limit"), netPageSize)
			}
			offsets = append(offsets, q.Get("offset"))
		}
	}
	if got := strings.Join(offsets, ","); got != "0,3,6,9" {
		t.Errorf("offsets %s, want 0,3,6,9", got)
	}
	if b.Scope != sdk.ScopeFull {
		t.Errorf("scope %s", b.Scope)
	}
}

func TestNetworkPaginationStopsWhenOffsetIgnored(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	// A server that ignores offset and claims more items than it returns.
	f.SetBody("/v1/sites/"+officeSite+"/devices", `{"offset":0,"limit":200,"count":1,"totalCount":99,"data":[{"id":"`+uidOf(0x101)+`","macAddress":"02:00:00:00:00:01","name":"Office A"}]}`)
	b := runNet(t, f)
	if b.Health.OK || !strings.Contains(b.Health.Error, "ignores offset") {
		t.Errorf("health %+v", b.Health)
	}
	n := 0
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, "/devices") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d list requests against a server that ignores offset, want 2", n)
	}
	if b.Scope != sdk.ScopePartial {
		t.Errorf("scope %s", b.Scope)
	}
}

// A server that pages for ever is cut at maxPages.
func TestListOffsetStopsAtMaxPages(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"offset":%d,"limit":1,"count":1,"totalCount":100000,"data":[{"id":"%d"}]}`, n, n)
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	_, err := listOffset[netSite](context.Background(), newClient(srv.Client(), base, "k", APINetwork), "/v1/sites", nil)
	if err == nil || kindOf(err) != errMalformed || !strings.Contains(err.Error(), "more than 50 pages") {
		t.Fatalf("got %v", err)
	}
	if calls != maxPages {
		t.Errorf("%d calls, want %d", calls, maxPages)
	}
}

func TestListOffsetNeedsADataArray(t *testing.T) {
	for name, body := range map[string]string{
		"no data":      `{"offset":0,"limit":25,"count":0,"totalCount":0}`,
		"data object":  `{"offset":0,"limit":25,"count":0,"totalCount":0,"data":{}}`,
		"data null":    `{"offset":0,"limit":25,"count":0,"totalCount":0,"data":null}`,
		"item is text": `{"offset":0,"limit":25,"count":1,"totalCount":1,"data":["x"]}`,
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		base, _ := url.Parse(srv.URL)
		_, err := listOffset[netSite](context.Background(), newClient(srv.Client(), base, "k", APINetwork), "/v1/sites", nil)
		srv.Close()
		if err == nil || kindOf(err) != errMalformed {
			t.Errorf("%s: got %v, want a malformed answer", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "Network Integration API") {
			t.Errorf("%s: %v does not name the API", name, err)
		}
	}
}

func TestNetworkReadsOnlyItsEndpoints(t *testing.T) {
	for _, set := range []string{SetNetwork, SetNetworkEdge} {
		f := newFakeNet(t, set)
		runNet(t, f)
		seen := map[string]int{}
		for _, r := range f.Requests() {
			shape := netPaths.FindStringSubmatch(r.Path)
			if shape == nil {
				t.Fatalf("%s: %+v", set, r)
			}
			seen[strings.SplitN(shape[1], "/", 2)[0]]++
			if r.Method != http.MethodGet {
				t.Errorf("%s %s", r.Method, r.Path)
			}
		}
		if seen["info"] != 1 || seen["sites"] < 3 {
			t.Errorf("%s: %v", set, seen)
		}
	}
	// The office: info, sites, one device list, ten details.
	f := newFakeNet(t, SetNetwork)
	runNet(t, f)
	if n := len(f.Requests()); n != 13 {
		t.Errorf("%d requests for the office, want 13", n)
	}
}

func TestNetworkFakeRejectsWrites(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	c := f.server.Client()
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(m, f.URL()+networkPrefix+"/v1/sites", nil)
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
	// Outside the prefix there is nothing: the cloud's paths do not exist on a console.
	req, _ := http.NewRequest(http.MethodGet, f.URL()+"/v1/sites", nil)
	req.Header.Set("X-API-KEY", APIKey)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a path outside the prefix: status %d", resp.StatusCode)
	}
}

func TestNetworkKeyOnlyInHeader(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	runNet(t, f)
	for _, r := range f.Requests() {
		if strings.Contains(r.Path, APIKey) || strings.Contains(r.Query, APIKey) {
			t.Errorf("the key is in the URL: %+v", r)
		}
		if !r.HasKey {
			t.Errorf("a request without the key in X-API-KEY: %+v", r)
		}
	}
}

// The key never reaches a log line, a health record, a warning or a batch,
// whatever goes wrong: it is run through the real runner, which logs.
func TestNetworkKeyNeverLeaks(t *testing.T) {
	site := "/v1/sites/" + edgeSite + "/devices"
	for _, tc := range []struct {
		name    string
		prepare func(f *FakeNetwork, tg *collect.Target)
	}{
		{"ok", func(*FakeNetwork, *collect.Target) {}},
		{"401", func(f *FakeNetwork, _ *collect.Target) { f.SetKey("someone-elses-key") }},
		{"403 sites", func(f *FakeNetwork, _ *collect.Target) { f.Fail("/v1/sites", 403) }},
		{"429 devices", func(f *FakeNetwork, _ *collect.Target) { f.Fail(site, 429) }},
		{"500 detail", func(f *FakeNetwork, _ *collect.Target) { f.Fail(site+"/"+uidOf(0x402), 500) }},
		{"malformed", func(f *FakeNetwork, _ *collect.Target) { f.Malformed("") }},
		{"closed server", func(f *FakeNetwork, _ *collect.Target) { f.server.Close() }},
		{"timeout", func(f *FakeNetwork, tg *collect.Target) {
			f.Slow("", 2*time.Second)
			tg.Timeout = 200 * time.Millisecond
		}},
		{"redirect", func(f *FakeNetwork, _ *collect.Target) { f.Redirect("/v1/info", "https://127.0.0.1:1/v1/info") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeNet(t, SetNetworkEdge)
			tg := withOptions(t, f.Target(office), "api: network\n")
			tc.prepare(f, &tg)
			var logs bytes.Buffer
			var mu sync.Mutex
			var batches []*sdk.Batch
			r, err := runner.New(runner.Config{
				Targets: []collect.Target{tg},
				Emitter: sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
					mu.Lock()
					defer mu.Unlock()
					batches = append(batches, b)
					return nil
				}),
				Log:    slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				Jitter: -1,
			})
			if err != nil {
				t.Fatal(err)
			}
			r.RunOnce(context.Background())
			doc, err := json.Marshal(batches)
			if err != nil {
				t.Fatal(err)
			}
			if len(batches) == 0 || !strings.Contains(logs.String(), `"source":"unifi:office"`) {
				t.Fatalf("the run left no trace (%d batches, logs %q): the check would be vacuous", len(batches), logs.String())
			}
			for what, text := range map[string]string{"log": logs.String(), "batches": string(doc)} {
				if strings.Contains(text, APIKey) {
					t.Errorf("the API key is in the %s: %s", what, text)
				}
			}
			if err := f.OnlyReads(); err != nil {
				t.Error(err)
			}
		})
	}
}

// A server that echoes the key in an error message has it removed (the client
// is shared with the Site Manager collector; this holds the network path).
func TestNetworkErrorMessagesDoNotEchoTheKey(t *testing.T) {
	const key = "secret-network-key-123"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `{"statusCode":403,"statusName":"FORBIDDEN","code":"api.forbidden","message":"key %s may not read %s"}`, r.Header.Get("X-API-KEY"), r.URL.Path)
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	_, err := getJSON[netInfo](context.Background(), newClient(srv.Client(), base, key, APINetwork), "/v1/info", nil)
	if err == nil || kindOf(err) != errForbidden {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("error %q", err)
	}
}

func TestNetworkRedirectNotFollowed(t *testing.T) {
	other := newFakeNet(t, SetNetwork)
	f := newFakeNet(t, SetNetwork)
	f.Redirect("/v1/info", other.URL()+networkPrefix+"/v1/info")
	b := runNet(t, f)
	if b.Health.OK || len(b.Objects) != 0 {
		t.Fatalf("health %+v, %d objects", b.Health, len(b.Objects))
	}
	if n := len(other.Requests()); n != 0 {
		t.Fatal("the redirect was followed: the key went to another origin")
	}
	if reqs := f.Requests(); len(reqs) != 1 || !reqs[0].HasKey {
		t.Fatalf("requests %+v", reqs)
	}
}

func TestNetworkAuthRefused(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	f.SetKey("another-key")
	b := runNet(t, f)
	if b.Health.OK || b.Scope != sdk.ScopePartial || len(b.Objects) != 0 {
		t.Fatalf("scope %s, %d objects, health %+v", b.Scope, len(b.Objects), b.Health)
	}
	// The error says what to do, and the console's own message is kept.
	if !strings.Contains(b.Health.Error, "authentication refused") || !strings.Contains(b.Health.Error, "Missing credentials") {
		t.Errorf("error %q", b.Health.Error)
	}
	if n := len(f.Requests()); n != 1 {
		t.Errorf("%d requests after a refused key, want exactly 1", n)
	}
}

func TestNetworkNotTheAPI(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	f.Malformed("")
	b := runNet(t, f)
	if b.Health.OK || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, "is this the Network Integration API?") {
		t.Errorf("health %+v, %d objects", b.Health, len(b.Objects))
	}
}

func TestNetworkTotalFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(*FakeNetwork)
		want    string
	}{
		"info 500":      {func(f *FakeNetwork) { f.Fail("/v1/info", 500) }, "HTTP 500"},
		"sites 403":     {func(f *FakeNetwork) { f.Fail("/v1/sites", 403) }, "forbidden"},
		"sites 404":     {func(f *FakeNetwork) { f.Fail("/v1/sites", 404) }, "not found"},
		"sites no list": {func(f *FakeNetwork) { f.SetBody("/v1/sites", `{"hello":1}`) }, "no data array"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeNet(t, SetNetwork)
			tc.prepare(f)
			b := runNet(t, f)
			if b.Health.OK || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, tc.want) {
				t.Errorf("health %+v, %d objects", b.Health, len(b.Objects))
			}
		})
	}
}

func TestNetworkPartial(t *testing.T) {
	t.Run("a site's device list fails", func(t *testing.T) {
		f := newFakeNet(t, SetNetworkEdge)
		f.Fail("/v1/sites/"+labSite+"/devices", 500)
		b := runNet(t, f)
		if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, "site Lab") {
			t.Errorf("scope %s, health %+v", b.Scope, b.Health)
		}
		if collecttest.Find([]*sdk.Batch{b}, "device", "base_mac", "02:00:00:01:00:01") == nil {
			t.Error("the other site's devices are gone")
		}
		if collecttest.Find([]*sdk.Batch{b}, "device", "base_mac", "02:00:00:01:00:11") != nil {
			t.Error("the lab switch was emitted although its list failed")
		}
	})
	t.Run("a detail call fails", func(t *testing.T) {
		f := newFakeNet(t, SetNetwork)
		f.Fail("/v1/sites/"+officeSite+"/devices/"+uidOf(0x103), 500)
		b := runNet(t, f)
		if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, "device ap-02") {
			t.Errorf("scope %s, health %+v", b.Scope, b.Health)
		}
		// The device is kept; its ports and its uplink are not refreshed.
		if !hasRef(b, devRef(3)) || len(portsOf(b, devRef(3))) != 0 || hasRel(b, "uplinked_to", devRef(3), devRef(10)) {
			t.Error("a device whose detail failed lost more than its ports and uplink, or kept them")
		}
		if n := count(b, "interface"); n != 44 {
			t.Errorf("%d interfaces, want 44", n)
		}
	})
	t.Run("the edge set is partial", func(t *testing.T) {
		b := runNet(t, newFakeNet(t, SetNetworkEdge))
		if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, "device ap-offline") || !strings.Contains(b.Health.Error, "not found") {
			t.Errorf("scope %s, health %+v", b.Scope, b.Health)
		}
	})
	t.Run("a refused detail call stops the run", func(t *testing.T) {
		f := newFakeNet(t, SetNetwork)
		f.Fail("/v1/sites/"+officeSite+"/devices/"+uidOf(0x101), 401)
		b := runNet(t, f)
		if n := len(f.Requests()); n != 4 {
			t.Errorf("%d requests, want 4 (info, sites, devices, the refused detail)", n)
		}
		if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, "authentication refused") || !strings.Contains(b.Health.Error, "not read after a refused or rate-limited call") {
			t.Errorf("health %+v", b.Health)
		}
		if n := count(b, "device"); n != 10 {
			t.Errorf("%d devices: the overview is still a source", n)
		}
	})
	t.Run("a rate limit stops the run and says when to retry", func(t *testing.T) {
		f := newFakeNet(t, SetNetwork)
		f.Fail("/v1/sites/"+officeSite+"/devices/"+uidOf(0x102), 429)
		b := runNet(t, f)
		if n := len(f.Requests()); n != 5 {
			t.Errorf("%d requests, want 5", n)
		}
		if !strings.Contains(b.Health.Error, "retry after 30") {
			t.Errorf("health %+v", b.Health)
		}
	})
}

func TestNetworkSitesFilter(t *testing.T) {
	f := newFakeNet(t, SetNetworkEdge)
	run := func(opts string) *sdk.Batch {
		tg := withOptions(t, f.Target(office), "api: network\n"+opts)
		return collecttest.Run(t, newCollector(t, tg))[0]
	}
	b := run("sites: [LAB]\n")
	if n := count(b, "device"); n != 1 || collecttest.Find([]*sdk.Batch{b}, "device", "name", "lab-sw") == nil {
		t.Errorf("%d devices with sites: [LAB]", n)
	}
	// By internal reference and by name; an entry that matches nothing is a warning.
	b = run("sites: [default, nowhere]\n")
	if collecttest.Find([]*sdk.Batch{b}, "device", "name", "uxg-gw") == nil || collecttest.Find([]*sdk.Batch{b}, "device", "name", "lab-sw") != nil {
		t.Error("sites: [default] did not select the default site only")
	}
	if !warnedNet(b, `options.sites: "nowhere" matches no site`) {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
	// None matching is a failure, with no further request.
	n := len(f.Requests())
	b = run("sites: [nowhere]\n")
	if b.Health.OK || !strings.Contains(b.Health.Error, "options.sites matches no site") {
		t.Errorf("health %+v", b.Health)
	}
	if got := len(f.Requests()) - n; got != 2 {
		t.Errorf("%d requests for a filter that matches nothing, want 2 (info, sites)", got)
	}
}

func TestNetworkOptionsValidation(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	good := withOptions(t, f.Target(office), "api: network\n")
	cases := []struct {
		name   string
		mutate func(*collect.Target)
		want   string
	}{
		{"unknown api", func(tg *collect.Target) { tg.Options = optionsNode(t, "api: cloud\n") }, `"cloud" is not site-manager or network`},
		{"hosts with network", func(tg *collect.Target) { tg.Options = optionsNode(t, "api: network\nhosts: [a]\n") }, "options.hosts applies to the Site Manager API"},
		{"isp_metrics with network", func(tg *collect.Target) { tg.Options = optionsNode(t, "api: network\nisp_metrics: false\n") }, "options.isp_metrics applies to the Site Manager API only"},
		{"sites with site-manager", func(tg *collect.Target) { tg.Options = optionsNode(t, "sites: [default]\n") }, "options.sites applies to api: network"},
		{"sites with explicit site-manager", func(tg *collect.Target) { tg.Options = optionsNode(t, "api: site-manager\nsites: [default]\n") }, "options.sites applies to api: network"},
		{"empty site", func(tg *collect.Target) { tg.Options = optionsNode(t, "api: network\nsites: [\"\"]\n") }, "empty site name"},
		{"no address", func(tg *collect.Target) { tg.Address = "" }, "needs the address of the console"},
		{"http address", func(tg *collect.Target) { tg.Address = "http://192.0.2.1" }, "must be https"},
		{"address with a path", func(tg *collect.Target) { tg.Address = "https://192.0.2.1/proxy/network/integration" }, "has a path"},
		{"address with the cloud's path", func(tg *collect.Target) { tg.Address = "https://192.0.2.1/v1" }, "has a path"},
		{"unknown option", func(tg *collect.Target) { tg.Options = optionsNode(t, "api: network\nsite: [a]\n") }, "site"},
		{"role not in the schema", func(tg *collect.Target) { tg.Options = optionsNode(t, "api: network\nroles: [\"X=spine\"]\n") }, `"spine" is not a device role`},
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
	// The valid forms: the prefix is added, a trailing slash and a bare host are accepted.
	for addr, want := range map[string]string{"https://192.0.2.1": "https://192.0.2.1" + networkPrefix, "192.0.2.1:8443": "https://192.0.2.1:8443" + networkPrefix, "https://192.0.2.1/": "https://192.0.2.1" + networkPrefix} {
		tg := good
		tg.Address = addr
		c, err := New(tg)
		if err != nil || c.(*collector).base.String() != want {
			t.Errorf("address %q: %v", addr, err)
		}
	}
	// The default is the Site Manager, and its options still work.
	tg := withOptions(t, f.Target(office), "api: site-manager\nhosts: [a]\nisp_metrics: false\n")
	if c, err := New(tg); err != nil || c.(*collector).api != APISiteManager {
		t.Errorf("site-manager options: %v", err)
	}
}

func TestNewFakeNetworkUnknownSet(t *testing.T) {
	for _, set := range []string{"nope", SetRecorded, SetSynthesised} {
		if _, err := NewFakeNetwork(set); err == nil {
			t.Errorf("set %q was accepted", set)
		}
	}
}

// The target of the collector is the console's own address: its answer comes
// back under the right source with the console's Network version.
func TestNetworkContextCancelled(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got *sdk.Batch
	tg := withOptions(t, f.Target(office), "api: network\n")
	err := newCollector(t, tg).Collect(ctx, sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
		got = b
		return nil
	}))
	if err != nil || got == nil || got.Health.OK {
		t.Fatalf("err %v, batch %+v", err, got)
	}
}
