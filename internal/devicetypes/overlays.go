package devicetypes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// OverlayDir is the directory of the library, beside the vendors', that holds
// the overlay files (ADR 0056). It holds no model.
const OverlayDir = "overlays"

// An overlay adds part_numbers to upstream models, and renames their
// interface templates, without changing their files. A UniFi controller
// reports "USPM24P" for a USW Pro Max 24 PoE, which the upstream file does
// not know: the overlay lists it under the model's slug, and the device then
// matches by SKU. The UniFi Network collector names a port "Port <idx>"
// (ADR 0060) where upstream names some of them "port.N" or "SFP+ 25":
// interface_names maps an upstream name to the one the collector reports.
// File format:
//
//	manufacturer: Ubiquiti
//	models:
//	  - slug: ubiquiti-u7-pro
//	    part_numbers: [U7PRO]
//	  - slug: ubiquiti-udm
//	    interface_names: {port.1: Port 1, port.2: Port 2}
type overlayEntry struct {
	Slug           string            `yaml:"slug"`
	PartNumbers    []string          `yaml:"part_numbers"`
	InterfaceNames map[string]string `yaml:"interface_names"`
}

type overlayFile struct {
	Manufacturer string         `yaml:"manufacturer"`
	Models       []overlayEntry `yaml:"models"`
}

// applyOverlays reads the .yaml files of root/overlays (none: nothing to do)
// and appends their part numbers to the models and renames their interface
// templates. Every mistake fails the
// load, as for a model file: an unknown key, an unknown slug, another
// manufacturer, a model that is not an upstream one (a project model lists
// its part_numbers itself), an empty entry, a rename of an interface the model does not have, to an
// empty name, to itself or onto a name another interface keeps, a shortname listed twice for a
// model, or a shortname another model of the vendor already lists (two models
// with one SKU match neither).
func applyOverlays(fsys fs.FS, root string, models []*Model) error {
	dir := path.Join(root, OverlayDir)
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	bySlug := map[string]*Model{}
	// owner maps vendor and normalised part number to the slug that lists it.
	owner := map[string]string{}
	for _, m := range models {
		bySlug[m.Slug] = m
		for _, pn := range m.PartNumbers {
			owner[key(m.Vendor, norm(pn))] = m.Slug
		}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
			continue
		}
		p := path.Join(dir, name)
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		var o overlayFile
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&o); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("%s: %w", p, err)
		}
		for i, entry := range o.Models {
			slug := strings.TrimSpace(entry.Slug)
			m := bySlug[slug]
			switch {
			case m == nil:
				return fmt.Errorf("%s: entry %d: no model has the slug %q", p, i+1, slug)
			case m.Vendor != strings.TrimSpace(o.Manufacturer):
				return fmt.Errorf("%s: %s is a %s model, the overlay is for %q", p, slug, m.Vendor, o.Manufacturer)
			case m.Origin != OriginUpstream:
				return fmt.Errorf("%s: %s is not an upstream model: a project model lists its own part_numbers", p, slug)
			case len(entry.PartNumbers) == 0 && len(entry.InterfaceNames) == 0:
				return fmt.Errorf("%s: %s lists no part number and renames no interface", p, slug)
			}
			for _, pn := range entry.PartNumbers {
				pn = strings.TrimSpace(pn)
				k := key(m.Vendor, norm(pn))
				if norm(pn) == "" {
					return fmt.Errorf("%s: %s has an empty part number", p, slug)
				}
				if prev, dup := owner[k]; dup {
					return fmt.Errorf("%s: %s: part number %q is already listed by %s", p, slug, pn, prev)
				}
				owner[k] = slug
				m.PartNumbers = append(m.PartNumbers, pn)
			}
			if err := renameInterfaces(m, entry.InterfaceNames); err != nil {
				return fmt.Errorf("%s: %s: %w", p, slug, err)
			}
		}
	}
	return nil
}

// renameInterfaces renames the interface templates of m, old name to new,
// all at once from the original names (a swap is allowed). Only interfaces
// are touched: a power port with the same name stays as it is. Type and
// mgmt_only are kept, and so is the label; an interface that upstream does not
// label gets its old name as label, so that "Port 1 (PoE+)" renamed "Port 1"
// still says what upstream said of it (#177).
func renameInterfaces(m *Model, names map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, p := range m.Ports {
		if p.Group == "interface" {
			have[p.Name] = true
		}
	}
	for _, old := range slices.Sorted(maps.Keys(names)) {
		nw := strings.TrimSpace(names[old])
		switch {
		case !have[old]:
			return fmt.Errorf("no interface is named %q", old)
		case nw == "":
			return fmt.Errorf("renames %q to nothing", old)
		case nw == old:
			return fmt.Errorf("renames %q to itself", old)
		}
	}
	ports := slices.Clone(m.Ports)
	seen := map[string]bool{}
	for i, p := range ports {
		if p.Group != "interface" {
			continue
		}
		if nw, ok := names[p.Name]; ok {
			if ports[i].Label == "" {
				ports[i].Label = p.Name
			}
			ports[i].Name = strings.TrimSpace(nw)
		}
		if seen[ports[i].Name] {
			return fmt.Errorf("interface %q would be named twice", ports[i].Name)
		}
		seen[ports[i].Name] = true
	}
	m.Ports = ports
	return nil
}
