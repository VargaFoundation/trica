package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Actions an Authorizer is asked about. Every handler checks exactly one.
const (
	// ActionRead: see inventory, schema, history, proposals, discrepancies, health.
	ActionRead = "read"
	// ActionPropose: submit batches (applied or queued, by the engine's policy)
	// and manage one's own saved queries.
	ActionPropose = "propose"
	// ActionApprove: approve or reject proposals, accept discrepancies.
	ActionApprove = "approve"
	// ActionAdmin: manage what other people created (delete someone else's saved query).
	ActionAdmin = "admin"
)

// PrincipalKind tells humans from machines: it decides the source a batch is
// attributed to.
type PrincipalKind string

const (
	// KindUser is a person (OIDC session). Batches are attributed to "user:<Subject>".
	KindUser PrincipalKind = "user"
	// KindToken is a machine credential (collector, agent, MCP client). It is
	// bound to one source and can only speak as that source.
	KindToken PrincipalKind = "token"
)

// Principal is who is calling, as established by the Authorizer.
type Principal struct {
	// Subject is the stable identifier: the login of a user, the name of a token.
	Subject string
	Kind    PrincipalKind
	// Source is the source a token is bound to ("arista:sw-a1", "mcp:claude").
	// Ignored for users. A token without a source cannot submit batches.
	Source string
	// Credential is opaque to the API: whatever the Authorizer needs to
	// answer Allow for this principal (internal/wiring keeps the verified
	// internal/auth principal, with its roles, scopes and restrictions).
	// Only the Authorizer that built the Principal reads it.
	Credential any `json:"-"`
}

// BatchSource is the only source the principal may speak as.
func (p Principal) BatchSource() string {
	if p.Kind == KindUser {
		return "user:" + p.Subject
	}
	return p.Source
}

// ErrUnauthenticated is what Authorizer.Principal returns for a request that
// carries no valid credential. Any error from Principal is answered with 401;
// this one only documents the intent.
var ErrUnauthenticated = errors.New("unauthenticated")

// Authorizer authenticates a request and answers rights questions. The server
// refuses to start without one: there is no anonymous mode.
//
// internal/auth implements it (OIDC, tokens, RBAC); StaticAuthorizer is for
// tests and development.
type Authorizer interface {
	// Principal authenticates r. It must not write to the response.
	Principal(r *http.Request) (Principal, error)
	// Allow reports whether p may perform action on objects of type objType
	// located in site (the id of the site the object is in: through its parent, or for a device the rack it is mounted in).
	//
	// Two forms are used:
	//   - Allow(p, action, "", ""): a global check, "does p hold this action at
	//     all". Used by endpoints not bound to an object (schema, health, saved
	//     queries, proposals) and as the first gate of every handler.
	//   - Allow(p, action, type, site): a scoped check on one object. An empty
	//     site means the site is unknown or the object has none: a grant limited
	//     to some sites must NOT match it (fail closed).
	Allow(p Principal, action, objType, site string) bool
}

// AuditEntry is one record of a mutating call.
type AuditEntry struct {
	At      time.Time
	Actor   string // Principal.Subject
	Kind    PrincipalKind
	Action  string // "batch.submit", "proposal.approve", ...
	Target  string // what was acted on: a proposal id, a saved query name, a source
	Outcome string // OutcomeOK, OutcomeDenied or OutcomeError
	Details map[string]any
}

// Outcomes of an audited call.
const (
	OutcomeOK     = "ok"
	OutcomeDenied = "denied"
	OutcomeError  = "error"
)

// Auditor records mutating calls (internal/audit implements it: chained,
// tamper-evident). The server refuses to start without one. A failure to
// record is logged; it does not undo a mutation that already committed.
type Auditor interface {
	Record(ctx context.Context, e AuditEntry) error
}

// MemoryAuditor keeps entries in memory. FOR TESTS AND DEVELOPMENT ONLY.
type MemoryAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// Record implements Auditor.
func (m *MemoryAuditor) Record(_ context.Context, e AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

// Entries returns a copy of what was recorded.
func (m *MemoryAuditor) Entries() []AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEntry(nil), m.entries...)
}

// Grant is what a StaticAuthorizer token may do.
type Grant struct {
	Principal Principal
	// Actions the token holds. Empty means none.
	Actions []string
	// Types restricts the grant to these object types; empty means all.
	Types []string
	// Sites restricts the grant to these sites (site object ids); empty means all.
	Sites []string
}

// StaticAuthorizer authenticates a fixed table of bearer tokens held in
// memory. FOR TESTS AND DEVELOPMENT ONLY: tokens are plain strings with no
// expiry, no revocation and no hashing. Production uses internal/auth.
type StaticAuthorizer struct {
	// Tokens maps a bearer token to what it may do. Subjects must be unique
	// (grants are looked up by subject). Not safe to modify while serving.
	Tokens map[string]Grant
}

// Principal implements Authorizer: "Authorization: Bearer <token>".
func (a *StaticAuthorizer) Principal(r *http.Request) (Principal, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return Principal{}, ErrUnauthenticated
	}
	var found *Grant
	for t, g := range a.Tokens {
		if subtle.ConstantTimeCompare([]byte(t), []byte(tok)) == 1 {
			found = &g
		}
	}
	if found == nil {
		return Principal{}, ErrUnauthenticated
	}
	return found.Principal, nil
}

// Allow implements Authorizer.
func (a *StaticAuthorizer) Allow(p Principal, action, objType, site string) bool {
	var g Grant
	found := false
	for _, cand := range a.Tokens {
		if cand.Principal.Subject == p.Subject {
			g, found = cand, true
			break
		}
	}
	if !found || !contains(g.Actions, action) {
		return false
	}
	if objType == "" && site == "" {
		return true
	}
	if objType != "" && len(g.Types) > 0 && !contains(g.Types, objType) {
		return false
	}
	if len(g.Sites) > 0 && (site == "" || !contains(g.Sites, site)) {
		return false
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
