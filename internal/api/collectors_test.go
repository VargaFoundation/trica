package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// acmeOptions are the options of the test module "acme".
type acmeOptions struct {
	TokenFile  collect.Secret `yaml:"token_file" doc:"A reference to the token."`
	VLANDomain string         `yaml:"vlan_domain" default:"default" doc:"Layer-2 domain."`
	Skip       []string       `yaml:"skip" enum:"a,b" doc:"Sections not to collect."`
	APIKey     string         `yaml:"api_key" doc:"Written as a value by mistake."`
	Fabrics    map[string]int `yaml:"fabrics" doc:"A map of integers."`
}

const (
	// A password written without a prefix, a URL credential and an option
	// that is named like a secret: none may ever be shown.
	plainOptionKey = "hunter2"
	urlPassword    = "pw0rd"
)

// testTargetsYAML are the targets of the in-server test module.
const testTargetsYAML = `
- {kind: acme, name: sw-1, address: "https://192.0.2.50:8443", username: trica, password: "file:/run/secrets/acme", domain: lab,
   tls: {ca_file: /etc/trica/ca.pem}, options: {token_file: "file:/run/secrets/token", vlan_domain: lab, skip: [a]}}
- {kind: acme, name: sw-2, address: "192.0.2.51", username: trica, password: "env:ACME_PASSWORD", interval: 1m, timeout: 10s}
- {kind: acme, name: sw-3, address: "192.0.2.52"}
- {kind: acme, name: old, address: "https://u:pw0rd@192.0.2.53", username: ops, password: "zz-declared-community-0d52",
   tls: {insecure: true}, options: {api_key: hunter2}}
`

type fakeCollectors struct {
	mods    []collect.Descriptor
	targets []collect.Target
}

func (f fakeCollectors) Modules() []collect.Descriptor { return f.mods }
func (f fakeCollectors) Targets() []collect.Target     { return f.targets }

var testCollectors = func() fakeCollectors {
	var targets []collect.Target
	if err := yaml.Unmarshal([]byte(testTargetsYAML), &targets); err != nil {
		panic(err)
	}
	acme := collect.Descriptor{
		Kind: "acme", Title: "Acme Switch OS", Vendors: []string{"Acme"}, Protocol: "REST over HTTPS",
		Summary:    schema.Label{EN: "Acme switches.", FR: "Commutateurs Acme."},
		Deployment: collect.DeploymentTarget, Collects: []string{"device", "interface"}, ReadOnly: "GET only.",
		Address:  "https://host[:port]",
		Username: collect.Credential{Use: collect.UseRequired, Meaning: "read-only account"},
		Secret:   collect.Credential{Use: collect.UseRequired, Meaning: "its password"},
		Options:  acmeOptions{}, Tested: []string{"Acme OS 1.0"}, Evidence: collect.EvidenceSynthesised,
		Docs: "internal/collect/README.md",
	}
	hostkit := collect.Descriptor{
		Kind: "hostkit", Title: "Host kit", Vendors: []string{"Acme"}, Protocol: "local probes",
		Summary:    schema.Label{EN: "A host agent.", FR: "Un agent d'hôte."},
		Deployment: collect.DeploymentAgent, Collects: []string{"device"}, ReadOnly: "reads files.",
		Tested: []string{"Linux"}, Evidence: collect.EvidenceSynthesised, Docs: "docs/reference/agent.md",
	}
	for _, d := range []collect.Descriptor{acme, hostkit} {
		if err := d.Validate(d.Deployment == collect.DeploymentTarget); err != nil {
			panic(err)
		}
	}
	return fakeCollectors{mods: []collect.Descriptor{acme, hostkit}, targets: targets}
}()

// healthOf applies an empty batch of a source that carries a health record.
func (e *env) healthOf(source, target string, ok bool, errText string) {
	e.t.Helper()
	b := sdk.NewBatch(source, sdk.ScopePartial)
	b.Health = &sdk.Health{Target: target, OK: ok, Error: errText, Collector: strings.SplitN(source, ":", 2)[0], Version: "1.2.3", Items: 7, Duration: 2 * time.Second,
		Warnings: []string{"slow link"}}
	e.apply(b)
}

// withCollectorHealth seeds the health of the module "acme" and others: one
// instance of each status, a reported one, an agent and a source no module has.
func (e *env) withCollectorHealth() {
	e.t.Helper()
	// The one that stopped running a day before the others reported.
	e.healthOf("acme:old", "192.0.2.53", true, "")
	e.clk.advance(24 * time.Hour)
	e.healthOf("acme:sw-1", "not-the-name", true, "")
	e.healthOf("acme:sw-2", "192.0.2.51", false, "dial https://trica:s3cr3t-pass@192.0.2.51/api: connection refused")
	e.healthOf("acme:remote-1", "remote-1", true, "")
	e.healthOf("hostkit:node-1", "node-1", true, "")
	e.healthOf("thirdparty:x", "x", true, "")
}

type collectorsBody struct {
	Modules []struct {
		Kind        string   `json:"kind"`
		Title       string   `json:"title"`
		Deployment  string   `json:"deployment"`
		Docs        string   `json:"docs"`
		DocsURL     string   `json:"docs_url"`
		Collects    []string `json:"collects"`
		Credentials *struct {
			Username struct{ Use, Meaning string } `json:"username"`
			Secret   struct{ Use, Meaning string } `json:"secret"`
		} `json:"credentials"`
		Options []struct {
			Name, Type, Default, Description string
			ValueKind                        string `json:"value_kind"`
			Values                           []string
		} `json:"options"`
		Counts  struct{ Instances, OK, Failing, Pending, Stale int } `json:"counts"`
		Summary schema.Label                                         `json:"summary"`
	} `json:"modules"`
	Unregistered []struct {
		Source string `json:"source"`
		Module string `json:"module"`
	} `json:"unregistered"`
}

type instanceBody struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Origin string `json:"origin"`
	Status string `json:"status"`
	Config *struct {
		Address         string `json:"address"`
		IntervalSeconds int64  `json:"interval_seconds"`
		TimeoutSeconds  int64  `json:"timeout_seconds"`
		Domain          string `json:"domain"`
		TLS             struct {
			CAFile   string `json:"ca_file"`
			Insecure bool   `json:"insecure"`
		} `json:"tls"`
		Options           map[string]any `json:"options"`
		CredentialsHidden bool           `json:"credentials_hidden"`
	} `json:"config"`
	Health *struct {
		OK        bool     `json:"ok"`
		LastError string   `json:"last_error"`
		Warnings  []string `json:"warnings"`
		Module    string   `json:"module"`
	} `json:"health"`
}

func newCollectorsEnv(t *testing.T) *env {
	e := newEnv(t, reconcile.Policy{})
	e.withCollectorHealth()
	return e
}

func (e *env) instance(path, tok string) instanceBody {
	e.t.Helper()
	r := e.get(path, tok)
	wantStatus(e.t, r, 200)
	var b instanceBody
	r.json(e.t, &b)
	return b
}

func TestCollectorCatalogue(t *testing.T) {
	e := newCollectorsEnv(t)
	r := e.get("/api/v1/collectors", tokReader)
	wantStatus(t, r, 200)
	var c collectorsBody
	r.json(t, &c)
	if len(c.Modules) != 2 || c.Modules[0].Kind != "acme" || c.Modules[1].Kind != "hostkit" {
		t.Fatalf("modules: %s", r.Body)
	}
	acme, host := c.Modules[0], c.Modules[1]
	if acme.Title != "Acme Switch OS" || acme.Deployment != "target" || acme.Summary.FR != "Commutateurs Acme." {
		t.Fatalf("acme: %+v", acme)
	}
	if acme.Credentials == nil || acme.Credentials.Username.Use != "required" || acme.Credentials.Secret.Meaning != "its password" {
		t.Fatalf("acme credentials: %+v", acme.Credentials)
	}
	if host.Deployment != "agent" || host.Credentials != nil {
		t.Fatalf("an agent module has no credentials: %+v", host)
	}
	// 4 targets (sw-3 never ran) and one instance only the health knows.
	if got := acme.Counts; got.Instances != 5 || got.OK != 2 || got.Failing != 1 || got.Pending != 1 || got.Stale != 1 {
		t.Fatalf("acme counts: %+v", got)
	}
	if got := host.Counts; got.Instances != 1 || got.OK != 1 {
		t.Fatalf("hostkit counts: %+v", got)
	}
	// Options are listed from the struct, in its order.
	var names []string
	for _, o := range acme.Options {
		names = append(names, o.Name+":"+o.Type+o.ValueKind)
	}
	if strings.Join(names, ",") != "token_file:secret_reference,vlan_domain:string,skip:list,api_key:string,fabrics:mapinteger" {
		t.Fatalf("options: %v", names)
	}
	if acme.DocsURL != "https://github.com/VargaFoundation/trica/blob/main/internal/collect/README.md" {
		t.Fatalf("docs_url (version test is not a release): %s", acme.DocsURL)
	}
	// What no module claims is shown apart: the fixtures' arista collector and
	// a collector built elsewhere.
	var un []string
	for _, h := range c.Unregistered {
		if h.Module != "" {
			t.Fatalf("unregistered with a module: %+v", h)
		}
		un = append(un, h.Source)
	}
	if strings.Join(un, ",") != "arista:sw-a1,thirdparty:x" {
		t.Fatalf("unregistered: %v", un)
	}
}

func TestCollectorInstancesJoinConfigAndHealth(t *testing.T) {
	e := newCollectorsEnv(t)
	p := e.page("/api/v1/collectors/acme/instances", tokReader)
	if p.Total != 5 {
		t.Fatalf("total %d", p.Total)
	}
	var order []string
	for _, it := range p.Items {
		order = append(order, field(t, it, "name")+":"+field(t, it, "status")+":"+field(t, it, "origin"))
	}
	// Failing first, then stale, pending, ok; by name within a status.
	want := "sw-2:failing:server,old:stale:server,sw-3:pending:server,remote-1:ok:reported,sw-1:ok:server"
	if strings.Join(order, ",") != want {
		t.Fatalf("order\n got %v\nwant %s", order, want)
	}

	if p := e.page("/api/v1/collectors/acme/instances?status=failing", tokReader); p.Total != 1 || field(t, p.Items[0], "name") != "sw-2" {
		t.Fatalf("status filter: %+v", p)
	}
	if p := e.page("/api/v1/collectors/acme/instances?status=ok&limit=1&offset=1", tokReader); p.Total != 2 || len(p.Items) != 1 || field(t, p.Items[0], "name") != "sw-1" {
		t.Fatalf("paging: %+v", p)
	}
	wantProblem(t, e.get("/api/v1/collectors/acme/instances?status=sleepy", tokReader), 400, "invalid-request")

	// The health is keyed by the source, not by the target the collector says.
	sw1 := e.instance("/api/v1/collectors/acme/instances/sw-1", tokReader)
	if sw1.Source != "acme:sw-1" || sw1.Kind != "acme" || sw1.Status != "ok" || sw1.Health == nil || !sw1.Health.OK || sw1.Health.Module != "acme" {
		t.Fatalf("sw-1: %+v", sw1)
	}
	if len(sw1.Health.Warnings) != 1 {
		t.Fatalf("warnings: %+v", sw1.Health)
	}
	cfg := sw1.Config
	if cfg == nil || cfg.Address != "https://192.0.2.50:8443" || cfg.IntervalSeconds != 300 || cfg.TimeoutSeconds != 30 || cfg.Domain != "lab" || cfg.TLS.CAFile != "/etc/trica/ca.pem" {
		t.Fatalf("sw-1 config: %+v", cfg)
	}
	if cfg.Options["token_file"] != "file:/run/secrets/token" || cfg.Options["vlan_domain"] != "lab" {
		t.Fatalf("options: %v", cfg.Options)
	}
	sw2 := e.instance("/api/v1/collectors/acme/instances/sw-2", tokReader)
	if sw2.Status != "failing" || sw2.Config.IntervalSeconds != 60 || sw2.Config.TimeoutSeconds != 10 {
		t.Fatalf("sw-2: %+v", sw2)
	}
	// The credentials a collector left in its error are cleaned.
	if strings.Contains(sw2.Health.LastError, "s3cr3t-pass") || !strings.Contains(sw2.Health.LastError, "https://***@192.0.2.51") {
		t.Fatalf("last_error: %q", sw2.Health.LastError)
	}
	if sw3 := e.instance("/api/v1/collectors/acme/instances/sw-3", tokReader); sw3.Status != "pending" || sw3.Health != nil || sw3.Config == nil {
		t.Fatalf("sw-3: %+v", sw3)
	}
	if old := e.instance("/api/v1/collectors/acme/instances/old", tokReader); old.Status != "stale" || old.Health == nil || !old.Health.OK {
		t.Fatalf("old: %+v", old)
	}
	// Only its health is known of an instance that runs elsewhere.
	if rem := e.instance("/api/v1/collectors/acme/instances/remote-1", tokReader); rem.Origin != "reported" || rem.Config != nil || rem.Status != "ok" {
		t.Fatalf("remote-1: %+v", rem)
	}
	if node := e.instance("/api/v1/collectors/hostkit/instances/node-1", tokReader); node.Origin != "reported" || node.Source != "hostkit:node-1" {
		t.Fatalf("node-1: %+v", node)
	}

	// The module of one kind, and the 404s.
	r := e.get("/api/v1/collectors/hostkit", tokReader)
	wantStatus(t, r, 200)
	for _, path := range []string{
		"/api/v1/collectors/nope", "/api/v1/collectors/nope/instances", "/api/v1/collectors/nope/instances/x",
		"/api/v1/collectors/acme/instances/nope", "/api/v1/collectors/Not_A-Kind",
		"/api/v1/collectors/thirdparty/instances/x", // seen in the health, but no module of this binary
	} {
		wantProblem(t, e.get(path, tokReader), 404, "not-found")
	}
}

func TestCollectorCredentialsOnlyForAdmin(t *testing.T) {
	e := newCollectorsEnv(t)

	reader := e.instance("/api/v1/collectors/acme/instances/sw-1", tokReader)
	if reader.Config == nil || !reader.Config.CredentialsHidden {
		t.Fatalf("a reader sees the references: %+v", reader.Config)
	}
	var raw struct {
		Config map[string]any `json:"config"`
	}
	e.get("/api/v1/collectors/acme/instances/sw-1", tokReader).json(t, &raw)
	if _, ok := raw.Config["credentials"]; ok {
		t.Fatalf("a reader's answer has a credentials key: %v", raw.Config)
	}

	// The same call, as an administrator.
	r := e.get("/api/v1/collectors/acme/instances/sw-1", tokAdmin)
	wantStatus(t, r, 200)
	var adm struct {
		Config struct {
			Credentials struct {
				Username  string `json:"username"`
				SecretRef string `json:"secret_ref"`
			} `json:"credentials"`
			Hidden bool `json:"credentials_hidden"`
		} `json:"config"`
	}
	r.json(t, &adm)
	if adm.Config.Hidden || adm.Config.Credentials.Username != "trica" || adm.Config.Credentials.SecretRef != "file:/run/secrets/acme" {
		t.Fatalf("admin: %s", r.Body)
	}
	// The list carries the same.
	var list struct {
		Items []struct {
			Name   string `json:"name"`
			Config *struct {
				Credentials *struct {
					SecretRef string `json:"secret_ref"`
				} `json:"credentials"`
			} `json:"config"`
		} `json:"items"`
	}
	e.get("/api/v1/collectors/acme/instances?status=ok", tokAdmin).json(t, &list)
	found := false
	for _, it := range list.Items {
		if it.Name == "sw-1" {
			found = it.Config != nil && it.Config.Credentials != nil && it.Config.Credentials.SecretRef == "file:/run/secrets/acme"
		}
	}
	if !found {
		t.Fatalf("the admin list has no credentials: %+v", list)
	}

	// A password written without a prefix is not a reference: it shows as
	// redacted, whoever asks.
	var old struct {
		Config struct {
			Address     string `json:"address"`
			Credentials struct {
				SecretRef string `json:"secret_ref"`
			} `json:"credentials"`
			Options map[string]any `json:"options"`
		} `json:"config"`
	}
	e.get("/api/v1/collectors/acme/instances/old", tokAdmin).json(t, &old)
	if old.Config.Credentials.SecretRef != "[redacted]" {
		t.Fatalf("plain password: %q", old.Config.Credentials.SecretRef)
	}
	// An address does not carry a password, and an option named like a secret
	// shows no value.
	if old.Config.Address != "https://***@192.0.2.53" || old.Config.Options["api_key"] != "[redacted]" {
		t.Fatalf("old: %+v", old.Config)
	}

	// None of the values appears anywhere, for any token.
	for _, tok := range []string{tokAdmin, tokReader, tokMCP, tokProposer, tokApprover} {
		for _, p := range []string{"/collectors", "/collectors/acme", "/collectors/acme/instances", "/collectors/acme/instances/old",
			"/collectors/acme/instances/sw-1", "/collectors/acme/instances/sw-2", "/health/collection"} {
			body := string(e.get("/api/v1"+p, tok).Body)
			for _, v := range []string{urlPassword, plainOptionKey, secretOther, "s3cr3t-pass"} {
				if strings.Contains(body, v) {
					t.Errorf("GET %s as %s returned %q: %s", p, tok, v, snippet(body, v))
				}
			}
		}
	}
}

func TestSourceHealthNamesItsModule(t *testing.T) {
	e := newCollectorsEnv(t)
	p := e.page("/api/v1/health/collection", tokReader)
	got := map[string]string{}
	for _, it := range p.Items {
		var m map[string]any
		if err := json.Unmarshal(it, &m); err != nil {
			t.Fatal(err)
		}
		mod, _ := m["module"].(string)
		got[m["source"].(string)] = mod
	}
	for source, want := range map[string]string{
		"acme:sw-1": "acme", "acme:remote-1": "acme", "hostkit:node-1": "hostkit",
		"thirdparty:x": "", "arista:sw-a1": "", // no module of this binary claims them
	} {
		if g, ok := got[source]; !ok || g != want {
			t.Errorf("%s: module %q (present %v), want %q", source, g, ok, want)
		}
	}
}

// Without a collector source the server lists the registry alone, with no
// instance configured (a test binary registers no module).
func TestCollectorsWithoutASource(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	h := e.serve(func(c *api.Config) { c.Collectors = nil })
	rec := serveOnce(h, "GET", "/api/v1/collectors", tokReader)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var c collectorsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Modules == nil || len(c.Unregistered) == 0 {
		t.Fatalf("nothing registered, the fixtures' sources are unclaimed: %s", rec.Body)
	}
}
