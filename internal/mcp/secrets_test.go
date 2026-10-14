package mcp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const (
	secretCommunity = "zz-community-4f9a1c"
	secretToken     = "zz-token-77b3e0"
	secretOther     = "zz-declared-community-0d52"
	secretProposed  = "zz-proposed-community-9e61"
)

const secretOverlay = `
types:
  device:
    attributes:
      snmp_community: {kind: string, label: {en: SNMP community}}
      site_note: {kind: string, search: true}
  vpn_gateway:
    label: {en: VPN gateway}
    parents: [site]
    display: name
    attributes:
      name: {kind: string, identity: strong}
      api_token: {kind: string}
      tunnel_count: {kind: int}
`

// A value the inventory holds under a secret-looking name (the engine accepts
// it from collectors) must come out of no tool, under any designation, any
// field list, any date and for any caller, whatever source it came from.
func TestSecretValuesNeverLeaveThroughAnyTool(t *testing.T) {
	ov, err := schema.Parse([]byte(secretOverlay), true)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := schema.Builtin().Merge(ov)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, reconcile.Policy{Propose: []string{"mcp:*"}}, true, reg)

	b := sdk.NewBatch("agent:vpn-gw", sdk.ScopePartial)
	b.Object("paris", "site").Identify("code", "PAR1")
	b.Object("gw", "vpn_gateway").Identify("name", "gw-1").In("paris").Set("api_token", secretToken).Set("tunnel_count", 4)
	e.apply(b)

	e.clk.advance(time.Minute)
	c := sdk.NewBatch("arista:sw-a1", sdk.ScopePartial)
	c.Object("sw", "device").Identify(sdk.IDKey, e.ids["sw-a1"]).Set("snmp_community", secretCommunity).Set("site_note", "second floor")
	e.apply(c)

	// Someone declares another value: a discrepancy on the secret key.
	e.clk.advance(time.Minute)
	d := sdk.NewBatch("user:alice", sdk.ScopePartial)
	d.Confidence = sdk.Declared
	d.Object("sw", "device").Identify(sdk.IDKey, e.ids["sw-a1"]).Set("snmp_community", secretOther)
	e.apply(d)

	// A proposal whose batch carries a secret (MCP cannot create one; other engine callers can).
	p := sdk.NewBatch("mcp:assistant", sdk.ScopePartial)
	p.ObservedAt = e.clk.now()
	p.Object("sw", "device").Identify(sdk.IDKey, e.ids["sw-a1"]).Set("snmp_community", secretProposed).Set("description", "proposed")
	if res, err := e.eng.Submit(t.Context(), p, "mcp-assistant", "test"); err != nil || res.ProposalID == 0 {
		t.Fatalf("fixture: no proposal queued: %v %+v", err, res)
	}
	// The state is really in the database: the test would pass vacuously otherwise.
	var n int
	if err := e.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM claims WHERE value::text LIKE '%' || $1 || '%'`, secretCommunity).Scan(&n); err != nil || n == 0 {
		t.Fatalf("fixture: the community is not stored (%d, %v)", n, err)
	}

	// The gateway's tunnel count grew over two days: a trend to extrapolate on an
	// object that also holds a secret.
	e.clk.advance(48 * time.Hour)
	grow := sdk.NewBatch("agent:vpn-gw", sdk.ScopePartial)
	grow.Object("paris", "site").Identify("code", "PAR1")
	grow.Object("gw", "vpn_gateway").Identify("name", "gw-1").In("paris").Set("tunnel_count", 6)
	e.apply(grow)

	sw, gw := e.ids["sw-a1"], e.ids["gw"]
	calls := []struct {
		tool string
		args m
	}{
		{"search", m{"query": "sw-a1"}},
		{"search", m{"query": "gw-1"}},
		{"search", m{"query": "second floor"}},
		{"search", m{"query": "type:device role:switch", "fields": []string{"snmp_community", "site_note"}}},
		{"list_objects", m{"type": "device", "fields": []string{"snmp_community", "api_token"}}},
		{"list_objects", m{"type": "vpn_gateway", "fields": []string{"api_token"}}},
		{"list_objects", m{"type": "vpn_gateway"}},
		{"filter_objects", m{"type": "device", "fields": []string{"snmp_community"}, "filters": []m{{"attribute": "role", "op": "eq", "value": "switch"}}}},
		{"rank_filter", m{"type": "device", "attribute": "power_w", "fields": []string{"snmp_community"}}},
		{"get_object", m{"object": sw}},
		{"get_object", m{"object": gw}},
		{"get_object", m{"object": sw, "at": "2026-03-01T08:00:30Z"}},
		{"get_object", m{"object": sw, "at": "2026-03-01T08:01:30Z"}},
		{"get_object", m{"object": gw, "at": "2026-03-01T08:00:30Z"}},
		{"object_summary", m{"object": sw}},
		{"object_summary", m{"object": gw}},
		{"object_history", m{"object": sw}},
		{"object_history", m{"object": gw}},
		{"object_history", m{"object": sw, "kind": "values", "attribute": "os_version"}},
		{"changes_since", m{"since": "2026-01-01", "limit": 1000}},
		{"changes_since", m{"since": "2026-01-01", "only": "discrepancies", "limit": 1000}},
		{"version_compliance", m{"target": "4.30.2"}},
		{"forecast_threshold", m{"type": "vpn_gateway", "attribute": "tunnel_count", "threshold": 8, "fields": []string{"api_token", "tunnel_count"}}},
		{"forecast_threshold", m{"type": "device", "attribute": "power_w", "threshold": 1000, "fields": []string{"snmp_community"}}},
		{"describe_schema", m{}},
		{"describe_schema", m{"type": "device"}},
		{"describe_schema", m{"type": "vpn_gateway"}},
	}
	secrets := []string{secretCommunity, secretToken, secretOther, secretProposed}
	for _, tok := range []string{tokAdmin, tokReader, tokMCP} {
		cs := e.connect(e.as(tok))
		for _, c := range calls {
			a := call(t, cs, c.tool, c.args)
			// A refusal is as good as an answer without the secret; a leak is not.
			for _, s := range secrets {
				if strings.Contains(a.Text, s) {
					t.Errorf("%s %v as %s returned a secret value: %s", c.tool, c.args, tok, a.Text)
				}
			}
		}
	}
	// And the calls that should succeed did: the checks above are not vacuous.
	cs := e.connect(e.as(tokReader))
	if got := num(t, ok(t, cs, "object_summary", m{"object": sw}).Data, "summary", "open_discrepancies"); got == 0 {
		t.Fatal("fixture: the discrepancy on the secret key is not there, the test would be vacuous")
	}
	f := ok(t, cs, "forecast_threshold", m{"type": "vpn_gateway", "attribute": "tunnel_count", "threshold": 8, "fields": []string{"api_token", "tunnel_count"}})
	if got := displays(t, f.Data, "items"); len(got) != 1 || got[0] != "gw-1" {
		t.Fatalf("the gateway's trend is not forecast (%v): the test would be vacuous", got)
	}
	if attrs, _ := dig(t, f.Data, "items", 0, "attributes").(map[string]any); attrs["tunnel_count"] == nil || attrs["api_token"] != nil {
		t.Fatalf("forecast fields: %v", attrs)
	}
	if a := ok(t, cs, "get_object", m{"object": gw}); !strings.Contains(a.Text, "gw-1") {
		t.Fatalf("the gateway is not readable at all: %s", a.Text)
	}
}
