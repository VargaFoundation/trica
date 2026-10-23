package search_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// TestSiteFollowsLocatedIn: a device in no rack is in the site of the room it
// was placed in (ADR 0058); a device in a rack is where its rack is, whatever
// a place says.
func TestSiteFollowsLocatedIn(t *testing.T) {
	st := testdb.New(t)
	reg := schema.Builtin()
	now := t0
	e := &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Now: func() time.Time { return now }}
	ctx := context.Background()

	b := sdk.NewBatch("declare:site", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("x", "site").Identify("code", "X1").Set("name", "site-x")
	b.Object("y", "site").Identify("code", "Y1").Set("name", "site-y")
	b.Object("b1", "building").Identify("name", "batiment-1").In("x")
	b.Object("salle-1", "room").Identify("name", "salle-1").In("b1")
	b.Object("salle-y", "room").Identify("name", "salle-y").In("y")
	b.Object("rack-x", "rack").Identify("name", "rack-x").In("x")
	b.Object("ap", "device").Identify("name", "ap-1").Set("role", "access_point")
	b.Object("sw", "device").Identify("name", "sw-1").Set("role", "switch")
	b.Object("lost", "device").Identify("name", "lost-1")
	res, err := e.Apply(ctx, b, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ids := res.Objects

	r := sdk.NewBatch("declare:place", sdk.ScopePartial)
	r.Confidence = sdk.Declared
	r.RelateEndpoints("located_in", sdk.ByID("device", ids["ap"]), sdk.ByID("room", ids["salle-1"]))
	r.RelateEndpoints("located_in", sdk.ByID("device", ids["sw"]), sdk.ByID("room", ids["salle-y"]))
	r.RelateEndpoints("mounted_in", sdk.ByID("device", ids["sw"]), sdk.ByID("rack", ids["rack-x"]))
	if _, err := e.Apply(ctx, r, "", ""); err != nil {
		t.Fatal(err)
	}

	s := &search.Searcher{Store: st, Now: func() time.Time { return now }}
	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"type:device site:X1", []string{"ap-1", "sw-1"}},
		{"type:device site:site-y", nil},
		{"type:device site!=X1", []string{"lost-1"}},
		{`type:device rel.located_in:"salle-1"`, []string{"ap-1"}},
		{"type:room site:X1", []string{"salle-1"}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			got, err := s.Search(ctx, tc.q, search.Page{})
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(sorted(names(got))) != fmt.Sprint(sorted(tc.want)) {
				t.Errorf("got %v, want %v", names(got), tc.want)
			}
		})
	}
}
