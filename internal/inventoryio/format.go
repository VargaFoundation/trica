// Package inventoryio exports the inventory to a JSON or YAML document and
// imports such a document back.
//
// Export reads through internal/store. Import never writes a table: it turns
// the document into an sdk.Batch and submits it to the reconciliation engine
// (ADR 0023), so a file is one more source like any collector.
package inventoryio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// FormatName identifies the document; Version is its schema version.
const (
	FormatName = "trica-inventory"
	Version    = 1
)

// Document is the exchange format. Only objects and relations (with their
// plain attribute values) are read back by import; provenance, status, ids
// and history are informational (see ADR 0023).
type Document struct {
	Format     string     `json:"format" yaml:"format"`
	Version    int        `json:"version" yaml:"version"`
	ExportedAt *time.Time `json:"exported_at,omitempty" yaml:"exported_at,omitempty"`
	// At is set when the document is the state at a past date.
	At        *time.Time  `json:"at,omitempty" yaml:"at,omitempty"`
	Objects   []Object    `json:"objects" yaml:"objects"`
	Relations []Relation  `json:"relations" yaml:"relations"`
	Warnings  []string    `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	Omitted   *OmitReport `json:"omitted,omitempty" yaml:"omitted,omitempty"`
}

// OmitReport says what the export left out on purpose.
type OmitReport struct {
	// SensitiveAttributes counts values never exported.
	SensitiveAttributes int `json:"sensitive_attributes" yaml:"sensitive_attributes"`
}

// Object is one inventory object. Ref is the object's id in the exporting
// inventory; it only has to be unique within the file.
type Object struct {
	Ref    string `json:"ref" yaml:"ref"`
	Type   string `json:"type" yaml:"type"`
	Parent string `json:"parent,omitempty" yaml:"parent,omitempty"`
	// Status is active or absent. Informational: import creates declared objects.
	Status string `json:"status,omitempty" yaml:"status,omitempty"`
	// Identity holds the identifying attributes (a subset of Attributes).
	Identity   map[string]string `json:"identity" yaml:"identity"`
	Attributes map[string]any    `json:"attributes,omitempty" yaml:"attributes,omitempty"`
	// Provenance says which source gave each attribute its current value.
	Provenance map[string]Provenance `json:"provenance,omitempty" yaml:"provenance,omitempty"`
	// History lists every value an attribute ever had (--history).
	History []ValueChange `json:"history,omitempty" yaml:"history,omitempty"`
}

// Provenance is where a value comes from.
type Provenance struct {
	Source     string    `json:"source" yaml:"source"`
	Confidence string    `json:"confidence" yaml:"confidence"`
	Since      time.Time `json:"since" yaml:"since"`
}

// ValueChange is one closed or open range of an attribute.
type ValueChange struct {
	Key        string     `json:"key" yaml:"key"`
	Value      any        `json:"value" yaml:"value"`
	Source     string     `json:"source" yaml:"source"`
	Confidence string     `json:"confidence" yaml:"confidence"`
	From       time.Time  `json:"from" yaml:"from"`
	Until      *time.Time `json:"until,omitempty" yaml:"until,omitempty"`
}

// Relation links two objects of the file by ref.
type Relation struct {
	Type       string         `json:"type" yaml:"type"`
	From       string         `json:"from" yaml:"from"`
	To         string         `json:"to" yaml:"to"`
	Attributes map[string]any `json:"attributes,omitempty" yaml:"attributes,omitempty"`
	Source     string         `json:"source,omitempty" yaml:"source,omitempty"`
	Confidence string         `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Since      *time.Time     `json:"since,omitempty" yaml:"since,omitempty"`
	// Until is set on a relation that was closed (--history only). Import
	// skips closed relations.
	Until *time.Time `json:"until,omitempty" yaml:"until,omitempty"`
}

// Encode writes d as "json" or "yaml".
func (d *Document) Encode(w io.Writer, format string) error {
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	case "yaml", "yml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(d); err != nil {
			return err
		}
		return enc.Close()
	}
	return fmt.Errorf("unknown format %q (use json or yaml)", format)
}

// Decode reads a document in "json" or "yaml" and checks its header. Unknown
// fields are an error: a typo in a hand-written file must not be silently
// ignored.
func Decode(r io.Reader, format string) (*Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var d Document
	switch strings.ToLower(format) {
	case "json":
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			return nil, fmt.Errorf("decode json: %w", err)
		}
	case "yaml", "yml":
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&d); err != nil {
			return nil, fmt.Errorf("decode yaml: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown format %q (use json or yaml)", format)
	}
	if d.Format != FormatName {
		return nil, fmt.Errorf("not a %s document (format %q)", FormatName, d.Format)
	}
	if d.Version != Version {
		return nil, fmt.Errorf("unsupported %s version %d (this build reads version %d)", FormatName, d.Version, Version)
	}
	return &d, nil
}
