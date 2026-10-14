package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Authenticator authenticates bearer credentials: API tokens (TokenPrefix) and OIDC JWTs.
// Either source may be nil when it is not configured; a credential for a source that is not
// configured is refused.
type Authenticator struct {
	Tokens *TokenStore
	OIDC   *Verifier
	// Policy answers Require and Authorize. Nil denies everything.
	Policy *Policy
	// Logger receives infrastructure failures (database, issuer); slog.Default when nil.
	// Credentials are never logged.
	Logger *slog.Logger
}

func (a *Authenticator) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

// bearer extracts the bearer credential. The query string is deliberately not consulted:
// credentials in URLs end up in logs.
func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, cred, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	cred = strings.TrimSpace(cred)
	return cred, cred != ""
}

// Authenticate verifies a raw bearer value.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (*Principal, error) {
	if strings.HasPrefix(raw, TokenPrefix) {
		if a.Tokens == nil {
			return nil, fmt.Errorf("%w: API tokens are not enabled", ErrUnauthenticated)
		}
		return a.Tokens.Authenticate(ctx, raw)
	}
	if a.OIDC == nil {
		return nil, fmt.Errorf("%w: OIDC is not enabled", ErrUnauthenticated)
	}
	return a.OIDC.Verify(ctx, raw)
}

// IsCredentialError separates "the caller's credential is bad" (401) from "we could not
// check it" (503); both fail closed.
func IsCredentialError(err error) bool {
	for _, target := range []error{ErrUnauthenticated, ErrTokenInvalid, ErrTokenMalformed, ErrTokenUnknown, ErrTokenRevoked, ErrTokenExpired} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// Middleware authenticates the bearer credential and puts the Principal (and the policy) in
// the request context. A missing or invalid credential gets a 401 and the handler is not
// called. The reason is recorded in the audit Slot when there is one, never sent to the client.
func Middleware(a *Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearer(r)
			if !ok {
				recordReason(r.Context(), "no credential presented")
				unauthorized(w)
				return
			}
			p, err := a.Authenticate(r.Context(), raw)
			if err != nil {
				recordReason(r.Context(), err.Error())
				if IsCredentialError(err) {
					unauthorized(w)
					return
				}
				a.logger().Error("authentication unavailable", "error", err.Error())
				writeError(w, http.StatusServiceUnavailable, "authentication unavailable")
				return
			}
			ctx := context.WithValue(WithPrincipal(r.Context(), p), policyKey, a.Policy)
			if s := slotFrom(ctx); s != nil {
				s.Principal = p
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ScopeFunc tells which object type and site a request touches, for the policy check.
type ScopeFunc func(r *http.Request) (objectType, site string)

// Authorize checks, from the request context, that the authenticated principal may perform
// action on objectType at site. It returns ErrUnauthenticated without a principal (the
// middleware was not mounted: fail closed) and ErrForbidden when the policy says no.
func Authorize(ctx context.Context, action Action, objectType, site string) error {
	p, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	pol, _ := ctx.Value(policyKey).(*Policy)
	if !pol.Allow(p, action, objectType, site) {
		return ErrForbidden
	}
	return nil
}

// Require returns a middleware that lets a request through only if the principal may perform
// action. Without a ScopeFunc the object type and site are empty, which a type- or
// site-restricted principal or token is refused: use a ScopeFunc, or Authorize in the handler
// once the object is known.
func Require(action Action, scope ...ScopeFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var objectType, site string
			if len(scope) > 0 && scope[0] != nil {
				objectType, site = scope[0](r)
			}
			switch err := Authorize(r.Context(), action, objectType, site); {
			case err == nil:
				next.ServeHTTP(w, r)
			case errors.Is(err, ErrUnauthenticated):
				recordReason(r.Context(), "no authenticated principal")
				unauthorized(w)
			default:
				recordReason(r.Context(), fmt.Sprintf("forbidden: %s on type %q site %q", action, objectType, site))
				writeError(w, http.StatusForbidden, "forbidden")
			}
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="trica"`)
	writeError(w, http.StatusUnauthorized, "unauthenticated")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
