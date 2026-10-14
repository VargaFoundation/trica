package search_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

var t0 = time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

// fixture loads a small site through the reconciliation engine, the only
// writer of the inventory, twice: decom-1 is not reported the second time and
// the sweep marks it absent.
func fixture(t *testing.T) *search.Searcher {
	t.Helper()
	s := testdb.New(t)
	now := t0
	reg := schema.Builtin()
	e := &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }, Now: func() time.Time { return now }}
	ctx := context.Background()
	for pass := range 2 {
		for _, b := range site() {
			if pass == 1 && b.Source == "maas:old" {
				continue
			}
			b.ObservedAt = now
			if _, err := e.Apply(ctx, b, "", ""); err != nil {
				t.Fatalf("%s: %v", b.Source, err)
			}
		}
		now = now.Add(2 * time.Hour)
	}
	now = now.Add(-2 * time.Hour)
	if _, err := e.SweepAbsent(ctx); err != nil {
		t.Fatal(err)
	}
	return &search.Searcher{Store: s, Now: func() time.Time { return now }}
}

func site() []*sdk.Batch {
	var out []*sdk.Batch
	b := sdk.NewBatch("declare:site", sdk.ScopeFull)
	b.Confidence = sdk.Declared
	b.Object("r1", "rack").Identify("name", "rack 1")
	b.Object("r2", "rack").Identify("name", "rack 2")
	b.Object("inet", "internet_access").Identify("name", "uplink-a").Set("public_ips", []any{"198.51.100.7", "198.51.100.8"})
	out = append(out, b)

	for i, sw := range []struct{ name, serial, version, mac string }{
		{"sw-a1", "JPE1111", "4.20.7M", "AA-BB-CC-00-00-01"},
		{"sw-a2", "JPE2222", "4.14.5F", "aabb.cc00.0002"},
		{"sw-b1", "JPE3333", "4.9.1", "aa:bb:cc:00:00:03"},
	} {
		b := sdk.NewBatch("arista:"+sw.name, sdk.ScopeFull)
		b.Object("d", "device").Identify("serial", sw.serial).Identify("name", sw.name).
			Set("role", "switch").Set("vendor", "Arista").Set("os_version", sw.version).
			Set("mgmt_ip", fmt.Sprintf("192.0.2.%d", 10+i)).Set("power_w", float64(100+50*i))
		b.Object("e1", "interface").Identify("name", "Ethernet1").In("d").Set("mac", sw.mac).Set("description", "uplink to core")
		out = append(out, b)
	}

	b = sdk.NewBatch("redfish:srv1", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "CZ0001").Identify("name", "srv1").Set("role", "server").Set("vendor", "HPE").Set("power_w", 420.0).Set("mgmt_ip", "198.51.100.20")
	b.Object("disk0", "disk").Identify("serial", "S3Z0A").In("d").Set("wear_pct", 74.0).Set("media", "ssd")
	b.Object("disk1", "disk").Identify("serial", "S3Z0B").In("d").Set("wear_pct", 12.0).Set("media", "ssd")
	b.Object("r2", "rack").Identify("name", "rack 2")
	b.Relate("mounted_in", "d", "r2")
	out = append(out, b)

	b = sdk.NewBatch("redfish:srv2", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "CZ0002").Identify("name", "srv2").Set("role", "server").Set("power_w", 380.0)
	b.Object("disk0", "disk").Identify("serial", "S3Z0C").In("d").Set("wear_pct", 91.0).Set("media", "ssd")
	b.Object("r1", "rack").Identify("name", "rack 1")
	b.Relate("mounted_in", "d", "r1")
	out = append(out, b)

	b = sdk.NewBatch("maas:old", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "OLD0001").Identify("name", "decom-1").Set("role", "server")
	return append(out, b)
}

// label names a result for the assertions: the MAC of an interface (they
// are all Ethernet1), else the name, else the display name.
func label(o *store.Object) string {
	if o.Type == "interface" {
		return fmt.Sprint(o.Attrs["mac"])
	}
	if n, ok := o.Attrs["name"].(string); ok {
		return n
	}
	return o.Display
}

func names(r *search.Result) []string {
	out := make([]string, 0, len(r.Items))
	for _, o := range r.Items {
		out = append(out, label(o))
	}
	return out
}

func sorted(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

func TestSearch(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	cases := []struct {
		q       string
		want    []string
		ordered bool
	}{
		// Free text and identifiers.
		{q: "sw-a1", want: []string{"sw-a1"}},
		{q: "SW-A", want: []string{"sw-a1", "sw-a2"}},
		{q: "192.0.2.11", want: []string{"sw-a2"}},
		{q: "jpe3333", want: []string{"sw-b1"}},
		{q: "AABB.CC00.0001", want: []string{"aa:bb:cc:00:00:01"}},
		{q: "aa-bb-cc-00-00-02", want: []string{"aa:bb:cc:00:00:02"}},
		{q: "198.51.100.8", want: []string{"uplink-a"}},
		// Structured.
		{q: "type:device role:switch", want: []string{"sw-a1", "sw-a2", "sw-b1"}},
		{q: "type:device vendor:arista os_version<4.20", want: []string{"sw-a2", "sw-b1"}},
		{q: "type:device os_version>=4.14.5", want: []string{"sw-a1", "sw-a2"}},
		{q: "type:disk wear_pct>70", want: []string{"S3Z0A", "S3Z0C"}},
		{q: `type:disk wear_pct>70 rel.mounted_in:"rack 2"`, want: []string{"S3Z0A"}},
		{q: "type:disk rel.mounted_in:rack*", want: []string{"S3Z0A", "S3Z0B", "S3Z0C"}},
		{q: "type:device rel.mounted_in:*", want: []string{"srv1", "srv2"}},
		{q: "type:device rel.mounted_in!=*", want: []string{"sw-a1", "sw-a2", "sw-b1"}},
		{q: "type:device mgmt_ip<<192.0.2.0/24", want: []string{"sw-a1", "sw-a2", "sw-b1"}},
		{q: "type:device role:server -vendor:hpe", want: []string{"srv2"}},
		{q: "type:device vendor!=hpe role:server", want: []string{"srv2"}},
		{q: "type:interface mac:AABBCC000003", want: []string{"aa:bb:cc:00:00:03"}},
		{q: "type:interface description~core", want: []string{"aa:bb:cc:00:00:01", "aa:bb:cc:00:00:02", "aa:bb:cc:00:00:03"}},
		{q: "public_ips:198.51.100.7", want: []string{"uplink-a"}},
		{q: "type:device name:sw-a*", want: []string{"sw-a1", "sw-a2"}},
		{q: "type:device display:srv*", want: []string{"srv1", "srv2"}},
		{q: "type:disk parent:srv1", want: []string{"S3Z0A", "S3Z0B"}},
		{q: "type:rack has:public_ips", want: nil},
		{q: "has:public_ips", want: []string{"uplink-a"}},
		{q: "type:device source:redfish:*", want: []string{"srv1", "srv2"}},
		{q: "type:device source!=redfish:* source!=declare:*", want: []string{"sw-a1", "sw-a2", "sw-b1"}},
		{q: "type:device (vendor:hpe OR os_version:4.9.1)", want: []string{"srv1", "sw-b1"}},
		{q: "type:rack,internet_access", want: []string{"rack 1", "rack 2", "uplink-a"}},
		{q: "type!=device type!=interface type!=disk", want: []string{"rack 1", "rack 2", "uplink-a"}},
		// Absent objects only when asked.
		{q: "decom-1", want: nil},
		{q: "decom-1 status:absent", want: []string{"decom-1"}},
		{q: "type:device last_seen<-1h status:active,absent", want: []string{"decom-1"}},
		{q: "type:device first_seen>=2026-03-01 last_seen>-1h role:server", want: []string{"srv1", "srv2"}},
		// Ranking and directives.
		{q: "type:device sort:-power_w limit:2", want: []string{"srv1", "srv2"}, ordered: true},
		{q: "type:device role:switch sort:os_version", want: []string{"sw-a2", "sw-a1", "sw-b1"}, ordered: true},
		{q: "type:device role:switch sort:-name", want: []string{"sw-b1", "sw-a2", "sw-a1"}, ordered: true},
		{q: "type:device role:switch sort:-last_seen,display", want: []string{"sw-a1", "sw-a2", "sw-b1"}, ordered: true},
		{q: "type:rack sort:-id", want: []string{"rack 1", "rack 2"}},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			r, err := s.Search(ctx, tc.q, search.Page{})
			if err != nil {
				t.Fatal(err)
			}
			got := names(r)
			want := tc.want
			if !tc.ordered {
				got, want = sorted(got), sorted(want)
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("got %v, want %v", got, want)
			}
			if r.Total != len(tc.want) {
				t.Errorf("total %d, want %d", r.Total, len(tc.want))
			}
		})
	}
}

func TestSearchRanksExactNameFirst(t *testing.T) {
	s := fixture(t)
	r, err := s.Search(context.Background(), "sw-a2", search.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) == 0 || label(r.Items[0]) != "sw-a2" {
		t.Errorf("exact match first: %v", names(r))
	}
}

// An exact hit hides its near neighbours; a typo falls back to them.
func TestSearchIsFuzzyOnlyWhenExactFindsNothing(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	r, err := s.Search(ctx, "sw-a1", search.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fuzzy || fmt.Sprint(names(r)) != "[sw-a1]" {
		t.Errorf("exact: fuzzy %v %v", r.Fuzzy, names(r))
	}
	r, err = s.Search(ctx, "swa-1", search.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Fuzzy || len(r.Items) == 0 || label(r.Items[0]) != "sw-a1" {
		t.Errorf("typo: fuzzy %v %v, want sw-a1 first", r.Fuzzy, names(r))
	}
	r, _ = s.Search(ctx, "type:device -swa-1", search.Page{})
	if r.Fuzzy {
		t.Error("a negated term does not trigger the fuzzy pass")
	}
}

func TestSearchPagesAndCounts(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	r, err := s.Search(ctx, "role:switch,server", search.Page{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 5 || r.ByType["device"] != 5 || len(r.Items) != 2 || r.Limit != 2 || r.Offset != 1 {
		t.Errorf("total %d by type %v items %d", r.Total, r.ByType, len(r.Items))
	}
	if r.Took <= 0 || r.Query != "role:switch,server" {
		t.Errorf("result metadata: %+v", r)
	}

	// limit: caps the result; the page slices within it.
	r, _ = s.Search(ctx, "type:device sort:-power_w limit:3", search.Page{Limit: 10, Offset: 2})
	if r.Total != 3 || len(r.Items) != 1 {
		t.Errorf("limit:3 offset 2: total %d items %v", r.Total, names(r))
	}
	r, _ = s.Search(ctx, "type:device limit:3", search.Page{Offset: 5})
	if len(r.Items) != 0 || r.Limit != 0 {
		t.Errorf("past the cap: %v", names(r))
	}
	r, _ = s.Search(ctx, "nothing-matches-this", search.Page{})
	if r.Total != 0 || r.Items == nil || len(r.ByType) != 0 {
		t.Errorf("empty result: %+v", r)
	}
	r, _ = s.Search(ctx, "", search.Page{Limit: 5000})
	if r.Limit != 1000 || r.Total != 14 {
		t.Errorf("an empty query lists every active object, capped pages: limit %d total %d by type %v", r.Limit, r.Total, r.ByType)
	}
}

func TestSearchRejectsBadQueries(t *testing.T) {
	s := fixture(t)
	for _, q := range []string{
		`"open`,
		"type<device",
		"last_seen:yesterday",
		"last_seen<yesterday",
		"parent!=x",
		"has:a-b",
		"has!=x",
		"source<x",
		"rel.9x:x",
		"rel.hosts<x",
		"mgmt_ip<<not-an-ip",
		"sort:9x",
	} {
		_, err := s.Search(context.Background(), q, search.Page{})
		if !errors.Is(err, search.ErrQuery) {
			t.Errorf("%q: want ErrQuery, got %v", q, err)
		}
		if err := search.Validate(q); !errors.Is(err, search.ErrQuery) {
			t.Errorf("Validate(%q): got %v", q, err)
		}
	}
}

func TestSearchDatabaseError(t *testing.T) {
	s := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, "sw", search.Page{}); err == nil || errors.Is(err, search.ErrQuery) {
		t.Errorf("a database error is not a query error: %v", err)
	}
}

func TestSavedQueries(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	if err := s.Save(ctx, search.Saved{Name: "worn ssd", Query: "type:disk wear_pct>70", Owner: "ops"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, search.Saved{Name: "worn ssd", Query: "type:disk wear_pct>80", Description: "tighter"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []search.Saved{{Name: " ", Query: "x"}, {Name: "bad", Query: "(x"}, {Name: string(make([]byte, 101)), Query: "x"}} {
		if err := s.Save(ctx, bad); !errors.Is(err, search.ErrQuery) {
			t.Errorf("%q: want ErrQuery, got %v", bad.Name, err)
		}
	}
	got, err := s.SavedQuery(ctx, "worn ssd")
	if err != nil || got.Query != "type:disk wear_pct>80" || got.Description != "tighter" {
		t.Fatalf("got %+v, %v", got, err)
	}
	all, err := s.SavedQueries(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list: %v %v", all, err)
	}
	r, err := s.Search(ctx, got.Query, search.Page{})
	if err != nil || fmt.Sprint(names(r)) != "[S3Z0C]" {
		t.Errorf("a saved query runs: %v %v", names(r), err)
	}
	if err := s.DeleteSavedQuery(ctx, "worn ssd"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavedQuery(ctx, "worn ssd"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.DeleteSavedQuery(ctx, "worn ssd"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}
