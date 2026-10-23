package api_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Made-up values: they only exist to be looked for in the answers.
const (
	secretCommunity = "zz-community-4f9a1c"
	secretToken     = "zz-apitoken-77b3e0"
	secretOther     = "zz-declared-community-0d52"
	secretProposed  = "zz-proposed-community-9e61"
)

const overlayYAML = `
types:
  device:
    attributes:
      snmp_community: {kind: string, label: {en: SNMP community}}
      site_note: {kind: string, search: true}
  vpn_gateway:
    label: {en: VPN gateway}
    parents: [site]
    display: name
    attributes:
      name: {kind: string, identity: strong}
      api_token: {kind: string}
      tunnel_count: {kind: int}
relations:
  tunnels_to:
    label: {en: tunnels to}
    from: [device]
    to: [vpn_gateway]
    attributes:
      api_token: {kind: string}
      note: {kind: string}
`

func overlayRegistry(t testing.TB) *schema.Registry {
	t.Helper()
	ov, err := schema.Parse([]byte(overlayYAML), true)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := schema.Builtin().Merge(ov)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// secretEnv is an environment whose schema has sensitive-looking attributes, with
// values for them loaded through the engine from several sources, so that
// every endpoint has something to leak.
func secretEnv(t *testing.T) *env {
	e := newEnv(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	e.reg.set(overlayRegistry(t))

	b := sdk.NewBatch("agent:vpn-gw", sdk.ScopePartial)
	b.ObservedAt = e.clk.now()
	b.Object("paris", "site").Identify("code", "PAR1")
	b.Object("gw", "vpn_gateway").Identify("name", "gw-1").In("paris").Set("api_token", secretToken).Set("tunnel_count", 4)
	e.apply(b)

	// A link whose own attributes carry a secret: the relation listings must redact them too.
	lb := sdk.NewBatch("agent:vpn-links", sdk.ScopePartial)
	lb.ObservedAt = e.clk.now()
	lb.Relations = append(lb.Relations, &sdk.Relation{Type: "tunnels_to", From: byID("device", e.ids["sw"]), To: byID("vpn_gateway", e.ids["gw"]),
		Attributes: map[string]any{"api_token": secretToken, "note": "site to site"}})
	e.apply(lb)
	var stored int
	if err := e.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM relations WHERE attrs::text LIKE '%' || $1 || '%'`, secretToken).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("fixture: the secret is not on a stored relation (%d, %v)", stored, err)
	}

	e.clk.advance(1)
	c := sdk.NewBatch("arista:sw-a1", sdk.ScopePartial)
	c.ObservedAt = e.clk.now()
	c.Object("sw", "device").Identify(sdk.IDKey, e.ids["sw"]).Set("snmp_community", secretCommunity).Set("site_note", "second floor")
	e.apply(c)

	// Someone declares another value: a discrepancy on the sensitive key.
	e.clk.advance(1)
	d := sdk.NewBatch("user:alice", sdk.ScopePartial)
	d.Confidence = sdk.Declared
	d.ObservedAt = e.clk.now()
	d.Object("sw", "device").Identify(sdk.IDKey, e.ids["sw"]).Set("snmp_community", secretOther)
	e.apply(d)

	// A proposal whose batch carries a secret (it cannot come in through the API: the engine has other callers).
	p := sdk.NewBatch("mcp:assistant", sdk.ScopePartial)
	p.ObservedAt = e.clk.now()
	p.Object("sw", "device").Identify(sdk.IDKey, e.ids["sw"]).Set("snmp_community", secretProposed).Set("description", "proposed")
	res, err := e.eng.Submit(t.Context(), p, "mcp-assistant", "test")
	if err != nil {
		t.Fatal(err)
	}
	if res.ProposalID == 0 {
		t.Fatalf("no proposal queued: %+v", res)
	}
	return e
}

func TestSecretLikeAttributesNeverLeave(t *testing.T) {
	e := secretEnv(t)
	sw, gw := e.ids["sw"], e.ids["gw"]
	if gw == "" {
		t.Fatal("fixture: no gateway")
	}
	// The state is really in the database: the test would pass vacuously otherwise.
	var n int
	if err := e.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM claims WHERE value::text LIKE '%' || $1 || '%'`, secretCommunity).Scan(&n); err != nil || n == 0 {
		t.Fatalf("fixture: the community is not stored (%d, %v)", n, err)
	}

	paths := []string{
		"/objects", "/objects?type=device", "/objects?type=vpn_gateway",
		"/objects/" + sw, "/objects/" + gw,
		"/objects/" + sw + "?at=2026-03-01T09:00:00Z", "/objects/" + gw + "?at=2026-03-01T09:00:00Z",
		"/objects/" + sw + "/provenance", "/objects/" + gw + "/provenance",
		"/objects/" + sw + "/history", "/objects/" + gw + "/history", "/objects/" + sw + "/history?key=os_version",
		"/objects/" + sw + "/relations", "/objects/" + gw + "/relations",
		"/relations?type=tunnels_to", "/relations?type=mounted_in,cabled_to,connects,neighbor_of,tunnels_to",
		"/objects/" + sw + "/timeline", "/objects/" + gw + "/timeline",
		"/changes?from=2026-01-01T00:00:00Z",
		"/search?q=sw-a1", "/search?q=gw-1", "/search?q=second+floor",
		"/whohad?key=os_version&value=4.20.7M",
		"/discrepancies", "/discrepancies?status=open",
		"/proposals", "/proposals/1",
		"/schema", "/schema/types/device", "/schema/types/vpn_gateway",
		"/openapi.json", "/health/collection",
		"/collectors", "/collectors/acme", "/collectors/acme/instances", "/collectors/acme/instances/old", "/collectors/acme/instances/sw-1",
	}
	secrets := []string{secretCommunity, secretToken, secretOther, secretProposed}
	for _, p := range paths {
		for _, tok := range []string{tokAdmin, tokReader, tokMCP} {
			r := e.get("/api/v1"+p, tok)
			if r.Status >= 500 {
				t.Fatalf("GET %s: %d %s", p, r.Status, r.Body)
			}
			body := string(r.Body)
			for _, s := range secrets {
				if strings.Contains(body, s) {
					t.Errorf("GET %s as %s returned a secret value: %s", p, tok, snippet(body, s))
				}
			}
			for _, k := range []string{"snmp_community", "api_token"} {
				// A discrepancy names the attribute it is about (never its values).
				if strings.Contains(body, k) && !strings.HasPrefix(p, "/discrepancies") {
					t.Errorf("GET %s as %s names the sensitive attribute %s: %s", p, tok, k, snippet(body, k))
				}
			}
		}
	}
	// Redaction removes the secrets and nothing else around them.
	r := e.get("/api/v1/objects/"+sw, tokReader)
	attrs := r.obj(t)["attributes"].(map[string]any)
	if attrs["site_note"] != "second floor" || attrs["os_version"] == nil {
		t.Fatalf("redaction removed more than the secret: %v", attrs)
	}
	if got := e.get("/api/v1/objects/"+gw, tokReader).obj(t)["attributes"].(map[string]any)["tunnel_count"]; got != float64(4) {
		t.Fatalf("redaction removed a neighbour of the secret: %v", got)
	}
	// The discrepancy on the secret is still listed, without values.
	found := false
	for _, it := range e.page("/api/v1/discrepancies?status=open", tokReader).Items {
		if field(t, it, "key") == "snmp_community" {
			found = true
		}
	}
	if !found {
		t.Fatal("the discrepancy on the secret is hidden altogether")
	}
	// The proposal is there, and its batch and preview keep their other attributes.
	pr := e.get("/api/v1/proposals/1", tokReader)
	wantStatus(t, pr, 200)
	if !strings.Contains(string(pr.Body), "proposed") {
		t.Fatalf("redaction emptied the proposal: %s", pr.Body)
	}
	// The audit trail does not hold secrets either.
	for _, a := range e.aud.Entries() {
		for _, s := range secrets {
			if strings.Contains(a.Target, s) {
				t.Fatalf("audit entry holds a secret: %+v", a)
			}
		}
	}
}

func snippet(body, needle string) string {
	i := strings.Index(body, needle)
	lo, hi := max(0, i-60), min(len(body), i+len(needle)+60)
	return "..." + body[lo:hi] + "..."
}

func TestSensitiveAttributesCannotBeQueried(t *testing.T) {
	e := secretEnv(t)
	sw := e.ids["sw"]
	for _, p := range []string{
		"/objects/" + sw + "/history?key=snmp_community",
		"/whohad?key=snmp_community&value=" + secretCommunity,
		"/objects?q=" + url.QueryEscape("snmp_community:"+secretCommunity),
		"/objects?q=" + url.QueryEscape("snmp_community:*"),
		"/objects?q=" + url.QueryEscape("has:snmp_community"),
		"/objects?q=" + url.QueryEscape("not has:api_token"),
		"/objects?q=" + url.QueryEscape("type:device or has:api_token"),
		"/objects?sort=snmp_community",
		"/objects?sort=-api_token",
		"/search?q=" + url.QueryEscape("api_token:"+secretToken),
	} {
		r := e.get("/api/v1"+p, tokReader)
		prob := wantProblem(t, r, 403, "sensitive-attribute")
		if strings.Contains(string(r.Body), secretCommunity) || strings.Contains(prob.Detail, secretToken) {
			t.Errorf("%s: the refusal echoes the value", p)
		}
	}
	// Free text does not find a secret either.
	for _, v := range []string{secretCommunity, secretToken, secretOther, secretProposed} {
		r := e.get("/api/v1/search?q="+url.QueryEscape(v), tokReader)
		wantStatus(t, r, 200)
		if got := r.obj(t)["total"]; got != float64(0) {
			t.Errorf("free text found a secret: %v: %s", got, r.Body)
		}
	}
}

func TestSecretsAreRefusedAtTheDoor(t *testing.T) {
	e := secretEnv(t)
	sw := e.ids["sw"]
	for name, body := range map[string]string{
		"attribute": batchJSON(t, userBatch("", func(b *sdk.Batch) { b.Object("x", "device").Identify(sdk.IDKey, sw).Set("snmp_community", "x") })),
		"gateway":   batchJSON(t, userBatch("", func(b *sdk.Batch) { b.Object("x", "vpn_gateway").Identify("name", "gw-2").Set("api_token", "x") })),
		"relation": batchJSON(t, userBatch("", func(b *sdk.Batch) {
			b.Object("x", "device").Identify(sdk.IDKey, sw)
			b.Relations = append(b.Relations, &sdk.Relation{Type: "mounted_in", From: sdk.Ref("x"), To: sdk.Ref("x"), Attributes: map[string]any{"password": "x"}})
		})),
	} {
		wantProblem(t, e.do("POST", "/api/v1/batches", tokProposer, body), 400, "secret-attribute")
		wantProblem(t, e.do("POST", "/api/v1/batches/preview", tokProposer, body), 400, "secret-attribute")
		_ = name
	}
	wantProblem(t, e.do("POST", "/api/v1/saved-queries", tokProposer, `{"name":"leak","query":"api_token:x"}`), 403, "sensitive-attribute")
}

func TestSensitiveNames(t *testing.T) {
	for name, want := range map[string]bool{
		"password": true, "Password": true, "ssh_password": true, "passwd": true, "passphrase": true,
		"snmp_community": true, "SNMP_Community": true, "api_token": true, "token": true, "apikey": true, "api_key": true,
		"private_key": true, "privateKey": true, "wifi_psk": true, "client_secret": true, "credentials": true,
		"name": false, "hostname": false, "mgmt_ip": false, "serial": false, "role": false, "os_version": false,
		"description": false, "vendor": false, "oper_status": false, "tunnel_count": false, "": false,
	} {
		if got := api.Sensitive(name); got != want {
			t.Errorf("Sensitive(%q) = %v, want %v", name, got, want)
		}
	}
}

// The name filter is an over-approximation: it must not swallow an attribute
// of the model itself, or the API would silently hide inventory.
func TestNoBuiltinAttributeLooksSensitive(t *testing.T) {
	reg := schema.Builtin()
	for tn, ty := range reg.Types {
		for an := range ty.Attributes {
			if api.Sensitive(an) {
				t.Errorf("built-in attribute %s.%s would be hidden by the API", tn, an)
			}
		}
	}
	for rn, rel := range reg.Relations {
		if api.Sensitive(rn) {
			t.Errorf("built-in relation %s would be hidden", rn)
		}
		for an := range rel.Attributes {
			if api.Sensitive(an) {
				t.Errorf("built-in relation attribute %s.%s would be hidden", rn, an)
			}
		}
	}
}
