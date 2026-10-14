package server

import (
	"testing"
	"time"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TRICA_HTTP_ADDR", "TRICA_LOG_LEVEL", "TRICA_TRANSPORT_ADDR", "TRICA_OIDC_ISSUER"} {
		t.Setenv(k, "")
	}
}

func TestLoadConfigComponents(t *testing.T) {
	clearConfigEnv(t)
	c, err := LoadConfig(writeFile(t, "c.yaml", `
auth:
  oidc: {issuer: "https://idp.example.test", audience: [trica]}
  group_roles: {ops: [operator]}
  rate_limit: {rate: 5, burst: 10}
mcp: {graph_refresh: 30s}
transport: {addr: ":8443", ca_dir: /var/lib/trica/ca, hosts: [trica.example.test]}
deduce: {interval: 10m}
web: {oidc: {client_id: trica-ui}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth.OIDC.Issuer != "https://idp.example.test" || c.Auth.RateLimit.Rate != 5 || c.Auth.RateLimit.Burst != 10 ||
		c.MCP.GraphRefresh != 30*time.Second || c.Transport.Addr != ":8443" || c.Deduce.Interval != 10*time.Minute {
		t.Fatalf("not applied: %+v", c)
	}
	if c.Web.OIDC.Issuer != c.Auth.OIDC.Issuer {
		t.Errorf("web.oidc.issuer must default to auth.oidc.issuer: %q", c.Web.OIDC.Issuer)
	}
	if c.Transport.ServerCertTTL != 90*24*time.Hour {
		t.Errorf("server_cert_ttl default: %v", c.Transport.ServerCertTTL)
	}

	d, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if d.Auth.RateLimit.Rate != 20 || d.Auth.RateLimit.Burst != 60 || d.Deduce.Interval != 5*time.Minute ||
		d.MCP.GraphRefresh != time.Minute || d.Transport.Addr != "" || d.Auth.OIDC != nil {
		t.Fatalf("defaults: %+v", d)
	}

	t.Setenv("TRICA_TRANSPORT_ADDR", ":9443")
	t.Setenv("TRICA_OIDC_ISSUER", "https://other.example.test")
	e, err := LoadConfig(writeFile(t, "e.yaml", "transport: {ca_dir: /ca, hosts: [h]}\nauth: {oidc: {issuer: x, audience: [a]}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Transport.Addr != ":9443" || e.Auth.OIDC.Issuer != "https://other.example.test" {
		t.Fatalf("environment: %+v", e)
	}
}

func TestLoadConfigRejectsComponentMistakes(t *testing.T) {
	clearConfigEnv(t)
	for name, body := range map[string]string{
		"oidc without audience":      "auth: {oidc: {issuer: \"https://idp.example.test\"}}\n",
		"transport without ca_dir":   "transport: {addr: \":8443\", hosts: [h]}\n",
		"transport without hosts":    "transport: {addr: \":8443\", ca_dir: /ca}\n",
		"web oidc without client_id": "web: {oidc: {issuer: \"https://idp.example.test\"}}\n",
		"unknown auth key":           "auth: {anonymous: true}\n",
		"unknown transport key":      "transport: {insecure: true}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeFile(t, "c.yaml", body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
