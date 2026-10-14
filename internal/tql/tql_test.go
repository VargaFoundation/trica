package tql

import (
	"errors"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Node
	}{
		{"", nil},
		{"sw-a1", Text{"sw-a1"}},
		{"192.0.2.13", Text{"192.0.2.13"}},
		{"aa:bb:cc:dd:ee:ff", Text{"aa:bb:cc:dd:ee:ff"}},
		{"2001:db8::1", Text{"2001:db8::1"}},
		{"https://example.org/x", Text{"https://example.org/x"}},
		{`"rack 2"`, Text{"rack 2"}},
		{"type:device", Pred{"type", OpEq, []string{"device"}}},
		{"Type=device,vm", Pred{"type", OpEq, []string{"device", "vm"}}},
		{"os_version<4.20", Pred{"os_version", OpLt, []string{"4.20"}}},
		{"wear_pct>=70", Pred{"wear_pct", OpGe, []string{"70"}}},
		{"role!=switch", Pred{"role", OpNe, []string{"switch"}}},
		{"description~uplink", Pred{"description", OpContains, []string{"uplink"}}},
		{"address<<192.0.2.0/24", Pred{"address", OpWithin, []string{"192.0.2.0/24"}}},
		{`rel.mounted_in:"rack 2"`, Pred{"rel.mounted_in", OpEq, []string{"rack 2"}}},
		{`name:"a,b"`, Pred{"name", OpEq, []string{"a,b"}}},
		{`model:"say \"hi\" \\o/"`, Pred{"model", OpEq, []string{`say "hi" \o/`}}},
		{"source:arista:*", Pred{"source", OpEq, []string{"arista:*"}}},
		{"type:device role:switch", And{[]Node{Pred{"type", OpEq, []string{"device"}}, Pred{"role", OpEq, []string{"switch"}}}}},
		{"a AND b", And{[]Node{Text{"a"}, Text{"b"}}}},
		{"a OR b c", Or{[]Node{Text{"a"}, And{[]Node{Text{"b"}, Text{"c"}}}}}},
		{"-status:absent", Not{Pred{"status", OpEq, []string{"absent"}}}},
		{"NOT (a OR b)", Not{Or{[]Node{Text{"a"}, Text{"b"}}}}},
		{"(a)", Text{"a"}},
		{"a-b -c", And{[]Node{Text{"a-b"}, Not{Text{"c"}}}}},
		{"x - y", And{[]Node{Text{"x"}, Text{"-"}, Text{"y"}}}},
	}
	for _, tc := range cases {
		q, err := Parse(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(q.Where, tc.want) {
			t.Errorf("%q:\n got %#v\nwant %#v", tc.in, q.Where, tc.want)
		}
	}
}

func TestDirectives(t *testing.T) {
	q, err := Parse("type:device sort:-power_w,name limit:10")
	if err != nil {
		t.Fatal(err)
	}
	if q.Limit != 10 || !reflect.DeepEqual(q.Sort, []SortKey{{"power_w", true}, {"name", false}}) {
		t.Errorf("got limit %d sort %v", q.Limit, q.Sort)
	}
	if !reflect.DeepEqual(q.Where, Pred{"type", OpEq, []string{"device"}}) {
		t.Errorf("directives are not conditions: %#v", q.Where)
	}
	q, err = Parse("sort:name")
	if err != nil || q.Where != nil || len(q.Sort) != 1 {
		t.Errorf("a query of directives only: %#v %v", q, err)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		`"open`,
		`name:"open`,
		"name:",
		"(a",
		"a)",
		"()",
		"OR a",
		"a OR",
		"a OR OR b",
		"AND a",
		"a AND",
		"a AND OR b",
		"NOT",
		"(NOT)",
		"limit:0",
		"limit:x",
		"limit:5000",
		"limit<5",
		"sort:-",
		"sort:a.b",
		"(a sort:name)",
		"NOT limit:5",
		"a OR b limit:5",
	} {
		_, err := Parse(in)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%q: want a syntax error, got %v", in, err)
		}
	}
}

func TestFieldsAndTexts(t *testing.T) {
	q, err := Parse("sw (role:switch OR vendor:arista) NOT hidden -status:absent")
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Fields(); !reflect.DeepEqual(got, []string{"role", "vendor", "status"}) {
		t.Errorf("fields: %v", got)
	}
	if got := q.Texts(); !reflect.DeepEqual(got, []string{"sw"}) {
		t.Errorf("texts leave negated terms out: %v", got)
	}
	empty, _ := Parse("")
	if empty.Fields() != nil || empty.Texts() != nil {
		t.Error("empty query")
	}
	if (&Error{3, "x"}).Error() != "tql: x at offset 3" {
		t.Error("error text")
	}
}
