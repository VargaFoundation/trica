package wiring_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/wiring"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The topology map reads the shared graph, which the MCP server used to be
// the only one to keep current. With MCP disabled a commit must still reach
// the map (ADR 0051): no timer may mark the graph stale during the test, so
// only the commit hook can.
func TestMapSeesACommitWithMCPDisabled(t *testing.T) {
	defer wiring.SetMapMaxStale(100 * time.Millisecond)()
	r := newRig(t, "mcp: {disabled: true, graph_refresh: 1h}\ndeduce: {interval: 1h}")
	if code, body := r.do("POST", "/api/v1/batches", r.token, topology()); code != 200 {
		t.Fatalf("submit: %d %s", code, body)
	}
	unplaced := func() string {
		code, body := r.do("GET", "/api/v1/topology/map?level=unplaced", r.readToken, "")
		if code != 200 {
			t.Fatalf("map: %d %s", code, body)
		}
		var m struct {
			Nodes []struct{ Display string }
		}
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, n := range m.Nodes {
			names = append(names, n.Display)
		}
		return strings.Join(names, ",")
	}
	if got := unplaced(); !strings.Contains(got, "srv-01") {
		t.Fatalf("the map does not see the first batch: %q", got)
	}
	b := sdk.NewBatch("", sdk.ScopePartial)
	b.ObservedAt = time.Now().UTC()
	b.Object("nas", "device").Identify("name", "nas-9").Set("role", "storage")
	raw, _ := json.Marshal(b)
	if code, body := r.do("POST", "/api/v1/batches", r.token, string(raw)); code != 200 {
		t.Fatalf("submit: %d %s", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(unplaced(), "nas-9") {
		if time.Now().After(deadline) {
			t.Fatalf("the map never saw the commit: %q", unplaced())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
