package reconcile_test

import (
	"context"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// A declared full-scope batch (an application catalog, an import of scope
// full) is the whole of what its source declares: what it named before and
// omits now is withdrawn, whole objects included (ADR 0071). Before, only the
// objects the batch still named lost their omitted values: an application
// taken out of a catalog kept its owner and criticality for ever.

func declared(c *clock, source string, apps map[string]string) *sdk.Batch {
	b := batch(c, source, sdk.ScopeFull)
	b.Confidence = sdk.Declared
	b.WithdrawOmitted = true
	for name, crit := range apps {
		b.Object("app/"+name, "application").Identify("name", name).Set("criticality", crit).Set("display_name", "App "+name)
	}
	return b
}

func object(t *testing.T, s *store.Store, id string) *store.Object {
	t.Helper()
	o, err := s.Object(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestDeclaredFullScopeWithdrawsOmittedObjects(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	first := apply(t, e, declared(c, "catalog:main", map[string]string{"a": "high", "b": "low"}))
	a, b := first.Objects["app/a"], first.Objects["app/b"]

	c.advance(5 * time.Minute)
	res := apply(t, e, declared(c, "catalog:main", map[string]string{"a": "high"}))
	if res.ValuesChanged == 0 {
		t.Error("omitting b changed no value")
	}
	ob := object(t, s, b)
	if ob.Status != "absent" {
		t.Errorf("b is %s, want absent: only this source declared it", ob.Status)
	}
	if ob.Attrs["criticality"] != nil || ob.Attrs["display_name"] != nil {
		t.Errorf("b keeps declared values %v", ob.Attrs)
	}
	if ob.Display != "b" {
		t.Errorf("b is displayed %q: its identity must stay, so that it is still named", ob.Display)
	}
	if oa := object(t, s, a); oa.Status != "active" || oa.Attrs["criticality"] != "high" {
		t.Errorf("a %+v: still declared, untouched", oa)
	}

	// Declared again: the same object comes back.
	c.advance(5 * time.Minute)
	again := apply(t, e, declared(c, "catalog:main", map[string]string{"a": "high", "b": "medium"}))
	if again.Objects["app/b"] != b || again.Created != 0 {
		t.Fatalf("b re-declared as %s (created %d), want the same object %s", again.Objects["app/b"], again.Created, b)
	}
	if ob := object(t, s, b); ob.Status != "active" || ob.Attrs["criticality"] != "medium" {
		t.Errorf("re-declared b %+v", ob)
	}
}

// An object the source only identified (a team declared by its name alone)
// is retired too: nothing was withdrawn on it, it is still omitted.
func TestDeclaredFullScopeRetiresIdentityOnlyObjects(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	b := declared(c, "catalog:main", map[string]string{"a": "high"})
	b.Object("team/t", "team").Identify("name", "lonely")
	first := apply(t, e, b)
	c.advance(5 * time.Minute)
	apply(t, e, declared(c, "catalog:main", map[string]string{"a": "high"}))
	if o := object(t, s, first.Objects["team/t"]); o.Status != "absent" || o.Display != "lonely" {
		t.Errorf("team %+v: want absent, still named", o)
	}
}

// An omitted object another source still claims something on stays active
// with that source's values; one a collector observed is left to the sweep.
func TestDeclaredFullScopeLeavesOthersClaims(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	first := apply(t, e, declared(c, "catalog:main", map[string]string{"shared": "high", "seen": "low"}))
	shared, seen := first.Objects["app/shared"], first.Objects["app/seen"]

	person := batch(c, "user:alice", sdk.ScopePartial)
	person.Confidence = sdk.Declared
	person.Object("x", "application").Identify("name", "shared").Set("domain", "finance")
	apply(t, e, person)
	k8s := batch(c, "kubernetes:prod", sdk.ScopeFull)
	k8s.Object("x", "application").Identify("name", "seen").Set("repository", "https://git.example.org/seen")
	apply(t, e, k8s)

	c.advance(5 * time.Minute)
	apply(t, e, declared(c, "catalog:main", map[string]string{"other": "low"}))

	if o := object(t, s, shared); o.Status != "active" || o.Attrs["domain"] != "finance" || o.Attrs["criticality"] != nil {
		t.Errorf("shared %+v: active with alice's domain, without the catalog's criticality", o)
	}
	if o := object(t, s, seen); o.Status != "active" || o.Attrs["repository"] == nil || o.Attrs["criticality"] != nil {
		t.Errorf("seen %+v: active with what the collector observed, without the catalog's criticality", o)
	}
}

// A declared full-scope batch without WithdrawOmitted (the device-type
// library) keeps the objects it omits.
func TestDeclaredFullScopeWithoutTheFlagKeepsOmitted(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	first := apply(t, e, declared(c, "devicetypes", map[string]string{"a": "high", "b": "low"}))
	c.advance(5 * time.Minute)
	b := declared(c, "devicetypes", map[string]string{"a": "high"})
	b.WithdrawOmitted = false
	apply(t, e, b)
	if o := object(t, s, first.Objects["app/b"]); o.Status != "active" || o.Attrs["criticality"] != "low" {
		t.Errorf("b %+v: without the flag, an omitted object keeps its values", o)
	}
}

// An observed full-scope batch keeps its rule: an object it no longer names
// keeps its values and goes absent through the sweep, not at once (ADR 0007).
func TestObservedFullScopeKeepsOmittedObjects(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	b1 := batch(c, "kubernetes:prod", sdk.ScopeFull)
	b1.Object("a", "application").Identify("name", "a").Set("repository", "https://git.example.org/a")
	b1.Object("b", "application").Identify("name", "b").Set("repository", "https://git.example.org/b")
	first := apply(t, e, b1)

	c.advance(5 * time.Minute)
	b2 := batch(c, "kubernetes:prod", sdk.ScopeFull)
	b2.Object("a", "application").Identify("name", "a").Set("repository", "https://git.example.org/a")
	apply(t, e, b2)
	if o := object(t, s, first.Objects["b"]); o.Status != "active" || o.Attrs["repository"] == nil {
		t.Errorf("b %+v: an observed source's omission must wait for the sweep", o)
	}
}

// A failed declared run is partial (the engine's rule for failed runs) and
// withdraws nothing, omitted objects included.
func TestFailedDeclaredRunWithdrawsNothing(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	first := apply(t, e, declared(c, "catalog:main", map[string]string{"a": "high"}))
	c.advance(5 * time.Minute)
	failed := declared(c, "catalog:main", nil)
	failed.Health = &sdk.Health{Target: "main", OK: false, Error: "parse error"}
	apply(t, e, failed)
	if o := object(t, s, first.Objects["app/a"]); o.Status != "active" || o.Attrs["criticality"] != "high" {
		t.Errorf("a %+v after a failed run", o)
	}
}
