package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/auth"
	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/internal/transport"
	"github.com/VargaFoundation/trica/internal/wiring"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// adminEnv is a migrated database, a configuration file with a custom RBAC
// policy and a transport section, and the objects the real server builds from
// them.
type adminEnv struct {
	t      *testing.T
	cfg    string
	caDir  string
	policy *auth.Policy
	conf   *server.Config
	st     *store.Store
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	url := testdb.NewURL(t)
	t.Setenv(server.EnvDatabaseURL, url)
	t.Setenv("TRICA_CONFIG", "")
	dir := t.TempDir()
	policyFile := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(policyFile, []byte("roles:\n  netops:\n    actions: [read, propose]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	caDir := filepath.Join(dir, "ca")
	cfgFile := filepath.Join(dir, "trica.yaml")
	yaml := fmt.Sprintf("auth:\n  policy_file: %s\ntransport:\n  addr: \"127.0.0.1:0\"\n  ca_dir: %s\n  hosts: [\"127.0.0.1\"]\n", policyFile, caDir)
	if err := os.WriteFile(cfgFile, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &adminEnv{t: t, cfg: cfgFile, caDir: caDir}
	if _, _, err := e.run("migrate", "up"); err != nil {
		t.Fatal(err)
	}
	var err error
	if e.conf, err = server.LoadConfig(cfgFile); err != nil {
		t.Fatal(err)
	}
	if e.policy, err = wiring.Policy(e.conf); err != nil {
		t.Fatal(err)
	}
	if e.st, err = store.Open(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.st.Close)
	return e
}

// run executes a command with the configuration file; stdout and stderr are
// kept apart, which is the point of the create commands.
func (e *adminEnv) run(args ...string) (stdout, stderr string, err error) {
	e.t.Helper()
	var out, errOut bytes.Buffer
	// The flag goes right after the command (and its action): migrate takes a
	// positional argument, which ends flag parsing.
	n := min(len(args), 2)
	if args[0] == "migrate" {
		n = 1
	}
	args = append(append(append([]string{}, args[:n]...), "--config", e.cfg), args[n:]...)
	err = run(context.Background(), args, nil, &out, &errOut, os.Getenv)
	return out.String(), errOut.String(), err
}

// authorizer is the one the real server builds around the same store and policy.
func (e *adminEnv) authorizer() *wiring.Authorizer {
	a := &auth.Authenticator{
		Tokens: &auth.TokenStore{Store: e.st, Policy: e.policy},
		Policy: e.policy,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	return &wiring.Authorizer{Auth: a, Policy: e.policy}
}

func (e *adminEnv) request(token string) *http.Request {
	r, err := http.NewRequest(http.MethodGet, "http://trica.test/api/v1/me", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestTokenCreateAuthenticatesThroughTheWiring(t *testing.T) {
	e := newAdminEnv(t)
	az := e.authorizer()

	out, errOut, err := e.run("token", "create", "--name", "claude-code", "--owner", "vincent", "--roles", "netops", "--scopes", "write", "--types", "device", "--expires", "720h")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, errOut)
	}
	token := strings.TrimSuffix(out, "\n")
	if !strings.HasPrefix(token, auth.TokenPrefix) || strings.ContainsAny(token, " \n\t") {
		t.Fatalf("stdout must be exactly the token, got %q", out)
	}
	if strings.Contains(errOut, token) {
		t.Fatalf("the token is on stderr:\n%s", errOut)
	}
	if !strings.Contains(errOut, "claude-code") || !strings.Contains(errOut, "user:vincent") {
		t.Fatalf("metadata missing from stderr: %q", errOut)
	}

	p, err := az.Principal(e.request(token))
	if err != nil {
		t.Fatalf("a created token does not authenticate: %v", err)
	}
	for _, c := range []struct {
		action, typ string
		want        bool
	}{
		{"read", "device", true},
		{"propose", "device", true}, // write scope on a netops owner, a role only the policy file defines
		{"propose", "rack", false},  // restricted to the device type
		{"approve", "device", false},
		{"admin", "device", false},
	} {
		if got := az.Allow(p, c.action, c.typ, ""); got != c.want {
			t.Errorf("Allow(%s, %s) = %v, want %v", c.action, c.typ, got, c.want)
		}
	}

	toks, err := (&auth.TokenStore{Store: e.st, Policy: e.policy}).List(context.Background())
	if err != nil || len(toks) != 1 {
		t.Fatalf("list: %v %v", toks, err)
	}
	if tk := toks[0]; tk.CreatedBy != "user:vincent" || tk.ExpiresAt == nil || time.Until(*tk.ExpiresAt) < 719*time.Hour || len(tk.Types) != 1 {
		t.Fatalf("stored token = %+v", tk)
	}
}

func TestTokenCreateRefusals(t *testing.T) {
	e := newAdminEnv(t)
	base := []string{"token", "create", "--name", "x", "--owner", "vincent"}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"a role outside the policy", append(base, "--roles", "ghost", "--scopes", "read"), "not in the RBAC policy"},
		{"a scope the owner's roles do not grant", append(base, "--roles", "viewer", "--scopes", "admin"), auth.ErrScopeExceedsRole.Error()},
		{"write for a viewer", append(base, "--roles", "viewer", "--scopes", "write"), auth.ErrScopeExceedsRole.Error()},
		{"an unknown scope", append(base, "--roles", "admin", "--scopes", "root"), "unknown scope"},
		{"no scope", append(base, "--roles", "admin"), "required"},
		{"no owner", []string{"token", "create", "--name", "x", "--roles", "admin", "--scopes", "read"}, "required"},
		{"a negative expiry", append(base, "--roles", "admin", "--scopes", "read", "--expires=-1h"), "positive"},
	} {
		out, _, err := e.run(c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
		if out != "" {
			t.Errorf("%s: stdout %q, want nothing", c.name, out)
		}
	}
	toks, _ := (&auth.TokenStore{Store: e.st, Policy: e.policy}).List(context.Background())
	if len(toks) != 0 {
		t.Fatalf("a refused creation stored %d token(s)", len(toks))
	}
	// The ceiling is the owner's roles: an admin owner may have admin.
	if _, errOut, err := e.run(append(base, "--roles", "admin", "--scopes", "admin")...); err != nil {
		t.Fatalf("admin owner, admin scope: %v %s", err, errOut)
	}
}

func TestTokenListAndRevoke(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	az := e.authorizer()
	secret1, _, err := e.run("token", "create", "--name", "first", "--owner", "vincent", "--roles", "admin", "--scopes", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret2, _, err := e.run("token", "create", "--name", "second", "--owner", "vincent", "--roles", "viewer", "--scopes", "read")
	if err != nil {
		t.Fatal(err)
	}
	secret1, secret2 = strings.TrimSpace(secret1), strings.TrimSpace(secret2)
	if _, err := az.Principal(e.request(secret1)); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := e.run("token", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"first", "second", "user:vincent", "ID"} {
		if !strings.Contains(out, s) {
			t.Errorf("list lacks %q:\n%s", s, out)
		}
	}
	// No secret and no hash, anywhere.
	rows, err := e.st.Pool.Query(ctx, `SELECT encode(hash, 'hex') FROM api_tokens`)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	rows.Close()
	if len(hashes) != 2 {
		t.Fatalf("hashes = %v", hashes)
	}
	parts1, parts2 := strings.Split(secret1, "_"), strings.Split(secret2, "_")
	for _, s := range append(hashes, secret1, secret2, parts1[2], parts2[2]) {
		if strings.Contains(out+errOut, s) {
			t.Fatalf("list leaks %q:\n%s", s, out)
		}
	}

	id := parts1[1]
	if _, _, err := e.run("token", "revoke", "--id", id); err != nil {
		t.Fatal(err)
	}
	if _, err := az.Principal(e.request(secret1)); err == nil {
		t.Fatal("a revoked token still authenticates")
	}
	if _, err := az.Principal(e.request(secret2)); err != nil {
		t.Fatalf("revoking one token revoked another: %v", err)
	}
	// Nothing is deleted: the row stays, marked.
	var n int
	var revoked bool
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*), bool_or(revoked_at IS NOT NULL) FROM api_tokens`).Scan(&n, &revoked); err != nil || n != 2 || !revoked {
		t.Fatalf("rows %d revoked %v err %v", n, revoked, err)
	}
	out, _, _ = e.run("token", "list")
	if !strings.Contains(out, "first") {
		t.Fatalf("a revoked token must stay listed:\n%s", out)
	}
	// Revoking twice, or an unknown id, is an error.
	for _, id := range []string{id, "99999", "0"} {
		if _, _, err := e.run("token", "revoke", "--id", id); err == nil {
			t.Errorf("revoke %s accepted", id)
		}
	}
}

func TestAdminCommandUsage(t *testing.T) {
	e := newAdminEnv(t)
	for _, args := range [][]string{{"token"}, {"token", "frobnicate"}, {"token", "list", "extra"}, {"enrol"}, {"enrol", "list"}, {"token", "create", "--no-such-flag"}} {
		if _, _, err := e.run(args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestEnrolCreateIsRedeemedOnce(t *testing.T) {
	e := newAdminEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, errOut, err := e.run("enrol", "create", "--binding", "agent:host-01", "--ttl", "1h")
	if err != nil {
		t.Fatalf("enrol create: %v\n%s", err, errOut)
	}
	token := strings.TrimSuffix(out, "\n")
	if token == "" || strings.ContainsAny(token, " \n") || strings.Contains(errOut, token) {
		t.Fatalf("stdout must be exactly the token, and only there: %q / %q", out, errOut)
	}
	if want := filepath.Join(e.caDir, pki.CACertFile); !strings.Contains(errOut, want) {
		t.Fatalf("stderr does not say where the CA certificate is (%s): %q", want, errOut)
	}

	// The real transport server on the same database, as the wiring builds it.
	ca, err := pki.LoadOrCreateCA(e.caDir, "trica transport CA", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueServer([]string{"127.0.0.1"}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	srv, err := transport.NewServer(transport.ServerConfig{
		CA: ca, Registry: transport.NewRegistry(e.st.Pool), ServerCert: cert,
		Sink: transport.SinkFunc(func(_ context.Context, b *sdk.Batch, id transport.Identity) error {
			got = append(got, id.Binding+"<-"+b.Source)
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	defer func() { cancel(); <-done }()

	newClient := func(dir string) *transport.Client {
		c, err := transport.NewClient(transport.ClientConfig{
			ServerURL: "https://" + ln.Addr().String(), CAFile: filepath.Join(e.caDir, pki.CACertFile),
			Dir: dir, EnrolToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		return c
	}
	b := sdk.NewBatch("agent:host-01", sdk.ScopePartial)
	b.Object("h", "device").Identify("name", "host-01")

	if err := newClient(t.TempDir()).Emit(ctx, b); err != nil {
		t.Fatalf("a token minted by the command is refused: %v", err)
	}
	if len(got) != 1 || got[0] != "agent:host-01<-agent:host-01" {
		t.Fatalf("batch not delivered under the binding: %v", got)
	}
	// A second machine with the same token: refused, nothing delivered.
	err = newClient(t.TempDir()).Emit(ctx, b)
	var se *transport.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
		t.Fatalf("a used token: %v, want a 401", err)
	}
	if len(got) != 1 {
		t.Fatalf("a used token delivered a batch: %v", got)
	}
}

func TestEnrolCreateRefusals(t *testing.T) {
	e := newAdminEnv(t)
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no binding", []string{"enrol", "create"}},
		{"one segment (a wildcard over every agent)", []string{"enrol", "create", "--binding", "agent"}},
		{"a space", []string{"enrol", "create", "--binding", "agent:host 01"}},
		{"a slash", []string{"enrol", "create", "--binding", "agent:../x"}},
		{"a zero ttl", []string{"enrol", "create", "--binding", "agent:host-01", "--ttl", "0s"}},
	} {
		out, _, err := e.run(c.args...)
		if err == nil || out != "" {
			t.Errorf("%s: accepted (%q, %v)", c.name, out, err)
		}
	}
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM enrol_tokens`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused command stored %d token(s) (%v)", n, err)
	}
}

func TestEnrolWithoutTransportWarns(t *testing.T) {
	t.Setenv(server.EnvDatabaseURL, testdb.NewURL(t))
	t.Setenv("TRICA_CONFIG", "")
	if _, err := runCmd(t, "migrate", "up"); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"enrol", "create", "--binding", "collector:arista"}, nil, &out, &errOut, os.Getenv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "transport.addr is not set") || out.Len() == 0 {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
}
