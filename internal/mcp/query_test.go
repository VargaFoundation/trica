package mcp_test

import (
	"reflect"
	"slices"
	"testing"
)

type m = map[string]any

func TestSearch(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))

	t.Run("an address finds its device, and the summary comes first", func(t *testing.T) {
		a := ok(t, cs, "search", m{"query": "192.0.2.13"})
		if got := num(t, a.Data, "summary", "total"); got != 1 {
			t.Fatalf("total %d, want 1", got)
		}
		if got := displays(t, a.Data, "items"); !reflect.DeepEqual(got, []string{"sw-b2"}) {
			t.Fatalf("items %v", got)
		}
		if num(t, a.Data, "summary", "by_type", "device") != 1 || dig(t, a.Data, "summary", "truncated") != false {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
		if str(t, a.Data, "items", 0, "attributes", "role") != "switch" {
			t.Fatalf("attributes %v", dig(t, a.Data, "items", 0))
		}
	})

	t.Run("a MAC address in another notation finds the port", func(t *testing.T) {
		a := ok(t, cs, "search", m{"query": "02-00-5E-00-53-03"})
		if got := displays(t, a.Data, "items"); !reflect.DeepEqual(got, []string{"eth0"}) || num(t, a.Data, "summary", "by_type", "interface") != 1 {
			t.Fatalf("items %v, summary %v", got, dig(t, a.Data, "summary"))
		}
	})

	t.Run("a page says whether it is the last, and where to read on", func(t *testing.T) {
		seen := map[string]bool{}
		first := ok(t, cs, "search", m{"query": "type:device role:server sort:display", "limit": 5})
		if num(t, first.Data, "summary", "total") != 12 || num(t, first.Data, "summary", "returned") != 5 ||
			dig(t, first.Data, "summary", "truncated") != true || num(t, first.Data, "summary", "next_offset") != 5 {
			t.Fatalf("first page: %v", dig(t, first.Data, "summary"))
		}
		for _, d := range displays(t, first.Data, "items") {
			seen[d] = true
		}
		last := ok(t, cs, "search", m{"query": "type:device role:server sort:display", "limit": 5, "offset": 10})
		if num(t, last.Data, "summary", "returned") != 2 || dig(t, last.Data, "summary", "truncated") != false || dig(t, last.Data, "summary", "next_offset") != nil {
			t.Fatalf("last page: %v", dig(t, last.Data, "summary"))
		}
		if got := displays(t, last.Data, "items"); !reflect.DeepEqual(got, []string{"srv-11", "srv-12"}) {
			t.Fatalf("last page items %v", got)
		}
		if seen["srv-11"] {
			t.Fatal("the pages overlap")
		}
	})

	t.Run("fields keep the answer small", func(t *testing.T) {
		a := ok(t, cs, "search", m{"query": "type:device role:server", "fields": []string{"power_w"}, "limit": 1})
		attrs, _ := dig(t, a.Data, "items", 0, "attributes").(map[string]any)
		if len(attrs) != 1 || attrs["power_w"] == nil {
			t.Fatalf("attributes %v", attrs)
		}
	})

	t.Run("bad input is refused with a reason", func(t *testing.T) {
		denied(t, cs, "search", m{"query": ""}, "invalid-request")
		denied(t, cs, "search", m{"query": "type:device AND ("}, "invalid-query")
		denied(t, cs, "search", m{"query": "server", "limit": 100000}, "invalid-request")
		denied(t, cs, "search", m{"query": "server", "offset": -1}, "invalid-request")
	})
}

func TestListObjects(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))

	a := ok(t, cs, "list_objects", m{"type": "rack", "sort": "display"})
	if got := displays(t, a.Data, "items"); !reflect.DeepEqual(got, []string{"rack-1", "rack-2", "rack-b"}) {
		t.Fatalf("racks %v", got)
	}
	if num(t, a.Data, "summary", "total") != 3 || num(t, a.Data, "summary", "by_type", "rack") != 3 {
		t.Fatalf("summary %v", dig(t, a.Data, "summary"))
	}
	b := ok(t, cs, "list_objects", m{"type": "device", "query": "role:switch", "sort": "-power_w", "limit": 2, "fields": []string{"power_w"}})
	if got := displays(t, b.Data, "items"); !reflect.DeepEqual(got, []string{"sw-a1", "sw-a2"}) || num(t, b.Data, "summary", "total") != 5 {
		t.Fatalf("switches %v, summary %v", got, dig(t, b.Data, "summary"))
	}
	denied(t, cs, "list_objects", m{"type": "no_such_type"}, "invalid-request")
}

func TestFilterObjects(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))
	filter := func(typ string, filters ...m) answer {
		return ok(t, cs, "filter_objects", m{"type": typ, "filters": filters, "sort": "display"})
	}
	for name, tc := range map[string]struct {
		filters []m
		want    []string
	}{
		"eq":              {[]m{{"attribute": "airflow", "op": "eq", "value": "rear_to_front"}}, []string{"sw-a1", "sw-b1"}},
		"gt as a number":  {[]m{{"attribute": "role", "op": "eq", "value": "switch"}, {"attribute": "power_w", "op": "gt", "value": "170"}}, []string{"sw-a1", "sw-a2"}},
		"lt on a version": {[]m{{"attribute": "os_version", "op": "lt", "value": "4.20"}}, []string{"sw-b1"}},
		"missing":         {[]m{{"attribute": "role", "op": "eq", "value": "switch"}, {"attribute": "os_version", "op": "missing"}}, []string{"sw-b2"}},
		"exists":          {[]m{{"attribute": "airflow", "op": "exists"}}, []string{"sw-a1", "sw-a2", "sw-b1", "sw-b2", "sw-b3"}},
		"in":              {[]m{{"attribute": "mgmt_ip", "op": "in", "value": "192.0.2.10, 192.0.2.14"}}, []string{"sw-a1", "sw-b3"}},
		"within":          {[]m{{"attribute": "mgmt_ip", "op": "within", "value": "192.0.2.8/29"}}, []string{"sw-a1", "sw-a2", "sw-b1", "sw-b2", "sw-b3"}},
		"ne":              {[]m{{"attribute": "role", "op": "eq", "value": "switch"}, {"attribute": "airflow", "op": "ne", "value": "rear_to_front"}}, []string{"sw-a2", "sw-b2", "sw-b3"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := displays(t, filter("device", tc.filters...).Data, "items")
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("a value is data, never a query", func(t *testing.T) {
		a := filter("", m{"attribute": "name", "op": "eq", "value": "x) OR type:site ("})
		if num(t, a.Data, "summary", "total") != 0 {
			t.Fatalf("the value was run as a query: %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("bad filters are refused", func(t *testing.T) {
		denied(t, cs, "filter_objects", m{"filters": []m{}}, "invalid-request")
		denied(t, cs, "filter_objects", m{"filters": []m{{"attribute": "role", "op": "like", "value": "x"}}}, "invalid-request")
		denied(t, cs, "filter_objects", m{"filters": []m{{"attribute": "role", "op": "eq"}}}, "invalid-request")
		denied(t, cs, "filter_objects", m{"filters": []m{{"attribute": "role:x OR type", "op": "eq", "value": "x"}}}, "invalid-request")
	})
}

func TestRankFilter(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))

	t.Run("the top of a population, best first, with the total", func(t *testing.T) {
		a := ok(t, cs, "rank_filter", m{"attribute": "power_w", "type": "device", "query": "role:server", "n": 3})
		if got := displays(t, a.Data, "items"); !reflect.DeepEqual(got, []string{"srv-12", "srv-11", "srv-10"}) {
			t.Fatalf("ranking %v", got)
		}
		if num(t, a.Data, "items", 0, "rank") != 1 || num(t, a.Data, "items", 0, "value") != 420 || num(t, a.Data, "items", 2, "rank") != 3 {
			t.Fatalf("items %v", dig(t, a.Data, "items"))
		}
		if str(t, a.Data, "summary", "ranked_by") != "power_w" || str(t, a.Data, "summary", "order") != "desc" ||
			num(t, a.Data, "summary", "total") != 12 || dig(t, a.Data, "summary", "truncated") != true {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("ten by default, ascending on request, with filters", func(t *testing.T) {
		a := ok(t, cs, "rank_filter", m{"attribute": "power_w", "type": "device", "query": "role:server"})
		if num(t, a.Data, "summary", "returned") != 10 {
			t.Fatalf("returned %v", dig(t, a.Data, "summary"))
		}
		b := ok(t, cs, "rank_filter", m{"attribute": "power_w", "type": "device", "order": "asc", "n": 2,
			"filters": []m{{"attribute": "role", "op": "eq", "value": "switch"}}, "fields": []string{"mgmt_ip"}})
		if got := displays(t, b.Data, "items"); !reflect.DeepEqual(got, []string{"sw-b3", "sw-b2"}) {
			t.Fatalf("ascending %v", got)
		}
		if str(t, b.Data, "items", 0, "attributes", "mgmt_ip") != "192.0.2.14" {
			t.Fatalf("extra attribute: %v", dig(t, b.Data, "items", 0))
		}
	})
	t.Run("objects without the attribute are left out", func(t *testing.T) {
		a := ok(t, cs, "rank_filter", m{"attribute": "power_w", "type": "interface"})
		if num(t, a.Data, "summary", "total") != 0 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("bad input is refused", func(t *testing.T) {
		denied(t, cs, "rank_filter", m{"attribute": "power_w", "n": 100000}, "invalid-request")
		denied(t, cs, "rank_filter", m{"attribute": "power_w", "order": "sideways"}, "invalid-request")
		denied(t, cs, "rank_filter", m{"attribute": "power w"}, "invalid-request")
	})
}
