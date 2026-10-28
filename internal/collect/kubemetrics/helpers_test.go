package kubemetrics

import (
	"context"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const tgt = "prod"

func newFake(t *testing.T) *FakeKubeMetrics {
	t.Helper()
	f, err := NewFake(SetRecorded)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f
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

// target is the fake's target for cluster "dev" with the given extra options.
func target(t *testing.T, f *FakeKubeMetrics, extra string) collect.Target {
	t.Helper()
	doc := "cluster: dev"
	if extra != "" {
		doc += "\n" + extra
	}
	return withOptions(t, f.Target(tgt), doc)
}

func newCollector(t *testing.T, tg collect.Target) sdk.Collector {
	t.Helper()
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// runOne runs the collector and returns the single batch it emits, checked
// against the schema and, on the fake, for allow-listed GETs only.
func runOne(t *testing.T, f *FakeKubeMetrics, extra string) *sdk.Batch {
	t.Helper()
	bs := collecttest.Run(t, newCollector(t, target(t, f, extra)))
	if len(bs) != 1 {
		t.Fatalf("got %d batches, want 1", len(bs))
	}
	if bs[0].Source != "kubemetrics:"+tgt {
		t.Fatalf("source %q", bs[0].Source)
	}
	if err := f.OnlyAllowed(); err != nil {
		t.Fatal(err)
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

func count(b *sdk.Batch, typ string) int { return len(collecttest.Objects([]*sdk.Batch{b}, typ)) }

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

// workloadOfPod returns the (kind, name) of the workload served_by a pod, or
// "" when it has none.
func workloadOfPod(b *sdk.Batch, podRef string) string {
	for _, r := range b.Relations {
		if r.Type == "served_by" && r.To.Ref == podRef {
			o, _ := b.Lookup(r.From.Ref)
			return o.Attributes["kind"].(string) + "/" + o.Identity["name"]
		}
	}
	return ""
}

// collectRaw runs the collector and returns whatever batch it emits, with no
// schema check (a failed run is a bare health record).
func collectRaw(t *testing.T, tg collect.Target) *sdk.Batch {
	t.Helper()
	var got []*sdk.Batch
	err := newCollector(t, tg).Collect(context.Background(), sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
		got = append(got, b)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d batches, want 1", len(got))
	}
	return got[0]
}

func collectSecret(s string) collect.Secret { return collect.Secret(s) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
