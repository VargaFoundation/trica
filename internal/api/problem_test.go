package api_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
)

// lockedBuffer is a log sink safe for concurrent writers.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// serve builds another server on the same engine, with its configuration changed.
func (e *env) serve(mutate func(c *api.Config)) http.Handler {
	e.t.Helper()
	c := api.Config{Engine: e.eng, Authorizer: e.auth, Auditor: e.aud, Version: "test", Now: e.clk.now}
	mutate(&c)
	s, err := api.New(c)
	if err != nil {
		e.t.Fatal(err)
	}
	return s.Handler()
}

func serveOnce(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestProblemShapes(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})

	// Unknown path: a problem, not the mux's plain text.
	r := e.get("/api/v1/nothing-here", tokReader)
	p := wantProblem(t, r, 404, "not-found")
	if p.Instance != "/api/v1/nothing-here" {
		t.Fatalf("instance %q", p.Instance)
	}
	wantProblem(t, e.get("/elsewhere", tokReader), 404, "not-found")

	// Wrong method: 405 with Allow.
	r = e.do("POST", "/api/v1/objects", tokAdmin, `{}`)
	wantProblem(t, r, 405, "method-not-allowed")
	if allow := r.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Fatalf("Allow %q", allow)
	}
	r = e.do("DELETE", "/api/v1/batches", tokAdmin, nil)
	wantProblem(t, r, 405, "method-not-allowed")
	if allow := r.Header.Get("Allow"); !strings.Contains(allow, "POST") {
		t.Fatalf("Allow %q", allow)
	}

	// Each kind of failure has its own status, type and a human title and detail.
	cases := []struct {
		name   string
		resp   resp
		status int
		slug   string
	}{
		{"bad request", e.get("/api/v1/objects?limit=zero", tokReader), 400, "invalid-request"},
		{"bad query", e.get("/api/v1/objects?q="+"role:(", tokReader), 400, "invalid-query"},
		{"unauthenticated", e.get("/api/v1/objects", ""), 401, "unauthenticated"},
		{"forbidden", e.do("POST", "/api/v1/proposals/1/approve", tokReader, nil), 403, "forbidden"},
		{"not found", e.get("/api/v1/objects/00000000-0000-4000-8000-000000000000", tokReader), 404, "not-found"},
	}
	for _, c := range cases {
		pr := wantProblem(t, c.resp, c.status, c.slug)
		if pr.Detail == "" || pr.Instance == "" {
			t.Errorf("%s: problem without detail or instance: %+v", c.name, pr)
		}
	}
	wantStatus(t, e.do("POST", "/api/v1/saved-queries", tokProposer, `{"name":"dup","query":"role:switch"}`), 201)
	conflict := wantProblem(t, e.do("POST", "/api/v1/saved-queries", tokProposer, `{"name":"dup","query":"role:switch"}`), 409, "conflict")
	if conflict.Detail == "" || conflict.Instance == "" {
		t.Errorf("conflict without detail or instance: %+v", conflict)
	}
	large := wantProblem(t, e.do("POST", "/api/v1/batches", tokProposer, `{"scope":"partial","pad":"`+strings.Repeat("a", 9<<20)+`"}`), 413, "too-large")
	if large.Instance != "/api/v1/batches" {
		t.Errorf("instance %q", large.Instance)
	}
}

func TestSecurityHeadersOnEveryAnswer(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	for _, r := range []resp{
		e.get("/api/v1/objects", tokReader), e.get("/api/v1/objects", ""), e.get("/api/v1/nothing", tokReader),
		e.get("/api/v1/healthz", ""), e.do("POST", "/api/v1/objects", tokReader, nil), e.get("/api/v1/openapi.json", tokReader),
	} {
		if got := r.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("status %d: Cache-Control %q", r.Status, got)
		}
		if got := r.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("status %d: X-Content-Type-Options %q", r.Status, got)
		}
	}
}

func TestInternalErrorsDoNotLeakTheirCause(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.st.Pool.Close() // every query now fails with a driver error
	for _, path := range []string{"/api/v1/objects", "/api/v1/changes?from=2026-01-01T00:00:00Z", "/api/v1/proposals", "/api/v1/discrepancies", "/api/v1/saved-queries", "/api/v1/health/collection"} {
		r := e.get(path, tokAdmin)
		p := wantProblem(t, r, 500, "internal")
		body := strings.ToLower(string(r.Body))
		for _, leak := range []string{"pool", "pgx", "postgres", "sql", "closed", "127.0.0.1"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: the 500 mentions %q: %s", path, leak, r.Body)
			}
		}
		if p.Detail == "" {
			t.Errorf("%s: no detail", path)
		}
	}
}

// panicker is an Authorizer that crashes.
type panicker struct{ api.Authorizer }

func (panicker) Allow(api.Principal, string, string, string) bool { panic("boom: super secret detail") }

func TestAPanicInAHandlerIsAnInternalErrorNotAnOutage(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	logs := &lockedBuffer{}
	h := e.serve(func(c *api.Config) {
		c.Authorizer = panicker{e.auth}
		c.Logger = slog.New(slog.NewJSONHandler(logs, nil))
	})
	rec := serveOnce(h, "GET", "/api/v1/objects", tokReader)
	if rec.Code != 500 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status %d type %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("the panic value is in the answer: %s", rec.Body)
	}
	if !strings.Contains(logs.String(), "boom") {
		t.Fatalf("the panic was not logged: %s", logs.String())
	}
	// The probes do not depend on the authorizer: the server is still up.
	if rec := serveOnce(h, "GET", "/api/v1/healthz", ""); rec.Code != 200 {
		t.Fatalf("healthz after a panic: %d", rec.Code)
	}
}

func TestAccessLogDoesNotRecordTheQueryString(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	logs := &lockedBuffer{}
	h := e.serve(func(c *api.Config) { c.Logger = slog.New(slog.NewJSONHandler(logs, nil)) })
	rec := serveOnce(h, "GET", "/api/v1/search?q=needle-in-the-query-string", tokReader)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	out := logs.String()
	if !strings.Contains(out, "/api/v1/search") {
		t.Fatalf("no access log line: %q", out)
	}
	if strings.Contains(out, "needle-in-the-query-string") {
		t.Fatalf("the query string was logged: %s", out)
	}
	if strings.Contains(out, tokReader) {
		t.Fatalf("the token was logged: %s", out)
	}
}

// Every list answers in the same envelope and refuses the same bad bounds.
func TestPaginationBounds(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	pid, _ := e.withMaterial(t)
	_ = pid
	sw := e.ids["sw"]
	wantStatus(t, e.do("POST", "/api/v1/saved-queries", tokProposer, `{"name":"q1","query":"role:switch"}`), 201)
	lists := []string{
		"/api/v1/objects", "/api/v1/search?q=sw", "/api/v1/proposals", "/api/v1/discrepancies", "/api/v1/saved-queries",
		"/api/v1/changes?from=2026-01-01T00:00:00Z", "/api/v1/whohad?key=os_version&value=4.20.7M",
		"/api/v1/objects/" + sw + "/history", "/api/v1/objects/" + sw + "/relations", "/api/v1/objects/" + sw + "/timeline",
	}
	join := func(path, q string) string {
		if strings.Contains(path, "?") {
			return path + "&" + q
		}
		return path + "?" + q
	}
	for _, path := range lists {
		for _, bad := range []string{"limit=0", "limit=-1", "limit=1001", "limit=abc", "limit=1.5", "offset=-1", "offset=abc", "limit=99999999999999999999"} {
			wantProblem(t, e.get(join(path, bad), tokReader), 400, "invalid-request")
		}
		def := e.page(path, tokReader)
		if def.Limit != api.DefaultLimit || def.Offset != 0 {
			t.Errorf("%s: default envelope limit %d offset %d", path, def.Limit, def.Offset)
		}
		maxed := e.page(join(path, "limit=1000"), tokReader)
		if maxed.Limit != 1000 || maxed.Total != def.Total {
			t.Errorf("%s: limit=1000: limit %d total %d (default total %d)", path, maxed.Limit, maxed.Total, def.Total)
		}
		one := e.page(join(path, "limit=1"), tokReader)
		if len(one.Items) > 1 || one.Total != def.Total {
			t.Errorf("%s: limit=1 gave %d items, total %d vs %d", path, len(one.Items), one.Total, def.Total)
		}
		far := e.page(join(path, "offset=100000"), tokReader)
		if len(far.Items) != 0 || far.Total != def.Total || far.Offset != 100000 {
			t.Errorf("%s: offset beyond the end: %d items, total %d vs %d, offset %d", path, len(far.Items), far.Total, def.Total, far.Offset)
		}
		if far.Items == nil {
			t.Errorf("%s: items is null, not an empty list", path)
		}
	}

	// The window is the right one: consecutive pages tile the whole result.
	all := ids(t, e.page("/api/v1/objects?sort=name&limit=1000", tokReader), "id")
	if len(all) != 8 {
		t.Fatalf("objects: %d", len(all))
	}
	var tiled []string
	for off := 0; off < len(all); off += 3 {
		tiled = append(tiled, ids(t, e.page("/api/v1/objects?sort=name&limit=3&offset="+itoa(int64(off)), tokReader), "id")...)
	}
	if strings.Join(tiled, ",") != strings.Join(all, ",") {
		t.Fatalf("pages do not tile the list:\n%v\n%v", tiled, all)
	}
	two := e.page("/api/v1/objects?sort=name&limit=2&offset=1", tokReader)
	if got := ids(t, two, "id"); len(got) != 2 || got[0] != all[1] || got[1] != all[2] || two.Total != 8 {
		t.Fatalf("limit=2&offset=1: %v (total %d)", got, two.Total)
	}
}
