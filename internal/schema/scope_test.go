package schema

import (
	"testing"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestBuiltinAddressesAreScoped(t *testing.T) {
	r := Builtin()
	for _, n := range []string{"ip_address", "prefix"} {
		if s := r.Types[n].IdentityScope; s != "domain" {
			t.Errorf("%s: identity scope %q, want domain", n, s)
		}
	}
	if s := r.Types["device"].IdentityScope; s != "" {
		t.Errorf("device: identity scope %q, want none", s)
	}
}

func TestParseRejectsBadIdentityScope(t *testing.T) {
	cases := map[string]string{
		"not an attribute":   "types: {x: {identity_scope: d, attributes: {a: {kind: string, identity: strong}}}}",
		"not a string":       "types: {x: {identity_scope: d, attributes: {a: {kind: string, identity: strong}, d: {kind: int}}}}",
		"itself an identity": "types: {x: {identity_scope: d, attributes: {a: {kind: string, identity: strong}, d: {kind: string, identity: weak}}}}",
		"nothing to scope":   "types: {x: {identity_scope: d, attributes: {d: {kind: string}}}}",
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y), false); err == nil {
			t.Errorf("%s: accepted %q", name, y)
		}
	}
	if _, err := Parse([]byte("types: {x: {identity_scope: d, attributes: {a: {kind: string, identity: strong}, d: {kind: string}}}}"), false); err != nil {
		t.Errorf("valid identity scope refused: %v", err)
	}
}

func TestNormalizeScope(t *testing.T) {
	for _, c := range []struct{ in, want string }{{"", ""}, {" Site-A ", "site-a"}, {"vrf:mgmt.fr1_2", "vrf:mgmt.fr1_2"}} {
		if got, err := NormalizeScope(c.in); err != nil || got != c.want {
			t.Errorf("NormalizeScope(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	long := "a"
	for len(long) < 65 {
		long += "a"
	}
	for _, bad := range []string{"site a", "a|b", "-a", "été", long} {
		if _, err := NormalizeScope(bad); err == nil {
			t.Errorf("NormalizeScope(%q) accepted", bad)
		}
	}
}

func TestScopedIdentity(t *testing.T) {
	r := Builtin()
	ip := r.Types["ip_address"]
	if v, _ := ip.ScopedIdentity("address", "10.0.0.1", ""); v != "10.0.0.1" {
		t.Errorf("global scope: %q, want the bare address (existing objects keep their identity)", v)
	}
	if v, _ := ip.ScopedIdentity("address", "10.0.0.1", "site-a"); v != "site-a|10.0.0.1" {
		t.Errorf("scoped: %q", v)
	}
	if _, err := ip.ScopedIdentity("address", "not-an-ip", "site-a"); err == nil {
		t.Error("bad address accepted")
	}
	// A type without an identity scope ignores the one it is given.
	if v, _ := r.Types["device"].ScopedIdentity("serial", "ABC", "site-a"); v != "abc" {
		t.Errorf("unscoped type: %q", v)
	}
}

func TestScopeBatch(t *testing.T) {
	r := Builtin()
	b := sdk.NewBatch("agent:h", sdk.ScopeFull)
	b.Object("h", "device").Identify("serial", "H1")
	b.Object("own", "ip_address").Identify("address", "10.0.0.1")
	b.Object("kept", "ip_address").Identify("address", "10.0.0.2").Identify("domain", "other")
	b.Object("net", "prefix").Identify("prefix", "10.0.0.0/24")
	b.RelateEndpoints("talks_to", sdk.Ref("h"), sdk.Endpoint{Type: "ip_address", Identity: map[string]string{"address": "10.0.0.9"}})
	b.Unrelate("talks_to", sdk.Ref("h"), sdk.Endpoint{Type: "ip_address", Identity: map[string]string{"address": "10.0.0.8"}})
	b.RelateEndpoints("assigned_to", sdk.ByID("ip_address", "00000000-0000-0000-0000-000000000001"), sdk.Ref("h"))

	if err := r.ScopeBatch(b, " Site-A "); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]string{"h": "", "own": "site-a", "kept": "other", "net": "site-a"} {
		o, _ := b.Lookup(ref)
		if got := o.Identity["domain"]; got != want {
			t.Errorf("%s: domain %q, want %q", ref, got, want)
		}
	}
	// A type without an identity scope is left as it was, with no key added.
	if h, _ := b.Lookup("h"); len(h.Identity) != 1 {
		t.Errorf("device identity %v, want its serial only", h.Identity)
	}
	if d := b.Relations[0].To.Identity["domain"]; d != "site-a" {
		t.Errorf("relation endpoint: domain %q", d)
	}
	if d := b.Unrelations[0].To.Identity["domain"]; d != "site-a" {
		t.Errorf("withdrawn relation endpoint: domain %q", d)
	}
	if _, ok := b.Relations[1].From.Identity["domain"]; ok {
		t.Error("an endpoint designated by id was given a domain")
	}

	if err := r.ScopeBatch(b, "not valid"); err == nil {
		t.Error("invalid scope accepted")
	}
	empty := sdk.NewBatch("agent:h", sdk.ScopeFull)
	empty.Object("ip", "ip_address").Identify("address", "10.0.0.1")
	if err := r.ScopeBatch(empty, ""); err != nil {
		t.Fatal(err)
	}
	if o, _ := empty.Lookup("ip"); o.Identity["domain"] != "" {
		t.Error("the global scope stamped a domain")
	}
}
