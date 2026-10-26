package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// renameBody is what the console sends to rename a place: the object designated by id, one new name.
func (e *env) renameBody(typ, id, name string) map[string]any {
	return map[string]any{
		"source": "ui", "scope": "partial", "observed_at": e.clk.now().Format(time.RFC3339), "confidence": "declared",
		"objects":   []any{map[string]any{"ref": "obj", "type": typ, "identity": map[string]any{"id": id}, "attributes": map[string]any{"name": name}}},
		"relations": []any{},
	}
}

// A person renames a rack: same object, the history keeps the old name, the provenance says who gave
// the name and who else still says the old one; a name a sibling holds is a 409 in the preview and in
// the submission, and nothing changes. ADR 0067.
func TestRenameAPlace(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := sdk.NewBatch("declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("paris", "site").Identify(sdk.IDKey, e.ids["paris"])
	b.Object("rack-c", "rack").Identify("name", "rack-c").In("paris")
	e.apply(b)
	e.clk.advance(time.Minute)

	// Taken by a sibling.
	for _, path := range []string{"/api/v1/batches/preview", "/api/v1/batches"} {
		p := wantProblem(t, e.do("POST", path, tokProposer, e.renameBody("rack", e.ids["rack-p"], "RACK-C")), 409, "name-taken")
		if !strings.Contains(p.Detail, `"RACK-C"`) || !strings.Contains(p.Detail, "declaration:test") {
			t.Fatalf("%s: detail %q does not say which name is taken and by whom", path, p.Detail)
		}
	}
	if r := e.get("/api/v1/objects/"+e.ids["rack-p"], tokReader).obj(t); r["display"] != "rack-a" {
		t.Fatalf("a refused rename changed the rack: %+v", r)
	}

	// A free name.
	wantStatus(t, e.do("POST", "/api/v1/batches", tokProposer, e.renameBody("rack", e.ids["rack-p"], "rack-z")), 200)
	r := e.get("/api/v1/objects/"+e.ids["rack-p"], tokReader).obj(t)
	if r["display"] != "rack-z" || r["id"] != e.ids["rack-p"] {
		t.Fatalf("renamed rack %+v", r)
	}
	if n := e.page("/api/v1/objects?type=rack", tokReader).Total; n != 3 {
		t.Fatalf("%d racks after the rename, want 3 (no new object)", n)
	}
	var prov struct {
		Attributes []struct {
			Key         string `json:"key"`
			Value       any    `json:"value"`
			Source      string `json:"source"`
			OtherClaims []struct {
				Source string `json:"source"`
				Value  any    `json:"value"`
			} `json:"other_claims"`
		} `json:"attributes"`
	}
	e.get("/api/v1/objects/"+e.ids["rack-p"]+"/provenance", tokReader).json(t, &prov)
	found := false
	for _, a := range prov.Attributes {
		if a.Key != "name" {
			continue
		}
		found = true
		if a.Value != "rack-z" || a.Source != "user:carol" || len(a.OtherClaims) != 1 ||
			a.OtherClaims[0].Source != "declaration:test" || a.OtherClaims[0].Value != "rack-a" {
			t.Fatalf("provenance of the name: %+v", a)
		}
	}
	if !found {
		t.Fatal("no provenance for the name")
	}
	// The old name is in the history.
	hist := e.page("/api/v1/objects/"+e.ids["rack-p"]+"/history?key=name", tokReader)
	if hist.Total < 2 {
		t.Fatalf("name history has %d entries, want the old and the new name", hist.Total)
	}
}
