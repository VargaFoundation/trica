package reconcile_test

import (
	"context"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// TestInstanceSeenTwiceIsOneObject (ADR 0069): the collector of a cluster that
// sees an application through its workload labels and the collector of the
// cluster that runs Argo CD, which sees the same instance through its
// Application, describe it with the same identity. The engine must end up
// with one application, one component, one instance and one environment.
func TestInstanceSeenTwiceIsOneObject(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	seen := func(source string, extra func(b *sdk.Batch)) {
		b := batch(c, source, sdk.ScopePartial)
		b.Object("app/orion", "application").Identify("name", "orion")
		b.Object("component/orion/app", "component").In("app/orion").Identify("name", "app")
		b.Object("instance/orion/app/dev/orion", "component_instance").In("component/orion/app").
			Identify("key", "orion/app/dev/orion").Set("environment", "dev")
		b.Object("env/dev", "environment").Identify("name", "dev")
		b.Relate("deployed_in", "instance/orion/app/dev/orion", "env/dev")
		extra(b)
		apply(t, e, b)
	}
	seen("kubernetes:dev", func(b *sdk.Batch) {
		b.Object("instance/orion/app/dev/orion", "component_instance").Set("version", "1.4.0")
	})
	seen("kubernetes:build", func(b *sdk.Batch) {
		b.Object("instance/orion/app/dev/orion", "component_instance").Set("sync_status", "Synced")
	})

	for typ, want := range map[string]int{"application": 1, "component": 1, "component_instance": 1, "environment": 1} {
		if _, n, err := s.List(ctx, store.ListFilter{Type: typ}); err != nil || n != want {
			t.Errorf("%s objects = %d (err %v), want %d", typ, n, err, want)
		}
	}
	items, _, err := s.List(ctx, store.ListFilter{Type: "component_instance"})
	if err != nil || len(items) != 1 {
		t.Fatalf("instances: %v, %v", items, err)
	}
	// Both collectors' claims landed on the one object.
	if a := items[0].Attrs; a["version"] != "1.4.0" || a["sync_status"] != "Synced" {
		t.Errorf("attributes = %v, want the version of one source and the sync status of the other", a)
	}
}
