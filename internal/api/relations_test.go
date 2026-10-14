package api_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// linkEnd is the part of a bulk relation the tests read.
type linkEnd struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Display  string `json:"display"`
	ParentID string `json:"parent_id"`
	SiteID   string `json:"site_id"`
}

type link struct {
	Type       string         `json:"type"`
	FromID     string         `json:"from_id"`
	ToID       string         `json:"to_id"`
	Source     string         `json:"source"`
	Confidence string         `json:"confidence"`
	Attributes map[string]any `json:"attributes"`
	From       linkEnd        `json:"from"`
	To         linkEnd        `json:"to"`
}

func links(t *testing.T, p page) []link {
	t.Helper()
	out := make([]link, 0, len(p.Items))
	for _, raw := range p.Items {
		var l link
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

// cabling adds, on top of the fixtures, an interface on srv-01 (Lyon), a second
// interface on sw-a1 (Paris) and a third device in Paris, then cables:
//
//	sw-a1 Ethernet1 <-> srv-01 eno1   cabled_to, deduced (crosses sites)
//	sw-a1 Ethernet2 <-> sw-a2 Eth1    cabled_to, observed (stays in Paris)
//	cable c1 connects sw-a2 Eth1 (end a)  documented cable, declared
//	sw-a1 Ethernet1 sees sw-a2 Eth1   neighbor_of lldp
func (e *env) cabling() {
	e.t.Helper()
	b := sdk.NewBatch("lldp:test", sdk.ScopePartial)
	b.Confidence = sdk.Deduced
	b.Object("srv", "device").Identify("serial", "SRV-01-SN")
	b.Object("eno1", "interface").Identify("name", "eno1").In("srv").Set("oper_status", "up")
	b.Object("sw2", "device").Identify("serial", "JPE-SW-A2").Identify("name", "sw-a2").Set("role", "switch")
	b.Object("a2e1", "interface").Identify("name", "Ethernet1").In("sw2")
	b.Object("c1", "cable").Identify("label", "C-0001").Set("media", "fiber")
	e.apply(b)

	rb := sdk.NewBatch("lldp:test-links", sdk.ScopePartial)
	rb.Confidence = sdk.Deduced
	rb.Relations = append(rb.Relations,
		&sdk.Relation{Type: "cabled_to", From: byID("interface", e.ids["e1"]), To: byID("interface", e.ids["eno1"]), Attributes: map[string]any{}},
		&sdk.Relation{Type: "neighbor_of", From: byID("interface", e.ids["e1"]), To: byID("interface", e.ids["a2e1"]), Attributes: map[string]any{"protocol": "lldp"}},
		&sdk.Relation{Type: "connects", From: byID("cable", e.ids["c1"]), To: byID("interface", e.ids["a2e1"]), Attributes: map[string]any{"end": "a"}, Confidence: sdk.Declared},
	)
	e.apply(rb)
	ob := sdk.NewBatch("lldp:test-observed", sdk.ScopePartial)
	ob.Confidence = sdk.Observed
	ob.Relations = append(ob.Relations,
		&sdk.Relation{Type: "cabled_to", From: byID("interface", e.ids["e2"]), To: byID("interface", e.ids["a2e1"]), Attributes: map[string]any{}},
	)
	e.apply(ob)
}

func byID(typ, id string) sdk.Endpoint {
	return sdk.Endpoint{Type: typ, Identity: map[string]string{sdk.IDKey: id}}
}

func TestListRelationsNamesBothEndsAndWhereTheyStand(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cabling()
	p := e.page("/api/v1/relations?type=cabled_to", tokReader)
	ls := links(t, p)
	if p.Total != 2 || len(ls) != 2 {
		t.Fatalf("cabled_to: total %d: %s", p.Total, p.Items)
	}
	byFrom := map[string]link{}
	for _, l := range ls {
		byFrom[l.From.Display] = l
	}
	l := byFrom["Ethernet1"]
	if l.Type != "cabled_to" || l.Source != "lldp:test-links" || l.Confidence != "deduced" {
		t.Fatalf("the link lost its provenance: %+v", l)
	}
	// The ends carry the type, the device the interface is on and the site, so a client needs no request per object.
	if l.From.ID != e.ids["e1"] || l.From.Type != "interface" || l.From.ParentID != e.ids["sw"] || l.From.SiteID != e.ids["paris"] {
		t.Fatalf("from end: %+v", l.From)
	}
	if l.To.ID != e.ids["eno1"] || l.To.ParentID != e.ids["srv"] || l.To.SiteID != e.ids["lyon"] {
		t.Fatalf("to end: %+v", l.To)
	}
	if o := byFrom["Ethernet2"]; o.Confidence != "observed" || o.Source != "lldp:test-observed" {
		t.Fatalf("observed link: %+v", o)
	}
}

func TestListRelationsCombinesTypesAndKeepsAttributes(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cabling()
	p := e.page("/api/v1/relations?type=connects,neighbor_of", tokReader)
	got := map[string]link{}
	for _, l := range links(t, p) {
		got[l.Type] = l
	}
	if len(got) != 2 {
		t.Fatalf("connects,neighbor_of: %s", p.Items)
	}
	if c := got["connects"]; c.From.Type != "cable" || c.From.Display != "C-0001" || c.Attributes["end"] != "a" || c.Confidence != "declared" {
		t.Fatalf("connects: %+v", c)
	}
	if n := got["neighbor_of"]; n.Attributes["protocol"] != "lldp" {
		t.Fatalf("neighbor_of: %+v", n)
	}
	// A type nobody asked for is not there.
	for _, l := range links(t, e.page("/api/v1/relations?type=connects", tokReader)) {
		if l.Type != "connects" {
			t.Fatalf("type filter leaked %s", l.Type)
		}
	}
}

func TestListRelationsOnlyCurrentOnes(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cabling()
	if p := e.page("/api/v1/relations?type=cabled_to", tokReader); p.Total != 2 {
		t.Fatalf("fixture: %d cabled_to links before the closure, want 2", p.Total)
	}
	// Later (a relation closed in the instant it was opened is deleted, which
	// would leave nothing to leak), a full-scope batch of the same source that
	// no longer reports the cable closes it.
	e.clk.advance(time.Hour)
	b := sdk.NewBatch("lldp:test-observed", sdk.ScopeFull)
	b.Confidence = sdk.Observed
	b.ObservedAt = e.clk.now()
	b.Object("e2", "interface").Identify("name", "Ethernet2").In("sw")
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1")
	e.apply(b)
	// The closed relation is still in the history: the test is about it being left out.
	var closed int
	if err := e.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM relations WHERE type = 'cabled_to' AND NOT upper_inf(valid)`).Scan(&closed); err != nil || closed != 1 {
		t.Fatalf("fixture: %d closed cabled_to relations (%v), want 1", closed, err)
	}
	p := e.page("/api/v1/relations?type=cabled_to", tokReader)
	if p.Total != 1 {
		t.Fatalf("a closed relation is still listed: %s", p.Items)
	}
	if l := links(t, p)[0]; l.Source == "lldp:test-observed" {
		t.Fatalf("the closed relation is the one listed: %+v", l)
	}
}

// The listing reads at most maxScan (5000) relations and says so when it
// stopped there: a client must not take a cut list for the whole network.
func TestListRelationsSaysWhenItStoppedReading(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	const ifaces = 101 // 101*100/2 = 5050 pairs, more than the scan cap
	b := sdk.NewBatch("lldp:bulk", sdk.ScopePartial)
	b.Confidence = sdk.Deduced
	b.Object("d", "device").Identify("name", "bulk-1").Set("role", "switch")
	for i := range ifaces {
		b.Object("i"+strconv.Itoa(i), "interface").Identify("name", "p"+strconv.Itoa(i)).In("d")
	}
	for i := range ifaces {
		for j := i + 1; j < ifaces; j++ {
			b.Relations = append(b.Relations, &sdk.Relation{Type: "cabled_to",
				From: sdk.Endpoint{Ref: "i" + strconv.Itoa(i)}, To: sdk.Endpoint{Ref: "i" + strconv.Itoa(j)}, Attributes: map[string]any{}})
		}
	}
	e.apply(b)
	p := e.page("/api/v1/relations?type=cabled_to&limit=1", tokReader)
	if !p.Truncated || p.Total != 5000 {
		t.Fatalf("5050 links: total %d, truncated %v; want 5000 and truncated", p.Total, p.Truncated)
	}
}

func TestListRelationsPaging(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cabling()
	first := e.page("/api/v1/relations?type=cabled_to&limit=1", tokReader)
	second := e.page("/api/v1/relations?type=cabled_to&limit=1&offset=1", tokReader)
	if first.Truncated || second.Truncated {
		t.Fatal("a short list is flagged as truncated")
	}
	if first.Total != 2 || len(first.Items) != 1 || second.Total != 2 || len(second.Items) != 1 {
		t.Fatalf("paging: %d/%d %d/%d", first.Total, len(first.Items), second.Total, len(second.Items))
	}
	if string(first.Items[0]) == string(second.Items[0]) {
		t.Fatal("two pages returned the same relation")
	}
	if p := e.page("/api/v1/relations?type=cabled_to&offset=5", tokReader); len(p.Items) != 0 || p.Total != 2 {
		t.Fatalf("past the end: %d items, total %d", len(p.Items), p.Total)
	}
}

func TestListRelationsRejectsABadType(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	for _, path := range []string{"/api/v1/relations", "/api/v1/relations?type="} {
		if p := wantProblem(t, e.get(path, tokReader), 400, "invalid-request"); !strings.Contains(p.Detail, "type is required") {
			t.Fatalf("%s: detail %q does not say a type is required", path, p.Detail)
		}
	}
	wantProblem(t, e.get("/api/v1/relations?type=teleports_to", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/relations?type=cabled_to,teleports_to", tokReader), 400, "invalid-request")
	nine := strings.TrimSuffix(strings.Repeat("cabled_to,", 9), ",")
	wantProblem(t, e.get("/api/v1/relations?type="+nine, tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/relations?type=cabled_to&limit=0", tokReader), 400, "invalid-request")
}

func TestListRelationsNeedsAToken(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	wantProblem(t, e.get("/api/v1/relations?type=cabled_to", ""), 401, "unauthenticated")
	wantProblem(t, e.get("/api/v1/relations?type=cabled_to", tokProposeNR), 403, "forbidden")
}

// A relation is listed only if both of its ends are readable: a grant limited
// to one site or to some types must not learn of the other side through a link.
func TestListRelationsLeavesOutWhatTheCallerCannotRead(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cabling()
	// Paris only: sw-a1 Ethernet1 <-> srv-01 (Lyon) is hidden, Ethernet2 <-> sw-a2 stays.
	paris := links(t, e.page("/api/v1/relations?type=cabled_to", tokSiteParis))
	if len(paris) != 0 {
		// sw-a2 has no rack: it is in no site, so a site-restricted grant fails closed on it.
		t.Fatalf("site-restricted reader saw links to objects outside its site: %+v", paris)
	}
	full := links(t, e.page("/api/v1/relations?type=cabled_to", tokAdmin))
	if len(full) != 2 {
		t.Fatalf("admin sees %d links", len(full))
	}
	// Put sw-a2 in the Paris rack: the intra-site link appears, the cross-site one still does not.
	b := sdk.NewBatch("declaration:test2", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Relations = append(b.Relations, &sdk.Relation{Type: "mounted_in", From: byID("device", e.ids["sw2"]), To: byID("rack", e.ids["rack-p"]), Attributes: map[string]any{}})
	e.apply(b)
	paris = links(t, e.page("/api/v1/relations?type=cabled_to", tokSiteParis))
	if len(paris) != 1 || paris[0].From.Display != "Ethernet2" {
		t.Fatalf("Paris reader should see exactly the intra-site link: %+v", paris)
	}
	// Devices only: interfaces are not readable, so no link between interfaces is.
	if dev := e.page("/api/v1/relations?type=cabled_to", tokDevices); dev.Total != 0 {
		t.Fatalf("devices-only reader saw interface links: %s", dev.Items)
	}
	if paris[0].To.Display != "Ethernet1" {
		t.Fatalf("to end: %+v", paris[0].To)
	}
}
