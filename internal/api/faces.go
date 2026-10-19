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
	// Unplaced says why a part has no spot: os (it is known only by the
	// name its operating system gives it, which says nothing of where it
	// sits), no_spot (its name points at a place this model's faces do not
	// have: another chassis variant, a cage the drawing shows empty) or
	// unread (the rules do not read its name). Empty for a placed part.
	Unplaced string `json:"unplaced,omitempty"`
	// Also are the other parts that are the same physical part, seen by
	// another source under another name (the same disk serial number, the
	// same port MAC address), when none of them has a spot; they are not
	// listed on their own. A part that has a spot shares it instead.
	Also []ObjectRef `json:"also,omitempty"`
}

// Why a part is not placed (FacePart.Unplaced).
const (
	UnplacedOS     = "os"
	UnplacedNoSpot = "no_spot"
	UnplacedUnread = "unread"
)

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
	out.Parts = append(out.Parts, partsOnFaces(dev.ID, parts, out.Faces)...)
	return c.json(200, out)
}

// partsOnFaces are the parts placeParts places, of the kinds the faces have
// spots for (a port also when the faces have slots: a card's port goes in
// its slot): the fans of a server whose fans are inside are no line of the
// faces.
func partsOnFaces(device string, parts []*store.Object, faces []DeviceFace) []FacePart {
	kinds := map[string]bool{}
	for _, f := range faces {
		for _, sp := range f.Spots {
			kinds[sp.Kind] = true
		}
	}
	var out []FacePart
	for _, fp := range placeParts(device, parts, faces) {
		if kinds[fp.Kind] || fp.Kind == spotPort && kinds[spotSlot] {
			out = append(out, fp)
		}
	}
	return out
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

	// The names an operating system gives a disk or a port: the kernel's
	// (sda, nvme0n1) and systemd's (eno1, ens1f0, enp3s0f1). They say what a
	// device is to the system, not where it sits in the chassis.
	osDiskName = regexp.MustCompile(`^(?:sd[a-z]+|hd[a-z]+|vd[a-z]+|xvd[a-z]+|nvme\d+n\d+|mmcblk\d+)$`)
	osPortName = regexp.MustCompile(`^(?:en[ospx]|eth|em|ib|wl)[0-9a-z]*$`)
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
		if fp.Spot == "" {
			fp.Unplaced = unplacedWhy(o, name, cands)
		}
		out = append(out, fp)
	}
	out = sameParts(parts, out)
	slices.SortStableFunc(out, func(a, b FacePart) int {
		return strings.Compare(a.Kind, b.Kind)
	})
	return out
}

// unplacedWhy says why a part has no spot (FacePart.Unplaced). cands are the
// spots its name pointed at, its own name first for a port.
func unplacedWhy(o *store.Object, name string, cands []spotKey) string {
	switch {
	case o.Type == "disk" && osDiskName.MatchString(name),
		o.Type == "interface" && osPortName.MatchString(name):
		return UnplacedOS
	case o.Type == "interface" && len(cands) > 1, o.Type != "interface" && len(cands) > 0:
		return UnplacedNoSpot
	}
	return UnplacedUnread
}

// physicalID is what makes two parts one physical part, whoever reports
// them: a disk's serial number, a port's MAC address. Empty when the part has
// none.
func physicalID(o *store.Object) string {
	key := ""
	switch o.Type {
	case "disk":
		key, _ = o.Attrs["serial"].(string)
	case "interface":
		key, _ = o.Attrs["mac"].(string)
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	return o.Type + "\x00" + key
}

// sameParts counts a physical part once. The BMC and the operating system
// report the same disk and the same port under two names (3:1 and nvme0n1;
// 1 and eno1); where one of them has a spot, the others share it, and where
// none has, the others are listed as Also of one of them, the one with a
// name a person reads (eno49d1 rather than 6).
func sameParts(parts []*store.Object, fps []FacePart) []FacePart {
	ids := make(map[string]string, len(parts)) // object id -> physical id
	for _, o := range parts {
		if k := physicalID(o); k != "" {
			ids[o.ID] = k
		}
	}
	placed := map[string]*FacePart{}
	for i := range fps {
		if k := ids[fps[i].Part.ID]; k != "" && fps[i].Spot != "" {
			if _, seen := placed[k]; !seen {
				placed[k] = &fps[i]
			}
		}
	}
	lead := map[string]int{} // physical id -> index in out of the part listed
	out := fps[:0:0]
	for _, fp := range fps {
		k := ids[fp.Part.ID]
		switch {
		case k == "" || fp.Spot != "":
			out = append(out, fp)
		case placed[k] != nil:
			p := placed[k]
			fp.Kind, fp.Face, fp.Spot, fp.Unplaced = p.Kind, p.Face, p.Spot, ""
			out = append(out, fp)
		default:
			i, seen := lead[k]
			if !seen {
				lead[k] = len(out)
				out = append(out, fp)
				continue
			}
			first := out[i]
			if allDigits(partNameOf(first.Part)) && !allDigits(partNameOf(fp.Part)) {
				fp.Also = append([]ObjectRef{refOf(first.Part)}, first.Also...)
				out[i] = fp
			} else {
				out[i].Also = append(out[i].Also, refOf(fp.Part))
			}
		}
	}
	return out
}

func refOf(v ObjectView) ObjectRef {
	return ObjectRef{ID: v.ID, Type: v.Type, Display: v.Display}
}

func partNameOf(v ObjectView) string {
	if n, _ := v.Attributes["name"].(string); n != "" {
		return n
	}
	return v.Display
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
