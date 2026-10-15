package auth

import (
	"cmp"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const testAudience = "trica-api"

// testIssuer is a local OIDC provider over TLS: discovery, a JWKS that tests can rotate, and
// a signer. Keys are generated at test time.
type testIssuer struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	keys     map[string]any // kid -> private key, the ones published in the JWKS
	jwksHits atomic.Int32
	// discoveryHits and discoveryFail observe and break the discovery endpoint.
	discoveryHits atomic.Int32
	discoveryFail atomic.Bool
	jwksFail      atomic.Bool
	// jwksDelay slows the JWKS endpoint down (nanoseconds), to hold a refresh in flight.
	jwksDelay atomic.Int64
	// algs overrides the "alg" the JWKS declares for a kid.
	algs map[string]string
}

func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	is := &testIssuer{t: t, keys: map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		is.discoveryHits.Add(1)
		if is.discoveryFail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": is.srv.URL, "jwks_uri": is.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		is.jwksHits.Add(1)
		time.Sleep(time.Duration(is.jwksDelay.Load()))
		if is.jwksFail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		is.mu.Lock()
		defer is.mu.Unlock()
		var set jose.JSONWebKeySet
		for kid, k := range is.keys {
			switch p := k.(type) {
			case *rsa.PrivateKey:
				set.Keys = append(set.Keys, jose.JSONWebKey{Key: &p.PublicKey, KeyID: kid, Use: "sig", Algorithm: cmp.Or(is.algs[kid], "RS256")})
			case *ecdsa.PrivateKey:
				set.Keys = append(set.Keys, jose.JSONWebKey{Key: &p.PublicKey, KeyID: kid, Use: "sig", Algorithm: cmp.Or(is.algs[kid], "ES256")})
			}
		}
		_ = json.NewEncoder(w).Encode(set)
	})
	is.srv = httptest.NewTLSServer(mux)
	t.Cleanup(is.srv.Close)
	return is
}

func (is *testIssuer) url() string { return is.srv.URL }

func (is *testIssuer) addRSA(kid string) *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		is.t.Fatal(err)
	}
	is.mu.Lock()
	is.keys[kid] = k
	is.mu.Unlock()
	return k
}

func (is *testIssuer) addEC(kid string) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		is.t.Fatal(err)
	}
	is.mu.Lock()
	is.keys[kid] = k
	is.mu.Unlock()
	return k
}

func (is *testIssuer) verifier(mod func(*OIDCConfig)) (*Verifier, *clock) {
	is.t.Helper()
	clk := newClock()
	cfg := OIDCConfig{
		Issuer:     is.url(),
		Audience:   []string{testAudience},
		HTTPClient: is.srv.Client(),
		Now:        clk.now,
		GroupRoles: map[string][]string{"ops": {"operator"}, "staff": {"viewer"}, "root": {"admin", "operator"}},
	}
	if mod != nil {
		mod(&cfg)
	}
	v, err := NewVerifier(context.Background(), cfg)
	if err != nil {
		is.t.Fatal(err)
	}
	return v, clk
}

// claims returns a valid claim set at the verifier clock; tests override what they test.
func (is *testIssuer) claims(clk *clock, over map[string]any) map[string]any {
	c := map[string]any{
		"iss": is.url(), "aud": testAudience, "sub": "alice",
		"exp": clk.t.Add(time.Hour).Unix(), "iat": clk.t.Unix(), "nbf": clk.t.Unix(),
		"groups": []string{"ops"},
	}
	for k, v := range over {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func (is *testIssuer) sign(alg jose.SignatureAlgorithm, kid string, key any, claims map[string]any) string {
	is.t.Helper()
	sg, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: jose.JSONWebKey{Key: key, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		is.t.Fatal(err)
	}
	raw, err := jwt.Signed(sg).Claims(claims).Serialize()
	if err != nil {
		is.t.Fatal(err)
	}
	return raw
}

func b64(v any) string {
	raw, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestOIDCVerifyValid(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	ek := is.addEC("ec-1")
	v, clk := is.verifier(nil)
	ctx := context.Background()

	for name, raw := range map[string]string{
		"RS256": is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil)),
		"ES256": is.sign(jose.ES256, "ec-1", ek, is.claims(clk, nil)),
	} {
		p, err := v.Verify(ctx, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Subject != "user:alice" || p.Kind != KindUser {
			t.Errorf("%s: principal = %+v", name, p)
		}
		if len(p.Roles) != 1 || p.Roles[0] != "operator" || len(p.Groups) != 1 || p.Groups[0] != "ops" {
			t.Errorf("%s: roles/groups = %v/%v", name, p.Roles, p.Groups)
		}
	}
	// An audience list containing ours is fine.
	raw := is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, map[string]any{"aud": []string{"other", testAudience}}))
	if _, err := v.Verify(ctx, raw); err != nil {
		t.Errorf("multi-audience token: %v", err)
	}
	// One JWKS fetch served all of it.
	if n := is.jwksHits.Load(); n != 1 {
		t.Errorf("JWKS fetched %d times, want 1", n)
	}
}

func TestOIDCRejects(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	ek := is.addEC("ec-1")
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	v, clk := is.verifier(nil)
	rsaOnly, _ := is.verifier(func(c *OIDCConfig) { c.Algorithms = []string{"RS256"} })
	ctx := context.Background()
	ok := func(over map[string]any) string { return is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, over)) }

	hmac := func() string {
		sg, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: jose.JSONWebKey{Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "rsa-1"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := jwt.Signed(sg).Claims(is.claims(clk, nil)).Serialize()
		return raw
	}

	cases := []struct {
		name string
		v    *Verifier
		raw  string
	}{
		{"wrong issuer", v, ok(map[string]any{"iss": "https://evil.example"})},
		{"issuer with trailing slash", v, ok(map[string]any{"iss": is.url() + "/"})},
		{"no issuer", v, ok(map[string]any{"iss": nil})},
		{"wrong audience", v, ok(map[string]any{"aud": "someone-else"})},
		{"audience list without ours", v, ok(map[string]any{"aud": []string{"a", "b"}})},
		{"no audience", v, ok(map[string]any{"aud": nil})},
		{"expired", v, ok(map[string]any{"exp": clk.t.Add(-time.Hour).Unix()})},
		{"expired within no leeway", v, ok(map[string]any{"exp": clk.t.Add(-time.Minute).Unix()})},
		{"no exp", v, ok(map[string]any{"exp": nil})},
		{"not yet valid", v, ok(map[string]any{"nbf": clk.t.Add(time.Hour).Unix()})},
		{"issued in the future", v, ok(map[string]any{"iat": clk.t.Add(time.Hour).Unix()})},
		{"no subject", v, ok(map[string]any{"sub": nil})},
		{"alg none", v, b64(map[string]any{"alg": "none", "typ": "JWT", "kid": "rsa-1"}) + "." + b64(is.claims(clk, nil)) + "."},
		{"alg none with signature part", v, b64(map[string]any{"alg": "none", "kid": "rsa-1"}) + "." + b64(is.claims(clk, nil)) + ".c2ln"},
		{"HS256", v, hmac()},
		{"ES256 when only RS256 is allowed", rsaOnly, is.sign(jose.ES256, "ec-1", ek, is.claims(clk, nil))},
		{"signed by another key under a known kid", v, is.sign(jose.RS256, "rsa-1", other, is.claims(clk, nil))},
		{"kid of an EC key used with RS256 header", v, is.sign(jose.RS256, "ec-1", rk, is.claims(clk, nil))},
		{"no kid", v, is.sign(jose.RS256, "", rk, is.claims(clk, nil))},
		{"garbage", v, "not.a.jwt"},
		{"empty", v, ""},
	}
	for _, c := range cases {
		_, err := c.v.Verify(ctx, c.raw)
		if !errors.Is(err, ErrTokenInvalid) {
			t.Errorf("%s: err = %v, want ErrTokenInvalid", c.name, err)
		}
	}
	// Control: the same keys and claims are accepted when nothing is wrong, so that the
	// refusals above are due to the one thing each case changes.
	if _, err := v.Verify(ctx, ok(nil)); err != nil {
		t.Fatalf("control token refused: %v", err)
	}
}

// The JWKS says what algorithm a key is for; a token that uses it with another one is refused
// even when the signature checks out (an RSA key published for PS256 signing RS256 tokens).
func TestOIDCKeyAlgorithmMustMatchHeader(t *testing.T) {
	is := newTestIssuer(t)
	declared := is.addRSA("declared-ps256")
	plain := is.addRSA("declared-rs256")
	is.mu.Lock()
	is.algs = map[string]string{"declared-ps256": "PS256"}
	is.mu.Unlock()
	v, clk := is.verifier(nil)
	ctx := context.Background()
	if _, err := v.Verify(ctx, is.sign(jose.RS256, "declared-rs256", plain, is.claims(clk, nil))); err != nil {
		t.Fatalf("control: %v", err)
	}
	if _, err := v.Verify(ctx, is.sign(jose.RS256, "declared-ps256", declared, is.claims(clk, nil))); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("RS256 token under a key published for PS256: err = %v", err)
	}
}

// A token without kid is refused before any network access: it cannot make the server fetch
// the JWKS.
func TestOIDCTokenWithoutKidCausesNoFetch(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	if _, err := v.Verify(context.Background(), is.sign(jose.RS256, "", rk, is.claims(clk, nil))); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("err = %v", err)
	}
	if n := is.jwksHits.Load(); n != 0 || v.JWKSFetches() != 0 {
		t.Errorf("a token without kid caused %d JWKS fetches", n)
	}
}

// Callers that arrive while a refresh is in flight wait for it and use its result; they are
// neither refused nor the cause of more fetches.
func TestOIDCConcurrentRotationFetchesOnce(t *testing.T) {
	is := newTestIssuer(t)
	oldKey := is.addRSA("old")
	v, clk := is.verifier(nil)
	ctx := context.Background()
	if _, err := v.Verify(ctx, is.sign(jose.RS256, "old", oldKey, is.claims(clk, nil))); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	newKey := is.addRSA("new")
	raw := is.sign(jose.RS256, "new", newKey, is.claims(clk, nil))
	before := is.jwksHits.Load()
	is.jwksDelay.Store(int64(300 * time.Millisecond))

	const callers = 8
	errs := make(chan error, callers)
	for range callers {
		go func() {
			_, err := v.Verify(ctx, raw)
			errs <- err
		}()
	}
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("concurrent verification during rotation: %v", err)
		}
	}
	if n := is.jwksHits.Load() - before; n != 1 {
		t.Errorf("%d concurrent callers caused %d fetches, want 1", callers, n)
	}
}

func TestOIDCLeeway(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	// 10 s past expiry is inside the 30 s default leeway; 60 s is not.
	if _, err := v.Verify(context.Background(), is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, map[string]any{"exp": clk.t.Add(-10 * time.Second).Unix()}))); err != nil {
		t.Errorf("10 s expired inside the leeway: %v", err)
	}
	if _, err := v.Verify(context.Background(), is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, map[string]any{"exp": clk.t.Add(-60 * time.Second).Unix()}))); err == nil {
		t.Error("60 s expired accepted")
	}
}

func TestOIDCUnknownKidRefreshesJWKSOnce(t *testing.T) {
	is := newTestIssuer(t)
	oldKey := is.addRSA("old")
	v, clk := is.verifier(nil)
	ctx := context.Background()

	if _, err := v.Verify(ctx, is.sign(jose.RS256, "old", oldKey, is.claims(clk, nil))); err != nil {
		t.Fatal(err)
	}
	if n := is.jwksHits.Load(); n != 1 {
		t.Fatalf("hits after first use = %d, want 1", n)
	}
	// The issuer rotates: a new kid appears. The first token with it triggers exactly one
	// refresh and is then accepted.
	clk.advance(time.Minute) // past the refresh cool-down
	newKey := is.addRSA("new")
	raw := is.sign(jose.RS256, "new", newKey, is.claims(clk, nil))
	if _, err := v.Verify(ctx, raw); err != nil {
		t.Fatalf("token with a rotated kid refused: %v", err)
	}
	if n := is.jwksHits.Load(); n != 2 {
		t.Errorf("hits after rotation = %d, want 2", n)
	}
	// Now it is cached: no further fetch.
	if _, err := v.Verify(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if n := is.jwksHits.Load(); n != 2 {
		t.Errorf("hits after cached use = %d, want 2", n)
	}
	if v.JWKSFetches() != 2 {
		t.Errorf("JWKSFetches = %d", v.JWKSFetches())
	}
}

func TestOIDCForgedKidsCannotAmplify(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	ctx := context.Background()
	if _, err := v.Verify(ctx, is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil))); err != nil {
		t.Fatal(err)
	}
	before := is.jwksHits.Load()
	clk.advance(time.Minute)
	attacker, _ := rsa.GenerateKey(rand.Reader, 2048)
	for i := range 20 {
		kid := "forged-" + string(rune('a'+i))
		_, err := v.Verify(ctx, is.sign(jose.RS256, kid, attacker, is.claims(clk, nil)))
		if !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("forged kid %s: %v", kid, err)
		}
	}
	if n := is.jwksHits.Load() - before; n != 1 {
		t.Errorf("20 forged kids caused %d JWKS fetches, want 1", n)
	}
	// After the cool-down a real rotation is picked up again.
	clk.advance(time.Minute)
	nk := is.addRSA("rotated")
	if _, err := v.Verify(ctx, is.sign(jose.RS256, "rotated", nk, is.claims(clk, nil))); err != nil {
		t.Errorf("rotation after cool-down: %v", err)
	}
}

func TestOIDCRemovedKeyStopsWorking(t *testing.T) {
	is := newTestIssuer(t)
	k1 := is.addRSA("k1")
	v, clk := is.verifier(nil)
	ctx := context.Background()
	raw := is.sign(jose.RS256, "k1", k1, is.claims(clk, nil))
	if _, err := v.Verify(ctx, raw); err != nil {
		t.Fatal(err)
	}
	// The issuer revokes k1 and publishes k2. A token for k2 triggers the refresh, which
	// replaces the cache, so k1 is gone.
	is.mu.Lock()
	delete(is.keys, "k1")
	is.mu.Unlock()
	k2 := is.addRSA("k2")
	clk.advance(time.Minute)
	if _, err := v.Verify(ctx, is.sign(jose.RS256, "k2", k2, is.claims(clk, nil))); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(ctx, raw); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("token of a withdrawn key: err = %v", err)
	}
}

func TestOIDCJWKSOutageIsNotACredentialError(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	is.jwksFail.Store(true)
	_, err := v.Verify(context.Background(), is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil)))
	if err == nil || errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("err = %v, want an infrastructure error", err)
	}
	// And the outage is retried after the cool-down.
	is.jwksFail.Store(false)
	clk.advance(time.Minute)
	if _, err := v.Verify(context.Background(), is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil))); err != nil {
		t.Errorf("after recovery: %v", err)
	}
}

func TestOIDCGroupsToRoles(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	v, clk := is.verifier(nil)
	ctx := context.Background()
	roles := func(over map[string]any) []string {
		t.Helper()
		p, err := v.Verify(ctx, is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, over)))
		if err != nil {
			t.Fatal(err)
		}
		return p.Roles
	}
	if got := roles(map[string]any{"groups": []string{"ops", "root", "unmapped"}}); len(got) != 2 || got[0] != "operator" || got[1] != "admin" {
		t.Errorf("roles = %v (operator once, admin once, unmapped ignored)", got)
	}
	if got := roles(map[string]any{"groups": "staff"}); len(got) != 1 || got[0] != "viewer" {
		t.Errorf("string claim: roles = %v", got)
	}
	for _, over := range []map[string]any{{"groups": nil}, {"groups": 42}, {"groups": []string{"nobody"}}} {
		if got := roles(over); len(got) != 0 {
			t.Errorf("claim %v: roles = %v, want none", over, got)
		}
	}

	custom, _ := is.verifier(func(c *OIDCConfig) { c.GroupsClaim = "roles_from_idp" })
	raw := is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, map[string]any{"roles_from_idp": []string{"root"}}))
	p, err := custom.Verify(ctx, raw)
	if err != nil || len(p.Roles) != 2 {
		t.Errorf("custom groups claim: %v, %+v", err, p)
	}
	// The default claim is ignored when another one is configured.
	p, _ = custom.Verify(ctx, is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, map[string]any{"groups": []string{"root"}})))
	if p != nil && len(p.Roles) != 0 {
		t.Errorf("default claim read although another is configured: %v", p.Roles)
	}
}

func TestNewVerifierRejectsBadConfig(t *testing.T) {
	is := newTestIssuer(t)
	ctx := context.Background()
	base := func() OIDCConfig {
		return OIDCConfig{Issuer: is.url(), Audience: []string{testAudience}, HTTPClient: is.srv.Client()}
	}
	cases := map[string]func(*OIDCConfig){
		"no audience":     func(c *OIDCConfig) { c.Audience = nil },
		"http issuer":     func(c *OIDCConfig) { c.Issuer = "http://idp.example" },
		"empty issuer":    func(c *OIDCConfig) { c.Issuer = "" },
		"HS256 requested": func(c *OIDCConfig) { c.Algorithms = []string{"HS256"} },
		"none requested":  func(c *OIDCConfig) { c.Algorithms = []string{"none"} },
		"PS256 requested": func(c *OIDCConfig) { c.Algorithms = []string{"PS256"} },
		"issuer unknown":  func(c *OIDCConfig) { c.Issuer = is.url() + "/nowhere" },
		"unreachable idp": func(c *OIDCConfig) {
			c.Issuer = "https://127.0.0.1:1"
			c.HTTPClient = &http.Client{Timeout: time.Second}
		},
		"discovery no TLS": func(c *OIDCConfig) { c.HTTPClient = &http.Client{Timeout: time.Second} }, // the test CA is not trusted
	}
	for name, mod := range cases {
		cfg := base()
		mod(&cfg)
		if _, err := NewVerifier(ctx, cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNewVerifierDiscoveryMismatches(t *testing.T) {
	mk := func(doc func(url string) map[string]string) *httptest.Server {
		var srv *httptest.Server
		srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(doc(srv.URL))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	for name, doc := range map[string]func(string) map[string]string{
		"issuer differs": func(string) map[string]string {
			return map[string]string{"issuer": "https://other.example", "jwks_uri": "https://x/jwks"}
		},
		"jwks_uri over http": func(u string) map[string]string { return map[string]string{"issuer": u, "jwks_uri": "http://x/jwks"} },
		"no jwks_uri":        func(u string) map[string]string { return map[string]string{"issuer": u} },
	} {
		srv := mk(doc)
		_, err := NewVerifier(context.Background(), OIDCConfig{Issuer: srv.URL, Audience: []string{"a"}, HTTPClient: srv.Client()})
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A verifier built while the issuer is down discovers it on first use: until then a JWT is an
// outage (not a bad credential), attempts are bounded like JWKS refreshes, and a token that is
// not even a JWT never reaches the issuer (#57).
func TestLazyVerifierDiscoversOnFirstUse(t *testing.T) {
	is := newTestIssuer(t)
	rk := is.addRSA("rsa-1")
	is.discoveryFail.Store(true)
	clk := newClock()
	v, err := NewLazyVerifier(OIDCConfig{Issuer: is.url(), Audience: []string{testAudience}, HTTPClient: is.srv.Client(), Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := v.Discover(ctx); !errors.Is(err, ErrIssuerUnreachable) {
		t.Fatalf("Discover while down: %v, want ErrIssuerUnreachable", err)
	}
	if v.Discovered() {
		t.Fatal("discovered while the issuer is down")
	}
	if _, err := v.Verify(ctx, "not-a-jwt"); !errors.Is(err, ErrTokenInvalid) || is.discoveryHits.Load() != 1 {
		t.Fatalf("garbage: %v, %d discoveries (want a 401 and no new discovery)", err, is.discoveryHits.Load())
	}
	raw := func() string { return is.sign(jose.RS256, "rsa-1", rk, is.claims(clk, nil)) }
	for i := range 3 {
		_, err := v.Verify(ctx, raw())
		if !errors.Is(err, ErrIssuerUnreachable) || IsCredentialError(err) {
			t.Fatalf("JWT %d while down: %v, want an outage", i, err)
		}
	}
	if n := is.discoveryHits.Load(); n != 2 {
		t.Fatalf("%d discoveries for 3 JWTs within the cool-down, want 2 (start, then one retry)", n)
	}
	is.discoveryFail.Store(false)
	clk.advance(time.Minute)
	p, err := v.Verify(ctx, raw())
	if err != nil || p.Subject != "user:alice" {
		t.Fatalf("after the issuer is back: %v %+v", err, p)
	}
	if !v.Discovered() {
		t.Fatal("not marked discovered")
	}
}

// NewVerifier still fails when discovery fails, and tells an outage from a contradiction.
func TestNewVerifierTellsOutageFromMismatch(t *testing.T) {
	is := newTestIssuer(t)
	is.discoveryFail.Store(true)
	_, err := NewVerifier(context.Background(), OIDCConfig{Issuer: is.url(), Audience: []string{"a"}, HTTPClient: is.srv.Client()})
	if !errors.Is(err, ErrIssuerUnreachable) {
		t.Fatalf("issuer down: %v, want ErrIssuerUnreachable", err)
	}
	is.discoveryFail.Store(false)
	_, err = NewVerifier(context.Background(), OIDCConfig{Issuer: is.url() + "/", Audience: []string{"a"}, HTTPClient: is.srv.Client()})
	if err == nil || errors.Is(err, ErrIssuerUnreachable) {
		t.Fatalf("issuer mismatch: %v, want a configuration error", err)
	}
}
