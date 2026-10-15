// Package wiring assembles the components of a trica server: it adapts
// internal/auth, internal/audit and internal/graph to the interfaces the API
// and the MCP server depend on, and mounts them, the web UI, the transport,
// the cabling deducer and the device type library on internal/server
// (ADR 0025).
//
// It exists apart from cmd/trica so that the whole assembly is testable and
// reusable, and apart from the packages it adapts because the dependency
// points one way: internal/api and internal/mcp define the interfaces,
// internal/auth, internal/audit and internal/graph know nothing of them.
package wiring

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/auth"
)

// Authorizer makes internal/auth an api.Authorizer: it authenticates the
// bearer credential (an API token or an OIDC JWT) and answers rights questions
// from the RBAC policy. There is no anonymous mode: a request without a valid
// credential has no principal, and a principal the adapter did not build
// (zero Credential) is allowed nothing.
type Authorizer struct {
	Auth   *auth.Authenticator
	Policy *auth.Policy
}

var _ api.Authorizer = (*Authorizer)(nil)

// Principal authenticates r. The principal the rate limiter already verified
// for this very request is reused, so a request costs one credential check.
func (a *Authorizer) Principal(r *http.Request) (api.Principal, error) {
	if p, ok := auth.FromContext(r.Context()); ok {
		return toAPI(p)
	}
	raw, ok := bearer(r)
	if !ok {
		return api.Principal{}, api.ErrUnauthenticated
	}
	p, err := a.Auth.Authenticate(r.Context(), raw)
	if err != nil {
		return api.Principal{}, credentialError(err)
	}
	return toAPI(p)
}

// credentialError maps an authentication failure: a bad credential is
// api.ErrUnauthenticated (401), anything else (the token store or the identity
// provider does not answer) is api.ErrAuthUnavailable (503), so that a client
// does not throw away a credential that may be valid.
func credentialError(err error) error {
	if auth.IsCredentialError(err) {
		return api.ErrUnauthenticated
	}
	return fmt.Errorf("%w: %w", api.ErrAuthUnavailable, err)
}

// Allow answers from the policy. The action is passed through as is: the
// policy knows four and refuses any other. A principal limited to some types
// or sites is refused an empty type or site (the policy's rule, which this
// adapter must not weaken: "unknown site" never matches a site grant).
func (a *Authorizer) Allow(p api.Principal, action, objType, site string) bool {
	cred, ok := p.Credential.(*auth.Principal)
	if !ok || cred == nil || a.Policy == nil {
		return false
	}
	return a.Policy.Allow(cred, auth.Action(action), objType, site)
}

// toAPI converts a verified internal/auth principal. The kind prefix of the
// auth subject ("user:", "token:") is dropped: api.Principal.BatchSource puts
// "user:" back for people, and a token speaks as the source it is bound to, or
// as "api:token-<id>" when it has none (ADR 0028). The audit adapter rebuilds
// the actor from kind and subject.
func toAPI(p *auth.Principal) (api.Principal, error) {
	switch p.Kind {
	case auth.KindUser:
		return api.Principal{Subject: strings.TrimPrefix(p.Subject, "user:"), Kind: api.KindUser, Credential: p}, nil
	case auth.KindToken:
		id := strings.TrimPrefix(p.Subject, "token:")
		src := p.Source
		if src == "" {
			src = "api:token-" + id
		}
		return api.Principal{Subject: id, Kind: api.KindToken, Source: src, Credential: p}, nil
	}
	return api.Principal{}, errors.New("wiring: unknown principal kind")
}

// bearer extracts "Authorization: Bearer <credential>". The query string is
// never consulted: credentials in URLs end up in logs.
func bearer(r *http.Request) (string, bool) {
	scheme, cred, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	cred = strings.TrimSpace(cred)
	return cred, cred != ""
}

// limit rate-limits next per principal. It authenticates the request first, so
// that the bucket is the principal's, and leaves the verified principal in the
// context for Authorizer.Principal. A request that does not authenticate is
// limited by peer address (auth.RateLimiter's rule) and passed on: the handler
// answers 401 itself, so the response format stays the API's or MCP's own.
func limit(l *auth.RateLimiter, a *Authorizer, next http.Handler) http.Handler {
	limited := l.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, ok := bearer(r); ok {
			if p, err := a.Auth.Authenticate(r.Context(), raw); err == nil {
				r = r.WithContext(auth.WithPrincipal(r.Context(), p))
			}
		}
		limited.ServeHTTP(w, r)
	})
}
