package auth

import (
	"strings"
	"testing"
)

func user(roles ...string) *Principal {
	return &Principal{Subject: "user:alice", Kind: KindUser, Roles: roles}
}

func tokenP(roles, scopes []string) *Principal {
	return &Principal{Subject: "token:1", Kind: KindToken, Roles: roles, Scopes: scopes}
}

func TestDefaultRoles(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		role   string
		action Action
		want   bool
	}{
		{"viewer", ActionRead, true},
		{"viewer", ActionPropose, false},
		{"viewer", ActionApprove, false},
		{"viewer", ActionAdmin, false},
		{"operator", ActionRead, true},
		{"operator", ActionPropose, true},
		{"operator", ActionApprove, false},
		{"operator", ActionAdmin, false},
		{"admin", ActionRead, true},
		{"admin", ActionPropose, true},
		{"admin", ActionApprove, true},
		{"admin", ActionAdmin, true},
	}
	for _, c := range cases {
		if got := p.Allow(user(c.role), c.action, "device", "paris-1"); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.role, c.action, got, c.want)
		}
	}
}

func TestDenyByDefault(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		name string
		p    *Policy
		pr   *Principal
		a    Action
	}{
		{"nil principal", p, nil, ActionRead},
		{"no roles", p, user(), ActionRead},
		{"unknown role", p, user("superuser"), ActionRead},
		{"unknown action", p, user("admin"), Action("delete")},
		{"empty action", p, user("admin"), Action("")},
		{"nil policy", nil, user("admin"), ActionRead},
		{"zero policy", &Policy{}, user("admin"), ActionRead},
		// A role built in code, bypassing ParsePolicy's validation, that lists an action the
		// package does not define: still denied.
		{"unvalidated role with unknown action", &Policy{roles: map[string]Role{"x": {Actions: []Action{"delete"}}}}, user("x"), Action("delete")},
		{"token without scopes", p, tokenP([]string{"admin"}, nil), ActionRead},
		{"token without roles", p, tokenP(nil, []string{ScopeAdmin}), ActionRead},
	}
	for _, c := range cases {
		if c.p.Allow(c.pr, c.a, "device", "s1") {
			t.Errorf("%s: allowed, want denied", c.name)
		}
	}
}

func TestTokenScopeCannotExceedOwnerRole(t *testing.T) {
	p := DefaultPolicy()
	// An admin scope on a viewer's token grants nothing beyond what a viewer can do.
	viewerAdminScope := tokenP([]string{"viewer"}, []string{ScopeAdmin})
	if !p.Allow(viewerAdminScope, ActionRead, "device", "s1") {
		t.Error("viewer token with admin scope should still read")
	}
	for _, a := range []Action{ActionPropose, ActionApprove, ActionAdmin} {
		if p.Allow(viewerAdminScope, a, "device", "s1") {
			t.Errorf("viewer token with admin scope was allowed %s", a)
		}
	}
	// And the other way round: a read scope narrows an admin.
	adminReadScope := tokenP([]string{"admin"}, []string{ScopeRead})
	if !p.Allow(adminReadScope, ActionRead, "device", "s1") {
		t.Error("admin token with read scope should read")
	}
	for _, a := range []Action{ActionPropose, ActionApprove, ActionAdmin} {
		if p.Allow(adminReadScope, a, "device", "s1") {
			t.Errorf("admin token with read scope was allowed %s", a)
		}
	}
	// The write scope proposes but does not approve, even for an admin.
	adminWrite := tokenP([]string{"admin"}, []string{ScopeWrite})
	if !p.Allow(adminWrite, ActionPropose, "device", "s1") || p.Allow(adminWrite, ActionApprove, "device", "s1") {
		t.Error("write scope must propose and not approve")
	}
}

func TestTokenTypeAndSiteRestrictions(t *testing.T) {
	p := DefaultPolicy()
	tok := tokenP([]string{"admin"}, []string{ScopeAdmin})
	tok.Types, tok.Sites = []string{"device"}, []string{"paris-1"}
	cases := []struct {
		typ, site string
		want      bool
	}{
		{"device", "paris-1", true},
		{"rack", "paris-1", false},
		{"device", "lyon-2", false},
		{"", "paris-1", false}, // an unscoped request cannot prove it stays inside
		{"device", "", false},
	}
	for _, c := range cases {
		if got := p.Allow(tok, ActionRead, c.typ, c.site); got != c.want {
			t.Errorf("type %q site %q = %v, want %v", c.typ, c.site, got, c.want)
		}
	}
}

func TestParsePolicy(t *testing.T) {
	const doc = `
roles:
  site-operator:
    actions: [read, propose]
    types: [device]
    sites: [paris-1]
  viewer:
    actions: [read, approve]
group_roles:
  trica-ops: [site-operator]
  trica-all: [viewer, admin]
`
	p, err := ParsePolicy(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	op := user("site-operator")
	if !p.Allow(op, ActionPropose, "device", "paris-1") {
		t.Error("site-operator should propose a device in paris-1")
	}
	if p.Allow(op, ActionPropose, "device", "lyon-2") || p.Allow(op, ActionPropose, "rack", "paris-1") {
		t.Error("site-operator is restricted to device in paris-1")
	}
	if !p.Allow(user("viewer"), ActionApprove, "x", "y") {
		t.Error("the YAML viewer role replaces the default one")
	}
	if !p.Allow(user("admin"), ActionAdmin, "x", "y") {
		t.Error("default roles that the YAML does not mention stay")
	}
	if got := p.GroupRoles["trica-ops"]; len(got) != 1 || got[0] != "site-operator" {
		t.Errorf("group roles = %v", p.GroupRoles)
	}
	if names := strings.Join(p.RoleNames(), ","); names != "admin,operator,site-operator,viewer" {
		t.Errorf("roles = %s", names)
	}
}

func TestParsePolicyRejects(t *testing.T) {
	cases := map[string]string{
		"unknown action":     "roles:\n  r:\n    actions: [read, delete]\n",
		"no actions":         "roles:\n  r:\n    actions: []\n",
		"unknown field":      "roles:\n  r:\n    actions: [read]\n    typos: [x]\n",
		"unknown top field":  "rolez: {}\n",
		"group to no role":   "group_roles:\n  g: [ghost]\n",
		"malformed yaml":     "roles: [",
		"empty role name":    "roles:\n  \"\":\n    actions: [read]\n",
		"restrict wrong typ": "roles:\n  r:\n    actions: read\n",
	}
	for name, doc := range cases {
		if _, err := ParsePolicy(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if p, err := ParsePolicy(strings.NewReader("")); err != nil || !p.Allow(user("admin"), ActionAdmin, "", "") {
		t.Errorf("an empty file gives the default policy: %v", err)
	}
}

func TestCanGrant(t *testing.T) {
	p := DefaultPolicy()
	if !p.CanGrant([]string{"operator"}, ActionPropose) || p.CanGrant([]string{"operator"}, ActionApprove) || p.CanGrant(nil, ActionRead) {
		t.Error("CanGrant disagrees with the default roles")
	}
}
