package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
)

// /me tells a UI what the caller holds, from the same Authorizer that gates
// every endpoint: it is the only source the UI has to decide which controls
// to offer.
func TestMeReportsTheCallersActions(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	for _, tc := range []struct {
		name, token, subject, kind, source string
		actions                            []string
	}{
		{"reader", tokReader, "bob", "user", "", []string{"read"}},
		{"proposer", tokProposer, "carol", "user", "", []string{"read", "propose"}},
		{"approver", tokApprover, "dave", "user", "", []string{"read", "approve"}},
		{"admin", tokAdmin, "alice", "user", "", []string{"read", "propose", "approve", "admin"}},
		{"machine token", tokMCP, "mcp-assistant", "token", "mcp:assistant", []string{"read", "propose"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.get("/api/v1/me", tc.token)
			wantStatus(t, r, 200)
			var got api.Me
			r.json(t, &got)
			if got.Subject != tc.subject || string(got.Kind) != tc.kind || got.Source != tc.source || !reflect.DeepEqual(got.Actions, tc.actions) {
				t.Fatalf("got %+v, want subject=%s kind=%s source=%q actions=%v", got, tc.subject, tc.kind, tc.source, tc.actions)
			}
		})
	}
	// Without a credential, or without the read action, there is no answer.
	wantProblem(t, e.get("/api/v1/me", ""), 401, "unauthenticated")
	wantProblem(t, e.get("/api/v1/me", tokProposeNR), 403, "forbidden")
}

// web/openapi.json is the snapshot the web client's types are generated from
// (web/src/api/schema.gen.ts). The server generates its document from the
// route table and the registry: when either changes, the snapshot must too,
// or the UI would be compiled against an API that no longer exists.
// Regenerate with TRICA_UPDATE_OPENAPI=1 go test ./internal/api -run OpenAPISnapshot,
// then `npm run gen:api` in web/.
func TestWebOpenAPISnapshotIsCurrent(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	raw, _ := openAPIDoc(t, e)
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte('\n')
	const path = "../../web/openapi.json"
	if os.Getenv("TRICA_UPDATE_OPENAPI") == "1" {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: generate it with TRICA_UPDATE_OPENAPI=1", err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		t.Fatal("web/openapi.json is stale: the API changed. Run TRICA_UPDATE_OPENAPI=1 go test ./internal/api -run WebOpenAPISnapshot, then `npm run gen:api` in web/ and commit both")
	}
}
