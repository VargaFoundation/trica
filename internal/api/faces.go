package api

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/internal/store"
)

// FaceSpot is the place of one part on a face: a bay, a power supply, a
// fan, a port or a PCIe slot, named as the device numbers it (ADR 0047).
type FaceSpot struct {
	// Kind is bay, psu, fan, port or slot.
	Kind string  `json:"kind"`
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

// DeviceFace is one face of a device's model: its image and, when the library
// has its layout, the places of the parts on it.
type DeviceFace struct {
	// Face is front or rear.
	Face string `json:"face"`
	// Image is the API path of the face's image.
	Image string `json:"image"`
	// Width and Height are the size of the image in the units of the spots;
	// 0 when the face has no layout.
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	// LayoutOrigin is trica for the project's drawings, operator for a
	// layout from devicetypes.extra_dir, empty without a layout.
	LayoutOrigin string     `json:"layout_origin,omitempty"`
	Spots        []FaceSpot `json:"spots"`
}

// FacePart is a part of the device with its place on a face, or none.
type FacePart struct {
	Part ObjectView `json:"part"`
	// Kind is the kind of spot the part goes in.
	Kind string `json:"kind"`
	// Face and Spot name where it sits; both are empty when its name does
	// not say which spot it is.
	Face string `json:"face,omitempty"`
	Spot string `json:"spot,omitempty"`
}

// DeviceFaces is the device drawn with its parts in place.
type DeviceFaces struct {
	// Model is the device's model, when it has one the caller may read.
	Model     *ObjectRef `json:"model,omitempty"`
	ModelName string     `json:"model_name,omitempty"`
	// Faces are the faces of the model that have an image, front first.
	Faces []DeviceFace `json:"faces"`
	// Parts are the device's parts the caller may read, of the kinds the
	// faces have spots for, placed or not; absent parts are left out.
	Parts []FacePart `json:"parts"`
	// Truncated says the device has more parts than were looked at.
	Truncated bool `json:"truncated"`
}

// maxFaceParts bounds the parts read below one device: a 64-port switch with
// its optics, VLANs and sensors stays well under it.
const maxFaceParts = 2000

// hObjectFaces answers what the object page draws for a device: the images
// of its model's faces, the places the library knows on them, and the
// device's parts with the place each one goes in.
func (s *Server) hObjectFaces(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	dev, site, err := c.loadObject(id)
	if err != nil {
		return err
	}
	out := DeviceFaces{Faces: []DeviceFace{}, Parts: []FacePart{}}
	rels, err := s.st.CurrentRelationsFrom(c.ctx(), "instance_of", []string{dev.ID})
	if err != nil {
		return err
	}
	rel, has := modelRelations(rels)[dev.ID]
	if !has || s.images == nil {
		return c.json(200, out)
	}
	models, _, err := c.visibleObjects([]string{rel.ToID})
	if err != nil {
		return err
	}
	m, ok := models[rel.ToID]
	if !ok {
		return c.json(200, out)
	}
	attrs := redactMap(m.Attrs)
	out.Model = &ObjectRef{ID: m.ID, Type: m.Type, Display: m.Display}
	out.ModelName, _ = attrs["model"].(string)
	slug, _ := attrs["slug"].(string)
	if lm := s.libraryModel(slug); lm != nil && out.ModelName == "" {
		out.ModelName = lm.Model // a model just chosen, not described yet
	}
	for _, face := range []string{"front", "rear"} {
		img, ok := s.images.DeviceTypeImage(slug, face)
		if slug == "" || !ok {
			continue
		}
		f := DeviceFace{Face: face, Image: Prefix + imagePath(slug, face), Spots: []FaceSpot{}}
		if l := img.Layout; l != nil {
			f.Width, f.Height, f.LayoutOrigin, f.Spots = l.Width, l.Height, l.Origin, l.Spots
		}
		out.Faces = append(out.Faces, f)
	}
	kinds := map[string]bool{}
	for _, f := range out.Faces {
		for _, sp := range f.Spots {
			kinds[sp.Kind] = true
		}
	}
	if len(kinds) == 0 {
		return c.json(200, out)
	}
	objs, truncated, err := s.st.Descendants(c.ctx(), dev.ID, partDepth, maxFaceParts)
	if err != nil {
		return err
	}
	out.Truncated = truncated
	var parts []*store.Object
	for _, o := range objs {
		if o.Status != "absent" && c.s.auth.Allow(c.p, ActionRead, o.Type, site) {
			parts = append(parts, o)
		}
	}
	for _, fp := range placeParts(dev.ID, parts, out.Faces) {
		if kinds[fp.Kind] || fp.Kind == spotPort && kinds[spotSlot] {
			out.Parts = append(out.Parts, fp)
		}
	}
	return c.json(200, out)
}

const (
	spotBay  = "bay"
	spotPSU  = "psu"
	spotFan  = "fan"
	spotPort = "port"
	spotSlot = "slot"
)

var (
	firstNumber = regexp.MustCompile(`\d+`)
	lastNumber  = regexp.MustCompile(`(\d+)\D*$`)

	// The bay of a disk, from its slot or its name, as the BMCs write it.
	diskBoxBay = []*regexp.Regexp{
		regexp.MustCompile(`Box=(\d+):Bay=(\d+)`),   // iLO 5: Port=1I:Box=3:Bay=5
		regexp.MustCompile(`^\d+[IE]:(\d+):(\d+)$`), // iLO 4: 1I:1:3 (port, box, bay)
		regexp.MustCompile(`^(\d+):(\d+)$`),         // 3:5 (box, bay)
	}
	diskBay = []*regexp.Regexp{
		regexp.MustCompile(`Disk (?:\d+):(?:\d+):(\d+)$`), // iDRAC: Solid State Disk 0:1:2 (bay last)
		regexp.MustCompile(`Disk\.Bay\.(\d+)`),            // iDRAC FQDD
		regexp.MustCompile(`^Bay (\d+)$`),
	}

	// The ports of a server, by the names an OS or a BMC gives them.
	embeddedNIC = []*regexp.Regexp{
		regexp.MustCompile(`^eno(\d+)$`),                  // systemd: on-board port n
		regexp.MustCompile(`^NIC\.Embedded\.(\d+)-`),      // iDRAC
		regexp.MustCompile(`^NIC\.Integrated\.\d+-(\d+)`), // iDRAC
	}
	slotNIC = []*regexp.Regexp{
		regexp.MustCompile(`^ens(\d+)(?:d\d+)?$`), // systemd: a card in PCIe slot n
		regexp.MustCompile(`^NIC\.Slot\.(\d+)-`),  // iDRAC
	}
	trailingLane = regexp.MustCompile(`[/-]\d+$`)
)

type spotKey struct{ kind, name string }

// placeParts gives each part the spot it goes in, from its type, its name
// and the spots of the faces. The rules read the names the collectors write
// (Redfish, the agent, the switches); a name they do not read leaves the part
// unplaced rather than guessed.
func placeParts(device string, parts []*store.Object, faces []DeviceFace) []FacePart {
	where := map[spotKey]string{}
	for _, f := range faces {
		for _, sp := range f.Spots {
			k := spotKey{sp.Kind, sp.Name}
			if _, dup := where[k]; !dup {
				where[k] = f.Face
			}
		}
	}
	// iLO 5 numbers power supplies from 0, the chassis from 1.
	psuShift := 0
	for _, o := range parts {
		if o.Type == "psu" {
			if n, ok := numberIn(lastNumber, partName(o)); ok && n == 0 {
				psuShift = 1
			}
		}
	}
	var out []FacePart
	for _, o := range parts {
		var kind string
		var cands []spotKey
		name := partName(o)
		switch o.Type {
		case "disk":
			kind = spotBay
			cands = diskSpots(o)
		case "psu":
			kind = spotPSU
			if n, ok := numberIn(lastNumber, name); ok {
				cands = []spotKey{{spotPSU, strconv.Itoa(n + psuShift)}}
			}
		case "fan":
			// A fan in a power supply or a module turns with it.
			if o.ParentID == nil || *o.ParentID != device {
				continue
			}
			kind = spotFan
			if n, ok := numberIn(firstNumber, name); ok {
				cands = []spotKey{{spotFan, strconv.Itoa(n)}}
			}
		case "interface":
			k, _ := o.Attrs["kind"].(string)
			if k != "" && k != "physical" && k != "bmc" {
				continue
			}
			kind = spotPort
			cands = interfaceSpots(name, k)
		default:
			continue
		}
		fp := FacePart{Part: objectView(o), Kind: kind}
		for _, c := range cands {
			if face, ok := where[c]; ok {
				fp.Kind, fp.Face, fp.Spot = c.kind, face, c.name
				break
			}
		}
		out = append(out, fp)
	}
	slices.SortStableFunc(out, func(a, b FacePart) int {
		return strings.Compare(a.Kind, b.Kind)
	})
	return out
}

func partName(o *store.Object) string {
	if n, _ := o.Attrs["name"].(string); n != "" {
		return n
	}
	return o.Display
}

func numberIn(re *regexp.Regexp, s string) (int, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[len(m)-1])
	return n, err == nil
}

// diskSpots are the bays a disk may be in, from its slot then its name. An
// HPE bay is named by its box ("1:3"), a Dell bay by its number.
func diskSpots(o *store.Object) []spotKey {
	var out []spotKey
	slot, _ := o.Attrs["slot"].(string)
	for _, s := range []string{slot, partName(o)} {
		if s == "" {
			continue
		}
		for _, re := range diskBoxBay {
			if m := re.FindStringSubmatch(s); m != nil {
				out = append(out, spotKey{spotBay, trimZeros(m[1]) + ":" + trimZeros(m[2])})
			}
		}
		for _, re := range diskBay {
			if m := re.FindStringSubmatch(s); m != nil {
				out = append(out, spotKey{spotBay, trimZeros(m[1])})
			}
		}
	}
	return out
}

func trimZeros(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil {
		return s
	}
	return strconv.Itoa(n)
}

// interfaceSpots are the places an interface may be: the port of its own
// name first (a switch names its ports as printed), the port without its
// breakout lane (Ethernet49/1, qsfpplus1-2), then a server's on-board ports
// and cards.
func interfaceSpots(name, kind string) []spotKey {
	out := []spotKey{{spotPort, name}}
	if t := trailingLane.ReplaceAllString(name, ""); t != name {
		out = append(out, spotKey{spotPort, t})
	}
	if kind == "bmc" {
		out = append(out, spotKey{spotPort, "bmc"})
	}
	for _, re := range embeddedNIC {
		if m := re.FindStringSubmatch(name); m != nil {
			out = append(out, spotKey{spotPort, "nic" + trimZeros(m[1])})
		}
	}
	for _, re := range slotNIC {
		if m := re.FindStringSubmatch(name); m != nil {
			out = append(out, spotKey{spotSlot, trimZeros(m[1])})
		}
	}
	return out
}
