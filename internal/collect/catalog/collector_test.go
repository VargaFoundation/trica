package catalog_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/catalog"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// fileTarget writes the fixtures into a temporary file and returns a target
// reading it.
func fileTarget(t *testing.T, fixture string) collect.Target {
	t.Helper()
	p := filepath.Join(t.TempDir(), fixture)
	if err := os.WriteFile(p, catalog.Fixture(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return collect.Target{Kind: catalog.Kind, Name: "main", Address: p}
}

func run(t *testing.T, tg collect.Target) *sdk.Batch {
	t.Helper()
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	bs := collecttest.Run(t, c)
	if len(bs) != 1 {
		t.Fatalf("%d batches, want 1", len(bs))
	}
	return bs[0]
}

func related(b *sdk.Batch, typ, from string, to sdk.Endpoint) bool {
	for _, r := range b.Relations {
		if r.Type != typ || r.From.Ref != from {
			continue
		}
		if r.To.Ref == to.Ref && r.To.Type == to.Type && equalMaps(r.To.Identity, to.Identity) {
			return true
		}
	}
	return false
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestFileCatalog(t *testing.T) {
	b := run(t, fileTarget(t, "applications.yaml"))
	if !b.Health.OK || b.Health.Error != "" {
		t.Fatalf("health %+v", b.Health)
	}
	if b.Source != "catalog:main" || b.Scope != sdk.ScopeFull || b.Confidence != sdk.Declared {
		t.Fatalf("source %s, scope %s, confidence %s: want catalog:main, full, declared", b.Source, b.Scope, b.Confidence)
	}
	app := collecttest.Find([]*sdk.Batch{b}, "application", "name", "orion")
	if app == nil {
		t.Fatal("no application orion")
	}
	for k, v := range map[string]string{"display_name": "Orion", "criticality": "high", "lifecycle": "production", "hosting": "self", "owner": "orion-team", "domain": "customer", "repository": "https://git.example.org/orion/orion", "description": "Customer portal."} {
		if app.Attributes[k] != v {
			t.Errorf("orion %s = %v, want %q", k, app.Attributes[k], v)
		}
	}
	if _, set := app.Attributes["vendor"]; set {
		t.Error("an empty vendor is declared: an absent key must not become a value")
	}
	if !related(b, "owned_by", app.Ref, sdk.Endpoint{Ref: "team/orion-team"}) {
		t.Error("orion is not owned by its team")
	}
	// A dependency of the catalog is related by ref, one outside it by name.
	if !related(b, "depends_on", app.Ref, sdk.Endpoint{Ref: "app/entra-id"}) {
		t.Error("orion does not depend on entra-id")
	}
	if !related(b, "calls", app.Ref, sdk.Endpoint{Type: "application", Identity: map[string]string{"name": "lyra"}}) {
		t.Error("orion's call to lyra (not in this file) is not designated by name")
	}

	saas := collecttest.Find([]*sdk.Batch{b}, "application", "name", "entra-id")
	if saas.Attributes["vendor"] != "Microsoft" || saas.Attributes["hosting"] != "saas" {
		t.Errorf("entra-id %v", saas.Attributes)
	}
	if !related(b, "supplied_by", saas.Ref, sdk.Endpoint{Ref: "vendor/Microsoft"}) {
		t.Error("entra-id is not supplied by its vendor")
	}

	erp := collecttest.Find([]*sdk.Batch{b}, "application", "name", "legacy-erp")
	if !related(b, "owned_by", erp.Ref, sdk.Endpoint{Ref: "person/erp.owner@example.org"}) {
		t.Error("legacy-erp is not owned by the person, by lowered e-mail")
	}
	if p := collecttest.Find([]*sdk.Batch{b}, "person", "email", "erp.owner@example.org"); p == nil || p.Attributes["name"] != "ERP Owner" {
		t.Errorf("person %+v", p)
	}

	inst := collecttest.Find([]*sdk.Batch{b}, "component_instance", "key", "legacy-erp/server/prod/erp-01")
	if inst == nil {
		t.Fatal("no instance legacy-erp/server/prod/erp-01 (location from the host's name)")
	}
	if inst.Parent != "component/legacy-erp/server" || inst.Attributes["version"] != "9.2" || inst.Attributes["environment"] != "prod" {
		t.Errorf("instance %+v", inst)
	}
	if !related(b, "deployed_in", inst.Ref, sdk.Endpoint{Ref: "env/prod"}) {
		t.Error("the instance is not deployed in prod")
	}
	if !related(b, "runs_on", inst.Ref, sdk.Endpoint{Type: "vm", Identity: map[string]string{"name": "erp-01"}}) {
		t.Error("the instance does not run on the vm erp-01")
	}
	db := collecttest.Find([]*sdk.Batch{b}, "component_instance", "key", "legacy-erp/db/prod/dc1")
	if db == nil || !related(b, "runs_on", db.Ref, sdk.Endpoint{Type: "device", Identity: map[string]string{"serial": "CZJ0000001"}}) {
		t.Error("the db instance does not run on the server found by serial")
	}
	for url, proto := range map[string]string{"https://erp.example.org": "https", "erp.example.org:8443": "tcp"} {
		e := collecttest.Find([]*sdk.Batch{b}, "endpoint", "url", url)
		if e == nil || e.Attributes["protocol"] != proto {
			t.Errorf("endpoint %s: %+v", url, e)
			continue
		}
		if !related(b, "exposed_by", inst.Ref, sdk.Endpoint{Ref: e.Ref}) {
			t.Errorf("the instance is not exposed by %s", url)
		}
	}
	if env := collecttest.Find([]*sdk.Batch{b}, "environment", "name", "prod"); env == nil || env.Attributes["kind"] != "prod" {
		t.Errorf("environment prod %+v", env)
	}
	if c := collecttest.Find([]*sdk.Batch{b}, "component", "name", "postgres"); c == nil || c.Parent != "app/orion" || c.Attributes["kind"] != "database" || c.Attributes["technology"] != "postgresql" {
		t.Errorf("component postgres %+v", c)
	}
}

// A mounted ConfigMap: each key is a link at the top to a file under a
// hidden, timestamped directory, and ..data points at the current one.
func configMapDir(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	ts := filepath.Join(dir, "..2026_11_25_10_00_00.000000001")
	if err := os.Mkdir(ts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(ts, f), catalog.Fixture(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Base(ts), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.Symlink(filepath.Join("..data", f), filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	// Not a catalog file: ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDirectoryCatalog(t *testing.T) {
	dir := configMapDir(t, "applications.yaml", "teams.yaml")
	b := run(t, collect.Target{Kind: catalog.Kind, Name: "cm", Address: "file:" + dir})
	if !b.Health.OK {
		t.Fatalf("health %+v", b.Health)
	}
	// The second file's application is owned by the team it declares, and
	// the first file's call to lyra now resolves inside the batch.
	lyra := collecttest.Find([]*sdk.Batch{b}, "application", "name", "lyra")
	if lyra == nil || !related(b, "owned_by", lyra.Ref, sdk.Endpoint{Ref: "team/lyra-team"}) {
		t.Fatalf("lyra %+v not owned by lyra-team", lyra)
	}
	if !related(b, "calls", "app/orion", sdk.Endpoint{Ref: "app/lyra"}) {
		t.Error("orion's call to lyra is not resolved within the merged catalog")
	}
	if n := len(collecttest.Objects([]*sdk.Batch{b}, "application")); n != 4 {
		t.Errorf("%d applications, want 4: each file read once, the ..data copy and other files skipped", n)
	}
	if len(b.Health.Warnings) != 1 || !strings.Contains(b.Health.Warnings[0], "applications.yaml, teams.yaml") {
		t.Errorf("warnings %v, want the two files named", b.Health.Warnings)
	}
}

// A catalog that does not parse or validate fails the run, and the failed
// batch carries nothing: applying it withdraws nothing (the engine turns a
// failed full run partial, and it has no claim anyway).
func TestBrokenCatalogFailsAndCarriesNothing(t *testing.T) {
	for name, src := range map[string]string{
		"syntax":                "format: trica-catalog\nversion: 1\napplications: [",
		"validation":            "format: trica-catalog\nversion: 1\napplications: [{name: Billing}]\n",
		"empty":                 "format: trica-catalog\nversion: 1\nteams: [{name: t}]\n",
		"wrong format":          "format: trica-inventory\nversion: 1\n",
		"owner nobody declares": "format: trica-catalog\nversion: 1\napplications: [{name: lyra, owner: lyra-team-typo}]\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "c.yaml"), []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			b := run(t, collect.Target{Kind: catalog.Kind, Name: "main", Address: dir})
			if b.Health.OK || b.Health.Error == "" {
				t.Fatalf("health %+v, want a failure", b.Health)
			}
			if len(b.Objects)+len(b.Relations) != 0 || b.Scope != sdk.ScopePartial {
				t.Errorf("a failed run carries %d objects, %d relations, scope %s", len(b.Objects), len(b.Relations), b.Scope)
			}
			if !strings.Contains(b.Health.Error, "c.yaml") && name == "syntax" {
				t.Errorf("error %q does not name the file", b.Health.Error)
			}
		})
	}
}

func TestAllowEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.yaml"), []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	tg := collect.Target{Kind: catalog.Kind, Name: "main", Address: dir}
	if err := tg.Options.Encode(map[string]any{"allow_empty": true}); err != nil {
		t.Fatal(err)
	}
	b := run(t, tg)
	if !b.Health.OK || b.Scope != sdk.ScopeFull {
		t.Errorf("health %+v, scope %s: an allowed empty catalog is a full run that withdraws everything", b.Health, b.Scope)
	}
}

const head = "format: trica-catalog\nversion: 1\n"

func TestMissingPathFails(t *testing.T) {
	for _, p := range []string{filepath.Join(t.TempDir(), "nope.yaml"), t.TempDir()} {
		b := run(t, collect.Target{Kind: catalog.Kind, Name: "main", Address: p})
		if b.Health.OK {
			t.Errorf("%s: health OK", p)
		}
	}
}

func TestNewRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		tg   collect.Target
		want string
	}{
		{"no address", collect.Target{}, "address is required"},
		{"http", collect.Target{Address: "http://git.example.org/c.yaml"}, "must be https"},
		{"bad URL", collect.Target{Address: "https://"}, "invalid address"},
		{"credentials in the URL", collect.Target{Address: "https://u:tok@git.example.org/c.yaml"}, "no credentials"},
		{"token in the query", collect.Target{Address: "https://git.example.org/c.yaml?private_token=abc"}, "no credentials"},
		{"access_token in the query", collect.Target{Address: "https://git.example.org/c.yaml?Access_Token=abc"}, "no credentials"},
		{"relative path", collect.Target{Address: "catalog.yaml"}, "absolute path"},
		{"password for a file", collect.Target{Address: "/etc/trica/c.yaml", Password: "env:X"}, "only for an https address"},
		{"bad TLS", collect.Target{Address: "https://git.example.org/c.yaml", TLS: collect.TLS{MinVersion: "9"}}, "min_version"},
		{"unknown option", optionTarget(t, map[string]any{"allowempty": true}), "allowempty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.tg.Kind, tc.tg.Name = catalog.Kind, "main"
			if _, err := catalog.New(tc.tg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want one containing %q", err, tc.want)
			}
		})
	}
	// A query that is not a credential is kept (a Git host's API takes ?ref=).
	if _, err := catalog.New(collect.Target{Kind: catalog.Kind, Name: "main", Address: "https://git.example.org/api/contents/c.yaml?ref=main"}); err != nil {
		t.Errorf("?ref= refused: %v", err)
	}
}

func optionTarget(t *testing.T, opts map[string]any) collect.Target {
	t.Helper()
	tg := collect.Target{Address: "/etc/trica/c.yaml"}
	if err := tg.Options.Encode(opts); err != nil {
		t.Fatal(err)
	}
	return tg
}

const token = "canary-catalog-token"

// writeToken puts the token in a file the target refers to.
func writeToken(t *testing.T) collect.Secret {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return collect.Secret("file:" + p)
}

func TestHTTPSCatalog(t *testing.T) {
	f := catalog.NewFakeCatalogServer()
	defer f.Close()
	f.Token = token
	tg, err := f.Target(t.TempDir(), "git", "/raw/applications.yaml", writeToken(t))
	if err != nil {
		t.Fatal(err)
	}
	b := run(t, tg)
	if !b.Health.OK {
		t.Fatalf("health %+v", b.Health)
	}
	if collecttest.Find([]*sdk.Batch{b}, "application", "name", "orion") == nil {
		t.Error("no application orion")
	}
	reqs := f.Requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Authorization != "Bearer "+token || reqs[0].RawQuery != "" {
		t.Errorf("requests %+v, want one GET with the token in the header only", reqs)
	}
	assertNoToken(t, b)
}

func TestHTTPSWithoutTokenSendsNoAuthorization(t *testing.T) {
	f := catalog.NewFakeCatalogServer()
	defer f.Close()
	tg, err := f.Target(t.TempDir(), "git", "/raw/teams.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	if b := run(t, tg); !b.Health.OK {
		t.Fatalf("health %+v", b.Health)
	}
	if r := f.Requests(); len(r) != 1 || r[0].Authorization != "" {
		t.Errorf("requests %+v", r)
	}
}

func TestHTTPSFailures(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		token            bool
		serverToken      string
	}{
		{"refused token", "/raw/applications.yaml", "401", true, "another"},
		{"not found", "/raw/nope.yaml", "404", false, ""},
		{"redirect", "/moved/applications.yaml", "redirect not followed", true, token},
		{"not a catalog", "/raw/README.md", "README.md", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := catalog.NewFakeCatalogServer()
			defer f.Close()
			f.Token = tc.serverToken
			var secret collect.Secret
			if tc.token {
				secret = writeToken(t)
			}
			tg, err := f.Target(t.TempDir(), "git", tc.path, secret)
			if err != nil {
				t.Fatal(err)
			}
			b := run(t, tg)
			if b.Health.OK || !strings.Contains(b.Health.Error, tc.want) {
				t.Fatalf("health %+v, want a failure with %q", b.Health, tc.want)
			}
			assertNoToken(t, b)
			for _, r := range f.Requests() {
				if r.Method != http.MethodGet {
					t.Errorf("%s sent", r.Method)
				}
				if strings.HasPrefix(r.Path, "/raw/") && tc.name == "redirect" {
					t.Error("the redirect was followed: the token went to another address")
				}
			}
		})
	}
}

func TestMissingTokenFileFails(t *testing.T) {
	f := catalog.NewFakeCatalogServer()
	defer f.Close()
	tg, err := f.Target(t.TempDir(), "git", "/raw/applications.yaml", collect.Secret("file:"+filepath.Join(t.TempDir(), "none")))
	if err != nil {
		t.Fatal(err)
	}
	b := run(t, tg)
	if b.Health.OK || !strings.Contains(b.Health.Error, "password") {
		t.Errorf("health %+v", b.Health)
	}
	if len(f.Requests()) != 0 {
		t.Error("a request went out without its token")
	}
}

func TestUnreachableServerFails(t *testing.T) {
	f := catalog.NewFakeCatalogServer()
	tg, err := f.Target(t.TempDir(), "git", "/raw/applications.yaml", writeToken(t))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	b := run(t, tg)
	if b.Health.OK || !strings.Contains(b.Health.Error, "GET https://") {
		t.Errorf("health %+v", b.Health)
	}
	assertNoToken(t, b)
}

// assertNoToken fails when the token appears anywhere in what the run emits:
// objects, relations, health and its error, warnings.
func assertNoToken(t *testing.T, b *sdk.Batch) {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Errorf("the token is in the batch: %s", raw)
	}
}

func TestDescriptor(t *testing.T) {
	d, ok := collect.Lookup(catalog.Kind)
	if !ok {
		t.Fatal("catalog is not registered")
	}
	if err := d.Validate(true); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.New(collect.Target{Kind: catalog.Kind, Name: "x", Address: "/etc/trica/c.yaml"})
	if err != nil || c.Kind() != catalog.Kind {
		t.Fatalf("%v %v", c, err)
	}
}
