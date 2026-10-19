package devicetypes_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func slugs(cs []devicetypes.Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Model.Slug)
	}
	return out
}

// What the embedded library says about the devices ADR 0043 left open: a
// name that maps to several chassis is not certain, and the chassis are the
// candidates; a SKU or a variant's own name is certain.
func TestChoicesOfTheEmbeddedLibrary(t *testing.T) {
	ix := defaultIndex(t)
	cases := []struct {
		name, vendor, model, pn string
		certain                 bool
		reason, match           string
		want                    []string // candidates that must be there
	}{
		{"dell r330 by name: the generic model, three chassis", "Dell Inc.", "PowerEdge R330", "", false, devicetypes.UncertainBase, "dell-poweredge-r330",
			[]string{"dell-poweredge-r330", "dell-poweredge-r330-8sff", "dell-poweredge-r330-4lff-hot-plug", "dell-poweredge-r330-4lff-cabled"}},
		{"vendor repeated in the model", "Dell", "Dell PowerEdge R330", "", false, devicetypes.UncertainBase, "dell-poweredge-r330",
			[]string{"dell-poweredge-r330-8sff"}},
		{"a variant's own name is certain", "Dell", "PowerEdge R330 8 x 2.5in hot-plug", "", true, devicetypes.ByModel, "dell-poweredge-r330-8sff",
			[]string{"dell-poweredge-r330-8sff"}},
		{"dl380 gen9 without a known sku", "HPE", "ProLiant DL380 Gen9", "", false, devicetypes.UncertainBase, "hpe-proliant-dl380-gen9",
			[]string{"hpe-proliant-dl380-gen9", "hpe-proliant-dl380-gen9-8sff", "hpe-proliant-dl380-gen9-24sff"}},
		{"dl380 gen9 with the 24sff sku: certain, siblings still listed", "HPE", "ProLiant DL380 Gen9", "767032-B21", true, devicetypes.BySKU, "hpe-proliant-dl380-gen9-24sff",
			[]string{"hpe-proliant-dl380-gen9-8sff", "hpe-proliant-dl380-gen9-24sff"}},
		{"dl180 gen9: no generic model, three chassis", "Hewlett Packard Enterprise", "ProLiant DL180 Gen9", "", false, devicetypes.UncertainAmbiguous, "",
			[]string{"hpe-proliant-dl180-gen9-8sff", "hpe-proliant-dl180-gen9-8lff", "hpe-proliant-dl180-gen9-12lff"}},
		{"airflow family", "Arista Networks", "DCS-7050SX3-48YC8", "", false, devicetypes.UncertainAirflow, "arista-dcs-7050sx3-48yc8",
			[]string{"arista-dcs-7050sx3-48yc8-f"}},
		{"mounting suffix, one model", "MikroTik", "CRS354-48G-4S+2Q+", "", true, devicetypes.ByMounting, "mikrotik-crs354-48g-4s-plus-2q-plus-rm",
			[]string{"mikrotik-crs354-48g-4s-plus-2q-plus-rm"}},
		{"unknown", "Acme", "Mystery box", "", false, devicetypes.UncertainNone, "", nil},
		{"no vendor, no guess", "", "PowerEdge R330", "", false, devicetypes.UncertainNone, "", nil},
	}
	for _, c := range cases {
		ch := ix.Choices(c.vendor, c.model, c.pn)
		if ch.Certain != c.certain || ch.Reason != c.reason {
			t.Errorf("%s: certain %v reason %q, want %v %q", c.name, ch.Certain, ch.Reason, c.certain, c.reason)
		}
		got := ""
		if ch.Match != nil {
			got = ch.Match.Model.Slug
		}
		if got != c.match {
			t.Errorf("%s: match %q, want %q", c.name, got, c.match)
		}
		have := slugs(ch.Candidates)
		for _, w := range c.want {
			if !slices.Contains(have, w) {
				t.Errorf("%s: candidates %v, want %s among them", c.name, have, w)
			}
		}
		if c.want == nil && len(have) != 0 {
			t.Errorf("%s: candidates %v, want none", c.name, have)
		}
		if !slices.IsSorted(have) {
			t.Errorf("%s: candidates %v not sorted by slug", c.name, have)
		}
		for _, s := range have {
			if ix.BySlug(s) == nil {
				t.Errorf("%s: candidate %s is not a model of the library", c.name, s)
			}
		}
	}
	// A variant's own name is certain; its file names the generic model
	// (variant_of), so its siblings stay candidates for a change.
	if ch := ix.Choices("Dell", "PowerEdge R330 8 x 2.5in hot-plug", ""); !ch.Certain ||
		!slices.Equal(slugs(ch.Candidates), []string{"dell-poweredge-r330", "dell-poweredge-r330-4lff-cabled", "dell-poweredge-r330-4lff-hot-plug", "dell-poweredge-r330-8sff"}) {
		t.Errorf("an exact variant: certain %v, candidates %v", ch.Certain, slugs(ch.Candidates))
	}
	// A family is the vendor's only.
	for _, c := range ix.Choices("Dell", "PowerEdge R330", "").Candidates {
		if c.Model.Vendor != "Dell" {
			t.Errorf("foreign candidate %s", c.Model.Slug)
		}
	}
}

func dell(slug, model string, u float64) *devicetypes.Model {
	return &devicetypes.Model{Slug: slug, Vendor: "Dell", Model: model, UHeight: u}
}

// variant_of links models whose names say nothing of the family, both ways:
// a variant pulls in its base and siblings, a base pulls in its variants.
func TestChoicesFollowVariantOf(t *testing.T) {
	base := dell("x-base", "Box 1", 1)
	a := dell("x-a", "Cage alpha", 1)
	a.VariantOf = "x-base"
	b := dell("x-b", "Cage beta", 2)
	b.VariantOf = "x-base"
	other := dell("y", "Other", 1)
	foreign := &devicetypes.Model{Slug: "z", Vendor: "HPE", Model: "Cage gamma", UHeight: 1, VariantOf: "x-base"}
	ix := devicetypes.NewIndex([]*devicetypes.Model{base, a, b, other, foreign})

	ch := ix.Choices("Dell", "Box 1", "")
	if ch.Certain || ch.Reason != devicetypes.UncertainBase || !slices.Equal(slugs(ch.Candidates), []string{"x-a", "x-b", "x-base"}) {
		t.Fatalf("base: %+v %v", ch, slugs(ch.Candidates))
	}
	for _, c := range ch.Candidates {
		if want := map[string]string{"x-base": devicetypes.CandidateName}[c.Model.Slug]; want != "" && c.How != want || want == "" && c.How != devicetypes.CandidateVariantOf {
			t.Errorf("%s found by %s", c.Model.Slug, c.How)
		}
	}
	// A variant's own name is certain, and its siblings stay candidates for a change.
	ch = ix.Choices("Dell", "Cage alpha", "")
	if !ch.Certain || ch.Reason != devicetypes.ByModel || !slices.Equal(slugs(ch.Candidates), []string{"x-a", "x-b", "x-base"}) {
		t.Fatalf("variant: %+v %v", ch, slugs(ch.Candidates))
	}
	if ch := ix.Choices("Dell", "Other", ""); !ch.Certain || ch.Reason != devicetypes.ByModel || len(ch.Candidates) != 1 {
		t.Fatalf("a model with no variant is certain: %+v", ch)
	}
}

// A name that fits too many models offers none: a bare "PowerEdge" is not a family.
func TestChoicesOfAVagueName(t *testing.T) {
	var ms []*devicetypes.Model
	for i := 0; i <= devicetypes.MaxCandidates; i++ {
		ms = append(ms, dell(fmt.Sprintf("pe-%02d", i), fmt.Sprintf("PowerEdge R%d", 100+i), 1))
	}
	ix := devicetypes.NewIndex(ms)
	if ch := ix.Choices("Dell", "PowerEdge", ""); len(ch.Candidates) != 0 || ch.Reason != devicetypes.UncertainNone {
		t.Fatalf("a vague name offered %d candidates (%s)", len(ch.Candidates), ch.Reason)
	}
	if ch := ix.Choices("Dell", "PowerEdge R100", ""); !ch.Certain {
		t.Fatalf("an exact name among many: %+v", ch)
	}
}

func TestParseVariantOf(t *testing.T) {
	const head = "manufacturer: Dell\nmodel: X\nslug: x\nu_height: 1\n"
	m, err := devicetypes.Parse([]byte(head+"variant_of: dell-x-base\n"), devicetypes.OriginLocal, "x.yaml")
	if err != nil || m.VariantOf != "dell-x-base" {
		t.Fatalf("%+v %v", m, err)
	}
	for _, bad := range []string{"variant_of: Not A Slug\n", "variant_of: x\n"} {
		if _, err := devicetypes.Parse([]byte(head+bad), devicetypes.OriginLocal, "x.yaml"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A declared model: the library describes it and claims no instance_of.
func TestBatchStepsAsideForADeclaredModel(t *testing.T) {
	ix := defaultIndex(t)
	at := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	b, links := ix.Batch([]devicetypes.Device{
		{ID: "00000000-0000-0000-0000-000000000001", Vendor: "Dell", Model: "PowerEdge R330", Declared: "dell-poweredge-r330-8sff"},
		{ID: "00000000-0000-0000-0000-000000000002", Vendor: "Dell", Model: "PowerEdge R330", Declared: "?"},
		{ID: "00000000-0000-0000-0000-000000000003", Vendor: "Dell", Model: "PowerEdge R330"},
	}, at)
	if len(b.Relations) != 1 || b.Relations[0].From.Identity["id"] != "00000000-0000-0000-0000-000000000003" {
		t.Fatalf("relations %+v, want the undeclared device's only", b.Relations)
	}
	var got []string
	for _, o := range b.Objects {
		got = append(got, o.Identity["slug"])
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"dell-poweredge-r330", "dell-poweredge-r330-8sff"}) {
		t.Fatalf("device types %v, want the declared and the matched one", got)
	}
	if len(links) != 2 || links[0].Match.How != devicetypes.ByDeclared {
		t.Fatalf("links %+v", links)
	}
}

// A person's choice of chassis wins over the library's match by name and
// survives the library's later passes and the collector's later runs: the
// library withdraws its own instance_of and only describes the chosen model.
func TestDeclaredChassisSurvivesLaterCollections(t *testing.T) {
	e, s, c := newEngine(t)
	ctx := context.Background()
	ix := defaultIndex(t)
	dev := addDevice(t, e, c, "SN-R330", "Dell Inc.", "PowerEdge R330")
	c.advance(time.Minute)
	if _, _, err := devicetypes.Sync(ctx, e, ix, ""); err != nil {
		t.Fatal(err)
	}
	if rels := instanceOf(t, s, dev); len(rels) != 1 || rels[0].ToID != deviceType(t, s, "dell-poweredge-r330").ID {
		t.Fatalf("before the choice: %+v, want the generic R330", rels)
	}

	// The choice, as the UI sends it: the model by its slug, the device by id.
	c.advance(time.Minute)
	choose := sdk.NewBatch("user:carol", sdk.ScopePartial)
	choose.ObservedAt = c.t
	choose.Object("m", "device_type").Identify("slug", "dell-poweredge-r330-4lff-cabled")
	choose.RelateEndpoints("instance_of", sdk.ByID("device", dev), sdk.Ref("m"))
	if _, err := e.Apply(ctx, choose, "carol", "front has four cabled bays"); err != nil {
		t.Fatal(err)
	}
	cabled := deviceType(t, s, "dell-poweredge-r330-4lff-cabled")
	if _, has := cabled.Attrs["u_height"]; has {
		t.Fatalf("the choice itself claims no model attribute: %+v", cabled.Attrs)
	}

	// The collector runs again, then the library: the choice stands.
	for i := 0; i < 2; i++ {
		c.advance(time.Minute)
		addDevice(t, e, c, "SN-R330", "Dell Inc.", "PowerEdge R330")
		_, links, err := devicetypes.Sync(ctx, e, ix, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || links[0].Match.How != devicetypes.ByDeclared || links[0].Match.Model.Slug != cabled.Attrs["slug"] {
			t.Fatalf("pass %d links %+v", i, links)
		}
		rels := instanceOf(t, s, dev)
		if len(rels) != 1 || rels[0].ToID != cabled.ID || rels[0].Source != "user:carol" {
			t.Fatalf("pass %d: %+v, want only carol's 4LFF cabled", i, rels)
		}
	}
	// The library described the chosen model.
	if dt := deviceType(t, s, "dell-poweredge-r330-4lff-cabled"); dt.Attrs["u_height"] != 1.0 || dt.Attrs["model"] != "PowerEdge R330 4 x 3.5in cabled" {
		t.Fatalf("chosen model not described: %+v", dt.Attrs)
	}
	claims, err := s.RelationClaims(ctx, "instance_of", []string{dev})
	if err != nil || len(claims) != 1 || claims[0].Source != "user:carol" {
		t.Fatalf("claims %+v %v, want carol's only (the library withdrew its own)", claims, err)
	}
	if none, err := s.RelationClaims(ctx, "instance_of", []string{}); err != nil || none != nil {
		t.Fatalf("no device, no claim: %v %v", none, err)
	}

	// Withdrawn, the choice gives the device back to the library's match.
	c.advance(time.Minute)
	undo := sdk.NewBatch("user:carol", sdk.ScopePartial)
	undo.ObservedAt = c.t
	undo.Unrelations = append(undo.Unrelations, &sdk.Relation{Type: "instance_of", From: sdk.ByID("device", dev), To: sdk.ByID("device_type", cabled.ID)})
	if _, err := e.Apply(ctx, undo, "carol", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := devicetypes.Sync(ctx, e, ix, ""); err != nil {
		t.Fatal(err)
	}
	if rels := instanceOf(t, s, dev); len(rels) != 1 || rels[0].Source != devicetypes.Source {
		t.Fatalf("after the withdrawal: %+v, want the library's match again", rels)
	}
}

// The catalogue curates its families with variant_of: a server reporting
// "ProLiant DL380 Gen10" is offered the generic model and its four chassis,
// not the DL380 Gen10 Plus whose name only shares the words; a family nobody
// curates (the DL180 Gen9 cages, an airflow pair) is still found by name.
func TestChoicesOfACuratedFamily(t *testing.T) {
	ix := defaultIndex(t)
	for _, c := range []struct {
		vendor, model string
		want, not     []string
	}{
		{"HPE", "ProLiant DL380 Gen10", []string{"hpe-proliant-dl380-gen10", "hpe-proliant-dl380-gen10-8lff", "hpe-proliant-dl380-gen10-8sff", "hpe-proliant-dl380-gen10-12lff", "hpe-proliant-dl380-gen10-24sff"}, []string{"hpe-proliant-dl380-gen10-plus"}},
		{"HPE", "ProLiant DL360 Gen10", []string{"hpe-proliant-dl360-gen10", "hpe-proliant-dl360-gen10-4lff", "hpe-proliant-dl360-gen10-8sff"}, []string{"hpe-proliant-dl360-gen10-plus"}},
		{"Dell Inc.", "PowerEdge R730xd", []string{"dell-poweredge-r730xd", "dell-poweredge-r730xd-12lff", "dell-poweredge-r730xd-24sff"}, nil},
		{"HPE", "ProLiant DL380 Gen10 Plus", []string{"hpe-proliant-dl380-gen10-plus"}, []string{"hpe-proliant-dl380-gen10"}},
		{"HPE", "ProLiant DL180 Gen9", []string{"hpe-proliant-dl180-gen9-8sff", "hpe-proliant-dl180-gen9-12lff"}, nil},
	} {
		got := slugs(ix.Choices(c.vendor, c.model, "").Candidates)
		for _, w := range c.want {
			if !slices.Contains(got, w) {
				t.Errorf("%s: candidates %v lack %s", c.model, got, w)
			}
		}
		for _, n := range c.not {
			if slices.Contains(got, n) {
				t.Errorf("%s: candidates %v hold %s", c.model, got, n)
			}
		}
	}
}
