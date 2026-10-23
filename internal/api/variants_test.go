package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// libIndex is an api.DeviceTypeLibrary over a devicetypes index.
type libIndex struct{ *devicetypes.Index }

func (l libIndex) DeviceTypeChoices(vendor, model, pn string) devicetypes.Choice {
	return l.Choices(vendor, model, pn)
}
func (l libIndex) DeviceTypeModel(slug string) *devicetypes.Model { return l.BySlug(slug) }

// testLibrary: the Acme SRV-2, a generic model and two chassis (the 12LFF
// listed by its part number), a family curated by variant_of like the
// catalogue's, and the 12LFF with an option
// cage, a variant of the 12LFF without a part number of its own, like HPE's
// DL380 Gen10 8SFF with its NVMe cage. Only the 8SFF and the 12LFF have
// pictures (testImages).
var testLibrary = libIndex{devicetypes.NewIndex([]*devicetypes.Model{
	{Slug: "acme-srv2", Vendor: "Acme", Model: "SRV-2", UHeight: 1},
	{Slug: "acme-srv2-8sff", Vendor: "Acme", Model: "SRV-2 8SFF", UHeight: 1, FullDepth: true, VariantOf: "acme-srv2"},
	{Slug: "acme-srv2-12lff", Vendor: "Acme", Model: "SRV-2 12LFF", UHeight: 2, PartNumbers: []string{"SKU-12"}, VariantOf: "acme-srv2"},
	{Slug: "acme-srv2-12lff-nvme", Vendor: "Acme", Model: "SRV-2 12LFF NVMe", UHeight: 2, VariantOf: "acme-srv2-12lff"},
	{Slug: "acme-desk-nas", Vendor: "Acme", Model: "DeskNAS", UHeight: 0},
})}

func init() {
	testImages["acme-srv2-8sff/front"] = api.DeviceImage{ContentType: "image/png", ETag: `"8"`, Data: pngBytes}
	testImages["acme-srv2-12lff/front"] = api.DeviceImage{ContentType: "image/png", ETag: `"12"`, Data: pngBytes}
}

type choice struct {
	Current *struct {
		Model struct {
			ID string `json:"id"`
		} `json:"model"`
		Slug   string `json:"slug"`
		Source string `json:"source"`
	} `json:"current"`
	LibraryMatch *struct {
		Slug string `json:"slug"`
		How  string `json:"how"`
	} `json:"library_match"`
	Certain     bool   `json:"certain"`
	Reason      string `json:"reason"`
	DeclaredBy  string `json:"declared_by"`
	SKUConflict *struct {
		Slug string `json:"slug"`
	} `json:"sku_conflict"`
	Declarations []struct {
		Slug   string `json:"slug"`
		Source string `json:"source"`
	} `json:"declarations"`
	Candidates []struct {
		Slug         string  `json:"slug"`
		UHeight      float64 `json:"u_height"`
		FrontImage   string  `json:"front_image"`
		How          string  `json:"how"`
		Current      bool    `json:"current"`
		LibraryMatch bool    `json:"library_match"`
	} `json:"candidates"`
	CanChoose bool `json:"can_choose"`
}

func (e *env) choice(id, token string) choice {
	e.t.Helper()
	r := e.get("/api/v1/objects/"+id+"/device-type-candidates", token)
	wantStatus(e.t, r, 200)
	var c choice
	r.json(e.t, &c)
	return c
}

// variantItem is the rack contents' item of srv-v.
func (e *env) variantItem() rackItem {
	e.t.Helper()
	for _, it := range rackItems(e.t, e.page("/api/v1/racks/"+e.ids["rack-p"]+"/contents", tokReader)) {
		if it.Device.Display == "srv-v" {
			return it
		}
	}
	e.t.Fatal("srv-v not in rack-a")
	return rackItem{}
}

// collectSrvV is a collector's run on srv-v: vendor, model, part number.
func (e *env) collectSrvV(pn string) {
	b := sdk.NewBatch("redfish:srv-v", sdk.ScopePartial)
	o := b.Object("srv-v", "device").Identify("name", "srv-v").Set("vendor", "Acme").Set("model", "SRV-2").Set("role", "server")
	if pn != "" {
		o.Set("part_number", pn)
	}
	b.Relations = append(b.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref("srv-v"),
		To: sdk.Endpoint{Type: "rack", Identity: map[string]string{sdk.IDKey: e.ids["rack-p"]}}, Attributes: map[string]any{"position": 20, "face": "front"}})
	e.apply(b)
}

func (e *env) syncLibrary() {
	e.t.Helper()
	if _, _, err := devicetypes.Sync(context.Background(), e.eng, testLibrary.Index, "devicetypes"); err != nil {
		e.t.Fatal(err)
	}
}

// The whole flow of ADR 0048 through the real API and engine: the library
// matches the generic model and says it is not sure, a person chooses the
// chassis with the batch the UI sends, the choice decides the rack's face,
// and it survives the collector's and the library's later passes.
func TestChooseTheChassisVariant(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.collectSrvV("")
	e.syncLibrary()
	set := loadUIBatches(t)

	c := e.choice(e.ids["srv-v"], tokReader)
	if c.Certain || c.Reason != devicetypes.UncertainBase || c.Current == nil || c.Current.Slug != "acme-srv2" || c.Current.Source != devicetypes.Source {
		t.Fatalf("before: %+v", c)
	}
	if c.CanChoose || len(c.Declarations) != 0 || c.LibraryMatch == nil || c.LibraryMatch.How != devicetypes.ByModel {
		t.Fatalf("before: %+v (a reader may not choose)", c)
	}
	if len(c.Candidates) != 4 || c.Candidates[0].Slug != "acme-srv2" || !c.Candidates[0].Current || !c.Candidates[0].LibraryMatch ||
		c.Candidates[3].Slug != "acme-srv2-8sff" || c.Candidates[3].FrontImage != "/api/v1/device-types/acme-srv2-8sff/front" || c.Candidates[1].UHeight != 2 {
		t.Fatalf("candidates %+v", c.Candidates)
	}
	if !e.choice(e.ids["srv-v"], tokProposer).CanChoose {
		t.Fatal("carol may propose devices and models: she may choose")
	}
	if it := e.variantItem(); !it.VariantUncertain || it.Variants != 4 || it.FrontImage != "" {
		t.Fatalf("rack item before %+v, want uncertain, 4 variants, no picture", it)
	}

	// Refused: a reader, and a person who may propose devices but not models.
	wantStatus(t, e.do("POST", "/api/v1/batches", tokReader, set.body(t, e, "chooseVariant")), 403)
	wantProblem(t, e.do("POST", "/api/v1/batches", tokDevProp, set.body(t, e, "chooseVariant")), 403, "forbidden")
	if e.choice(e.ids["srv-v"], tokDevProp).CanChoose {
		t.Fatal("hugo may not propose models: he may not choose")
	}

	// Carol chooses the 8SFF: the preview relates, the submit applies.
	if p := e.previewUI(set, "chooseVariant", tokProposer); !p.relates() {
		t.Fatalf("preview %+v", p.Result.Changes)
	}
	out := e.submitUI(set, "chooseVariant", tokProposer, 200)
	if out.Status != "applied" || out.Source != "user:carol" {
		t.Fatalf("choice %+v", out)
	}
	check := func(when string) {
		t.Helper()
		c := e.choice(e.ids["srv-v"], tokReader)
		if !c.Certain || c.Reason != devicetypes.ByDeclared || c.DeclaredBy != "user:carol" || c.Current == nil || c.Current.Slug != "acme-srv2-8sff" {
			t.Fatalf("%s: %+v", when, c)
		}
		if len(c.Declarations) != 1 || c.Declarations[0].Slug != "acme-srv2-8sff" || !c.Candidates[3].Current || c.Candidates[0].Current {
			t.Fatalf("%s: declarations %+v candidates %+v", when, c.Declarations, c.Candidates)
		}
		it := e.variantItem()
		if it.VariantUncertain || it.FrontImage != "/api/v1/device-types/acme-srv2-8sff/front" || it.UHeight == nil || *it.UHeight != 1 || !it.FullDepth || it.ModelName != "SRV-2 8SFF" {
			t.Fatalf("%s: rack item %+v, want the 8SFF's face, 1 U, full depth", when, it)
		}
		var f struct {
			ModelName string `json:"model_name"`
			Faces     []struct {
				Image string `json:"image"`
			} `json:"faces"`
		}
		e.get("/api/v1/objects/"+e.ids["srv-v"]+"/faces", tokReader).json(t, &f)
		if f.ModelName != "SRV-2 8SFF" || len(f.Faces) != 1 || f.Faces[0].Image != "/api/v1/device-types/acme-srv2-8sff/front" {
			t.Fatalf("%s: faces %+v", when, f)
		}
	}
	// Right after the choice the library's relation is still there: the choice wins the reads.
	check("right after the choice")
	// The collector runs again, then the library: the choice stands, the library's relation is gone.
	e.clk.advance(60e9)
	e.collectSrvV("")
	e.syncLibrary()
	check("after a collection and a library pass")
	if m := e.relationsOfType(e.ids["srv-v"], "instance_of"); len(m) != 1 {
		t.Fatalf("instance_of relations %v, want carol's only", m)
	}

	// A part number naming another chassis arrives: the declaration stands, the conflict is said.
	e.clk.advance(60e9)
	e.collectSrvV("SKU-12")
	e.syncLibrary()
	c = e.choice(e.ids["srv-v"], tokReader)
	if c.SKUConflict == nil || c.SKUConflict.Slug != "acme-srv2-12lff" || c.Current.Slug != "acme-srv2-8sff" || !c.Certain {
		t.Fatalf("sku conflict: %+v", c)
	}

	// Carol changes her mind to the 12LFF: her 8SFF claim is withdrawn.
	e.ids["model-8sff"] = c.Current.Model.ID
	e.submitUI(set, "changeVariant", tokProposer, 200)
	c = e.choice(e.ids["srv-v"], tokReader)
	if c.Current.Slug != "acme-srv2-12lff" || len(c.Declarations) != 1 || c.SKUConflict != nil {
		t.Fatalf("after the change: %+v", c)
	}
}

// relationsOfType lists the other ends of an object's current outgoing relations of one type.
func (e *env) relationsOfType(id, typ string) []string {
	e.t.Helper()
	var out []string
	for _, raw := range e.page("/api/v1/objects/"+id+"/relations", tokReader).Items {
		var r struct {
			Type      string `json:"type"`
			Direction string `json:"direction"`
			ToID      string `json:"to_id"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			e.t.Fatal(err)
		}
		if r.Type == typ && r.Direction == "out" {
			out = append(out, r.ToID)
		}
	}
	return out
}

func TestDeviceTypeCandidatesRefusals(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	wantProblem(t, e.get("/api/v1/objects/"+e.ids["rack-p"]+"/device-type-candidates", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/objects/00000000-0000-4000-8000-000000000999/device-type-candidates", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/objects/not-a-uuid/device-type-candidates", tokReader), 400, "invalid-request")
	wantStatus(t, e.get("/api/v1/objects/"+e.ids["sw"]+"/device-type-candidates", ""), 401)
	// A device nobody knows the model of, on a server without a library: nothing to choose.
	srv, err := api.New(api.Config{Engine: e.eng, Authorizer: e.auth, Auditor: e.aud, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/objects/"+e.ids["sw"]+"/device-type-candidates", nil)
	r.Header.Set("Authorization", "Bearer "+tokReader)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var c choice
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != 200 || c.Certain || c.Reason != devicetypes.UncertainNone || len(c.Candidates) != 0 {
		t.Fatalf("no library: %d %s", w.Code, w.Body)
	}
}

// A declared model that is a variant of the chassis the part number names
// (an option cage in the same CTO chassis) refines that chassis: no
// sku_conflict. Declaring another chassis is one.
func TestDeclaredRefinementIsNoSKUConflict(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.collectSrvV("SKU-12")
	e.syncLibrary()
	if c := e.choice(e.ids["srv-v"], tokReader); !c.Certain || c.Reason != devicetypes.BySKU || c.Current == nil || c.Current.Slug != "acme-srv2-12lff" {
		t.Fatalf("before: %+v, want the 12LFF by its part number", c)
	}
	declare := func(slug string) {
		b := sdk.NewBatch("declaration:racks", sdk.ScopeFull)
		b.Confidence = sdk.Declared
		b.Object("dev", "device").Identify(sdk.IDKey, e.ids["srv-v"])
		b.Object("dt", "device_type").Identify("slug", slug)
		b.Relations = append(b.Relations, &sdk.Relation{Type: "instance_of", From: sdk.Ref("dev"), To: sdk.Ref("dt")})
		e.apply(b)
	}
	declare("acme-srv2-12lff-nvme")
	c := e.choice(e.ids["srv-v"], tokReader)
	if c.Current == nil || c.Current.Slug != "acme-srv2-12lff-nvme" || c.Reason != devicetypes.ByDeclared || c.SKUConflict != nil {
		t.Fatalf("the NVMe cage of the 12LFF: %+v, want it declared and no conflict", c)
	}
	declare("acme-srv2-8sff")
	if c := e.choice(e.ids["srv-v"], tokReader); c.SKUConflict == nil || c.SKUConflict.Slug != "acme-srv2-12lff" {
		t.Fatalf("another chassis: %+v, want the part number's 12LFF as a conflict", c)
	}
}
