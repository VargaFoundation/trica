// Package devicetypes is the device-type library: models of equipment (rack
// height, depth, airflow, weight, port templates) in trica's model-file format
// (docs/reference/devicetypes.md).
//
// The library only ever speaks through the reconciliation engine: Batch turns
// the models that match known devices into a declared sdk.Batch (device_type
// objects and instance_of relations). Being declared, a library value never
// overrides an observed one (ADR 0020).
package devicetypes

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Origin of a model.
const (
	// OriginTrica: a model, image or layout of the library embedded in the
	// binary.
	OriginTrica = "trica"
	// OriginOperator: a model or an image from the operator's directory
	// (devicetypes.extra_dir), read at start, not embedded.
	OriginOperator = "operator"
)

// Model is one validated device model.
type Model struct {
	Slug       string `json:"slug"`
	Vendor     string `json:"vendor"`
	Model      string `json:"model"`
	PartNumber string `json:"part_number,omitempty"`
	// PartNumbers are the vendor part numbers (SKUs) of this exact variant
	// of a chassis, a key of the format: a device whose
	// part number is one of them matches this model before any name does,
	// so that "ProLiant DL360 Gen9" + 755258-B21 picks the 8SFF chassis.
	PartNumbers []string `json:"part_numbers,omitempty"`
	// VariantOf is the slug of the generic model this one is a chassis
	// variant of (ADR 0048): the models that share it, and
	// the generic model itself, are what a person chooses among when the
	// device does not say which chassis it is. Without it the variants are
	// found by name (see Index.Choices).
	VariantOf string `json:"variant_of,omitempty"`
	// UHeight is the rack height in U. 0 means the model occupies no rack
	// unit (a desktop NAS, a wall access point, a vertical PDU), as in the
	// rack-elevation convention (ADR 0056).
	UHeight   float64 `json:"u_height"`
	FullDepth bool    `json:"full_depth"`
	// HeightMM is the physical height of the chassis, when the model file
	// says it (height_mm). A model that occupies no rack unit uses it to size
	// the shelf it stands on in a rack elevation (ADR 0068).
	HeightMM float64 `json:"height_mm,omitempty"`
	// Airflow uses the trica schema spelling (front_to_rear).
	Airflow  string  `json:"airflow,omitempty"`
	WeightKg float64 `json:"weight_kg,omitempty"`
	Ports    []Port  `json:"ports,omitempty"`
	// Origin and Path say where the model comes from.
	Origin string `json:"origin"`
	Path   string `json:"path"`
	// FrontImage and RearImage are the face illustrations, when the library
	// or the operator's directory has one (see Open).
	FrontImage *Image `json:"front_image,omitempty"`
	RearImage  *Image `json:"rear_image,omitempty"`
	// FrontLayout and RearLayout say where the parts sit on those images
	// (ADR 0047), when the library or the operator's directory has it.
	FrontLayout *Layout `json:"-"`
	RearLayout  *Layout `json:"-"`
}

func (m *Model) setImage(face string, img *Image) {
	if face == Front {
		m.FrontImage = img
	} else {
		m.RearImage = img
	}
}

// Image returns the illustration of a face (Front or Rear), or nil.
func (m *Model) Image(face string) *Image {
	switch face {
	case Front:
		return m.FrontImage
	case Rear:
		return m.RearImage
	}
	return nil
}

// Port is one port template. Group is interface, console, console_server,
// power, power_outlet, front or rear.
type Port struct {
	Group    string `json:"group"`
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Label    string `json:"label,omitempty"`
	MgmtOnly bool   `json:"mgmt_only,omitempty"`
	// MaxDrawW is the maximum draw of a power port, in watts.
	MaxDrawW int `json:"max_draw_w,omitempty"`
}

type rawPort struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Label    string   `yaml:"label"`
	MgmtOnly bool     `yaml:"mgmt_only"`
	MaxDraw  *float64 `yaml:"maximum_draw"`
}

type rawModel struct {
	Manufacturer       string    `yaml:"manufacturer"`
	Model              string    `yaml:"model"`
	Slug               string    `yaml:"slug"`
	PartNumber         string    `yaml:"part_number"`
	PartNumbers        []string  `yaml:"part_numbers"`
	VariantOf          string    `yaml:"variant_of"`
	UHeight            *float64  `yaml:"u_height"`
	IsFullDepth        *bool     `yaml:"is_full_depth"`
	HeightMM           *float64  `yaml:"height_mm"`
	Airflow            string    `yaml:"airflow"`
	Weight             *float64  `yaml:"weight"`
	WeightUnit         string    `yaml:"weight_unit"`
	Interfaces         []rawPort `yaml:"interfaces"`
	ConsolePorts       []rawPort `yaml:"console-ports"`
	ConsoleServerPorts []rawPort `yaml:"console-server-ports"`
	PowerPorts         []rawPort `yaml:"power-ports"`
	PowerOutlets       []rawPort `yaml:"power-outlets"`
	FrontPorts         []rawPort `yaml:"front-ports"`
	RearPorts          []rawPort `yaml:"rear-ports"`
	// FrontImage and RearImage are booleans in a model file: the image exists at
	// elevation-images/<manufacturer>/<slug>.<face>.<ext>. They are decoded so
	// that a malformed value fails, and otherwise not trusted: Open looks for
	// the file whatever they say.
	FrontImage any `yaml:"front_image"`
	RearImage  any `yaml:"rear_image"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// airflows maps the model-file spelling to the trica schema enum.
var airflows = map[string]string{
	"front-to-rear": "front_to_rear",
	"rear-to-front": "rear_to_front",
	"left-to-right": "left_to_right",
	"right-to-left": "right_to_left",
	"side-to-rear":  "side_to_rear",
	"passive":       "passive",
	"mixed":         "mixed",
}

// toKg converts a weight; the unit must be one the format defines.
var toKg = map[string]float64{
	"kg": 1,
	"g":  0.001,
	"lb": 0.45359237,
	"oz": 0.028349523125,
}

// Parse decodes and validates one model file. Keys the library does not use
// (comments, module bays...) are ignored; the keys it does use are strictly checked.
func Parse(data []byte, origin, path string) (*Model, error) {
	var r rawModel
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := &Model{
		Slug: strings.TrimSpace(r.Slug), Vendor: strings.TrimSpace(r.Manufacturer),
		Model: strings.TrimSpace(r.Model), PartNumber: strings.TrimSpace(r.PartNumber), VariantOf: strings.TrimSpace(r.VariantOf),
		Origin: origin, Path: path,
	}
	fail := func(format string, a ...any) (*Model, error) {
		return nil, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, a...))
	}
	switch {
	case m.Vendor == "":
		return fail("manufacturer is required")
	case m.Model == "":
		return fail("model is required")
	case !slugRe.MatchString(m.Slug):
		return fail("slug %q must be lowercase letters, digits, dots, dashes", m.Slug)
	case m.VariantOf != "" && !slugRe.MatchString(m.VariantOf):
		return fail("variant_of %q must be the slug of another model", m.VariantOf)
	case m.VariantOf == m.Slug:
		return fail("variant_of names the model itself")
	}
	if r.UHeight == nil || math.IsNaN(*r.UHeight) || *r.UHeight < 0 || *r.UHeight > 100 {
		return fail("u_height is required and must be in [0, 100] (0: occupies no rack unit)")
	}
	for k, v := range map[string]any{"front_image": r.FrontImage, "rear_image": r.RearImage} {
		if _, ok := v.(bool); v != nil && !ok {
			return fail("%s must be true or false (the file is found by its name, see the library README)", k)
		}
	}
	seenPN := map[string]bool{}
	for i, pn := range r.PartNumbers {
		pn = strings.TrimSpace(pn)
		if pn == "" {
			return fail("part_numbers entry %d is empty", i+1)
		}
		if seenPN[pn] {
			return fail("part_numbers lists %q twice", pn)
		}
		seenPN[pn] = true
		m.PartNumbers = append(m.PartNumbers, pn)
	}
	m.UHeight = *r.UHeight
	if r.HeightMM != nil {
		if math.IsNaN(*r.HeightMM) || *r.HeightMM < 0 || *r.HeightMM > 5000 {
			return fail("height_mm must be in [0, 5000]")
		}
		m.HeightMM = *r.HeightMM
	}
	m.FullDepth = r.IsFullDepth != nil && *r.IsFullDepth
	if r.Airflow != "" {
		a, ok := airflows[r.Airflow]
		if !ok {
			return fail("unknown airflow %q", r.Airflow)
		}
		m.Airflow = a
	}
	if r.Weight != nil {
		unit := r.WeightUnit
		if unit == "" {
			unit = "kg"
		}
		f, ok := toKg[unit]
		if !ok {
			return fail("unknown weight_unit %q", r.WeightUnit)
		}
		if *r.Weight < 0 {
			return fail("negative weight")
		}
		m.WeightKg = math.Round(*r.Weight*f*1000) / 1000
	}
	groups := []struct {
		name  string
		ports []rawPort
	}{
		{"interface", r.Interfaces}, {"console", r.ConsolePorts}, {"console_server", r.ConsoleServerPorts},
		{"power", r.PowerPorts}, {"power_outlet", r.PowerOutlets}, {"front", r.FrontPorts}, {"rear", r.RearPorts},
	}
	for _, g := range groups {
		seen := map[string]bool{}
		for i, p := range g.ports {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				return fail("%s port %d has no name", g.name, i+1)
			}
			if seen[name] {
				return fail("duplicate %s port %q", g.name, name)
			}
			seen[name] = true
			port := Port{Group: g.name, Name: name, Type: p.Type, Label: p.Label, MgmtOnly: p.MgmtOnly}
			if p.MaxDraw != nil {
				if *p.MaxDraw < 0 {
					return fail("negative maximum_draw on %q", name)
				}
				port.MaxDrawW = int(math.Round(*p.MaxDraw))
			}
			m.Ports = append(m.Ports, port)
		}
	}
	return m, nil
}

// Racked reports whether the model occupies rack units (u_height above 0).
func (m *Model) Racked() bool { return m.UHeight > 0 }

// PSUCount is the number of power ports (power supply slots).
func (m *Model) PSUCount() int {
	n := 0
	for _, p := range m.Ports {
		if p.Group == "power" {
			n++
		}
	}
	return n
}
