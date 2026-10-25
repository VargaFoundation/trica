package devicetypes_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/VargaFoundation/trica/internal/devicetypes"
)

var (
	png  = []byte("\x89PNG\r\n\x1a\nbody")
	jpg  = []byte("\xff\xd8\xff\xe0body")
	webp = []byte("RIFF\x00\x00\x00\x00WEBPbody")
)

func file(b []byte) *fstest.MapFile { return &fstest.MapFile{Data: b} }

// The embedded library: each image belongs to a model and is found by its
// manufacturer, slug and face; a model without a file has no image. Every
// embedded image has the origin trica; one with an SVG source under
// drawings/ is Drawn, the others are pictures of the unit.
func TestEmbeddedImagesAreFoundByTheirName(t *testing.T) {
	ix := defaultIndex(t)
	const (
		picture = false
		drawn   = true
		jpeg    = "image/jpeg"
		pngT    = "image/png"
	)
	type img struct {
		ct    string
		drawn bool
	}
	none := img{}
	cases := []struct {
		slug        string
		front, rear img
	}{
		{"hpe-proliant-dl360-gen9", img{jpeg, picture}, img{jpeg, picture}},
		{"hpe-proliant-dl380-gen10", img{pngT, picture}, img{pngT, picture}},
		// Pictures at the front, drawn rears.
		{"arista-dcs-7050sx-64-f", img{pngT, picture}, img{pngT, drawn}},
		{"arista-dcs-7050sx-64-r", img{pngT, picture}, img{pngT, drawn}},
		// The base model of an airflow family shows its first variant.
		{"arista-dcs-7050sx-64", img{pngT, picture}, img{pngT, drawn}},
		{"arista-dcs-7280se-64", img{pngT, drawn}, img{pngT, drawn}},
		// Models whose faces the project drew.
		{"mikrotik-crs354-48g-4s-plus-2q-plus-rm", img{pngT, drawn}, img{pngT, drawn}},
		// Models written from vendor documents and their drawings.
		{"hpe-proliant-dl360-gen9-8sff", img{pngT, drawn}, img{pngT, drawn}},
		{"hpe-proliant-dl380-gen9-8sff", img{pngT, drawn}, img{pngT, drawn}},
		{"hpe-proliant-dl380-gen9-24sff", img{pngT, drawn}, img{pngT, drawn}},
		{"hpe-proliant-dl180-gen9-8sff", img{pngT, drawn}, img{pngT, drawn}},
		{"hpe-proliant-dl180-gen9-8lff", img{pngT, drawn}, img{pngT, drawn}},
		{"hpe-proliant-dl180-gen9-12lff", img{pngT, drawn}, img{pngT, drawn}},
		{"dell-poweredge-r330-8sff", img{pngT, drawn}, img{pngT, drawn}},
		{"dell-poweredge-r330-4lff-hot-plug", img{pngT, drawn}, img{pngT, drawn}},
		{"dell-poweredge-r330-4lff-cabled", img{pngT, drawn}, img{pngT, drawn}},
		{"arista-dcs-7280se-64-f", img{pngT, drawn}, img{pngT, drawn}},
		{"arista-dcs-7280se-64-r", img{pngT, drawn}, img{pngT, drawn}},
		{"hpe-proliant-dl360-gen10", img{pngT, picture}, img{pngT, picture}},
		// Catalogue: pictures, and drawn faces.
		{"dell-poweredge-r730xd", img{pngT, picture}, img{pngT, picture}},
		{"cisco-ws-c2960x-48ts-l", img{pngT, picture}, img{pngT, drawn}},
		{"dell-poweredge-r730xd-12lff", img{pngT, drawn}, img{pngT, drawn}},
		{"hpe-proliant-dl380-gen10-24sff", img{pngT, drawn}, img{pngT, drawn}},
		{"apc-smt1500rmi2u", img{pngT, drawn}, img{pngT, drawn}},
		{"dell-poweredge-r730", none, none},
		// ADR 0056: the pictures of the rack-mounted UniFi gateways and
		// of the USW Pro Max 24 PoE; the other UniFi models have none.
		{"ubiquiti-unifi-dream-machine-pro-max", img{pngT, picture}, img{pngT, picture}},
		{"ubiquiti-unifi-dream-machine-pro", img{pngT, picture}, img{pngT, picture}},
		{"ubiquiti-unifi-dream-machine-pro-special-edition", img{pngT, picture}, img{pngT, picture}},
		{"ubiquiti-enterprise-fortress-gateway", img{pngT, picture}, img{pngT, picture}},
		{"ubiquiti-unifi-switch-pro-max-24-poe", img{pngT, picture}, img{pngT, picture}},
		{"ubiquiti-u7-pro", none, none},
		{"synology-ds1821-plus", none, none},
		// No picture: the variant is not known from the name alone.
		{"hpe-proliant-dl380-gen9", none, none},
		{"dell-poweredge-r330", none, none},
		{"generic-1u-pfsense-appliance", none, none},
	}
	for _, c := range cases {
		m := ix.BySlug(c.slug)
		if m == nil {
			t.Errorf("%s: not in the library", c.slug)
			continue
		}
		for face, want := range map[string]img{devicetypes.Front: c.front, devicetypes.Rear: c.rear} {
			i := m.Image(face)
			got := none
			if i != nil {
				got = img{i.ContentType, i.Drawn}
				if i.Origin != devicetypes.OriginTrica {
					t.Errorf("%s %s: origin %s, want %s", c.slug, face, i.Origin, devicetypes.OriginTrica)
				}
				if len(i.Data) == 0 || len(i.Data) > devicetypes.MaxImageBytes || !strings.HasPrefix(i.ETag, `"`) {
					t.Errorf("%s %s: %d bytes, etag %s", c.slug, face, len(i.Data), i.ETag)
				}
			}
			if got != want {
				t.Errorf("%s %s image: %+v, want %+v", c.slug, face, got, want)
			}
		}
	}
	if m := ix.BySlug("arista-dcs-7280se-64-r"); m == nil || m.Origin != devicetypes.OriginTrica || m.UHeight != 1 || m.Airflow != "rear_to_front" {
		t.Fatalf("7280SE-64-R: %+v, want a 1U project model, rear to front", m)
	}
	if m := ix.Find("Arista Networks", "DCS-7280SE-64", ""); m == nil || m.How != devicetypes.ByVariant || m.Model.UHeight != 1 {
		t.Fatalf("a switch reporting DCS-7280SE-64 matched %+v, want the airflow-less base model", m)
	}
	if ix.BySlug("nobody") != nil {
		t.Fatal("an unknown slug found a model")
	}
}

const acme = "manufacturer: Acme\nmodel: Box 1\nslug: acme-box-1\nu_height: 1\nfront_image: true\n"

// The operator's directory wins: its image over the embedded one, its model
// over the embedded model with the same slug (which keeps its image).
func TestOperatorDirectoryOverridesTheLibrary(t *testing.T) {
	lib := fstest.MapFS{
		"lib/Acme/box.yaml":                              file([]byte(acme)),
		"lib/Acme/box2.yaml":                             file([]byte(strings.ReplaceAll(acme, "box-1", "box-2"))),
		"lib/elevation-images/Acme/acme-box-1.front.png": file(png),
		"lib/elevation-images/Acme/acme-box-2.rear.png":  file(png),
	}
	extra := fstest.MapFS{
		"elevation-images/Acme/acme-box-1.front.jpg": file(jpg),
		"elevation-images/Acme/acme-box-1.rear.webp": file(webp),
		"elevation-images/Acme/acme-box-9.front.png": file(png),
		"elevation-images/Acme/acme-box-1.top.png":   file(png),
		"elevation-images/README.md":                 file([]byte("photos of our own racks")),
		"mine/box2.yaml":                             file([]byte(strings.ReplaceAll(strings.ReplaceAll(acme, "box-1", "box-2"), "u_height: 1", "u_height: 2"))),
		"mine/own.yaml":                              file([]byte("manufacturer: Mine\nmodel: Shelf\nslug: mine-shelf\nu_height: 2\n")),
		// A Kubernetes volume: the same files again under a hidden directory.
		"..2026_10_17/mine/own.yaml": file([]byte("manufacturer: Mine\nmodel: Shelf\nslug: mine-shelf\nu_height: 2\n")),
	}
	l, err := devicetypes.OpenFS(lib, "lib", extra)
	if err != nil {
		t.Fatal(err)
	}
	ix := devicetypes.NewIndex(l.Models)
	box := ix.BySlug("acme-box-1")
	if box.FrontImage == nil || box.FrontImage.ContentType != "image/jpeg" || box.FrontImage.Origin != devicetypes.OriginOperator {
		t.Fatalf("front %+v, want the operator's JPEG", box.FrontImage)
	}
	if box.RearImage == nil || box.RearImage.ContentType != "image/webp" {
		t.Fatalf("rear %+v, want the operator's WebP", box.RearImage)
	}
	box2 := ix.BySlug("acme-box-2")
	if box2.Origin != devicetypes.OriginOperator || box2.UHeight != 2 {
		t.Fatalf("box 2 %+v, want the operator's model", box2)
	}
	if box2.RearImage == nil || box2.RearImage.Origin != devicetypes.OriginTrica || box2.RearImage.Drawn {
		t.Fatalf("the operator's model lost the embedded image of its slug: %+v", box2.RearImage)
	}
	if ix.BySlug("mine-shelf") == nil || len(l.Models) != 3 {
		t.Fatalf("%d models, want the two embedded ones and the operator's shelf", len(l.Models))
	}
	if len(l.Warnings) != 2 || !strings.Contains(strings.Join(l.Warnings, "\n"), "acme-box-9.front.png") ||
		!strings.Contains(strings.Join(l.Warnings, "\n"), "acme-box-1.top.png") {
		t.Fatalf("warnings %q, want the two unused files (an unknown slug, an unknown face)", l.Warnings)
	}
}

func TestImagesThatFailTheLoad(t *testing.T) {
	base := fstest.MapFS{"lib/Acme/box.yaml": file([]byte(acme))}
	with := func(fs fstest.MapFS, k string, v []byte) fstest.MapFS {
		out := fstest.MapFS{}
		for kk, vv := range fs {
			out[kk] = vv
		}
		out[k] = file(v)
		return out
	}
	cases := map[string]struct {
		lib, extra fstest.MapFS
		want       string
	}{
		"embedded image of no model": {with(base, "lib/elevation-images/Acme/acme-box-7.front.png", png), nil, "no model"},
		"embedded svg":               {with(base, "lib/elevation-images/Acme/acme-box-1.front.svg", []byte("<svg/>")), nil, "no model"},
		"misnamed content":           {with(base, "lib/elevation-images/Acme/acme-box-1.front.png", jpg), nil, "not image/png"},
		"operator content":           {base, fstest.MapFS{"elevation-images/Acme/acme-box-1.front.webp": file(png)}, "not image/webp"},
		"too large":                  {base, fstest.MapFS{"elevation-images/Acme/acme-box-1.front.png": file(append(png, make([]byte, devicetypes.MaxImageBytes)...))}, "more than"},
		"operator bad model":         {base, fstest.MapFS{"x/bad.yaml": file([]byte("manufacturer: X\n"))}, "extra_dir"},
		"operator duplicate":         {base, fstest.MapFS{"x/a.yaml": file([]byte(acme)), "y/b.yaml": file([]byte(acme))}, "already used"},
	}
	for name, c := range cases {
		var err error
		if c.extra == nil {
			_, err = devicetypes.OpenFS(c.lib, "lib", nil) // a nil MapFS is not a nil fs.FS
		} else {
			_, err = devicetypes.OpenFS(c.lib, "lib", c.extra)
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, c.want)
		}
	}
}

func TestParseChecksTheImageKeys(t *testing.T) {
	for _, y := range []string{minimal + "front_image: true\nrear_image: false\n", minimal} {
		mustParse(t, y)
	}
	if _, err := devicetypes.Parse([]byte(minimal+"front_image: acme.png\n"), devicetypes.OriginTrica, "x.yaml"); err == nil {
		t.Fatal("front_image must be a boolean")
	}
}

// Open reads a real directory, and refuses one that is not there.
func TestOpenReadsTheOperatorDirectory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, devicetypes.ImageDir, "HPE", "hpe-proliant-dl380-gen9.front.png")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, png, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := devicetypes.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := devicetypes.NewIndex(l.Models).BySlug("hpe-proliant-dl380-gen9")
	if m.FrontImage == nil || m.FrontImage.Origin != devicetypes.OriginOperator {
		t.Fatalf("the operator's photograph of a model the library has no image of: %+v", m.FrontImage)
	}
	if _, err := devicetypes.Open(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing directory must fail")
	}
	if _, err := devicetypes.Open(p); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a file: %v", err)
	}
}

// The device_type a device gets says where its images are; a model without
// one says nothing.
func TestBatchNamesTheImages(t *testing.T) {
	ix := defaultIndex(t)
	b, _ := ix.Batch([]devicetypes.Device{
		{ID: "00000000-0000-0000-0000-000000000001", Vendor: "HPE", Model: "ProLiant DL360 Gen9"},
		{ID: "00000000-0000-0000-0000-000000000002", Vendor: "HPE", Model: "ProLiant DL380 Gen9"},
		{ID: "00000000-0000-0000-0000-000000000003", Vendor: "Arista", Model: "DCS-7050SX-64-R"},
		{ID: "00000000-0000-0000-0000-000000000004", Vendor: "HPE", Model: "ProLiant DL380 Gen9", PartNumber: "767032-B21"},
	}, time.Now())
	got := map[string][2]any{}
	for _, o := range b.Objects {
		got[o.Identity["slug"]] = [2]any{o.Attributes["front_image"], o.Attributes["rear_image"]}
	}
	want := map[string][2]any{
		"hpe-proliant-dl360-gen9": {"elevation-images/HPE/hpe-proliant-dl360-gen9.front.jpg", "elevation-images/HPE/hpe-proliant-dl360-gen9.rear.jpg"},
		"hpe-proliant-dl380-gen9": {nil, nil},
		"arista-dcs-7050sx-64-r":  {"elevation-images/Arista/arista-dcs-7050sx-64-r.front.png", "elevation-images/Arista/arista-dcs-7050sx-64-r.rear.png"},
		"hpe-proliant-dl380-gen9-24sff": {
			"elevation-images/HPE/hpe-proliant-dl380-gen9-24sff.front.png", "elevation-images/HPE/hpe-proliant-dl380-gen9-24sff.rear.png",
		},
	}
	if len(got) != len(want) {
		t.Errorf("device types %v, want %d", got, len(want))
	}
	for slug, w := range want {
		if got[slug] != w {
			t.Errorf("%s images %v, want %v", slug, got[slug], w)
		}
	}
}
