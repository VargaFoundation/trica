package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Bearer tokens of the test principals: made up, they grant nothing outside the tests.
const (
	tokAdmin     = "t-admin"      // user alice: every action
	tokReader    = "t-reader"     // user bob: read
	tokMCP       = "t-mcp"        // token bound to mcp:assistant: read + propose
	tokProposeNR = "t-propose-nr" // user erin: propose only, cannot read
	tokDevices   = "t-devices"    // user frank: read limited to devices
	tokSiteParis = "t-site-paris" // user gina: read limited to the site paris-1
	tokDevProp   = "t-dev-prop"   // user hugo: read + propose limited to devices
	tokNone      = "t-none"       // user ivan: no action at all
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// t0 is the start of the invented history.
var t0 = time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

type env struct {
	t    *testing.T
	eng  *reconcile.Engine
	st   *store.Store
	srv  *mcp.Server
	aud  *api.MemoryAuditor
	auth *api.StaticAuthorizer
	clk  *clock
	// ids maps a fixture ref to the object id.
	ids   map[string]string
	graph *fakeGraph
}

// fakeGraph is a GraphQuerier with canned answers.
type fakeGraph struct {
	mu      sync.Mutex
	impact  *mcp.ImpactResult
	path    []mcp.Hop
	err     error
	failed  []string
	anchors []string
	from    string
	to      string
	rels    []string
}

func (g *fakeGraph) Impact(_ context.Context, failed, anchors []string) (*mcp.ImpactResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failed, g.anchors = failed, anchors
	return g.impact, g.err
}

func (g *fakeGraph) Path(_ context.Context, from, to string, rels []string) ([]mcp.Hop, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.from, g.to, g.rels = from, to, rels
	return g.path, g.err
}

// newEnv builds the engine, the MCP server and a small anonymised inventory,
// loaded through the engine like any other write. policy decides which sources
// are queued as proposals; graph may be nil.
func newEnv(t *testing.T, policy reconcile.Policy, withGraph bool) *env {
	return newEnvWith(t, policy, withGraph, schema.Builtin())
}

// newEnvWith is newEnv over a given schema.
func newEnvWith(t *testing.T, policy reconcile.Policy, withGraph bool, reg *schema.Registry) *env {
	t.Helper()
	t.Parallel()
	st := testdb.New(t)
	e := &env{t: t, st: st, aud: &api.MemoryAuditor{}, clk: &clock{t: t0}, ids: map[string]string{}, graph: &fakeGraph{}}
	e.eng = &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Policy: policy, Now: e.clk.now}
	e.fixtures()
	e.auth = &api.StaticAuthorizer{Tokens: map[string]api.Grant{
		tokAdmin:     {Principal: api.Principal{Subject: "alice", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose, api.ActionApprove, api.ActionAdmin}},
		tokReader:    {Principal: api.Principal{Subject: "bob", Kind: api.KindUser}, Actions: []string{api.ActionRead}},
		tokMCP:       {Principal: api.Principal{Subject: "mcp-assistant", Kind: api.KindToken, Source: "mcp:assistant"}, Actions: []string{api.ActionRead, api.ActionPropose}},
		tokProposeNR: {Principal: api.Principal{Subject: "erin", Kind: api.KindUser}, Actions: []string{api.ActionPropose}},
		tokDevices:   {Principal: api.Principal{Subject: "frank", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Types: []string{"device"}},
		tokSiteParis: {Principal: api.Principal{Subject: "gina", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Sites: []string{e.ids["paris"]}},
		tokDevProp:   {Principal: api.Principal{Subject: "hugo", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose}, Types: []string{"device"}},
		tokNone:      {Principal: api.Principal{Subject: "ivan", Kind: api.KindUser}},
	}}
	cfg := mcp.Config{Engine: e.eng, Authorizer: e.auth, Auditor: e.aud, Version: "test", Now: e.clk.now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if withGraph {
		cfg.Graph = e.graph
	}
	srv, err := mcp.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.srv = srv
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

func idRef(typ, id string) sdk.Endpoint {
	return sdk.Endpoint{Type: typ, Identity: map[string]string{sdk.IDKey: id}}
}

// fixtures: two sites with racks, switches, twelve servers, a version policy
// and a cable between a switch port and a server port. Everything is invented:
// documentation addresses, made-up serials, locally administered MACs.
func (e *env) fixtures() {
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("paris", "site").Identify("code", "PAR1").Set("name", "paris-1")
	b.Object("lyon", "site").Identify("code", "LYO1").Set("name", "lyon-1")
	b.Object("rack-1", "rack").Identify("name", "rack-1").In("paris").Set("u_height", 42)
	b.Object("rack-2", "rack").Identify("name", "rack-2").In("paris").Set("u_height", 42)
	b.Object("rack-b", "rack").Identify("name", "rack-b").In("lyon").Set("u_height", 42)
	b.Object("pol", "version_policy").Identify("name", "switch-os").Set("software", "network-os").Set("attribute", "os_version").
		Set("selector", "role:switch").Set("reference_version", "4.30.2").Set("max_minor_lag", 1)
	e.apply(b)

	type sw struct {
		ref, serial, ip, os, airflow, rack string
		power                              float64
	}
	switches := []sw{
		{"sw-a1", "JPE-SW-A1", "192.0.2.10", "4.20.7M", "rear_to_front", "rack-1", 180},
		{"sw-a2", "JPE-SW-A2", "192.0.2.11", "4.30.2", "front_to_rear", "rack-1", 175},
		{"sw-b1", "JPE-SW-B1", "192.0.2.12", "4.19.9", "rear_to_front", "rack-b", 160},
		{"sw-b2", "JPE-SW-B2", "192.0.2.13", "", "front_to_rear", "rack-b", 150},
		{"sw-b3", "JPE-SW-B3", "192.0.2.14", "4.29.9", "front_to_rear", "rack-b", 140},
	}
	for _, s := range switches {
		c := sdk.NewBatch("arista:"+s.ref, sdk.ScopeFull)
		o := c.Object(s.ref, "device").Identify("serial", s.serial).Identify("name", s.ref).
			Set("role", "switch").Set("vendor", "Acme").Set("mgmt_ip", s.ip).Set("airflow", s.airflow).Set("power_w", s.power)
		if s.os != "" {
			o.Set("os_version", s.os)
		}
		for _, n := range []string{"Ethernet1", "Ethernet2"} {
			c.Object(s.ref+"/"+n, "interface").Identify("name", n).In(s.ref).Set("oper_status", "up")
		}
		c.Relations = append(c.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref(s.ref), To: idRef("rack", e.ids[s.rack]), Attributes: map[string]any{}})
		e.apply(c)
	}
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("srv-%02d", i)
		d := sdk.NewBatch("agent:"+name, sdk.ScopeFull)
		d.Object(name, "device").Identify("serial", fmt.Sprintf("SRV-%02d-SN", i)).Identify("name", name).
			Set("role", "server").Set("primary_ip", fmt.Sprintf("198.51.100.%d", 20+i)).Set("os_version", "6.1.0").Set("power_w", float64(300+10*i))
		d.Object(name+"/eth0", "interface").Identify("name", "eth0").In(name).Set("oper_status", "up").Set("mac", fmt.Sprintf("02:00:5e:00:53:%02x", i))
		d.Relations = append(d.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref(name), To: idRef("rack", e.ids["rack-2"]), Attributes: map[string]any{}})
		e.apply(d)
	}
}

// cable connects (or disconnects) two ports, as the cabling declaration does.
func (e *env) cable(unplug bool, a, b string) {
	e.t.Helper()
	x := sdk.NewBatch("declaration:cabling", sdk.ScopePartial)
	x.Confidence = sdk.Declared
	if unplug {
		x.Unrelate("cabled_to", idRef("interface", e.ids[a]), idRef("interface", e.ids[b]))
	} else {
		x.Relations = []*sdk.Relation{{Type: "cabled_to", From: idRef("interface", e.ids[a]), To: idRef("interface", e.ids[b]), Attributes: map[string]any{}}}
	}
	e.apply(x)
}

// ------------------------------------------------------------ MCP clients

// connect opens an in-memory MCP session as principal p.
func (e *env) connect(p api.Principal) *gosdk.ClientSession {
	e.t.Helper()
	ctx := context.Background()
	ct, st := gosdk.NewInMemoryTransports()
	ss, err := e.srv.ForPrincipal(p).Connect(ctx, st, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	cs, err := gosdk.NewClient(&gosdk.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cs.Close(); _ = ss.Close() })
	return cs
}

// as returns the principal bound to a test token.
func (e *env) as(token string) api.Principal { return e.auth.Tokens[token].Principal }

// answer is what a tool call returned.
type answer struct {
	IsError bool
	Text    string
	Data    map[string]any
}

// call runs a tool and fails the test on a protocol error.
func call(t testing.TB, cs *gosdk.ClientSession, tool string, args any) answer {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &gosdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", tool, err)
	}
	a := answer{IsError: res.IsError}
	for _, c := range res.Content {
		if tc, ok := c.(*gosdk.TextContent); ok {
			a.Text += tc.Text
		}
	}
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &a.Data); err != nil {
			t.Fatalf("%s: structured content is not an object: %v", tool, err)
		}
	}
	return a
}

// ok runs a tool and fails unless it succeeded.
func ok(t testing.TB, cs *gosdk.ClientSession, tool string, args any) answer {
	t.Helper()
	a := call(t, cs, tool, args)
	if a.IsError {
		t.Fatalf("%s failed: %s", tool, a.Text)
	}
	return a
}

// denied runs a tool and fails unless it was refused with this problem slug.
func denied(t testing.TB, cs *gosdk.ClientSession, tool string, args any, slug string) {
	t.Helper()
	a := call(t, cs, tool, args)
	if !a.IsError {
		t.Fatalf("%s succeeded, want %s: %s", tool, slug, a.Text)
	}
	if len(a.Text) < len(slug) || a.Text[:len(slug)] != slug {
		t.Fatalf("%s refused with %q, want a %s error", tool, a.Text, slug)
	}
}

// path digs into decoded JSON: path("summary", "total").
func dig(t testing.TB, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("no object at %v (got %T)", p, v)
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				t.Fatalf("no element %d (got %T)", k, v)
			}
			v = s[k]
		}
	}
	return v
}

func num(t testing.TB, v any, path ...any) int {
	t.Helper()
	f, ok := dig(t, v, path...).(float64)
	if !ok {
		t.Fatalf("%v is not a number: %v", path, dig(t, v, path...))
	}
	return int(f)
}

func str(t testing.TB, v any, path ...any) string {
	t.Helper()
	s, _ := dig(t, v, path...).(string)
	return s
}

// displays lists the display names of the items of an answer.
func displays(t testing.TB, v any, path ...any) []string {
	t.Helper()
	items, _ := dig(t, v, path...).([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		s, _ := m["display"].(string)
		out = append(out, s)
	}
	return out
}

func toolNames(t testing.TB, cs *gosdk.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tool := range res.Tools {
		out = append(out, tool.Name)
	}
	return out
}

// names lists each item's display up to the " · " its type's display template
// adds after the name: what a test means by "which drive".
func names(t testing.TB, v any, path ...any) []string {
	t.Helper()
	out := displays(t, v, path...)
	for i, d := range out {
		out[i], _, _ = strings.Cut(d, " · ")
	}
	return out
}
