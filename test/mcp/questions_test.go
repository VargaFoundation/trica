// Package mcp_test runs the twelve questions of the product specification
// (questions.yaml) through an MCP client session against a real PostgreSQL:
// each question that lot 1 can answer must be answered by ONE tool call, and
// the questions it cannot answer must say why.
package mcp_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

//go:embed questions.yaml
var questionsYAML []byte

type expectation struct {
	Path     string   `yaml:"path"`
	Equals   any      `yaml:"equals"`
	Contains any      `yaml:"contains"`
	Gte      *float64 `yaml:"gte"`
	Present  *bool    `yaml:"present"`
}

type question struct {
	ID              int            `yaml:"id"`
	Spec            string         `yaml:"spec"`
	Tool            string         `yaml:"tool"`
	Answerable      bool           `yaml:"answerable"`
	Reason          string         `yaml:"reason"`
	Caveat          string         `yaml:"caveat"`
	CreatesProposal bool           `yaml:"creates_proposal"`
	Arguments       map[string]any `yaml:"arguments"`
	Expect          []expectation  `yaml:"expect"`
}

type questionFile struct {
	Now             time.Time  `yaml:"now"`
	AnswerableCount int        `yaml:"answerable_count"`
	Questions       []question `yaml:"questions"`
}

func loadQuestions(t *testing.T) *questionFile {
	t.Helper()
	var f questionFile
	if err := yaml.Unmarshal(questionsYAML, &f); err != nil {
		t.Fatalf("questions.yaml: %v", err)
	}
	return &f
}

// ----------------------------------------------------------------- the seed

type world struct {
	t   *testing.T
	eng *reconcile.Engine
	st  *store.Store
	now time.Time
	ids map[string]string
	// at is the time of the next batch; the engine's clock follows it.
	at    time.Time
	clock *time.Time
}

func (w *world) apply(b *sdk.Batch) {
	w.t.Helper()
	b.ObservedAt = w.at
	*w.clock = w.at
	res, err := w.eng.Apply(context.Background(), b, "", "")
	if err != nil {
		w.t.Fatalf("seed %s: %v", b.Source, err)
	}
	for ref, id := range res.Objects {
		w.ids[ref] = id
	}
}

func idEnd(typ, id string) sdk.Endpoint {
	return sdk.Endpoint{Type: typ, Identity: map[string]string{sdk.IDKey: id}}
}

func (w *world) cable(unplug bool, a, b string) {
	w.t.Helper()
	x := sdk.NewBatch("declaration:cabling", sdk.ScopePartial)
	x.Confidence = sdk.Declared
	if unplug {
		x.Unrelate("cabled_to", idEnd("interface", w.ids[a]), idEnd("interface", w.ids[b]))
	} else {
		x.Relations = []*sdk.Relation{{Type: "cabled_to", From: idEnd("interface", w.ids[a]), To: idEnd("interface", w.ids[b]), Attributes: map[string]any{}}}
	}
	w.apply(x)
}

func (w *world) setOS(name, serial, version string) {
	w.t.Helper()
	b := sdk.NewBatch("arista:"+name, sdk.ScopePartial)
	b.Object(name, "device").Identify("serial", serial).Set("os_version", version)
	w.apply(b)
}

// seed invents a small room: a site with two racks, five switches, twelve
// servers, three applications, a version policy, a cable that moved, and a
// switch whose firmware was upgraded yesterday although another version was
// declared for it.
func seed(t *testing.T, eng *reconcile.Engine, st *store.Store, clock *time.Time, now time.Time) *world {
	t.Helper()
	w := &world{t: t, eng: eng, st: st, now: now, ids: map[string]string{}, at: now.AddDate(0, 0, -60), clock: clock}

	b := sdk.NewBatch("declaration:room", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("paris", "site").Identify("code", "PAR1").Set("name", "paris-1")
	b.Object("rack-1", "rack").Identify("name", "rack-1").In("paris").Set("u_height", 42)
	b.Object("rack-2", "rack").Identify("name", "rack-2").In("paris").Set("u_height", 42)
	b.Object("pol", "version_policy").Identify("name", "switch-os").Set("software", "network-os").Set("attribute", "os_version").
		Set("selector", "role:switch").Set("reference_version", "4.30.2").Set("max_minor_lag", 1)
	for _, app := range []string{"orders-api", "orders-db", "billing"} {
		b.Object(app, "application").Identify("name", app).Set("criticality", "high")
	}
	w.apply(b)

	switches := []struct{ name, serial, ip, os, airflow, rack string }{
		{"sw-core-01", "SN-CORE-01", "192.0.2.1", "4.30.2", "front_to_rear", "rack-1"},
		{"sw-agg-01", "SN-AGG-01", "192.0.2.2", "4.28.0", "front_to_rear", "rack-1"},
		{"sw-agg-02", "SN-AGG-02", "192.0.2.3", "4.20.7M", "front_to_rear", "rack-1"},
		{"sw-edge-01", "SN-EDGE-01", "192.0.2.12", "4.19.9", "rear_to_front", "rack-2"},
		{"sw-edge-02", "SN-EDGE-02", "192.0.2.13", "", "rear_to_front", "rack-2"},
	}
	for i, s := range switches {
		c := sdk.NewBatch("arista:"+s.name, sdk.ScopeFull)
		o := c.Object(s.name, "device").Identify("serial", s.serial).Identify("name", s.name).
			Set("role", "switch").Set("vendor", "Acme").Set("mgmt_ip", s.ip).Set("airflow", s.airflow).Set("power_w", float64(150+i))
		if s.os != "" {
			o.Set("os_version", s.os)
		}
		for _, n := range []string{"Ethernet1", "Ethernet2"} {
			c.Object(s.name+"/"+n, "interface").Identify("name", n).In(s.name).Set("oper_status", "up")
		}
		c.Relations = append(c.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref(s.name), To: idEnd("rack", w.ids[s.rack]), Attributes: map[string]any{}})
		w.apply(c)
	}
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("srv-%02d", i)
		d := sdk.NewBatch("agent:"+name, sdk.ScopeFull)
		d.Object(name, "device").Identify("serial", fmt.Sprintf("SN-SRV-%02d", i)).Identify("name", name).
			Set("role", "server").Set("airflow", "front_to_rear").Set("primary_ip", fmt.Sprintf("198.51.100.%d", 20+i)).
			Set("os_version", "6.1.0").Set("power_w", float64(300+10*i))
		d.Object(name+"/eth0", "interface").Identify("name", "eth0").In(name).Set("oper_status", "up").Set("mac", fmt.Sprintf("02:00:5e:00:53:%02x", i))
		d.Relations = append(d.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref(name), To: idEnd("rack", w.ids["rack-2"]), Attributes: map[string]any{}})
		w.apply(d)
	}

	// The cable of sw-agg-01/Ethernet2 moved: srv-01 in September, srv-02 from October.
	at := func(month time.Month, day int) time.Time { return time.Date(2026, month, day, 9, 0, 0, 0, time.UTC) }
	w.at = at(9, 2)
	w.cable(false, "sw-agg-01/Ethernet2", "srv-01/eth0")
	w.at = at(9, 20)
	w.cable(true, "sw-agg-01/Ethernet2", "srv-01/eth0")
	w.at = at(10, 5)
	w.cable(false, "sw-agg-01/Ethernet2", "srv-02/eth0")

	// Yesterday: 4.30.2 was declared for sw-agg-01 (20 h ago), then the switch
	// was upgraded, to 4.29.9 (12 h ago): a change nobody planned as such.
	w.at = now.Add(-20 * time.Hour)
	plan := sdk.NewBatch("declaration:planned", sdk.ScopePartial)
	plan.Confidence = sdk.Declared
	plan.Object("sw-agg-01", "device").Identify("serial", "SN-AGG-01").Set("os_version", "4.30.2")
	w.apply(plan)
	w.at = now.Add(-12 * time.Hour)
	w.setOS("sw-agg-01", "SN-AGG-01", "4.29.9")
	w.at = now

	// Drive wear, as the servers' agents reported it over the last three months
	// (days before now, wear in %). The trends are invented: ssd-03a and ssd-05a
	// climb by 0.22 %/day, ssd-07a barely moves, ssd-09a is past 80 %, ssd-11a
	// was seen once, and the hard disk hdd-02a is on the fast trend too.
	w.disk("srv-03", "ssd-03a", "nvme", [2]float64{90, 55}, [2]float64{45, 65}, [2]float64{0, 75})
	w.disk("srv-05", "ssd-05a", "ssd", [2]float64{90, 30}, [2]float64{45, 40}, [2]float64{0, 50})
	w.disk("srv-07", "ssd-07a", "ssd", [2]float64{90, 20}, [2]float64{0, 22})
	w.disk("srv-09", "ssd-09a", "ssd", [2]float64{60, 85}, [2]float64{0, 90})
	w.disk("srv-11", "ssd-11a", "ssd", [2]float64{0, 40})
	w.disk("srv-02", "hdd-02a", "hdd", [2]float64{90, 50}, [2]float64{45, 60}, [2]float64{0, 70})
	return w
}

// disk records the wear readings of one drive of a server, oldest first.
func (w *world) disk(host, serial, media string, readings ...[2]float64) {
	w.t.Helper()
	for _, r := range readings {
		w.at = w.now.Add(-time.Duration(r[0] * float64(24*time.Hour)))
		b := sdk.NewBatch("redfish:"+serial, sdk.ScopePartial)
		b.Object("host", "device").Identify(sdk.IDKey, w.ids[host])
		b.Object(serial, "disk").Identify("serial", serial).In("host").Set("name", serial).Set("media", media).Set("wear_pct", r[1])
		w.apply(b)
	}
	w.at = w.now
}

// canned is the dependency graph's stand-in: the graph is a separate PR, its
// answers for the two questions that need it are fixed here.
type canned struct {
	impact *mcp.ImpactResult
	path   []mcp.Hop
}

func (c *canned) Impact(context.Context, []string, []string) (*mcp.ImpactResult, error) {
	return c.impact, nil
}
func (c *canned) Path(context.Context, string, string, []string) ([]mcp.Hop, error) {
	return c.path, nil
}

func (w *world) graph() *canned {
	aff := func(ref, typ string, state string, depth int) mcp.Affected {
		return mcp.Affected{ID: w.ids[ref], Type: typ, Display: ref, State: state, Rule: "cabled_to", Cause: w.ids["sw-core-01"], Depth: depth}
	}
	node := func(ref, typ string) *mcp.GraphNode { return &mcp.GraphNode{ID: w.ids[ref], Type: typ, Display: ref} }
	via := func(typ, from, to string) *mcp.GraphEdge {
		return &mcp.GraphEdge{Type: typ, From: w.ids[from], To: w.ids[to]}
	}
	return &canned{
		impact: &mcp.ImpactResult{
			Failed: []string{w.ids["sw-core-01"]},
			Affected: []mcp.Affected{
				aff("srv-01", "device", mcp.StateDown, 1),
				aff("orders-api", "application", mcp.StateDown, 2),
				aff("orders-db", "application", mcp.StateDown, 3),
				aff("billing", "application", mcp.StateDegraded, 2),
			},
			Down:     map[string]int{"device": 1, "application": 2},
			Degraded: map[string]int{"application": 1},
		},
		path: []mcp.Hop{
			{Node: node("orders-api", "application")},
			{Node: node("srv-01", "device"), Depth: 1, Via: via("runs_on", "orders-api", "srv-01")},
			{Node: node("sw-agg-01", "device"), Depth: 2, Via: via("connected_to", "srv-01", "sw-agg-01")},
			{Node: node("srv-02", "device"), Depth: 3, Via: via("connected_to", "sw-agg-01", "srv-02")},
			{Node: node("orders-db", "application"), Depth: 4, Via: via("runs_on", "orders-db", "srv-02")},
		},
	}
}

// ------------------------------------------------------------ the assertions

// lookup reads a dot path in decoded JSON. A `*` segment fans out over the
// elements of a list, and the result is then a list.
func lookup(v any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var walk func(v any, parts []string) (any, bool)
	walk = func(v any, parts []string) (any, bool) {
		if len(parts) == 0 {
			return v, v != nil
		}
		switch p := parts[0]; x := v.(type) {
		case map[string]any:
			child, ok := x[p]
			if !ok {
				return nil, false
			}
			return walk(child, parts[1:])
		case []any:
			if p == "*" {
				out := make([]any, 0, len(x))
				for _, el := range x {
					if r, ok := walk(el, parts[1:]); ok {
						out = append(out, r)
					}
				}
				return out, true
			}
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			return walk(x[i], parts[1:])
		}
		return nil, false
	}
	return walk(v, parts)
}

// normalize turns what yaml decoded into what encoding/json would have.
func normalize(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// substitute replaces ${id:ref} in every string of the arguments.
func (w *world) substitute(t *testing.T, v any) any {
	t.Helper()
	switch x := v.(type) {
	case string:
		for ref, id := range w.ids {
			x = strings.ReplaceAll(x, "${id:"+ref+"}", id)
		}
		if strings.Contains(x, "${id:") {
			t.Fatalf("unknown seeded object in %q", x)
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = w.substitute(t, el)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, el := range x {
			out[k] = w.substitute(t, el)
		}
		return out
	}
	return v
}

func check(t *testing.T, answer any, e expectation) {
	t.Helper()
	got, found := lookup(answer, e.Path)
	switch {
	case e.Present != nil:
		if found != *e.Present {
			t.Errorf("%s: present = %v, want %v", e.Path, found, *e.Present)
		}
	case !found:
		t.Errorf("%s: not in the answer", e.Path)
	case e.Equals != nil:
		if want := normalize(t, e.Equals); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", e.Path, got, want)
		}
	case e.Contains != nil:
		want := normalize(t, e.Contains)
		switch g := got.(type) {
		case []any:
			has := false
			for _, el := range g {
				has = has || reflect.DeepEqual(el, want)
			}
			if !has {
				t.Errorf("%s = %v, want it to hold %v", e.Path, g, want)
			}
		case string:
			if s, _ := want.(string); !strings.Contains(g, s) {
				t.Errorf("%s = %q, want it to contain %q", e.Path, g, s)
			}
		default:
			t.Errorf("%s = %v: contains applies to a string or a list", e.Path, got)
		}
	case e.Gte != nil:
		if n, ok := got.(float64); !ok || n < *e.Gte {
			t.Errorf("%s = %v, want >= %v", e.Path, got, *e.Gte)
		}
	default:
		t.Errorf("%s: the expectation checks nothing", e.Path)
	}
}

func inventory(t *testing.T, st *store.Store) string {
	t.Helper()
	var out []string
	for _, table := range []string{"objects", "claims", "attr_history", "relations", "rel_claims"} {
		var n int
		if err := st.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s=%d", table, n))
	}
	return strings.Join(out, " ")
}

func TestSpecQuestions(t *testing.T) {
	f := loadQuestions(t)
	if len(f.Questions) != 12 {
		t.Fatalf("%d questions in questions.yaml, the specification has 12", len(f.Questions))
	}
	st := testdb.New(t)
	now := f.Now
	reg := schema.Builtin()
	clock := now
	// The engine's clock follows the seed, then stands at now.
	eng := &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Policy: reconcile.Policy{Propose: []string{"mcp:*"}}, Now: func() time.Time { return clock }}
	world := seed(t, eng, st, &clock, now)
	clock = now

	auth := &api.StaticAuthorizer{Tokens: map[string]api.Grant{
		"assistant": {Principal: api.Principal{Subject: "assistant", Kind: api.KindToken, Source: "mcp:assistant"}, Actions: []string{api.ActionRead, api.ActionPropose}},
	}}
	srv, err := mcp.New(mcp.Config{Engine: eng, Authorizer: auth, Auditor: &api.MemoryAuditor{}, Graph: world.graph(), Version: "test",
		Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, stp := gosdk.NewInMemoryTransports()
	ss, err := srv.ForPrincipal(auth.Tokens["assistant"].Principal).Connect(ctx, stp, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := gosdk.NewClient(&gosdk.Implementation{Name: "questions", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Every tool the questions name exists.
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	offered := map[string]bool{}
	for _, tl := range tools.Tools {
		offered[tl.Name] = true
	}

	answered := 0
	for _, q := range f.Questions {
		t.Run(fmt.Sprintf("Q%02d", q.ID), func(t *testing.T) {
			if !offered[q.Tool] {
				t.Fatalf("the tool %q is not offered", q.Tool)
			}
			if !q.Answerable {
				if strings.TrimSpace(q.Reason) == "" {
					t.Fatal("a question that is not answered must say why")
				}
				if len(q.Expect) != 0 {
					t.Fatal("a question that is not answered has no expectations")
				}
				t.Logf("not answerable in lot 1: %s", q.Reason)
				return
			}
			if len(q.Expect) == 0 {
				t.Fatal("an answerable question needs expectations")
			}
			before := inventory(t, st)
			res, err := cs.CallTool(ctx, &gosdk.CallToolParams{Name: q.Tool, Arguments: world.substitute(t, q.Arguments)})
			if err != nil {
				t.Fatalf("%s: %v", q.Tool, err)
			}
			if res.IsError {
				t.Fatalf("%s: %v", q.Tool, res.Content)
			}
			answer := normalize(t, res.StructuredContent)
			if t.Failed() {
				return
			}
			for _, e := range q.Expect {
				e.Equals, e.Contains = world.substitute(t, e.Equals), world.substitute(t, e.Contains)
				check(t, answer, e)
			}
			if after := inventory(t, st); after != before {
				t.Errorf("a question changed the inventory:\n before %s\n after  %s", before, after)
			}
			if q.CreatesProposal {
				pending, err := eng.ListProposals(ctx, "pending", 10)
				if err != nil || len(pending) != 1 {
					t.Errorf("pending proposals: %v, %v", pending, err)
				}
			}
			if !t.Failed() {
				answered++
			}
		})
	}
	t.Logf("%d of %d questions answered in one call (specification target: 10)", answered, len(f.Questions))
	if answered != f.AnswerableCount {
		t.Errorf("%d questions answered, questions.yaml claims %d", answered, f.AnswerableCount)
	}
}
