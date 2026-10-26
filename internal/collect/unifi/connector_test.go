package unifi

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const consoleID = "70A7413E7B4A0000000000000000000000000000A1B2:1234567890"

// viaCloud points the only host a connector target may name at the fake for
// the length of the test (tests of the connector must not run in parallel).
func viaCloud(t *testing.T, f *FakeNetwork) {
	t.Helper()
	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatal(err)
	}
	old := cloudHost
	cloudHost = u.Host
	t.Cleanup(func() { cloudHost = old })
}

func runConnector(t *testing.T, f *FakeNetwork) *sdk.Batch {
	t.Helper()
	viaCloud(t, f)
	tg := withOptions(t, f.ConnectorTarget(office, consoleID), "api: network\nconsole: \""+consoleID+"\"\n")
	bs := collecttest.Run(t, newCollector(t, tg))
	if len(bs) != 1 {
		t.Fatalf("got %d batches, want 1", len(bs))
	}
	return bs[0]
}

func TestConnectorPathIsBuiltAndEscaped(t *testing.T) {
	tg := collect.Target{Kind: Kind, Name: office, Address: "https://api.ui.com"}
	for _, address := range []string{"", "https://api.ui.com", "https://API.ui.com/", "api.ui.com"} {
		tg.Address = address
		tg = withOptions(t, tg, "api: network\nconsole: \""+consoleID+"\"\n")
		c, err := New(tg)
		if err != nil {
			t.Fatalf("address %q: %v", address, err)
		}
		got := c.(*collector).base
		want := "/v1/connector/consoles/" + url.PathEscape(consoleID) + "/proxy/network/integration"
		if got.Scheme != "https" || !strings.EqualFold(got.Host, "api.ui.com") || got.Path != want {
			t.Errorf("address %q: base %s, want path %s", address, got, want)
		}
		if p := got.JoinPath(epNetInfo).String(); !strings.HasSuffix(p, want+"/v1/info") {
			t.Errorf("request URL %s", p)
		}
	}
}

func TestConnectorRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		address, opts, want string
	}{
		"another host":            {"https://192.0.2.1", "api: network\nconsole: abc\n", "must be https://api.ui.com"},
		"look-alike host":         {"https://api.ui.com.example.net", "api: network\nconsole: abc\n", "must be https://api.ui.com"},
		"another port":            {"https://api.ui.com:8443", "api: network\nconsole: abc\n", "must be https://api.ui.com"},
		"a path":                  {"https://api.ui.com/v1", "api: network\nconsole: abc\n", "must be https://api.ui.com"},
		"plain http":              {"http://api.ui.com", "api: network\nconsole: abc\n", "must be https"},
		"no api":                  {"", "console: abc\n", "applies to api: network"},
		"site manager explicitly": {"", "api: site-manager\nconsole: abc\n", "applies to api: network"},
		"slash in the id":         {"", "api: network\nconsole: ../x\n", "not a console host id"},
		"percent in the id":       {"", "api: network\nconsole: \"a%2fb\"\n", "not a console host id"},
		"too long":                {"", "api: network\nconsole: " + strings.Repeat("a", 201) + "\n", "not a console host id"},
		"hosts with console":      {"", "api: network\nconsole: abc\nhosts: [x]\n", "options.hosts"},
	} {
		tg := withOptions(t, collect.Target{Kind: Kind, Name: office, Address: tc.address}, tc.opts)
		_, err := New(tg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", name, err, tc.want)
		}
	}
	// Without console, a LAN target still needs its address and refuses a path.
	if _, err := New(withOptions(t, collect.Target{Kind: Kind, Name: office}, "api: network\n")); err == nil {
		t.Error("api: network without address nor console accepted")
	}
}

func TestConnectorRunGivesTheLANBatch(t *testing.T) {
	lan := runNet(t, newFakeNet(t, SetNetwork))

	f := newFakeNet(t, SetNetwork)
	via := runConnector(t, f)
	if !via.Health.OK || via.Scope != lan.Scope || len(via.Health.Warnings) != 0 {
		t.Fatalf("health %+v, scope %s", via.Health, via.Scope)
	}
	if len(via.Objects) != len(lan.Objects) || len(via.Relations) != len(lan.Relations) {
		t.Fatalf("%d objects / %d relations through the connector, %d / %d from the LAN",
			len(via.Objects), len(via.Relations), len(lan.Objects), len(lan.Relations))
	}
	for _, o := range lan.Objects {
		got := byRef(t, via, o.Ref)
		if !reflect.DeepEqual(got, o) {
			t.Errorf("%s differs through the connector:\n%+v\n%+v", o.Ref, got, o)
		}
	}
	// Same four shapes, GET only, never the clients, all under the connector.
	if err := f.OnlyReads(); err != nil {
		t.Fatal(err)
	}
	reqs := f.Requests()
	if len(reqs) != 13 {
		t.Errorf("%d requests, want 13", len(reqs))
	}
	for _, r := range reqs {
		if !r.Prefixed || r.Console != consoleID || !r.HasKey || !netPaths.MatchString(r.Path) || strings.Contains(r.Path, "client") {
			t.Errorf("request %+v", r)
		}
	}
}

func TestConnectorKeyOfANonOwner(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	f.NotOwner()
	b := runConnector(t, f)
	if b.Health.OK || len(b.Objects) != 0 {
		t.Fatalf("health %+v, %d objects", b.Health, len(b.Objects))
	}
	for _, want := range []string{"HTTP 403", "OWNER", "not the owner of this host"} {
		if !strings.Contains(b.Health.Error, want) {
			t.Errorf("error %q lacks %q", b.Health.Error, want)
		}
	}
	if n := len(f.Requests()); n != 1 {
		t.Errorf("%d requests after a refusal, want 1", n)
	}
	// A 403 that is not the owner one keeps the generic text.
	g := newFakeNet(t, SetNetwork)
	g.Fail("/v1/info", 403)
	b = runConnector(t, g)
	if strings.Contains(b.Health.Error, "OWNER") || !strings.Contains(b.Health.Error, "HTTP 403") {
		t.Errorf("error %q", b.Health.Error)
	}
}

func TestConnectorErrors(t *testing.T) {
	f := newFakeNet(t, SetNetwork)
	f.SetKey("another-key")
	if b := runConnector(t, f); b.Health.OK || !strings.Contains(b.Health.Error, "authentication refused") {
		t.Errorf("401: %+v", b.Health)
	}
	g := newFakeNet(t, SetNetwork)
	viaCloud(t, g)
	tg := withOptions(t, g.ConnectorTarget(office, "AABBCC:1"), "api: network\nconsole: \""+consoleID+"\"\n")
	b := collecttest.Run(t, newCollector(t, tg))[0]
	if b.Health.OK || !strings.Contains(b.Health.Error, "is unknown to the cloud") || !strings.Contains(b.Health.Error, "HTTP 404") {
		t.Errorf("404: %+v", b.Health)
	}
	h := newFakeNet(t, SetNetwork)
	h.Fail("/v1/info", 429)
	if b := runConnector(t, h); b.Health.OK || !strings.Contains(b.Health.Error, "rate limited") {
		t.Errorf("429: %+v", b.Health)
	}
}
