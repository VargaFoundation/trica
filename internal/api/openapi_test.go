package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	"github.com/pb33f/libopenapi-validator/schema_validation"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// openAPIDoc fetches the document as a client would.
func openAPIDoc(t testing.TB, e *env) (raw []byte, doc map[string]any) {
	t.Helper()
	r := e.get("/api/v1/openapi.json", tokReader)
	wantStatus(t, r, 200)
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
	r.json(t, &doc)
	return r.Body, doc
}

func newDocValidator(t testing.TB, raw []byte) validator.Validator {
	t.Helper()
	d, err := libopenapi.NewDocument(raw)
	if err != nil {
		t.Fatalf("the generated document does not even parse: %v", err)
	}
	v, errs := validator.NewValidator(d)
	if len(errs) > 0 {
		t.Fatalf("validator: %v", errs)
	}
	return v
}

// pathsOf walks doc.paths: "METHOD /path" -> operation.
func pathsOf(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for p, item := range doc["paths"].(map[string]any) {
		for m, op := range item.(map[string]any) {
			out[strings.ToUpper(m)+" "+p] = op.(map[string]any)
		}
	}
	return out
}

func TestOpenAPIDocumentIsValidOpenAPI31(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	raw, doc := openAPIDoc(t, e)
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi %v", doc["openapi"])
	}
	d, err := libopenapi.NewDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	if v := d.GetSpecInfo().Version; v != "3.1.0" {
		t.Fatalf("the validator library reads version %q", v)
	}
	ok, errs := schema_validation.ValidateOpenAPIDocument(d)
	for _, ve := range errs {
		t.Errorf("invalid OpenAPI: %s: %s", ve.Message, ve.Reason)
	}
	if !ok {
		t.Fatal("the document is not valid OpenAPI 3.1")
	}
	// A broken document is caught by the same call: the check is not vacuous.
	broken := strings.Replace(string(raw), `"openapi":"3.1.0"`, `"openapi":"3.1.0","paths_typo":1`, 1)
	bd, err := libopenapi.NewDocument([]byte(strings.Replace(broken, `"operationId":"getHealthz"`, `"operationId":"getHealthz","responses":42`, 1)))
	if err == nil {
		if ok, _ := schema_validation.ValidateOpenAPIDocument(bd); ok {
			t.Fatal("the validator accepted a document with a malformed operation")
		}
	}
}

func TestOpenAPIAndRouteTableAgree(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	_, doc := openAPIDoc(t, e)
	ops := pathsOf(doc)

	var table []string
	table = append(table, e.srv.RouteKeys()...)
	var documented []string
	for k := range ops {
		documented = append(documented, k)
	}
	sort.Strings(table)
	sort.Strings(documented)
	if !slices.Equal(table, documented) {
		t.Fatalf("route table and document differ:\n table:      %v\n documented: %v", table, documented)
	}

	seen := map[string]string{}
	for key, op := range ops {
		id, _ := op["operationId"].(string)
		if id == "" {
			t.Errorf("%s: no operationId", key)
		}
		if prev, dup := seen[id]; dup {
			t.Errorf("operationId %s used by %s and %s", id, prev, key)
		}
		seen[id] = key
		if s, _ := op["summary"].(string); s == "" {
			t.Errorf("%s: no summary", key)
		}
		responses := op["responses"].(map[string]any)
		if _, ok := responses["500"]; !ok {
			t.Errorf("%s: 500 not documented", key)
		}
		action, _ := op["x-trica-action"].(string)
		switch action {
		case "public":
			if sec, ok := op["security"].([]any); !ok || len(sec) != 0 {
				t.Errorf("%s: a public operation must say security: []", key)
			}
		case "read", "propose", "approve", "admin":
			for _, st := range []string{"401", "403"} {
				if _, ok := responses[st]; !ok {
					t.Errorf("%s: %s not documented for a protected operation", key, st)
				}
			}
		default:
			t.Errorf("%s: x-trica-action %q", key, action)
		}
		isWrite := strings.HasPrefix(key, "POST ") || strings.HasPrefix(key, "DELETE ")
		audited, _ := op["x-trica-audited"].(bool)
		preview := key == "POST /batches/preview"
		if isWrite && !audited && !preview {
			t.Errorf("%s: a mutating operation that is not marked audited", key)
		}
		if !isWrite && audited {
			t.Errorf("%s: a read marked audited", key)
		}
	}
	// Every operation the document promises is really served.
	pid, did := e.withMaterial(t)
	calls := e.calls(t, pid, did)
	for id, key := range seen {
		c, ok := calls[id]
		if !ok {
			t.Errorf("operation %s has no test request", id)
			continue
		}
		r := e.do(c.method, c.path, tokAdmin, nilIfEmpty(c.body))
		if r.Status == 405 || (r.Status == 404 && !strings.Contains(string(r.Body), "no ")) {
			t.Errorf("%s (%s) is documented but not served as documented: %d %s", id, key, r.Status, r.Body)
		}
		documentedStatuses := ops[key]["responses"].(map[string]any)
		if _, ok := documentedStatuses[itoa(int64(r.Status))]; !ok {
			t.Errorf("%s answered %d, which its document entry does not list", id, r.Status)
		}
	}
}

// Real answers conform to the document, and so do the requests it describes.
func TestRealTrafficConformsToTheDocument(t *testing.T) {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	pid, did := e.withMaterial(t)
	calls := e.calls(t, pid, did)
	raw, doc := openAPIDoc(t, e)
	v := newDocValidator(t, raw)
	ops := pathsOf(doc)

	check := func(name, method, path, token, body string, invalidOnPurpose bool) int {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rd)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		// The validator reads the request body: give it a copy.
		vreq := req.Clone(req.Context())
		if body != "" {
			vreq.Body = io.NopCloser(strings.NewReader(body))
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		resp := rec.Result()
		if ok, errs := v.ValidateHttpRequest(vreq); !ok && !invalidOnPurpose {
			for _, ve := range errs {
				t.Errorf("%s: request %s %s breaks the document: %s: %s", name, method, path, ve.Message, ve.Reason)
			}
		}
		if ok, errs := v.ValidateHttpResponse(vreq, resp); !ok {
			for _, ve := range errs {
				t.Errorf("%s: answer %d to %s %s breaks the document: %s: %s\n%s", name, resp.StatusCode, method, path, ve.Message, ve.Reason, rec.Body.String())
			}
		}
		return resp.StatusCode
	}

	ids := make([]string, 0, len(calls))
	for id := range calls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := calls[id]
		if _, ok := ops[c.method+" "+strings.TrimPrefix(strings.SplitN(c.path, "?", 2)[0], "/api/v1")]; !ok && !strings.Contains(c.path, "/") {
			t.Errorf("%s: not in the document", id)
		}
		check(id, c.method, c.path, tokAdmin, c.body, false)
	}
	// Error answers conform too.
	check("401", "GET", "/api/v1/objects", "", "", true) // the request lacks its credential on purpose
	check("403", "POST", "/api/v1/proposals/1/approve", tokReader, "", false)
	check("404", "GET", "/api/v1/objects/00000000-0000-4000-8000-000000000000", tokReader, "", false)
	check("400", "GET", "/api/v1/objects?limit=0", tokReader, "", true)
	check("409", "POST", "/api/v1/proposals/"+itoa(pid)+"/approve", tokApprover, "", false)
	check("400 body", "POST", "/api/v1/batches", tokProposer, `{`, true)
}

// A type added to the registry shows up in /schema and in /openapi.json on the
// next request, and leaves with the registry: nothing is cached or generated at build time.
func TestCustomTypeAppearsWithoutRestart(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	_, before := openAPIDoc(t, e)
	schemas := func(doc map[string]any) map[string]any {
		return doc["components"].(map[string]any)["schemas"].(map[string]any)
	}
	if _, ok := schemas(before)["Object.ups_battery"]; ok {
		t.Fatal("fixture: the type is there already")
	}
	wantProblem(t, e.get("/api/v1/schema/types/ups_battery", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/objects?type=ups_battery", tokReader), 400, "invalid-request")
	wantProblem(t, e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, userBatch("", func(b *sdk.Batch) {
		b.Object("u", "ups_battery").Identify("serial", "UPS-0001")
	}))), 400, "invalid-batch")

	ov, err := schema.Parse([]byte(`
types:
  ups_battery:
    label: {en: UPS battery, fr: Batterie d'onduleur}
    parents: [rack]
    display: serial
    attributes:
      serial: {kind: string, identity: strong}
      capacity_va: {kind: int, unit: VA}
      chemistry: {kind: enum, values: [lead_acid, lithium]}
      api_token: {kind: string}
`), true)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := schema.Builtin().Merge(ov)
	if err != nil {
		t.Fatal(err)
	}
	e.reg.set(merged) // no restart: the same server, the same handler

	raw, after := openAPIDoc(t, e)
	sc := schemas(after)
	for _, name := range []string{"Object.ups_battery", "Attributes.ups_battery"} {
		if _, ok := sc[name]; !ok {
			t.Fatalf("%s missing from the document after the type was added", name)
		}
	}
	attrs := sc["Attributes.ups_battery"].(map[string]any)["properties"].(map[string]any)
	if _, ok := attrs["api_token"]; ok {
		t.Fatal("a sensitive attribute is documented")
	}
	if a := attrs["capacity_va"].(map[string]any); a["type"] != "integer" || a["x-trica-unit"] != "VA" {
		t.Fatalf("capacity_va: %v", a)
	}
	if a := attrs["chemistry"].(map[string]any); len(a["enum"].([]any)) != 2 {
		t.Fatalf("chemistry: %v", a)
	}
	union := sc["Object"].(map[string]any)["oneOf"].([]any)
	found := false
	for _, u := range union {
		if u.(map[string]any)["$ref"] == "#/components/schemas/Object.ups_battery" {
			found = true
		}
	}
	if !found {
		t.Fatal("Object does not list the new type")
	}
	typeParam := func(doc map[string]any) []any {
		for _, p := range pathsOf(doc)["GET /objects"]["parameters"].([]any) {
			if pm := p.(map[string]any); pm["name"] == "type" {
				return pm["schema"].(map[string]any)["enum"].([]any)
			}
		}
		t.Fatal("no type parameter")
		return nil
	}
	if !slices.Contains(typeParam(after), any("ups_battery")) || slices.Contains(typeParam(before), any("ups_battery")) {
		t.Fatal("the type parameter enum does not follow the registry")
	}
	// The document with the new type is still valid.
	d, err := libopenapi.NewDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ok, errs := schema_validation.ValidateOpenAPIDocument(d); !ok {
		t.Fatalf("invalid after the type was added: %v", errs)
	}

	// /schema and the type page.
	r := e.get("/api/v1/schema/types/ups_battery", tokReader)
	wantStatus(t, r, 200)
	if !strings.Contains(string(r.Body), "capacity_va") || strings.Contains(string(r.Body), "api_token") {
		t.Fatalf("type page: %s", r.Body)
	}
	if s := e.get("/api/v1/schema", tokReader); !strings.Contains(string(s.Body), "ups_battery") {
		t.Fatalf("/schema does not list the new type: %s", s.Body)
	}
	// The API accepts objects of the new type, and the answer conforms to the document that now describes it.
	rack := e.ids["rack-p"]
	b := userBatch("", func(b *sdk.Batch) {
		b.Object("rack", "rack").Identify(sdk.IDKey, rack)
		b.Object("u", "ups_battery").Identify("serial", "UPS-0001").In("rack").Set("capacity_va", 3000).Set("chemistry", "lithium")
	})
	wantStatus(t, e.do("POST", "/api/v1/batches", tokProposer, batchJSON(t, b)), 200)
	list := e.page("/api/v1/objects?type=ups_battery", tokReader)
	if list.Total != 1 {
		t.Fatalf("ups_battery objects: %d", list.Total)
	}
	v := newDocValidator(t, raw)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/objects/"+field(t, list.Items[0], "id"), nil)
	req.Header.Set("Authorization", "Bearer "+tokReader)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if ok, errs := v.ValidateHttpResponse(req, rec.Result()); !ok {
		for _, ve := range errs {
			t.Errorf("the new type's object breaks its own schema: %s: %s", ve.Message, ve.Reason)
		}
	}

	// And it leaves with the registry.
	e.reg.set(schema.Builtin())
	_, gone := openAPIDoc(t, e)
	if _, ok := schemas(gone)["Object.ups_battery"]; ok {
		t.Fatal("the type is still documented after it left the registry")
	}
	wantProblem(t, e.get("/api/v1/schema/types/ups_battery", tokReader), 404, "not-found")
}

// The registry can change while documents are generated: no data race, no half-built document.
func TestOpenAPIWhileTheRegistryChanges(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	ov, err := schema.Parse([]byte("types:\n  ups_battery:\n    parents: [rack]\n    attributes:\n      serial: {kind: string, identity: strong}\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := schema.Builtin().Merge(ov)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				e.reg.set(merged)
			} else {
				e.reg.set(schema.Builtin())
			}
		}
	}()
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 15 {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
				req.Header.Set("Authorization", "Bearer "+tokReader)
				rec := httptest.NewRecorder()
				e.h.ServeHTTP(rec, req)
				var doc map[string]any
				if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
					t.Errorf("status %d: %.200s", rec.Code, rec.Body)
					return
				}
				sc := doc["components"].(map[string]any)["schemas"].(map[string]any)
				_, a := sc["Object.ups_battery"]
				_, b := sc["Attributes.ups_battery"]
				if a != b {
					t.Errorf("half-built document: Object.ups_battery %v, Attributes.ups_battery %v", a, b)
					return
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	wg.Wait()
}

func TestAttributeKindsMapToJSONSchema(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	_, doc := openAPIDoc(t, e)
	sc := doc["components"].(map[string]any)["schemas"].(map[string]any)
	dev := sc["Attributes.device"].(map[string]any)["properties"].(map[string]any)
	iface := sc["Attributes.interface"].(map[string]any)["properties"].(map[string]any)
	get := func(m map[string]any, k string) map[string]any {
		t.Helper()
		v, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("attribute %s is not documented", k)
		}
		return v
	}
	if a := get(dev, "mgmt_ip"); a["type"] != "string" || a["x-trica-kind"] != "ip" {
		t.Errorf("mgmt_ip: %v", a)
	}
	if a := get(dev, "warranty_end"); a["type"] != "string" || a["format"] != "date-time" {
		t.Errorf("warranty_end: %v", a)
	}
	if a := get(dev, "base_mac"); a["pattern"] == nil || a["x-trica-kind"] != "mac" {
		t.Errorf("base_mac: %v", a)
	}
	if a := get(dev, "role"); a["type"] != "string" || len(a["enum"].([]any)) < 2 {
		t.Errorf("role: %v", a)
	}
	if a := get(iface, "speed_mbps"); a["type"] != "integer" {
		t.Errorf("speed_mbps: %v", a)
	}
	if a := get(dev, "vendor"); a["title"] == nil || a["description"] == nil {
		t.Errorf("vendor lost its label or description: %v", a)
	}
	// One schema per type of the registry, no more.
	n := 0
	for k := range sc {
		if strings.HasPrefix(k, "Object.") {
			n++
		}
	}
	if want := len(schema.Builtin().Types); n != want {
		t.Errorf("%d Object.* schemas for %d types", n, want)
	}
	// Every attribute of every built-in type is documented, typed by its kind.
	for tn, ty := range schema.Builtin().Types {
		props := sc["Attributes."+tn].(map[string]any)["properties"].(map[string]any)
		for an, a := range ty.Attributes {
			p, ok := props[an].(map[string]any)
			if !ok {
				t.Errorf("%s.%s is not documented", tn, an)
				continue
			}
			if p["x-trica-kind"] != string(a.Kind) {
				t.Errorf("%s.%s: kind %v, want %s", tn, an, p["x-trica-kind"], a.Kind)
			}
		}
	}
}
