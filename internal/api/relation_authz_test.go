package api_test

import (
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// relate adds a relation between two endpoints of any kind (Batch.Relate only
// takes refs).
func relate(b *sdk.Batch, typ string, from, to sdk.Endpoint) {
	b.Relations = append(b.Relations, &sdk.Relation{Type: typ, From: from, To: to, Attributes: map[string]any{}})
}

// A relation or unrelation is a write on both of its ends: the caller needs
// the action on the type of each, whether the end is designated by a batch
// ref, by id (the stored type counts, not the declared one), by identity, or
// through a parent.
func TestRelationEndsAreCheckedLikeObjects(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	rackP, sw, srv := e.ids["rack-p"], e.ids["sw"], e.ids["srv"]
	cases := []struct {
		name  string
		build func(b *sdk.Batch)
		want  int
	}{
		{"device to device by id is allowed", func(b *sdk.Batch) {
			relate(b, "depends_on", sdk.ByID("device", sw), sdk.ByID("device", srv))
		}, 200},
		{"relation to a rack by id", func(b *sdk.Batch) {
			b.Object("n", "device").Identify("name", "srv-02")
			relate(b, "mounted_in", sdk.Ref("n"), sdk.ByID("rack", rackP))
		}, 403},
		{"relation to a rack by id under a type that lies", func(b *sdk.Batch) {
			b.Object("n", "device").Identify("name", "srv-02")
			relate(b, "mounted_in", sdk.Ref("n"), sdk.ByID("device", rackP))
		}, 403},
		{"relation to a rack by identity", func(b *sdk.Batch) {
			b.Object("n", "device").Identify("name", "srv-02")
			relate(b, "mounted_in", sdk.Ref("n"), sdk.Endpoint{Type: "rack", Identity: map[string]string{"name": "rack-a"}})
		}, 403},
		{"end found through a parent of another type", func(b *sdk.Batch) {
			b.Object("n", "device").Identify("name", "srv-02")
			relate(b, "depends_on", sdk.Ref("n"), sdk.Endpoint{Type: "device", Identity: map[string]string{"name": "x"},
				Parent: &sdk.Endpoint{Type: "site", Identity: map[string]string{"code": "PAR1"}}})
		}, 403},
		{"relation from a rack by id", func(b *sdk.Batch) {
			b.Object("n", "device").Identify("name", "srv-02")
			relate(b, "mounted_in", sdk.ByID("rack", rackP), sdk.Ref("n"))
		}, 403},
		{"unrelation of a rack by id", func(b *sdk.Batch) {
			b.Unrelate("mounted_in", sdk.ByID("device", sw), sdk.ByID("rack", rackP))
		}, 403},
		{"unrelation of a rack under a type that lies", func(b *sdk.Batch) {
			b.Unrelate("mounted_in", sdk.ByID("device", sw), sdk.ByID("device", rackP))
		}, 403},
	}
	for _, tc := range cases {
		for _, path := range []string{"/api/v1/batches", "/api/v1/batches/preview"} {
			t.Run(tc.name+path, func(t *testing.T) {
				before := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokAdmin).Total
				r := e.do("POST", path, tokDevProp, batchJSON(t, userBatch("", tc.build)))
				if tc.want != 403 {
					wantStatus(t, r, tc.want)
					return
				}
				wantProblem(t, r, 403, "forbidden")
				if after := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokAdmin).Total; after != before {
					t.Fatalf("a refused batch wrote changes: %d then %d", before, after)
				}
			})
		}
	}
}

// The change feed keeps a relation change on a readable object but blanks
// other_id when the other end is unreadable, by type or by site.
func TestChangeFeedRedactsUnreadableOtherEnd(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw, srv, rackP := e.ids["sw"], e.ids["srv"], e.ids["rack-p"]
	b := sdk.NewBatch("declaration:cabling", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	relate(b, "depends_on", sdk.ByID("device", sw), sdk.ByID("device", srv))
	e.apply(b)

	// others returns the other_id token sees on the relation changes of object.
	others := func(token, object, relation string) []string {
		var out []string
		for _, it := range e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", token).Items {
			if field(t, it, "object_id") == object && field(t, it, "relation") == relation {
				out = append(out, field(t, it, "other_id"))
			}
		}
		return out
	}
	cases := []struct {
		name             string
		token            string
		object, relation string
		want             string // expected other_id; "" means redacted
	}{
		{"unrestricted reader sees the rack", tokReader, sw, "mounted_in", rackP},
		{"unrestricted reader sees the device", tokReader, sw, "depends_on", srv},
		{"devices-only reader: rack is unreadable", tokDevices, sw, "mounted_in", ""},
		{"devices-only reader: device is readable", tokDevices, sw, "depends_on", srv},
		{"Paris reader: rack in Paris is readable", tokSiteParis, sw, "mounted_in", rackP},
		{"Paris reader: device in Lyon is unreadable", tokSiteParis, sw, "depends_on", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := others(tc.token, tc.object, tc.relation)
			if len(got) == 0 {
				t.Fatal("the relation change is gone from the feed: it must be kept, redacted")
			}
			for _, g := range got {
				if g != tc.want {
					t.Fatalf("other_id = %q, want %q", g, tc.want)
				}
			}
		})
	}
}
