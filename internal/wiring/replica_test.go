package wiring_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// A commit made by another replica reaches this one's graph through the
// changes channel, long before mcp.graph_refresh (#57, ADR 0032).
func TestGraphSeesAnotherReplicasCommit(t *testing.T) {
	// No timer may mark the graph stale during the test: neither the refresh
	// nor the deducer, whose every run is a commit on this replica.
	r := newRig(t, "mcp: {disable_localhost_protection: true, graph_refresh: 1h}\ndeduce: {interval: 1h}")
	ctx := context.Background()
	if code, body := r.do("POST", "/api/v1/batches", r.token, topology()); code != 200 {
		t.Fatalf("submit: %d %s", code, body)
	}
	cs := r.mcpSession(r.readToken)
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
		t.Fatalf("the graph does not see the first batch: %s", got)
	}

	// The other replica: its own engine on the same database. Its OnCommit
	// does not reach this server.
	st, err := store.Open(ctx, r.url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	other := &reconcile.Engine{Store: st, Schema: schema.Builtin}
	b := sdk.NewBatch("api:replica-b", sdk.ScopePartial)
	b.ObservedAt = time.Now().UTC()
	b.Object("sw", "device").Identify("name", "sw-a1")
	b.Object("srv", "device").Identify("name", "srv-02").Set("role", "server")
	b.Relate("depends_on", "srv", "sw")
	if _, err := other.Apply(ctx, b, "", "commit on another replica"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(patience)
	for !strings.Contains(impact(), "srv-02") {
		if time.Now().After(deadline) {
			t.Fatal("the graph never saw the other replica's commit")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
