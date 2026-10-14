package inventoryio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/inventoryio"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// overlay adds a secret-looking attribute: the registry has no sensitive
// marker yet, so the export guard works on names.
const overlay = `
types:
  device:
    attributes:
      snmp_community: {kind: string, label: {en: SNMP community, fr: Communauté SNMP}, description: "Secret."}
relations:
  peers_with:
    label: {en: peers with, fr: pair de}
    inverse: {en: peers with, fr: pair de}
    description: Test relation carrying a secret attribute.
    from: [device]
    to: [device]
    attributes:
      shared_secret: {kind: string, label: {en: Secret, fr: Secret}, description: "Secret."}
      vrf: {kind: string, label: {en: VRF, fr: VRF}, description: "VRF."}
`

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newEngine(t *testing.T) (*reconcile.Engine, *clock) {
	t.Helper()
	ov, err := schema.Parse([]byte(overlay), true)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := schema.Builtin().Merge(ov)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	return &reconcile.Engine{Store: testdb.New(t), Schema: func() *schema.Registry { return reg }, Now: c.now}, c
}

func apply(t *testing.T, e *reconcile.Engine, b *sdk.Batch) {
	t.Helper()
	if _, err := e.Apply(context.Background(), b, "", ""); err != nil {
		t.Fatal(err)
	}
}

// populate builds a small inventory from three sources.
func populate(t *testing.T, e *reconcile.Engine, c *clock) {
	t.Helper()
	d := sdk.NewBatch("user:vd", sdk.ScopePartial)
	d.ObservedAt = c.t
	d.Confidence = sdk.Declared
	d.Object("site", "site").Identify("code", "LAB-1").Set("name", "Lab")
	d.Object("rack", "rack").Identify("name", "R01").In("site").Set("u_height", 42)
	apply(t, e, d)

	o := sdk.NewBatch("arista:sw-a1", sdk.ScopeFull)
	o.ObservedAt = c.t
	o.Object("sw", "device").Identify("serial", "JPE111").Identify("name", "sw-a1").Set("role", "switch").
		Set("mgmt_ip", "192.0.2.10").Set("snmp_community", "hunter2")
	o.Object("e1", "interface").Identify("name", "Ethernet1").In("sw").Set("oper_status", "up").Set("speed_mbps", 10000)
	o.RelateEndpoints("mounted_in", sdk.Ref("sw"), sdk.Endpoint{Type: "rack", Identity: map[string]string{"name": "R01"}, Parent: &sdk.Endpoint{Type: "site", Identity: map[string]string{"code": "LAB-1"}}}).With("position", 10.0).With("face", "front")
	apply(t, e, o)
}

// canon reduces a document to what a round trip must preserve: types,
// identities, attributes, parent identity and relations by identity. Refs,
// ids, sources, confidences and timestamps are deliberately dropped.
func canon(d *inventoryio.Document) []string {
	key := map[string]string{}
	for _, o := range d.Objects {
		key[o.Ref] = o.Type + "{" + fmt.Sprint(o.Identity) + "}"
	}
	var out []string
	for _, o := range d.Objects {
		attrs, _ := json.Marshal(o.Attributes)
		out = append(out, fmt.Sprintf("O %s parent=%s attrs=%s status=%s", key[o.Ref], key[o.Parent], attrs, o.Status))
	}
	for _, r := range d.Relations {
		attrs, _ := json.Marshal(r.Attributes)
		out = append(out, fmt.Sprintf("R %s %s -> %s %s", r.Type, key[r.From], key[r.To], attrs))
	}
	sort.Strings(out)
	return out
}

func export(t *testing.T, e *reconcile.Engine, opt inventoryio.ExportOptions) *inventoryio.Document {
	t.Helper()
	d, err := inventoryio.Export(context.Background(), e.Store, e.Schema(), opt)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRoundTripJSONAndYAML(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			src, c := newEngine(t)
			populate(t, src, c)
			doc := export(t, src, inventoryio.ExportOptions{})
			if len(doc.Objects) != 4 || len(doc.Relations) != 1 {
				t.Fatalf("export has %d objects, %d relations; want 4 and 1", len(doc.Objects), len(doc.Relations))
			}

			var buf bytes.Buffer
			if err := doc.Encode(&buf, format); err != nil {
				t.Fatal(err)
			}
			back, err := inventoryio.Decode(&buf, format)
			if err != nil {
				t.Fatal(err)
			}

			dst, _ := newEngine(t) // an empty database
			res, err := inventoryio.Import(context.Background(), dst, back, inventoryio.ImportOptions{Name: "rt"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Created != 4 {
				t.Fatalf("created %d, want 4 (warnings %v)", res.Created, res.Warnings)
			}
			again := export(t, dst, inventoryio.ExportOptions{})
			if got, want := canon(again), canon(doc); !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip differs\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
			}
		})
	}
}

// Import must reach the inventory through the engine: its claims carry the
// import source and the declared confidence, and nothing outranks the
// collector that observed the same value.
func TestImportGoesThroughTheEngine(t *testing.T) {
	src, c := newEngine(t)
	populate(t, src, c)
	doc := export(t, src, inventoryio.ExportOptions{})

	// Change the file's role for the switch: the arista observation must win.
	for i, o := range doc.Objects {
		if o.Type == "device" {
			doc.Objects[i].Attributes["role"] = "router"
		}
	}
	if _, err := inventoryio.Import(context.Background(), src, doc, inventoryio.ImportOptions{Name: "rt"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var n int
	if err := src.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM claims WHERE source = 'import:rt' AND confidence = 'declared'`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("no declared claim from import:rt (n=%d err=%v)", n, err)
	}
	var kind string
	if err := src.Store.Pool.QueryRow(ctx, `SELECT kind FROM changesets WHERE source = 'import:rt'`).Scan(&kind); err != nil || kind != "declaration" {
		t.Fatalf("import changeset kind %q err %v", kind, err)
	}
	after := export(t, src, inventoryio.ExportOptions{})
	for _, o := range after.Objects {
		if o.Type == "device" {
			if o.Attributes["role"] != "switch" || o.Provenance["role"].Source != "arista:sw-a1" {
				t.Fatalf("declared import overrode an observed value: %v %v", o.Attributes["role"], o.Provenance["role"])
			}
		}
	}
	// Idempotent: re-importing changes nothing.
	res, err := inventoryio.Import(context.Background(), src, doc, inventoryio.ImportOptions{Name: "rt"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.ValuesChanged != 0 {
		t.Fatalf("second import changed things: %+v", res)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e, _ := newEngine(t)
	doc := &inventoryio.Document{Format: inventoryio.FormatName, Version: 1, Objects: []inventoryio.Object{
		{Ref: "a", Type: "site", Identity: map[string]string{"code": "X"}, Attributes: map[string]any{"code": "X"}},
	}}
	res, err := inventoryio.Import(context.Background(), e, doc, inventoryio.ImportOptions{Name: "dry", DryRun: true})
	if err != nil || res.Created != 1 {
		t.Fatalf("preview: %+v %v", res, err)
	}
	var n int
	if err := e.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM objects`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dry run left %d objects (err %v)", n, err)
	}
}

func TestSecretsNeverExported(t *testing.T) {
	e, c := newEngine(t)
	populate(t, e, c)
	p := sdk.NewBatch("user:vd", sdk.ScopePartial)
	p.ObservedAt = c.t
	p.Confidence = sdk.Declared
	p.Object("a", "device").Identify("name", "sw-a1")
	p.Object("b", "device").Identify("name", "sw-a2")
	p.Relate("peers_with", "a", "b").With("shared_secret", "s3cr3t").With("vrf", "mgmt")
	apply(t, e, p)
	for _, opt := range []inventoryio.ExportOptions{{}, {History: true}} {
		doc := export(t, e, opt)
		var buf bytes.Buffer
		for _, f := range []string{"json", "yaml"} {
			buf.Reset()
			if err := doc.Encode(&buf, f); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "mgmt") {
				t.Fatalf("the non-secret relation attribute vanished:\n%s", buf.String())
			}
			if strings.Contains(buf.String(), "hunter2") || strings.Contains(buf.String(), "snmp_community") ||
				strings.Contains(buf.String(), "s3cr3t") || strings.Contains(buf.String(), "shared_secret") {
				t.Fatalf("secret leaked in %s export (history=%v):\n%s", f, opt.History, buf.String())
			}
		}
		if doc.Omitted == nil || doc.Omitted.SensitiveAttributes == 0 {
			t.Fatalf("export did not report the omitted secret: %+v", doc.Omitted)
		}
	}
}

func TestImportRejectsSecrets(t *testing.T) {
	e, _ := newEngine(t)
	doc := &inventoryio.Document{Format: inventoryio.FormatName, Version: 1, Objects: []inventoryio.Object{
		{Ref: "a", Type: "device", Identity: map[string]string{"name": "x"}, Attributes: map[string]any{"snmp_community": "hunter2"}},
	}}
	if _, err := inventoryio.Import(context.Background(), e, doc, inventoryio.ImportOptions{Name: "s"}); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("want a secret refusal, got %v", err)
	}
}

func TestStateAtADateAndHistory(t *testing.T) {
	e, c := newEngine(t)
	populate(t, e, c)
	t1 := c.t
	c.t = c.t.Add(24 * time.Hour)
	o := sdk.NewBatch("arista:sw-a1", sdk.ScopeFull)
	o.ObservedAt = c.t
	o.Object("sw", "device").Identify("serial", "JPE111").Identify("name", "sw-a1").Set("role", "switch").Set("mgmt_ip", "192.0.2.99")
	o.Object("e1", "interface").Identify("name", "Ethernet1").In("sw").Set("oper_status", "up")
	o.Object("e2", "interface").Identify("name", "Ethernet2").In("sw").Set("oper_status", "down")
	apply(t, e, o) // new IP, new interface, mounted_in withdrawn

	at := t1.Add(time.Hour)
	old := export(t, e, inventoryio.ExportOptions{At: &at})
	cur := export(t, e, inventoryio.ExportOptions{})
	ip := func(d *inventoryio.Document) any {
		for _, x := range d.Objects {
			if x.Type == "device" {
				return x.Attributes["mgmt_ip"]
			}
		}
		return nil
	}
	if ip(old) != "192.0.2.10" || ip(cur) != "192.0.2.99" {
		t.Fatalf("ip at date %v, now %v", ip(old), ip(cur))
	}
	if len(old.Objects) != 4 || len(cur.Objects) != 5 {
		t.Fatalf("objects at date %d (want 4), now %d (want 5)", len(old.Objects), len(cur.Objects))
	}
	if len(old.Relations) != 1 || len(cur.Relations) != 0 {
		t.Fatalf("relations at date %d (want 1), now %d (want 0)", len(old.Relations), len(cur.Relations))
	}
	if cur.At != nil || old.At == nil {
		t.Fatal("the at header is wrong")
	}

	hist := export(t, e, inventoryio.ExportOptions{History: true})
	var past int
	for _, r := range hist.Relations {
		if r.Until != nil {
			past++
		}
	}
	if past != 1 {
		t.Fatalf("closed relations in history export: %d, want 1", past)
	}
	for _, x := range hist.Objects {
		if x.Type == "device" {
			var ips []any
			for _, h := range x.History {
				if h.Key == "mgmt_ip" {
					ips = append(ips, h.Value)
				}
			}
			if len(ips) != 2 {
				t.Fatalf("mgmt_ip history %v, want two values", ips)
			}
		}
	}
	// The history export imports as its state: closed relations are not replayed.
	dst, _ := newEngine(t)
	if _, err := inventoryio.Import(context.Background(), dst, hist, inventoryio.ImportOptions{Name: "h"}); err != nil {
		t.Fatal(err)
	}
	if got, want := canon(export(t, dst, inventoryio.ExportOptions{})), canon(cur); !reflect.DeepEqual(got, want) {
		t.Fatalf("history import differs from the current state\n got: %v\nwant: %v", got, want)
	}

	noProv := export(t, e, inventoryio.ExportOptions{NoProvenance: true})
	for _, x := range noProv.Objects {
		if len(x.Provenance) != 0 {
			t.Fatal("provenance exported despite NoProvenance")
		}
	}
}

func TestFullScopeWithdraws(t *testing.T) {
	e, c := newEngine(t)
	mk := func(attrs map[string]any) *inventoryio.Document {
		return &inventoryio.Document{Format: inventoryio.FormatName, Version: 1, Objects: []inventoryio.Object{
			{Ref: "a", Type: "site", Identity: map[string]string{"code": "X"}, Attributes: attrs},
		}}
	}
	ctx := context.Background()
	if _, err := inventoryio.Import(ctx, e, mk(map[string]any{"code": "X", "address": "1 rue A"}), inventoryio.ImportOptions{Name: "f", Scope: sdk.ScopeFull}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
	if _, err := inventoryio.Import(ctx, e, mk(map[string]any{"code": "X"}), inventoryio.ImportOptions{Name: "f", Scope: sdk.ScopeFull}); err != nil {
		t.Fatal(err)
	}
	if doc := export(t, e, inventoryio.ExportOptions{}); doc.Objects[0].Attributes["address"] != nil {
		t.Fatalf("full-scope import kept an omitted value: %v", doc.Objects[0].Attributes)
	}
	// Nothing is deleted: the object is still there, the value is in history.
	if doc := export(t, e, inventoryio.ExportOptions{History: true}); len(doc.Objects) != 1 || len(doc.Objects[0].History) < 2 {
		t.Fatalf("history lost: %+v", doc.Objects)
	}
}

func TestToBatchRejections(t *testing.T) {
	reg := schema.Builtin()
	site := func(ref, code string) inventoryio.Object {
		return inventoryio.Object{Ref: ref, Type: "site", Identity: map[string]string{"code": code}}
	}
	cases := []struct {
		name string
		opt  inventoryio.ImportOptions
		doc  inventoryio.Document
		want string
	}{
		{"bad name", inventoryio.ImportOptions{Name: "Bad Name"}, inventoryio.Document{}, "import name"},
		{"empty ref", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{site("", "A")}}, "empty ref"},
		{"duplicate ref", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{site("a", "A"), site("a", "B")}}, "duplicate ref"},
		{"unknown type", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{{Ref: "a", Type: "nope", Identity: map[string]string{"x": "y"}}}}, "unknown object type"},
		{"no identity", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{{Ref: "a", Type: "site"}}}, "no identity"},
		{"non-identifying key", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{{Ref: "a", Type: "site", Identity: map[string]string{"address": "y"}}}}, "not an identifying"},
		{"unknown parent", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{{Ref: "a", Type: "rack", Parent: "zz", Identity: map[string]string{"name": "R"}}}}, "unknown parent"},
		{"unknown relation", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{site("a", "A")}, Relations: []inventoryio.Relation{{Type: "nope", From: "a", To: "a"}}}, "unknown relation"},
		{"dangling endpoint", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{site("a", "A")}, Relations: []inventoryio.Relation{{Type: "contains", From: "a", To: "zz"}}}, "unknown endpoint"},
		{"secret relation attribute", inventoryio.ImportOptions{Name: "x"}, inventoryio.Document{Objects: []inventoryio.Object{site("a", "A")}, Relations: []inventoryio.Relation{{Type: "contains", From: "a", To: "a", Attributes: map[string]any{"password": "x"}}}}, "secret"},
		{"bad scope", inventoryio.ImportOptions{Name: "x", Scope: "weird"}, inventoryio.Document{Objects: []inventoryio.Object{site("a", "A")}}, "scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inventoryio.ToBatch(&tc.doc, reg, tc.opt)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
	// A valid minimal document is accepted, with the declared defaults.
	b, err := inventoryio.ToBatch(&inventoryio.Document{Objects: []inventoryio.Object{site("a", "A")}}, reg, inventoryio.ImportOptions{Name: "ok"})
	if err != nil || b.Source != "import:ok" || b.Scope != sdk.ScopePartial || b.Confidence != sdk.Declared {
		t.Fatalf("batch %+v err %v", b, err)
	}
}

func TestDecodeChecksHeader(t *testing.T) {
	for name, tc := range map[string]struct{ format, in, want string }{
		"wrong format":    {"json", `{"format":"other","version":1}`, "not a trica-inventory"},
		"future version":  {"json", `{"format":"trica-inventory","version":2}`, "unsupported"},
		"unknown field":   {"json", `{"format":"trica-inventory","version":1,"objcts":[]}`, "decode json"},
		"yaml unknown":    {"yaml", "format: trica-inventory\nversion: 1\nobjcts: []\n", "decode yaml"},
		"bad format name": {"toml", ``, "unknown format"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := inventoryio.Decode(strings.NewReader(tc.in), tc.format)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	if err := (&inventoryio.Document{}).Encode(&bytes.Buffer{}, "toml"); err == nil {
		t.Fatal("encode accepted an unknown format")
	}
}

func TestStoreReadPaths(t *testing.T) {
	e, c := newEngine(t)
	populate(t, e, c)
	ctx := context.Background()
	s := e.Store
	cur, err := s.ValuesAt(ctx, nil, false)
	if err != nil || len(cur) == 0 {
		t.Fatalf("ValuesAt current: %d %v", len(cur), err)
	}
	before := c.t.Add(-time.Hour)
	none, err := s.ValuesAt(ctx, &before, false)
	if err != nil || len(none) != 0 {
		t.Fatalf("ValuesAt before the first write: %d %v", len(none), err)
	}
	rels, err := s.RelationsAt(ctx, &before, false)
	if err != nil || len(rels) != 0 {
		t.Fatalf("RelationsAt before the first write: %d %v", len(rels), err)
	}
}
