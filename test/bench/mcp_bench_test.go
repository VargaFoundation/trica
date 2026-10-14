// Package bench measures the MCP tools on a synthetic inventory. These are
// benchmarks, not tests: they assert nothing and CI does not run them. Run
//
//	TRICA_TEST_DATABASE_URL=... go test -run '^$' -bench . -benchtime 200x ./test/bench/
//
// and read the figures as what one machine measured once.
package bench

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const devices = 2000

// session seeds `devices` devices (servers and switches, versions and power
// draws spread over a few values) and returns an MCP session on them.
func session(b *testing.B) *gosdk.ClientSession {
	b.Helper()
	st := testdb.New(b)
	reg := schema.Builtin()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	eng := &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Policy: reconcile.Policy{Propose: []string{"mcp:*"}}, Now: func() time.Time { return now }}
	ctx := context.Background()

	for start := 0; start < devices; start += 500 {
		batch := sdk.NewBatch("declaration:bench", sdk.ScopePartial)
		batch.Confidence = sdk.Declared
		batch.ObservedAt = now
		for i := start; i < start+500; i++ {
			name := fmt.Sprintf("dev-%04d", i)
			role := "server"
			if i%10 == 0 {
				role = "switch"
			}
			o := batch.Object(name, "device").Identify("serial", fmt.Sprintf("SN-%06d", i)).Identify("name", name).
				Set("role", role).Set("vendor", []string{"Acme", "Globex", "Initech"}[i%3]).
				Set("os_version", fmt.Sprintf("4.%d.%d", 20+i%12, i%5)).Set("power_w", float64(100+i%400))
			if i < 250 { // the documentation range holds 250 hosts
				o.Set("mgmt_ip", fmt.Sprintf("198.51.100.%d", 1+i))
			}
		}
		if _, err := eng.Apply(ctx, batch, "", ""); err != nil {
			b.Fatal(err)
		}
	}

	auth := &api.StaticAuthorizer{Tokens: map[string]api.Grant{
		"reader": {Principal: api.Principal{Subject: "bench", Kind: api.KindToken}, Actions: []string{api.ActionRead}},
	}}
	srv, err := mcp.New(mcp.Config{Engine: eng, Authorizer: auth, Auditor: &api.MemoryAuditor{}, Version: "bench",
		Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		b.Fatal(err)
	}
	ct, stp := gosdk.NewInMemoryTransports()
	ss, err := srv.ForPrincipal(auth.Tokens["reader"].Principal).Connect(ctx, stp, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = ss.Close() })
	cs, err := gosdk.NewClient(&gosdk.Implementation{Name: "bench", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cs.Close() })
	return cs
}

func BenchmarkTools(b *testing.B) {
	cs := session(b)
	for _, c := range []struct {
		name, tool string
		args       map[string]any
	}{
		{"search free text (a name)", "search", map[string]any{"query": "dev-1234"}},
		{"search free text (an address)", "search", map[string]any{"query": "198.51.100.8"}},
		{"search TQL (attribute, sorted)", "search", map[string]any{"query": "type:device role:switch vendor:acme sort:-power_w limit:20"}},
		{"search TQL (version comparison)", "search", map[string]any{"query": "type:device os_version<4.25 role:server limit:20"}},
		{"rank_filter top 10 power", "rank_filter", map[string]any{"type": "device", "attribute": "power_w", "n": 10, "query": "role:server"}},
		{"version_compliance, target", "version_compliance", map[string]any{"type": "device", "target": "4.30.2"}},
		{"filter_objects", "filter_objects", map[string]any{"type": "device", "filters": []map[string]any{{"attribute": "power_w", "op": "gt", "value": "450"}}}},
	} {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				res, err := cs.CallTool(context.Background(), &gosdk.CallToolParams{Name: c.tool, Arguments: c.args})
				if err != nil || res.IsError {
					b.Fatalf("%s: %v %v", c.tool, err, res)
				}
			}
		})
	}
}
