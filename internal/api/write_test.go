package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestNewRefusesMissingCollaborators(t *testing.T) {
	eng := &reconcile.Engine{Store: &store.Store{}, Schema: nil}
	full := func() api.Config {
		e := &reconcile.Engine{Store: &store.Store{}, Schema: schema.Builtin}
		return api.Config{Engine: e, Authorizer: &api.StaticAuthorizer{}, Auditor: &api.MemoryAuditor{}}
	}
	if _, err := api.New(full()); err != nil {
		t.Fatalf("complete config refused: %v", err)
	}
	cases := map[string]func(c *api.Config){
		"no authorizer":           func(c *api.Config) { c.Authorizer = nil },
		"no auditor":              func(c *api.Config) { c.Auditor = nil },
		"no engine":               func(c *api.Config) { c.Engine = nil },
		"engine without registry": func(c *api.Config) { c.Engine = eng },
	}
	for name, mutate := range cases {
		c := full()
		mutate(&c)
		if _, err := api.New(c); err == nil {
			t.Errorf("%s: New accepted the configuration", name)
		}
	}
}

// batchBody is a batch as JSON: a person's client sends this.
func batchJSON(t testing.TB, b *sdk.Batch) string {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func provenanceOf(t testing.TB, e *env, token, id, key string) (source, confidence string) {
	t.Helper()
	r := e.get("/api/v1/objects/"+id+"/provenance", token)
	wantStatus(t, r, 200)
	var prov struct {
		Attributes []struct {
			Key        string `json:"key"`
			Source     string `json:"source"`
			Confidence string `json:"confidence"`
		} `json:"attributes"`
	}
	r.json(t, &prov)
	for _, a := range prov.Attributes {
		if a.Key == key {
			return a.Source, a.Confidence
		}
	}
	t.Fatalf("no provenance for %s: %s", key, r.Body)
	return "", ""
}

func TestSubmitForcesTheSourceFromThePrincipal(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw := e.ids["sw"]

	// A person claims to be the Arista collector, observing: the server does not listen.
	b := userBatch("arista:sw-a1", func(b *sdk.Batch) {
		b.Confidence = sdk.Observed
		b.Object("x", "device").Identify(sdk.IDKey, sw).Set("description", "core switch, room 1")
		b.Objects[0].Confidence = sdk.Observed
	})
	r := e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, b))
	wantStatus(t, r, 200)
	out := r.obj(t)
	if out["source"] != "user:carol" || out["status"] != "applied" {
		t.Fatalf("outcome: %s", r.Body)
	}
	src, conf := provenanceOf(t, e, tokReader, sw, "description")
	if src != "user:carol" {
		t.Fatalf("the claim was recorded for source %q, not for the principal", src)
	}
	if conf != "declared" {
		t.Fatalf("a person's claim was recorded with confidence %q, want declared", conf)
	}

	// A collector token speaks as its bound source, whatever the body says.
	b = userBatch("user:alice", func(b *sdk.Batch) {
		b.Object("x", "device").Identify(sdk.IDKey, sw).Set("vendor", "Acme Networks")
	})
	r = e.do("POST", "/api/v1/batches", tokCollector, batchJSON(t, b))
	wantStatus(t, r, 200)
	if got := r.obj(t)["source"]; got != "arista:sw-a1" {
		t.Fatalf("outcome source %v", got)
	}
	if src, _ := provenanceOf(t, e, tokReader, sw, "vendor"); src != "arista:sw-a1" {
		t.Fatalf("vendor claimed by %q", src)
	}
	// No claim was ever made in the name the clients asked for.
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM claims WHERE source = 'user:alice'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("claims for the spoofed source: %d %v", n, err)
	}

	// The audit trail names the principal and what was claimed.
	var last api.AuditEntry
	for _, a := range e.aud.Entries() {
		if a.Action == "batch.submit" {
			last = a
		}
	}
	if last.Actor != "collector-sw-a1" || last.Target != "arista:sw-a1" || last.Outcome != api.OutcomeOK || last.Details["claimed_source"] != "user:alice" {
		t.Fatalf("audit entry: %+v", last)
	}
}

func TestPersonsConfidenceIsDeclaredAndDoesNotOutrankObservation(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw := e.ids["sw"]
	// carol says the switch runs 4.30; the switch itself reports 4.20.7M.
	b := userBatch("", func(b *sdk.Batch) {
		b.Confidence = sdk.Observed
		o := b.Object("x", "device").Identify(sdk.IDKey, sw).Set("os_version", "4.30.0F")
		o.Confidence = sdk.Observed
	})
	wantStatus(t, e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, b)), 200)
	r := e.get("/api/v1/objects/"+sw, tokReader)
	if v := r.obj(t)["attributes"].(map[string]any)["os_version"]; v != "4.20.7M" {
		t.Fatalf("a person's claim displaced the collector's observation: os_version %v", v)
	}
	p := e.page("/api/v1/discrepancies?status=open&kind=value_mismatch&object="+sw, tokReader)
	if p.Total != 1 {
		t.Fatalf("the gap between declared and observed was not flagged: %d", p.Total)
	}
}

func TestSubmitRefusals(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw := e.ids["sw"]
	post := func(token, body string) resp { return e.do("POST", "/api/v1/batches", token, body) }
	good := batchJSON(t, userBatch("", func(b *sdk.Batch) { b.Object("x", "device").Identify(sdk.IDKey, sw).Set("description", "d") }))
	changesBefore := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokReader).Total

	// A token bound to no source cannot speak at all.
	wantProblem(t, post(tokNoSource, good), 403, "forbidden")

	// A secret is never written.
	secret := batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("x", "device").Identify(sdk.IDKey, sw).Set("snmp_community", "public-xyz")
	}))
	p := wantProblem(t, post(tokProposer, secret), 400, "secret-attribute")
	if strings.Contains(p.Detail, "public-xyz") {
		t.Fatalf("the problem echoes the secret: %+v", p)
	}
	// ... nor is its name accepted as an identity.
	wantProblem(t, post(tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("x", "device").Identify("password", "x").Set("name", "n")
	}))), 400, "secret-attribute")

	// Unknown type, bad value, bad JSON.
	wantProblem(t, post(tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) { b.Object("x", "starship").Identify("name", "n") }))), 400, "invalid-batch")
	wantProblem(t, post(tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("x", "device").Set("role", "server") // no identity: nothing to match or create it by
	}))), 400, "invalid-batch")
	wantProblem(t, post(tokProposer, `{"source": `), 400, "invalid-request")
	wantProblem(t, post(tokProposer, ``), 400, "invalid-request")
	wantProblem(t, post(tokProposer, good+` {}`), 400, "invalid-request")
	wantProblem(t, post(tokProposer, `{"scope":"partial","objects":[],"relations":[],"surprise":1}`), 400, "invalid-request")
	wantProblem(t, post(tokProposer, `{"scope":"partial","objects":[null],"relations":[]}`), 400, "invalid-request")
	wantProblem(t, post(tokProposer, `{"scope":"partial","objects":[],"relations":[null]}`), 400, "invalid-request")
	// An id that is not a UUID.
	wantProblem(t, post(tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) { b.Object("x", "device").Identify(sdk.IDKey, "nope").Set("description", "d") }))), 400, "invalid-request")
	// A reason too long.
	wantProblem(t, e.do("POST", "/api/v1/batches?reason="+strings.Repeat("a", 501), tokProposer, good), 400, "invalid-request")

	// Too large.
	big := `{"scope":"partial","objects":[],"relations":[],"pad":"` + strings.Repeat("a", 9<<20) + `"}`
	wantProblem(t, post(tokProposer, big), 413, "too-large")

	// Nothing above changed the inventory.
	if n := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokReader).Total; n != changesBefore {
		t.Fatalf("refused batches changed the feed: %d changes, %d before", n, changesBefore)
	}
	// A value the schema rejects is refused for an API caller (ADR 0005): 422,
	// and nothing of the batch applies.
	rej := post(tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("x", "device").Identify("name", "srv-02").Set("role", "starship")
	})))
	wantProblem(t, rej, 422, "rejected")
	if n := e.page("/api/v1/changes?from=2026-01-01T00:00:00Z", tokReader).Total; n != changesBefore {
		t.Fatalf("a rejected batch changed the feed: %d changes, %d before", n, changesBefore)
	}
}

func TestSubmitAppliesAndReportsWhatChanged(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := userBatch("", func(b *sdk.Batch) {
		b.Object("n", "device").Identify("name", "srv-02").Set("role", "server").Set("primary_ip", "198.51.100.22")
	})
	r := e.do("POST", "/api/v1/batches?reason=new+server", tokProposer, batchJSON(t, b))
	wantStatus(t, r, 200)
	var out struct {
		Source string `json:"source"`
		Status string `json:"status"`
		Result struct {
			Created int               `json:"created"`
			Objects map[string]string `json:"objects"`
		} `json:"result"`
	}
	r.json(t, &out)
	id := out.Result.Objects["n"]
	if out.Status != "applied" || out.Result.Created != 1 || id == "" {
		t.Fatalf("outcome: %s", r.Body)
	}
	g := e.get("/api/v1/objects/"+id, tokReader)
	wantStatus(t, g, 200)
	if g.obj(t)["display"] != "srv-02" {
		t.Fatalf("created object: %s", g.Body)
	}
}

func TestPreviewChangesNothing(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	before := e.page("/api/v1/objects", tokReader).Total
	b := userBatch("", func(b *sdk.Batch) {
		b.Object("n", "device").Identify("name", "srv-02").Set("role", "server")
	})
	r := e.do("POST", "/api/v1/batches/preview", tokProposer, batchJSON(t, b))
	wantStatus(t, r, 200)
	out := r.obj(t)
	res := out["result"].(map[string]any)
	if out["source"] != "user:carol" || res["created"].(float64) != 1 {
		t.Fatalf("preview: %s", r.Body)
	}
	if after := e.page("/api/v1/objects", tokReader).Total; after != before {
		t.Fatalf("a preview created objects: %d -> %d", before, after)
	}
	if n := e.page("/api/v1/proposals", tokReader).Total; n != 0 {
		t.Fatalf("a preview queued a proposal")
	}
	// Preview is not audited (it changes nothing); it needs propose and read.
	for _, a := range e.aud.Entries() {
		if strings.Contains(a.Action, "preview") {
			t.Fatalf("preview was audited: %+v", a)
		}
	}
	wantProblem(t, e.do("POST", "/api/v1/batches/preview", tokProposeNR, batchJSON(t, b)), 403, "forbidden")
	wantProblem(t, e.do("POST", "/api/v1/batches/preview", tokProposer, `{`), 400, "invalid-request")
	wantProblem(t, e.do("POST", "/api/v1/batches/preview", tokNoSource, batchJSON(t, b)), 403, "forbidden")
}

func TestProposalFlow(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	b := userBatch("", func(b *sdk.Batch) {
		b.Object("n", "device").Identify("name", "srv-02").Set("role", "server")
	})
	r := e.do("POST", "/api/v1/batches?reason=document+srv-02", tokMCP, batchJSON(t, b))
	wantStatus(t, r, 202)
	var sub struct {
		Source string `json:"source"`
		Status string `json:"status"`
		Result struct {
			ProposalID int64 `json:"proposal_id"`
			Created    int   `json:"created"`
		} `json:"result"`
	}
	r.json(t, &sub)
	if sub.Status != "proposed" || sub.Source != "mcp:assistant" || sub.Result.ProposalID == 0 || sub.Result.Created != 1 {
		t.Fatalf("submit outcome: %s", r.Body)
	}
	pid := itoa(sub.Result.ProposalID)
	if n := e.page("/api/v1/objects?type=device", tokReader).Total; n != 2 {
		t.Fatalf("a proposal was applied before approval: %d devices", n)
	}

	// Listing and reading.
	list := e.page("/api/v1/proposals?status=pending", tokReader)
	if list.Total != 1 || field(t, list.Items[0], "reason") != "document srv-02" {
		t.Fatalf("pending proposals: %s", list.Items)
	}
	if e.page("/api/v1/proposals?status=applied", tokReader).Total != 0 {
		t.Fatal("applied proposals listed")
	}
	g := e.get("/api/v1/proposals/"+pid, tokReader)
	wantStatus(t, g, 200)
	gp := g.obj(t)
	if gp["status"] != "pending" || gp["source"] != "mcp:assistant" || gp["preview"] == nil {
		t.Fatalf("proposal: %s", g.Body)
	}

	// Approval needs the approve action.
	for _, tok := range []string{tokReader, tokProposer, tokMCP} {
		wantProblem(t, e.do("POST", "/api/v1/proposals/"+pid+"/approve", tok, nil), 403, "forbidden")
		wantProblem(t, e.do("POST", "/api/v1/proposals/"+pid+"/reject", tok, nil), 403, "forbidden")
	}
	if e.get("/api/v1/proposals/"+pid, tokReader).obj(t)["status"] != "pending" {
		t.Fatal("a refused approval changed the proposal")
	}

	ap := e.do("POST", "/api/v1/proposals/"+pid+"/approve", tokApprover, nil)
	wantStatus(t, ap, 200)
	if o := ap.obj(t); o["status"] != "applied" || o["result"].(map[string]any)["created"].(float64) != 1 {
		t.Fatalf("approve: %s", ap.Body)
	}
	if n := e.page("/api/v1/objects?type=device", tokReader).Total; n != 3 {
		t.Fatalf("approved proposal not applied: %d devices", n)
	}
	done := e.get("/api/v1/proposals/"+pid, tokReader).obj(t)
	if done["status"] != "applied" || done["decided_by"] != "dave" {
		t.Fatalf("decided proposal: %v", done)
	}
	// A second decision is a conflict, whichever way.
	wantProblem(t, e.do("POST", "/api/v1/proposals/"+pid+"/approve", tokApprover, nil), 409, "conflict")
	wantProblem(t, e.do("POST", "/api/v1/proposals/"+pid+"/reject", tokApprover, nil), 409, "conflict")

	// Rejection.
	b2 := userBatch("", func(b *sdk.Batch) { b.Object("n", "device").Identify("name", "srv-03").Set("role", "server") })
	r2 := e.do("POST", "/api/v1/batches", tokMCP, batchJSON(t, b2))
	wantStatus(t, r2, 202)
	pid2 := itoa(int64(r2.obj(t)["result"].(map[string]any)["proposal_id"].(float64)))
	rj := e.do("POST", "/api/v1/proposals/"+pid2+"/reject", tokApprover, nil)
	wantStatus(t, rj, 200)
	if rj.obj(t)["status"] != "rejected" {
		t.Fatalf("reject: %s", rj.Body)
	}
	if n := e.page("/api/v1/objects?type=device", tokReader).Total; n != 3 {
		t.Fatalf("a rejected proposal was applied: %d devices", n)
	}
	if e.page("/api/v1/proposals?status=rejected", tokReader).Total != 1 {
		t.Fatal("rejected proposal not listed")
	}
	wantProblem(t, e.do("POST", "/api/v1/proposals/"+pid2+"/approve", tokApprover, nil), 409, "conflict")

	// Unknown, malformed, bad filter.
	wantProblem(t, e.get("/api/v1/proposals/999999", tokReader), 404, "not-found")
	wantProblem(t, e.do("POST", "/api/v1/proposals/999999/approve", tokApprover, nil), 404, "not-found")
	wantProblem(t, e.do("POST", "/api/v1/proposals/999999/reject", tokApprover, nil), 404, "not-found")
	wantProblem(t, e.get("/api/v1/proposals/abc", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/proposals?status=maybe", tokReader), 400, "invalid-request")

	// Audit: submissions, approvals and rejections are all on record.
	seen := map[string]int{}
	for _, a := range e.aud.Entries() {
		if a.Outcome == api.OutcomeOK {
			seen[a.Action]++
		}
	}
	if seen["batch.submit"] != 2 || seen["proposal.approve"] != 1 || seen["proposal.reject"] != 1 {
		t.Fatalf("audited successes: %v", seen)
	}
}

func itoa(n int64) string { return strings.TrimSpace(strings.Join([]string{jsonNum(n)}, "")) }

func jsonNum(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestDiscrepancies(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sw, srv := e.ids["sw"], e.ids["srv"]
	for _, id := range []string{sw, srv} {
		b := userBatch("", func(b *sdk.Batch) { b.Object("x", "device").Identify(sdk.IDKey, id).Set("os_version", "9.9") })
		wantStatus(t, e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, b)), 200)
	}
	all := e.page("/api/v1/discrepancies?status=open", tokReader)
	if all.Total != 2 {
		t.Fatalf("open discrepancies: %d: %s", all.Total, all.Items)
	}
	one := e.page("/api/v1/discrepancies?status=open&kind=value_mismatch&object="+sw, tokReader)
	if one.Total != 1 || field(t, one.Items[0], "object_id") != sw || field(t, one.Items[0], "key") != "os_version" {
		t.Fatalf("filtered: %s", one.Items)
	}
	if e.page("/api/v1/discrepancies?kind=unresolved_neighbor", tokReader).Total != 0 {
		t.Fatal("kind filter ignored")
	}
	if e.page("/api/v1/discrepancies?limit=1", tokReader).Total != 2 {
		t.Fatal("total must count the whole result, not the page")
	}
	did := int64(0)
	var d struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(one.Items[0], &d)
	did = d.ID

	for _, tok := range []string{tokReader, tokProposer, tokMCP} {
		wantProblem(t, e.do("POST", "/api/v1/discrepancies/"+itoa(did)+"/accept", tok, nil), 403, "forbidden")
	}
	r := e.do("POST", "/api/v1/discrepancies/"+itoa(did)+"/accept", tokApprover, `{"note":"expected: pending upgrade"}`)
	wantStatus(t, r, 200)
	if r.obj(t)["status"] != "accepted" {
		t.Fatalf("accept: %s", r.Body)
	}
	if e.page("/api/v1/discrepancies?status=open", tokReader).Total != 1 {
		t.Fatal("accepted discrepancy still open")
	}
	acc := e.page("/api/v1/discrepancies?status=accepted", tokReader)
	var ad struct {
		ClosedBy string `json:"closed_by"`
		Note     string `json:"note"`
	}
	_ = json.Unmarshal(acc.Items[0], &ad)
	if acc.Total != 1 || ad.ClosedBy != "dave" {
		t.Fatalf("accepted: %s", acc.Items)
	}
	// Accepting twice, or something that does not exist.
	wantProblem(t, e.do("POST", "/api/v1/discrepancies/"+itoa(did)+"/accept", tokApprover, nil), 404, "not-found")
	wantProblem(t, e.do("POST", "/api/v1/discrepancies/987654/accept", tokApprover, nil), 404, "not-found")
	wantProblem(t, e.do("POST", "/api/v1/discrepancies/abc/accept", tokApprover, nil), 400, "invalid-request")
	wantProblem(t, e.do("POST", "/api/v1/discrepancies/1/accept", tokApprover, `{"note": 3}`), 400, "invalid-request")
	wantProblem(t, e.do("POST", "/api/v1/discrepancies/1/accept", tokApprover, `{"surprise": 3}`), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/discrepancies?status=bogus", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/discrepancies?object=zzz", tokReader), 400, "invalid-request")
}

func TestSavedQueries(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	create := func(tok, body string) resp { return e.do("POST", "/api/v1/saved-queries", tok, body) }

	r := create(tokProposer, `{"name":"switches","query":"type:device role:switch","description":"all switches"}`)
	wantStatus(t, r, 201)
	q := r.obj(t)
	if q["name"] != "switches" || q["owner"] != "carol" || q["query"] != "type:device role:switch" {
		t.Fatalf("created: %s", r.Body)
	}
	// The owner is the caller; a client cannot choose it.
	wantProblem(t, create(tokProposer, `{"name":"x","query":"role:switch","owner":"alice"}`), 400, "invalid-request")
	// Names are not overwritten silently.
	wantProblem(t, create(tokAdmin, `{"name":"switches","query":"role:server"}`), 409, "conflict")
	if got := e.get("/api/v1/saved-queries/switches", tokReader).obj(t)["query"]; got != "type:device role:switch" {
		t.Fatalf("the existing query was replaced: %v", got)
	}
	// Validation.
	wantProblem(t, create(tokProposer, `{"name":"bad","query":"role:("}`), 400, "invalid-query")
	wantProblem(t, create(tokProposer, `{"name":"","query":"role:switch"}`), 400, "invalid-request")
	wantProblem(t, create(tokProposer, `{"name":"../etc","query":"role:switch"}`), 400, "invalid-request")
	wantProblem(t, create(tokProposer, `{"name":"leaky","query":"snmp_community:public-xyz"}`), 403, "sensitive-attribute")
	wantProblem(t, create(tokReader, `{"name":"ro","query":"role:switch"}`), 403, "forbidden")

	// Read.
	if p := e.page("/api/v1/saved-queries", tokReader); p.Total != 1 {
		t.Fatalf("list: %d", p.Total)
	}
	wantStatus(t, e.get("/api/v1/saved-queries/switches", tokReader), 200)
	wantProblem(t, e.get("/api/v1/saved-queries/nothing", tokReader), 404, "not-found")

	// Delete: the owner, or an administrator; nobody else.
	wantProblem(t, e.do("DELETE", "/api/v1/saved-queries/switches", tokReader, nil), 403, "forbidden")
	wantProblem(t, e.do("DELETE", "/api/v1/saved-queries/switches", tokMCP, nil), 403, "forbidden")
	other := e.auth.Tokens
	other["t-propose2"] = api.Grant{Principal: api.Principal{Subject: "ivan", Kind: api.KindUser}, Actions: []string{api.ActionRead, api.ActionPropose}}
	wantProblem(t, e.do("DELETE", "/api/v1/saved-queries/switches", "t-propose2", nil), 403, "forbidden")
	if e.get("/api/v1/saved-queries/switches", tokReader).Status != 200 {
		t.Fatal("a refused delete deleted")
	}
	wantStatus(t, e.do("DELETE", "/api/v1/saved-queries/switches", tokProposer, nil), 204)
	wantProblem(t, e.get("/api/v1/saved-queries/switches", tokReader), 404, "not-found")
	wantProblem(t, e.do("DELETE", "/api/v1/saved-queries/switches", tokProposer, nil), 404, "not-found")

	wantStatus(t, create(tokProposer, `{"name":"mine","query":"role:server"}`), 201)
	wantStatus(t, e.do("DELETE", "/api/v1/saved-queries/mine", tokAdmin, nil), 204)
}

// failingAuditor always fails to record.
type failingAuditor struct{}

func (failingAuditor) Record(context.Context, api.AuditEntry) error {
	return errors.New("audit store down")
}

func TestAuditFailureDoesNotUndoACommittedMutation(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	srv, err := api.New(api.Config{Engine: e.eng, Authorizer: e.auth, Auditor: failingAuditor{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: e.clk.now})
	if err != nil {
		t.Fatal(err)
	}
	b := userBatch("", func(b *sdk.Batch) { b.Object("n", "device").Identify("name", "srv-02").Set("role", "server") })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/batches", strings.NewReader(batchJSON(t, b)))
	req.Header.Set("Authorization", "Bearer "+tokProposer)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if n := e.page("/api/v1/objects?type=device", tokReader).Total; n != 3 {
		t.Fatalf("devices %d", n)
	}
}

// mutating lists every mutating call: how to make it succeed, how to make it
// fail on input, and the audit action it must leave.
func TestEveryMutatingCallIsAudited(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	sw := e.ids["sw"]
	// Material: a pending proposal and an open discrepancy.
	pb := userBatch("", func(b *sdk.Batch) { b.Object("n", "device").Identify("name", "srv-02").Set("role", "server") })
	pr := e.do("POST", "/api/v1/batches", tokMCP, batchJSON(t, pb))
	wantStatus(t, pr, 202)
	pid := itoa(int64(pr.obj(t)["result"].(map[string]any)["proposal_id"].(float64)))
	pb2 := userBatch("", func(b *sdk.Batch) { b.Object("n", "device").Identify("name", "srv-03").Set("role", "server") })
	pr2 := e.do("POST", "/api/v1/batches", tokMCP, batchJSON(t, pb2))
	pid2 := itoa(int64(pr2.obj(t)["result"].(map[string]any)["proposal_id"].(float64)))
	wantStatus(t, e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("x", "device").Identify(sdk.IDKey, sw).Set("os_version", "9.9")
	}))), 200)
	disc := e.page("/api/v1/discrepancies?status=open", tokReader)
	var d struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(disc.Items[0], &d)
	e.aud = reset(e) // start counting from here

	type call struct {
		name, method, path, token, body string
		status                          int
		outcome                         string
		action                          string
		actor                           string
	}
	calls := []call{
		{"submit ok", "POST", "/api/v1/batches", tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) { b.Object("n", "device").Identify("name", "srv-09").Set("role", "server") })), 200, api.OutcomeOK, "batch.submit", "carol"},
		{"submit invalid", "POST", "/api/v1/batches", tokProposer, `{`, 400, api.OutcomeError, "batch.submit", "carol"},
		{"submit denied", "POST", "/api/v1/batches", tokReader, `{}`, 403, api.OutcomeDenied, "batch.submit", "bob"},
		{"submit unbound", "POST", "/api/v1/batches", tokNoSource, batchJSON(t, userBatch("", func(b *sdk.Batch) {})), 403, api.OutcomeDenied, "batch.submit", "orphan"},
		{"approve ok", "POST", "/api/v1/proposals/" + pid + "/approve", tokApprover, "", 200, api.OutcomeOK, "proposal.approve", "dave"},
		{"approve conflict", "POST", "/api/v1/proposals/" + pid + "/approve", tokApprover, "", 409, api.OutcomeError, "proposal.approve", "dave"},
		{"approve denied", "POST", "/api/v1/proposals/" + pid2 + "/approve", tokReader, "", 403, api.OutcomeDenied, "proposal.approve", "bob"},
		{"reject ok", "POST", "/api/v1/proposals/" + pid2 + "/reject", tokApprover, "", 200, api.OutcomeOK, "proposal.reject", "dave"},
		{"reject unknown", "POST", "/api/v1/proposals/999999/reject", tokApprover, "", 404, api.OutcomeError, "proposal.reject", "dave"},
		{"reject denied", "POST", "/api/v1/proposals/" + pid2 + "/reject", tokProposer, "", 403, api.OutcomeDenied, "proposal.reject", "carol"},
		{"accept ok", "POST", "/api/v1/discrepancies/" + itoa(d.ID) + "/accept", tokApprover, "", 200, api.OutcomeOK, "discrepancy.accept", "dave"},
		{"accept unknown", "POST", "/api/v1/discrepancies/987654/accept", tokApprover, "", 404, api.OutcomeError, "discrepancy.accept", "dave"},
		{"accept denied", "POST", "/api/v1/discrepancies/1/accept", tokReader, "", 403, api.OutcomeDenied, "discrepancy.accept", "bob"},
		{"query create ok", "POST", "/api/v1/saved-queries", tokProposer, `{"name":"q1","query":"role:switch"}`, 201, api.OutcomeOK, "query.create", "carol"},
		{"query create invalid", "POST", "/api/v1/saved-queries", tokProposer, `{"name":"q2","query":"role:("}`, 400, api.OutcomeError, "query.create", "carol"},
		{"query create denied", "POST", "/api/v1/saved-queries", tokReader, `{"name":"q3","query":"role:switch"}`, 403, api.OutcomeDenied, "query.create", "bob"},
		{"query delete denied", "DELETE", "/api/v1/saved-queries/q1", tokReader, "", 403, api.OutcomeDenied, "query.delete", "bob"},
		{"query delete ok", "DELETE", "/api/v1/saved-queries/q1", tokProposer, "", 204, api.OutcomeOK, "query.delete", "carol"},
		{"query delete unknown", "DELETE", "/api/v1/saved-queries/q1", tokProposer, "", 404, api.OutcomeError, "query.delete", "carol"},
	}
	for _, c := range calls {
		var body any
		if c.body != "" {
			body = c.body
		}
		before := len(e.aud.Entries())
		r := e.do(c.method, c.path, c.token, body)
		if r.Status != c.status {
			t.Fatalf("%s: status %d, want %d: %s", c.name, r.Status, c.status, r.Body)
		}
		entries := e.aud.Entries()
		if len(entries) != before+1 {
			t.Fatalf("%s: %d audit entries recorded, want exactly 1", c.name, len(entries)-before)
		}
		got := entries[before]
		if got.Action != c.action || got.Outcome != c.outcome || got.Actor != c.actor || got.At.IsZero() || got.Target == "" {
			t.Fatalf("%s: audit entry %+v, want action %s outcome %s actor %s", c.name, got, c.action, c.outcome, c.actor)
		}
		if got.Details["method"] != c.method || got.Details["path"] != c.path {
			t.Fatalf("%s: audit details %+v", c.name, got.Details)
		}
		if c.status >= 400 && got.Details["status"] != float64(c.status) && got.Details["status"] != c.status {
			t.Fatalf("%s: audit status detail %v", c.name, got.Details["status"])
		}
	}
	// Reads and previews are not audited.
	n := len(e.aud.Entries())
	e.get("/api/v1/objects", tokReader)
	e.do("POST", "/api/v1/batches/preview", tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) {})))
	if len(e.aud.Entries()) != n {
		t.Fatal("reads or previews were audited")
	}
	// An unauthenticated call has no principal: nothing to attribute, nothing recorded.
	e.do("POST", "/api/v1/batches", "", `{}`)
	if len(e.aud.Entries()) != n {
		t.Fatal("an unauthenticated call was audited as if it had an actor")
	}
	_ = time.Second
}

// reset swaps the env's auditor for a fresh one used by a new server and returns it.
func reset(e *env) *api.MemoryAuditor {
	aud := &api.MemoryAuditor{}
	srv, err := api.New(api.Config{Engine: e.eng, Authorizer: e.auth, Auditor: aud, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test", Now: e.clk.now})
	if err != nil {
		e.t.Fatal(err)
	}
	e.srv, e.h = srv, srv.Handler()
	return aud
}
