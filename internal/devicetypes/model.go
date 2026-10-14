// Package devicetypes is the device-type library: models of equipment (rack
// height, depth, airflow, weight, port templates) in the file format of the
// netbox-community/devicetype-library, so that upstream files can be dropped in
// unchanged.
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
	// OriginUpstream: a file vendored from netbox-community/devicetype-library
	// (CC0-1.0, see NOTICE).
	OriginUpstream = "netbox-devicetype-library"
	// OriginLocal: a model written by the trica project.
	OriginLocal = "trica"
)

// Model is one validated device model.
type Model struct {
	Slug       string  `json:"slug"`
	Vendor     string  `json:"vendor"`
	Model      string  `json:"model"`
	PartNumber string  `json:"part_number,omitempty"`
	UHeight    float64 `json:"u_height"`
	FullDepth  bool    `json:"full_depth"`
	// Airflow uses the trica schema spelling (front_to_rear).
	Airflow  string  `json:"airflow,omitempty"`
	WeightKg float64 `json:"weight_kg,omitempty"`
	Ports    []Port  `json:"ports,omitempty"`
	// Origin and Path say where the model comes from.
	Origin string `json:"origin"`
	Path   string `json:"path"`
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
	UHeight            *float64  `yaml:"u_height"`
	IsFullDepth        *bool     `yaml:"is_full_depth"`
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
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// airflows maps the upstream spelling to the trica schema enum.
var airflows = map[string]string{
	"front-to-rear": "front_to_rear",
	"rear-to-front": "rear_to_front",
	"left-to-right": "left_to_right",
	"right-to-left": "right_to_left",
	"side-to-rear":  "side_to_rear",
	"passive":       "passive",
	"mixed":         "mixed",
}

// toKg converts a weight; the unit must be one the upstream format defines.
var toKg = map[string]float64{
	"kg": 1,
	"g":  0.001,
	"lb": 0.45359237,
	"oz": 0.028349523125,
}

// Parse decodes and validates one model file. Keys the library does not use
// (comments, images, module bays...) are ignored, so upstream files parse
// unchanged; the keys it does use are strictly checked.
func Parse(data []byte, origin, path string) (*Model, error) {
	var r rawModel
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := &Model{
		Slug: strings.TrimSpace(r.Slug), Vendor: strings.TrimSpace(r.Manufacturer),
		Model: strings.TrimSpace(r.Model), PartNumber: strings.TrimSpace(r.PartNumber),
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
	}
	if r.UHeight == nil || *r.UHeight <= 0 || *r.UHeight > 100 || math.IsNaN(*r.UHeight) {
		return fail("u_height is required and must be in (0, 100]")
	}
	m.UHeight = *r.UHeight
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
