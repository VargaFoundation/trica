package devicetypes_test

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg" // decode the size of the embedded JPEG faces
	_ "image/png"  // and of the PNG ones
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/devicetypes"
)

var update = flag.Bool("update", false, "rewrite docs/reference/devicetypes-catalogue.md from the embedded library")

// Size budget of the library embedded in the binary (docs/reference/devicetypes.md,
// "Catalogue"): past it, the catalogue moves to a pack outside the binary.
const (
	maxFaceBytes   = 256 << 10
	maxFaceWidth   = 1000
	maxLibrarySize = 15 << 20
)

// The chassis variants of one server are a family: the key variant_of
// (ADR 0048) names the family's generic model, so that the chooser offers
// them together. In the catalogue every variant_of names a model of the
// library, of the same manufacturer and height, the variant's slug is the
// generic one's plus the chassis, and every variant has both faces.
func TestCatalogueFamilies(t *testing.T) {
	ix := defaultIndex(t)
	families := map[string][]*devicetypes.Model{}
	for _, m := range ix.Models() {
		if m.VariantOf == "" {
			continue
		}
		base := ix.BySlug(m.VariantOf)
		switch {
		case base == nil:
			t.Errorf("%s: variant_of %s is not in the library", m.Slug, m.VariantOf)
		case base.Vendor != m.Vendor || base.UHeight != m.UHeight:
			t.Errorf("%s (%s, %gU) is a variant of %s (%s, %gU)", m.Slug, m.Vendor, m.UHeight, base.Slug, base.Vendor, base.UHeight)
		case !strings.HasPrefix(m.Slug, m.VariantOf+"-"):
			t.Errorf("%s: slug does not start with its generic model's, %s", m.Slug, m.VariantOf)
		}
		// A variant exists to show its chassis: it has both faces.
		if m.FrontImage == nil || m.RearImage == nil {
			t.Errorf("%s: a variant without both faces", m.Slug)
		}
		families[m.VariantOf] = append(families[m.VariantOf], m)
	}
	if len(families) < 20 {
		t.Fatalf("%d families, want the catalogue's (at least 20)", len(families))
	}
	// The variants a chooser lists for a DL380 Gen10 and an R730xd.
	for fam, want := range map[string][]string{
		"hpe-proliant-dl380-gen10": {"8lff", "8sff", "12lff", "24sff"},
		"dell-poweredge-r730xd":    {"12lff", "24sff"},
	} {
		got := map[string]bool{}
		for _, m := range families[fam] {
			got[strings.TrimPrefix(m.Slug, fam+"-")] = true
		}
		for _, v := range want {
			if !got[v] {
				t.Errorf("family %s: no %s variant (%v)", fam, v, got)
			}
		}
	}
}

// Every embedded face fits the binary's budget: at most 1000 pixels wide
// (a face is drawn 280 units wide), at most 256 KiB, and its content decodes
// as the image its name says; the whole library (models, faces, drawings,
// layouts) stays under the budget of the binary.
func TestCatalogueImagesFitTheBudget(t *testing.T) {
	lib := os.DirFS("library")
	total := 0
	n := 0
	err := fs.WalkDir(lib, devicetypes.ImageDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(lib, p)
		if err != nil {
			return err
		}
		n++
		total += len(data)
		if len(data) > maxFaceBytes {
			t.Errorf("%s: %d bytes, want at most %d", p, len(data), maxFaceBytes)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		if ext := strings.TrimPrefix(path.Ext(p), "."); format != ext && (format != "jpeg" || ext != "jpg") {
			t.Errorf("%s: content is %s", p, format)
		}
		if cfg.Width > maxFaceWidth || cfg.Height > cfg.Width {
			t.Errorf("%s: %dx%d, want at most %d wide and wider than high", p, cfg.Width, cfg.Height, maxFaceWidth)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 300 {
		t.Errorf("%d face images, want the catalogue's (at least 300)", n)
	}
	all := 0
	err = fs.WalkDir(lib, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil {
			all += int(info.Size())
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if all > maxLibrarySize {
		t.Errorf("the embedded library weighs %d bytes, over the %d budget of the binary", all, maxLibrarySize)
	}
	t.Logf("%d face images, %d bytes; the whole library %d bytes", n, total, all)
}

// catalogueDoc renders the list of every model of the library.
func catalogueDoc(models []*devicetypes.Model) string {
	byVendor := map[string][]*devicetypes.Model{}
	var vendors []string
	for _, m := range models {
		if _, ok := byVendor[m.Vendor]; !ok {
			vendors = append(vendors, m.Vendor)
		}
		byVendor[m.Vendor] = append(byVendor[m.Vendor], m)
	}
	sort.Slice(vendors, func(i, j int) bool { return strings.ToLower(vendors[i]) < strings.ToLower(vendors[j]) })
	face := func(img *devicetypes.Image) string {
		switch {
		case img == nil:
			return "—"
		case img.Drawn:
			return "drawn"
		default:
			return "picture"
		}
	}
	var b strings.Builder
	b.WriteString("# Device-type catalogue\n\n")
	b.WriteString("<!-- Generated by `go test ./internal/devicetypes/ -run TestCatalogueListIsCurrent -update` from the embedded library. Do not edit. -->\n\n")
	fmt.Fprintf(&b, "Every one of the %d models of the embedded library, by manufacturer (see [the library](devicetypes.md)).\n\n", len(models))
	b.WriteString("**U** is the rack height (0: occupies no rack unit, a desktop or wall device). **Front** and **Rear**: ")
	b.WriteString("picture = a picture of the unit, drawn = schematic face drawn by the project, — = none. ")
	b.WriteString("↳ marks a chassis variant (`variant_of`). **SKUs** are the `part_numbers` that match a device by part number.\n")
	for _, v := range vendors {
		ms := byVendor[v]
		sort.Slice(ms, func(i, j int) bool { return ms[i].Slug < ms[j].Slug })
		fmt.Fprintf(&b, "\n## %s (%d models)\n\n| Model | Slug | U | Front | Rear | SKUs |\n|---|---|---|---|---|---|\n", v, len(ms))
		for _, m := range ms {
			name := strings.ReplaceAll(m.Model, "|", "\\|")
			if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(v)) {
				name = v + " " + name
			}
			if m.VariantOf != "" {
				name = "↳ " + name
			}
			fmt.Fprintf(&b, "| %s | `%s` | %g | %s | %s | %s |\n", name, m.Slug, m.UHeight, face(m.FrontImage), face(m.RearImage), strings.Join(m.PartNumbers, ", "))
		}
	}
	return b.String()
}

// The list of the models in docs/reference/devicetypes-catalogue.md is
// generated, so that it cannot miss a file (there were 191 hand-written rows
// and 7 models they did not list). With -update the test rewrites it.
func TestCatalogueListIsCurrent(t *testing.T) {
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	const doc = "../../docs/reference/devicetypes-catalogue.md"
	want := catalogueDoc(models)
	if *update {
		if err := os.WriteFile(doc, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date: run `go test ./internal/devicetypes/ -run TestCatalogueListIsCurrent -update` and commit it", doc)
	}
	// Every model is in the list (the doc is compared whole above; this
	// names what a hand edit would have dropped).
	for _, m := range models {
		if !strings.Contains(string(got), "`"+m.Slug+"`") {
			t.Errorf("%s is not in the catalogue list", m.Slug)
		}
	}
}
