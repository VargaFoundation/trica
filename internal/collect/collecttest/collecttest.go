// Package collecttest helps test collectors against their fakes: it runs a
// collector, keeps the batches it emits, and checks them against the built-in
// schema. The engine drops an attribute the schema does not know, with only a
// warning; CheckSchema turns that into a test failure, so a collector cannot
// report data that silently vanishes.
package collecttest

import (
	"context"
	"sort"
	"testing"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Run runs c once and returns the batches it emitted. Each batch must pass
// sdk validation and CheckSchema.
func Run(t testing.TB, c sdk.Collector) []*sdk.Batch {
	t.Helper()
	var out []*sdk.Batch
	err := c.Collect(context.Background(), sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
		if err := b.Validate(); err != nil {
			t.Errorf("invalid batch: %v", err)
		}
		CheckSchema(t, b)
		out = append(out, b)
		return nil
	}))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return out
}

// CheckSchema fails t for every type, attribute, identity key, parent or
// relation of b that the built-in schema does not accept.
func CheckSchema(t testing.TB, b *sdk.Batch) {
	t.Helper()
	reg := schema.Builtin()
	types := map[string]string{}
	for _, o := range b.Objects {
		types[o.Ref] = o.Type
	}
	for _, o := range b.Objects {
		ty, err := reg.Type(o.Type)
		if err != nil {
			t.Errorf("%s: object %s: %v", b.Source, o.Ref, err)
			continue
		}
		for k, v := range o.Identity {
			if k == sdk.IDKey {
				continue
			}
			a, ok := ty.Attributes[k]
			if !ok || a.Identity == "" {
				t.Errorf("%s: object %s (%s): %q is not an identity key", b.Source, o.Ref, o.Type, k)
				continue
			}
			if _, err := ty.NormalizeIdentity(k, v); err != nil {
				t.Errorf("%s: object %s (%s): identity %s=%q: %v", b.Source, o.Ref, o.Type, k, v, err)
			}
		}
		for k, v := range o.Attributes {
			a, ok := ty.Attributes[k]
			if !ok {
				t.Errorf("%s: object %s (%s): unknown attribute %q", b.Source, o.Ref, o.Type, k)
				continue
			}
			if _, err := schema.Normalize(a, v); err != nil {
				t.Errorf("%s: object %s (%s): attribute %s=%v: %v", b.Source, o.Ref, o.Type, k, v, err)
			}
		}
		if o.Parent != "" && !ty.AllowsParent(types[o.Parent]) {
			t.Errorf("%s: object %s (%s): parent type %s not allowed", b.Source, o.Ref, o.Type, types[o.Parent])
		}
	}
	for _, r := range append(append([]*sdk.Relation(nil), b.Relations...), b.Unrelations...) {
		rt, err := reg.Relation(r.Type)
		if err != nil {
			t.Errorf("%s: %v", b.Source, err)
			continue
		}
		from, to := endpointType(types, r.From), endpointType(types, r.To)
		if !rt.Allows(from, to) {
			t.Errorf("%s: relation %s from %s to %s not allowed", b.Source, r.Type, from, to)
		}
		for k, v := range r.Attributes {
			a, ok := rt.Attributes[k]
			if !ok {
				t.Errorf("%s: relation %s: unknown attribute %q", b.Source, r.Type, k)
				continue
			}
			if _, err := schema.Normalize(a, v); err != nil {
				t.Errorf("%s: relation %s: attribute %s=%v: %v", b.Source, r.Type, k, v, err)
			}
		}
	}
}

func endpointType(types map[string]string, e sdk.Endpoint) string {
	if e.Ref != "" {
		return types[e.Ref]
	}
	return e.Type
}

// Objects returns the objects of type typ across batches, sorted by ref.
func Objects(batches []*sdk.Batch, typ string) []*sdk.Object {
	var out []*sdk.Object
	for _, b := range batches {
		for _, o := range b.Objects {
			if o.Type == typ {
				out = append(out, o)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// Find returns the first object of type typ whose identity key equals value.
func Find(batches []*sdk.Batch, typ, key, value string) *sdk.Object {
	for _, o := range Objects(batches, typ) {
		if o.Identity[key] == value {
			return o
		}
	}
	return nil
}

// Relations returns the relations of type typ across batches.
func Relations(batches []*sdk.Batch, typ string) []*sdk.Relation {
	var out []*sdk.Relation
	for _, b := range batches {
		for _, r := range b.Relations {
			if r.Type == typ {
				out = append(out, r)
			}
		}
	}
	return out
}
