package wiring

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/auth"
	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/mcp"
)

func TestAuthorizerAllow(t *testing.T) {
	a := &Authorizer{Policy: auth.DefaultPolicy()}
	user := func(roles ...string) api.Principal {
		p, err := toAPI(&auth.Principal{Kind: auth.KindUser, Subject: "user:alice", Roles: roles})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	token := func(sites, types []string) api.Principal {
		p, err := toAPI(&auth.Principal{Kind: auth.KindToken, Subject: "token:7", Roles: []string{"operator"},
			Scopes: []string{"write"}, Sites: sites, Types: types})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name   string
		p      api.Principal
		action string
		typ    string
		site   string
		want   bool
	}{
		{"a viewer reads", user("viewer"), "read", "device", "paris-1", true},
		{"a viewer does not propose", user("viewer"), "propose", "device", "paris-1", false},
		{"an unknown action is refused", user("admin"), "delete", "device", "paris-1", false},
		{"no role, no right", user(), "read", "device", "paris-1", false},
		{"a site-limited token matches its site", token([]string{"paris-1"}, nil), "propose", "device", "paris-1", true},
		{"a site-limited token does not match another site", token([]string{"paris-1"}, nil), "propose", "device", "lyon-1", false},
		{"a site-limited token does not match an empty site", token([]string{"paris-1"}, nil), "propose", "device", "", false},
		{"a type-limited token does not match an empty type", token(nil, []string{"device"}), "propose", "", "paris-1", false},
		{"a type-limited token matches its type", token(nil, []string{"device"}), "propose", "device", "", true},
		{"a principal the adapter did not build is refused", api.Principal{Subject: "x", Kind: api.KindUser}, "read", "device", "paris-1", false},
		{"a foreign credential is refused", api.Principal{Subject: "x", Kind: api.KindUser, Credential: "admin"}, "read", "device", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.Allow(tc.p, tc.action, tc.typ, tc.site); got != tc.want {
				t.Fatalf("Allow = %v, want %v", got, tc.want)
			}
		})
	}
	if (&Authorizer{}).Allow(user("admin"), "read", "device", "") {
		t.Fatal("an authorizer without a policy allowed")
	}
}

func TestToAPIAttribution(t *testing.T) {
	u, _ := toAPI(&auth.Principal{Kind: auth.KindUser, Subject: "user:alice"})
	if u.BatchSource() != "user:alice" {
		t.Errorf("user source %q", u.BatchSource())
	}
	k, _ := toAPI(&auth.Principal{Kind: auth.KindToken, Subject: "token:7"})
	if k.BatchSource() != "api:token-7" {
		t.Errorf("token source %q", k.BatchSource())
	}
	if _, err := toAPI(&auth.Principal{Kind: "service"}); err == nil {
		t.Error("an unknown kind was accepted")
	}
}

func TestPrincipalFailsClosed(t *testing.T) {
	a := &Authorizer{Auth: &auth.Authenticator{Tokens: &auth.TokenStore{}, Policy: auth.DefaultPolicy()}, Policy: auth.DefaultPolicy()}
	for name, h := range map[string]string{
		"no header":    "",
		"basic":        "Basic dXNlcjpwdw==",
		"empty bearer": "Bearer ",
		"not a token":  "Bearer nonsense",
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			if h != "" {
				r.Header.Set("Authorization", h)
			}
			if _, err := a.Principal(r); !errors.Is(err, api.ErrUnauthenticated) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	// The query string is never a credential.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me?access_token=trica_1_x", nil)
	if _, err := a.Principal(r); !errors.Is(err, api.ErrUnauthenticated) {
		t.Fatalf("query credential: %v", err)
	}
}

func TestRateLimitIsEnforced(t *testing.T) {
	l := auth.NewRateLimiter(0.001, 1)
	h := limit(l, &Authorizer{Auth: &auth.Authenticator{Tokens: &auth.TokenStore{}}}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	get := func() int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.RemoteAddr = "192.0.2.9:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if get() != http.StatusOK {
		t.Fatal("first request refused")
	}
	if get() != http.StatusTooManyRequests {
		t.Fatal("the burst was not enforced")
	}
}

func TestGraphErrorMapping(t *testing.T) {
	if _, err := convert[mcp.ImpactResult](nil, graph.ErrUnknownObject); !errors.Is(err, mcp.ErrUnknownObject) {
		t.Fatalf("err = %v", err)
	}
	boom := errors.New("boom")
	if _, err := convert[mcp.ImpactResult](nil, boom); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	res, err := convert[mcp.ImpactResult](&graph.ImpactResult{Failed: []string{"a"}}, nil)
	if err != nil || len(res.Failed) != 1 || res.Failed[0] != "a" {
		t.Fatalf("round trip: %+v %v", res, err)
	}
}

func TestAuditorRejectsAnUnknownOutcome(t *testing.T) {
	if err := (&Auditor{}).Record(context.Background(), api.AuditEntry{Outcome: "maybe"}); err == nil {
		t.Fatal("an unknown outcome was recorded")
	}
}
