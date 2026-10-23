package collecttest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// recorder is a testing.TB that keeps errors instead of failing.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper()                   {}
func (r *recorder) Errorf(f string, a ...any) { r.errs = append(r.errs, fmt.Sprintf(f, a...)) }

func goodBatch() *sdk.Batch {
	b := sdk.NewBatch("test:a", sdk.ScopeFull)
	b.Object("sw", "device").Identify("serial", "SN1").Identify("base_mac", "00:1c:73:00:00:01").Set("role", "switch")
	b.Object("e1", "interface").In("sw").Identify("name", "Ethernet1").Set("speed_mbps", 10000)
	b.RelateEndpoints("neighbor_of", sdk.Ref("e1"), sdk.Endpoint{Type: "interface", Identity: map[string]string{"name": "eno1"}})
	return b
}

func TestCheckSchemaAcceptsAValidBatch(t *testing.T) {
	r := &recorder{TB: t}
	CheckSchema(r, goodBatch())
	if len(r.errs) > 0 {
		t.Fatal(r.errs)
	}
}

func TestCheckSchemaCatchesWhatTheEngineWouldDrop(t *testing.T) {
	cases := map[string]func(b *sdk.Batch){
		"unknown type":         func(b *sdk.Batch) { b.Object("x", "spaceship").Identify("name", "x") },
		"unknown attribute":    func(b *sdk.Batch) { o, _ := b.Lookup("sw"); o.Set("colour", "blue") },
		"bad enum":             func(b *sdk.Batch) { o, _ := b.Lookup("sw"); o.Set("role", "toaster") },
		"non-identity key":     func(b *sdk.Batch) { o, _ := b.Lookup("sw"); o.Identify("vendor", "Arista") },
		"bad identity value":   func(b *sdk.Batch) { o, _ := b.Lookup("sw"); o.Identify("base_mac", "not-a-mac") },
		"bad parent":           func(b *sdk.Batch) { b.Object("s", "site").In("sw").Identify("name", "s") },
		"unknown relation":     func(b *sdk.Batch) { b.Relate("loves", "sw", "e1") },
		"relation not allowed": func(b *sdk.Batch) { b.Relate("neighbor_of", "sw", "sw") },
		"unknown rel attr":     func(b *sdk.Batch) { b.Relate("contains", "sw", "e1").With("weight", 3) },
	}
	for name, mutate := range cases {
		b := goodBatch()
		mutate(b)
		r := &recorder{TB: t}
		CheckSchema(r, b)
		if len(r.errs) == 0 {
			t.Errorf("%s: not caught", name)
		}
	}
}

type fixed struct{ b *sdk.Batch }

func (f fixed) Kind() string { return "fixed" }
func (f fixed) Collect(ctx context.Context, e sdk.Emitter) error {
	return e.Emit(ctx, f.b)
}

func TestRunAndHelpers(t *testing.T) {
	bs := Run(t, fixed{goodBatch()})
	if len(bs) != 1 {
		t.Fatalf("%d batches", len(bs))
	}
	if Find(bs, "interface", "name", "Ethernet1") == nil || Find(bs, "interface", "name", "nope") != nil {
		t.Fatal("Find")
	}
	if len(Objects(bs, "device")) != 1 || len(Relations(bs, "neighbor_of")) != 1 {
		t.Fatal("Objects or Relations")
	}
	if !strings.HasPrefix(bs[0].Source, "test:") {
		t.Fatal(bs[0].Source)
	}
}

type fakeKind struct{ fixed }

func (fakeKind) Kind() string { return "collecttest-fake" }

// A fake run that emits a type its module does not declare fails Run, so a
// descriptor's Collects always covers what the fakes emit.
func TestRunRejectsUndeclaredTypes(t *testing.T) {
	if _, ok := collect.Lookup("collecttest-fake"); !ok {
		collect.Register(collect.Descriptor{
			Kind: "collecttest-fake", Title: "Fake", Vendors: []string{"Acme"}, Protocol: "none",
			Summary:    schema.Label{EN: "A fake.", FR: "Un faux."},
			Deployment: collect.DeploymentTarget,
			Collects:   []string{"device"}, // the batch also emits an interface
			ReadOnly:   "n/a", Address: "n/a", Tested: []string{"1"}, Evidence: collect.EvidenceSynthesised,
			Username: collect.Credential{Use: collect.UseUnused}, Secret: collect.Credential{Use: collect.UseUnused},
			Docs: "n/a",
		}, func(collect.Target) (sdk.Collector, error) { return nil, nil })
	}
	r := &recorder{TB: t}
	Run(r, fakeKind{fixed{goodBatch()}})
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "interface") || !strings.Contains(r.errs[0], "Collects") {
		t.Fatalf("errors: %v", r.errs)
	}

	// Declaring the type makes the same batch pass.
	d, _ := collect.Lookup("collecttest-fake")
	d.Collects = []string{"device", "interface"}
	r = &recorder{TB: t}
	CheckCollects(r, d, goodBatch())
	if len(r.errs) != 0 {
		t.Fatalf("declared types rejected: %v", r.errs)
	}
}
