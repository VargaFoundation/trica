package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("TRICA_HTTP_ADDR", "")
	t.Setenv("TRICA_LOG_LEVEL", "")
	c, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.Addr != ":8080" || c.HTTP.ShutdownTimeout != 20*time.Second || c.SweepInterval != time.Minute ||
		!*c.MigrateOnStart || c.LogLevel != "info" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadConfigFileAndEnvironment(t *testing.T) {
	p := writeFile(t, "trica.yaml", `
http: {addr: ":9000", shutdown_timeout: 5s}
sweep_interval: 2m
migrate_on_start: false
log_level: debug
policy:
  absent_after: 1h
  propose: ["mcp:*"]
`)
	t.Setenv("TRICA_HTTP_ADDR", "127.0.0.1:7000")
	t.Setenv("TRICA_LOG_LEVEL", "")
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.Addr != "127.0.0.1:7000" {
		t.Errorf("environment did not override the file: %q", c.HTTP.Addr)
	}
	if c.HTTP.ShutdownTimeout != 5*time.Second || c.SweepInterval != 2*time.Minute || *c.MigrateOnStart || c.LogLevel != "debug" {
		t.Errorf("file not applied: %+v", c)
	}
	if c.Policy.AbsentAfter != time.Hour || len(c.Policy.Propose) != 1 {
		t.Errorf("policy not applied: %+v", c.Policy)
	}
}

func TestLoadConfigRejects(t *testing.T) {
	t.Setenv("TRICA_HTTP_ADDR", "")
	t.Setenv("TRICA_LOG_LEVEL", "")
	for name, body := range map[string]string{
		"unknown field":       "htp: {addr: x}\n",
		"bad level":           "log_level: loud\n",
		"bad duration":        "sweep_interval: often\n",
		"unknown collect key": "collect: {targets: x}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeFile(t, "c.yaml", body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestCollectTargetsFile(t *testing.T) {
	t.Setenv("TRICA_HTTP_ADDR", "")
	t.Setenv("TRICA_LOG_LEVEL", "")
	c, err := LoadConfig(writeFile(t, "c.yaml", "collect: {targets_file: /etc/trica/collect/targets.yaml}\n"))
	if err != nil || c.Collect.TargetsFile != "/etc/trica/collect/targets.yaml" {
		t.Fatalf("%+v %v", c.Collect, err)
	}
	if c, err = LoadConfig(writeFile(t, "d.yaml", "log_level: info\n")); err != nil || c.Collect.TargetsFile != "" {
		t.Fatalf("collecting must be off by default: %+v %v", c.Collect, err)
	}
}

func TestDatabaseURL(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "")
	c := &Config{}
	if _, err := c.DatabaseURL(); err == nil {
		t.Fatal("no source accepted")
	}
	c.Database.URLFile = writeFile(t, "url", "  postgres://from-file\n")
	if u, err := c.DatabaseURL(); err != nil || u != "postgres://from-file" {
		t.Fatalf("file: %q %v", u, err)
	}
	t.Setenv(EnvDatabaseURL, "postgres://from-env")
	if u, _ := c.DatabaseURL(); u != "postgres://from-env" {
		t.Fatalf("environment does not take precedence: %q", u)
	}
	t.Setenv(EnvDatabaseURL, "")
	c.Database.URLFile = writeFile(t, "empty", "\n")
	if _, err := c.DatabaseURL(); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty file: %v", err)
	}
	c.Database.URLFile = filepath.Join(t.TempDir(), "absent")
	if _, err := c.DatabaseURL(); err == nil {
		t.Fatal("missing file accepted")
	}
}
