package reconcile_test

import (
	"context"
	"errors"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// addresses reports the same private address seen by two sources, each in the
// routing domain it gives ("" = none).
func addresses(t *testing.T, domainA, domainB string) (string, string, *store.Store) {
	t.Helper()
	e, s, c := newEngine(t, reconcile.Policy{})
	ids := make([]string, 2)
	for i, d := range []string{domainA, domainB} {
		b := batch(c, []string{"agent:a", "agent:b"}[i], sdk.ScopePartial)
		o := b.Object("ip", "ip_address").Identify("address", "10.0.0.1")
		if d != "" {
			o.Identify("domain", d)
		}
		ids[i] = apply(t, e, b).Objects["ip"]
	}
	return ids[0], ids[1], s
}

func TestIdentityScopeSeparatesOverlappingAddresses(t *testing.T) {
	cases := []struct {
		name, a, b string
		same       bool
	}{
		{"two domains", "site-a", "site-b", false},
		{"one domain, spelt differently", "Site-A", " site-a ", true},
		{"no domain on either side", "", "", true},
		{"a domain and the global scope", "site-a", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, _ := addresses(t, tc.a, tc.b)
			if (a == b) != tc.same {
				t.Fatalf("objects %s and %s: merged = %v, want %v", a, b, a == b, tc.same)
			}
		})
	}
}

func TestIdentityScopeIsAnAttribute(t *testing.T) {
	a, _, s := addresses(t, "Site-A", "site-b")
	o, err := s.Object(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if o.Attrs["domain"] != "site-a" || o.Attrs["address"] != "10.0.0.1" {
		t.Fatalf("attributes %v, want the normalised domain and the address", o.Attrs)
	}
}

func TestIdentityScopeResolvesEndpoints(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	b := batch(c, "agent:a", sdk.ScopePartial)
	b.Object("a", "ip_address").Identify("address", "10.0.0.1").Identify("domain", "site-a")
	b.Object("b", "ip_address").Identify("address", "10.0.0.1").Identify("domain", "site-b")
	ips := apply(t, e, b).Objects

	b = batch(c, "agent:h", sdk.ScopePartial)
	b.Object("h", "device").Identify("serial", "H1")
	b.RelateEndpoints("talks_to", sdk.Ref("h"), sdk.Endpoint{Type: "ip_address", Identity: map[string]string{"address": "10.0.0.1", "domain": "site-b"}})
	host := apply(t, e, b).Objects["h"]

	rels, err := s.RelationsOf(ctx, host, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].ToID != ips["b"] {
		t.Fatalf("relations %+v, want one to %s (site-b), not %s (site-a)", rels, ips["b"], ips["a"])
	}
}

func TestIdentityScopeErrors(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()

	b := batch(c, "agent:a", sdk.ScopePartial)
	b.Object("ip", "ip_address").Identify("address", "10.0.0.1").Identify("domain", "site a/b")
	if _, err := e.Apply(ctx, b, "", ""); !errors.Is(err, reconcile.ErrInvalid) {
		t.Fatalf("invalid domain: %v, want ErrInvalid", err)
	}

	// Given as a plain value, the domain would not be part of the identity: it
	// is refused rather than stored on a global address.
	b = batch(c, "agent:a", sdk.ScopePartial)
	b.Object("ip", "ip_address").Identify("address", "10.0.0.1").Set("domain", "site-a")
	r := apply(t, e, b)
	if len(r.Warnings) != 1 {
		t.Fatalf("warnings %v, want one about the domain", r.Warnings)
	}
	o, err := s.Object(ctx, r.Objects["ip"])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := o.Attrs["domain"]; ok {
		t.Fatalf("domain stored from a plain value: %v", o.Attrs)
	}
}
