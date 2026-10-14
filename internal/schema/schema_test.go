package schema

import (
	"strings"
	"testing"
)

func TestBuiltinIsValid(t *testing.T) {
	r := Builtin()
	for _, name := range []string{"site", "rack", "device", "interface", "cable", "ip_address", "vm", "pod", "application", "software", "version_policy"} {
		if _, err := r.Type(name); err != nil {
			t.Errorf("builtin type %s missing: %v", name, err)
		}
	}
	for _, name := range []string{"contains", "mounted_in", "powered_by", "cabled_to", "neighbor_of", "hosts", "depends_on", "calls", "runs"} {
		if _, err := r.Relation(name); err != nil {
			t.Errorf("builtin relation %s missing: %v", name, err)
		}
	}
}

func TestBuiltinEveryTypeIsIdentifiable(t *testing.T) {
	r := Builtin()
	for _, n := range r.TypeNames() {
		ty := r.Types[n]
		strong, weak := ty.IdentityKeys()
		if len(strong)+len(weak) == 0 {
			t.Errorf("type %s has no identifying attribute", n)
		}
		if _, ok := ty.Attributes[ty.Display]; !ok {
			t.Errorf("type %s displays %q, which is not an attribute", n, ty.Display)
		}
		if ty.Label.EN == "" || ty.Label.FR == "" || ty.Description == "" {
			t.Errorf("type %s lacks a label or a description", n)
		}
		for an, a := range ty.Attributes {
			if a.Description == "" || a.Label.EN == "" || a.Label.FR == "" {
				t.Errorf("%s.%s lacks a label or a description", n, an)
			}
		}
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"bad kind":       "types: {x: {attributes: {a: {kind: nope}}}}",
		"enum no values": "types: {x: {attributes: {a: {kind: enum}}}}",
		"bad identity":   "types: {x: {attributes: {a: {kind: string, identity: maybe}}}}",
		"bad name":       "types: {X: {attributes: {}}}",
		"unknown parent": "types: {x: {parents: [y]}}",
		"unknown end":    "types: {x: {}}\nrelations: {r: {from: [x], to: [y]}}",
		"empty end":      "types: {x: {}}\nrelations: {r: {from: [x], to: []}}",
		"bad impact":     "types: {x: {}}\nrelations: {r: {from: [x], to: [x], impact: sideways}}",
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y), false); err == nil {
			t.Errorf("%s: accepted %q", name, y)
		}
	}
}

func TestMergeAddsCustomTypeAndField(t *testing.T) {
	base := Builtin()
	ov, err := Parse([]byte(`
types:
  device:
    attributes:
      cost_center: {kind: string, search: true}
  ups_battery:
    parents: [rack]
    attributes:
      serial: {kind: string, identity: strong}
relations:
  feeds:
    from: [ups_battery]
    to: [device]
`), true)
	if err != nil {
		t.Fatal(err)
	}
	m, err := base.Merge(ov)
	if err != nil {
		t.Fatal(err)
	}
	if a := m.Types["device"].Attributes["cost_center"]; a == nil || !a.Custom {
		t.Fatal("custom field not merged")
	}
	if ty, ok := m.Types["ups_battery"]; !ok || !ty.AllowsParent("rack") {
		t.Fatal("custom type not merged")
	}
	if _, ok := base.Types["device"].Attributes["cost_center"]; ok {
		t.Fatal("merge mutated the base registry")
	}
	if !m.Relations["feeds"].Allows("ups_battery", "device") {
		t.Fatal("custom relation not merged")
	}
}

func TestMergeValidatesOverlay(t *testing.T) {
	ov, err := Parse([]byte("types: {x: {parents: [nowhere]}}"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Builtin().Merge(ov); err == nil {
		t.Fatal("dangling parent accepted after merge")
	}
}

func TestMergeRefusesKindChange(t *testing.T) {
	ov := &Registry{Types: map[string]*Type{"device": {Attributes: map[string]*Attribute{"serial": {Name: "serial", Kind: Int}}}}}
	if _, err := Builtin().Merge(ov); err == nil || !strings.Contains(err.Error(), "cannot change kind") {
		t.Fatalf("got %v", err)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		kind Kind
		in   any
		want any
		bad  bool
	}{
		{MAC, "AA-BB-CC-DD-EE-FF", "aa:bb:cc:dd:ee:ff", false},
		{MAC, "aabb.ccdd.eeff", "aa:bb:cc:dd:ee:ff", false},
		{MAC, "AABBCCDDEEFF", "aa:bb:cc:dd:ee:ff", false},
		{MAC, "zz:bb:cc:dd:ee:ff", nil, true},
		{IP, "192.0.2.10/24", "192.0.2.10", false},
		{IP, "::ffff:10.0.0.1", "10.0.0.1", false},
		{IP, "nope", nil, true},
		{Prefix, "192.0.2.7/24", "192.0.2.0/24", false},
		{Int, float64(3), int64(3), false},
		{Int, 3.5, nil, true},
		{Int, "3", nil, true},
		{Float, 2, float64(2), false},
		{Bool, "true", nil, true},
		{Time, "2026-03-03", "2026-03-03T00:00:00Z", false},
		{Time, "yesterday", nil, true},
		{String, 42, "42", false},
	}
	for _, c := range cases {
		got, err := Normalize(&Attribute{Name: "x", Kind: c.kind}, c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%s %v: want error, got %v", c.kind, c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s %v: got %v, %v; want %v", c.kind, c.in, got, err, c.want)
		}
	}
	got, err := Normalize(&Attribute{Name: "e", Kind: Enum, Values: []string{"on", "off"}}, "ON")
	if err != nil || got != "on" {
		t.Errorf("enum: %v %v", got, err)
	}
	if _, err := Normalize(&Attribute{Name: "e", Kind: Enum, Values: []string{"on"}}, "maybe"); err == nil {
		t.Error("enum accepted an unknown value")
	}
	l, err := Normalize(&Attribute{Name: "l", Kind: List}, []any{"b", "a"})
	if err != nil || strings.Join(l.([]string), ",") != "a,b" {
		t.Errorf("list: %v %v", l, err)
	}
}

func TestNormalizeIdentity(t *testing.T) {
	dev := Builtin().Types["device"]
	if v, _ := dev.NormalizeIdentity("name", " SW-A1 "); v != "sw-a1" {
		t.Errorf("name: %q", v)
	}
	if v, _ := dev.NormalizeIdentity("base_mac", "00-1C-73-AA-BB-CC"); v != "00:1c:73:aa:bb:cc" {
		t.Errorf("mac: %q", v)
	}
	if _, err := dev.NormalizeIdentity("base_mac", "garbage"); err == nil {
		t.Error("bad mac accepted")
	}
}

func TestAllows(t *testing.T) {
	r := Builtin()
	if !r.Relations["mounted_in"].Allows("device", "rack") {
		t.Error("device mounted_in rack refused")
	}
	if r.Relations["mounted_in"].Allows("rack", "device") {
		t.Error("rack mounted_in device accepted")
	}
	if !r.Relations["depends_on"].Allows("application", "device") {
		t.Error("wildcard refused")
	}
	if !r.Types["interface"].AllowsParent("device") || r.Types["interface"].AllowsParent("rack") {
		t.Error("interface parents")
	}
}
