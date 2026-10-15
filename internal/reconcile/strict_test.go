package reconcile_test

import (
	"context"
	"errors"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// badEnum describes a valid device and an interface with an invalid enum value.
func badEnum(c *clock, source string) *sdk.Batch {
	b := batch(c, source, sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "S1").Identify("name", "sw1")
	b.Object("e1", "interface").Identify("name", "Ethernet1").In("sw").Set("oper_status", "sideways")
	return b
}

// badRelation uses a relation type the schema does not know.
func badRelation(c *clock, source string) *sdk.Batch {
	b := batch(c, source, sdk.ScopePartial)
	b.Object("a", "device").Identify("serial", "A")
	b.Object("b", "device").Identify("serial", "B")
	b.Relate("teleports_to", "a", "b")
	return b
}

func TestStrictValidationForAPICallers(t *testing.T) {
	cases := []struct {
		name   string
		build  func(*clock, string) *sdk.Batch
		source string
		reject bool
	}{
		{"user, invalid enum", badEnum, "user:v", true},
		{"token, invalid enum", badEnum, "token:ci", true},
		{"user, unknown relation type", badRelation, "user:v", true},
		{"token, unknown relation type", badRelation, "token:ci", true},
		{"unbound token, invalid enum", badEnum, "api:token-7", true},
		{"unbound token, unknown relation type", badRelation, "api:token-7", true},
		{"collector, invalid enum", badEnum, "arista:sw1", false},
		{"collector, unknown relation type", badRelation, "arista:sw1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, s, c := newEngine(t, reconcile.Policy{})
			ctx := context.Background()
			res, err := e.Apply(ctx, tc.build(c, tc.source), "", "")
			_, n, _ := s.List(ctx, store.ListFilter{Type: "device"})
			if tc.reject {
				if !errors.Is(err, reconcile.ErrRejected) {
					t.Fatalf("err = %v, want ErrRejected", err)
				}
				if n != 0 {
					t.Fatalf("%d devices applied from a rejected batch", n)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Warnings) != 1 || n == 0 {
				t.Fatalf("collector batch should apply with one warning: warnings %v, devices %d", res.Warnings, n)
			}
		})
	}
}

func TestStrictValidationAppliesToProposals(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{Propose: []string{"user:*"}})
	if _, err := e.Submit(context.Background(), badEnum(c, "user:v"), "v", ""); !errors.Is(err, reconcile.ErrRejected) {
		t.Fatalf("proposal with an invalid enum: %v", err)
	}
}
