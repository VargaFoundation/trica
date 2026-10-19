package devicetypes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// LayoutDir holds where the parts sit on a face, beside its image:
// layouts/<manufacturer>/<slug>.<face>.json (ADR 0047). The project's are
// written by scripts/devicetype-faces with its drawings; an operator can add
// one for their own photograph.
const LayoutDir = "layouts"

// MaxLayoutBytes bounds one layout file: a 64-port switch takes 5 KB.
const MaxLayoutBytes = 256 << 10

// maxSpots bounds the spots of one face.
const maxSpots = 512

// Spot kinds: the parts a collector reports that have a place on a face.
const (
	SpotBay  = "bay"
	SpotPSU  = "psu"
	SpotFan  = "fan"
	SpotPort = "port"
	SpotSlot = "slot"
)

var spotKinds = map[string]bool{SpotBay: true, SpotPSU: true, SpotFan: true, SpotPort: true, SpotSlot: true}

// Spot is the place of one part on a face, in the layout's units, the origin
// at the top left of the image.
type Spot struct {
	// Kind is bay, psu, fan, port or slot.
	Kind string `json:"kind"`
	// Name is what the part is called on the face: the bay number ("3", or
	// "<box>:<bay>" when the vendor numbers bays per box), the power supply,
	// fan or slot number, the port's name on the device's own system
	// ("Ethernet12", "ether3") or, on a server, "bmc", "nic<n>" for the
	// embedded ports and "flexlom<n>".
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

// Layout is where the parts sit on one face image. WidthMM and HeightMM are
// the size of the whole image in the units of the spots (millimetres for the
// project's drawings; any unit for an operator's photograph, as long as the
// spots use the same).
type Layout struct {
	// Path is the file inside its source (layouts/HPE/x.front.json).
	Path     string  `json:"path"`
	Origin   string  `json:"origin"`
	WidthMM  float64 `json:"width_mm"`
	HeightMM float64 `json:"height_mm"`
	Spots    []Spot  `json:"spots"`
}

// layoutPath is where the layout of a face of m is.
func layoutPath(m *Model, face string) string {
	return path.Join(LayoutDir, m.Vendor, m.Slug+"."+face+".json")
}

// readLayout reads the layout of one face from fsys under root, if any.
func readLayout(fsys fs.FS, root string, m *Model, face, origin string) (*Layout, error) {
	if fsys == nil {
		return nil, nil
	}
	p := path.Join(root, layoutPath(m, face))
	data, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, nil
	}
	rel := strings.TrimPrefix(p, root+"/")
	l, err := parseLayout(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	l.Path, l.Origin = rel, origin
	return l, nil
}

// parseLayout decodes and checks a layout: known fields only, a positive
// size, every spot of a known kind, named, inside the image, and no two spots
// of one kind with the same name (a part would not know where it goes).
func parseLayout(data []byte) (*Layout, error) {
	if len(data) > MaxLayoutBytes {
		return nil, fmt.Errorf("%d bytes, more than the %d a layout may weigh", len(data), MaxLayoutBytes)
	}
	var l Layout
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("not a layout: %w", err)
	}
	if l.Path != "" || l.Origin != "" {
		return nil, fmt.Errorf("path and origin are set by the library, not the file")
	}
	if !(l.WidthMM > 0) || !(l.HeightMM > 0) {
		return nil, fmt.Errorf("width_mm and height_mm must be positive")
	}
	if len(l.Spots) == 0 || len(l.Spots) > maxSpots {
		return nil, fmt.Errorf("%d spots, want 1 to %d", len(l.Spots), maxSpots)
	}
	// A rounding margin: the generator rounds to 0.01 mm.
	const eps = 0.01
	seen := map[string]bool{}
	for i, s := range l.Spots {
		switch {
		case !spotKinds[s.Kind]:
			return nil, fmt.Errorf("spot %d: kind %q is not bay, psu, fan, port or slot", i, s.Kind)
		case s.Name == "" || len(s.Name) > 64:
			return nil, fmt.Errorf("spot %d: the name must have 1 to 64 bytes", i)
		case !(s.W > 0) || !(s.H > 0) || s.X < 0 || s.Y < 0 || s.X+s.W > l.WidthMM+eps || s.Y+s.H > l.HeightMM+eps:
			return nil, fmt.Errorf("spot %s %s: %gx%g at %g,%g is not inside the %gx%g image", s.Kind, s.Name, s.W, s.H, s.X, s.Y, l.WidthMM, l.HeightMM)
		case seen[s.Kind+"\x00"+s.Name]:
			return nil, fmt.Errorf("spot %s %s is twice in the layout", s.Kind, s.Name)
		}
		seen[s.Kind+"\x00"+s.Name] = true
	}
	return &l, nil
}

func (m *Model) setLayout(face string, l *Layout) {
	if face == Front {
		m.FrontLayout = l
	} else {
		m.RearLayout = l
	}
}

// Layout returns where the parts sit on the image of a face, or nil. A
// layout is only ever kept with the image it was made for.
func (m *Model) Layout(face string) *Layout {
	switch face {
	case Front:
		return m.FrontLayout
	case Rear:
		return m.RearLayout
	}
	return nil
}
