package devicetypes_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// What a collector reports for the equipment ADR 0056 added (a UniFi site, a
// Synology NAS, an HPE server and an Aruba switch) finds its model. Each row
// is what the device says (vendor, model, part number), the model it must
// get and how.
func TestCollectorNamesMatchTheLibrary(t *testing.T) {
	ix := defaultIndex(t)
	cases := []struct {
		vendor, model, pn string
		slug, how         string
		zeroU             bool
	}{
		// UniFi: the display name and the shortname the controller reports.
		{"Ubiquiti", "UDM Pro Max", "UDMPROMAX", "ubiquiti-unifi-dream-machine-pro-max", devicetypes.BySKU, false},
		{"Ubiquiti Inc.", "U7 Pro", "U7PRO", "ubiquiti-u7-pro", devicetypes.BySKU, true},
		{"Ubiquiti Networks", "USW Pro Max 24 PoE", "USPM24P", "ubiquiti-unifi-switch-pro-max-24-poe", devicetypes.BySKU, false},
		{"UBNT", "UCG Ultra", "UDRULT", "ubiquiti-unifi-cloud-gateway-ultra", devicetypes.BySKU, true},
		{"Ubiquiti", "UCG Max", "UCGMAX", "ubiquiti-unifi-cloud-gateway-max", devicetypes.BySKU, true},
		{"Ubiquiti", "UDM SE", "UDMPROSE", "ubiquiti-unifi-dream-machine-pro-special-edition", devicetypes.BySKU, false},
		{"Ubiquiti", "UDM Pro", "UDMPRO", "ubiquiti-unifi-dream-machine-pro", devicetypes.BySKU, false},
		{"Ubiquiti", "UDM", "UDM", "ubiquiti-udm", devicetypes.BySKU, true},
		{"Ubiquiti", "UXG Lite", "UXG", "ubiquiti-unifi-gateway-lite", devicetypes.BySKU, true},
		{"Ubiquiti", "UXG Max", "UXGB", "ubiquiti-unifi-gateway-max", devicetypes.BySKU, true},
		{"Ubiquiti", "UXG Pro", "UXGPRO", "ubiquiti-unifi-gateway-pro", devicetypes.BySKU, false},
		{"Ubiquiti", "EFG", "EFG", "ubiquiti-enterprise-fortress-gateway", devicetypes.BySKU, false},
		{"Ubiquiti", "UDR7", "UDR7", "ubiquiti-unifi-dream-router-7", devicetypes.BySKU, true},
		// The display name alone is enough when it is upstream's name, a
		// part number or a slug.
		{"Ubiquiti", "UDM Pro Max", "", "ubiquiti-unifi-dream-machine-pro-max", devicetypes.ByModel, false},
		{"Ubiquiti", "U7 Pro", "", "ubiquiti-u7-pro", devicetypes.ByModel, true},
		{"Ubiquiti", "USW Pro Max 24 PoE", "", "ubiquiti-unifi-switch-pro-max-24-poe", devicetypes.ByModel, false},
		// UniFi Access: not upstream, written by trica, SKU first.
		{"Ubiquiti", "UA Hub Door", "UAH-DOOR", "ubiquiti-ua-hub-door", devicetypes.BySKU, true},
		{"Ubiquiti", "UA Hub Door", "", "ubiquiti-ua-hub-door", devicetypes.ByModel, true},
		{"Ubiquiti", "UA G2 Pro", "", "ubiquiti-ua-g2-pro", devicetypes.ByModel, true},
		// Synology.
		{"Synology", "DS1821+", "", "synology-ds1821-plus", devicetypes.ByModel, true},
		{"Synology Inc.", "DS3617xs", "", "synology-ds3617xs", devicetypes.ByModel, true},
		{"Synology Inc.", "RS3617RPxs", "", "synology-rs3617rpxs", devicetypes.ByModel, false},
		// HPE and Aruba.
		{"HPE", "ProLiant DL380 Gen11", "", "hpe-proliant-dl380-gen11", devicetypes.ByModel, false},
		{"Hewlett Packard Enterprise", "Aruba 6300M-48G-4SFP56", "", "hpe-aruba-6300m-48g-4sfp56", devicetypes.ByModel, false},
		{"HPE", "ProLiant DL385 Gen10", "", "hpe-proliant-dl385-gen10", devicetypes.ByModel, false},
	}
	for _, c := range cases {
		m := ix.Find(c.vendor, c.model, c.pn)
		if m == nil {
			t.Errorf("%s %q %q: no model, want %s", c.vendor, c.model, c.pn, c.slug)
			continue
		}
		if m.Model.Slug != c.slug || m.How != c.how {
			t.Errorf("%s %q %q: %s by %s, want %s by %s", c.vendor, c.model, c.pn, m.Model.Slug, m.How, c.slug, c.how)
		}
		if got := m.Model.UHeight == 0; got != c.zeroU {
			t.Errorf("%s: u_height %v, want zero %v", c.slug, m.Model.UHeight, c.zeroU)
		}
		if m.Model.Racked() == c.zeroU {
			t.Errorf("%s: Racked() %v with u_height %v", c.slug, m.Model.Racked(), m.Model.UHeight)
		}
	}
}

// The pairs recorded on a real UniFi site, and a few display names that do
// not match by name at all, resolve through the shortnames of the overlay
// (ADR 0056): by SKU, and with certainty. Without the overlay, or without the
// part_numbers of the UniFi Access files, they come back by name or not at all.
func TestUniFiRecordedPairsResolve(t *testing.T) {
	ix := defaultIndex(t)
	cases := []struct{ model, shortname, slug string }{
		{"UDM Pro Max", "UDMPROMAX", "ubiquiti-unifi-dream-machine-pro-max"},
		{"U7 Pro", "U7PRO", "ubiquiti-u7-pro"},
		{"USW Pro Max 24 PoE", "USPM24P", "ubiquiti-unifi-switch-pro-max-24-poe"},
		{"UA Hub Door", "UAH-DOOR", "ubiquiti-ua-hub-door"},
		{"UA G2 Pro", "UA-G2-PRO", "ubiquiti-ua-g2-pro"},
		// Display names no upstream name or part number says.
		{"Express 7", "UX7", "ubiquiti-unifi-express-7"},
		{"AC Pro", "U7PG2", "ubiquiti-uap-ac-pro"},
		{"UCG Ultra", "UDRULT", "ubiquiti-unifi-cloud-gateway-ultra"},
		{"UDM SE", "UDMPROSE", "ubiquiti-unifi-dream-machine-pro-special-edition"},
		{"UNVR Gen 2 Pro", "UNVRAI8", "ubiquiti-unifi-unvr-g2-pro"},
		{"UCK G2", "UCKG2", "ubiquiti-cloud-key-gen2"},
		{"Nano HD", "U7NHD", "ubiquiti-uap-nanohd"},
	}
	for _, c := range cases {
		m := ix.Find("Ubiquiti", c.model, c.shortname)
		if m == nil || m.Model.Slug != c.slug || m.How != devicetypes.BySKU {
			t.Errorf("%q %q: %+v, want %s by sku", c.model, c.shortname, m, c.slug)
			continue
		}
		if ch := ix.Choices("Ubiquiti", c.model, c.shortname); !ch.Certain || ch.Reason != devicetypes.BySKU {
			t.Errorf("%q %q: certain %v reason %q, want certain by sku", c.model, c.shortname, ch.Certain, ch.Reason)
		}
	}
}

// Every shortname of the overlay file finds its model by SKU, whichever name
// the device reports: the file is what the collectors will rely on.
func TestOverlayShortnamesResolve(t *testing.T) {
	data, err := os.ReadFile("library/" + devicetypes.OverlayDir + "/Ubiquiti.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		Manufacturer string `yaml:"manufacturer"`
		Models       []struct {
			Slug        string   `yaml:"slug"`
			PartNumbers []string `yaml:"part_numbers"`
		} `yaml:"models"`
	}
	if err := yaml.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	ix := defaultIndex(t)
	shortnames := 0
	for _, e := range o.Models {
		for _, sn := range e.PartNumbers {
			shortnames++
			m := ix.Find(o.Manufacturer, "", sn)
			if m == nil || m.Model.Slug != e.Slug || m.How != devicetypes.BySKU {
				t.Errorf("%s: shortname %s gives %+v, want %s by sku", e.Slug, sn, m, e.Slug)
			}
			// A model name the device repeats does not change it.
			if m := ix.Find(o.Manufacturer, ix.BySlug(e.Slug).Model, sn); m == nil || m.Model.Slug != e.Slug {
				t.Errorf("%s: shortname %s with the upstream name gives %+v", e.Slug, sn, m)
			}
		}
	}
	if len(o.Models) < 90 || shortnames < 120 {
		t.Errorf("overlay lists %d models and %d shortnames, want the UniFi catalogue's (at least 90 and 120)", len(o.Models), shortnames)
	}
}

func TestOverlaysThatFailTheLoad(t *testing.T) {
	const acme = "manufacturer: Acme\nmodel: Box 1\nslug: acme-box-1\nu_height: 1\n"
	const two = "manufacturer: Acme\nmodel: Box 2\nslug: acme-box-2\nu_height: 1\npart_numbers: [B2-LOCAL]\n"
	const other = "manufacturer: Other\nmodel: Thing\nslug: other-thing\nu_height: 1\n"
	lib := func(overlay string) fstest.MapFS {
		return fstest.MapFS{
			"lib/Acme/box1.yaml":          file([]byte(acme)),
			"lib/Acme/box3.yaml":          file([]byte("manufacturer: Acme\nmodel: Box 3\nslug: acme-box-3\nu_height: 1\npart_numbers: [B3-HELD]\n")),
			"lib/Other/thing.yaml":        file([]byte(other)),
			"lib/trica/Acme-Box-2.yaml":   file([]byte(two)),
			"lib/overlays/Acme.yaml":      file([]byte(overlay)),
			"lib/overlays/notes.txt":      file([]byte("not an overlay")),
			"lib/overlays/.hidden.yaml":   file([]byte("garbage: [")),
			"lib/overlays/sub/inner.yaml": file([]byte("garbage: [")),
		}
	}
	bad := map[string]struct{ overlay, want string }{
		"unknown slug":            {"manufacturer: Acme\nmodels:\n  - slug: acme-nope\n    part_numbers: [X1]\n", "no model has the slug"},
		"other manufacturer":      {"manufacturer: Acme\nmodels:\n  - slug: other-thing\n    part_numbers: [X1]\n", "Other model"},
		"project model":           {"manufacturer: Acme\nmodels:\n  - slug: acme-box-2\n    part_numbers: [X1]\n", "not an upstream model"},
		"no part number":          {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: []\n", "lists no part number"},
		"empty part number":       {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [X1, ' ']\n", "empty part number"},
		"duplicate once normal":   {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [UDM-SE, UDMSE]\n", "already listed"},
		"listed by another model": {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [b3 held]\n", "already listed by acme-box-3"},
		"unknown key":             {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [X1]\n    note: hi\n", "field note"},
		"not yaml":                {"manufacturer: [unclosed\n", "overlays/Acme.yaml"},
	}
	for name, c := range bad {
		_, err := devicetypes.Load(lib(c.overlay), "lib")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one saying %q", name, err, c.want)
		}
	}
	// A valid overlay is not read as a model, and appends its part numbers.
	models, err := devicetypes.Load(lib("manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [X1, 'X-2']\n  - slug: acme-box-3\n    part_numbers: [X3]\n"), "lib")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 4 {
		t.Fatalf("%d models, want the 4 model files (the overlay is not one)", len(models))
	}
	got := map[string][]string{}
	for _, m := range models {
		got[m.Slug] = m.PartNumbers
	}
	if !slices.Equal(got["acme-box-1"], []string{"X1", "X-2"}) || !slices.Equal(got["acme-box-3"], []string{"B3-HELD", "X3"}) || !slices.Equal(got["acme-box-2"], []string{"B2-LOCAL"}) {
		t.Fatalf("part numbers %v", got)
	}
	if m := devicetypes.NewIndex(models).Find("Acme", "", "x2"); m == nil || m.Model.Slug != "acme-box-1" || m.How != devicetypes.BySKU {
		t.Fatalf("an overlay shortname: %+v, want acme-box-1 by sku", m)
	}
	// No overlays directory: nothing to apply.
	if _, err := devicetypes.Load(fstest.MapFS{"lib/Acme/box1.yaml": file([]byte(acme))}, "lib"); err != nil {
		t.Fatal(err)
	}
}

// Name matching is by the model's own name: a library where two models have
// the same normalised name (DS218 and DS218+, which lose their "+"), or a
// model whose name is another model's, would match neither. Every model of
// the library is found by its own name.
func TestEveryModelIsFoundByItsOwnName(t *testing.T) {
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	ix := devicetypes.NewIndex(models)
	for _, m := range models {
		got := ix.Find(m.Vendor, m.Model, "")
		if got == nil || got.Model.Slug != m.Slug {
			t.Errorf("%s (%s %q) is found as %+v", m.Slug, m.Vendor, m.Model, got)
		}
	}
}

// The site of the UniFi controller and the Synology NAS the story was
// written for: a collector reports the display name and the shortname, and
// Sync (the engine, on PostgreSQL) links each device to its model, one
// device_type per model, the 0 U ones included, and a second pass changes
// nothing. Needs PostgreSQL (TRICA_TEST_DATABASE_URL), skipped without.
func TestSyncLinksTheUniFiSiteAndTheNAS(t *testing.T) {
	e, s, c := newEngine(t)
	ctx := context.Background()
	devices := []struct{ serial, vendor, model, pn, slug string }{
		{"UDM-1", "Ubiquiti", "UDM Pro Max", "UDMPROMAX", "ubiquiti-unifi-dream-machine-pro-max"},
		{"AP-1", "Ubiquiti Inc.", "U7 Pro", "U7PRO", "ubiquiti-u7-pro"},
		{"AP-2", "Ubiquiti Inc.", "U7 Pro", "U7PRO", "ubiquiti-u7-pro"},
		{"SW-1", "Ubiquiti Networks", "USW Pro Max 24 PoE", "USPM24P", "ubiquiti-unifi-switch-pro-max-24-poe"},
		{"DOOR-1", "Ubiquiti", "UA Hub Door", "UAH-DOOR", "ubiquiti-ua-hub-door"},
		{"NAS-1", "Synology", "DS1821+", "", "synology-ds1821-plus"},
		{"NAS-2", "Synology Inc.", "DS3617xs", "", "synology-ds3617xs"},
	}
	ids := map[string]string{}
	for _, d := range devices {
		b := sdk.NewBatch("collector:"+d.serial, sdk.ScopePartial)
		b.ObservedAt = c.t
		o := b.Object("d", "device").Identify("serial", d.serial).Set("vendor", d.vendor).Set("model", d.model).Set("role", "network")
		if d.pn != "" {
			o.Set("part_number", d.pn)
		}
		r, err := e.Apply(ctx, b, "", "")
		if err != nil {
			t.Fatal(err)
		}
		ids[d.serial] = r.Objects["d"]
	}
	c.advance(time.Minute)
	res, links, err := devicetypes.Sync(ctx, e, defaultIndex(t), "tester")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != len(devices) || res.RelationsOpened != len(devices) {
		t.Fatalf("links %d, relations opened %d, want %d", len(links), res.RelationsOpened, len(devices))
	}
	models := map[string]bool{}
	for _, d := range devices {
		models[d.slug] = true
		dt := deviceType(t, s, d.slug)
		if rels := instanceOf(t, s, ids[d.serial]); len(rels) != 1 || rels[0].ToID != dt.ID {
			t.Errorf("%s (%s %q): relations %+v, want one to %s", d.serial, d.vendor, d.model, rels, d.slug)
		}
	}
	// One device_type per distinct model (the two U7 Pro share theirs).
	if all, _, err := s.List(ctx, store.ListFilter{Type: "device_type"}); err != nil || len(all) != len(models) {
		t.Fatalf("%d device_type objects (%v), want %d", len(all), err, len(models))
	}
	for _, slug := range []string{"ubiquiti-u7-pro", "ubiquiti-ua-hub-door", "synology-ds1821-plus", "synology-ds3617xs"} {
		if h := deviceType(t, s, slug).Attrs["u_height"]; h != 0.0 {
			t.Errorf("%s: u_height %v, want 0 (occupies no rack unit)", slug, h)
		}
	}
	if a := deviceType(t, s, "ubiquiti-u7-pro").Attrs["airflow"]; a != "passive" {
		t.Errorf("u7 pro airflow %v, want passive", a)
	}
	if h := deviceType(t, s, "ubiquiti-unifi-dream-machine-pro-max").Attrs["u_height"]; h != 1.0 {
		t.Errorf("udm pro max u_height %v, want 1", h)
	}
	// Idempotent.
	c.advance(time.Minute)
	res, links, err = devicetypes.Sync(ctx, e, defaultIndex(t), "tester")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != len(devices) || res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
		t.Fatalf("second sync: %d links, %+v, want the same links and no change", len(links), res)
	}
}

// Longer-named siblings do not make a name match uncertain: only a model that
// a variant_of names has chassis variants (ADR 0056, amending ADR 0048).
func TestNamePrefixSiblingsDoNotMakeAMatchUncertain(t *testing.T) {
	box := &devicetypes.Model{Slug: "acme-box-1", Vendor: "Acme", Model: "Box 1", UHeight: 1}
	plus := &devicetypes.Model{Slug: "acme-box-1-plus", Vendor: "Acme", Model: "Box 1 Plus", UHeight: 1}
	ix := devicetypes.NewIndex([]*devicetypes.Model{box, plus})
	ch := ix.Choices("Acme", "Box 1", "")
	if !ch.Certain || ch.Reason != devicetypes.ByModel || ch.Match.Model != box {
		t.Fatalf("Box 1: %+v, want certain by model", ch)
	}
	if got := slugs(ch.Candidates); !slices.Equal(got, []string{"acme-box-1", "acme-box-1-plus"}) {
		t.Fatalf("candidates %v, want both (the person may still change the model)", got)
	}
	// With a variant_of naming it, the same name is uncertain.
	plus.VariantOf = "acme-box-1"
	ix = devicetypes.NewIndex([]*devicetypes.Model{box, plus})
	if ch := ix.Choices("Acme", "Box 1", ""); ch.Certain || ch.Reason != devicetypes.UncertainBase {
		t.Fatalf("with a variant_of: %+v, want uncertain (base)", ch)
	}
}
