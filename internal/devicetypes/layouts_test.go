package devicetypes_test

import (
	"io/fs"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/VargaFoundation/trica/internal/devicetypes"
)

// Every layout of the library lies over a drawing of the same face, has its
// size, and the faces of the fleet's models have the spots their parts are
// matched to (ADR 0047).
func TestEmbeddedLayoutsFitTheirDrawings(t *testing.T) {
	ix := defaultIndex(t)
	lib := os.DirFS("library")
	layouts := 0
	for _, m := range ix.Models() {
		for _, face := range []string{devicetypes.Front, devicetypes.Rear} {
			l := m.Layout(face)
			if l == nil {
				continue
			}
			layouts++
			svg, err := fs.ReadFile(lib, path.Join(devicetypes.DrawingDir, m.Vendor, m.Slug+"."+face+".svg"))
			if err != nil {
				t.Errorf("%s: no drawing under it: %v", l.Path, err)
				continue
			}
			vb := viewBox.FindStringSubmatch(string(svg))
			w, _ := strconv.ParseFloat(vb[1], 64)
			h, _ := strconv.ParseFloat(vb[2], 64)
			if math.Abs(l.WidthMM-w) > 0.01 || math.Abs(l.HeightMM-h) > 0.01 {
				t.Errorf("%s: %gx%g, the drawing is %gx%g", l.Path, l.WidthMM, l.HeightMM, w, h)
			}
			if l.Origin != devicetypes.OriginTrica || !m.Image(face).Drawn {
				t.Errorf("%s: origin %s over an image that is not drawn (origin %s)", l.Path, l.Origin, m.Image(face).Origin)
			}
		}
	}
	if layouts < 20 {
		t.Fatalf("%d layouts, want the 32 the generator writes", layouts)
	}
	has := func(slug, face, kind string, names ...string) {
		t.Helper()
		l := ix.BySlug(slug).Layout(face)
		if l == nil {
			t.Errorf("%s %s: no layout", slug, face)
			return
		}
		got := map[string]bool{}
		for _, s := range l.Spots {
			if s.Kind == kind {
				got[s.Name] = true
			}
		}
		for _, n := range names {
			if !got[n] {
				t.Errorf("%s %s: no %s %q", slug, face, kind, n)
			}
		}
	}
	has("hpe-proliant-dl360-gen9-8sff", devicetypes.Front, devicetypes.SpotBay, "1:1", "1:8")
	has("hpe-proliant-dl360-gen9-8sff", devicetypes.Rear, devicetypes.SpotPSU, "1", "2")
	has("hpe-proliant-dl360-gen9-8sff", devicetypes.Rear, devicetypes.SpotPort, "bmc", "nic1", "nic4", "flexlom1")
	has("hpe-proliant-dl380-gen9-24sff", devicetypes.Front, devicetypes.SpotBay, "1:1", "2:8", "3:8")
	has("dell-poweredge-r330-8sff", devicetypes.Front, devicetypes.SpotBay, "0", "7")
	has("arista-dcs-7280se-64-r", devicetypes.Front, devicetypes.SpotPort, "Ethernet1", "Ethernet48", "Ethernet52", "Management1")
	has("arista-dcs-7280se-64-r", devicetypes.Rear, devicetypes.SpotFan, "1", "4")
	has("mikrotik-crs354-48g-4s-plus-2q-plus-rm", devicetypes.Front, devicetypes.SpotPort, "ether1", "ether49", "sfp-sfpplus4", "qsfpplus2")
	// DL380 Gen10 (QuickSpecs a00008180enw): the 8SFF CTO has its drives in
	// Box 3, the box iLO names 3 (3:1 for an NVMe drive, 1I:3:1 for SAS);
	// the NVMe cage takes the same bays; the 24SFF has the three boxes. The
	// rear has the eight slots of the three risers, both supplies, the iLO
	// port, the embedded and the FlexibleLOM ports; every chassis shares it.
	for _, slug := range []string{"hpe-proliant-dl380-gen10-8sff", "hpe-proliant-dl380-gen10-8sff-nvme"} {
		has(slug, devicetypes.Front, devicetypes.SpotBay, "3:1", "3:5", "3:8")
		if n := len(ix.BySlug(slug).Layout(devicetypes.Front).Spots); n != 8 {
			t.Errorf("%s front: %d spots, want the 8 bays of Box 3", slug, n)
		}
	}
	for _, slug := range []string{"hpe-proliant-dl380-gen10-24sff", "hpe-proliant-dl380-gen10-24sff-nvme"} {
		has(slug, devicetypes.Front, devicetypes.SpotBay, "1:1", "2:8", "3:1", "3:8")
		if n := len(ix.BySlug(slug).Layout(devicetypes.Front).Spots); n != 24 {
			t.Errorf("%s front: %d spots, want the 24 bays of the three boxes", slug, n)
		}
	}
	for _, slug := range []string{"hpe-proliant-dl380-gen10-8sff", "hpe-proliant-dl380-gen10-8sff-nvme", "hpe-proliant-dl380-gen10-24sff", "hpe-proliant-dl380-gen10-24sff-nvme", "hpe-proliant-dl380-gen10-12lff", "hpe-proliant-dl380-gen10-8lff"} {
		has(slug, devicetypes.Rear, devicetypes.SpotPSU, "1", "2")
		has(slug, devicetypes.Rear, devicetypes.SpotSlot, "1", "2", "3", "4", "5", "6", "7", "8")
		has(slug, devicetypes.Rear, devicetypes.SpotPort, "bmc", "nic1", "nic4", "flexlom1", "flexlom4")
	}
	// Box 1 on the left, Box 3 beside the controls; power supply bay 1 on the
	// far right of the rear, bay 2 beside it (the rear legend and the CC0
	// picture).
	x := func(slug, face, kind, name string) float64 {
		for _, s := range ix.BySlug(slug).Layout(face).Spots {
			if s.Kind == kind && s.Name == name {
				return s.X
			}
		}
		t.Fatalf("%s %s: no %s %s", slug, face, kind, name)
		return 0
	}
	if b1, b2, b3 := x("hpe-proliant-dl380-gen10-24sff", devicetypes.Front, devicetypes.SpotBay, "1:1"),
		x("hpe-proliant-dl380-gen10-24sff", devicetypes.Front, devicetypes.SpotBay, "2:1"),
		x("hpe-proliant-dl380-gen10-24sff", devicetypes.Front, devicetypes.SpotBay, "3:1"); b1 >= b2 || b2 >= b3 {
		t.Errorf("DL380 Gen10 boxes at %g, %g, %g: want Box 1, 2, 3 from left to right", b1, b2, b3)
	}
	if p1, p2 := x("hpe-proliant-dl380-gen10-8sff", devicetypes.Rear, devicetypes.SpotPSU, "1"),
		x("hpe-proliant-dl380-gen10-8sff", devicetypes.Rear, devicetypes.SpotPSU, "2"); p1 <= p2 {
		t.Errorf("DL380 Gen10 supplies: bay 1 at %g, bay 2 at %g; want bay 1 on the right", p1, p2)
	}
	// A photograph has no layout: nobody measured it.
	if l := ix.BySlug("hpe-proliant-dl360-gen9").Layout(devicetypes.Front); l != nil {
		t.Errorf("a picture got a layout: %s", l.Path)
	}
}

const box1Layout = `{"width_mm": 482.6, "height_mm": 44.45, "spots": [{"kind": "bay", "name": "1", "x": 10, "y": 5, "w": 20, "h": 10}]}`

func TestLayoutsThatFailTheLoad(t *testing.T) {
	base := fstest.MapFS{
		"lib/Acme/box.yaml": file([]byte(acme)),
		"lib/elevation-images/Acme/acme-box-1.front.png": file(png),
	}
	const at = "lib/layouts/Acme/acme-box-1.front.json"
	spot := func(s string) string {
		return `{"width_mm": 100, "height_mm": 40, "spots": [` + s + `]}`
	}
	cases := map[string]struct {
		path, body, want string
	}{
		"no model":         {"lib/layouts/Acme/acme-box-7.front.json", box1Layout, "no model"},
		"no image":         {"lib/layouts/Acme/acme-box-1.rear.json", box1Layout, "no rear image"},
		"not json":         {"lib/layouts/Acme/acme-box-1.front.yaml", box1Layout, "no model"},
		"unknown field":    {at, `{"width_mm": 1, "height_mm": 1, "spots": [], "scale": 2}`, "unknown field"},
		"path in the file": {at, `{"path": "x", "width_mm": 1, "height_mm": 1, "spots": []}`, "set by the library"},
		"no size":          {at, `{"width_mm": 0, "height_mm": 40, "spots": []}`, "positive"},
		"no spot":          {at, spot(""), "0 spots"},
		"unknown kind":     {at, spot(`{"kind": "cpu", "name": "1", "x": 1, "y": 1, "w": 1, "h": 1}`), "kind \"cpu\""},
		"no name":          {at, spot(`{"kind": "bay", "name": "", "x": 1, "y": 1, "w": 1, "h": 1}`), "1 to 64 bytes"},
		"empty":            {at, spot(`{"kind": "bay", "name": "1", "x": 1, "y": 1, "w": 0, "h": 1}`), "not inside"},
		"outside":          {at, spot(`{"kind": "bay", "name": "1", "x": 90, "y": 1, "w": 20, "h": 1}`), "not inside"},
		"negative":         {at, spot(`{"kind": "bay", "name": "1", "x": -1, "y": 1, "w": 2, "h": 1}`), "not inside"},
		"twice": {at, spot(`{"kind": "psu", "name": "1", "x": 1, "y": 1, "w": 1, "h": 1},` +
			`{"kind": "psu", "name": "1", "x": 5, "y": 1, "w": 1, "h": 1}`), "twice"},
		"too large": {at, box1Layout + strings.Repeat(" ", devicetypes.MaxLayoutBytes), "more than"},
	}
	for name, c := range cases {
		lib := fstest.MapFS{}
		for k, v := range base {
			lib[k] = v
		}
		lib[c.path] = file([]byte(c.body))
		_, err := devicetypes.OpenFS(lib, "lib", nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, c.want)
		}
	}
	// One spot of each kind under the same name is not a duplicate: bay 1 and
	// power supply 1 are two places.
	lib := fstest.MapFS{at: file([]byte(spot(`{"kind": "bay", "name": "1", "x": 1, "y": 1, "w": 1, "h": 1},` +
		`{"kind": "psu", "name": "1", "x": 5, "y": 1, "w": 1, "h": 1}`)))}
	for k, v := range base {
		lib[k] = v
	}
	l, err := devicetypes.OpenFS(lib, "lib", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := devicetypes.NewIndex(l.Models).BySlug("acme-box-1").Layout(devicetypes.Front); got == nil || len(got.Spots) != 2 || got.Path != "layouts/Acme/acme-box-1.front.json" {
		t.Fatalf("layout %+v, want two spots read from layouts/Acme/acme-box-1.front.json", got)
	}
}

// A layout belongs to the image it was measured on: the operator's photograph
// drops the embedded layout unless the operator gives one; the operator's
// model keeps the embedded layouts with the embedded images.
func TestOperatorLayouts(t *testing.T) {
	lib := fstest.MapFS{
		"lib/Acme/box.yaml":                               file([]byte(acme)),
		"lib/Acme/box2.yaml":                              file([]byte(strings.ReplaceAll(acme, "box-1", "box-2"))),
		"lib/Acme/box3.yaml":                              file([]byte(strings.ReplaceAll(acme, "box-1", "box-3"))),
		"lib/elevation-images/Acme/acme-box-1.front.png":  file(png),
		"lib/elevation-images/Acme/acme-box-1.rear.png":   file(png),
		"lib/elevation-images/Acme/acme-box-2.front.png":  file(png),
		"lib/elevation-images/Acme/acme-box-3.front.png":  file(png),
		"lib/layouts/Acme/acme-box-1.front.json":          file([]byte(box1Layout)),
		"lib/layouts/Acme/acme-box-1.rear.json":           file([]byte(box1Layout)),
		"lib/layouts/Acme/acme-box-2.front.json":          file([]byte(box1Layout)),
		"lib/layouts/Acme/acme-box-3.front.json":          file([]byte(box1Layout)),
		"lib/elevation-images/Acme/README.md":             file([]byte("x")),
		"lib/layouts/README.md":                           file([]byte("x")),
		"lib/layouts/Acme/.acme-box-1.front.json.swp":     file([]byte("x")),
		"lib/elevation-images/Acme/.acme-box-1.front.swp": file([]byte("x")),
	}
	photo := `{"width_mm": 1000, "height_mm": 92, "spots": [{"kind": "psu", "name": "1", "x": 800, "y": 10, "w": 90, "h": 70}]}`
	extra := fstest.MapFS{
		// box 1: a photograph of the front without a layout, a photograph of
		// the rear with one.
		"elevation-images/Acme/acme-box-1.front.jpg": file(jpg),
		"elevation-images/Acme/acme-box-1.rear.jpg":  file(jpg),
		"layouts/Acme/acme-box-1.rear.json":          file([]byte(photo)),
		// box 2: the operator's model, the embedded image and layout.
		"mine/box2.yaml": file([]byte(strings.ReplaceAll(strings.ReplaceAll(acme, "box-1", "box-2"), "u_height: 1", "u_height: 2"))),
		// box 3: a corrected layout over the embedded drawing.
		"layouts/Acme/acme-box-3.front.json": file([]byte(photo)),
		// Unused: no image for the face, no model.
		"layouts/Acme/acme-box-2.rear.json": file([]byte(photo)),
		"layouts/Acme/acme-box-9.rear.json": file([]byte(photo)),
	}
	l, err := devicetypes.OpenFS(lib, "lib", extra)
	if err != nil {
		t.Fatal(err)
	}
	ix := devicetypes.NewIndex(l.Models)
	box := ix.BySlug("acme-box-1")
	if got := box.Layout(devicetypes.Front); got != nil {
		t.Errorf("the operator's front photograph kept the drawing's layout: %+v", got)
	}
	if got := box.Layout(devicetypes.Rear); got == nil || got.Origin != devicetypes.OriginOperator || got.WidthMM != 1000 {
		t.Errorf("rear layout %+v, want the operator's", got)
	}
	box2 := ix.BySlug("acme-box-2")
	if box2.Origin != devicetypes.OriginOperator || box2.Layout(devicetypes.Front) == nil || box2.Layout(devicetypes.Front).Origin != devicetypes.OriginTrica {
		t.Errorf("the operator's model lost the embedded layout of its slug: %+v", box2.Layout(devicetypes.Front))
	}
	if got := ix.BySlug("acme-box-3").Layout(devicetypes.Front); got == nil || got.Origin != devicetypes.OriginOperator {
		t.Errorf("box 3 layout %+v, want the operator's correction", got)
	}
	w := strings.Join(l.Warnings, "\n")
	if len(l.Warnings) != 2 || !strings.Contains(w, "acme-box-2.rear.json") || !strings.Contains(w, "acme-box-9.rear.json") {
		t.Errorf("warnings %q, want the layout without an image and the one without a model", l.Warnings)
	}
	bad := fstest.MapFS{"layouts/Acme/acme-box-1.rear.json": file([]byte(`{}`))}
	if _, err := devicetypes.OpenFS(lib, "lib", bad); err == nil || !strings.Contains(err.Error(), "extra_dir") {
		t.Errorf("a broken operator layout: %v, want the load to fail and name extra_dir", err)
	}
}
