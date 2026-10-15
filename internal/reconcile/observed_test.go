package reconcile_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestObservedAtAgeBound(t *testing.T) {
	policy := reconcile.Policy{Propose: []string{"mcp:*"}, MaxObservedAge: map[string]time.Duration{"redfish": time.Hour}}
	e, s, c := newEngine(t, policy)
	ctx := context.Background()
	old := func(source string, age time.Duration) *sdk.Batch {
		b := batch(c, source, sdk.ScopePartial)
		b.ObservedAt = c.t.Add(-age)
		b.Object("srv", "device").Identify("name", "srv-"+age.String())
		return b
	}
	cases := []struct {
		name   string
		source string
		age    time.Duration
		reject bool
	}{
		{"user within 15 minutes", "user:v", 10 * time.Minute, false},
		{"user older than 15 minutes", "user:v", 30 * time.Minute, true},
		{"token older than 15 minutes", "token:ci", 2 * time.Hour, true},
		{"unbound token older than 15 minutes", "api:token-7", 2 * time.Hour, true},
		{"collector replaying a spool", "arista:sw1", 3 * 24 * time.Hour, false},
		{"collector older than a week", "arista:sw1", 8 * 24 * time.Hour, true},
		{"per-kind override rejects", "redfish:bmc1", 2 * time.Hour, true},
		{"per-kind override accepts", "redfish:bmc1", 30 * time.Minute, false},
	}
	accepted := 0
	for _, tc := range cases {
		_, err := e.Apply(ctx, old(tc.source, tc.age), "", "")
		if tc.reject != errors.Is(err, reconcile.ErrRejected) || (!tc.reject && err != nil) {
			t.Errorf("%s: err = %v, reject = %v", tc.name, err, tc.reject)
		}
		if !tc.reject {
			accepted++
		}
	}
	// Rejected batches leave nothing behind.
	if _, n, _ := s.List(ctx, store.ListFilter{Type: "device"}); n != accepted {
		t.Errorf("devices = %d, want %d", n, accepted)
	}
	// A proposal is checked when proposed, not when approved days later.
	if _, err := e.Submit(ctx, old("mcp:assistant", 30*24*time.Hour), "claude", ""); !errors.Is(err, reconcile.ErrRejected) {
		t.Errorf("stale proposal: %v", err)
	}
	b := old("mcp:assistant", 0)
	b.Confidence = sdk.Declared
	r, err := e.Submit(ctx, b, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	c.advance(48 * time.Hour)
	if _, err := e.Approve(ctx, r.ProposalID, "vincent"); err != nil {
		t.Errorf("approving a proposal that is old by now: %v", err)
	}
	// A future observed_at is still clamped, not rejected.
	f := batch(c, "user:v", sdk.ScopePartial)
	f.ObservedAt = c.t.Add(time.Hour)
	f.Object("x", "device").Identify("name", "future")
	if _, err := e.Apply(ctx, f, "", ""); err != nil {
		t.Errorf("future observed_at: %v", err)
	}
}
