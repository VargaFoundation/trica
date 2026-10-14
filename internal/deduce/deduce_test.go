package deduce

import (
	"reflect"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/store"
)

// inv builds a small inventory: devices, their ports, relations.
type inv struct {
	objs []*store.Object
	rels []store.Relation
}

func (v *inv) device(id string) *inv {
	v.objs = append(v.objs, &store.Object{ID: id, Type: "device", Status: "active", Attrs: map[string]any{}})
	return v
}

func (v *inv) vm(id string) *inv {
	v.objs = append(v.objs, &store.Object{ID: id, Type: "vm", Status: "active", Attrs: map[string]any{}})
	return v
}

// port adds an interface; attrs are key, value pairs.
func (v *inv) port(id, parent string, attrs ...any) *inv {
	o := &store.Object{ID: id, Type: "interface", ParentID: &parent, Status: "active", Attrs: map[string]any{}}
	for i := 0; i+1 < len(attrs); i += 2 {
		o.Attrs[attrs[i].(string)] = attrs[i+1]
	}
	v.objs = append(v.objs, o)
	return v
}

func (v *inv) rel(typ, from, to, source string) *inv {
	v.rels = append(v.rels, store.Relation{Type: typ, FromID: from, ToID: to, Source: source})
	return v
}

func (v *inv) lldp(from, to string) *inv { return v.rel("neighbor_of", from, to, "arista:x") }

// cable documents a cable between two ports.
func (v *inv) cable(id, a, b string) *inv {
	v.objs = append(v.objs, &store.Object{ID: id, Type: "cable", Status: "active", Attrs: map[string]any{}})
	return v.rel("connects", id, a, "file:site").rel("connects", id, b, "file:site")
}

func learned(macs ...string) []any {
	out := make([]any, len(macs))
	for i, m := range macs {
		out[i] = "10/" + m
	}
	return out
}

// base: two switches s1, s2 and two servers h1, h2.
func base() *inv {
	v := &inv{}
	return v.device("s1").device("s2").device("h1").device("h2").
		port("s1e1", "s1").port("s1e2", "s1").port("s1e3", "s1").port("s1e4", "s1").
		port("s2e1", "s2").port("s2e2", "s2").
		port("h1n1", "h1", "mac", "02:00:00:00:01:01").port("h1n2", "h1", "mac", "02:00:00:00:01:02").
		port("h2n1", "h2", "mac", "02:00:00:00:02:01")
}

func cables(p *Plan) []Cable { return p.Cables }

func TestDeduceCables(t *testing.T) {
	cases := []struct {
		name string
		v    *inv
		want []Cable
		skip int
	}{
		{"lldp both ends", base().lldp("s1e1", "s2e1").lldp("s2e1", "s1e1"),
			[]Cable{{"s1e1", "s2e1", EvidenceLLDP, LevelHigh}}, 0},
		{"lldp one end", base().lldp("s2e1", "s1e1"),
			[]Cable{{"s1e1", "s2e1", EvidenceLLDPOne, LevelMedium}}, 0},
		{"lldp self loop ignored", base().lldp("s1e1", "s1e1"), nil, 0},
		{"lldp hub: two neighbours on one port", base().lldp("s1e1", "s2e1").lldp("s1e1", "s2e2"),
			nil, 3},
		{"two mutual neighbours on one port", base().lldp("s1e1", "s2e1").lldp("s2e1", "s1e1").lldp("s1e1", "s2e2").lldp("s2e2", "s1e1"),
			nil, 3},
		{"mutual wins over a stale one-sided entry", base().lldp("s1e1", "s2e1").lldp("s2e1", "s1e1").lldp("s1e2", "s2e1"),
			[]Cable{{"s1e1", "s2e1", EvidenceLLDP, LevelHigh}}, 1},
		{"mac table: one host port", base().port("s1e9", "s1", "learned_macs", learned("02-00-00-00-01-01")),
			[]Cable{{"h1n1", "s1e9", EvidenceMACTable, LevelLow}}, 0},
		{"mac table: unknown address", base().port("s1e9", "s1", "learned_macs", learned("02:00:00:00:09:09")), nil, 0},
		{"mac table: malformed entry", base().port("s1e9", "s1", "learned_macs", []any{"junk", 3}), nil, 0},
		{"mac table: two hosts on one port", base().port("s1e9", "s1", "learned_macs", learned("02:00:00:00:01:01", "02:00:00:00:02:01")),
			nil, 1},
		{"mac table: aggregate port", base().port("s1po1", "s1", "kind", "lag", "learned_macs", learned("02:00:00:00:01:01")),
			nil, 1},
		{"mac table: host learned on two edge ports", base().
			port("s1e9", "s1", "learned_macs", learned("02:00:00:00:01:01")).
			port("s2e9", "s2", "learned_macs", learned("02:00:00:00:01:01")),
			nil, 2},
		{"mac table: bond, one address on two host ports", base().
			port("h2n2", "h2", "mac", "02:00:00:00:02:01").
			port("s1e9", "s1", "learned_macs", learned("02:00:00:00:02:01")),
			nil, 1},
		{"mac table: a bridge sharing the address is not a second port", base().
			port("h2br", "h2", "kind", "bridge", "mac", "02:00:00:00:02:01").
			port("s1e9", "s1", "learned_macs", learned("02:00:00:00:02:01")),
			[]Cable{{"h2n1", "s1e9", EvidenceMACTable, LevelLow}}, 0},
		{"mac table: own address ignored", base().port("s1e9", "s1", "mac", "02:00:00:00:0a:0a", "learned_macs", learned("02:00:00:00:0a:0a")),
			nil, 0},
		{"mac table: vm address is not a cable", base().vm("vm1").port("vm1n", "vm1", "mac", "02:00:00:00:0b:0b").
			port("s1e9", "s1", "learned_macs", learned("02:00:00:00:0b:0b")),
			nil, 0},
		{"mac table yields to lldp on the host port", base().lldp("h1n1", "s2e2").
			port("s1e9", "s1", "learned_macs", learned("02:00:00:00:01:01")),
			[]Cable{{"h1n1", "s2e2", EvidenceLLDPOne, LevelMedium}}, 1},
		{"mac table ignored on an lldp port", base().
			port("s1e9", "s1", "learned_macs", learned("02:00:00:00:01:01")).
			lldp("s1e9", "s2e2").lldp("s2e2", "s1e9"),
			[]Cable{{"s1e9", "s2e2", EvidenceLLDP, LevelHigh}}, 0},
		{"absent objects ignored", func() *inv {
			v := base().lldp("s1e1", "s2e1").lldp("s2e1", "s1e1")
			for _, o := range v.objs {
				if o.ID == "s2e1" {
					o.Status = "absent"
				}
			}
			return v
		}(), nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Deduce(c.v.objs, c.v.rels)
			if !reflect.DeepEqual(cables(p), c.want) {
				t.Errorf("cables = %+v, want %+v", p.Cables, c.want)
			}
			if len(p.Skipped) != c.skip {
				t.Errorf("skipped = %+v, want %d", p.Skipped, c.skip)
			}
		})
	}
}

func TestDeduceDeterministic(t *testing.T) {
	v := base().lldp("s1e1", "s2e1").lldp("s2e1", "s1e1").lldp("s2e2", "s1e2").
		port("s1e9", "s1", "learned_macs", learned("02:00:00:00:01:01"))
	first := Deduce(v.objs, v.rels)
	for range 20 {
		if got := Deduce(v.objs, v.rels); !reflect.DeepEqual(got, first) {
			t.Fatalf("pass differs: %+v vs %+v", got, first)
		}
	}
}

func TestCompareWithDocumentation(t *testing.T) {
	kinds := func(p *Plan) []string {
		var out []string
		for _, f := range p.Findings {
			out = append(out, f.Kind+":"+f.Port+"-"+f.Other)
		}
		return out
	}
	cases := []struct {
		name string
		v    *inv
		want []string
	}{
		{"documented and observed agree", base().cable("c1", "s1e1", "s2e1").lldp("s1e1", "s2e1").lldp("s2e1", "s1e1"), nil},
		{"documented ends in the other order", base().cable("c1", "s2e1", "s1e1").lldp("s1e1", "s2e1"), nil},
		{"moved: documented to s2e1, observed on s2e2", base().cable("c1", "s1e1", "s2e1").lldp("s1e1", "s2e2").lldp("s2e2", "s1e1"),
			[]string{KindConflict + ":s1e1-s2e1"}},
		{"documented, not observed on an observed device", base().cable("c1", "s1e3", "h2n1").lldp("s1e1", "s2e1"),
			// s1 has documented cables, so its undocumented LLDP cable is
			// reported too.
			[]string{KindNotObserved + ":h2n1-s1e3", KindNotDeclared + ":s1e1-s2e1"}},
		{"documented between devices nobody observes: silent", base().cable("c1", "h1n2", "h2n1"), nil},
		{"observed, not documented, on a documented device", base().cable("c1", "s1e3", "h2n1").lldp("s1e3", "h2n1").lldp("s1e1", "s2e1"),
			[]string{KindNotDeclared + ":s1e1-s2e1"}},
		{"observed on a site without documentation: silent", base().lldp("s1e1", "s2e1"), nil},
		{"deduced connects relations are not documentation", base().
			device("x").port("xe", "x").cable("c1", "s1e1", "xe").lldp("s1e1", "s2e1"),
			[]string{KindConflict + ":s1e1-xe"}},
	}
	// The last case checks the opposite too: the same relations from a
	// deduce: source document nothing.
	cases = append(cases, struct {
		name string
		v    *inv
		want []string
	}{"connects from a deduce source ignored", func() *inv {
		v := base().cable("c1", "s1e1", "s2e2").lldp("s1e1", "s2e1")
		for i := range v.rels {
			if v.rels[i].Type == "connects" {
				v.rels[i].Source = "deduce:cabling"
			}
		}
		return v
	}(), nil})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kinds(Deduce(c.v.objs, c.v.rels)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("findings = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFingerprintIsOrderFree(t *testing.T) {
	a := Finding{Kind: KindConflict, Port: "p1", Other: "p2"}.Fingerprint()
	b := Finding{Kind: KindConflict, Port: "p2", Other: "p1"}.Fingerprint()
	if a != b || !strings.HasPrefix(a, "cable:") {
		t.Errorf("fingerprints %q %q", a, b)
	}
}

func TestDeviceOfThroughModules(t *testing.T) {
	v := (&inv{}).device("s1")
	m := "s1"
	v.objs = append(v.objs, &store.Object{ID: "lc1", Type: "module", ParentID: &m, Status: "active"})
	v.port("p", "lc1").port("orphan", "missing")
	g := newGraph(v.objs)
	if g.ports["p"].device != "s1" || g.ports["orphan"].device != "" {
		t.Errorf("devices: %q %q", g.ports["p"].device, g.ports["orphan"].device)
	}
}
