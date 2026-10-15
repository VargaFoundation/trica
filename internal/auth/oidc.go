package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Verifier errors. ErrTokenInvalid is a bad credential: the client only ever sees a 401, the
// reason goes to the audit log. ErrIssuerUnreachable is not about the credential: discovery
// could not reach the issuer, and the client gets a 503 (the credential may be valid).
var (
	ErrTokenInvalid      = errors.New("auth: invalid credential")
	ErrIssuerUnreachable = errors.New("auth: oidc issuer unreachable")
)

const (
	maxDocBytes        = 1 << 20
	defaultLeeway      = 30 * time.Second
	defaultMinRefresh  = 30 * time.Second
	defaultGroupsClaim = "groups"
)

// OIDCConfig configures a Verifier.
type OIDCConfig struct {
	// Issuer is the exact `iss` value, an https URL; discovery is at
	// Issuer + "/.well-known/openid-configuration".
	Issuer string
	// Audience lists the accepted `aud` values (the client id of trica, the API audience).
	// At least one is required: a token minted for another service is not ours.
	Audience []string
	// Algorithms is the allow-list; RS256 and ES256 when empty. Only those two are accepted
	// at all, so "none" and HMAC algorithms can never be enabled by configuration.
	Algorithms []string
	// GroupsClaim is the claim holding the user's groups; "groups" when empty.
	GroupsClaim string
	// GroupRoles maps a group to RBAC roles (Policy.GroupRoles).
	GroupRoles map[string][]string
	// Leeway tolerates clock skew on exp, nbf and iat; 30 s when zero.
	Leeway time.Duration
	// MinRefreshInterval bounds JWKS refreshes triggered by unknown key ids, so a stream of
	// forged kids cannot turn the verifier into a request amplifier; 30 s when zero.
	MinRefreshInterval time.Duration
	// HTTPClient fetches the discovery document and the JWKS; a 10 s-timeout client when nil.
	HTTPClient *http.Client
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Verifier validates bearer JWTs (ID or access tokens) issued by one OIDC provider.
//
// It checks, in order: the algorithm against an allow-list before any key is touched, the
// signature against the issuer's JWKS, then iss, aud, exp (required), nbf and iat.
type Verifier struct {
	cfg     OIDCConfig
	algs    []jose.SignatureAlgorithm
	client  *http.Client
	jwksURL string

	discoverMu    sync.Mutex // serialises discoveries
	refreshMu     sync.Mutex // serialises JWKS refreshes
	mu            sync.Mutex // guards jwksURL, keys, lastRefresh, lastDiscovery, fetches
	keys          map[string]jose.JSONWebKey
	lastRefresh   time.Time
	lastDiscovery time.Time
	fetches       int
}

// NewVerifier performs discovery and returns a verifier. The JWKS is fetched on first use.
func NewVerifier(ctx context.Context, cfg OIDCConfig) (*Verifier, error) {
	v, err := NewLazyVerifier(cfg)
	if err != nil {
		return nil, err
	}
	if err := v.Discover(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// NewLazyVerifier checks the configuration and returns a verifier that has not discovered
// its issuer yet: Verify discovers it on first use, and retries at most every
// MinRefreshInterval while the issuer does not answer. A server can then start, and serve
// API tokens, while its identity provider is down.
func NewLazyVerifier(cfg OIDCConfig) (*Verifier, error) {
	if err := requireHTTPS(cfg.Issuer); err != nil {
		return nil, fmt.Errorf("auth: oidc issuer: %w", err)
	}
	if len(cfg.Audience) == 0 {
		return nil, errors.New("auth: oidc: at least one audience is required")
	}
	v := &Verifier{cfg: cfg, client: cfg.HTTPClient, keys: map[string]jose.JSONWebKey{}}
	if v.client == nil {
		v.client = &http.Client{Timeout: 10 * time.Second}
	}
	if v.cfg.GroupsClaim == "" {
		v.cfg.GroupsClaim = defaultGroupsClaim
	}
	if v.cfg.Leeway == 0 {
		v.cfg.Leeway = defaultLeeway
	}
	if v.cfg.MinRefreshInterval == 0 {
		v.cfg.MinRefreshInterval = defaultMinRefresh
	}
	names := cfg.Algorithms
	if len(names) == 0 {
		names = []string{string(jose.RS256), string(jose.ES256)}
	}
	for _, n := range names {
		switch a := jose.SignatureAlgorithm(n); a {
		case jose.RS256, jose.ES256:
			v.algs = append(v.algs, a)
		default:
			return nil, fmt.Errorf("auth: oidc: algorithm %q is not allowed (RS256, ES256 only)", n)
		}
	}
	return v, nil
}

// Discover fetches the discovery document and records the JWKS URL. An issuer that does not
// answer, or answers with an error status, gives ErrIssuerUnreachable; a document that does
// not match the configuration gives another error, which no retry will fix.
func (v *Verifier) Discover(ctx context.Context) error {
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := v.getJSON(ctx, strings.TrimSuffix(v.cfg.Issuer, "/")+"/.well-known/openid-configuration", &doc); err != nil {
		return fmt.Errorf("%w: discovery: %w", ErrIssuerUnreachable, err)
	}
	if doc.Issuer != v.cfg.Issuer {
		return fmt.Errorf("auth: oidc discovery: issuer %q does not match the configured %q", doc.Issuer, v.cfg.Issuer)
	}
	if err := requireHTTPS(doc.JWKSURI); err != nil {
		return fmt.Errorf("auth: oidc discovery: jwks_uri: %w", err)
	}
	v.mu.Lock()
	v.jwksURL = doc.JWKSURI
	v.mu.Unlock()
	return nil
}

// Discovered reports whether the issuer has been discovered.
func (v *Verifier) Discovered() bool { return v.jwks() != "" }

func (v *Verifier) jwks() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.jwksURL
}

// discovered makes sure the issuer is discovered before a key is looked up. Attempts are
// bounded by MinRefreshInterval, like JWKS refreshes, so that a stream of requests while the
// issuer is down does not become a stream of requests to it.
func (v *Verifier) discovered(ctx context.Context) error {
	if v.Discovered() {
		return nil
	}
	v.discoverMu.Lock()
	defer v.discoverMu.Unlock()
	if v.Discovered() { // discovered while we waited
		return nil
	}
	v.mu.Lock()
	tooSoon := !v.lastDiscovery.IsZero() && v.now().Sub(v.lastDiscovery) < v.cfg.MinRefreshInterval
	if !tooSoon {
		v.lastDiscovery = v.now()
	}
	v.mu.Unlock()
	if tooSoon {
		return fmt.Errorf("%w: not discovered yet, retried at most every %s", ErrIssuerUnreachable, v.cfg.MinRefreshInterval)
	}
	return v.Discover(ctx)
}

func requireHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%q is not an https URL", raw)
	}
	return nil
}

func (v *Verifier) now() time.Time {
	if v.cfg.Now != nil {
		return v.cfg.Now()
	}
	return time.Now()
}

func (v *Verifier) getJSON(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxDocBytes)).Decode(dst)
}

// JWKSFetches returns how many times the JWKS was fetched; tests and metrics use it.
func (v *Verifier) JWKSFetches() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fetches
}

// cached returns the cached key for kid.
func (v *Verifier) cached(kid string) (*jose.JSONWebKey, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	k, ok := v.keys[kid]
	return &k, ok
}

// key returns the signing key for kid. On an unknown kid it refreshes the JWKS once (the
// issuer may have rotated), at most every MinRefreshInterval. Concurrent callers wait for
// the one refresh in flight instead of each issuing their own, and cache hits never wait.
func (v *Verifier) key(ctx context.Context, kid string) (*jose.JSONWebKey, error) {
	if k, ok := v.cached(kid); ok {
		return k, nil
	}
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	if k, ok := v.cached(kid); ok { // refreshed while we waited
		return k, nil
	}
	v.mu.Lock()
	tooSoon := !v.lastRefresh.IsZero() && v.now().Sub(v.lastRefresh) < v.cfg.MinRefreshInterval
	if !tooSoon {
		v.lastRefresh = v.now()
		v.fetches++
	}
	v.mu.Unlock()
	if tooSoon {
		return nil, fmt.Errorf("%w: unknown key id", ErrTokenInvalid)
	}
	var set jose.JSONWebKeySet
	if err := v.getJSON(ctx, v.jwks(), &set); err != nil {
		return nil, fmt.Errorf("auth: oidc jwks: %w", err)
	}
	keys := make(map[string]jose.JSONWebKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.KeyID != "" && k.Valid() && k.IsPublic() && (k.Use == "" || k.Use == "sig") {
			keys[k.KeyID] = k
		}
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	if k, ok := v.cached(kid); ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown key id", ErrTokenInvalid)
}

// Verify validates a bearer JWT and returns the user principal.
func (v *Verifier) Verify(ctx context.Context, raw string) (*Principal, error) {
	// The allow-list is applied while parsing, before any key is looked up: "none" and
	// HS256 never get as far as a signature check.
	tok, err := jwt.ParseSigned(raw, v.algs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	if len(tok.Headers) != 1 {
		return nil, fmt.Errorf("%w: exactly one signature expected", ErrTokenInvalid)
	}
	kid := tok.Headers[0].KeyID
	if kid == "" {
		return nil, fmt.Errorf("%w: missing key id", ErrTokenInvalid)
	}
	if err := v.discovered(ctx); err != nil {
		return nil, err
	}
	key, err := v.key(ctx, kid)
	if err != nil {
		return nil, err
	}
	if key.Algorithm != "" && key.Algorithm != tok.Headers[0].Algorithm {
		return nil, fmt.Errorf("%w: key %q is for %s, token uses %s", ErrTokenInvalid, kid, key.Algorithm, tok.Headers[0].Algorithm)
	}
	var std jwt.Claims
	var all map[string]any
	if err := tok.Claims(key.Key, &std, &all); err != nil {
		return nil, fmt.Errorf("%w: signature: %w", ErrTokenInvalid, err)
	}
	if std.Expiry == nil {
		return nil, fmt.Errorf("%w: no exp claim", ErrTokenInvalid)
	}
	if std.Subject == "" {
		return nil, fmt.Errorf("%w: no sub claim", ErrTokenInvalid)
	}
	err = std.ValidateWithLeeway(jwt.Expected{
		Issuer:      v.cfg.Issuer,
		AnyAudience: v.cfg.Audience,
		Time:        v.now(),
	}, v.cfg.Leeway)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	groups := claimStrings(all[v.cfg.GroupsClaim])
	return &Principal{
		Subject: "user:" + std.Subject,
		Kind:    KindUser,
		Groups:  groups,
		Roles:   v.rolesFor(groups),
	}, nil
}

func (v *Verifier) rolesFor(groups []string) []string {
	var roles []string
	for _, g := range groups {
		for _, r := range v.cfg.GroupRoles[g] {
			if !slices.Contains(roles, r) {
				roles = append(roles, r)
			}
		}
	}
	return roles
}

// claimStrings reads a claim that is a string or an array of strings; other types give nil.
func claimStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
