package sdk

import (
	"context"
	"strings"
	"testing"
)

func TestBuilderAndValidate(t *testing.T) {
	b := NewBatch("arista:sw-a1", ScopeFull)
	sw := b.Object("sw", "device").Identify("serial", "JPE1").Identify("name", "").Set("role", "switch").Set("model", "")
	if _, ok := sw.Identity["name"]; ok {
		t.Error("empty identifier kept")
	}
	if _, ok := sw.Attributes["model"]; ok {
		t.Error("empty attribute kept")
	}
	if b.Object("sw", "device") != sw {
		t.Error("Object did not return the existing ref")
	}
	b.Object("e1", "interface").Identify("name", "Ethernet1").In("sw")
	b.Relate("contains", "sw", "e1")
	b.RelateEndpoints("neighbor_of", Endpoint{Ref: "e1"}, Endpoint{Type: "interface", Identity: map[string]string{"name": "ether1"},
		Parent: &Endpoint{Type: "device", Identity: map[string]string{"base_mac": "aa:bb:cc:dd:ee:ff"}}}).With("protocol", "lldp").With("x", nil)
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Lookup("e1"); !ok {
		t.Error("lookup")
	}
	if len(b.Relations[1].Attributes) != 1 {
		t.Error("nil relation attribute kept")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(b *Batch){
		"no source":   func(b *Batch) { b.Source = "" },
		"bad scope":   func(b *Batch) { b.Scope = "some" },
		"bad conf":    func(b *Batch) { b.Confidence = "guessed" },
		"no type":     func(b *Batch) { b.Object("x", "").Identify("name", "x") },
		"no identity": func(b *Batch) { b.Object("x", "device") },
		"bad parent":  func(b *Batch) { b.Object("x", "device").Identify("name", "x").In("nope") },
		"obj conf":    func(b *Batch) { o := b.Object("x", "device").Identify("name", "x"); o.Confidence = "meh" },
		"dup ref": func(b *Batch) {
			b.Objects = append(b.Objects, &Object{Ref: "a", Type: "device", Identity: map[string]string{"n": "1"}}, &Object{Ref: "a", Type: "device", Identity: map[string]string{"n": "1"}})
		},
		"cycle": func(b *Batch) {
			b.Object("x", "device").Identify("name", "x").In("y")
			b.Object("y", "device").Identify("name", "y").In("x")
		},
		"rel no type": func(b *Batch) { b.Object("x", "device").Identify("name", "x"); b.Relate("", "x", "x") },
		"rel bad ref": func(b *Batch) { b.Object("x", "device").Identify("name", "x"); b.Relate("contains", "x", "zz") },
		"rel bad ep": func(b *Batch) {
			b.Object("x", "device").Identify("name", "x")
			b.RelateEndpoints("contains", Endpoint{Ref: "x"}, Endpoint{Type: "device"})
		},
		"rel bad paren": func(b *Batch) {
			b.Object("x", "device").Identify("name", "x")
			b.RelateEndpoints("contains", Endpoint{Ref: "x"}, Endpoint{Type: "device", Identity: map[string]string{"name": "y"}, Parent: &Endpoint{}})
		},
	}
	for name, mutate := range cases {
		b := NewBatch("s", ScopePartial)
		mutate(b)
		if err := b.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEmitterFuncAndSortedKeys(t *testing.T) {
	var got string
	e := EmitterFunc(func(_ context.Context, b *Batch) error { got = b.Source; return nil })
	_ = e.Emit(context.Background(), NewBatch("x", ScopeFull))
	if got != "x" {
		t.Error("emitter func")
	}
	if strings.Join(SortedKeys(map[string]int{"b": 1, "a": 2}), ",") != "a,b" {
		t.Error("sorted keys")
	}
	if !Observed.Valid() || Confidence("x").Valid() {
		t.Error("confidence")
	}
}
