package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// stack mounts Middleware and the given inner handler; it reports the principal seen.
type stack struct {
	h       http.Handler
	calls   int
	seen    *Principal
	auth    *Authenticator
	tokens  *TokenStore
	slot    *Slot
	clockFn *clock
	// query is the raw query string of requests made with do.
	query string
}

func newStack(t *testing.T, withOIDC *Verifier, inner func(next http.Handler) http.Handler) *stack {
	t.Helper()
	ts, clk := newTokenStore(t)
	s := &stack{tokens: ts, clockFn: clk, query: "access_token=ignored"}
	s.auth = &Authenticator{Tokens: ts, OIDC: withOIDC, Policy: ts.Policy, Logger: quiet()}
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		s.seen, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	var h http.Handler = final
	if inner != nil {
		h = inner(final)
	}
	h = Middleware(s.auth)(h)
	// Outer layer standing in for audit.Middleware: it provides the Slot.
	s.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, slot := WithSlot(r.Context())
		s.slot = slot
		h.ServeHTTP(w, r.WithContext(ctx))
	})
	return s
}

func (s *stack) do(method, bearerValue string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/objects?"+s.query, nil)
	req.RemoteAddr = "192.0.2.7:4242"
	if bearerValue != "" {
		req.Header.Set("Authorization", bearerValue)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func TestMiddleware401(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	s := newStack(t, v, nil)
	expired := is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, map[string]any{"exp": clk.t.Add(-time.Hour).Unix()}))
	cases := map[string]string{
		"no header":        "",
		"basic scheme":     "Basic dXNlcjpwYXNz",
		"bearer, no value": "Bearer ",
		"unknown token":    "Bearer trica_1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"malformed token":  "Bearer trica_garbage",
		"not a jwt":        "Bearer abc",
		"expired jwt":      "Bearer " + expired,
	}
	for name, h := range cases {
		rec := s.do(http.MethodGet, h)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: no WWW-Authenticate", name)
		}
		if strings.Contains(rec.Body.String(), "expired") || strings.Contains(rec.Body.String(), "signature") {
			t.Errorf("%s: the reason leaks to the client: %s", name, rec.Body)
		}
		if s.slot.Reason == "" {
			t.Errorf("%s: the reason was not recorded for the audit log", name)
		}
	}
	// A valid credential in the URL is not read: URLs end up in logs.
	valid := is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil))
	s.query = "access_token=" + valid + "&token=" + valid
	if rec := s.do(http.MethodGet, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a valid credential in the query string: status %d, want 401", rec.Code)
	}
	s.query = "access_token=ignored"
	if rec := s.do(http.MethodGet, "Bearer "+valid); rec.Code != http.StatusNoContent {
		t.Errorf("control: the same credential in the header: status %d, want 204", rec.Code)
	}
	if s.calls != 1 {
		t.Errorf("the handler ran %d times, want only the control request", s.calls)
	}
}

func TestMiddlewareAcceptsTokenAndOIDC(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	s := newStack(t, v, nil)
	raw, _, err := s.tokens.Create(context.Background(), user("operator"), CreateParams{Name: "x", Scopes: []string{ScopeRead}})
	if err != nil {
		t.Fatal(err)
	}
	if rec := s.do(http.MethodGet, "Bearer "+raw); rec.Code != http.StatusNoContent || s.seen == nil || s.seen.Kind != KindToken {
		t.Fatalf("token: status %d, principal %+v", rec.Code, s.seen)
	}
	if s.slot.Principal == nil || s.slot.Principal.Subject != s.seen.Subject {
		t.Error("the slot did not receive the principal")
	}
	jwtRaw := is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil))
	if rec := s.do(http.MethodGet, "bearer "+jwtRaw); rec.Code != http.StatusNoContent || s.seen.Subject != "user:alice" {
		t.Fatalf("jwt: status %d, principal %+v", rec.Code, s.seen)
	}
}

func TestMiddlewareRevokedTokenCutOffImmediately(t *testing.T) {
	s := newStack(t, nil, nil)
	raw, tok, _ := s.tokens.Create(context.Background(), user("admin"), CreateParams{Name: "x", Scopes: []string{ScopeRead}})
	if rec := s.do(http.MethodGet, "Bearer "+raw); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if err := s.tokens.Revoke(context.Background(), tok.ID); err != nil {
		t.Fatal(err)
	}
	if rec := s.do(http.MethodGet, "Bearer "+raw); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: status %d", rec.Code)
	}
}

func TestMiddlewareSourcesNotConfigured(t *testing.T) {
	// No OIDC: a JWT is refused. No token store: a trica token is refused.
	s := newStack(t, nil, nil)
	if rec := s.do(http.MethodGet, "Bearer eyJhbGciOiJSUzI1NiJ9.e30.c2ln"); rec.Code != http.StatusUnauthorized {
		t.Errorf("jwt without OIDC: %d", rec.Code)
	}
	s.auth.Tokens = nil
	if rec := s.do(http.MethodGet, "Bearer trica_1_x"); rec.Code != http.StatusUnauthorized {
		t.Errorf("token without store: %d", rec.Code)
	}
}

func TestMiddleware503WhenIssuerUnreachable(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	is.jwksFail.Store(true)
	s := newStack(t, v, nil)
	rec := s.do(http.MethodGet, "Bearer "+is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (cannot check is not the same as bad credential)", rec.Code)
	}
	if s.calls != 0 {
		t.Error("the handler ran although the credential could not be checked")
	}
}

func TestRequireDeniesByDefault(t *testing.T) {
	// Require without Middleware in front: no principal, no access.
	called := false
	h := Require(ActionRead)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("status %d, called %v", rec.Code, called)
	}
	// A principal in the context but no policy: deny.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(WithPrincipal(context.Background(), user("admin"))))
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("no policy: status %d, called %v", rec.Code, called)
	}
}

func TestRequire(t *testing.T) {
	scope := func(r *http.Request) (string, string) { return r.URL.Query().Get("type"), r.URL.Query().Get("site") }
	s := newStack(t, nil, func(next http.Handler) http.Handler { return Require(ActionPropose, scope)(next) })
	ctx := context.Background()
	mint := func(owner *Principal, p CreateParams) string {
		raw, _, err := s.tokens.Create(ctx, owner, p)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + raw
	}
	operatorWrite := mint(user("operator"), CreateParams{Name: "w", Scopes: []string{ScopeWrite}})
	operatorRead := mint(user("operator"), CreateParams{Name: "r", Scopes: []string{ScopeRead}})
	viewer := mint(user("viewer"), CreateParams{Name: "v", Scopes: []string{ScopeRead}})
	restricted := mint(user("operator"), CreateParams{Name: "p", Scopes: []string{ScopeWrite}, Types: []string{"device"}, Sites: []string{"paris-1"}})

	get := func(bearerValue, query string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/proposals?"+query, nil)
		req.Header.Set("Authorization", bearerValue)
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, req)
		return rec.Code
	}
	cases := []struct {
		name  string
		token string
		query string
		want  int
	}{
		{"write scope, operator role", operatorWrite, "", http.StatusNoContent},
		{"read scope cannot propose", operatorRead, "", http.StatusForbidden},
		{"viewer cannot propose", viewer, "", http.StatusForbidden},
		{"restricted token inside its perimeter", restricted, "type=device&site=paris-1", http.StatusNoContent},
		{"restricted token, other type", restricted, "type=rack&site=paris-1", http.StatusForbidden},
		{"restricted token, other site", restricted, "type=device&site=lyon-2", http.StatusForbidden},
		{"restricted token, unscoped request", restricted, "", http.StatusForbidden},
	}
	for _, c := range cases {
		if got := get(c.token, c.query); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	if s.slot.Reason == "" {
		t.Error("the refusal was not recorded for the audit log")
	}
}

func TestAuthorizeInHandler(t *testing.T) {
	p := user("viewer")
	ctx := context.WithValue(WithPrincipal(context.Background(), p), policyKey, DefaultPolicy())
	if err := Authorize(ctx, ActionRead, "device", "s"); err != nil {
		t.Errorf("read: %v", err)
	}
	if err := Authorize(ctx, ActionApprove, "device", "s"); !errors.Is(err, ErrForbidden) {
		t.Errorf("approve: %v", err)
	}
	if err := Authorize(context.Background(), ActionRead, "", ""); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("no principal: %v", err)
	}
}

func TestRateLimiter(t *testing.T) {
	clk := newClock()
	l := NewRateLimiter(2, 3) // 2 per second, burst 3
	l.Now = clk.now
	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d of the burst refused", i+1)
		}
	}
	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("4th immediate request allowed")
	}
	if wait <= 0 || wait > time.Second {
		t.Errorf("wait = %v, want within (0, 1s]", wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("another key is affected by a's exhaustion")
	}
	clk.advance(500 * time.Millisecond) // one token back at 2/s
	if ok, _ := l.Allow("a"); !ok {
		t.Error("no refill after 500 ms")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("only one token should have been refilled")
	}
	// The bucket never holds more than the burst: after 50 s idle (inside the sweep period,
	// so the bucket is still there and only the cap holds it back) and after an hour.
	for _, idle := range []time.Duration{50 * time.Second, time.Hour} {
		clk.advance(idle)
		n := 0
		for ok, _ := l.Allow("a"); ok; ok, _ = l.Allow("a") {
			n++
		}
		if n != 3 {
			t.Errorf("after %v idle the burst is %d, want 3", idle, n)
		}
	}
}

func TestRateLimiterSweepsIdleKeys(t *testing.T) {
	clk := newClock()
	l := NewRateLimiter(1, 2)
	l.Now = clk.now
	for i := range 100 {
		l.Allow("k" + strconv.Itoa(i))
	}
	if l.Size() != 100 {
		t.Fatalf("size = %d", l.Size())
	}
	clk.advance(10 * time.Minute)
	l.Allow("fresh")
	if l.Size() != 1 {
		t.Errorf("size after sweep = %d, want 1: idle buckets leak memory", l.Size())
	}
}

func TestRateLimiterZeroRateRefuses(t *testing.T) {
	l := NewRateLimiter(0, 0)
	if ok, wait := l.Allow("a"); ok || wait <= 0 {
		t.Errorf("ok=%v wait=%v", ok, wait)
	}
}

func TestRateLimitMiddleware429(t *testing.T) {
	clk := newClock()
	lim := NewRateLimiter(1, 2)
	lim.Now = clk.now
	s := newStack(t, nil, lim.Middleware)
	ctx := context.Background()
	rawA, _, _ := s.tokens.Create(ctx, user("admin"), CreateParams{Name: "a", Scopes: []string{ScopeRead}})
	rawB, _, _ := s.tokens.Create(ctx, user("admin"), CreateParams{Name: "b", Scopes: []string{ScopeRead}})

	for i := range 2 {
		if rec := s.do(http.MethodGet, "Bearer "+rawA); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	rec := s.do(http.MethodGet, "Bearer "+rawA)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if s.slot.Reason == "" {
		t.Error("the refusal was not recorded for the audit log")
	}
	calls := s.calls
	// Limits are per principal: B is unaffected, and A's bucket is not shared with it.
	if rec := s.do(http.MethodGet, "Bearer "+rawB); rec.Code != http.StatusNoContent {
		t.Errorf("another principal: %d", rec.Code)
	}
	if s.calls != calls+1 {
		t.Error("the limited request reached the handler")
	}
}

// Retry-After tells the client how long until a token is back, in whole seconds rounded up.
func TestRateLimitRetryAfterReflectsTheWait(t *testing.T) {
	clk := newClock()
	lim := NewRateLimiter(0.2, 1) // one token every 5 s
	lim.Now = clk.now
	h := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.1:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(); rec.Code != http.StatusNoContent {
		t.Fatalf("first request: %d", rec.Code)
	}
	if rec := get(); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "5" {
		t.Errorf("immediately after: %d, Retry-After %q, want 429 and 5", rec.Code, rec.Header().Get("Retry-After"))
	}
	clk.advance(3500 * time.Millisecond) // 1.5 s to go: rounded up to 2
	if rec := get(); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" {
		t.Errorf("3.5 s later: %d, Retry-After %q, want 429 and 2", rec.Code, rec.Header().Get("Retry-After"))
	}
	clk.advance(2 * time.Second) // the advertised wait
	if rec := get(); rec.Code != http.StatusNoContent {
		t.Errorf("after the advertised wait: %d, want 204", rec.Code)
	}
}

func TestRateLimitKeysAnonymousByAddress(t *testing.T) {
	lim := NewRateLimiter(1, 1)
	h := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	do := func(addr string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if do("192.0.2.1:1") != http.StatusNoContent || do("192.0.2.1:2") != http.StatusTooManyRequests {
		t.Error("same address, different ports must share a bucket")
	}
	if do("192.0.2.2:1") != http.StatusNoContent {
		t.Error("another address must have its own bucket")
	}
	if do("no-port") != http.StatusNoContent {
		t.Error("an address without a port is still a key")
	}
}

func TestHasRole(t *testing.T) {
	if !user("a", "b").HasRole("b") || user("a").HasRole("b") {
		t.Error("HasRole")
	}
	var p *Principal
	if p.HasRole("a") {
		t.Error("nil principal has no role")
	}
}
