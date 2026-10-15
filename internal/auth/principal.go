// Package auth authenticates callers (OIDC bearer tokens and scoped API tokens), authorises
// them (RBAC, deny by default) and provides the net/http middleware the REST API and the MCP
// server mount: bearer authentication, a per-action guard and a per-principal rate limiter.
// See docs/adr/0013-authentication-and-audit.md.
package auth

import (
	"context"
	"errors"
	"slices"
)

// Kind tells how a principal authenticated.
type Kind string

// The two kinds of principal.
const (
	KindUser  Kind = "user"  // an OIDC identity
	KindToken Kind = "token" // a scoped API token
)

// Errors shared by the authentication and authorisation paths.
var (
	// ErrUnauthenticated means no valid credential was presented (HTTP 401).
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	// ErrForbidden means the principal is known and the policy refuses (HTTP 403).
	ErrForbidden = errors.New("auth: forbidden")
)

// Principal is who calls. It is built by authentication, never from request data other than
// the verified credential.
type Principal struct {
	// Subject is "user:<sub>" for an OIDC identity and "token:<id>" for an API token. It is
	// what the audit log records as the actor.
	Subject string
	Kind    Kind
	// Roles are the RBAC roles: mapped from the OIDC groups claim for a user, a snapshot of
	// the owner's roles at creation time for a token.
	Roles []string
	// Groups are the raw OIDC groups (users only), kept for display and audit.
	Groups []string
	// Scopes are the token's scopes (tokens only). They can only narrow what Roles grant.
	Scopes []string
	// Types and Sites restrict a token to object types and sites; empty means no restriction.
	Types []string
	Sites []string
	// Source is the source a token is bound to (tokens only, optional, ADR 0028).
	Source string
	// Owner is the user that created the token (tokens only).
	Owner string
}

// HasRole reports whether the principal holds role.
func (p *Principal) HasRole(role string) bool {
	return p != nil && slices.Contains(p.Roles, role)
}

type ctxKey int

const (
	principalKey ctxKey = iota
	policyKey
	slotKey
)

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// FromContext returns the authenticated principal, if any.
func FromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey).(*Principal)
	return p, ok && p != nil
}

// Slot lets an outer middleware (the audit one) learn what an inner one decided: a request
// context only flows inwards, so the outer middleware puts a Slot in and reads it after.
type Slot struct {
	// Principal is set by Middleware on successful authentication.
	Principal *Principal
	// Reason says why authentication or authorisation failed. It is for the audit log only and
	// never reaches the client.
	Reason string
}

// WithSlot returns ctx carrying a new Slot, and the Slot.
func WithSlot(ctx context.Context) (context.Context, *Slot) {
	s := &Slot{}
	return context.WithValue(ctx, slotKey, s), s
}

func slotFrom(ctx context.Context) *Slot {
	s, _ := ctx.Value(slotKey).(*Slot)
	return s
}

func recordReason(ctx context.Context, reason string) {
	if s := slotFrom(ctx); s != nil {
		s.Reason = reason
	}
}
