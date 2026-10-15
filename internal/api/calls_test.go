package api_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// callSpec is one concrete request for an operation of the API.
type callSpec struct{ method, path, body string }

// withMaterial queues a proposal (from an mcp:* source) and opens a
// discrepancy, and returns their ids: the material the id-taking operations
// need to be called for real.
func (e *env) withMaterial(t testing.TB) (proposalID, discrepancyID int64) {
	t.Helper()
	b := userBatch("", func(b *sdk.Batch) { b.Object("n", "device").Identify("name", "srv-02").Set("role", "server") })
	r := e.do("POST", "/api/v1/batches", tokMCP, batchJSON(t, b))
	wantStatus(t, r, 202)
	var sub struct {
		Result struct {
			ProposalID int64 `json:"proposal_id"`
		} `json:"result"`
	}
	r.json(t, &sub)
	b = userBatch("", func(b *sdk.Batch) { b.Object("x", "device").Identify(sdk.IDKey, e.ids["sw"]).Set("os_version", "9.9") })
	wantStatus(t, e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, b)), 200)
	d := e.page("/api/v1/discrepancies?status=open", tokReader)
	if d.Total < 1 {
		t.Fatal("no discrepancy was opened")
	}
	var first struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(d.Items[0], &first); err != nil {
		t.Fatal(err)
	}
	return sub.Result.ProposalID, first.ID
}

// calls maps every operationId of the API to a request that reaches it with
// valid parameters. Tests that walk the OpenAPI document use it, and fail on
// an operation that has no entry.
func (e *env) calls(t testing.TB, proposalID, discrepancyID int64) map[string]callSpec {
	sw := e.ids["sw"]
	pid, did := strconv.FormatInt(proposalID, 10), strconv.FormatInt(discrepancyID, 10)
	newBatch := func(name string) string {
		return batchJSON(t, userBatch("", func(b *sdk.Batch) {
			b.Object("n", "device").Identify("name", name).Set("role", "server")
		}))
	}
	return map[string]callSpec{
		"getHealthz":          {"GET", "/api/v1/healthz", ""},
		"getReadyz":           {"GET", "/api/v1/readyz", ""},
		"getOpenAPI":          {"GET", "/api/v1/openapi.json", ""},
		"getCollectionHealth": {"GET", "/api/v1/health/collection", ""},
		"getMe":               {"GET", "/api/v1/me", ""},
		"getSchema":           {"GET", "/api/v1/schema", ""},
		"getSchemaType":       {"GET", "/api/v1/schema/types/device", ""},
		"listObjects":         {"GET", "/api/v1/objects?type=device", ""},
		"listRelations":       {"GET", "/api/v1/relations?type=cabled_to", ""},
		"getObject":           {"GET", "/api/v1/objects/" + sw, ""},
		"getObjectProvenance": {"GET", "/api/v1/objects/" + sw + "/provenance", ""},
		"getObjectHistory":    {"GET", "/api/v1/objects/" + sw + "/history?key=os_version", ""},
		"getObjectRelations":  {"GET", "/api/v1/objects/" + sw + "/relations", ""},
		"getObjectTimeline":   {"GET", "/api/v1/objects/" + sw + "/timeline", ""},
		"getRackContents":     {"GET", "/api/v1/racks/" + e.ids["rack-p"] + "/contents", ""},
		"search":              {"GET", "/api/v1/search?q=sw-a1", ""},
		"whoHad":              {"GET", "/api/v1/whohad?key=mgmt_ip&value=192.0.2.10", ""},
		"listChanges":         {"GET", "/api/v1/changes?from=2026-01-01T00:00:00Z", ""},
		"submitBatch":         {"POST", "/api/v1/batches", newBatch("srv-10")},
		"previewBatch":        {"POST", "/api/v1/batches/preview", newBatch("srv-11")},
		"listProposals":       {"GET", "/api/v1/proposals", ""},
		"getProposal":         {"GET", "/api/v1/proposals/" + pid, ""},
		"approveProposal":     {"POST", "/api/v1/proposals/" + pid + "/approve", ""},
		"rejectProposal":      {"POST", "/api/v1/proposals/" + pid + "/reject", ""},
		"listDiscrepancies":   {"GET", "/api/v1/discrepancies", ""},
		"acceptDiscrepancy":   {"POST", "/api/v1/discrepancies/" + did + "/accept", ""},
		"listSavedQueries":    {"GET", "/api/v1/saved-queries", ""},
		"createSavedQuery":    {"POST", "/api/v1/saved-queries", `{"name":"sw","query":"role:switch"}`},
		"getSavedQuery":       {"GET", "/api/v1/saved-queries/sw", ""},
		"deleteSavedQuery":    {"DELETE", "/api/v1/saved-queries/sw", ""},
	}
}

// grantOnly and grantAllBut add principals to the authorizer: one that holds a
// single action, one that holds every action but one.
func (e *env) grantOnly(action string) string {
	tok := "t-only-" + action
	e.auth.Tokens[tok] = api.Grant{Principal: api.Principal{Subject: "only-" + action, Kind: api.KindUser}, Actions: []string{action}}
	return tok
}

func (e *env) grantAllBut(action string) string {
	var acts []string
	for _, a := range []string{api.ActionRead, api.ActionPropose, api.ActionApprove, api.ActionAdmin} {
		if a != action {
			acts = append(acts, a)
		}
	}
	tok := "t-but-" + action
	e.auth.Tokens[tok] = api.Grant{Principal: api.Principal{Subject: "but-" + action, Kind: api.KindUser}, Actions: acts}
	return tok
}
