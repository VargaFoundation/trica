package mcp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
)

var proposeMCP = reconcile.Policy{Propose: []string{"mcp:*"}}

func TestToolList(t *testing.T) {
	e := newEnv(t, proposeMCP, true)
	readers := []string{"search", "list_objects", "filter_objects", "rank_filter", "forecast_threshold", "version_compliance", "get_object",
		"object_summary", "object_history", "changes_since", "describe_schema", "impact_analysis", "network_path"}

	t.Run("a reader sees the read tools and no write tool", func(t *testing.T) {
		got := toolNames(t, e.connect(e.as(tokReader)))
		for _, n := range readers {
			if !slices.Contains(got, n) {
				t.Errorf("tool %s is missing: %v", n, got)
			}
		}
		if slices.Contains(got, "propose_change") {
			t.Errorf("a credential without propose sees propose_change: %v", got)
		}
	})
	t.Run("a proposer sees propose_change, and it is the only write tool", func(t *testing.T) {
		got := toolNames(t, e.connect(e.as(tokAdmin)))
		if want := len(readers) + 1; len(got) != want || !slices.Contains(got, "propose_change") {
			t.Fatalf("tools %v, want the %d read tools and propose_change", got, len(readers))
		}
		// ADR 0009: nothing approves, applies or deletes through MCP, whatever the credential holds.
		for _, n := range got {
			for _, bad := range []string{"approve", "reject", "apply", "delete", "remove", "accept", "write", "update", "submit"} {
				if strings.Contains(n, bad) {
					t.Errorf("tool %q looks like a write other than propose_change", n)
				}
			}
		}
	})
}
