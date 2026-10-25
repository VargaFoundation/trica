package devicetypes_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
		// The display name alone is enough when it is the model's own name, a
		// part number or a slug.
		{"Ubiquiti", "UDM Pro Max", "", "ubiquiti-unifi-dream-machine-pro-max", devicetypes.ByModel, false},
		{"Ubiquiti", "U7 Pro", "", "ubiquiti-u7-pro", devicetypes.ByModel, true},
		{"Ubiquiti", "USW Pro Max 24 PoE", "", "ubiquiti-unifi-switch-pro-max-24-poe", devicetypes.ByModel, false},
		// UniFi Access: written by trica, SKU first.
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
		// Display names no model name or part number says.
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

// Every part_numbers entry of a UniFi model finds that model by SKU, whichever
// name the device reports: the files are what the collectors rely on. A part
// number two models share is ambiguous and matches neither, which fails here.
func TestEveryPartNumberFindsItsModel(t *testing.T) {
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	ix := devicetypes.NewIndex(models)
	withSKUs, shortnames := 0, 0
	for _, m := range models {
		if m.Vendor != "Ubiquiti" || len(m.PartNumbers) == 0 {
			continue
		}
		withSKUs++
		for _, sn := range m.PartNumbers {
			shortnames++
			got := ix.Find(m.Vendor, "", sn)
			if got == nil || got.Model.Slug != m.Slug || got.How != devicetypes.BySKU {
				t.Errorf("%s: shortname %s gives %+v, want %s by sku", m.Slug, sn, got, m.Slug)
			}
			// A model name the device repeats does not change it.
			if got := ix.Find(m.Vendor, m.Model, sn); got == nil || got.Model.Slug != m.Slug || got.How != devicetypes.BySKU {
				t.Errorf("%s: shortname %s with the model name gives %+v", m.Slug, sn, got)
			}
		}
	}
	if withSKUs < 90 || shortnames < 120 {
		t.Errorf("%d Ubiquiti models list %d shortnames, want the UniFi catalogue's (at least 90 and 120)", withSKUs, shortnames)
	}
}

// blockedPortNames lists the UniFi models of the library whose interface names
// do not number every wired interface exactly once from 1 to N.
// They keep the names their files give: renaming them would be a guess about the
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
// templates are named so when, and only when, its own names or labels number
// every wired interface exactly once from 1 to N: the rule of #177, applied to
// the model files themselves (ADR 0063), where the label of a renamed
// interface keeps the name it had before and is the evidence. It does not
// prove that idx is the printed number (README point 9 of
// internal/collect/unifi/testdata).
func TestUniFiPortTemplatesAreNamedAsTheConsoleNamesThem(t *testing.T) {
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	labelled := regexp.MustCompile(`^Port\s*(\d+)\b`)
	named := regexp.MustCompile(`^[Pp]ort ?\.?(\d+)(?: \(PoE[^)]*\)| - SFP\+)?$`)
	prefixed := regexp.MustCompile(`^(?:SFP\+|SFP28|SFP|QSFP28|GbE|2\.5 GbE|2\.5G Port|10G Port|SFP28 Port|QSFP28 Port) (\d+)$`)
	// ev is the number a text says, 0 when it numbers nothing.
	ev := func(s string) int {
		for _, re := range []struct {
			re  *regexp.Regexp
			src string
		}{{labelled, s}, {named, s}, {prefixed, s}} {
			if m := re.re.FindStringSubmatch(re.src); m != nil {
				n, _ := strconv.Atoi(m[1])
				return n
			}
		}
		return 0
	}
	renamed, blocked, seen := 0, 0, map[string]bool{}
	for _, m := range models {
		if !strings.HasPrefix(m.Path, "Ubiquiti/") {
			continue
		}
		seen[m.Slug] = true
		var wi []devicetypes.Port
		for _, p := range m.Ports {
			if p.Group == "interface" && !strings.HasPrefix(p.Type, "ieee802.11") && p.Type != "other-wireless" {
				wi = append(wi, p)
			}
		}
		complete, changed := len(wi) > 0, false
		for k, p := range wi {
			if p.Name != fmt.Sprintf("Port %d", k+1) || (ev(p.Label) != 0 && ev(p.Label) != k+1) {
				complete = false
			}
			if p.Label != p.Name && ev(p.Label) == k+1 {
				changed = true
			}
		}
		reason, isBlocked := blockedPortNames[m.Slug]
		if !complete {
			blocked++
			if !isBlocked {
				t.Errorf("%s: its interfaces are not named Port 1..%d with the evidence of their labels and the model is not in blockedPortNames", m.Slug, len(wi))
			}
			// A partial rename: an interface named Port k whose label says it
			// is the k-th, in a model that is not renamed as a whole.
			for k, p := range wi {
				if p.Name == fmt.Sprintf("Port %d", k+1) && p.Label != p.Name && ev(p.Label) == k+1 {
					t.Errorf("%s: interface %d is renamed %q (label %q) but the model is blocked", m.Slug, k+1, p.Name, p.Label)
				}
			}
			continue
		}
		if isBlocked {
			t.Errorf("%s: named Port 1..%d with evidence but still listed as blocked (%s)", m.Slug, len(wi), reason)
		}
		if changed {
			renamed++
		}
	}
	for slug, reason := range blockedPortNames {
		if !seen[slug] {
			t.Errorf("blockedPortNames lists %s (%s), which is not a Ubiquiti model", slug, reason)
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
