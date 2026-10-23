package devicetypes_test

import (
	"slices"
	"testing"

	"github.com/VargaFoundation/trica/internal/devicetypes"
)

func plusIndex() *devicetypes.Index {
	return devicetypes.NewIndex([]*devicetypes.Model{
		{Slug: "acme-box-1", Vendor: "Acme", Model: "Box 1", PartNumber: "B1"},
		{Slug: "acme-box-1-plus", Vendor: "Acme", Model: "Box 1+", PartNumber: "B1+"},
		{Slug: "mikrotik-sw-24s-plus-rm", Vendor: "MikroTik", Model: "SW-24S+RM"},
	})
}

// A "+" is the word "plus" (#159): Box 1 and Box 1+ are two names, and Box
// 1+, Box 1 Plus and the slug are one.
func TestPlusIsAWord(t *testing.T) {
	ix := plusIndex()
	for _, c := range []struct {
		vendor, model, pn, slug, how string
	}{
		{"Acme", "Box 1", "", "acme-box-1", devicetypes.ByModel},
		{"Acme", "Box 1+", "", "acme-box-1-plus", devicetypes.ByModel},
		{"Acme", "Box 1 Plus", "", "acme-box-1-plus", devicetypes.ByModel},
		{"Acme", "BOX-1-PLUS", "", "acme-box-1-plus", devicetypes.ByModel},
		{"Acme", "Box1plus", "", "acme-box-1-plus", devicetypes.ByModel},
		{"Acme", "acme-box-1-plus", "", "acme-box-1-plus", devicetypes.ByModel},
		{"Acme", "", "B1+", "acme-box-1-plus", devicetypes.ByPartNumber},
		{"Acme", "", "b1 plus", "acme-box-1-plus", devicetypes.ByPartNumber},
		{"Acme", "", "B1", "acme-box-1", devicetypes.ByPartNumber},
		{"MikroTik", "SW-24S+", "", "mikrotik-sw-24s-plus-rm", devicetypes.ByMounting},
		{"MikroTik", "SW-24S Plus", "", "mikrotik-sw-24s-plus-rm", devicetypes.ByMounting},
	} {
		m := ix.Find(c.vendor, c.model, c.pn)
		if m == nil || m.Model.Slug != c.slug || m.How != c.how {
			t.Errorf("%s %q %q = %+v, want %s by %s", c.vendor, c.model, c.pn, m, c.slug, c.how)
		}
	}
	if m := ix.Find("MikroTik", "SW-24S", ""); m != nil {
		t.Errorf("a name without its plus matched %s", m.Model.Slug)
	}
}

func TestNormAndWordsReadPlus(t *testing.T) {
	for in, want := range map[string]string{"DS218+": "ds218plus", "DS218": "ds218", "UDM-SE": "udmse", "DS218 Plus": "ds218plus"} {
		if got := devicetypes.Norm(in); got != want {
			t.Errorf("Norm(%q) = %q, want %q", in, got, want)
		}
	}
	if got := devicetypes.Words("Synology", "Synology DS218+"); !slices.Equal(got, []string{"ds218", "plus"}) {
		t.Errorf("Words = %v", got)
	}
	if got := devicetypes.Words("MikroTik", "CRS326-24G-2S+RM"); !slices.Equal(got, []string{"crs326", "24g", "2s", "plus", "rm"}) {
		t.Errorf("Words = %v", got)
	}
}

// "+" and "Plus" give the same choice (#159): DS218+ is not offered the
// DS218, and the DS218 is offered both.
func TestPlusSpelledOutHasTheSameChoices(t *testing.T) {
	ix := defaultIndex(t)
	for _, c := range []struct {
		vendor, model, match string
		candidates           []string
	}{
		{"Synology", "DS218", "synology-ds218", []string{"synology-ds218", "synology-ds218-plus"}},
		{"Synology", "DS218+", "synology-ds218-plus", []string{"synology-ds218-plus"}},
		{"Synology", "DS218 Plus", "synology-ds218-plus", []string{"synology-ds218-plus"}},
		{"Synology", "RS1221+", "synology-rs1221-plus", []string{"synology-rs1221-plus"}},
		{"Synology", "RS1221 Plus", "synology-rs1221-plus", []string{"synology-rs1221-plus"}},
		{"Ubiquiti", "U6+", "ubiquiti-u6-plus", []string{"ubiquiti-u6-plus"}},
		{"Ubiquiti", "U6 Plus", "ubiquiti-u6-plus", []string{"ubiquiti-u6-plus"}},
	} {
		ch := ix.Choices(c.vendor, c.model, "")
		if ch.Match == nil || ch.Match.Model.Slug != c.match || !ch.Certain {
			t.Errorf("%s: %+v, want %s, certain", c.model, ch, c.match)
			continue
		}
		if got := slugs(ch.Candidates); !slices.Equal(got, c.candidates) {
			t.Errorf("%s: candidates %v, want %v", c.model, got, c.candidates)
		}
	}
}
