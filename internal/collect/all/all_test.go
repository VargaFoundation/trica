package all_test

import (
	"slices"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
	_ "github.com/VargaFoundation/trica/internal/collect/all"
)

// Importing the package is what makes every collector available to a runner.
func TestEveryCollectorIsRegistered(t *testing.T) {
	kinds := collect.Kinds()
	for _, k := range []string{"arista", "kubernetes", "maas", "mikrotik", "pfsense", "redfish"} {
		if !slices.Contains(kinds, k) {
			t.Errorf("%s is not registered (have %v)", k, kinds)
		}
	}
}
