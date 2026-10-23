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

// The operator's device-type directory: from the file or the environment,
// and a start refused when it is not a directory, rather than a library
// that silently lacks the operator's models.
func TestDeviceTypesExtraDir(t *testing.T) {
	t.Setenv("TRICA_HTTP_ADDR", "")
	t.Setenv("TRICA_LOG_LEVEL", "")
	t.Setenv("TRICA_DEVICETYPES_EXTRA_DIR", "")
	dir := t.TempDir()
	c, err := LoadConfig(writeFile(t, "c.yaml", "devicetypes: {extra_dir: "+dir+"}\n"))
	if err != nil || c.DeviceTypes.ExtraDir != dir {
		t.Fatalf("%+v %v", c.DeviceTypes, err)
	}
	other := t.TempDir()
	t.Setenv("TRICA_DEVICETYPES_EXTRA_DIR", other)
	if c, err = LoadConfig(""); err != nil || c.DeviceTypes.ExtraDir != other {
		t.Fatalf("environment: %+v %v", c.DeviceTypes, err)
	}
	t.Setenv("TRICA_DEVICETYPES_EXTRA_DIR", "")
	for _, bad := range []string{filepath.Join(dir, "missing"), writeFile(t, "file", "x")} {
		if _, err := LoadConfig(writeFile(t, "d.yaml", "devicetypes: {extra_dir: "+bad+"}\n")); err == nil || !strings.Contains(err.Error(), "extra_dir") {
			t.Errorf("%s: %v, want a refusal", bad, err)
		}
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

func TestLiveBlock(t *testing.T) {
	t.Setenv("TRICA_HTTP_ADDR", "")
	t.Setenv("TRICA_LOG_LEVEL", "")
	c, err := LoadConfig("")
	if err != nil || c.Live != nil {
		t.Fatalf("without a live block the overlay is off: %v %+v", err, c.Live)
	}
	p := writeFile(t, "trica.yaml", `
live:
  url: https://metrics.example.net/prometheus
  username: trica
  password: file:/run/secrets/live/password
  refresh: 1m
  tls: {ca_file: /etc/trica/ca.pem}
  sources:
    - kind: arista
      matchers: {job: arista-exporter}
    - kind: generic
      name: snmp
      metrics: {rx: ifHCInOctets, tx: ifHCOutOctets, oper: "-"}
      labels: {device: [instance], interface: ifName}
`)
	c, err = LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Live == nil || c.Live.Refresh != time.Minute || c.Live.Timeout != 5*time.Second || len(c.Live.Sources) != 2 {
		t.Fatalf("live block: %+v", c.Live)
	}
	bad := writeFile(t, "bad.yaml", "live:\n  url: https://metrics.example.net\n  password: hunter2\n  sources: [{kind: node}]\n")
	if _, err := LoadConfig(bad); err == nil || !strings.Contains(err.Error(), "live.password") {
		t.Fatalf("a password value stops the start: %v", err)
	}
	typo := writeFile(t, "typo.yaml", "live:\n  url: https://metrics.example.net\n  sources: [{kind: node, metric: {rx: x}}]\n")
	if _, err := LoadConfig(typo); err == nil {
		t.Fatal("an unknown key in the live block stops the start")
	}
}

func TestWebThemeConfig(t *testing.T) {
	dir := t.TempDir()
	good := map[string]string{
		"builtin": "web: {theme: {builtin: flat}}",
		"path":    "web: {theme: {path: " + dir + ", reload: 30s}}",
	}
	for name, yaml := range good {
		t.Run(name, func(t *testing.T) {
			c, err := LoadConfig(writeFile(t, "c.yaml", yaml))
			if err != nil {
				t.Fatal(err)
			}
			if c.Web.Theme == nil || c.Web.Theme.Reload == 0 {
				t.Fatalf("theme: %+v", c.Web.Theme)
			}
		})
	}
	if c, _ := LoadConfig(writeFile(t, "c.yaml", "web: {theme: {path: "+dir+"}}")); c.Web.Theme.Reload != time.Minute {
		t.Errorf("reload defaults to %v, want 1m", c.Web.Theme.Reload)
	}
	bad := map[string]struct{ yaml, want string }{
		"two sources":       {"web: {theme: {builtin: flat, path: " + dir + "}}", "not both"},
		"no source":         {"web: {theme: {}}", "needs builtin or path"},
		"unknown built-in":  {"web: {theme: {builtin: neon}}", "not a built-in theme"},
		"missing path":      {"web: {theme: {path: " + dir + "/none}}", "web.theme.path"},
		"negative reload":   {"web: {theme: {path: " + dir + ", reload: -1s}}", "negative"},
		"unknown key":       {"web: {theme: {builtin: flat, locked: true}}", "locked"},
		"unknown key (url)": {"web: {theme: {url: https://x/t.zip}}", "url"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfig(writeFile(t, "c.yaml", c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want it to mention %q", err, c.want)
			}
		})
	}
}
