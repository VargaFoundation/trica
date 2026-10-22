package api_test

import (
	"sort"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// mountedFixture adds, to the fixtures (sw-a1 and srv-01 are mounted by their
// collectors): spare-01, in no rack, and old-01, declared in rack-a and then
// taken out again (a closed relation is not a mount).
func (e *env) mountedFixture() {
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("spare", "device").Identify("name", "spare-01").Set("role", "server")
	b.Object("old", "device").Identify("name", "old-01").Set("role", "server")
	rack := sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: e.ids["rack-p"]}}
	b.RelateEndpoints("mounted_in", sdk.Ref("old"), rack)
	e.apply(b)
	out := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	out.Confidence = sdk.Declared
	out.Unrelate("mounted_in", sdk.Endpoint{Type: "device", Identity: map[string]string{sdk.IDKey: e.ids["old"]}}, rack)
	e.apply(out)
}

func names(t *testing.T, p page) []string {
	t.Helper()
	out := ids(t, p, "display")
	sort.Strings(out)
	return out
}

func sameNames(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: %v, want %v", what, got, want)
		}
	}
}

// The rack page offers only devices that are in no rack: mounted=false is the
// objects with no open mounted_in relation, whoever claims it, and a mount
// that was withdrawn no longer counts.
func TestObjectsMountedFilter(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.mountedFixture()
	sameNames(t, "mounted=false", names(t, e.page("/api/v1/objects?type=device&mounted=false", tokReader)), "old-01", "spare-01")
	sameNames(t, "mounted=true", names(t, e.page("/api/v1/objects?type=device&mounted=true", tokReader)), "srv-01", "sw-a1")
	sameNames(t, "no filter", names(t, e.page("/api/v1/objects?type=device", tokReader)), "old-01", "spare-01", "srv-01", "sw-a1")
	// The filter is ANDed with the query, even an OR.
	sameNames(t, "q with OR", names(t, e.page("/api/v1/objects?type=device&mounted=false&q=name:spare-01+OR+name:sw-a1", tokReader)), "spare-01")
	wantProblem(t, e.get("/api/v1/objects?type=device&mounted=maybe", tokReader), 400, "invalid-request")
	// A caller limited to a site is refused as without the filter.
	wantStatus(t, e.get("/api/v1/objects?type=device&mounted=false", tokSiteParis), 403)
}
