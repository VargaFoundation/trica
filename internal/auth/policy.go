package auth

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"go.yaml.in/yaml/v3"
)

// Action is what a principal asks to do.
type Action string

// The four actions. Writes to the inventory are never direct (ADR 0005, 0009): "propose"
// submits a batch that the reconciliation policy applies or queues.
const (
	ActionRead    Action = "read"
	ActionPropose Action = "propose"
	ActionApprove Action = "approve"
	ActionAdmin   Action = "admin"
)

// Actions lists every action.
var Actions = []Action{ActionRead, ActionPropose, ActionApprove, ActionAdmin}

// Token scopes. A scope is a ceiling: it can only narrow what the owner's roles grant.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
)

// Scopes lists every token scope.
var Scopes = []string{ScopeRead, ScopeWrite, ScopeAdmin}

// scopeActions maps a token scope to the actions it allows.
var scopeActions = map[string][]Action{
	ScopeRead:  {ActionRead},
	ScopeWrite: {ActionRead, ActionPropose},
	ScopeAdmin: {ActionRead, ActionPropose, ActionApprove, ActionAdmin},
}

// ScopeAllows reports whether any of scopes allows action.
func ScopeAllows(scopes []string, action Action) bool {
	for _, s := range scopes {
		if slices.Contains(scopeActions[s], action) {
			return true
		}
	}
	return false
}

// Role is a named set of actions, optionally limited to object types and sites. An empty
// Types or Sites means no restriction on that axis.
type Role struct {
	Actions []Action `yaml:"actions"`
	Types   []string `yaml:"types,omitempty"`
	Sites   []string `yaml:"sites,omitempty"`
}

func (r Role) allows(action Action, objectType, site string) bool {
	return slices.Contains(r.Actions, action) && within(r.Types, objectType) && within(r.Sites, site)
}

// within is true when no restriction is set, or v is in the restriction. A restricted
// principal asking about "no type" or "no site" (an unscoped listing) is refused: the caller
// must name what it touches.
func within(restriction []string, v string) bool {
	return len(restriction) == 0 || (v != "" && slices.Contains(restriction, v))
}

// Policy answers authorisation questions. The zero value denies everything.
type Policy struct {
	roles map[string]Role
	// GroupRoles maps an OIDC group to roles; wire it into OIDCConfig.GroupRoles.
	GroupRoles map[string][]string
}

// DefaultPolicy has three roles: viewer reads, operator reads and proposes, admin does
// everything including approving proposals and administering tokens.
func DefaultPolicy() *Policy {
	return &Policy{roles: map[string]Role{
		"viewer":   {Actions: []Action{ActionRead}},
		"operator": {Actions: []Action{ActionRead, ActionPropose}},
		"admin":    {Actions: []Action{ActionRead, ActionPropose, ActionApprove, ActionAdmin}},
	}}
}

// policyFile is the YAML form:
//
//	roles:
//	  site-operator:
//	    actions: [read, propose]
//	    types: [device, interface]
//	    sites: [paris-1]
//	group_roles:
//	  trica-admins: [admin]
type policyFile struct {
	Roles      map[string]Role     `yaml:"roles"`
	GroupRoles map[string][]string `yaml:"group_roles"`
}

// ParsePolicy reads a YAML policy on top of the default roles: a role of the same name
// replaces the default one, a new name adds a role. Unknown fields, unknown actions and
// groups mapped to unknown roles are errors, so a typo cannot silently widen or drop access.
func ParsePolicy(r io.Reader) (*Policy, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("auth: read policy: %w", err)
	}
	var f policyFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("auth: parse policy: %w", err)
	}
	p := DefaultPolicy()
	for name, role := range f.Roles {
		if name == "" {
			return nil, errors.New("auth: policy: empty role name")
		}
		if len(role.Actions) == 0 {
			return nil, fmt.Errorf("auth: policy: role %q has no actions", name)
		}
		for _, a := range role.Actions {
			if !slices.Contains(Actions, a) {
				return nil, fmt.Errorf("auth: policy: role %q: unknown action %q", name, a)
			}
		}
		p.roles[name] = role
	}
	for group, roles := range f.GroupRoles {
		for _, role := range roles {
			if _, ok := p.roles[role]; !ok {
				return nil, fmt.Errorf("auth: policy: group %q maps to unknown role %q", group, role)
			}
		}
	}
	p.GroupRoles = f.GroupRoles
	return p, nil
}

// RoleNames lists the defined roles, sorted.
func (p *Policy) RoleNames() []string {
	names := make([]string, 0, len(p.roles))
	for n := range p.roles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Allow answers whether principal may perform action on an object of objectType at site.
// Deny by default: a nil principal, an unknown action or role, a missing grant all say no.
//
// A token must pass twice. Its scopes must allow the action and its type/site restrictions
// must contain the object (it can only narrow), and the roles it was created with must grant
// it too (the scopes can never exceed the owner's roles).
func (p *Policy) Allow(principal *Principal, action Action, objectType, site string) bool {
	if p == nil || principal == nil || !slices.Contains(Actions, action) {
		return false
	}
	if principal.Kind == KindToken {
		if !ScopeAllows(principal.Scopes, action) || !within(principal.Types, objectType) || !within(principal.Sites, site) {
			return false
		}
	}
	for _, name := range principal.Roles {
		if role, ok := p.roles[name]; ok && role.allows(action, objectType, site) {
			return true
		}
	}
	return false
}

// CanGrant reports whether the roles can perform action at all, on some object. It is what
// token creation checks: a scope is only issued to an owner whose roles cover it.
func (p *Policy) CanGrant(roles []string, action Action) bool {
	for _, name := range roles {
		if role, ok := p.roles[name]; ok && slices.Contains(role.Actions, action) {
			return true
		}
	}
	return false
}
