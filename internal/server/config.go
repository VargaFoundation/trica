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
}

func (c *Config) validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log_level %q: want debug, info, warn or error", c.LogLevel)
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
