package audit

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/auth"
)

type rig struct {
	t      *testing.T
	log    *Log
	tokens *auth.TokenStore
	h      http.Handler
}

// newRig mounts audit (outermost), authentication, a rate limiter and a Require-guarded mux,
// in the order the API will use.
func newRig(t *testing.T, limiter *auth.RateLimiter) *rig {
	t.Helper()
	return newRigWith(t, limiter, Options{})
}

// newRigWith is newRig with middleware options. The rig has a route whose
// handler records its own entry, as the API's do.
func newRigWith(t *testing.T, limiter *auth.RateLimiter, o Options) *rig {
	t.Helper()
	l := newLog(t)
	pol := auth.DefaultPolicy()
	ts := &auth.TokenStore{Store: l.Store, Policy: pol}
	a := &auth.Authenticator{Tokens: ts, Policy: pol}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/objects", auth.Require(auth.ActionRead)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	mux.Handle("POST /v1/proposals", auth.Require(auth.ActionPropose)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Annotate(r.Context(), "proposal", 42)
		Annotate(r.Context(), "password", "should-not-be-stored")
		w.WriteHeader(http.StatusCreated)
	})))
	mux.Handle("POST /v1/boom", auth.Require(auth.ActionRead)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	mux.Handle("POST /v1/self", auth.Require(auth.ActionPropose)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := l.Append(r.Context(), Entry{Actor: "user:alice", Action: "proposal.create", Target: "proposal/7"}); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
	})))
	mux.Handle("POST /v1/bad", auth.Require(auth.ActionRead)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnprocessableEntity)
	})))
	var inner http.Handler = mux
	if limiter != nil {
		inner = limiter.Middleware(inner)
	}
	inner = auth.Middleware(a)(inner)
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &rig{t: t, log: l, tokens: ts, h: NewMiddleware(l, o)(inner)}
}

func (r *rig) token(roles string, scopes ...string) string {
	r.t.Helper()
	raw, _, err := r.tokens.Create(context.Background(),
		&auth.Principal{Subject: "user:alice", Kind: auth.KindUser, Roles: []string{roles}},
		auth.CreateParams{Name: "t", Scopes: scopes})
	if err != nil {
		r.t.Fatal(err)
	}
	return raw
}

func (r *rig) do(method, target, bearer string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "192.0.2.9:5555"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func (r *rig) entries() []Entry {
	r.t.Helper()
	es, err := r.log.List(context.Background(), 100)
	if err != nil {
		r.t.Fatal(err)
	}
	// oldest first
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
	return es
}

func TestMiddlewareRecordsMutatingRequests(t *testing.T) {
	r := newRig(t, nil)
	tok := r.token("operator", auth.ScopeWrite)
	rec := r.do(http.MethodPost, "/v1/proposals?access_token=querysecret&x=1", tok, "X-Request-Id", "req-abc.1")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get(RequestIDHeader) != "req-abc.1" {
		t.Errorf("request id not echoed: %q", rec.Header().Get(RequestIDHeader))
	}
	es := r.entries()
	if len(es) != 1 {
		t.Fatalf("%d entries, want 1", len(es))
	}
	e := es[0]
	if e.Actor != "token:1" || e.Action != "http.POST" || e.Target != "/v1/proposals" || e.Outcome != OutcomeSuccess || e.RequestID != "req-abc.1" {
		t.Errorf("entry = %+v", e)
	}
	if e.Details["status"] != 201.0 || e.Details["proposal"] != 42.0 || e.Details["remote"] != "192.0.2.9" || e.Details["method"] != "POST" {
		t.Errorf("details = %v", e.Details)
	}
	if e.Details["password"] != Redacted {
		t.Errorf("an annotated secret was stored: %v", e.Details["password"])
	}
	dump := e.Target + " " + strings.Join(keysOf(e.Details), " ")
	for _, leak := range []string{"querysecret", tok} {
		if strings.Contains(dump, leak) {
			t.Errorf("%q leaked into the entry: %s", leak, dump)
		}
	}
	if res := verify(t, r.log); !res.Intact() {
		t.Errorf("chain broken: %+v", res.First)
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k, v := range m {
		out = append(out, k)
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestMiddlewareSkipsSuccessfulReads(t *testing.T) {
	r := newRig(t, nil)
	tok := r.token("viewer", auth.ScopeRead)
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		r.do(m, "/v1/objects", tok)
	}
	if n := len(r.entries()); n != 0 {
		t.Errorf("%d entries for successful reads, want 0", n)
	}
}

func TestMiddlewareRecordsDeniedRequests(t *testing.T) {
	r := newRig(t, nil)
	viewer := r.token("viewer", auth.ScopeRead)

	// 403: a viewer proposes.
	if rec := r.do(http.MethodPost, "/v1/proposals", viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	// 401 on a GET: denied requests are recorded whatever the method.
	if rec := r.do(http.MethodGet, "/v1/objects", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	if rec := r.do(http.MethodGet, "/v1/objects", "trica_1_"+strings.Repeat("Z", 43)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	es := r.entries()
	if len(es) != 3 {
		t.Fatalf("%d entries, want 3", len(es))
	}
	for _, e := range es {
		if e.Outcome != OutcomeDenied {
			t.Errorf("outcome %q for %s", e.Outcome, e.Details["status"])
		}
	}
	if es[0].Actor != "token:1" || es[0].Details["status"] != 403.0 || es[0].Details["reason"] == nil {
		t.Errorf("403 entry = %+v", es[0])
	}
	if es[1].Actor != "anonymous" || es[1].Details["status"] != 401.0 || es[1].Details["reason"] != "no credential presented" {
		t.Errorf("401 entry = %+v", es[1])
	}
	if es[1].Action != "http.GET" {
		t.Errorf("action = %s", es[1].Action)
	}
	if strings.Contains(strings.Join(keysOf(es[2].Details), " "), strings.Repeat("Z", 43)) {
		t.Error("the presented credential was recorded")
	}
}

func TestMiddlewareRecordsRateLimited(t *testing.T) {
	lim := auth.NewRateLimiter(0.001, 1)
	r := newRig(t, lim)
	tok := r.token("viewer", auth.ScopeRead)
	if rec := r.do(http.MethodGet, "/v1/objects", tok); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec := r.do(http.MethodGet, "/v1/objects", tok); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d", rec.Code)
	}
	es := r.entries()
	if len(es) != 1 || es[0].Outcome != OutcomeDenied || es[0].Details["status"] != 429.0 || es[0].Actor != "token:1" {
		t.Fatalf("entries = %+v", es)
	}
}

func TestMiddlewareFailureOutcomeAndPanic(t *testing.T) {
	r := newRig(t, nil)
	tok := r.token("viewer", auth.ScopeRead)
	if rec := r.do(http.MethodPost, "/v1/bad", tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		r.do(http.MethodPost, "/v1/boom", tok)
	}()
	es := r.entries()
	if len(es) != 2 {
		t.Fatalf("%d entries, want 2", len(es))
	}
	if es[0].Outcome != OutcomeFailure || es[0].Details["status"] != 422.0 {
		t.Errorf("422 entry = %+v", es[0])
	}
	if es[1].Outcome != OutcomeFailure || es[1].Details["status"] != 500.0 {
		t.Errorf("panic entry = %+v: a mutating request that crashed must still be recorded", es[1])
	}
}

func TestMiddlewareRequestIDs(t *testing.T) {
	r := newRig(t, nil)
	tok := r.token("viewer", auth.ScopeRead)
	rec := r.do(http.MethodPost, "/v1/bad", tok, "X-Request-Id", "bad id\twith junk;"+strings.Repeat("x", 100))
	id := rec.Header().Get(RequestIDHeader)
	if id == "" || strings.ContainsAny(id, " \t;") || len(id) > 64 {
		t.Errorf("unsafe client request id was kept: %q", id)
	}
	rec = r.do(http.MethodPost, "/v1/bad", tok)
	id2 := rec.Header().Get(RequestIDHeader)
	if id2 == "" || id2 == id {
		t.Errorf("no fresh request id generated: %q", id2)
	}
	es := r.entries()
	if es[0].RequestID != id || es[1].RequestID != id2 {
		t.Errorf("recorded request ids %q %q, responded %q %q", es[0].RequestID, es[1].RequestID, id, id2)
	}
}

func TestRequestIDFromContext(t *testing.T) {
	if RequestID(context.Background()) != "" {
		t.Error("request id without middleware")
	}
	Annotate(context.Background(), "k", "v") // must not panic outside the middleware
	r := newRig(t, nil)
	var seen string
	h := Middleware(r.log, nil)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = RequestID(req.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || seen != rec.Header().Get(RequestIDHeader) {
		t.Errorf("context id %q, header %q", seen, rec.Header().Get(RequestIDHeader))
	}
}

func TestStatusWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	w.WriteHeader(http.StatusAccepted)
	w.WriteHeader(http.StatusTeapot) // ignored by net/http, and by the recorded status
	if w.status != http.StatusAccepted || w.Unwrap() != rec {
		t.Errorf("status %d", w.status)
	}
}

// A handler that records its own entry gets no second one from the middleware.
func TestMiddlewareDoesNotDuplicateTheHandlersEntry(t *testing.T) {
	r := newRig(t, nil)
	tok := r.token("operator", auth.ScopeWrite)
	if rec := r.do(http.MethodPost, "/v1/self", tok); rec.Code != http.StatusCreated {
		t.Fatalf("status %d", rec.Code)
	}
	es := r.entries()
	if len(es) != 1 || es[0].Action != "proposal.create" {
		t.Fatalf("entries = %+v, want the handler's only", es)
	}
}

// With RefusalsOnly, what the handlers record themselves (mutating calls,
// successful or failed) is left to them; 401, 403 and 429 are recorded.
func TestRefusalsOnlyRecordsRefusals(t *testing.T) {
	r := newRigWith(t, nil, Options{RefusalsOnly: true})
	tok := r.token("operator", auth.ScopeWrite)
	viewer := r.token("viewer", auth.ScopeRead)
	for _, c := range []struct {
		method, path, tok string
		want              int
	}{
		{http.MethodPost, "/v1/proposals", tok, http.StatusCreated},
		{http.MethodPost, "/v1/bad", tok, http.StatusUnprocessableEntity},
		{http.MethodGet, "/v1/objects", tok, http.StatusOK},
		{http.MethodGet, "/v1/objects", "", http.StatusUnauthorized},
		{http.MethodPost, "/v1/proposals", viewer, http.StatusForbidden},
	} {
		if rec := r.do(c.method, c.path, c.tok); rec.Code != c.want {
			t.Fatalf("%s %s: status %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
	es := r.entries()
	if len(es) != 2 {
		t.Fatalf("%d entries, want the 401 and the 403: %+v", len(es), es)
	}
	if es[0].Details["status"] != 401.0 || es[0].Actor != "anonymous" || es[1].Details["status"] != 403.0 || es[1].Actor != "token:2" {
		t.Errorf("entries = %+v", es)
	}
}

// A flood of 429s is one entry per caller and period; the next entry says how
// many were not recorded.
func TestRateLimitedRequestsAreSampled(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	lim := auth.NewRateLimiter(0.000001, 1)
	lim.Now = clock
	r := newRigWith(t, lim, Options{RateLimitedEvery: time.Minute, Now: clock})
	tok := r.token("viewer", auth.ScopeRead)
	limited := func(n int, bearer string) {
		t.Helper()
		for range n {
			if rec := r.do(http.MethodGet, "/v1/objects", bearer); rec.Code != http.StatusTooManyRequests {
				t.Fatalf("status %d", rec.Code)
			}
		}
	}
	if rec := r.do(http.MethodGet, "/v1/objects", tok); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	limited(4, tok)
	if es := r.entries(); len(es) != 1 || es[0].Details["status"] != 429.0 || es[0].Details["suppressed"] != nil {
		t.Fatalf("after the first period: %+v", es)
	}
	mu.Lock()
	now = now.Add(61 * time.Second)
	mu.Unlock()
	limited(2, tok)
	es := r.entries()
	if len(es) != 2 || es[1].Actor != "token:1" || es[1].Details["suppressed"] != 3.0 {
		t.Fatalf("after the second period: %+v", es)
	}
}

// The sampler forgets the callers whose period is over, and logs what it
// counted for them, since no entry will carry it.
func TestSamplerSweepLogsWhatItDrops(t *testing.T) {
	var buf strings.Builder
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	s := &sampler{every: time.Minute, now: func() time.Time { return now }, logger: slog.New(slog.NewTextHandler(&buf, nil)), seen: map[string]*sample{}}
	for _, key := range []string{"token:1", "token:1", "token:1", "ip:192.0.2.9"} {
		s.admit(key)
	}
	now = now.Add(2 * time.Minute)
	if ok, n := s.admit("token:2"); !ok || n != 0 {
		t.Fatalf("admit = %v, %d", ok, n)
	}
	if len(s.seen) != 1 {
		t.Errorf("%d keys kept, want the new one only", len(s.seen))
	}
	if !strings.Contains(buf.String(), "caller=token:1 count=2") || strings.Contains(buf.String(), "192.0.2.9") {
		t.Errorf("log = %q", buf.String())
	}
}
