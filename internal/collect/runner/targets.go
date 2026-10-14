package runner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
)

// file is the targets file: a list of collect.Target. Credentials in it are
// references (file: or env:), never values.
type file struct {
	Targets []collect.Target `yaml:"targets"`
}

// LoadTargets reads a targets file. Unknown keys are an error (a typo must not
// silently do nothing), and so are a missing kind or name, a duplicate source
// and a password that is not a file: or env: reference.
func LoadTargets(path string) ([]collect.Target, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator names the targets file
	if err != nil {
		return nil, fmt.Errorf("targets: %w", err)
	}
	return ParseTargets(raw, path)
}

// ParseTargets decodes the content of a targets file; name is used in errors.
func ParseTargets(raw []byte, name string) ([]collect.Target, error) {
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("targets %s: %w", name, err)
	}
	seen := map[string]bool{}
	for i, t := range f.Targets {
		if t.Kind == "" || t.Name == "" {
			return nil, fmt.Errorf("targets %s: target %d needs a kind and a name", name, i+1)
		}
		if seen[t.Source()] {
			return nil, fmt.Errorf("targets %s: %s is declared twice", name, t.Source())
		}
		seen[t.Source()] = true
		if t.Password != "" && t.Password.String() == "[redacted]" {
			// The value is never echoed: it may be a pasted password.
			return nil, fmt.Errorf("targets %s: %s: password must be a reference (file:/path or env:NAME), not a value", name, t.Source())
		}
	}
	return f.Targets, nil
}
