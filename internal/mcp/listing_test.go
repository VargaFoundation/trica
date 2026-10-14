package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// listing builds a server that never touches the database: listing tools and
// reading their schemas needs none. Calls would fail, so tests here do not call.
func listing(t *testing.T, graph mcp.GraphQuerier) *gosdk.ClientSession {
	t.Helper()
	reg := schema.Builtin()
	eng := &reconcile.Engine{Store: &store.Store{}, Schema: func() *schema.Registry { return reg }}
	auth := &api.StaticAuthorizer{Tokens: map[string]api.Grant{
		"r": {Principal: api.Principal{Subject: "bob", Kind: api.KindUser}, Actions: []string{api.ActionRead}},
		"w": {Principal: api.Principal{Subject: "alice", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose}},
	}}
	srv, err := mcp.New(mcp.Config{Engine: eng, Authorizer: auth, Auditor: &api.MemoryAuditor{}, Graph: graph,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return connectListing(t, srv, auth.Tokens["w"].Principal)
}

func connectListing(t *testing.T, srv *mcp.Server, p api.Principal) *gosdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := gosdk.NewInMemoryTransports()
	ss, err := srv.ForPrincipal(p).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := gosdk.NewClient(&gosdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = ss.Close() })
	return cs
}

func TestNewRefusesAnIncompleteConfig(t *testing.T) {
	reg := schema.Builtin()
	eng := &reconcile.Engine{Store: &store.Store{}, Schema: func() *schema.Registry { return reg }}
	auth := &api.StaticAuthorizer{}
	for name, cfg := range map[string]mcp.Config{
		"no engine":     {Authorizer: auth, Auditor: &api.MemoryAuditor{}},
		"no authorizer": {Engine: eng, Auditor: &api.MemoryAuditor{}},
		"no auditor":    {Engine: eng, Authorizer: auth},
		"no store":      {Engine: &reconcile.Engine{Schema: eng.Schema}, Authorizer: auth, Auditor: &api.MemoryAuditor{}},
	} {
		if _, err := mcp.New(cfg); err == nil {
			t.Errorf("%s: New accepted the configuration", name)
		}
	}
}

// Every tool has an input schema a client can read, and says whether it only
// reads. propose_change takes an sdk.Batch, whose endpoints refer to themselves
// through their parent: the schema must still be inferred.
func TestToolSchemas(t *testing.T) {
	cs := listing(t, &fakeGraph{})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 14 {
		t.Fatalf("%d tools, want 14", len(res.Tools))
	}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil || !strings.Contains(string(raw), `"type":"object"`) {
			t.Errorf("%s: input schema %s (%v)", tool.Name, raw, err)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.Annotations == nil {
			t.Errorf("%s has no annotations", tool.Name)
			continue
		}
		write := tool.Name == "propose_change"
		if tool.Annotations.ReadOnlyHint == write {
			t.Errorf("%s: readOnlyHint = %v", tool.Name, tool.Annotations.ReadOnlyHint)
		}
		if d := tool.Annotations.DestructiveHint; d != nil && *d {
			t.Errorf("%s is annotated destructive: nothing is deleted through MCP", tool.Name)
		}
	}
}

// Without a graph, impact and path are not offered: a tool that can only fail is not a tool.
func TestNoGraphNoGraphTools(t *testing.T) {
	cs := listing(t, nil)
	for _, n := range toolNames(t, cs) {
		if n == "impact_analysis" || n == "network_path" {
			t.Errorf("%s is offered without a graph", n)
		}
	}
}
