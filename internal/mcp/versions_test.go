package mcp_test

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestVersionCompliance(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokReader))

	t.Run("against a policy: the summary says how the fleet stands, the worst come first", func(t *testing.T) {
		a := ok(t, cs, "version_compliance", m{"policy": "switch-os"})
		s := dig(t, a.Data, "summary")
		for k, want := range map[string]int{"governed": 5, "compliant": 1, "within_tolerance": 1, "behind": 2, "unknown": 1} {
			if got := num(t, s, k); got != want {
				t.Errorf("%s = %d, want %d (%v)", k, got, want, s)
			}
		}
		if str(t, s, "reference_version") != "4.30.2" || str(t, s, "policy") != "switch-os" || num(t, s, "max_minor_lag") != 1 {
			t.Errorf("summary %v", s)
		}
		// Default listing: what is behind, the furthest first, then what is unknown.
		if got := displays(t, a.Data, "items"); !reflect.DeepEqual(got, []string{"sw-b1", "sw-a1", "sw-b2"}) {
			t.Fatalf("items %v", got)
		}
		if str(t, a.Data, "items", 0, "status") != "behind" || str(t, a.Data, "items", 0, "current") != "4.19.9" ||
			str(t, a.Data, "items", 0, "lag", "level") != "minor" || num(t, a.Data, "items", 0, "lag", "by") != 11 {
			t.Errorf("worst %v", dig(t, a.Data, "items", 0))
		}
		if str(t, a.Data, "items", 2, "status") != "unknown" || num(t, s, "listed", "total") != 3 {
			t.Errorf("last %v, listed %v", dig(t, a.Data, "items", 2), dig(t, s, "listed"))
		}
		if num(t, s, "by_version", "4.30.2") != 1 || num(t, s, "by_version", "(none)") != 1 {
			t.Errorf("by_version %v", dig(t, s, "by_version"))
		}
	})
	t.Run("every status on request", func(t *testing.T) {
		a := ok(t, cs, "version_compliance", m{"policy": "switch-os", "statuses": []string{"behind", "unknown", "within_tolerance", "compliant"}})
		if got := displays(t, a.Data, "items"); !reflect.DeepEqual(got, []string{"sw-b1", "sw-a1", "sw-b2", "sw-b3", "sw-a2"}) {
			t.Fatalf("items %v", got)
		}
		if str(t, a.Data, "items", 3, "status") != "within_tolerance" || str(t, a.Data, "items", 4, "status") != "compliant" {
			t.Errorf("statuses %v", dig(t, a.Data, "items"))
		}
	})
	t.Run("against a target, strict unless a lag is allowed", func(t *testing.T) {
		strict := ok(t, cs, "version_compliance", m{"target": "4.30.2", "query": "role:switch"})
		if num(t, strict.Data, "summary", "behind") != 3 || num(t, strict.Data, "summary", "governed") != 5 {
			t.Fatalf("strict %v", dig(t, strict.Data, "summary"))
		}
		lax := ok(t, cs, "version_compliance", m{"target": "4.30.2", "query": "role:switch", "max_minor_lag": 1})
		if num(t, lax.Data, "summary", "behind") != 2 || num(t, lax.Data, "summary", "within_tolerance") != 1 {
			t.Fatalf("with a lag %v", dig(t, lax.Data, "summary"))
		}
	})
	t.Run("the whole device population by default", func(t *testing.T) {
		a := ok(t, cs, "version_compliance", m{"target": "6.0"})
		if num(t, a.Data, "summary", "governed") != 17 || num(t, a.Data, "summary", "compliant") != 12 {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("the list is paginated, the summary is not", func(t *testing.T) {
		a := ok(t, cs, "version_compliance", m{"policy": "switch-os", "limit": 1})
		if num(t, a.Data, "summary", "behind") != 2 || num(t, a.Data, "summary", "listed", "returned") != 1 ||
			dig(t, a.Data, "summary", "listed", "truncated") != true {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
	})
	t.Run("a policy the tool cannot evaluate is refused or flagged, never read as all clear", func(t *testing.T) {
		b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
		b.Confidence = sdk.Declared
		b.Object("p2", "version_policy").Identify("name", "empty-selector").Set("software", "network-os").Set("attribute", "os_version").
			Set("selector", "type:device vendor:nobody-makes-this").Set("reference_version", "4.30.2")
		b.Object("p3", "version_policy").Identify("name", "no-attribute").Set("software", "network-os").Set("reference_version", "4.30.2")
		e.apply(b)
		// A selector that matches no object (a malformed one is refused when
		// written): the answer says nothing is governed and why, instead of
		// reporting a clean fleet.
		a := ok(t, cs, "version_compliance", m{"policy": "empty-selector"})
		if num(t, a.Data, "summary", "governed") != 0 || !strings.Contains(str(t, a.Data, "summary", "warning"), "matched no object") {
			t.Fatalf("summary %v", dig(t, a.Data, "summary"))
		}
		denied(t, cs, "version_compliance", m{"policy": "no-attribute"}, "invalid-request")
	})
	t.Run("bad input is refused", func(t *testing.T) {
		denied(t, cs, "version_compliance", m{}, "invalid-request")
		denied(t, cs, "version_compliance", m{"policy": "switch-os", "target": "4.30.2"}, "invalid-request")
		denied(t, cs, "version_compliance", m{"policy": "sw-a1"}, "invalid-request")
		denied(t, cs, "version_compliance", m{"target": "latest"}, "invalid-request")
		denied(t, cs, "version_compliance", m{"target": "4.30.2", "statuses": []string{"fine"}}, "invalid-request")
		denied(t, cs, "version_compliance", m{"target": "4.30.2", "max_minor_lag": -1}, "invalid-request")
		denied(t, cs, "version_compliance", m{"target": "4.30.2", "attribute": "snmp_community"}, "forbidden")
		denied(t, cs, "version_compliance", m{"target": "4.30.2", "query": "snmp_community:x"}, "sensitive-attribute")
	})
}

// The tool reads versions as the search does: the numbers in the text, in
// order. It is the same reading, checked here against the SQL function, so
// that "os_version<4.20" in a query and "behind 4.20" in this tool agree on
// what 4.20.7M is.
func TestVersionKeyAgreesWithSQL(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	for _, v := range []string{"4.20.7M", "4.9", "6.1.0", "v2.0-rc1", "10", "4.20.1F", "", "unknown", "1.2.3.4.5", "0.0.1", "007.08"} {
		var raw string
		if err := e.st.Pool.QueryRow(context.Background(), `SELECT trica_version_key($1)::text`, v).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		want := []uint64{}
		for _, f := range strings.Split(strings.Trim(raw, "{}"), ",") {
			if f == "" {
				continue
			}
			n, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, n)
		}
		if got := mcp.VersionKey(v); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: Go reads %v, SQL reads %v", v, got, want)
		}
	}
}

// A policy is data that anyone allowed to write policies may have edited: its
// selector is as untrusted as a query typed by the model.
func TestPolicySelectorCannotQuerySecrets(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("leaky", "version_policy").Identify("name", "leaky-os").Set("software", "network-os").Set("attribute", "os_version").
		Set("selector", "role:switch snmp_community:x").Set("reference_version", "4.30.2")
	e.apply(b)
	denied(t, e.connect(e.as(tokReader)), "version_compliance", m{"policy": "leaky-os"}, "sensitive-attribute")
}
