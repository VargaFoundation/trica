package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/web"
)

func serveWith(t *testing.T, cfg web.Config, target string) *http.Response {
	t.Helper()
	h, err := web.NewWithConfig(testFS(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Result()
}

var sso = web.Config{OIDC: &web.OIDC{Issuer: "https://idp.example.test/realms/trica", ClientID: "trica-ui"}}

// Without single sign-on the page is told so, and the policy keeps connect-src to the origin.
func TestConfigWithoutSSOSaysNull(t *testing.T) {
	r := serve(t, http.MethodGet, "/ui-config.json")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status %d", r.StatusCode)
	}
	var got struct {
		OIDC *web.OIDC `json:"oidc"`
	}
	if err := json.Unmarshal([]byte(body(t, r)), &got); err != nil {
		t.Fatal(err)
	}
	if got.OIDC != nil {
		t.Fatalf("oidc = %+v, want null", got.OIDC)
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "connect-src 'self';") {
		t.Fatal("connect-src must stay 'self' without SSO")
	}
}

// The page reads the client settings from /ui-config.json, with defaults filled in.
func TestConfigServesTheClientSettings(t *testing.T) {
	r := serveWith(t, sso, "/ui-config.json")
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control %q: the config must never be cached", cc)
	}
	var got struct {
		OIDC web.OIDC `json:"oidc"`
	}
	if err := json.Unmarshal([]byte(body(t, r)), &got); err != nil {
		t.Fatal(err)
	}
	want := web.OIDC{Issuer: "https://idp.example.test/realms/trica", ClientID: "trica-ui", Scopes: []string{"openid", "profile", "email"}, Bearer: "access_token"}
	if got.OIDC.Issuer != want.Issuer || got.OIDC.ClientID != want.ClientID || got.OIDC.Bearer != want.Bearer ||
		strings.Join(got.OIDC.Scopes, " ") != strings.Join(want.Scopes, " ") {
		t.Fatalf("config = %+v, want %+v", got.OIDC, want)
	}
}

// The browser may reach the issuer, and only the issuer, from script.
func TestCSPAllowsTheIssuerOriginOnly(t *testing.T) {
	for _, target := range []string{"/", "/ui-config.json", "/assets/index-3fa91c.js", "/objects/x"} {
		csp := serveWith(t, sso, target).Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "connect-src 'self' https://idp.example.test;") {
			t.Errorf("%s: connect-src lacks the issuer origin: %s", target, csp)
		}
		if strings.Contains(csp, "/realms") {
			t.Errorf("%s: the policy must name an origin, not a path: %s", target, csp)
		}
		for _, want := range []string{"script-src 'self';", "default-src 'none'", "form-action 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: policy weakened, lacks %q: %s", target, want, csp)
			}
		}
	}
}

func TestConfigIsValidated(t *testing.T) {
	for name, o := range map[string]web.OIDC{
		"http issuer":        {Issuer: "http://idp.example.test", ClientID: "c"},
		"issuer with a path": {Issuer: "https://idp.example.test/realms/a?x=1", ClientID: "c"},
		"issuer credentials": {Issuer: "https://u:p@idp.example.test", ClientID: "c"},
		"no issuer":          {ClientID: "c"},
		"no client id":       {Issuer: "https://idp.example.test"},
		"blank client id":    {Issuer: "https://idp.example.test", ClientID: "  "},
		"bad scope":          {Issuer: "https://idp.example.test", ClientID: "c", Scopes: []string{"a b"}},
		"bad bearer":         {Issuer: "https://idp.example.test", ClientID: "c", Bearer: "refresh_token"},
	} {
		t.Run(name, func(t *testing.T) {
			o := o
			if _, err := web.NewWithConfig(testFS(), web.Config{OIDC: &o}); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestOpenIDScopeIsAlwaysRequested(t *testing.T) {
	cfg := web.Config{OIDC: &web.OIDC{Issuer: "https://idp.example.test", ClientID: "c", Scopes: []string{"groups"}, Bearer: web.BearerIDToken}}
	var got struct {
		OIDC web.OIDC `json:"oidc"`
	}
	if err := json.Unmarshal([]byte(body(t, serveWith(t, cfg, "/ui-config.json"))), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.OIDC.Scopes, " ") != "openid groups" || got.OIDC.Bearer != "id_token" {
		t.Fatalf("got %+v", got.OIDC)
	}
}

// The callback address is a client route: the shell answers it, so the identity
// provider's redirect lands in the application.
func TestOIDCCallbackIsAClientRoute(t *testing.T) {
	r := serveWith(t, sso, "/oidc/callback?code=abc&state=xyz")
	if r.StatusCode != http.StatusOK || !strings.Contains(body(t, r), `id="root"`) {
		t.Fatalf("status %d", r.StatusCode)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control %q", cc)
	}
	if rp := r.Header.Get("Referrer-Policy"); rp != "no-referrer" {
		t.Fatalf("Referrer-Policy %q: the code in the URL must not leak", rp)
	}
}
