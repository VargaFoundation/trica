package devicetypes_test

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/VargaFoundation/trica/internal/devicetypes"
)

// HPE reports the same model name for every chassis of a generation
// ("ProLiant DL380 Gen9" for the 8SFF and the 24SFF); the part number (the
// Redfish SKU) says which. A part number listed in a model's part_numbers
// picks that model before the name does.
func TestSKUPicksTheChassisVariant(t *testing.T) {
	ix := defaultIndex(t)
	cases := []struct {
		vendor, model, pn string
		slug, how         string
	}{
		{"HPE", "ProLiant DL360 Gen9", "755258-B21", "hpe-proliant-dl360-gen9-8sff", devicetypes.BySKU},
		{"HP", "ProLiant DL360 Gen9", "755258-b21", "hpe-proliant-dl360-gen9-8sff", devicetypes.BySKU},
		{"HPE", "ProLiant DL380 Gen9", "767032-B21", "hpe-proliant-dl380-gen9-24sff", devicetypes.BySKU},
		{"HPE", "ProLiant DL380 Gen9", "719064-B21", "hpe-proliant-dl380-gen9-8sff", devicetypes.BySKU},
		{"Hewlett Packard Enterprise", "ProLiant DL180 Gen9", "775506-B21", "hpe-proliant-dl180-gen9-12lff", devicetypes.BySKU},
		{"HPE", "ProLiant DL180 Gen9", "833971-B21", "hpe-proliant-dl180-gen9-8lff", devicetypes.BySKU},
		{"HPE", "ProLiant DL180 Gen9", "754523-B21", "hpe-proliant-dl180-gen9-8sff", devicetypes.BySKU},
		// Without a known SKU, the name decides as before.
		{"HPE", "ProLiant DL360 Gen9", "", "hpe-proliant-dl360-gen9", devicetypes.ByModel},
		{"HPE", "ProLiant DL380 Gen9", "000000-B21", "hpe-proliant-dl380-gen9", devicetypes.ByModel},
		// The variant's own name still matches.
		{"Dell Inc.", "PowerEdge R330 8 x 2.5in hot-plug", "", "dell-poweredge-r330-8sff", devicetypes.ByModel},
		{"Dell Inc.", "PowerEdge R330", "ABC1234", "dell-poweredge-r330", devicetypes.ByModel},
	}
	for _, c := range cases {
		m := ix.Find(c.vendor, c.model, c.pn)
		if m == nil {
			t.Errorf("%v: no match", c)
			continue
		}
		if m.Model.Slug != c.slug || m.How != c.how {
			t.Errorf("%s %s %s: %s by %s, want %s by %s", c.vendor, c.model, c.pn, m.Model.Slug, m.How, c.slug, c.how)
		}
	}
	// A DL180 Gen9 with an unknown SKU: three chassis, no generic model; no
	// guess.
	if m := ix.Find("HPE", "ProLiant DL180 Gen9", "999999-B21"); m != nil {
		t.Errorf("a DL180 Gen9 of unknown chassis matched %s", m.Model.Slug)
	}
	// SKUs are scoped by vendor.
	if m := ix.Find("Dell", "", "755258-B21"); m != nil {
		t.Errorf("an HPE SKU matched a Dell device: %s", m.Model.Slug)
	}
}

func sku(slug, model, pn string, pns ...string) *devicetypes.Model {
	return &devicetypes.Model{Slug: slug, Vendor: "HPE", Model: model, PartNumber: pn, PartNumbers: pns, UHeight: 1}
}

func TestSKURules(t *testing.T) {
	generic := sku("x", "X Gen1", "000-B21")
	small := sku("x-8", "X Gen1 8SFF", "", "111-B21", "222-B21")
	ix := devicetypes.NewIndex([]*devicetypes.Model{generic, small})
	// The list wins over the name.
	if m := ix.Find("HPE", "X Gen1", "111-B21"); m == nil || m.Model != small || m.How != devicetypes.BySKU {
		t.Fatalf("SKU in part_numbers: %+v, want x-8 by sku", m)
	}
	// The upstream part_number keeps its place, after the name.
	if m := ix.Find("HPE", "", "000-B21"); m == nil || m.Model != generic || m.How != devicetypes.ByPartNumber {
		t.Fatalf("upstream part number: %+v, want x by part_number", m)
	}
	if m := ix.Find("HPE", "unknown", "333-B21"); m != nil {
		t.Fatalf("unknown SKU matched %s", m.Model.Slug)
	}
	// Two models listing the same SKU: ambiguous, the SKU picks neither
	// and the name decides.
	other := sku("x-24", "X Gen1 24SFF", "", "222-B21")
	ix = devicetypes.NewIndex([]*devicetypes.Model{generic, small, other})
	if m := ix.Find("HPE", "X Gen1", "222-B21"); m == nil || m.Model != generic || m.How != devicetypes.ByModel {
		t.Fatalf("ambiguous SKU: %+v, want the generic model by name", m)
	}
	// The airflow base model of a family lists no SKU: it is not one variant.
	f := &devicetypes.Model{Slug: "s-f", Vendor: "HPE", Model: "S-1-F", PartNumbers: []string{"9-F"}, UHeight: 1}
	r := &devicetypes.Model{Slug: "s-r", Vendor: "HPE", Model: "S-1-R", PartNumbers: []string{"9-R"}, UHeight: 1}
	if m := devicetypes.NewIndex([]*devicetypes.Model{f, r}).Find("HPE", "S-1", ""); m == nil || m.Model.PartNumbers != nil {
		t.Fatalf("variant base model: %+v", m)
	}
}

func TestParseReadsPartNumbers(t *testing.T) {
	m := mustParse(t, minimal+"part_numbers:\n  - ' 755258-B21 '\n  - 755259-B21\n")
	if strings.Join(m.PartNumbers, ",") != "755258-B21,755259-B21" {
		t.Fatalf("part_numbers %q", m.PartNumbers)
	}
	for _, bad := range []string{
		minimal + "part_numbers:\n  - ''\n",
		minimal + "part_numbers:\n  - A\n  - A\n",
		minimal + "part_numbers: A\n",
	} {
		if _, err := devicetypes.Parse([]byte(bad), devicetypes.OriginLocal, "x.yaml"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// No SKU of the embedded library is listed by two models: it would match
// neither.
func TestEmbeddedSKUsAreUnique(t *testing.T) {
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	n := 0
	for _, m := range models {
		for _, pn := range m.PartNumbers {
			n++
			k := m.Vendor + "|" + strings.ToLower(pn)
			if prev, dup := seen[k]; dup {
				t.Errorf("%s lists %s, already listed by %s", m.Slug, pn, prev)
			}
			seen[k] = m.Slug
		}
	}
	if n == 0 {
		t.Fatal("no part_numbers in the library")
	}
}

// An upstream model's image with an SVG source under drawings/ is the
// project's; the operator's photograph still wins over it.
func TestDrawingMarksTheImageAsTheProjects(t *testing.T) {
	lib := fstest.MapFS{
		"lib/Acme/box.yaml": file([]byte(acme)),
		"lib/Acme/two.yaml": file([]byte("manufacturer: Acme\nmodel: Box 2\nslug: acme-box-2\nu_height: 1\n")),
		"lib/elevation-images/Acme/acme-box-1.front.png": file(png),
		"lib/elevation-images/Acme/acme-box-1.rear.png":  file(png),
		"lib/elevation-images/Acme/acme-box-2.front.png": file(png),
		"lib/drawings/Acme/acme-box-1.front.svg":         file([]byte("<svg/>")),
		// A model file under drawings/ is not a model.
		"lib/drawings/Acme/stray.yaml": file([]byte("not: a model\n")),
	}
	l, err := devicetypes.OpenFS(lib, "lib", nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := devicetypes.NewIndex(l.Models)
	one := ix.BySlug("acme-box-1")
	if one.FrontImage.Origin != devicetypes.OriginLocal || one.RearImage.Origin != devicetypes.OriginUpstream {
		t.Fatalf("front %s rear %s, want trica (drawn) and upstream", one.FrontImage.Origin, one.RearImage.Origin)
	}
	if two := ix.BySlug("acme-box-2"); two.FrontImage.Origin != devicetypes.OriginUpstream {
		t.Fatalf("no drawing: origin %s", two.FrontImage.Origin)
	}
	l, err = devicetypes.OpenFS(lib, "lib", fstest.MapFS{"elevation-images/Acme/acme-box-1.front.png": file(png)})
	if err != nil {
		t.Fatal(err)
	}
	if got := devicetypes.NewIndex(l.Models).BySlug("acme-box-1").FrontImage.Origin; got != devicetypes.OriginOperator {
		t.Fatalf("operator photograph: origin %s", got)
	}
}

var viewBox = regexp.MustCompile(`viewBox="0 0 ([\d.]+) ([\d.]+)"`)

// Every drawing has its PNG in the library, 1000 px wide, with the
// proportions of a 19-inch panel of the model's height, and the model it
// is named after; every image of a trica model is a drawing (or the
// operator's). Regenerate with scripts/devicetype-faces/generate.mjs --png.
func TestDrawingsMatchTheirImagesAndModels(t *testing.T) {
	ix := defaultIndex(t)
	lib := os.DirFS("library")
	drawings := 0
	err := fs.WalkDir(lib, devicetypes.DrawingDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		drawings++
		vendor := path.Base(path.Dir(p))
		name := strings.TrimSuffix(path.Base(p), ".svg")
		slug, face := name[:strings.LastIndex(name, ".")], name[strings.LastIndex(name, ".")+1:]
		m := ix.BySlug(slug)
		if m == nil || m.Vendor != vendor {
			return fmt.Errorf("%s: no model %s of %s", p, slug, vendor)
		}
		img := m.Image(face)
		if img == nil || img.ContentType != "image/png" || img.Origin != devicetypes.OriginLocal {
			return fmt.Errorf("%s: image %+v, want a PNG of origin trica", p, img)
		}
		svg, err := fs.ReadFile(lib, p)
		if err != nil {
			return err
		}
		vb := viewBox.FindStringSubmatch(string(svg))
		if vb == nil {
			return fmt.Errorf("%s: no viewBox", p)
		}
		w, _ := strconv.ParseFloat(vb[1], 64)
		h, _ := strconv.ParseFloat(vb[2], 64)
		if w != 482.6 || math.Abs(h-m.UHeight*44.45) > 0.01 {
			return fmt.Errorf("%s: %gx%g mm, want 482.6 x %g (%gU)", p, w, h, m.UHeight*44.45, m.UHeight)
		}
		pw, ph := binary.BigEndian.Uint32(img.Data[16:20]), binary.BigEndian.Uint32(img.Data[20:24])
		if pw != 1000 || int(ph) != int(math.Round(1000*h/w)) {
			return fmt.Errorf("%s: %dx%d px, want 1000x%d", img.Path, pw, ph, int(math.Round(1000*h/w)))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if drawings < 20 {
		t.Fatalf("%d drawings, want the 26 the generator writes", drawings)
	}
	for _, m := range ix.Models() {
		for _, face := range []string{devicetypes.Front, devicetypes.Rear} {
			img := m.Image(face)
			if img == nil || m.Origin != devicetypes.OriginLocal || img.Origin == devicetypes.OriginLocal {
				continue
			}
			t.Errorf("%s %s: a trica model's image of origin %s has no drawing", m.Slug, face, img.Origin)
		}
	}
}
