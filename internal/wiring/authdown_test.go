package wiring

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/auth"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// unreachableStore is a store whose database does not answer: pgxpool
// connects lazily, so the first query fails.
func unreachableStore(t *testing.T) *store.Store {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &store.Store{Pool: pool}
}

// A token store that does not answer is not a bad credential: the client gets
// 503 with Retry-After from the API and from MCP, and keeps its token (#54).
func TestAuthBackendOutageIs503(t *testing.T) {
	st := unreachableStore(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Authorizer{Auth: &auth.Authenticator{Tokens: &auth.TokenStore{Store: st}, Policy: auth.DefaultPolicy(), Logger: quiet}, Policy: auth.DefaultPolicy()}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer trica_1_secret")
	if _, err := a.Principal(r); !errors.Is(err, api.ErrAuthUnavailable) || errors.Is(err, api.ErrUnauthenticated) {
		t.Fatalf("Principal: %v, want ErrAuthUnavailable", err)
	}

	eng := &reconcile.Engine{Store: st, Schema: schema.Builtin}
	apiSrv, err := api.New(api.Config{Engine: eng, Authorizer: a, Auditor: &api.MemoryAuditor{}, Logger: quiet, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	mcpSrv, err := mcp.New(mcp.Config{Engine: eng, Authorizer: a, Auditor: &api.MemoryAuditor{}, Logger: quiet, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, body string
		h                        http.Handler
	}{
		{"api", http.MethodGet, "/api/v1/me", "", apiSrv.Handler()},
		{"mcp", http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, http.StripPrefix("/mcp", mcpSrv.Handler())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			do := func(cred string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Accept", "application/json, text/event-stream")
				if cred != "" {
					r.Header.Set("Authorization", "Bearer "+cred)
				}
				w := httptest.NewRecorder()
				tc.h.ServeHTTP(w, r)
				return w
			}
			w := do("trica_1_secret")
			if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
				t.Fatalf("well-formed token, store down: %d Retry-After=%q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
			}
			// A credential that is bad on its face is still a 401: the outage
			// does not turn every refusal into a 503.
			for _, cred := range []string{"", "nonsense"} {
				if w := do(cred); w.Code != http.StatusUnauthorized {
					t.Fatalf("credential %q: %d %s", cred, w.Code, w.Body)
				}
			}
		})
	}
}
