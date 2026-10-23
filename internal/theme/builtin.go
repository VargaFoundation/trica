package theme

import (
	"embed"
	"fmt"
	"sort"
)

// The built-in themes ship inside the binary, so that a deployment with no
// theme archive looks exactly as before and `flat` needs no file at all. They
// are ordinary manifests (no files, no stylesheet), read by the same code as an
// archive; the archives may not take their names.
//
//go:embed builtin/default/theme.json builtin/flat/theme.json
var builtinFS embed.FS

// DefaultName is the theme in force when none is configured.
const DefaultName = "default"

var builtinNames = []string{"default", "flat"}

// BuiltinNames returns the names of the built-in themes, sorted.
func BuiltinNames() []string {
	out := append([]string(nil), builtinNames...)
	sort.Strings(out)
	return out
}

func isBuiltinName(name string) bool {
	for _, n := range builtinNames {
		if n == name {
			return true
		}
	}
	return false
}

// Builtin returns a built-in theme by name.
func Builtin(name string) (*Theme, error) {
	if !isBuiltinName(name) {
		return nil, fmt.Errorf("no built-in theme %q (have %v)", name, BuiltinNames())
	}
	raw, err := builtinFS.ReadFile("builtin/" + name + "/theme.json")
	if err != nil {
		return nil, err
	}
	t, err := build(map[string][]byte{ManifestName: raw}, "builtin:"+name, true)
	if err != nil {
		return nil, fmt.Errorf("built-in theme %s: %w", name, err)
	}
	t.Source = "builtin"
	return t, nil
}
