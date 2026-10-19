package devicetypes_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const minimal = `
manufacturer: Acme
model: Box 1
slug: acme-box-1
u_height: 1
`

func mustParse(t *testing.T, y string) *devicetypes.Model {
	t.Helper()
	m, err := devicetypes.Parse([]byte(y), devicetypes.OriginLocal, "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEmbeddedLibraryLoadsAndIsValid(t *testing.T) {
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) < 20 {
		t.Fatalf("library holds %d models, want at least 20", len(models))
	}
	origins := map[string]int{}
	vendors := map[string]bool{}
	for _, m := range models {
		origins[m.Origin]++
		vendors[m.Vendor] = true
		if m.UHeight <= 0 || m.Slug == "" || m.Model == "" {
			t.Errorf("%s: incomplete model %+v", m.Path, m)
		}
		if len(m.Ports) == 0 {
			t.Errorf("%s: no port template", m.Path)
		}
	}
	if origins[devicetypes.OriginUpstream] == 0 || origins[devicetypes.OriginLocal] == 0 {
		t.Errorf("origins %v: want upstream and local models", origins)
	}
	for _, v := range []string{"Arista", "MikroTik", "HPE", "Dell", "Netgate", "Generic"} {
		if !vendors[v] {
			t.Errorf("no %s model", v)
		}
	}
}

func TestParseRejectsInvalidFiles(t *testing.T) {
	cases := map[string]string{
		"no vendor":         "model: A\nslug: a\nu_height: 1\n",
		"no model":          "manufacturer: V\nslug: a\nu_height: 1\n",
		"bad slug":          "manufacturer: V\nmodel: A\nslug: Bad Slug\nu_height: 1\n",
		"no u_height":       "manufacturer: V\nmodel: A\nslug: a\n",
		"zero u_height":     "manufacturer: V\nmodel: A\nslug: a\nu_height: 0\n",
		"huge u_height":     "manufacturer: V\nmodel: A\nslug: a\nu_height: 500\n",
		"unknown airflow":   minimal + "airflow: sideways\n",
		"unknown unit":      minimal + "weight: 3\nweight_unit: stone\n",
		"negative weight":   minimal + "weight: -3\nweight_unit: kg\n",
		"nameless port":     minimal + "interfaces:\n  - type: 1000base-t\n",
		"duplicate port":    minimal + "interfaces:\n  - name: e1\n  - name: e1\n",
		"negative draw":     minimal + "power-ports:\n  - name: p\n    maximum_draw: -5\n",
		"not yaml":          "manufacturer: [unclosed\n",
		"u_height not text": "manufacturer: V\nmodel: A\nslug: a\nu_height: tall\n",
	}
	for name, y := range cases {
		if _, err := devicetypes.Parse([]byte(y), devicetypes.OriginLocal, "x.yaml"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The same port name in two groups is fine.
	if _, err := devicetypes.Parse([]byte(minimal+"interfaces:\n  - name: x\nconsole-ports:\n  - name: x\n"), "t", "x.yaml"); err != nil {
		t.Errorf("same name in two groups refused: %v", err)
	}
}

func TestParseConvertsUpstreamValues(t *testing.T) {
	m := mustParse(t, `
manufacturer: Acme
model: Box 1
slug: acme-box-1
part_number: P-1
u_height: 2
is_full_depth: true
airflow: rear-to-front
weight: 21
weight_unit: lb
comments: ignored
subdevice_role: parent
module-bays:
  - name: bay
interfaces:
  - name: Ethernet1
    type: 25gbase-x-sfp28
    mgmt_only: true
console-ports:
  - name: Console
power-ports:
  - name: PSU1
    maximum_draw: 750
  - name: PSU2
    maximum_draw: 750
`)
	if m.Airflow != "rear_to_front" || !m.FullDepth || m.UHeight != 2 || m.PartNumber != "P-1" {
		t.Fatalf("%+v", m)
	}
	if math.Abs(m.WeightKg-9.525) > 1e-9 {
		t.Fatalf("21 lb = %v kg, want 9.525", m.WeightKg)
	}
	if m.PSUCount() != 2 {
		t.Fatalf("PSUCount %d", m.PSUCount())
	}
	got := map[string]devicetypes.Port{}
	for _, p := range m.Ports {
		got[p.Group+"/"+p.Name] = p
	}
	if p := got["interface/Ethernet1"]; p.Type != "25gbase-x-sfp28" || !p.MgmtOnly {
		t.Errorf("interface %+v", p)
	}
	if got["power/PSU2"].MaxDrawW != 750 || got["console/Console"].Name == "" {
		t.Errorf("ports %+v", got)
	}
	g := mustParse(t, minimal+"weight: 2500\nweight_unit: g\n")
	if g.WeightKg != 2.5 {
		t.Errorf("2500 g = %v kg", g.WeightKg)
	}
	k := mustParse(t, minimal+"weight: 13.04\n")
	if k.WeightKg != 13.04 {
		t.Errorf("default unit is kg, got %v", k.WeightKg)
	}
}

func TestLoadRefusesDuplicateSlugAndBadFile(t *testing.T) {
	fs := fstest.MapFS{
		"lib/A/one.yaml": {Data: []byte(minimal)},
		"lib/B/two.yaml": {Data: []byte(minimal)},
	}
	if _, err := devicetypes.Load(fs, "lib"); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("duplicate slug: %v", err)
	}
	fs = fstest.MapFS{
		"lib/A/one.yaml": {Data: []byte(minimal)},
		"lib/A/bad.yaml": {Data: []byte("manufacturer: X\n")},
	}
	if _, err := devicetypes.Load(fs, "lib"); err == nil {
		t.Fatal("a bad file must fail the whole load")
	}
	fs = fstest.MapFS{
		"lib/A/one.yaml":  {Data: []byte(minimal)},
		"lib/A/readme.md": {Data: []byte("not a model")},
		"lib/trica/x.yml": {Data: []byte(strings.Replace(minimal, "acme-box-1", "acme-box-2", 1))},
	}
	ms, err := devicetypes.Load(fs, "lib")
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v %d", err, len(ms))
	}
	if ms[0].Origin != devicetypes.OriginUpstream || ms[1].Origin != devicetypes.OriginLocal {
		t.Fatalf("origins %s %s", ms[0].Origin, ms[1].Origin)
	}
}

func defaultIndex(t *testing.T) *devicetypes.Index {
	t.Helper()
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	return devicetypes.NewIndex(models)
}

func TestMatching(t *testing.T) {
	ix := defaultIndex(t)
	cases := []struct {
		name, vendor, model, pn string
		slug, how               string
	}{
		{"exact", "Arista", "DCS-7050SX3-48YC8-F", "", "arista-dcs-7050sx3-48yc8-f", devicetypes.ByModel},
		{"vendor alias", "Arista Networks", "DCS-7050TX-48", "", "arista-dcs-7050tx-48", devicetypes.ByModel},
		{"case and punctuation", "ARISTA", "dcs 7050tx 48", "", "arista-dcs-7050tx-48", devicetypes.ByModel},
		{"dell inc", "Dell Inc.", "PowerEdge R640", "", "dell-poweredge-r640", devicetypes.ByModel},
		{"vendor repeated in model", "Dell", "Dell PowerEdge R740", "", "dell-poweredge-r740", devicetypes.ByModel},
		{"hp alias", "HP", "ProLiant DL360 Gen10", "", "hpe-proliant-dl360-gen10", devicetypes.ByModel},
		{"hewlett packard enterprise", "Hewlett Packard Enterprise", "ProLiant DL380 Gen9", "", "hpe-proliant-dl380-gen9", devicetypes.ByModel},
		{"routerboard plus sign", "Routerboard", "CRS326-24G-2S+RM", "", "mikrotik-crs326-24g-2s-plus-rm", devicetypes.ByModel},
		{"vendor repeated, punctuation differs from slug", "MikroTik", "MikroTik CRS326-24G-2S+RM", "", "mikrotik-crs326-24g-2s-plus-rm", devicetypes.ByModel},
		{"long vendor name repeated", "Hewlett Packard Enterprise", "Hewlett Packard Enterprise ProLiant DL380 Gen9", "", "hpe-proliant-dl380-gen9", devicetypes.ByModel},
		{"part number only", "HPE", "unknown chassis", "673648-B21", "hpe-proliant-dl360p-gen8", devicetypes.ByPartNumber},
		// The upstream part number of the DL360 Gen10 is the 8SFF NC CTO SKU: the chassis variant that lists it wins.
		{"variant sku over upstream part number", "HPE", "ProLiant DL360 Gen10", "P19766-B21", "hpe-proliant-dl360-gen10-8sff", devicetypes.BySKU},
		{"slug as model", "MikroTik", "mikrotik-crs326-24g-2s-plus-rm", "", "mikrotik-crs326-24g-2s-plus-rm", devicetypes.ByModel},
		{"airflow variant", "Arista Networks", "DCS-7050SX3-48YC8", "", "arista-dcs-7050sx3-48yc8", devicetypes.ByVariant},
		{"own model", "Generic", "Generic 1U pfSense appliance", "", "generic-1u-pfsense-appliance", devicetypes.ByModel},
	}
	for _, c := range cases {
		m := ix.Find(c.vendor, c.model, c.pn)
		if m == nil {
			t.Errorf("%s: no match", c.name)
			continue
		}
		if m.How != c.how {
			t.Errorf("%s: matched by %s, want %s", c.name, m.How, c.how)
		}
		if m.Model.Slug != c.slug {
			t.Errorf("%s: slug %s, want %s", c.name, m.Model.Slug, c.slug)
		}
	}
	for _, c := range [][3]string{
		{"Arista", "DCS-9999", ""},            // unknown model
		{"Cisco", "PowerEdge R640", ""},       // model of another vendor
		{"", "PowerEdge R640", ""},            // no vendor: never guess
		{"Dell", "", ""},                      // no model, no part number
		{"Dell", "PowerEdge R6400", "XYZ"},    // near miss is a miss
		{"Arista", "DCS-7050SX3-48YC8-R", ""}, // other airflow is not vendored: a variant match must not apply to the suffixed name
	} {
		if m := ix.Find(c[0], c[1], c[2]); m != nil {
			t.Errorf("%v matched %s", c, m.Model.Slug)
		}
	}
}

func TestVariantMatchClearsAirflowAndPartNumber(t *testing.T) {
	m := defaultIndex(t).Find("Arista", "DCS-7050SX3-48YC8", "")
	if m == nil {
		t.Fatal("no variant match")
	}
	if m.Model.Airflow != "" || m.Model.PartNumber != "" {
		t.Fatalf("a variant match claims no airflow nor part number: %+v", m.Model)
	}
	if m.Model.UHeight != 1 || len(m.Model.Ports) == 0 {
		t.Fatalf("variant lost the shared facts: %+v", m.Model)
	}
	if strings.HasSuffix(m.Model.Slug, "-f") || strings.HasSuffix(m.Model.Model, "-F") {
		t.Fatalf("variant keeps the suffix: %s / %s", m.Model.Slug, m.Model.Model)
	}
}

func fam(slug, model string, u float64, ports ...string) *devicetypes.Model {
	m := &devicetypes.Model{Slug: slug, Vendor: "Arista", Model: model, UHeight: u, Airflow: "front_to_rear"}
	for _, p := range ports {
		m.Ports = append(m.Ports, devicetypes.Port{Group: "interface", Name: p})
	}
	return m
}

func TestVariantRequiresAgreement(t *testing.T) {
	agree := devicetypes.NewIndex([]*devicetypes.Model{fam("x-f", "X-1-F", 1, "e1"), fam("x-r", "X-1-R", 1, "e1")})
	if agree.Find("Arista", "X-1", "") == nil {
		t.Fatal("variants that agree must match the base name")
	}
	for name, other := range map[string]*devicetypes.Model{
		"height": fam("x-r", "X-1-R", 2, "e1"),
		"ports":  fam("x-r", "X-1-R", 1, "e1", "e2"),
	} {
		ix := devicetypes.NewIndex([]*devicetypes.Model{fam("x-f", "X-1-F", 1, "e1"), other})
		if m := ix.Find("Arista", "X-1", ""); m != nil {
			t.Errorf("variants differing by %s matched: %+v", name, m.Model)
		}
	}
	d := fam("x-r", "X-1-R", 1, "e1")
	d.FullDepth = true
	if devicetypes.NewIndex([]*devicetypes.Model{fam("x-f", "X-1-F", 1, "e1"), d}).Find("Arista", "X-1", "") != nil {
		t.Error("variants differing by depth matched")
	}
	w := fam("x-r", "X-1-R", 1, "e1")
	w.WeightKg = 9
	if devicetypes.NewIndex([]*devicetypes.Model{fam("x-f", "X-1-F", 1, "e1"), w}).Find("Arista", "X-1", "") != nil {
		t.Error("variants differing by weight matched")
	}
	// An exact model of the base name wins over a synthesised variant.
	ix := devicetypes.NewIndex([]*devicetypes.Model{fam("x-f", "X-1-F", 1, "e1"), fam("x", "X-1", 3, "e1")})
	if m := ix.Find("Arista", "X-1", ""); m == nil || m.How != devicetypes.ByModel || m.Model.UHeight != 3 {
		t.Errorf("exact base model lost: %+v", m)
	}
}

func TestAmbiguousNameMatchesNothing(t *testing.T) {
	a := fam("a", "Same", 1, "e1")
	b := fam("b", "Same", 2, "e1")
	ix := devicetypes.NewIndex([]*devicetypes.Model{a, b})
	if m := ix.Find("Arista", "Same", ""); m != nil {
		t.Fatalf("ambiguous name matched %s", m.Model.Slug)
	}
	if m := ix.Find("Arista", "", "a"); m == nil || m.Model != a {
		t.Fatalf("the slug stays unambiguous: %+v", m)
	}
}

func TestBatchShape(t *testing.T) {
	ix := defaultIndex(t)
	at := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	devs := []devicetypes.Device{
		{ID: "00000000-0000-0000-0000-000000000002", Vendor: "Dell Inc.", Model: "PowerEdge R640"},
		{ID: "00000000-0000-0000-0000-000000000001", Vendor: "Dell Inc.", Model: "PowerEdge R640"},
		{ID: "00000000-0000-0000-0000-000000000003", Vendor: "Acme", Model: "Unknown"},
	}
	b, links := ix.Batch(devs, at)
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	if b.Source != devicetypes.Source || b.Scope != sdk.ScopeFull || b.Confidence != sdk.Declared || !b.ObservedAt.Equal(at) {
		t.Fatalf("batch header %+v", b)
	}
	if len(b.Objects) != 1 || len(b.Relations) != 2 || len(links) != 2 {
		t.Fatalf("objects %d relations %d links %d, want 1, 2, 2 (one model shared by two devices, the unknown one skipped)", len(b.Objects), len(b.Relations), len(links))
	}
	o := b.Objects[0]
	if o.Type != "device_type" || o.Identity["slug"] != "dell-poweredge-r640" {
		t.Fatalf("%+v", o)
	}
	if o.Attributes["u_height"] != 1.0 || o.Attributes["psu_count"] != 2 || o.Attributes["vendor"] != "Dell" {
		t.Fatalf("attributes %+v", o.Attributes)
	}
	if ports, _ := o.Attributes["ports"].([]devicetypes.Port); len(ports) == 0 {
		t.Fatalf("no port templates in %+v", o.Attributes)
	}
	r := b.Relations[0]
	if r.Type != "instance_of" || r.From.Identity[sdk.IDKey] != "00000000-0000-0000-0000-000000000001" || r.To.Ref != o.Ref {
		t.Fatalf("relation %+v (devices must come in a stable order)", r)
	}
	if b2, _ := ix.Batch(nil, at); len(b2.Objects) != 0 || len(b2.Relations) != 0 {
		t.Fatal("no device, no claim")
	}
}

// ------------------------------------------------------------ engine tests

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newEngine(t *testing.T) (*reconcile.Engine, *store.Store, *clock) {
	t.Helper()
	s := testdb.New(t)
	c := &clock{t: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	reg := schema.Builtin()
	return &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }, Now: c.now}, s, c
}

func addDevice(t *testing.T, e *reconcile.Engine, c *clock, serial, vendor, model string) string {
	t.Helper()
	b := sdk.NewBatch("collector:"+serial, sdk.ScopePartial)
	b.ObservedAt = c.t
	b.Object("d", "device").Identify("serial", serial).Set("vendor", vendor).Set("model", model).Set("role", "server")
	r, err := e.Apply(context.Background(), b, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return r.Objects["d"]
}

func deviceType(t *testing.T, s *store.Store, slug string) *store.Object {
	t.Helper()
	objs, _, err := s.List(context.Background(), store.ListFilter{Type: "device_type", Attrs: map[string]any{"slug": slug}})
	if err != nil || len(objs) != 1 {
		t.Fatalf("device_type %s: %d %v", slug, len(objs), err)
	}
	return objs[0]
}

func instanceOf(t *testing.T, s *store.Store, deviceID string) []store.Relation {
	t.Helper()
	rels, err := s.RelationsOf(context.Background(), deviceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Relation
	for _, r := range rels {
		if r.Type == "instance_of" {
			out = append(out, r)
		}
	}
	return out
}

func TestSyncDeclaresModelAndLinksDevices(t *testing.T) {
	e, s, c := newEngine(t)
	ctx := context.Background()
	ix := defaultIndex(t)
	r640 := addDevice(t, e, c, "SN-R640", "Dell Inc.", "PowerEdge R640")
	sw := addDevice(t, e, c, "SN-SW", "Arista Networks", "DCS-7050SX3-48YC8-F")
	other := addDevice(t, e, c, "SN-X", "Acme", "Mystery box")
	c.advance(time.Minute)

	res, links, err := devicetypes.Sync(ctx, e, ix, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || res.RelationsOpened != 2 {
		t.Fatalf("links %d, relations opened %d", len(links), res.RelationsOpened)
	}
	dt := deviceType(t, s, "dell-poweredge-r640")
	if dt.Attrs["u_height"] != 1.0 || dt.Attrs["vendor"] != "Dell" || dt.Attrs["psu_count"] != 2.0 {
		t.Fatalf("attributes %+v", dt.Attrs)
	}
	var ports []map[string]any
	raw, _ := json.Marshal(dt.Attrs["ports"])
	if err := json.Unmarshal(raw, &ports); err != nil || len(ports) < 5 {
		t.Fatalf("port templates %d %v", len(ports), err)
	}
	if rels := instanceOf(t, s, r640); len(rels) != 1 || rels[0].ToID != dt.ID || rels[0].Source != devicetypes.Source || rels[0].Confidence != "declared" {
		t.Fatalf("relation %+v", rels)
	}
	if w := deviceType(t, s, "arista-dcs-7050sx3-48yc8-f").Attrs["weight_kg"]; w != 9.525 {
		t.Fatalf("weight %v, want 9.525 (21 lb)", w)
	}
	if len(instanceOf(t, s, sw)) != 1 {
		t.Fatal("switch not linked")
	}
	if len(instanceOf(t, s, other)) != 0 {
		t.Fatal("an unknown device must not be linked")
	}
	// The library never touches the device itself.
	d, _ := s.Object(ctx, r640)
	if _, has := d.Attrs["u_height"]; has || d.Attrs["model"] != "PowerEdge R640" {
		t.Fatalf("device changed: %+v", d.Attrs)
	}

	// Idempotent.
	c.advance(time.Minute)
	res, links, err = devicetypes.Sync(ctx, e, ix, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("second sync links %d, want 2: device_type objects must not be matched as devices", len(links))
	}
	if res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
		t.Fatalf("second sync changed things: %+v", res)
	}
}

func TestLibraryNeverOverridesObservedValue(t *testing.T) {
	e, s, c := newEngine(t)
	ctx := context.Background()
	addDevice(t, e, c, "SN-SW", "Arista Networks", "DCS-7050SX3-48YC8-F")
	// A collector read this model's height on the equipment: 2 U (not what
	// the library says), under the same slug.
	o := sdk.NewBatch("collector:rack-scan", sdk.ScopePartial)
	o.ObservedAt = c.t
	o.Object("dt", "device_type").Identify("slug", "arista-dcs-7050sx3-48yc8-f").Set("u_height", 2.0)
	if _, err := e.Apply(ctx, o, "", ""); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	if _, _, err := devicetypes.Sync(ctx, e, defaultIndex(t), ""); err != nil {
		t.Fatal(err)
	}
	dt := deviceType(t, s, "arista-dcs-7050sx3-48yc8-f")
	if dt.Attrs["u_height"] != 2.0 {
		t.Fatalf("library overrode an observed value: %v", dt.Attrs["u_height"])
	}
	if dt.Attrs["airflow"] != "front_to_rear" {
		t.Fatalf("library should still fill what nobody observed: %+v", dt.Attrs)
	}
	// The disagreement is surfaced, not hidden.
	_, n, err := reconcile.ListDiscrepancies(ctx, s, reconcile.DiscrepancyFilter{Status: "open", Kind: "value_mismatch"})
	if err != nil || n != 1 {
		t.Fatalf("open mismatches %d %v", n, err)
	}
}

func TestObservedValueArrivingLaterWins(t *testing.T) {
	e, s, c := newEngine(t)
	ctx := context.Background()
	addDevice(t, e, c, "SN-SW", "Arista Networks", "DCS-7050SX3-48YC8-F")
	c.advance(time.Minute)
	if _, _, err := devicetypes.Sync(ctx, e, defaultIndex(t), ""); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	o := sdk.NewBatch("collector:rack-scan", sdk.ScopePartial)
	o.ObservedAt = c.t
	o.Object("dt", "device_type").Identify("slug", "arista-dcs-7050sx3-48yc8-f").Set("u_height", 2.0)
	if _, err := e.Apply(ctx, o, "", ""); err != nil {
		t.Fatal(err)
	}
	if dt := deviceType(t, s, "arista-dcs-7050sx3-48yc8-f"); dt.Attrs["u_height"] != 2.0 {
		t.Fatalf("observed value lost to the library: %v", dt.Attrs["u_height"])
	}
	// A later library run does not take it back.
	c.advance(time.Minute)
	if _, _, err := devicetypes.Sync(ctx, e, defaultIndex(t), ""); err != nil {
		t.Fatal(err)
	}
	if dt := deviceType(t, s, "arista-dcs-7050sx3-48yc8-f"); dt.Attrs["u_height"] != 2.0 {
		t.Fatalf("library took the value back: %v", dt.Attrs["u_height"])
	}
}

func TestModelChangeMovesTheLinkAndKeepsHistory(t *testing.T) {
	e, s, c := newEngine(t)
	ctx := context.Background()
	ix := defaultIndex(t)
	id := addDevice(t, e, c, "SN-1", "Dell Inc.", "PowerEdge R640")
	c.advance(time.Minute)
	if _, _, err := devicetypes.Sync(ctx, e, ix, ""); err != nil {
		t.Fatal(err)
	}
	// The collector now reports a bigger model of the same machine.
	c.advance(time.Minute)
	b := sdk.NewBatch("collector:SN-1", sdk.ScopePartial)
	b.ObservedAt = c.t
	b.Object("d", "device").Identify("serial", "SN-1").Set("model", "PowerEdge R740")
	if _, err := e.Apply(ctx, b, "", ""); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	res, _, err := devicetypes.Sync(ctx, e, ix, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.RelationsClosed != 1 || res.RelationsOpened != 1 {
		t.Fatalf("closed %d opened %d, want 1 and 1", res.RelationsClosed, res.RelationsOpened)
	}
	cur := instanceOf(t, s, id)
	if len(cur) != 1 || cur[0].ToID != deviceType(t, s, "dell-poweredge-r740").ID {
		t.Fatalf("current links %+v", cur)
	}
	hist, err := s.RelationHistory(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	for _, r := range hist {
		if r.Type == "instance_of" && r.ValidTo != nil {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("history keeps %d closed links, want 1", closed)
	}
	// The R640 model is still known: nothing is deleted.
	if deviceType(t, s, "dell-poweredge-r640").Status == "absent" {
		t.Fatal("an unused model went absent")
	}
}

func TestSyncWithoutDevicesDeclaresNothing(t *testing.T) {
	e, s, _ := newEngine(t)
	res, links, err := devicetypes.Sync(context.Background(), e, defaultIndex(t), "")
	if err != nil || len(links) != 0 || res.Created != 0 {
		t.Fatalf("%v %d %+v", err, len(links), res)
	}
	if _, n, _ := s.List(context.Background(), store.ListFilter{Type: "device_type"}); n != 0 {
		t.Fatalf("%d device types created out of thin air", n)
	}
}
