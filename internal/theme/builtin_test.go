package theme_test

import (
	"testing"

	"github.com/VargaFoundation/trica/internal/theme"
)

func TestBuiltinsAreValid(t *testing.T) {
	for _, name := range theme.BuiltinNames() {
		th, err := theme.Builtin(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if th.Name != name || th.Source != "builtin" || len(th.CSS()) == 0 {
			t.Errorf("%s: got %+v", name, th)
		}
	}
}
