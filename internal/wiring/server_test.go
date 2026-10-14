package wiring_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/audit"
	"github.com/VargaFoundation/trica/internal/auth"
	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/internal/transport"
	"github.com/VargaFoundation/trica/internal/web"
	"github.com/VargaFoundation/trica/internal/wiring"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const patience = 15 * time.Second

// rig is a real server, all components mounted, on a test database.
type rig struct {
	t         *testing.T
	base      string
	w         *wiring.Wiring
	url       string
	caDir     string
	token     string // a write token
	readToken string // a read-only token
	siteToken string // a write token limited to a site
	stop      func() error
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// newRig migrates a database, mints tokens (there is no CLI for it yet), and
// starts the server from a YAML configuration, as cmd/trica does.
func newRig(t *testing.T, extraYAML string) *rig {
	t.Helper()
	if os.Getenv(server.EnvDatabaseURL) != "" {
		t.Skip(server.EnvDatabaseURL + " is set; it would override the test database")
	}
	r := &rig{t: t, url: testdb.NewURL(t), caDir: filepath.Join(t.TempDir(), "ca")}
	ctx := context.Background()

	st, err := store.Open(ctx, r.url)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := &auth.Principal{Kind: auth.KindUser, Subject: "user:test", Roles: []string{"admin"}}
	ts := &auth.TokenStore{Store: st, Policy: auth.DefaultPolicy()}
	mk := func(name string, scopes, sites []string) string {
		raw, _, err := ts.Create(ctx, owner, auth.CreateParams{Name: name, Scopes: scopes, Sites: sites})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	r.token = mk("rw", []string{"write"}, nil)
	r.readToken = mk("ro", []string{"read"}, nil)
	r.siteToken = mk("site", []string{"write"}, []string{"paris-1"})
	st.Close()
	r.start(extraYAML)
	return r
}

// start runs a server on the rig's database and waits until it is ready; a
// server started earlier is stopped first, so a restart keeps the data.
func (r *rig) start(extraYAML string) {
	t := r.t
	t.Helper()
	if r.stop != nil {
		_ = r.stop()
	}
	ctx := context.Background()
	cfgYAML := fmt.Sprintf(`
database: {url_file: %s}
http: {addr: "127.0.0.1:0"}
sweep_interval: 1h
deduce: {interval: 50ms}
mcp: {disable_localhost_protection: true}
%s
`, writeFile(t, "url", r.url), strings.ReplaceAll(extraYAML, "{CA}", r.caDir))
	cfg, err := server.LoadConfig(writeFile(t, "trica.yaml", cfgYAML))
	if err != nil {
		t.Fatal(err)
	}
	r.w = wiring.New(cfg, "test")
	srv := server.New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), "test", r.w.Components()...)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Run(runCtx) }()
	var once sync.Once
	var stopErr error
	r.stop = func() error {
		once.Do(func() { cancel(); stopErr = <-done })
		return stopErr
	}
	t.Cleanup(func() { _ = r.stop() })

	deadline := time.Now().Add(patience)
	for {
		if a := srv.Addr(); a != "" {
			r.base = "http://" + a
			if res, err := http.Get(r.base + "/readyz"); err == nil {
				_ = res.Body.Close()
				if res.StatusCode == http.StatusOK {
					break
				}
			}
		}
		select {
		case err := <-done:
			t.Fatalf("server exited before ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("server not ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *rig) do(method, path, token, body string) (int, string) {
	r.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, r.base+path, strings.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

func (r *rig) mcpSession(token string) *gosdk.ClientSession {
	r.t.Helper()
	tr := &gosdk.StreamableClientTransport{
		Endpoint: r.base + mcp.Prefix, HTTPClient: &http.Client{Transport: bearerTransport{token}}, MaxRetries: -1,
	}
	cs, err := gosdk.NewClient(&gosdk.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// topology: srv-01 depends on sw-a1 (192.0.2.1). Failing sw-a1 takes srv-01 down.
func topology() string {
	b := sdk.NewBatch("", sdk.ScopePartial)
	b.ObservedAt = time.Now().UTC()
	b.Object("sw", "device").Identify("name", "sw-a1").Set("role", "switch")
	b.Object("srv", "device").Identify("name", "srv-01").Set("role", "server")
	b.Relate("depends_on", "srv", "sw")
	raw, _ := json.Marshal(b)
	return string(raw)
}

func TestServerMountsItsComponents(t *testing.T) {
	r := newRig(t, `
transport:
  addr: "127.0.0.1:0"
  ca_dir: "{CA}"
  hosts: [localhost, 127.0.0.1]
`)
	ctx := context.Background()

	t.Run("the API refuses a request without credential, serves one with", func(t *testing.T) {
		if code, _ := r.do("GET", "/api/v1/me", "", ""); code != http.StatusUnauthorized {
			t.Fatalf("no credential: %d", code)
		}
		if code, _ := r.do("GET", "/api/v1/me", "trica_1_forged", ""); code != http.StatusUnauthorized {
			t.Fatalf("forged credential: %d", code)
		}
		if code, body := r.do("GET", "/api/v1/me", r.token, ""); code != http.StatusOK {
			t.Fatalf("valid token: %d %s", code, body)
		}
	})

	t.Run("a read token cannot submit", func(t *testing.T) {
		if code, _ := r.do("POST", "/api/v1/batches", r.readToken, topology()); code != http.StatusForbidden {
			t.Fatalf("read token submitted: %d", code)
		}
	})

	t.Run("a token limited to a site cannot write an object that has no site", func(t *testing.T) {
		// The batch creates objects with no site: a grant on paris-1 must not
		// match "unknown site".
		if code, body := r.do("POST", "/api/v1/batches", r.siteToken, topology()); code != http.StatusForbidden {
			t.Fatalf("site-limited token wrote a site-less object: %d %s", code, body)
		}
	})

	t.Run("a batch through the API reaches the inventory", func(t *testing.T) {
		if code, body := r.do("POST", "/api/v1/batches", r.token, topology()); code != http.StatusOK {
			t.Fatalf("submit: %d %s", code, body)
		}
		_, body := r.do("GET", "/api/v1/objects?type=device", r.readToken, "")
		if !strings.Contains(body, "sw-a1") || !strings.Contains(body, "srv-01") {
			t.Fatalf("objects: %s", body)
		}
	})

	t.Run("MCP: 401 without a bearer, tools with one, and the graph sees the commit", func(t *testing.T) {
		if code, _ := r.do("POST", mcp.Prefix, "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusUnauthorized {
			t.Fatalf("mcp without credential: %d", code)
		}
		cs := r.mcpSession(r.readToken)
		tools, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range tools.Tools {
			names = append(names, tl.Name)
		}
		if !strings.Contains(strings.Join(names, " "), "impact_analysis") {
			t.Fatalf("tools: %v", names)
		}
		impact := func() string {
			res, err := cs.CallTool(ctx, &gosdk.CallToolParams{Name: "impact_analysis", Arguments: map[string]any{"objects": []string{"sw-a1"}}})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(res)
			if res.IsError {
				t.Fatalf("impact_analysis: %s", raw)
			}
			return string(raw)
		}
		if got := impact(); !strings.Contains(got, "srv-01") {
			t.Fatalf("the graph does not see the batch committed through the API: %s", got)
		}
		// A second commit must be visible at once: the cache is invalidated on commit.
		b := sdk.NewBatch("", sdk.ScopePartial)
		b.ObservedAt = time.Now().UTC()
		b.Object("sw", "device").Identify("name", "sw-a1")
		b.Object("srv", "device").Identify("name", "srv-02").Set("role", "server")
		b.Relate("depends_on", "srv", "sw")
		raw, _ := json.Marshal(b)
		if code, body := r.do("POST", "/api/v1/batches", r.token, string(raw)); code != http.StatusOK {
			t.Fatalf("second batch: %d %s", code, body)
		}
		if got := impact(); !strings.Contains(got, "srv-02") {
			t.Fatalf("the graph cache was not refreshed on commit: %s", got)
		}
	})

	t.Run("the audit log has a verified chain entry for the submission", func(t *testing.T) {
		st, err := store.Open(ctx, r.url)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		l := &audit.Log{Store: st}
		res, err := l.Verify(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Intact() || res.Entries < 1 {
			t.Fatalf("chain: %+v", res)
		}
		es, err := l.List(ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range es {
			if strings.HasPrefix(e.Actor, "token:") && e.Outcome == audit.OutcomeSuccess {
				found = true
			}
		}
		if !found {
			t.Fatalf("no entry for the token's submission: %+v", es)
		}
	})

	t.Run("the web UI serves its index", func(t *testing.T) {
		code, body := r.do("GET", "/", "", "")
		if code != http.StatusOK || !strings.Contains(strings.ToLower(body), "<html") {
			t.Fatalf("/: %d %.200s", code, body)
		}
		if !web.Built && !strings.Contains(strings.ToLower(body), "trica") {
			t.Fatalf("placeholder does not say what it is: %.200s", body)
		}
		if code, _ := r.do("GET", "/api/v1/nope", r.token, ""); code != http.StatusNotFound {
			t.Fatalf("an unknown API path must be a 404, not the SPA: %d", code)
		}
	})

	t.Run("the deducer runs and reports", func(t *testing.T) {
		deadline := time.Now().Add(patience)
		for {
			_, body := r.do("GET", "/metrics", "", "")
			if strings.Contains(body, "trica_deduce_runs_total") && !strings.Contains(body, "trica_deduce_runs_total 0\n") {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the deducer never ran:\n%s", body)
			}
			time.Sleep(50 * time.Millisecond)
		}
	})

	t.Run("the transport enrols an agent and its batch reaches the inventory", func(t *testing.T) {
		addr := r.w.TransportAddr()
		if addr == "" {
			t.Fatal("transport not listening")
		}
		st, err := store.Open(ctx, r.url)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		tok, err := transport.NewRegistry(st.Pool).NewEnrolmentToken(ctx, "agent:host-01", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		cl, err := transport.NewClient(transport.ClientConfig{
			ServerURL: "https://" + addr, ServerName: "localhost", CAFile: filepath.Join(r.caDir, "ca.pem"),
			Dir: t.TempDir(), EnrolToken: tok,
		})
		if err != nil {
			t.Fatal(err)
		}
		b := sdk.NewBatch("agent:host-01", sdk.ScopePartial)
		b.ObservedAt = time.Now().UTC()
		b.Object("h", "device").Identify("name", "host-01").Set("role", "server")
		if err := cl.Emit(ctx, b); err != nil {
			t.Fatal(err)
		}
		_, body := r.do("GET", "/api/v1/objects?type=device", r.readToken, "")
		if !strings.Contains(body, "host-01") {
			t.Fatalf("the agent's batch is not in the inventory: %s", body)
		}
		// A batch under another source than the certificate's is refused.
		other := sdk.NewBatch("agent:host-02", sdk.ScopePartial)
		other.Object("h", "device").Identify("name", "host-02")
		if err := cl.Emit(ctx, other); err == nil {
			t.Fatal("a batch under a foreign source was accepted")
		}
	})
}

// The device type library is applied once, at start: a device known before the
// restart is linked to its model, and a library that fails would not stop the
// server (its failure is logged, see runDeviceTypes).
// A device that appears while the server runs gets its device type without a
// restart (#55): the library is matched again every deduce.interval.
func TestServerSyncsTheDeviceTypeLibrary(t *testing.T) {
	r := newRig(t, "")
	models, err := devicetypes.Default()
	if err != nil || len(models) == 0 {
		t.Fatalf("library: %d models, %v", len(models), err)
	}
	m := models[0]
	b := sdk.NewBatch("", sdk.ScopePartial)
	b.ObservedAt = time.Now().UTC()
	b.Object("d", "device").Identify("name", "sw-a1").Set("vendor", m.Vendor).Set("model", m.Model)
	raw, _ := json.Marshal(b)
	if code, body := r.do("POST", "/api/v1/batches", r.token, string(raw)); code != http.StatusOK {
		t.Fatalf("submit: %d %s", code, body)
	}

	deadline := time.Now().Add(patience)
	for {
		_, body := r.do("GET", "/api/v1/objects?type=device_type", r.readToken, "")
		if strings.Contains(body, m.Model) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a device submitted after the start got no device type: %s", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestServerWithoutTransportOpensNoListener(t *testing.T) {
	r := newRig(t, "")
	if a := r.w.TransportAddr(); a != "" {
		t.Fatalf("transport listening on %s though not configured", a)
	}
}

func TestServerRefusesToStartOnAnUnknownGroupRole(t *testing.T) {
	// A typo in a role must stop the server, not drop or widen access.
	cfg := &server.Config{}
	cfg.Auth.GroupRoles = map[string][]string{"ops": {"admn"}}
	cfg.Database.URLFile = writeFile(t, "url", testdb.NewURL(t))
	loaded, err := server.LoadConfig(writeFile(t, "c.yaml", fmt.Sprintf("database: {url_file: %s}\nhttp: {addr: \"127.0.0.1:0\"}\nauth:\n  group_roles: {ops: [admn]}\n", cfg.Database.URLFile)))
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(loaded, slog.New(slog.NewJSONHandler(io.Discard, nil)), "test", wiring.New(loaded, "test").Components()...)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	err = srv.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("Run = %v, want an unknown role error", err)
	}
}
