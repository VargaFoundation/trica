package wiring_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/arista"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// wiringtest is a module this test file adds, the way a new collector
// package adds itself with one import line: nothing in the API or the UI
// knows it.
func init() {
	collect.Register(collect.Descriptor{
		Kind: "wiringtest", Title: "Wiring test module", Vendors: []string{"Acme"}, Protocol: "none",
		Summary:    schema.Label{EN: "A module only this test knows.", FR: "Un module que seul ce test connaît."},
		Deployment: collect.DeploymentTarget, Collects: []string{"device"}, ReadOnly: "It emits one device and touches nothing.",
		Address:  "anything",
		Username: collect.Credential{Use: collect.UseOptional, Meaning: "a name"},
		Secret:   collect.Credential{Use: collect.UseRequired, Meaning: "a reference"},
		Tested:   []string{"none"}, Evidence: collect.EvidenceSynthesised, Docs: "docs/reference/collectors.md",
	}, func(t collect.Target) (sdk.Collector, error) { return wiringCollector{t}, nil })
}

type wiringCollector struct{ t collect.Target }

func (wiringCollector) Kind() string { return "wiringtest" }

func (c wiringCollector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	b := sdk.NewBatch(c.t.Source(), sdk.ScopePartial)
	b.Object("d", "device").Identify("name", "wt-device").Set("role", "server")
	return emit.Emit(ctx, collect.Succeeded(b, c.t, "wiringtest", "0.0.1", started, nil))
}

// A module that is only registered, and a target of it in the targets file,
// reach the running server's API with no change to the API: the catalogue,
// the instance, its health and its configuration.
func TestServerListsCollectorModulesAndTheirTargets(t *testing.T) {
	f := arista.NewFakeArista(arista.EOS414)
	defer f.Close()
	pw := writeFile(t, "arista-password", arista.FakePassword+"\n")
	targets := targetsYAML(t, pw, map[string]*arista.FakeArista{"sw-a1": f})
	targets = writeFile(t, "targets.yaml", mustRead(t, targets)+fmt.Sprintf(`  - kind: wiringtest
    name: wt-1
    address: https://192.0.2.7
    username: ops
    password: "file:%s"
    interval: 200ms
    timeout: 10s
`, pw))
	r := newRig(t, "collect: {targets_file: "+targets+"}\n")

	_, body := r.do("GET", "/api/v1/collectors", r.readToken, "")
	var cat struct {
		Modules []struct {
			Kind       string `json:"kind"`
			Deployment string `json:"deployment"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(body), &cat); err != nil {
		t.Fatal(err, body)
	}
	var kinds []string
	for _, m := range cat.Modules {
		kinds = append(kinds, m.Kind+":"+m.Deployment)
	}
	want := "agent:agent,arista:target,kubernetes:target,maas:target,mikrotik:target,pfsense:target,redfish:target,synology:target,unifi:target,wiringtest:target"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("modules\n got %v\nwant %s", kinds, want)
	}

	// The instance appears, configured by the file and healthy once it ran.
	deadline := time.Now().Add(patience)
	var inst string
	for {
		var code int
		code, inst = r.do("GET", "/api/v1/collectors/wiringtest/instances/wt-1", r.readToken, "")
		if code == 200 && strings.Contains(inst, `"status":"ok"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wt-1 never became ok: %d %s", code, inst)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, s := range []string{`"origin":"server"`, `"address":"https://192.0.2.7"`, `"module":"wiringtest"`, `"credentials_hidden":true`} {
		if !strings.Contains(inst, s) {
			t.Errorf("reader: %s is missing from %s", s, inst)
		}
	}
	if strings.Contains(inst, "file:"+pw) || strings.Contains(inst, `"ops"`) {
		t.Errorf("a reader sees credential references: %s", inst)
	}

	// An administrator sees the references, never the value they point to.
	_, adm := r.do("GET", "/api/v1/collectors/wiringtest/instances/wt-1", r.adminToken, "")
	if !strings.Contains(adm, `"secret_ref":"file:`+pw+`"`) || !strings.Contains(adm, `"username":"ops"`) {
		t.Errorf("admin does not see the references: %s", adm)
	}
	for _, tok := range []string{r.adminToken, r.readToken} {
		for _, p := range []string{"/api/v1/collectors", "/api/v1/collectors/arista/instances", "/api/v1/collectors/wiringtest/instances", "/api/v1/health/collection"} {
			if _, b := r.do("GET", p, tok, ""); strings.Contains(b, arista.FakePassword) {
				t.Errorf("GET %s leaked the password: %s", p, b)
			}
		}
	}

	// The shipped modules show with their target too.
	_, a := r.do("GET", "/api/v1/collectors/arista/instances", r.readToken, "")
	if !strings.Contains(a, `"source":"arista:sw-a1"`) {
		t.Errorf("arista instances: %s", a)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
