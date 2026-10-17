package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/deduce"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// topologySnapshot is what the UI's topology model is tested against
// (web/src/topology/deduced.test.ts): the answers of the real API over cables
// the real deducer wrote, not a hand-written mock (#49).
const topologySnapshot = "../../web/src/topology/testdata/deduced-api.json"

// observeNetwork adds, on top of the fixtures, what the switches and a second
// server report, so that the deducer finds one cable of each level:
//
//	sw-a1 Ethernet1 <-> sw-a2 Ethernet1   LLDP from both ends      high
//	sw-a2 Ethernet2 <-  srv-02 eno2       LLDP from one end only   medium
//	sw-a2 Ethernet3  =  srv-02 eno1       MAC learned on the port  low
func (e *env) observeNetwork() {
	e.t.Helper()
	b := sdk.NewBatch("arista:sw-a2", sdk.ScopePartial)
	b.Object("sw2", "device").Identify("serial", "JPE-SW-A2").Identify("name", "sw-a2").Set("role", "switch")
	for _, p := range []string{"Ethernet1", "Ethernet2", "Ethernet3"} {
		b.Object("a2:"+p, "interface").Identify("name", p).In("sw2")
	}
	o, _ := b.Lookup("a2:Ethernet3")
	o.Set("learned_macs", []any{"10/02:00:5e:00:53:21"})
	b.Object("srv2", "device").Identify("serial", "SRV-02-SN").Identify("name", "srv-02").Set("role", "server")
	b.Object("srv2:eno1", "interface").Identify("name", "eno1").In("srv2").Set("mac", "02:00:5e:00:53:21")
	b.Object("srv2:eno2", "interface").Identify("name", "eno2").In("srv2")
	b.Relations = append(b.Relations,
		&sdk.Relation{Type: "mounted_in", From: sdk.Ref("sw2"), To: byID("rack", e.ids["rack-p"]), Attributes: map[string]any{}},
		&sdk.Relation{Type: "mounted_in", From: sdk.Ref("srv2"), To: byID("rack", e.ids["rack-p"]), Attributes: map[string]any{}},
	)
	e.apply(b)

	l := sdk.NewBatch("lldp:test", sdk.ScopePartial)
	l.Relations = append(l.Relations,
		&sdk.Relation{Type: "neighbor_of", From: byID("interface", e.ids["e1"]), To: byID("interface", e.ids["a2:Ethernet1"]), Attributes: map[string]any{"protocol": "lldp"}},
		&sdk.Relation{Type: "neighbor_of", From: byID("interface", e.ids["a2:Ethernet1"]), To: byID("interface", e.ids["e1"]), Attributes: map[string]any{"protocol": "lldp"}},
		&sdk.Relation{Type: "neighbor_of", From: byID("interface", e.ids["srv2:eno2"]), To: byID("interface", e.ids["a2:Ethernet2"]), Attributes: map[string]any{"protocol": "lldp"}},
	)
	e.apply(l)
}

// The topology reads the deducer's cables as the deducer wrote them: through
// the engine, the store and GET /relations, each keeps the confidence
// "deduced", its source, its level and its evidence, and both ends name the
// device they sit on. The UI side of the same answers is tested in
// web/src/topology/deduced.test.ts, from the snapshot written here.
func TestDeducedCablesReachTheTopologyAsWritten(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.observeNetwork()
	e.clk.advance(time.Minute)
	d := &deduce.Deducer{Engine: e.eng, Store: e.st, Now: e.clk.now}
	rep, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result.ProposalID != 0 || len(rep.Plan.Cables) != 3 {
		t.Fatalf("the deducer did not apply three cables: %+v, proposal %d", rep.Plan, rep.Result.ProposalID)
	}

	linksPage := e.page("/api/v1/relations?type=cabled_to,connects,neighbor_of&limit=1000", tokReader)
	devicesPage := e.page("/api/v1/objects?type=device&limit=1000", tokReader)
	sitesPage := e.page("/api/v1/objects?type=site&limit=1000", tokReader)

	want := map[string][2]string{ // level -> evidence, then the two devices
		"high":   {"lldp", "sw-a1|sw-a2"},
		"medium": {"lldp_one_sided", "srv-02|sw-a2"},
		"low":    {"mac_table", "srv-02|sw-a2"},
	}
	devices := map[string]string{}
	for _, raw := range devicesPage.Items {
		devices[field(t, raw, "id")] = field(t, raw, "display")
	}
	seen := map[string]bool{}
	for _, l := range links(t, linksPage) {
		if l.Source != deduce.Source {
			continue
		}
		lv, _ := l.Attributes["level"].(string)
		w, ok := want[lv]
		reason, _ := l.Attributes["reason"].(string)
		if !ok || l.Type != "cabled_to" || l.Confidence != "deduced" || l.Attributes["evidence"] != w[0] || reason == "" {
			t.Errorf("deduced cable read back wrong: %+v", l)
			continue
		}
		ends := []string{devices[l.From.ParentID], devices[l.To.ParentID]}
		slices.Sort(ends)
		if l.From.Type != "interface" || l.To.Type != "interface" || strings.Join(ends, "|") != w[1] {
			t.Errorf("%s cable joins %v (%s, %s), want %s", lv, ends, l.From.Type, l.To.Type, w[1])
		}
		seen[lv] = true
	}
	if len(seen) != 3 {
		t.Fatalf("deduced cables read back: %v, want high, medium and low", seen)
	}

	snap := normalize(t, map[string]any{
		"links":   linksPage.Items,
		"devices": devicesPage.Items,
		"sites":   sitesPage.Items,
	})
	if os.Getenv("TRICA_UPDATE_TOPOLOGY") == "1" {
		if err := os.WriteFile(topologySnapshot, snap, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(topologySnapshot)
	if err != nil {
		t.Fatalf("%v: write it with TRICA_UPDATE_TOPOLOGY=1", err)
	}
	if !bytes.Equal(got, snap) {
		t.Fatalf("%s is stale: the API or the deducer changed what the topology reads. Run TRICA_UPDATE_TOPOLOGY=1 go test ./internal/api -run DeducedCablesReachTheTopology, then the web tests", topologySnapshot)
	}
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// normalize makes the answers reproducible: object ids (random per database)
// become names built from what the object is, timestamps a fixed instant, and
// lists are sorted, so the snapshot only changes when the shape does.
func normalize(t *testing.T, v map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	// Name every id: objects by type and display, interfaces under their device.
	names := map[string]string{}
	for _, k := range []string{"sites", "devices"} {
		for _, o := range tree[k].([]any) {
			m := o.(map[string]any)
			names[m["id"].(string)] = m["type"].(string) + ":" + m["display"].(string)
		}
	}
	for _, o := range tree["links"].([]any) {
		for _, end := range []string{"from", "to"} {
			m := o.(map[string]any)[end].(map[string]any)
			id := m["id"].(string)
			if _, ok := names[id]; ok {
				continue
			}
			n := m["type"].(string) + ":" + m["display"].(string)
			if p, ok := m["parent_id"].(string); ok && names[p] != "" {
				n = names[p] + "/" + n
			}
			names[id] = n
		}
	}
	var walk func(any) any
	walk = func(x any) any {
		switch v := x.(type) {
		case map[string]any:
			for k, e := range v {
				v[k] = walk(e)
			}
			return v
		case []any:
			for i, e := range v {
				v[i] = walk(e)
			}
			return v
		case string:
			if uuidRE.MatchString(v) {
				if n, ok := names[v]; ok {
					return n
				}
				return "id:unnamed"
			}
			if _, err := time.Parse(time.RFC3339Nano, v); err == nil && len(v) >= 20 {
				return "2026-03-01T00:00:00Z"
			}
			return v
		}
		return x
	}
	walk(tree)
	for _, o := range tree["links"].([]any) {
		m := o.(map[string]any)
		// Relation ids follow insertion order, which the map iteration of a
		// batch does not fix.
		m["id"] = 0
		// The deducer orders a cable's ends by object id, random here; a cable
		// has no direction, so the snapshot orders them by name.
		if m["type"] == "cabled_to" && m["from_id"].(string) > m["to_id"].(string) {
			m["from"], m["to"] = m["to"], m["from"]
			m["from_id"], m["to_id"] = m["to_id"], m["from_id"]
		}
	}
	for _, k := range []string{"links", "devices", "sites"} {
		list := tree[k].([]any)
		slices.SortFunc(list, func(a, b any) int {
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			return bytes.Compare(x, y)
		})
	}
	out, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}
