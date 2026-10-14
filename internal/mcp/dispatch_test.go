package mcp_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// These tests need no database: they prove the guards that stand before any
// data is read (authentication, the action gate, the audit trail, the tools
// offered). A tool that got past a guard would reach the empty store and fail
// with "internal", which is not what is asserted.

const (
	lRead    = "l-read"
	lPropose = "l-propose"
	lBoth    = "l-both"
	lNone    = "l-none"
)

type lite struct {
	srv  *mcp.Server
	aud  *api.MemoryAuditor
	auth *api.StaticAuthorizer
}

func newLite(t *testing.T) *lite {
	t.Helper()
	reg := schema.Builtin()
	eng := &reconcile.Engine{Store: &store.Store{}, Schema: func() *schema.Registry { return reg }}
	l := &lite{aud: &api.MemoryAuditor{}, auth: &api.StaticAuthorizer{Tokens: map[string]api.Grant{
		lRead:    {Principal: api.Principal{Subject: "bob", Kind: api.KindUser}, Actions: []string{api.ActionRead}},
		lPropose: {Principal: api.Principal{Subject: "erin", Kind: api.KindUser}, Actions: []string{api.ActionPropose}},
		lBoth:    {Principal: api.Principal{Subject: "alice", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose}},
		lNone:    {Principal: api.Principal{Subject: "ivan", Kind: api.KindUser}},
	}}}
	srv, err := mcp.New(mcp.Config{Engine: eng, Authorizer: l.auth, Auditor: l.aud, Graph: &fakeGraph{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	l.srv = srv
	return l
}

// validArgs are arguments that pass each tool's input schema.
var validArgs = map[string]m{
	"search":             {"query": "sw-a1"},
	"list_objects":       {},
	"filter_objects":     {"filters": []m{{"attribute": "role", "op": "eq", "value": "switch"}}},
	"rank_filter":        {"attribute": "power_w"},
	"forecast_threshold": {"attribute": "wear_pct", "threshold": 80},
	"version_compliance": {"target": "4.30.2"},
	"get_object":         {"object": "sw-a1"},
	"object_summary":     {"object": "sw-a1"},
	"object_history":     {"object": "sw-a1"},
	"changes_since":      {"since": "24h"},
	"describe_schema":    {},
	"impact_analysis":    {"objects": []string{"sw-a1"}},
	"network_path":       {"from": "sw-a1", "to": "sw-b1"},
	"propose_change":     {"reason": "r", "objects": []m{{"ref": "a", "type": "device", "identity": m{"name": "x"}}}},
}

func TestActionGate(t *testing.T) {
	l := newLite(t)
	read := []string{"search", "list_objects", "filter_objects", "rank_filter", "forecast_threshold", "version_compliance", "get_object",
		"object_summary", "object_history", "changes_since", "describe_schema", "impact_analysis", "network_path"}

	t.Run("a credential without read is refused by every read tool, before any data is touched", func(t *testing.T) {
		before := len(l.aud.Entries())
		cs := connectListing(t, l.srv, l.auth.Tokens[lPropose].Principal)
		for _, tool := range read {
			denied(t, cs, tool, validArgs[tool], "forbidden")
		}
		entries := l.aud.Entries()[before:]
		if len(entries) != len(read) {
			t.Fatalf("%d calls, %d audit entries", len(read), len(entries))
		}
		for i, e := range entries {
			if e.Action != "mcp.tool."+read[i] || e.Outcome != api.OutcomeDenied || e.Actor != "erin" || e.Kind != api.KindUser {
				t.Errorf("entry %d: %+v", i, e)
			}
			if r, _ := e.Details["reason"].(string); !strings.Contains(r, "not granted") {
				t.Errorf("entry %d does not say why: %v", i, e.Details)
			}
		}
	})
	t.Run("a credential with no right at all is refused everywhere, the write tool included", func(t *testing.T) {
		cs := connectListing(t, l.srv, l.auth.Tokens[lNone].Principal)
		for _, tool := range read {
			denied(t, cs, tool, validArgs[tool], "forbidden")
		}
		if got := toolNames(t, cs); len(got) != len(read) {
			t.Fatalf("tools offered to a credential that cannot propose: %v", got)
		}
	})
	t.Run("the write tool is offered to who may propose, and to nobody else", func(t *testing.T) {
		has := func(token string) bool {
			for _, n := range toolNames(t, connectListing(t, l.srv, l.auth.Tokens[token].Principal)) {
				if n == "propose_change" {
					return true
				}
			}
			return false
		}
		if has(lRead) || has(lNone) || !has(lPropose) || !has(lBoth) {
			t.Fatalf("propose_change offered: read=%v none=%v propose=%v both=%v", has(lRead), has(lNone), has(lPropose), has(lBoth))
		}
	})
	t.Run("a reader that calls the write tool anyway is refused", func(t *testing.T) {
		cs := connectListing(t, l.srv, l.auth.Tokens[lRead].Principal)
		refused(t, cs, "propose_change", validArgs["propose_change"])
	})
	t.Run("an empty proposal is refused before the engine is reached", func(t *testing.T) {
		cs := connectListing(t, l.srv, l.auth.Tokens[lPropose].Principal)
		a := call(t, cs, "propose_change", m{"reason": "r"})
		if !a.IsError || !strings.HasPrefix(a.Text, "invalid-request") {
			t.Fatalf("got %q", a.Text)
		}
	})
	t.Run("no tool survives without a name in the audit trail", func(t *testing.T) {
		for _, e := range l.aud.Entries() {
			if !strings.HasPrefix(e.Action, "mcp.tool.") || e.Details["tool"] == nil || e.At.IsZero() {
				t.Errorf("audit entry %+v", e)
			}
		}
	})
}

// Secrets are refused wherever a tool takes an attribute name, before the
// store is asked anything (the empty store of these tests would fail with
// "internal" if it were).
func TestSecretsAreRefusedBeforeTheStore(t *testing.T) {
	l := newLite(t)
	cs := connectListing(t, l.srv, l.auth.Tokens[lBoth].Principal)
	for _, attr := range []string{"snmp_community", "password", "api_token", "private_key", "psk"} {
		t.Run(attr, func(t *testing.T) {
			denied(t, cs, "search", m{"query": attr + ":x"}, "sensitive-attribute")
			denied(t, cs, "search", m{"query": "type:device sort:-" + attr}, "sensitive-attribute")
			denied(t, cs, "search", m{"query": "has:" + attr}, "sensitive-attribute")
			denied(t, cs, "list_objects", m{"sort": "-" + attr}, "sensitive-attribute")
			denied(t, cs, "filter_objects", m{"filters": []m{{"attribute": attr, "op": "exists"}}}, "forbidden")
			denied(t, cs, "rank_filter", m{"attribute": attr}, "forbidden")
			denied(t, cs, "forecast_threshold", m{"attribute": attr, "threshold": 1}, "forbidden")
			denied(t, cs, "object_history", m{"object": "00000000-0000-0000-0000-000000000001", "attribute": attr}, "forbidden")
			denied(t, cs, "version_compliance", m{"target": "1.0", "attribute": attr}, "forbidden")
			denied(t, cs, "propose_change", m{"reason": "r", "objects": []m{{"ref": "a", "type": "device", "identity": m{"name": "x"}, "attributes": m{attr: "x"}}}}, "secret-attribute")
		})
	}
}

// switching lets a test change the credential between two calls of one session.
type switching struct {
	mu    sync.Mutex
	token string
}

func (s *switching) set(token string) { s.mu.Lock(); defer s.mu.Unlock(); s.token = token }
func (s *switching) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	tok := s.token
	s.mu.Unlock()
	r = r.Clone(r.Context())
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestHTTPAuthentication(t *testing.T) {
	l := newLite(t)
	mux := http.NewServeMux()
	mux.Handle(mcp.Prefix, l.srv.Handler())
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)

	post := func(token string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, web.URL+mcp.Prefix,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		return res
	}
	t.Run("no credential, no answer", func(t *testing.T) {
		res := post("")
		if res.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Fatalf("status %d, WWW-Authenticate %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("Cache-Control %q", res.Header.Get("Cache-Control"))
		}
	})
	t.Run("an unknown credential, no answer", func(t *testing.T) {
		if res := post("made-up"); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d", res.StatusCode)
		}
	})
	t.Run("a valid credential is served", func(t *testing.T) {
		if res := post(lRead); res.StatusCode != http.StatusOK {
			t.Fatalf("status %d", res.StatusCode)
		}
	})
	t.Run("a client without credential cannot even open a session", func(t *testing.T) {
		tr := &gosdk.StreamableClientTransport{Endpoint: web.URL + mcp.Prefix, MaxRetries: -1}
		if _, err := gosdk.NewClient(&gosdk.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), tr, nil); err == nil {
			t.Fatal("connected without a credential")
		}
	})
	t.Run("the credential is checked on every call, not once per session", func(t *testing.T) {
		sw := &switching{token: lBoth}
		tr := &gosdk.StreamableClientTransport{Endpoint: web.URL + mcp.Prefix, HTTPClient: &http.Client{Transport: sw}, MaxRetries: -1}
		cs, err := gosdk.NewClient(&gosdk.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), tr, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		offered := func() bool {
			for _, n := range toolNames(t, cs) {
				if n == "propose_change" {
					return true
				}
			}
			return false
		}
		if !offered() {
			t.Fatal("the proposer is not offered propose_change")
		}
		sw.set(lRead) // same session, a weaker credential
		if offered() {
			t.Fatal("the session kept the rights of the credential it opened with")
		}
		refused(t, cs, "propose_change", validArgs["propose_change"])
		sw.set("revoked")
		if _, err := cs.ListTools(context.Background(), nil); err == nil {
			t.Fatal("a credential that stopped being valid was still served")
		}
	})
	t.Run("a call is attributed to the credential of its own request", func(t *testing.T) {
		sw := &switching{token: lNone}
		tr := &gosdk.StreamableClientTransport{Endpoint: web.URL + mcp.Prefix, HTTPClient: &http.Client{Transport: sw}, MaxRetries: -1}
		cs, err := gosdk.NewClient(&gosdk.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), tr, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		before := len(l.aud.Entries())
		denied(t, cs, "describe_schema", m{}, "forbidden")
		sw.set(lPropose)
		denied(t, cs, "describe_schema", m{}, "forbidden")
		entries := l.aud.Entries()[before:]
		if len(entries) != 2 || entries[0].Actor != "ivan" || entries[1].Actor != "erin" {
			t.Fatalf("audit %+v", entries)
		}
	})
}
