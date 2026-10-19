package api

import (
	"fmt"
	"testing"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/store"
)

// libraryFaces are the faces of an embedded model as the faces endpoint
// gives them, with the layouts the generator wrote.
func libraryFaces(t *testing.T, slug string) []DeviceFace {
	t.Helper()
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	m := devicetypes.NewIndex(models).BySlug(slug)
	if m == nil {
		t.Fatalf("%s: not in the library", slug)
	}
	var out []DeviceFace
	for _, face := range []string{devicetypes.Front, devicetypes.Rear} {
		l := m.Layout(face)
		if l == nil {
			t.Fatalf("%s %s: no layout", slug, face)
		}
		f := DeviceFace{Face: face, Width: l.WidthMM, Height: l.HeightMM}
		for _, s := range l.Spots {
			f.Spots = append(f.Spots, FaceSpot{Kind: s.Kind, Name: s.Name, X: s.X, Y: s.Y, W: s.W, H: s.H})
		}
		out = append(out, f)
	}
	return out
}

// A synthetic DL380 Gen10 8SFF shaped like a storage node: five NVMe drives
// in Box 3 as iLO 5 reports them (named and slotted "3:<bay>", each also a
// module of its own, its controller), the same drives as the OS sees them,
// two SATA system disks the OS alone knows, two power supplies iLO 5 numbers
// from 0, six fans inside the chassis, the embedded ports as iLO numbers them
// and as Linux names them.
func syntheticStorageNode() []*store.Object {
	dev := "dev"
	var parts []*store.Object
	n := 0
	add := func(typ, parent, name string, attrs map[string]any) {
		n++
		a := map[string]any{"name": name}
		for k, v := range attrs {
			a[k] = v
		}
		p := parent
		parts = append(parts, &store.Object{ID: fmt.Sprintf("p%02d", n), Type: typ, ParentID: &p, Display: name, Status: "active", Attrs: a})
	}
	for bay := 1; bay <= 5; bay++ {
		serial := fmt.Sprintf("SYNTH-NVME-%d", bay)
		add("module", dev, fmt.Sprintf("DA00000%d", bay-1), map[string]any{"kind": "storage_controller", "serial": serial})
		add("disk", fmt.Sprintf("p%02d", n), fmt.Sprintf("3:%d", bay), map[string]any{"slot": fmt.Sprintf("3:%d", bay), "serial": serial, "media": "nvme", "health": "ok"})
		add("disk", dev, fmt.Sprintf("nvme%dn1", bay-1), map[string]any{"serial": serial, "media": "nvme"})
	}
	add("disk", dev, "sda", map[string]any{"serial": "SYNTH-SATA-1", "media": "ssd"})
	add("disk", dev, "sdb", map[string]any{"serial": "SYNTH-SATA-2", "media": "ssd"})
	add("module", dev, "DA00000A", map[string]any{"kind": "storage_controller"})
	add("psu", dev, "HpeServerPowerSupply 0", map[string]any{"health": "ok"})
	add("psu", dev, "HpeServerPowerSupply 1", map[string]any{"health": "ok"})
	for i := 1; i <= 6; i++ {
		add("fan", dev, fmt.Sprintf("Fan %d", i), map[string]any{"health": "ok"})
	}
	add("interface", dev, "bmc", map[string]any{"kind": "bmc"})
	for i := 1; i <= 4; i++ {
		mac := fmt.Sprintf("02:00:5e:00:00:0%d", i)
		add("interface", dev, fmt.Sprint(i), map[string]any{"kind": "physical", "mac": mac})
		add("interface", dev, fmt.Sprintf("eno%d", i), map[string]any{"kind": "physical", "mac": mac})
	}
	add("interface", dev, "5", map[string]any{"kind": "physical", "mac": "02:00:5e:00:00:50"})
	add("interface", dev, "eno5", map[string]any{"kind": "physical", "mac": "02:00:5e:00:00:50"})
	add("interface", dev, "eno5d1", map[string]any{"kind": "physical", "mac": "02:00:5e:00:00:51"})
	return parts
}

// On the DL380 Gen10 8SFF drawing, the NVMe drives iLO calls 3:N sit in
// Box 3, bay N, with the names the OS gives them; both supplies are lit, bay 1
// for iLO's 0; the fans, inside, are not listed; nothing is counted twice:
// a drive is one bay, its controller module no part of a face, a port one
// spot or one line; the system disks the OS alone knows are listed as such.
func TestPlaceAStorageNodeOnTheDL380Gen10(t *testing.T) {
	for _, slug := range []string{"hpe-proliant-dl380-gen10-8sff", "hpe-proliant-dl380-gen10-8sff-nvme"} {
		faces := libraryFaces(t, slug)
		got := map[string]FacePart{}
		for _, fp := range partsOnFaces("dev", syntheticStorageNode(), faces) {
			name := partNameOf(fp.Part)
			if _, dup := got[name]; dup {
				t.Errorf("%s: %s listed twice", slug, name)
			}
			got[name] = fp
		}
		want := map[string]string{
			"3:1": "front 3:1", "3:2": "front 3:2", "3:3": "front 3:3", "3:4": "front 3:4", "3:5": "front 3:5",
			"nvme0n1": "front 3:1", "nvme4n1": "front 3:5",
			"HpeServerPowerSupply 0": "rear 1", "HpeServerPowerSupply 1": "rear 2",
			"bmc": "rear bmc", "1": "rear nic1", "eno1": "rear nic1", "4": "rear nic4", "eno4": "rear nic4",
			"sda": " os", "sdb": " os", "eno5": " os", "eno5d1": " os",
		}
		for name, w := range want {
			fp, ok := got[name]
			where := fp.Face + " " + fp.Spot
			if fp.Spot == "" {
				where = " " + fp.Unplaced
			}
			if !ok || where != w {
				t.Errorf("%s: %s at %q, want %q", slug, name, where, w)
			}
		}
		if fp := got["eno5"]; len(fp.Also) != 1 || fp.Also[0].Display != "5" {
			t.Errorf("%s: eno5 also %+v, want iLO's port 5 with it", slug, fp.Also)
		}
		for _, name := range []string{"5", "DA000000", "DA00000A", "Fan 1"} {
			if _, listed := got[name]; listed {
				t.Errorf("%s: %s listed on its own", slug, name)
			}
		}
		placed := 0
		for _, fp := range got {
			if fp.Spot != "" {
				placed++
			}
		}
		if len(got) != 25 || placed != 21 {
			t.Errorf("%s: %d parts, %d placed; want 25 and 21", slug, len(got), placed)
		}
	}
}

// A drive in a box the drawing has no cage for is not moved to another box:
// it is unplaced, and the reason says its name points at a place the drawing
// does not have (another chassis variant).
func TestADriveInAnEmptyBoxIsNoSpot(t *testing.T) {
	dev := "dev"
	parts := []*store.Object{{ID: "d", Type: "disk", ParentID: &dev, Display: "2:1", Status: "active", Attrs: map[string]any{"name": "2:1", "slot": "2:1"}}}
	fps := placeParts(dev, parts, libraryFaces(t, "hpe-proliant-dl380-gen10-8sff"))
	if len(fps) != 1 || fps[0].Spot != "" || fps[0].Unplaced != UnplacedNoSpot {
		t.Fatalf("%+v, want unplaced, no_spot", fps)
	}
	fps = placeParts(dev, parts, libraryFaces(t, "hpe-proliant-dl380-gen10-24sff"))
	if len(fps) != 1 || fps[0].Spot != "2:1" || fps[0].Unplaced != "" {
		t.Fatalf("24SFF: %+v, want bay 2:1", fps)
	}
}
