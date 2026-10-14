package mcp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
)

// Every call is authorized per object type and per site, as the REST API does.
func TestReadsAreScopedToWhatTheCallerMayRead(t *testing.T) {
	e := newEnv(t, proposeMCP, false)

	t.Run("a grant limited to devices sees no rack, no port", func(t *testing.T) {
		cs := e.connect(e.as(tokDevices))
		// A type the grant does not cover is refused (or empty): never listed.
		for _, tool := range []struct {
			name string
			args m
		}{{"search", m{"query": "type:rack"}}, {"list_objects", m{"type": "rack"}}} {
			if a := call(t, cs, tool.name, tool.args); !a.IsError && num(t, a.Data, "summary", "total") != 0 {
				t.Errorf("%s shows racks to a grant limited to devices: %v", tool.name, a.Data)
			}
		}
		denied(t, cs, "list_objects", m{"type": "rack"}, "forbidden")
		if n := num(t, ok(t, cs, "search", m{"query": "type:device role:switch"}).Data, "summary", "total"); n != 5 {
			t.Errorf("switches found: %d", n)
		}
		denied(t, cs, "get_object", m{"object": e.ids["rack-1"]}, "forbidden")
		denied(t, cs, "object_summary", m{"object": e.ids["rack-1"]}, "forbidden")
		denied(t, cs, "object_history", m{"object": e.ids["sw-a1/Ethernet1"]}, "forbidden")
		denied(t, cs, "get_object", m{"object": "02:00:5e:00:53:03"}, "not-found")
		// Names of what the caller cannot read resolve to nothing (not to "forbidden", which would say it exists).
		denied(t, cs, "get_object", m{"object": "rack-1"}, "not-found")
		denied(t, cs, "get_object", m{"object": "srv-01/eth0"}, "not-found")
		denied(t, cs, "object_summary", m{"object": "paris-1"}, "not-found")
		// What a device is related to is shown only when the caller may read the other end.
		s := ok(t, cs, "object_summary", m{"object": "sw-a1"})
		for _, r := range dig(t, s.Data, "relations").([]any) {
			if str(t, r, "other", "type") != "device" {
				t.Errorf("a relation to an unreadable object is shown: %v", r)
			}
		}
	})
	t.Run("a grant limited to a site reads objects of that site by id only", func(t *testing.T) {
		cs := e.connect(e.as(tokSiteParis))
		// Listing needs a type readable on every site, which this grant is not:
		// the tools that enumerate refuse rather than filter after the fact.
		denied(t, cs, "search", m{"query": "type:device role:switch"}, "forbidden")
		denied(t, cs, "list_objects", m{"type": "device"}, "forbidden")
		denied(t, cs, "rank_filter", m{"attribute": "power_w", "type": "device"}, "forbidden")
		denied(t, cs, "forecast_threshold", m{"attribute": "power_w", "threshold": 500, "type": "device"}, "forbidden")
		denied(t, cs, "version_compliance", m{"target": "4.30.2"}, "forbidden")
		// By id: the site's own objects are read, the other site's do not exist.
		if got := str(t, ok(t, cs, "get_object", m{"object": e.ids["sw-a1"]}).Data, "display"); got != "sw-a1" {
			t.Fatalf("read by id: %q", got)
		}
		ok(t, cs, "object_summary", m{"object": e.ids["sw-a1"]})
		denied(t, cs, "get_object", m{"object": e.ids["sw-b1"]}, "forbidden")
		denied(t, cs, "object_summary", m{"object": e.ids["sw-b1"]}, "forbidden")
		denied(t, cs, "object_history", m{"object": e.ids["sw-b1/Ethernet1"]}, "forbidden")
		// The change feed too: a change on lyon-1 is not reported.
		e.setPower("sw-b1", "JPE-SW-B1", 999)
		if a := call(t, cs, "changes_since", m{"since": "2026-02-01", "only": "changes", "limit": 200}); !a.IsError {
			for _, ch := range dig(t, a.Data, "changes").([]any) {
				if strings.Contains(fmt.Sprint(ch), e.ids["sw-b1"]) {
					t.Fatalf("a change of another site is reported: %v", ch)
				}
			}
		}
	})
	t.Run("a credential with no right gets nothing, and every refusal is audited", func(t *testing.T) {
		cs := e.connect(e.as(tokNone))
		before := len(e.aud.Entries())
		for _, tool := range []string{"search", "get_object", "describe_schema", "changes_since", "rank_filter"} {
			denied(t, cs, tool, validArgs[tool], "forbidden")
		}
		entries := e.aud.Entries()[before:]
		if len(entries) != 5 {
			t.Fatalf("%d audit entries", len(entries))
		}
		for _, en := range entries {
			if en.Outcome != api.OutcomeDenied || en.Actor != "ivan" {
				t.Errorf("entry %+v", en)
			}
		}
	})
}

// Asking for a secret as a returned field gives nothing (the refusals of the
// other tools are in TestSecretsAreRefusedBeforeTheStore).
func TestSecretsAreNeverReturned(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokAdmin))
	for _, attr := range []string{"snmp_community", "password", "api_token", "private_key", "psk"} {
		t.Run(attr, func(t *testing.T) {
			a := ok(t, cs, "search", m{"query": "sw-a1", "fields": []string{attr}})
			if attrs, _ := dig(t, a.Data, "items", 0, "attributes").(map[string]any); attrs[attr] != nil {
				t.Errorf("a secret attribute was returned: %v", attrs)
			}
		})
	}
}

func TestNamesAreResolvedOrRefused(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))

	t.Run("a name, an address, a serial, a port and an id designate the same object", func(t *testing.T) {
		for _, ref := range []string{"sw-a1", "192.0.2.10", "JPE-SW-A1", e.ids["sw-a1"]} {
			if got := str(t, ok(t, cs, "get_object", m{"object": ref}).Data, "id"); got != e.ids["sw-a1"] {
				t.Errorf("%q resolved to %s", ref, got)
			}
		}
		if got := str(t, ok(t, cs, "get_object", m{"object": "sw-a1/Ethernet2"}).Data, "id"); got != e.ids["sw-a1/Ethernet2"] {
			t.Errorf("port resolved to %s", got)
		}
	})
	t.Run("an ambiguous name is refused with the candidates, never guessed", func(t *testing.T) {
		a := call(t, cs, "get_object", m{"object": "eth0"})
		if !a.IsError || !strings.HasPrefix(a.Text, "invalid-request") || !strings.Contains(a.Text, "ambiguous") || !strings.Contains(a.Text, "parent/name") {
			t.Fatalf("got %q", a.Text)
		}
	})
	t.Run("an unknown name is not found", func(t *testing.T) {
		denied(t, cs, "get_object", m{"object": "no-such-object"}, "not-found")
		denied(t, cs, "get_object", m{"object": "sw-a1/Ethernet99"}, "not-found")
		denied(t, cs, "get_object", m{"object": ""}, "invalid-request")
	})
}

func TestEveryCallIsAudited(t *testing.T) {
	e := newEnv(t, proposeMCP, true)
	cs := e.connect(e.as(tokMCP))
	before := len(e.aud.Entries())
	ok(t, cs, "search", m{"query": "sw-a1"})
	ok(t, cs, "get_object", m{"object": "sw-a1"})
	denied(t, cs, "get_object", m{"object": "nothing"}, "not-found")
	ok(t, cs, "describe_schema", m{})
	a := ok(t, cs, "propose_change", e.newServer("planned server"))

	entries := e.aud.Entries()[before:]
	if len(entries) != 5 {
		t.Fatalf("%d audit entries for 5 calls: %+v", len(entries), entries)
	}
	want := []struct{ action, outcome string }{
		{"mcp.tool.search", api.OutcomeOK}, {"mcp.tool.get_object", api.OutcomeOK}, {"mcp.tool.get_object", api.OutcomeError},
		{"mcp.tool.describe_schema", api.OutcomeOK}, {"mcp.tool.propose_change", api.OutcomeOK},
	}
	for i, w := range want {
		if entries[i].Action != w.action || entries[i].Outcome != w.outcome || entries[i].Actor != "mcp-assistant" || entries[i].Kind != api.KindToken {
			t.Errorf("entry %d: %+v, want %v", i, entries[i], w)
		}
	}
	if q, _ := entries[0].Details["args"].(string); !strings.Contains(q, "sw-a1") {
		t.Errorf("the arguments of the search were not kept: %v", entries[0].Details)
	}
	// A proposal is audited by its id and its size: what it contains is in the proposal, not in the audit log.
	p := entries[4]
	if p.Target != fmt.Sprintf("proposal:%d", num(t, a.Data, "proposal_id")) || p.Details["source"] != "mcp:assistant" {
		t.Errorf("proposal entry %+v", p)
	}
	if raw := fmt.Sprint(p.Details); strings.Contains(raw, "SRV-13-SN") || strings.Contains(raw, "Acme") || !strings.Contains(raw, "objects:1") {
		t.Errorf("a proposal's content is in the audit log: %s", raw)
	}
}
