package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Bearer tokens of the test principals. They are made up and grant nothing outside the tests.
const (
	tokAdmin     = "t-admin"      // user alice: every action
	tokReader    = "t-reader"     // user bob: read
	tokProposer  = "t-proposer"   // user carol: read + propose
	tokApprover  = "t-approver"   // user dave: read + approve
	tokCollector = "t-collector"  // token bound to arista:sw-a1: propose
	tokNoSource  = "t-nosource"   // token bound to no source: propose
	tokMCP       = "t-mcp"        // token bound to mcp:assistant: read + propose
	tokProposeNR = "t-propose-nr" // user erin: propose only, cannot read
	tokDevices   = "t-devices"    // user frank: read limited to devices
	tokSiteParis = "t-site-paris" // user gina: read limited to the site paris-1
	tokDevProp   = "t-dev-prop"   // user hugo: read + propose limited to devices
	tokRackView  = "t-rack-view"  // user iris: read limited to racks, devices and models (not their parts)
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// regHolder lets a test replace the schema registry while the server runs.
type regHolder struct {
	mu  sync.Mutex
	reg *schema.Registry
}

func (h *regHolder) get() *schema.Registry  { h.mu.Lock(); defer h.mu.Unlock(); return h.reg }
func (h *regHolder) set(r *schema.Registry) { h.mu.Lock(); defer h.mu.Unlock(); h.reg = r }

type env struct {
	t    *testing.T
	eng  *reconcile.Engine
	st   *store.Store
	srv  *api.Server
	h    http.Handler
	aud  *api.MemoryAuditor
	auth *api.StaticAuthorizer
	clk  *clock
	reg  *regHolder
	// ids maps a fixture name to the object id.
	ids map[string]string
}

var t0 = time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

// newEnv builds the engine, the server and a small anonymised inventory,
// loaded through the engine like any other write.
func newEnv(t *testing.T, policy reconcile.Policy) *env {
	t.Helper()
	// Database creation is slow on a busy machine: let tests overlap.
	t.Parallel()
	st := testdb.New(t)
	e := &env{t: t, st: st, aud: &api.MemoryAuditor{}, clk: &clock{t: t0}, reg: &regHolder{reg: schema.Builtin()}, ids: map[string]string{}}
	e.eng = &reconcile.Engine{Store: st, Schema: e.reg.get, Policy: policy, Now: e.clk.now}
	e.fixtures()
	e.auth = &api.StaticAuthorizer{Tokens: map[string]api.Grant{
		tokAdmin:     {Principal: api.Principal{Subject: "alice", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose, api.ActionApprove, api.ActionAdmin}},
		tokReader:    {Principal: api.Principal{Subject: "bob", Kind: api.KindUser}, Actions: []string{api.ActionRead}},
		tokProposer:  {Principal: api.Principal{Subject: "carol", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose}},
		tokApprover:  {Principal: api.Principal{Subject: "dave", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionApprove}},
		tokCollector: {Principal: api.Principal{Subject: "collector-sw-a1", Kind: api.KindToken, Source: "arista:sw-a1"}, Actions: []string{api.ActionPropose}},
		tokNoSource:  {Principal: api.Principal{Subject: "orphan", Kind: api.KindToken}, Actions: []string{api.ActionPropose}},
		tokMCP:       {Principal: api.Principal{Subject: "mcp-assistant", Kind: api.KindToken, Source: "mcp:assistant"}, Actions: []string{api.ActionRead, api.ActionPropose}},
		tokProposeNR: {Principal: api.Principal{Subject: "erin", Kind: api.KindUser}, Actions: []string{api.ActionPropose}},
		tokDevices:   {Principal: api.Principal{Subject: "frank", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Types: []string{"device"}},
		tokSiteParis: {Principal: api.Principal{Subject: "gina", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Sites: []string{e.ids["paris"]}},
		tokDevProp:   {Principal: api.Principal{Subject: "hugo", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose}, Types: []string{"device"}},
		tokRackView:  {Principal: api.Principal{Subject: "iris", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Types: []string{"rack", "device", "device_type"}},
	}}
	srv, err := api.New(api.Config{Engine: e.eng, Authorizer: e.auth, Auditor: e.aud, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test", Now: e.clk.now,
		Images: testImages, Library: testLibrary})
	if err != nil {
		t.Fatal(err)
	}
	e.srv, e.h = srv, srv.Handler()
	return e
}

func (e *env) apply(b *sdk.Batch) *reconcile.Result {
	e.t.Helper()
	b.ObservedAt = e.clk.now()
	res, err := e.eng.Apply(context.Background(), b, "", "")
	if err != nil {
		e.t.Fatalf("fixture %s: %v", b.Source, err)
	}
	for ref, id := range res.Objects {
		e.ids[ref] = id
	}
	return res
}

// fixtures: two sites, a rack in each, a switch in Paris with two interfaces
// and a server in Lyon. Everything is invented: documentation addresses only.
func (e *env) fixtures() {
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("paris", "site").Identify("code", "PAR1").Set("name", "paris-1")
	b.Object("lyon", "site").Identify("code", "LYO1").Set("name", "lyon-1")
	b.Object("rack-p", "rack").Identify("name", "rack-a").In("paris")
	b.Object("rack-l", "rack").Identify("name", "rack-b").In("lyon")
	e.apply(b)

	c := sdk.NewBatch("arista:sw-a1", sdk.ScopeFull)
	c.Object("sw", "device").Identify("serial", "JPE-SW-A1").Identify("name", "sw-a1").
		Set("role", "switch").Set("mgmt_ip", "192.0.2.10").Set("os_version", "4.20.7M").Set("vendor", "Acme")
	c.Object("e1", "interface").Identify("name", "Ethernet1").In("sw").Set("oper_status", "up").Set("speed_mbps", 10000).Set("mac", "02:00:5e:00:53:01")
	c.Object("e2", "interface").Identify("name", "Ethernet2").In("sw").Set("oper_status", "down")
	c.Health = &sdk.Health{Target: "192.0.2.10", OK: true, Collector: "arista", Version: "0.1", Items: 3, Duration: 1500 * time.Millisecond}
	// The rack is not in this batch: designate it by its id.
	c.Relations = append(c.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref("sw"), To: sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: e.ids["rack-p"]}}, Attributes: map[string]any{}})
	e.apply(c)

	d := sdk.NewBatch("agent:srv-01", sdk.ScopeFull)
	d.Object("srv", "device").Identify("serial", "SRV-01-SN").Identify("name", "srv-01").
		Set("role", "server").Set("primary_ip", "198.51.100.21").Set("os_version", "6.1.0")
	d.Relations = append(d.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref("srv"), To: sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: e.ids["rack-l"]}}, Attributes: map[string]any{}})
	e.apply(d)
}

// resp is one HTTP answer.
type resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r resp) json(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("answer is not the expected JSON (%d): %v\n%s", r.Status, err, r.Body)
	}
}

func (r resp) obj(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	r.json(t, &m)
	return m
}

// do sends a request; body may be nil, a string (raw), or any value (JSON).
func (e *env) do(method, path, token string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return resp{Status: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
}

func (e *env) get(path, token string) resp { return e.do("GET", path, token, nil) }

// wantStatus fails unless the answer has this status.
func wantStatus(t testing.TB, r resp, want int) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d, want %d: %s", r.Status, want, r.Body)
	}
}

// wantProblem fails unless the answer is an RFC 9457 problem with this status and slug.
func wantProblem(t testing.TB, r resp, status int, slug string) api.Problem {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d, want %d: %s", r.Status, status, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q, want application/problem+json", ct)
	}
	var p api.Problem
	r.json(t, &p)
	if p.Status != status || p.Title == "" || p.Type != "urn:trica:problem:"+slug {
		t.Fatalf("problem %+v, want status %d type urn:trica:problem:%s", p, status, slug)
	}
	return p
}

// page is the envelope of every list, items kept raw.
type page struct {
	Total     int               `json:"total"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
	Truncated bool              `json:"truncated"`
	Items     []json.RawMessage `json:"items"`
}

func (e *env) page(path, token string) page {
	e.t.Helper()
	r := e.get(path, token)
	wantStatus(e.t, r, 200)
	var p page
	r.json(e.t, &p)
	return p
}

// field extracts a string field of a raw JSON object.
func field(t testing.TB, raw json.RawMessage, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	s, _ := m[key].(string)
	return s
}

func ids(t testing.TB, p page, key string) []string {
	t.Helper()
	var out []string
	for _, it := range p.Items {
		out = append(out, field(t, it, key))
	}
	return out
}

// userBatch is a batch body as a client would send it.
func userBatch(source string, build func(b *sdk.Batch)) *sdk.Batch {
	b := sdk.NewBatch(source, sdk.ScopePartial)
	b.ObservedAt = t0.Add(time.Hour)
	build(b)
	return b
}
