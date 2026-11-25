package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/catalog"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/internal/collect/kubernetes"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// These tests put the catalog and the kubernetes collector (build cluster,
// Argo CD on) through the real engine on PostgreSQL: what is declared and what
// is discovered must be one inventory, not two.

type world struct {
	t       *testing.T
	st      *store.Store
	reg     *schema.Registry
	engine  *reconcile.Engine
	dir     string
	results []*reconcile.Result
}

func newWorld(t *testing.T) *world {
	t.Helper()
	reg := schema.Builtin()
	st := testdb.New(t)
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	w := &world{t: t, st: st, reg: reg, dir: t.TempDir()}
	w.engine = &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Now: func() time.Time { return now }}

	// What the agent and Redfish would report: the VM and the server the
	// legacy ERP runs on.
	hosts := sdk.NewBatch("test-hosts", sdk.ScopePartial)
	hosts.Object("vm", "vm").Identify("name", "erp-01")
	hosts.Object("srv", "device").Identify("serial", "CZJ0000001").Set("name", "db-srv-01")
	w.apply(hosts)

	kf := kubernetes.FakeKubernetesVersion(kubernetes.Build131)
	ktg := kf.TargetWith("build", map[string]any{
		"cluster": "build", "environment": "build", "argocd": true,
		"argocd_clusters":          map[string]string{"https://203.0.113.1:6443": "dev", "https://198.51.100.1:6443": "prod"},
		"argocd_platform_projects": []string{"infra"},
	})
	kc, err := kf.Collector(ktg)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range collecttest.Run(t, kc) {
		w.apply(b)
	}
	return w
}

func (w *world) apply(b *sdk.Batch) *reconcile.Result {
	w.t.Helper()
	res, err := w.engine.Apply(context.Background(), b, "", "")
	if err != nil {
		w.t.Fatalf("apply %s: %v", b.Source, err)
	}
	w.results = append(w.results, res)
	return res
}

// catalogRun writes src as the catalog file and applies one run of it.
func (w *world) catalogRun(src []byte) (*sdk.Batch, *reconcile.Result) {
	w.t.Helper()
	p := filepath.Join(w.dir, "applications.yaml")
	if err := os.WriteFile(p, src, 0o600); err != nil {
		w.t.Fatal(err)
	}
	b := run(w.t, collect.Target{Kind: catalog.Kind, Name: "main", Address: p})
	return b, w.apply(b)
}

func (w *world) objects() []*store.Object {
	w.t.Helper()
	objs, err := w.st.AllObjects(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return objs
}

func (w *world) all(typ, display string) []*store.Object {
	var out []*store.Object
	for _, o := range w.objects() {
		if o.Type == typ && o.Display == display {
			out = append(out, o)
		}
	}
	return out
}

func (w *world) one(typ, display string) *store.Object {
	w.t.Helper()
	got := w.all(typ, display)
	if len(got) != 1 {
		w.t.Fatalf("%d %s %q, want exactly 1", len(got), typ, display)
	}
	return got[0]
}

func (w *world) related(typ, from, to string) bool {
	w.t.Helper()
	rels, err := w.st.CurrentRelations(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	for _, r := range rels {
		if r.Type == typ && r.FromID == from && r.ToID == to {
			return true
		}
	}
	return false
}

func (w *world) openDiscrepancies(kind string) []reconcile.Discrepancy {
	w.t.Helper()
	ds, _, err := reconcile.ListDiscrepancies(context.Background(), w.st, reconcile.DiscrepancyFilter{Status: "open", Kind: kind, Limit: 1000})
	if err != nil {
		w.t.Fatal(err)
	}
	return ds
}

// TestDeclaredAndDiscoveredAreOneApplication: Argo CD gives orion (project
// tenant-orion) and its components; the catalog declares orion with its owner
// and criticality. One application, one component app, both sources on it.
func TestDeclaredAndDiscoveredAreOneApplication(t *testing.T) {
	w := newWorld(t)
	before := w.one("application", "orion")
	_, res := w.catalogRun(catalog.Fixture("applications.yaml"))
	// The one warning: payroll, which nobody declares or discovers.
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "application name=payroll not found") {
		t.Errorf("engine warnings %q, want only the unresolved payroll", res.Warnings)
	}

	orion := w.one("application", "Orion") // displayed by its declared name now
	if orion.ID != before.ID {
		t.Fatal("the catalog created a second orion instead of completing the discovered one")
	}
	for k, v := range map[string]string{"criticality": "high", "lifecycle": "production", "owner": "orion-team", "domain": "customer"} {
		if orion.Attrs[k] != v {
			t.Errorf("orion %s = %v, want %q", k, orion.Attrs[k], v)
		}
	}
	// Argo CD's repository is observed and outranks the declared one.
	if orion.Attrs["repository"] == "https://git.example.org/orion/orion" {
		t.Error("the declared repository outranks the observed one")
	}
	if ds := w.openDiscrepancies("value_mismatch"); len(ds) == 0 {
		t.Error("declared and observed repositories differ and no value_mismatch is open")
	}
	// The component Argo CD derived from orion-app-* and the declared one
	// are one object under the one application.
	var apps []*store.Object
	for _, c := range w.all("component", "app") {
		if c.ParentID != nil && *c.ParentID == orion.ID {
			apps = append(apps, c)
		}
	}
	if len(apps) != 1 || apps[0].Attrs["kind"] != "service" || apps[0].Attrs["technology"] != "java" {
		t.Errorf("components app of orion: %+v, want one, with the declared kind and technology", apps)
	}

	team := w.one("team", "orion-team")
	if !w.related("owned_by", orion.ID, team.ID) {
		t.Error("orion is not owned by its team")
	}
	entra := w.one("application", "Microsoft Entra ID")
	if !w.related("depends_on", orion.ID, entra.ID) {
		t.Error("orion does not depend on entra-id")
	}
	// lyra is discovered (tenant-lyra), not declared in this file: found by name.
	lyra := w.one("application", "lyra")
	if !w.related("calls", orion.ID, lyra.ID) {
		t.Error("orion's call to the discovered lyra is not resolved")
	}
	if !w.related("supplied_by", entra.ID, w.one("vendor", "Microsoft").ID) {
		t.Error("entra-id is not supplied by Microsoft")
	}
}

// TestInstancesOutsideKubernetes: the ERP instances run on the VM and the
// server the other sources reported, found by name and by serial; payroll,
// which nobody declares or discovers, is an open unresolved reference.
func TestInstancesOutsideKubernetes(t *testing.T) {
	w := newWorld(t)
	w.catalogRun(catalog.Fixture("applications.yaml"))

	srv := w.one("component_instance", "legacy-erp/server/prod/erp-01")
	if !w.related("runs_on", srv.ID, w.one("vm", "erp-01").ID) {
		t.Error("the ERP server instance does not run on the vm erp-01")
	}
	db := w.one("component_instance", "legacy-erp/db/prod/dc1")
	if !w.related("runs_on", db.ID, w.one("device", "db-srv-01").ID) {
		t.Error("the ERP db instance does not run on the server found by serial")
	}
	prod := w.one("environment", "prod")
	if !w.related("deployed_in", srv.ID, prod.ID) || prod.Attrs["kind"] != "prod" {
		t.Errorf("instance not deployed in prod, or prod %v", prod.Attrs)
	}
	if !w.related("exposed_by", srv.ID, w.one("endpoint", "https://erp.example.org").ID) {
		t.Error("the ERP is not exposed by its URL")
	}
	ds := w.openDiscrepancies("unresolved_reference")
	found := false
	for _, d := range ds {
		if strings.Contains(d.Fingerprint+string(d.Details)+string(d.Observed), "payroll") {
			found = true
		}
	}
	if !found {
		t.Errorf("no unresolved_reference for payroll among %+v", ds)
	}
}

// TestSecondRunChangesNothing: the catalog is read every interval.
func TestSecondRunChangesNothing(t *testing.T) {
	w := newWorld(t)
	w.catalogRun(catalog.Fixture("applications.yaml"))
	_, res := w.catalogRun(catalog.Fixture("applications.yaml"))
	if res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
		t.Errorf("second run: %+v", res)
	}
}

// TestRemovedFromTheCatalog: legacy-erp taken out of the catalog loses what
// the catalog said (owner, dependencies, its instances' hosts); orion, also
// discovered, keeps what Argo CD says and loses only what was declared.
func TestRemovedFromTheCatalog(t *testing.T) {
	w := newWorld(t)
	w.catalogRun(catalog.Fixture("applications.yaml"))
	erp := w.one("application", "Legacy ERP")
	orion := w.one("application", "Orion")
	vm := w.one("vm", "erp-01")
	inst := w.one("component_instance", "legacy-erp/server/prod/erp-01")

	w.catalogRun([]byte(head + `teams: [{name: it-ops}]
applications:
  - {name: entra-id, display_name: Microsoft Entra ID, hosting: saas, vendor: Microsoft, owner: it-ops}
`))
	if w.related("runs_on", inst.ID, vm.ID) {
		t.Error("the withdrawn instance still runs on the vm")
	}
	if w.related("depends_on", erp.ID, w.one("application", "Microsoft Entra ID").ID) {
		t.Error("the withdrawn application still depends on entra-id")
	}
	var after *store.Object
	for _, o := range w.objects() {
		if o.ID == orion.ID {
			after = o
		}
	}
	if after == nil || after.Status != "active" {
		t.Fatalf("orion %+v, want active: Argo CD still sees it", after)
	}
	if _, ok := after.Attrs["criticality"]; ok {
		t.Error("orion keeps its declared criticality after leaving the catalog")
	}
	if after.Attrs["repository"] == nil {
		t.Error("orion lost what Argo CD says of it")
	}
	if w.related("owned_by", orion.ID, w.one("team", "orion-team").ID) {
		t.Error("orion is still owned by its team")
	}

	// Only the catalog declared the ERP, its team-less owner and orion's
	// team: they are absent, under their names, and nothing was deleted.
	for _, o := range []struct{ typ, display string }{
		{"application", "legacy-erp"}, {"team", "orion-team"}, {"person", "erp.owner@example.org"},
		{"component_instance", "legacy-erp/server/prod/erp-01"},
	} {
		got := w.one(o.typ, o.display)
		if got.Status != "absent" {
			t.Errorf("%s %s is %s, want absent", o.typ, o.display, got.Status)
		}
	}
	if got := w.one("application", "legacy-erp"); got.ID != erp.ID || got.Attrs["criticality"] != nil {
		t.Errorf("legacy-erp %+v: the same object, without its declared values", got)
	}
	// What the catalog still declares stays active.
	if got := w.one("application", "Microsoft Entra ID"); got.Status != "active" {
		t.Errorf("entra-id is %s", got.Status)
	}
	// The vm the other source reported is untouched.
	if got := w.one("vm", "erp-01"); got.Status != "active" {
		t.Errorf("the vm is %s", got.Status)
	}

	// Declared again: the same object, active again.
	w.catalogRun(catalog.Fixture("applications.yaml"))
	if got := w.one("application", "Legacy ERP"); got.ID != erp.ID || got.Status != "active" || got.Attrs["criticality"] != "medium" {
		t.Errorf("re-declared legacy-erp %+v", got)
	}
	if !w.related("runs_on", inst.ID, vm.ID) {
		t.Error("the re-declared instance does not run on the vm again")
	}
}

// TestBrokenCatalogWithdrawsNothing: a typo in the file fails the run, and the
// inventory keeps what the last good run declared.
func TestBrokenCatalogWithdrawsNothing(t *testing.T) {
	w := newWorld(t)
	w.catalogRun(catalog.Fixture("applications.yaml"))
	b, res := w.catalogRun([]byte(head + "applications: [{name: Orion}]\n"))
	if b.Health.OK {
		t.Fatal("the broken catalog ran")
	}
	if res.RelationsClosed != 0 || res.ValuesChanged != 0 {
		t.Errorf("a failed run changed the inventory: %+v", res)
	}
	orion := w.one("application", "Orion")
	if orion.Attrs["criticality"] != "high" || !w.related("owned_by", orion.ID, w.one("team", "orion-team").ID) {
		t.Error("the failed run withdrew what the catalog declared")
	}
}

// TestGovernanceSearches: the searches the README gives answer what they say.
func TestGovernanceSearches(t *testing.T) {
	w := newWorld(t)
	w.catalogRun(catalog.Fixture("applications.yaml"))
	s := &search.Searcher{Store: w.st}
	names := func(q string) map[string]bool {
		t.Helper()
		r, err := s.Search(context.Background(), q, search.Page{Limit: 500})
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		out := map[string]bool{}
		for _, h := range r.Items {
			out[h.Display] = true
		}
		return out
	}
	undeclared := names("type:application -source:catalog:*")
	if undeclared["Orion"] || undeclared["Legacy ERP"] || !undeclared["lyra"] {
		t.Errorf("discovered but not declared: %v; want lyra, not Orion or Legacy ERP", undeclared)
	}
	unowned := names("type:application -rel.owned_by:*")
	if unowned["Orion"] || !unowned["lyra"] {
		t.Errorf("without owner: %v", unowned)
	}
	if got := names("type:application lifecycle:deprecated"); len(got) != 1 || !got["Legacy ERP"] {
		t.Errorf("deprecated: %v", got)
	}
}
