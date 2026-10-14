package reconcile_test

import (
	"context"
	"errors"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestVersionPolicySelectorValidatedOnWrite(t *testing.T) {
	cases := []struct {
		name     string
		selector any
		valid    bool
	}{
		{"field predicate", "vendor:arista", true},
		{"several predicates", "type:device role:switch", true},
		{"the former schema example", "device where vendor = 'Arista'", false},
		{"free text only", "arista", false},
		{"predicate plus stray text", "device vendor:arista", false},
		{"unbalanced parenthesis", "(vendor:arista", false},
		{"missing value", "vendor:", false},
		{"sort directive", "vendor:arista sort:display", false},
		{"not a string", 42, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, s, c := newEngine(t, reconcile.Policy{})
			ctx := context.Background()
			b := batch(c, "declare:policies", sdk.ScopePartial)
			b.Object("p", "version_policy").Identify("name", "eos").Set("software", "eos").
				Set("reference_version", "4.20.7M").Set("selector", tc.selector)
			_, err := e.Apply(ctx, b, "", "")
			_, n, _ := s.List(ctx, store.ListFilter{Type: "version_policy"})
			if tc.valid {
				if err != nil || n != 1 {
					t.Fatalf("valid selector refused: %v (policies %d)", err, n)
				}
				return
			}
			if !errors.Is(err, reconcile.ErrInvalid) || n != 0 {
				t.Fatalf("bad selector accepted: err = %v, policies %d", err, n)
			}
		})
	}
}
