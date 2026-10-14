package api_test

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// docOps lists the operations of the published document: operationId ->
// (method, x-trica-action).
type docOp struct{ method, action string }

func docOps(t testing.TB, e *env) map[string]docOp {
	t.Helper()
	r := e.get("/api/v1/openapi.json", tokReader)
	wantStatus(t, r, 200)
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Action      string `json:"x-trica-action"`
		} `json:"paths"`
	}
	r.json(t, &doc)
	out := map[string]docOp{}
	for _, item := range doc.Paths {
		for method, op := range item {
			if op.OperationID == "" {
				continue
			}
			out[op.OperationID] = docOp{method: strings.ToUpper(method), action: op.Action}
		}
	}
	return out
}

// Every endpoint refuses the caller who lacks its action, and anyone who is
// not authenticated, before it does anything.
func TestEveryOperationEnforcesItsAction(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	pid, did := e.withMaterial(t)
	calls := e.calls(t, pid, did)
	ops := docOps(t, e)
	if len(ops) < 25 {
		t.Fatalf("only %d operations in the document", len(ops))
	}
	before := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokAdmin).Total
	pending := e.page("/api/v1/proposals?status=pending", tokAdmin).Total

	for id, op := range ops {
		call, ok := calls[id]
		if !ok {
			t.Errorf("operation %s has no request in the test table", id)
			continue
		}
		if call.method != op.method {
			t.Errorf("%s: documented as %s, requested as %s", id, op.method, call.method)
		}
		if op.action == "public" {
			if r := e.do(call.method, call.path, "", nilIfEmpty(call.body)); r.Status == 401 || r.Status == 403 {
				t.Errorf("%s is public but answered %d", id, r.Status)
			}
			continue
		}
		if !slices.Contains([]string{api.ActionRead, api.ActionPropose, api.ActionApprove, api.ActionAdmin}, op.action) {
			t.Errorf("%s: unknown action %q", id, op.action)
			continue
		}
		// Not authenticated.
		r := e.do(call.method, call.path, "", nilIfEmpty(call.body))
		wantProblem(t, r, 401, "unauthenticated")
		if got := r.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
			t.Errorf("%s: 401 without WWW-Authenticate: %q", id, got)
		}
		wantProblem(t, e.do(call.method, call.path, "not-a-token", nilIfEmpty(call.body)), 401, "unauthenticated")
		// Authenticated, with every action but the one it needs.
		wantProblem(t, e.do(call.method, call.path, e.grantAllBut(op.action), nilIfEmpty(call.body)), 403, "forbidden")
	}

	// None of the refusals did anything.
	if after := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokAdmin).Total; after != before {
		t.Fatalf("refused calls changed the inventory: %d -> %d changes", before, after)
	}
	if n := e.page("/api/v1/proposals?status=pending", tokAdmin).Total; n != pending {
		t.Fatalf("refused calls decided a proposal: %d -> %d pending", pending, n)
	}
	if e.page("/api/v1/saved-queries", tokAdmin).Total != 0 {
		t.Fatal("a refused call created a saved query")
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// A caller that holds only the action an endpoint asks for is let through.
func TestAnActionAloneOpensItsEndpoints(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	pid, did := e.withMaterial(t)
	calls := e.calls(t, pid, did)
	for id, op := range docOps(t, e) {
		if op.action == "public" || id == "previewBatch" { // preview needs propose and read
			continue
		}
		call := calls[id]
		r := e.do(call.method, call.path, e.grantOnly(op.action), nilIfEmpty(call.body))
		if r.Status == 401 || r.Status == 403 {
			t.Errorf("%s: a caller holding only %q was refused (%d): %s", id, op.action, r.Status, r.Body)
		}
	}
}

func TestProposeOnlyCallerCannotReadWhatItSubmitted(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := userBatch("", func(b *sdk.Batch) { b.Object("n", "device").Identify("name", "srv-02").Set("role", "server") })
	r := e.do("POST", "/api/v1/batches", tokProposeNR, batchJSON(t, b))
	wantStatus(t, r, 200)
	// The answer to a caller who cannot read says nothing about the inventory.
	var out map[string]any
	r.json(t, &out)
	res, _ := out["result"].(map[string]any)
	if objs, _ := res["objects"].(map[string]any); res["changes"] != nil || res["touched"] != nil || len(objs) != 0 {
		t.Fatalf("a caller without read learned what its batch touched: %s", r.Body)
	}
	if res["created"].(float64) != 1 {
		t.Fatalf("counts missing: %s", r.Body)
	}
	wantProblem(t, e.get("/api/v1/objects", tokProposeNR), 403, "forbidden")
	wantProblem(t, e.get("/api/v1/schema", tokProposeNR), 403, "forbidden")
	wantProblem(t, e.do("POST", "/api/v1/batches/preview", tokProposeNR, batchJSON(t, b)), 403, "forbidden")
}

func TestTypeLimitedReader(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw, e1, paris := e.ids["sw"], e.ids["e1"], e.ids["paris"]

	// Lists are limited to what is readable, and the counts agree.
	p := e.page("/api/v1/objects", tokDevices)
	if p.Total != 2 || len(p.Items) != 2 {
		t.Fatalf("a devices-only reader saw %d objects", p.Total)
	}
	for _, id := range ids(t, p, "type") {
		if id != "device" {
			t.Fatalf("leaked type %s", id)
		}
	}
	if e.page("/api/v1/objects?q="+url.QueryEscape("type:interface"), tokDevices).Total != 0 {
		t.Fatal("the type restriction can be undone from the query")
	}
	if e.page("/api/v1/objects?q="+url.QueryEscape("NOT type:device"), tokDevices).Total != 0 {
		t.Fatal("negation escapes the type restriction")
	}
	if got := e.page("/api/v1/objects?q="+url.QueryEscape("NOT type:interface"), tokDevices); got.Total != 2 {
		t.Fatalf("a negation hides what is readable: %d items", got.Total)
	}
	if got := e.page("/api/v1/objects?q="+url.QueryEscape("type:device OR type:interface"), tokDevices); got.Total != 2 {
		t.Fatalf("an or escapes the type restriction, or hides what is readable: %d items: %s", got.Total, got.Items)
	}
	wantProblem(t, e.get("/api/v1/objects?type=interface", tokDevices), 403, "forbidden")
	s := e.get("/api/v1/search?q=Ethernet1", tokDevices)
	wantStatus(t, s, 200)
	var sp struct {
		Summary struct {
			Total int `json:"total"`
		} `json:"summary"`
	}
	s.json(t, &sp)
	if sp.Summary.Total != 0 {
		t.Fatalf("search found an interface for a devices-only reader: %s", s.Body)
	}

	// By id: devices yes, others no, and unknown stays unknown.
	wantStatus(t, e.get("/api/v1/objects/"+sw, tokDevices), 200)
	for _, path := range []string{"", "/provenance", "/history?key=oper_status", "/relations", "/timeline"} {
		wantProblem(t, e.get("/api/v1/objects/"+e1+path, tokDevices), 403, "forbidden")
		wantProblem(t, e.get("/api/v1/objects/"+paris+path, tokDevices), 403, "forbidden")
	}
	wantProblem(t, e.get("/api/v1/objects/"+e1+"?at=2026-03-01T09:00:00Z", tokDevices), 403, "forbidden")

	// The relations of a device leave out the ends the caller may not read.
	r := e.page("/api/v1/objects/"+sw+"/relations", tokReader)
	full := r.Total
	if full < 1 { // mounted_in the rack: a type the devices-only reader may not read
		t.Fatalf("fixture has %d relations", full)
	}
	if lim := e.page("/api/v1/objects/"+sw+"/relations", tokDevices); lim.Total != 0 {
		t.Fatalf("the relation to a rack leaked to a devices-only reader: %s", lim.Items)
	}
	// Feeds are filtered by type.
	for _, it := range e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokDevices).Items {
		if field(t, it, "type") != "device" {
			t.Fatalf("change on a %s leaked: %s", field(t, it, "type"), it)
		}
	}
	if e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokDevices).Total >= e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokReader).Total {
		t.Fatal("the changes feed was not filtered")
	}
	// whohad finds only readable objects: a MAC lives on an interface.
	if e.page("/api/v1/whohad?key=mac&value=02:00:5e:00:53:01", tokReader).Total != 1 {
		t.Fatal("fixture: the interface mac is not found by an unrestricted reader")
	}
	if e.page("/api/v1/whohad?key=mac&value=02:00:5e:00:53:01", tokDevices).Total != 0 {
		t.Fatal("whohad leaked an object the caller may not read")
	}
	if e.page("/api/v1/whohad?key=mgmt_ip&value=192.0.2.10", tokDevices).Total != 1 {
		t.Fatal("whohad hid a readable object")
	}
}

func TestSiteLimitedReader(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	// The harness builds the grant before the ids exist in newEnv's literal
	// order, so assert the grant really names the site.
	if got := e.auth.Tokens[tokSiteParis].Sites; len(got) != 1 || got[0] != e.ids["paris"] {
		t.Fatalf("site grant %v", got)
	}
	paris, lyon := e.ids["paris"], e.ids["lyon"]
	rackP, rackL := e.ids["rack-p"], e.ids["rack-l"]
	sw, srv, e1 := e.ids["sw"], e.ids["srv"], e.ids["e1"]

	// In Paris: the site, its rack, the switch (mounted in the rack), its interfaces.
	for _, id := range []string{paris, rackP, sw, e1} {
		r := e.get("/api/v1/objects/"+id, tokSiteParis)
		wantStatus(t, r, 200)
		if got := r.obj(t)["site_id"]; got != paris {
			t.Fatalf("object %s: site_id %v", id, got)
		}
	}
	// Elsewhere: refused, not hidden as unknown.
	for _, id := range []string{lyon, rackL, srv} {
		for _, path := range []string{"", "/provenance", "/relations", "/timeline", "/history?key=os_version"} {
			wantProblem(t, e.get("/api/v1/objects/"+id+path, tokSiteParis), 403, "forbidden")
		}
	}
	// A site-limited principal reads no list: the exact total of a subset cannot be promised.
	wantProblem(t, e.get("/api/v1/objects", tokSiteParis), 403, "forbidden")
	wantProblem(t, e.get("/api/v1/search?q=sw-a1", tokSiteParis), 403, "forbidden")

	// Feeds and joins keep to the site.
	ch := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokSiteParis)
	if ch.Total == 0 {
		t.Fatal("the site's own changes are hidden")
	}
	for _, it := range ch.Items {
		if oid := field(t, it, "object_id"); oid == lyon || oid == rackL || oid == srv {
			t.Fatalf("a change in another site leaked: %s", it)
		}
	}
	if e.page("/api/v1/whohad?key=os_version&value=6.1.0", tokSiteParis).Total != 0 {
		t.Fatal("whohad leaked a Lyon server")
	}
	if e.page("/api/v1/whohad?key=os_version&value=4.20.7M", tokSiteParis).Total != 1 {
		t.Fatal("whohad hid a Paris switch")
	}
	rels := e.page("/api/v1/objects/"+rackP+"/relations", tokSiteParis)
	if rels.Total != 1 { // the switch mounted in it
		t.Fatalf("rack-a relations: %s", rels.Items)
	}

	// A device in two places has no single site: a site-limited reader is refused.
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.ObservedAt = e.clk.now()
	b.Relations = append(b.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Endpoint{Type: "device", Identity: map[string]string{sdk.IDKey: sw}}, To: sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: rackL}}, Attributes: map[string]any{}})
	e.clk.advance(1)
	if _, err := e.eng.Apply(t.Context(), b, "", ""); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.get("/api/v1/objects/"+sw, tokSiteParis), 403, "forbidden")
	wantProblem(t, e.get("/api/v1/objects/"+e1, tokSiteParis), 403, "forbidden")
	// An unrestricted reader still reads it, and its site is unknown.
	r := e.get("/api/v1/objects/"+sw, tokReader)
	wantStatus(t, r, 200)
	if got, ok := r.obj(t)["site_id"]; ok && got != "" && got != nil {
		t.Fatalf("an ambiguous object claims the site %v", got)
	}
}

func TestTypeLimitedProposer(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	// Devices yes.
	wantStatus(t, e.do("POST", "/api/v1/batches", tokDevProp, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("n", "device").Identify("name", "srv-02").Set("role", "server")
	}))), 200)
	// Another type no, whether created or edited through its id, and nothing is written.
	n := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokAdmin).Total
	if n == 0 {
		t.Fatal("fixture: no changes")
	}
	wantProblem(t, e.do("POST", "/api/v1/batches", tokDevProp, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("i", "interface").Identify("name", "Ethernet9").Identify(sdk.IDKey, e.ids["e1"]).Set("oper_status", "down")
	}))), 403, "forbidden")
	wantProblem(t, e.do("POST", "/api/v1/batches", tokDevProp, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("i", "interface").Identify("name", "Ethernet9").In("n")
		b.Object("n", "device").Identify("name", "srv-03")
	}))), 403, "forbidden")
	wantProblem(t, e.do("POST", "/api/v1/batches/preview", tokDevProp, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("i", "site").Identify("code", "XXX1")
	}))), 403, "forbidden")
	if after := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokAdmin).Total; after != n {
		t.Fatalf("refused batches wrote changes: %d then %d", n, after)
	}

	// Proposals touching types the caller may not read are hidden from lists and refused by id.
	pb := userBatch("", func(b *sdk.Batch) { b.Object("s", "site").Identify("code", "NEW1").Set("name", "new-1") })
	r := e.do("POST", "/api/v1/batches", tokMCP, batchJSON(t, pb))
	wantStatus(t, r, 202)
	siteProposal := int64(r.obj(t)["result"].(map[string]any)["proposal_id"].(float64))
	if e.page("/api/v1/proposals", tokAdmin).Total != 1 {
		t.Fatal("fixture: no proposal")
	}
	if got := e.page("/api/v1/proposals", tokDevices).Total; got != 0 {
		t.Fatalf("a devices-only reader sees a site proposal in the list: %d", got)
	}
	wantProblem(t, e.get("/api/v1/proposals/"+itoa(siteProposal), tokDevices), 403, "forbidden")
	// An approver limited to devices cannot decide it either.
	e.auth.Tokens["t-approve-dev"] = api.Grant{Principal: api.Principal{Subject: "judy", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionApprove}, Types: []string{"device"}}
	wantProblem(t, e.do("POST", "/api/v1/proposals/"+itoa(siteProposal)+"/approve", "t-approve-dev", nil), 403, "forbidden")
	wantProblem(t, e.do("POST", "/api/v1/proposals/"+itoa(siteProposal)+"/reject", "t-approve-dev", nil), 403, "forbidden")
	if e.get("/api/v1/proposals/"+itoa(siteProposal), tokAdmin).obj(t)["status"] != "pending" {
		t.Fatal("a refused decision changed the proposal")
	}
	// Discrepancies on objects the caller may not read are not listed, nor accepted.
	i1 := e.ids["e1"]
	wantStatus(t, e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("x", "interface").Identify(sdk.IDKey, i1).Set("oper_status", "down")
	}))), 200)
	all := e.page("/api/v1/discrepancies?status=open", tokAdmin)
	if all.Total < 1 {
		t.Fatalf("fixture: no discrepancy on the interface")
	}
	if got := e.page("/api/v1/discrepancies?status=open", tokDevices).Total; got != 0 {
		t.Fatalf("a devices-only reader sees %d discrepancies on interfaces", got)
	}
	var d struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(all.Items[0], &d); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.do("POST", "/api/v1/discrepancies/"+itoa(d.ID)+"/accept", "t-approve-dev", nil), 403, "forbidden")
	if e.page("/api/v1/discrepancies?status=open", tokAdmin).Total != all.Total {
		t.Fatal("a refused acceptance closed a discrepancy")
	}
}
