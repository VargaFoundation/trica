package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// setPower records a new observed power draw of a switch, at the clock's time.
func (e *env) setPower(name, serial string, w float64) {
	e.t.Helper()
	b := sdk.NewBatch("arista:"+name, sdk.ScopePartial)
	b.Object(name, "device").Identify("serial", serial).Set("power_w", w)
	e.apply(b)
}

func TestGetObject(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))

	for ref, want := range map[string]string{
		"sw-a1":                          "sw-a1",
		"192.0.2.11":                     "sw-a2",
		"JPE-SW-B1":                      "sw-b1",
		e.ids["sw-b3"]:                   "sw-b3",
		"sw-a1/Ethernet2":                "Ethernet2",
		"srv-07/eth0":                    "eth0",
		strings.ToUpper(e.ids["rack-2"]): "rack-2",
	} {
		a := ok(t, cs, "get_object", m{"object": ref})
		if str(t, a.Data, "display") != want {
			t.Errorf("get_object(%q) = %q, want %q", ref, str(t, a.Data, "display"), want)
		}
	}
	a := ok(t, cs, "get_object", m{"object": "srv-07/eth0"})
	if str(t, a.Data, "attributes", "mac") != "02:00:5e:00:53:07" {
		t.Errorf("port attributes %v", dig(t, a.Data, "attributes"))
	}

	t.Run("the object as it was", func(t *testing.T) {
		e.clk.advance(48 * time.Hour)
		e.setPower("sw-a1", "JPE-SW-A1", 190)
		now := ok(t, cs, "get_object", m{"object": "sw-a1"})
		then := ok(t, cs, "get_object", m{"object": "sw-a1", "at": t0.Add(time.Hour).Format(time.RFC3339)})
		if num(t, now.Data, "attributes", "power_w") != 190 || num(t, then.Data, "attributes", "power_w") != 180 {
			t.Fatalf("now %v, then %v", dig(t, now.Data, "attributes", "power_w"), dig(t, then.Data, "attributes", "power_w"))
		}
		if str(t, then.Data, "as_of") == "" {
			t.Error("a past reading does not say so")
		}
		denied(t, cs, "get_object", m{"object": "sw-a1", "at": "last tuesday"}, "invalid-request")
	})

	t.Run("what is not there, or not clear, is refused", func(t *testing.T) {
		denied(t, cs, "get_object", m{"object": "no-such-thing"}, "not-found")
		denied(t, cs, "get_object", m{"object": ""}, "invalid-request")
		denied(t, cs, "get_object", m{"object": "00000000-0000-4000-8000-000000000000"}, "not-found")
		// Ethernet1 exists on every switch: a bare name designates nothing in particular.
		denied(t, cs, "get_object", m{"object": "Ethernet1"}, "invalid-request")
	})
}

func TestObjectSummary(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))
	e.clk.advance(time.Hour)
	e.cable(false, "sw-a1/Ethernet2", "srv-01/eth0")
	e.clk.advance(time.Hour)
	e.setPower("sw-a1", "JPE-SW-A1", 185)

	a := ok(t, cs, "object_summary", m{"object": "sw-a1"})
	if str(t, a.Data, "summary", "object") != "sw-a1" || str(t, a.Data, "summary", "type") != "device" || str(t, a.Data, "summary", "status") != "active" {
		t.Fatalf("head %v", dig(t, a.Data, "summary"))
	}
	if num(t, a.Data, "summary", "relations") != 1 || dig(t, a.Data, "summary", "truncated") != false {
		t.Fatalf("relations: %v", dig(t, a.Data, "summary"))
	}
	if str(t, a.Data, "object", "display") != "sw-a1" || num(t, a.Data, "object", "attributes", "power_w") != 185 {
		t.Fatalf("object %v", dig(t, a.Data, "object"))
	}
	// Provenance: where the value comes from, in the same call.
	var prov string
	for _, p := range dig(t, a.Data, "provenance").([]any) {
		if p.(map[string]any)["key"] == "power_w" {
			prov = p.(map[string]any)["source"].(string)
		}
	}
	if prov != "arista:sw-a1" {
		t.Errorf("provenance of power_w: %q", prov)
	}
	// The timeline starts with the latest change.
	if str(t, a.Data, "timeline", 0, "key") != "power_w" || num(t, a.Data, "timeline", 0, "after") != 185 {
		t.Errorf("timeline %v", dig(t, a.Data, "timeline", 0))
	}

	t.Run("the port sees its cable from either end", func(t *testing.T) {
		b := ok(t, cs, "object_summary", m{"object": "sw-a1/Ethernet2"})
		if num(t, b.Data, "summary", "relations") != 1 || str(t, b.Data, "relations", 0, "type") != "cabled_to" ||
			str(t, b.Data, "relations", 0, "other", "display") != "eth0" {
			t.Fatalf("relations %v", dig(t, b.Data, "relations"))
		}
		c := ok(t, cs, "object_summary", m{"object": "srv-01/eth0"})
		if str(t, c.Data, "relations", 0, "direction") == str(t, b.Data, "relations", 0, "direction") {
			t.Error("both ends see the cable in the same direction")
		}
	})
	t.Run("limits are honoured and said", func(t *testing.T) {
		b := ok(t, cs, "object_summary", m{"object": "sw-a1", "timeline_limit": 1, "relation_limit": 1})
		if len(dig(t, b.Data, "timeline").([]any)) != 1 {
			t.Errorf("timeline %v", dig(t, b.Data, "timeline"))
		}
		denied(t, cs, "object_summary", m{"object": "sw-a1", "timeline_limit": 1000}, "invalid-request")
		denied(t, cs, "object_summary", m{"object": "sw-a1", "relation_limit": -2}, "invalid-request")
	})
}

func TestObjectHistory(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))
	// sw-a1/Ethernet2 is cabled to srv-01 for a day, left alone, then to srv-02.
	e.clk.advance(24 * time.Hour)
	plug := e.clk.now()
	e.cable(false, "sw-a1/Ethernet2", "srv-01/eth0")
	e.clk.advance(24 * time.Hour)
	unplug := e.clk.now()
	e.cable(true, "sw-a1/Ethernet2", "srv-01/eth0")
	e.clk.advance(24 * time.Hour)
	replug := e.clk.now()
	e.cable(false, "sw-a1/Ethernet2", "srv-02/eth0")
	e.setPower("sw-a1", "JPE-SW-A1", 190)

	t.Run("what was plugged on a port in a period", func(t *testing.T) {
		a := ok(t, cs, "object_history", m{"object": "sw-a1/Ethernet2", "kind": "relations",
			"from": plug.Add(-time.Hour).Format(time.RFC3339), "to": unplug.Add(time.Hour).Format(time.RFC3339)})
		if num(t, a.Data, "summary", "total") != 1 || num(t, a.Data, "summary", "by_kind", "relation") != 1 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
		if str(t, a.Data, "items", 0, "name") != "cabled_to" || str(t, a.Data, "items", 0, "other", "id") != e.ids["srv-01/eth0"] {
			t.Fatalf("entry %v", dig(t, a.Data, "items", 0))
		}
		if str(t, a.Data, "items", 0, "valid_from") == "" || str(t, a.Data, "items", 0, "valid_to") == "" {
			t.Fatalf("a closed cable has both ends: %v", dig(t, a.Data, "items", 0))
		}
	})
	t.Run("the whole history, newest first", func(t *testing.T) {
		a := ok(t, cs, "object_history", m{"object": "sw-a1/Ethernet2", "kind": "relations"})
		if num(t, a.Data, "summary", "total") != 2 || str(t, a.Data, "items", 0, "other", "id") != e.ids["srv-02/eth0"] {
			t.Fatalf("items %v", dig(t, a.Data, "items"))
		}
		if dig(t, a.Data, "items", 0, "valid_to") != nil {
			t.Error("the current cable is closed")
		}
	})
	t.Run("a window that holds nothing", func(t *testing.T) {
		a := ok(t, cs, "object_history", m{"object": "sw-a1/Ethernet2", "kind": "relations",
			"from": unplug.Add(time.Hour).Format(time.RFC3339), "to": replug.Add(-time.Hour).Format(time.RFC3339)})
		if num(t, a.Data, "summary", "total") != 0 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("an attribute over time", func(t *testing.T) {
		a := ok(t, cs, "object_history", m{"object": "sw-a1", "attribute": "power_w"})
		if num(t, a.Data, "summary", "total") != 2 || num(t, a.Data, "items", 0, "value") != 190 || num(t, a.Data, "items", 1, "value") != 180 {
			t.Fatalf("items %v", dig(t, a.Data, "items"))
		}
		if str(t, a.Data, "items", 0, "source") != "arista:sw-a1" || str(t, a.Data, "items", 0, "kind") != "value" {
			t.Fatalf("entry %v", dig(t, a.Data, "items", 0))
		}
	})
	t.Run("both kinds by default", func(t *testing.T) {
		a := ok(t, cs, "object_history", m{"object": "sw-a1/Ethernet2"})
		if num(t, a.Data, "summary", "by_kind", "relation") != 2 || num(t, a.Data, "summary", "by_kind", "value") == 0 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("pages", func(t *testing.T) {
		a := ok(t, cs, "object_history", m{"object": "sw-a1/Ethernet2", "kind": "relations", "limit": 1})
		if num(t, a.Data, "summary", "returned") != 1 || dig(t, a.Data, "summary", "truncated") != true {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("bad input is refused", func(t *testing.T) {
		denied(t, cs, "object_history", m{"object": "sw-a1", "kind": "everything"}, "invalid-request")
		denied(t, cs, "object_history", m{"object": "sw-a1", "kind": "relations", "attribute": "power_w"}, "invalid-request")
		denied(t, cs, "object_history", m{"object": "sw-a1", "from": "2026-04-01", "to": "2026-03-01"}, "invalid-request")
		denied(t, cs, "object_history", m{"object": "sw-a1", "from": "soon"}, "invalid-request")
		denied(t, cs, "object_history", m{"object": "sw-a1", "attribute": "not an attribute"}, "invalid-request")
		denied(t, cs, "object_history", m{"object": "nothing"}, "not-found")
	})
}

func TestChangesSince(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))
	e.clk.advance(72 * time.Hour)
	// Planned: the switch should run 4.30.2. Observed: it runs 4.20.7M.
	plan := sdk.NewBatch("declaration:planned", sdk.ScopePartial)
	plan.Confidence = sdk.Declared
	plan.Object("sw-a1", "device").Identify("serial", "JPE-SW-A1").Set("os_version", "4.30.2")
	e.apply(plan)
	e.clk.advance(time.Hour)
	e.setPower("sw-b1", "JPE-SW-B1", 165)
	e.clk.advance(time.Hour)

	t.Run("what changed, and what nobody planned", func(t *testing.T) {
		a := ok(t, cs, "changes_since", m{"since": "3h"})
		if num(t, a.Data, "summary", "discrepancies", "total") != 1 || num(t, a.Data, "summary", "discrepancies_by_kind", "value_mismatch") != 1 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
		if str(t, a.Data, "discrepancies", 0, "key") != "os_version" || str(t, a.Data, "discrepancies", 0, "object_id") != e.ids["sw-a1"] {
			t.Fatalf("discrepancy %v", dig(t, a.Data, "discrepancies", 0))
		}
		if num(t, a.Data, "summary", "by_source", "arista:sw-b1") != 1 || str(t, a.Data, "changes", 0, "key") != "power_w" {
			t.Fatalf("changes %v, summary %v", dig(t, a.Data, "changes"), dig(t, a.Data, "summary"))
		}
	})
	t.Run("a quiet period is quiet", func(t *testing.T) {
		a := ok(t, cs, "changes_since", m{"since": "10m"})
		if num(t, a.Data, "summary", "changes", "total") != 0 || num(t, a.Data, "summary", "discrepancies", "total") != 0 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("one object, one kind of answer, one type", func(t *testing.T) {
		a := ok(t, cs, "changes_since", m{"since": "3h", "object": "sw-b1", "only": "changes"})
		if num(t, a.Data, "summary", "changes", "total") != 1 || len(dig(t, a.Data, "discrepancies").([]any)) != 0 {
			t.Fatalf("answer %v", a.Data)
		}
		b := ok(t, cs, "changes_since", m{"since": "3h", "only": "discrepancies"})
		if num(t, b.Data, "summary", "changes", "total") != 0 || num(t, b.Data, "summary", "discrepancies", "total") != 1 {
			t.Fatalf("summary %v", dig(t, b.Data, "summary"))
		}
		c := ok(t, cs, "changes_since", m{"since": "3h", "types": []string{"rack"}, "only": "changes"})
		if num(t, c.Data, "summary", "changes", "total") != 0 {
			t.Fatalf("summary %v", dig(t, c.Data, "summary"))
		}
	})
	t.Run("bad input is refused", func(t *testing.T) {
		refused(t, cs, "changes_since", m{})
		denied(t, cs, "changes_since", m{"since": "yesterday"}, "invalid-request")
		denied(t, cs, "changes_since", m{"since": "1d", "only": "weird"}, "invalid-request")
		denied(t, cs, "changes_since", m{"since": "1d", "until": "5d"}, "invalid-request")
	})
}

func TestDescribeSchema(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))
	a := ok(t, cs, "describe_schema", m{})
	if num(t, a.Data, "summary", "types") < 10 || num(t, a.Data, "summary", "relations") < 5 {
		t.Fatalf("summary %v", dig(t, a.Data, "summary"))
	}
	found := false
	for _, ty := range dig(t, a.Data, "types").([]any) {
		td := ty.(map[string]any)
		if td["name"] == "device" {
			found = true
		}
		for _, at := range td["attributes"].([]any) {
			if api.Sensitive(at.(string)) {
				t.Errorf("type %v lists the sensitive attribute %v", td["name"], at)
			}
		}
	}
	if !found {
		t.Error("device is not described")
	}
	b := ok(t, cs, "describe_schema", m{"type": "device"})
	if str(t, b.Data, "type", "name") != "device" {
		t.Fatalf("type %v", dig(t, b.Data, "type", "name"))
	}
	raw, _ := json.Marshal(b.Data)
	if !strings.Contains(string(raw), `"power_w"`) || !strings.Contains(string(raw), `"mounted_in"`) {
		t.Errorf("a device has power_w and is mounted_in a rack: %s", raw[:min(len(raw), 300)])
	}
	denied(t, cs, "describe_schema", m{"type": "no_such_type"}, "not-found")
}
