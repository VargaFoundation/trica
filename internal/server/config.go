// Package server assembles a trica server: configuration, the store, the
// reconciliation engine, the HTTP listener and the background loops. The
// API, the MCP server, the transport and the deducer are components mounted
// on it; this package only owns their lifecycle.
package server

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/theme"
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
	// Collect runs collectors inside the server (ADR 0026).
	Collect CollectConfig `yaml:"collect"`
	// Audit configures what the audit log records of refusals, and the
	// anchoring of its head (ADR 0013, 0034).
	Audit AuditConfig `yaml:"audit"`
	// Live overlays live link measurements, read from a Prometheus-compatible
	// endpoint, on the topology (ADR 0041). Nil (no live block) disables it:
	// GET /api/v1/topology/live then answers enabled: false.
	Live *live.Config `yaml:"live"`
	// DeviceTypes configures the device-type library (ADR 0020, 0042).
	DeviceTypes DeviceTypesConfig `yaml:"devicetypes"`
}

// DeviceTypesConfig is the "devicetypes" section.
type DeviceTypesConfig struct {
	// ExtraDir is a directory of the operator's own models and face images,
	// in the layout of the embedded library, read at start and laid over it:
	// a model or an image found there wins over the embedded one
	// (docs/reference/devicetypes.md). Empty: the embedded library only.
	ExtraDir string `yaml:"extra_dir"`
}

// AuditConfig is the "audit" section.
type AuditConfig struct {
	// AnchorInterval is how often the head of the audit chain is logged and
	// published as a metric, out of the database's reach. Five minutes when
	// zero.
	AnchorInterval time.Duration `yaml:"anchor_interval"`
	// RateLimitedEvery is how often a 429 is recorded per caller; the others
	// are counted in the next entry. One minute when zero.
	RateLimitedEvery time.Duration `yaml:"rate_limited_every"`
}

// CollectConfig is the "collect" section: collectors run by the server itself,
// for targets it can reach. Sites it cannot reach run `trica collector`.
type CollectConfig struct {
	// TargetsFile is a YAML file listing the targets (docs/reference/collectors.md).
	// Empty disables in-server collection. Credentials in it are file: or env:
	// references.
	TargetsFile string `yaml:"targets_file"`
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
	// GraphRefresh is how often the dependency graph is marked stale anyway.
	// Commits invalidate it at once, on this replica through OnCommit and on
	// the others through a PostgreSQL notification; the timer covers a
	// notification lost while a listener reconnects. Zero means one minute.
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
	// start and renewed after two thirds of it, in place: the listener and
	// the open connections are kept. 90 days when zero.
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
	// Theme picks the look of the console (ADR 0054). Absent, the built-in
	// "default" is in force.
	Theme *WebThemeConfig `yaml:"theme"`
}

// WebThemeConfig is the "web.theme" section: exactly one of Builtin and Path.
type WebThemeConfig struct {
	// Builtin names a theme shipped in the binary (default, flat).
	Builtin string `yaml:"builtin"`
	// Path is a theme archive (.zip) or a directory with the same layout, for
	// instance a mounted ConfigMap. It is read again every Reload.
	Path string `yaml:"path"`
	// Reload is how often Path is read again; zero means one minute.
	Reload time.Duration `yaml:"reload"`
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
	if v := os.Getenv("TRICA_DEVICETYPES_EXTRA_DIR"); v != "" {
		c.DeviceTypes.ExtraDir = v
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
	if c.Audit.AnchorInterval <= 0 {
		c.Audit.AnchorInterval = 5 * time.Minute
	}
	if c.Audit.RateLimitedEvery <= 0 {
		c.Audit.RateLimitedEvery = time.Minute
	}
	if c.Web.OIDC != nil && c.Web.OIDC.Issuer == "" && c.Auth.OIDC != nil {
		c.Web.OIDC.Issuer = c.Auth.OIDC.Issuer
	}
	if t := c.Web.Theme; t != nil && t.Reload == 0 {
		t.Reload = theme.DefaultReload
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
	if d := c.DeviceTypes.ExtraDir; d != "" {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return fmt.Errorf("config: devicetypes.extra_dir %q is not a readable directory", d)
		}
	}
	if w := c.Web.OIDC; w != nil && (w.Issuer == "" || w.ClientID == "") {
		return errors.New("config: web.oidc needs a client_id and an issuer (or auth.oidc.issuer)")
	}
	if t := c.Web.Theme; t != nil {
		if err := validateTheme(t); err != nil {
			return err
		}
	}
	if c.Live != nil {
		if err := c.Live.Validate(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	return nil
}

// validateTheme refuses what the server could not honour: a start with a
// configuration that names a theme it cannot find is a mistake to report, not
// to hide behind the default (a theme that fails later, while running, is
// another matter and never stops the server).
func validateTheme(t *WebThemeConfig) error {
	switch {
	case t.Builtin != "" && t.Path != "":
		return errors.New("config: web.theme takes builtin or path, not both")
	case t.Builtin == "" && t.Path == "":
		return errors.New("config: web.theme needs builtin or path")
	case t.Reload < 0:
		return errors.New("config: web.theme.reload must not be negative")
	case t.Builtin != "" && !slices.Contains(theme.BuiltinNames(), t.Builtin):
		return fmt.Errorf("config: web.theme.builtin %q is not a built-in theme (%s)", t.Builtin, strings.Join(theme.BuiltinNames(), ", "))
	}
	if t.Path != "" {
		if _, err := os.Stat(t.Path); err != nil {
			return fmt.Errorf("config: web.theme.path: %w", err)
		}
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
