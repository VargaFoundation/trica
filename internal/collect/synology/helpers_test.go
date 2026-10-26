package synology

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const nas = "nas"

func newFake(t *testing.T, set string) *FakeDSM {
	t.Helper()
	f, err := NewFake(set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f
}

func newCollector(t *testing.T, tg collect.Target) sdk.Collector {
	t.Helper()
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// runOne runs the collector against f and returns the single batch it emits.
// Every run is checked against the schema, for allow-listed POSTs only, and
// for a closed session.
func runOne(t *testing.T, f *FakeDSM) *sdk.Batch {
	t.Helper()
	return runTarget(t, f, f.Target(nas))
}

func runTarget(t *testing.T, f *FakeDSM, tg collect.Target) *sdk.Batch {
	t.Helper()
	bs := collecttest.Run(t, newCollector(t, tg))
	if len(bs) != 1 {
		t.Fatalf("got %d batches, want 1", len(bs))
	}
	if bs[0].Source != "synology:"+tg.Name {
		t.Fatalf("source %q", bs[0].Source)
	}
	if err := f.OnlyAllowed(); err != nil {
		t.Fatal(err)
	}
	if n := f.OpenSessions(); n != 0 {
		t.Fatalf("%d sessions left open: the collector must log out", n)
	}
	return bs[0]
}

func obj(t *testing.T, b *sdk.Batch, typ, key, value string) *sdk.Object {
	t.Helper()
	o := collecttest.Find([]*sdk.Batch{b}, typ, key, value)
	if o == nil {
		t.Fatalf("no %s with %s=%q", typ, key, value)
	}
	return o
}

func attrs(t *testing.T, o *sdk.Object, want map[string]any) {
	t.Helper()
	for k, w := range want {
		if got, ok := o.Attributes[k]; !ok || !equal(got, w) {
			t.Errorf("%s %v: attribute %s = %v (present %v), want %v", o.Type, o.Identity, k, got, ok, w)
		}
	}
}

func absent(t *testing.T, o *sdk.Object, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := o.Attributes[k]; ok {
			t.Errorf("%s %v: attribute %s = %v, want none", o.Type, o.Identity, k, v)
		}
	}
}

func equal(a, b any) bool {
	if sa, ok := a.([]string); ok {
		sb, _ := b.([]string)
		return slices.Equal(sa, sb)
	}
	return a == b || (isNumber(a) && isNumber(b) && fmt.Sprint(a) == fmt.Sprint(b))
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int32, int64, float64:
		return true
	}
	return false
}

func count(b *sdk.Batch, typ string) int { return len(collecttest.Objects([]*sdk.Batch{b}, typ)) }

func relCount(b *sdk.Batch, typ string) int { return len(collecttest.Relations([]*sdk.Batch{b}, typ)) }

func hasRel(b *sdk.Batch, typ, from, to string) bool {
	for _, r := range b.Relations {
		if r.Type == typ && r.From.Ref == from && r.To.Ref == to {
			return true
		}
	}
	return false
}

func hasWarning(b *sdk.Batch, sub string) bool {
	for _, w := range b.Health.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func optionsNode(t *testing.T, doc string) yaml.Node {
	t.Helper()
	var w struct {
		Options yaml.Node `yaml:"options"`
	}
	indented := "options:\n  " + strings.ReplaceAll(strings.TrimRight(doc, "\n"), "\n", "\n  ") + "\n"
	if err := yaml.Unmarshal([]byte(indented), &w); err != nil {
		t.Fatal(err)
	}
	return w.Options
}

func withOptions(t *testing.T, tg collect.Target, doc string) collect.Target {
	t.Helper()
	tg.Options = optionsNode(t, doc)
	return tg
}

func collectTarget(addr string) collect.Target {
	return collect.Target{Kind: Kind, Name: nas, Address: addr, Username: Account, Password: "file:/nonexistent"}
}

func unmarshalJSON(s string, v any) error { return json.Unmarshal([]byte(s), v) }
