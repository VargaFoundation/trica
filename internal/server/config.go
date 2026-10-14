// Package server assembles a trica server: configuration, the store, the
// reconciliation engine, the HTTP listener and the background loops. The
// API, the MCP server, the transport and the deducer are components mounted
// on it; this package only owns their lifecycle.
package server

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/reconcile"
)

// Config is the server configuration, read from a YAML file and overridden
// by environment variables. It holds references to secrets, never secrets:
// the database URL comes from a file or the environment.
type Config struct {
	Database DatabaseConfig `yaml:"database"`
	HTTP     HTTPConfig     `yaml:"http"`
	// Policy is the reconciliation policy: source priorities, proposals,
	// absence delay (ADR 0004, 0005, 0007).
	Policy reconcile.Policy `yaml:"policy"`
	// SweepInterval is how often absent objects are swept. Zero means one
	// minute.
	SweepInterval time.Duration `yaml:"sweep_interval"`
	// SchemaOverlays are YAML files with custom types and attributes merged
	// over the built-in schema at start.
	SchemaOverlays []string `yaml:"schema_overlays"`
	// MigrateOnStart applies pending migrations before serving. Defaults to
	// true; turn it off when migrations run as a separate job.
	MigrateOnStart *bool `yaml:"migrate_on_start"`
	// LogLevel is debug, info, warn or error.
	LogLevel string `yaml:"log_level"`

	// Auth configures authentication and authorisation of the API and the MCP
	// server (ADR 0013, 0025). API tokens are always accepted; OIDC only when
	// an issuer is configured.
	Auth AuthConfig `yaml:"auth"`
	// MCP configures the Model Context Protocol endpoint, mounted at /mcp.
	MCP MCPConfig `yaml:"mcp"`
	// Transport configures the mutual-TLS ingestion endpoint of agents and
	// collectors. Disabled unless transport.addr is set.
	Transport TransportConfig `yaml:"transport"`
	// Deduce configures the cabling deducer loop.
	Deduce DeduceConfig `yaml:"deduce"`
	// Web configures the embedded UI, mounted at /.
	Web WebConfig `yaml:"web"`
}

// AuthConfig is the "auth" section. It holds references (a file), never
// secrets: the OIDC verifier needs only public keys, found through discovery.
type AuthConfig struct {
	// OIDC enables bearer ID/access tokens of one identity provider. Nil: only
	// API tokens authenticate.
	OIDC *OIDCConfig `yaml:"oidc"`
	// PolicyFile is a YAML RBAC policy layered on the default roles
	// (internal/auth ParsePolicy): extra roles and group_roles.
	PolicyFile string `yaml:"policy_file"`
	// GroupRoles maps an OIDC group to roles, in addition to the policy file's
	// own group_roles (a group in both takes the union). The roles must exist.
	GroupRoles map[string][]string `yaml:"group_roles"`
	// RateLimit bounds requests per principal (per peer address before
	// authentication) on the API and the MCP endpoint.
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

// OIDCConfig is the server side of single sign-on.
type OIDCConfig struct {
	// Issuer is the exact `iss`, an https URL. Discovery runs at start; the
	// server does not start when it fails.
	Issuer string `yaml:"issuer"`
	// Audience lists the accepted `aud` values. Required.
	Audience []string `yaml:"audience"`
	// GroupsClaim holds the user's groups; "groups" when empty.
	GroupsClaim string `yaml:"groups_claim"`
}

// RateLimitConfig is a token bucket per principal.
type RateLimitConfig struct {
	// Rate is requests per second, sustained. Zero means 20.
	Rate float64 `yaml:"rate"`
	// Burst is how many requests may arrive at once. Zero means 60.
	Burst int `yaml:"burst"`
}

// MCPConfig is the "mcp" section.
type MCPConfig struct {
	// Disabled leaves /mcp unmounted.
	Disabled bool `yaml:"disabled"`
	// GraphRefresh is how often the dependency graph is marked stale, so that
	// a replica sees commits made by another one. Commits on this replica
	// invalidate it at once. Zero means one minute.
	GraphRefresh time.Duration `yaml:"graph_refresh"`
	// DisableLocalhostProtection turns off the MCP SDK's Host-header check for
	// a server listening on a loopback address behind a same-host proxy.
	DisableLocalhostProtection bool `yaml:"disable_localhost_protection"`
}

// TransportConfig is the "transport" section (ADR 0008, 0025).
type TransportConfig struct {
	// Addr is the listen address of the mTLS endpoint, its own port apart
	// from http.addr. Empty disables the transport.
	Addr string `yaml:"addr"`
	// CADir holds ca.pem and ca-key.pem: a directory on a persistent mounted
	// volume. The CA is created there on the first start, and never replaced.
	CADir string `yaml:"ca_dir"`
	// Hosts are the names and IPs the server certificate is valid for: what
	// agents dial. Required when the transport is enabled.
	Hosts []string `yaml:"hosts"`
	// CertTTL is the validity of client certificates; seven days when zero.
	CertTTL time.Duration `yaml:"cert_ttl"`
	// ServerCertTTL is the validity of the server certificate, issued at
	// start and renewed (the listener is re-opened) after two thirds of it;
	// 90 days when zero.
	ServerCertTTL time.Duration `yaml:"server_cert_ttl"`
}

// DeduceConfig is the "deduce" section.
type DeduceConfig struct {
	// Disabled stops the cabling deducer.
	Disabled bool `yaml:"disabled"`
	// Interval between passes; five minutes when zero. A moved cable must show
	// within 15 minutes, collection interval included.
	Interval time.Duration `yaml:"interval"`
}

// WebConfig is the "web" section.
type WebConfig struct {
	// Disabled leaves / unmounted.
	Disabled bool `yaml:"disabled"`
	// OIDC enables "Sign in with SSO" in the browser (public client, PKCE).
	OIDC *WebOIDCConfig `yaml:"oidc"`
}

// WebOIDCConfig is what the browser learns from /ui-config.json.
type WebOIDCConfig struct {
	// Issuer defaults to auth.oidc.issuer.
	Issuer string `yaml:"issuer"`
	// ClientID is the public client registered for the UI. No secret.
	ClientID string `yaml:"client_id"`
	// Scopes requested besides openid.
	Scopes []string `yaml:"scopes"`
	// Bearer is access_token (default) or id_token: the token sent to the API.
	Bearer string `yaml:"bearer"`
}

// DatabaseConfig locates PostgreSQL.
type DatabaseConfig struct {
	// URLFile is a file holding the connection URL (a mounted Kubernetes
	// Secret). TRICA_DATABASE_URL takes precedence over it.
	URLFile string `yaml:"url_file"`
}

// HTTPConfig configures the listener.
type HTTPConfig struct {
	Addr            string        `yaml:"addr"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// EnvDatabaseURL is the environment variable holding the database URL.
const EnvDatabaseURL = "TRICA_DATABASE_URL"

// LoadConfig reads path (empty: defaults only) and applies the environment.
func LoadConfig(path string) (*Config, error) {
	c := &Config{}
	if path != "" {
		raw, err := os.ReadFile(path) //nolint:gosec // the operator names the configuration file
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(c); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	}
	if v := os.Getenv("TRICA_HTTP_ADDR"); v != "" {
		c.HTTP.Addr = v
	}
	if v := os.Getenv("TRICA_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv("TRICA_TRANSPORT_ADDR"); v != "" {
		c.Transport.Addr = v
	}
	if v := os.Getenv("TRICA_OIDC_ISSUER"); v != "" {
		if c.Auth.OIDC == nil {
			c.Auth.OIDC = &OIDCConfig{}
		}
		c.Auth.OIDC.Issuer = v
	}
	c.defaults()
	return c, c.validate()
}

func (c *Config) defaults() {
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = ":8080"
	}
	if c.HTTP.ShutdownTimeout <= 0 {
		c.HTTP.ShutdownTimeout = 20 * time.Second
	}
	if c.SweepInterval <= 0 {
		c.SweepInterval = time.Minute
	}
	if c.MigrateOnStart == nil {
		t := true
		c.MigrateOnStart = &t
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.Auth.RateLimit.Rate <= 0 {
		c.Auth.RateLimit.Rate = 20
	}
	if c.Auth.RateLimit.Burst <= 0 {
		c.Auth.RateLimit.Burst = 60
	}
	if c.MCP.GraphRefresh <= 0 {
		c.MCP.GraphRefresh = time.Minute
	}
	if c.Transport.ServerCertTTL <= 0 {
		c.Transport.ServerCertTTL = 90 * 24 * time.Hour
	}
	if c.Deduce.Interval <= 0 {
		c.Deduce.Interval = 5 * time.Minute
	}
	if c.Web.OIDC != nil && c.Web.OIDC.Issuer == "" && c.Auth.OIDC != nil {
		c.Web.OIDC.Issuer = c.Auth.OIDC.Issuer
	}
}

func (c *Config) validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log_level %q: want debug, info, warn or error", c.LogLevel)
	}
	if o := c.Auth.OIDC; o != nil && (o.Issuer == "" || len(o.Audience) == 0) {
		return errors.New("config: auth.oidc needs an issuer and at least one audience")
	}
	if t := c.Transport; t.Addr != "" && (t.CADir == "" || len(t.Hosts) == 0) {
		return errors.New("config: transport.addr needs transport.ca_dir and transport.hosts")
	}
	if w := c.Web.OIDC; w != nil && (w.Issuer == "" || w.ClientID == "") {
		return errors.New("config: web.oidc needs a client_id and an issuer (or auth.oidc.issuer)")
	}
	return nil
}

// DatabaseURL returns the connection URL from the environment or the file.
// The value is a secret: callers must not log it.
func (c *Config) DatabaseURL() (string, error) {
	if v := os.Getenv(EnvDatabaseURL); v != "" {
		return v, nil
	}
	if c.Database.URLFile == "" {
		return "", errors.New("config: no database URL: set " + EnvDatabaseURL + " or database.url_file")
	}
	raw, err := os.ReadFile(c.Database.URLFile)
	if err != nil {
		return "", fmt.Errorf("config: database.url_file: %w", err)
	}
	url := strings.TrimSpace(string(raw))
	if url == "" {
		return "", fmt.Errorf("config: database.url_file %s is empty", c.Database.URLFile)
	}
	return url, nil
}
