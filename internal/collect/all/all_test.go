package all_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
	_ "github.com/VargaFoundation/trica/internal/collect/all"
	"github.com/VargaFoundation/trica/internal/schema"
)

// Importing the package is what makes every collector available to a runner.
func TestEveryCollectorIsRegistered(t *testing.T) {
	kinds := collect.Kinds()
	for _, k := range []string{"arista", "kubernetes", "maas", "mikrotik", "pfsense", "redfish", "synology", "unifi"} {
		if !slices.Contains(kinds, k) {
			t.Errorf("%s is not registered (have %v)", k, kinds)
		}
	}
	// The host agent is described but cannot be a target.
	if slices.Contains(kinds, "agent") {
		t.Errorf("agent is listed as a target kind: %v", kinds)
	}
	if _, ok := collect.Lookup("agent"); !ok {
		t.Error("the host agent is not described")
	}
}

// What the API and the UI show is built from the descriptors: each must be
// complete, point at a README that exists and name only types the schema has.
func TestEveryModuleIsDescribed(t *testing.T) {
	reg := schema.Builtin()
	mods := collect.Modules()
	if len(mods) < 7 {
		t.Fatalf("only %d modules", len(mods))
	}
	for _, d := range mods {
		if err := d.Validate(d.Deployment == collect.DeploymentTarget); err != nil {
			t.Errorf("%s: %v", d.Kind, err)
		}
		if _, err := os.Stat(filepath.Join("..", "..", "..", d.Docs)); err != nil {
			t.Errorf("%s: docs %s: %v", d.Kind, d.Docs, err)
		}
		for _, typ := range d.Collects {
			if _, err := reg.Type(typ); err != nil {
				t.Errorf("%s: collects %q: %v", d.Kind, typ, err)
			}
		}
		if !slices.IsSorted(d.Collects) {
			t.Errorf("%s: collects is not sorted: %v", d.Kind, d.Collects)
		}
	}
}

// A package that registers a module but is not imported by all.go would work
// in its own tests and be invisible in the server: this is the forgotten
// import line.
func TestEveryModulePackageIsImported(t *testing.T) {
	src, err := os.ReadFile("all.go")
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range dirs {
		if !e.IsDir() || e.Name() == "all" {
			continue
		}
		if !registersAModule(t, filepath.Join("..", e.Name())) {
			continue
		}
		found++
		want := `"github.com/VargaFoundation/trica/internal/collect/` + e.Name() + `"`
		if !strings.Contains(string(src), want) {
			t.Errorf("internal/collect/%s registers a module but all.go does not import it", e.Name())
		}
	}
	if found < 7 {
		t.Errorf("found only %d module packages: the scan is broken", found)
	}
}

// registersAModule reports whether a non-test file of dir calls collect.Register.
func registersAModule(t *testing.T, dir string) bool {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := false
	for _, e := range files {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Register" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "collect" {
					found = true
				}
			}
			return true
		})
	}
	return found
}
