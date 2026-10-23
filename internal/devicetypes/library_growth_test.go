package devicetypes_test

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
		// A "+" is the word plus (#159): DS218 and DS218+ are two models.
		{"Synology", "DS218", "", "synology-ds218", devicetypes.ByModel, true},
		{"Synology", "DS218+", "", "synology-ds218-plus", devicetypes.ByModel, true},
		{"Synology", "DS218 Plus", "", "synology-ds218-plus", devicetypes.ByModel, true},
		{"Synology Inc.", "RS815", "", "synology-rs815", devicetypes.ByModel, false},
		{"Synology", "RS815+", "", "synology-rs815-plus", devicetypes.ByModel, false},
		{"Synology", "RS1221 Plus", "", "synology-rs1221-plus", devicetypes.ByModel, false},
		{"Synology", "DS1821 Plus", "", "synology-ds1821-plus", devicetypes.ByModel, true},
		{"Synology", "", "DS218+", "synology-ds218-plus", devicetypes.ByPartNumber, true},
		{"Ubiquiti", "U6 Plus", "", "ubiquiti-u6-plus", devicetypes.ByModel, true},
		{"HPE", "Aruba 2930M-24G-PoE Plus", "", "hpe-aruba-2930m-24g-poep", devicetypes.ByModel, false},
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

// A name that drops the "+" its model has is another name (#159): DS920 is
// not the DS920+.
func TestANameWithoutItsPlusMatchesNothing(t *testing.T) {
	ix := defaultIndex(t)
	for _, c := range [][2]string{{"Synology", "DS920"}, {"Synology", "RS1221"}, {"HPE", "Aruba 2930M-24G-PoE"}, {"MikroTik", "CRS326-24G-2S"}} {
		if m := ix.Find(c[0], c[1], ""); m != nil {
			t.Errorf("%s %q matched %s", c[0], c[1], m.Model.Slug)
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
	const acme = "manufacturer: Acme\nmodel: Box 1\nslug: acme-box-1\nu_height: 1\ninterfaces:\n  - name: e1\n    type: 1000base-t\n    label: First\n  - name: e2\npower-ports:\n  - name: e1\n"
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
		"unknown slug":                   {"manufacturer: Acme\nmodels:\n  - slug: acme-nope\n    part_numbers: [X1]\n", "no model has the slug"},
		"other manufacturer":             {"manufacturer: Acme\nmodels:\n  - slug: other-thing\n    part_numbers: [X1]\n", "Other model"},
		"project model":                  {"manufacturer: Acme\nmodels:\n  - slug: acme-box-2\n    part_numbers: [X1]\n", "not an upstream model"},
		"no part number":                 {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: []\n", "lists no part number"},
		"empty part number":              {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [X1, ' ']\n", "empty part number"},
		"duplicate once plus is spelled": {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [B1+, B1 Plus]\n", "already listed"},
		"duplicate once normal":          {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [UDM-SE, UDMSE]\n", "already listed"},
		"listed by another model":        {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [b3 held]\n", "already listed by acme-box-3"},
		"unknown key":                    {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    part_numbers: [X1]\n    note: hi\n", "field note"},
		"not yaml":                       {"manufacturer: [unclosed\n", "overlays/Acme.yaml"},
		"unknown interface":              {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e9: x}\n", `no interface is named "e9"`},
		"power port is not an interface": {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {PSU: x}\n", "no interface is named"},
		"rename onto a kept name":        {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e1: e2}\n", `interface "e2" would be named twice`},
		"rename to nothing":              {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e1: ' '}\n", "to nothing"},
		"rename to itself":               {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e1: e1}\n", "to itself"},
		"two ends renamed alike":         {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e1: z, e2: z}\n", `would be named twice`},
		"duplicate key":                  {"manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e1: a, e1: b}\n", "already defined"},
		"rename on a project model":      {"manufacturer: Acme\nmodels:\n  - slug: acme-box-2\n    interface_names: {e1: a}\n", "not an upstream model"},
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

// An overlay renames interface templates (ADR 0056, #173): at once, so that a
// swap works, only interfaces, and an entry may do nothing else.
func TestOverlayRenamesInterfaces(t *testing.T) {
	const acme = "manufacturer: Acme\nmodel: Box 1\nslug: acme-box-1\nu_height: 1\ninterfaces:\n  - name: e1\n    type: 1000base-t\n    label: First\n  - name: e2\n    mgmt_only: true\npower-ports:\n  - name: e1\n"
	load := func(overlay string) *devicetypes.Model {
		t.Helper()
		models, err := devicetypes.Load(fstest.MapFS{
			"lib/Acme/box1.yaml":     file([]byte(acme)),
			"lib/overlays/Acme.yaml": file([]byte(overlay)),
		}, "lib")
		if err != nil {
			t.Fatal(err)
		}
		return models[0]
	}
	names := func(m *devicetypes.Model, group string) string {
		var out []string
		for _, p := range m.Ports {
			if p.Group == group {
				out = append(out, p.Name)
			}
		}
		return strings.Join(out, ",")
	}
	m := load("manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e1: e2, e2: e1}\n")
	if got := names(m, "interface"); got != "e2,e1" {
		t.Fatalf("interfaces %q after a swap, want e2,e1", got)
	}
	if p := m.Ports[0]; p.Type != "1000base-t" || p.Label != "First" {
		t.Fatalf("type and label are kept: %+v", p)
	}
	if p := m.Ports[1]; !p.MgmtOnly {
		t.Fatalf("mgmt_only is kept: %+v", p)
	}
	if got := names(m, "power"); got != "e1" {
		t.Fatalf("the power port %q must not be renamed", got)
	}
	m = load("manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {e1: ' Port 1 '}\n")
	if got := names(m, "interface"); got != "Port 1,e2" {
		t.Fatalf("interfaces %q, want a trimmed Port 1,e2", got)
	}
}

// The unlabelled interface that an overlay renames keeps its old name as its
// label; a label upstream wrote stays (#177).
func TestOverlayRenamedInterfaceKeepsItsOldNameAsLabel(t *testing.T) {
	const acme = "manufacturer: Acme\nmodel: Box 1\nslug: acme-box-1\nu_height: 1\ninterfaces:\n  - name: Port 1 (PoE+)\n    type: 1000base-t\n  - name: e2\n    type: 1000base-t\n    label: Second\n"
	models, err := devicetypes.Load(fstest.MapFS{
		"lib/Acme/box1.yaml":     file([]byte(acme)),
		"lib/overlays/Acme.yaml": file([]byte("manufacturer: Acme\nmodels:\n  - slug: acme-box-1\n    interface_names: {Port 1 (PoE+): Port 1, e2: Port 2}\n")),
	}, "lib")
	if err != nil {
		t.Fatal(err)
	}
	p := models[0].Ports
	if p[0].Name != "Port 1" || p[0].Label != "Port 1 (PoE+)" {
		t.Errorf("unlabelled interface: %+v, want Port 1 labelled with its old name", p[0])
	}
	if p[1].Name != "Port 2" || p[1].Label != "Second" {
		t.Errorf("labelled interface: %+v, want Port 2 keeping its label", p[1])
	}
}

// blockedPortNames lists the UniFi models of the library whose upstream
// interface names do not number every wired interface exactly once from 1 to N.
// They stay as upstream names them: renaming them would be a guess about the
// "idx" the Integration API reports (#177). Each entry says what is missing.
var blockedPortNames = map[string]string{}

func init() {
	const (
		gateway = "role names or Linux names (WAN, LAN1, eth0), no printed numbering: needs a recorded device detail and which physical port was plugged in"
		ap      = "single-port access point named eth0 / lan0 / Ethernet: needs one recorded device detail"
		repeat  = "numbering restarts after the RJ45 ports (SFP 1, SFP+ 1, GbE 1): needs the idx the console gives the SFP ports"
		other   = "unnumbered or role names; not known to be listed with ports by the Integration API"
	)
	for _, s := range []string{
		"ubiquiti-unifi-cloud-gateway-max", "ubiquiti-unifi-cloud-gateway-fiber", "ubiquiti-unifi-gateway-lite",
		"ubiquiti-unifi-gateway-max", "ubiquiti-unifi-gateway-pro", "ubiquiti-unifi-express-7", "ubiquiti-unifi-express-ux",
		"ubiquiti-usg", "ubiquiti-unifi-security-gateway-pro",
	} {
		blockedPortNames[s] = gateway
	}
	for _, s := range []string{
		"ubiquiti-u6-plus", "ubiquiti-u6-enterprise", "ubiquiti-u6-lr", "ubiquiti-u6-lite", "ubiquiti-u6-mesh",
		"ubiquiti-u7-long-range", "ubiquiti-u7-pro-xgs", "ubiquiti-uap-ac-lr", "ubiquiti-uap-ac-lite", "ubiquiti-uap-nanohd",
	} {
		blockedPortNames[s] = ap
	}
	blockedPortNames["ubiquiti-u6-iw"] = "eth0 uplink plus eth1..4 labelled 1..4: needs the idx of the uplink"
	blockedPortNames["ubiquiti-uap-ac-pro"] = "eth0 and eth1: needs the idx of the uplink"
	for _, s := range []string{
		"ubiquiti-unifi-pro-max-16", "ubiquiti-unifi-pro-max-16-poe", "ubiquiti-unifi-switch-pro-xg-8-poe",
		"ubiquiti-unifi-switch-24-gen1", "ubiquiti-unifi-switch-48-gen1", "ubiquiti-unifi-switch-24-poe-250w-gen1",
		"ubiquiti-unifi-switch-48-poe-500w-gen1", "ubiquiti-unifi-switch-24-gen2", "ubiquiti-unifi-switch-24-poe-gen2",
		"ubiquiti-unifi-switch-16-poe-gen2",
	} {
		blockedPortNames[s] = repeat
	}
	blockedPortNames["ubiquiti-unifi-wan-switch-rj45"] = "MGMT is unnumbered and Port1..3 follow: needs whether the console lists MGMT and at which idx"
	blockedPortNames["ubiquiti-usp-pdu-pro"] = "named Port 21..24, not 1..N: whether the console uses those numbers is unknown"
	blockedPortNames["ubiquiti-es-48-lite"] = "an EdgeSwitch (0/1..), not a UniFi Network device"
	for _, s := range []string{
		"ubiquiti-usp-pdu-hd", "ubiquiti-ups-2u-eu", "ubiquiti-ups-2u-us", "ubiquiti-unifi-protect-network-video-recorder",
		"ubiquiti-unifi-protect-network-video-recorder-pro", "ubiquiti-unifi-unvr-g2-pro", "ubiquiti-enterprise-network-video-recorder",
		"ubiquiti-unas-pro", "ubiquiti-cloud-key-gen2", "ubiquiti-cloud-key-gen2-plus", "ubiquiti-ckg2-rm",
		"ubiquiti-unifi-application-server",
	} {
		blockedPortNames[s] = other
	}
}

// The UniFi Network collector names a port "Port <idx>" (ADR 0060). A model's
// templates are named so when, and only when, upstream's own names or labels
// number every wired interface exactly once from 1 to N: the rule of #177,
// applied to the raw upstream files so that it sees the names before the
// overlay. It does not prove that idx is the printed number (README point 9
// of internal/collect/unifi/testdata).
func TestUniFiPortTemplatesAreNamedAsTheConsoleNamesThem(t *testing.T) {
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*devicetypes.Model{}
	for _, m := range models {
		by[m.Slug] = m
	}
	type iface struct {
		Name  string `yaml:"name"`
		Type  string `yaml:"type"`
		Label string `yaml:"label"`
	}
	wired := func(in []iface) []iface {
		var out []iface
		for _, i := range in {
			if !strings.HasPrefix(i.Type, "ieee802.11") && i.Type != "other-wireless" {
				out = append(out, i)
			}
		}
		return out
	}
	labelled := regexp.MustCompile(`^Port\s*(\d+)\b`)
	named := regexp.MustCompile(`^[Pp]ort ?\.?(\d+)(?: \(PoE[^)]*\)| - SFP\+)?$`)
	prefixed := regexp.MustCompile(`^(?:SFP\+|SFP28|SFP|QSFP28|GbE|2\.5 GbE|2\.5G Port|10G Port|SFP28 Port|QSFP28 Port) (\d+)$`)
	number := func(i iface) int {
		for _, re := range []struct {
			re  *regexp.Regexp
			src string
		}{{labelled, i.Label}, {named, i.Name}, {prefixed, i.Name}} {
			if m := re.re.FindStringSubmatch(re.src); m != nil {
				n, _ := strconv.Atoi(m[1])
				return n
			}
		}
		return 0
	}
	var files []string
	for _, ext := range []string{"yaml", "yml"} {
		f, err := filepath.Glob("library/Ubiquiti/*." + ext)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f...)
	}
	if len(files) < 90 {
		t.Fatalf("only %d upstream Ubiquiti files", len(files))
	}
	renamed, blocked, seen := 0, 0, map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var up struct {
			Slug       string  `yaml:"slug"`
			Interfaces []iface `yaml:"interfaces"`
		}
		if err := yaml.Unmarshal(data, &up); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		m := by[up.Slug]
		if m == nil {
			t.Errorf("%s: no loaded model %q", f, up.Slug)
			continue
		}
		seen[up.Slug] = true
		wi := wired(up.Interfaces)
		var got []devicetypes.Port
		for _, p := range m.Ports {
			if p.Group == "interface" && !strings.HasPrefix(p.Type, "ieee802.11") && p.Type != "other-wireless" {
				got = append(got, p)
			}
		}
		complete := len(wi) > 0
		for k, i := range wi {
			if number(i) != k+1 {
				complete = false
			}
		}
		reason, isBlocked := blockedPortNames[up.Slug]
		if !complete {
			blocked++
			if !isBlocked {
				t.Errorf("%s: upstream does not number its interfaces 1..%d and the model is not in blockedPortNames", up.Slug, len(wi))
			}
			for k, i := range wi {
				if k >= len(got) || got[k].Name != i.Name {
					t.Errorf("%s: interface %d is renamed without evidence (upstream %q)", up.Slug, k+1, i.Name)
				}
			}
			continue
		}
		if isBlocked {
			t.Errorf("%s: numbered 1..%d by upstream but still listed as blocked (%s)", up.Slug, len(wi), reason)
		}
		if len(got) != len(wi) {
			t.Errorf("%s: %d interfaces, upstream has %d", up.Slug, len(got), len(wi))
			continue
		}
		changed := false
		for k, i := range wi {
			want := fmt.Sprintf("Port %d", k+1)
			if got[k].Name != want {
				t.Errorf("%s: interface %d is %q, want %q (upstream %q, label %q)", up.Slug, k+1, got[k].Name, want, i.Name, i.Label)
			}
			if i.Name != want {
				changed = true
				if wantLabel := cmp.Or(i.Label, i.Name); got[k].Label != wantLabel {
					t.Errorf("%s: %s has label %q, want %q", up.Slug, want, got[k].Label, wantLabel)
				}
			}
		}
		if changed {
			renamed++
		}
	}
	for slug, reason := range blockedPortNames {
		if !seen[slug] {
			t.Errorf("blockedPortNames lists %s (%s), which is not an upstream Ubiquiti model", slug, reason)
		}
	}
	if renamed != 40 || blocked != 46 || len(blockedPortNames) != 46 {
		t.Errorf("%d models renamed, %d blocked (%d in the table), want 40 and 46: a model moved, say why in #177", renamed, blocked, len(blockedPortNames))
	}
}

// Name matching is by the model's own name: a library where two models have
// the same normalised name (DS218 and DS218+, which lost their "+" before #159), or a
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
