package collect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestSecretResolve(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "pw")
	if err := os.WriteFile(f, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRICA_TEST_SECRET", "from-env")

	if v, err := Secret("file:" + f).Resolve(); err != nil || v != "s3cret" {
		t.Fatalf("file: got %q, %v", v, err)
	}
	if v, err := Secret("env:TRICA_TEST_SECRET").Resolve(); err != nil || v != "from-env" {
		t.Fatalf("env: got %q, %v", v, err)
	}
	if _, err := Secret("").Resolve(); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := Secret("env:TRICA_TEST_UNSET_SECRET").Resolve(); err == nil {
		t.Fatal("unset env accepted")
	}
	if _, err := Secret("file:" + filepath.Join(dir, "missing")).Resolve(); err == nil {
		t.Fatal("missing file accepted")
	}
	// A bare value is refused and never echoed back.
	_, err := Secret("hunter2").Resolve()
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("bare secret: %v", err)
	}
}

func TestSecretStringNeverShowsAValue(t *testing.T) {
	for in, want := range map[string]string{
		"file:/run/pw": "file:/run/pw",
		"env:PW":       "env:PW",
		"hunter2":      "[redacted]",
		"":             "",
	} {
		if got := Secret(in).String(); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

type nop struct{ t Target }

func (n nop) Kind() string                               { return n.t.Kind }
func (n nop) Collect(context.Context, sdk.Emitter) error { return nil }

func TestRegistry(t *testing.T) {
	Register("test-nop", func(t Target) (sdk.Collector, error) { return nop{t}, nil })
	c, err := New(Target{Kind: "test-nop", Name: "a"})
	if err != nil || c.Kind() != "test-nop" {
		t.Fatalf("New: %v, %v", c, err)
	}
	if _, err := New(Target{Kind: "nope", Name: "a"}); err == nil || !strings.Contains(err.Error(), "test-nop") {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := New(Target{Kind: "test-nop"}); err == nil {
		t.Fatal("target without name accepted")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate kind accepted")
		}
	}()
	Register("test-nop", nil)
}

func TestTargetDefaultsAndOptions(t *testing.T) {
	var tg Target
	if err := yaml.Unmarshal([]byte(`
kind: arista
name: sw-a1
address: http://192.0.2.10
password: env:PW
options:
  vrf: mgmt
  ports: [1, 2]
`), &tg); err != nil {
		t.Fatal(err)
	}
	if tg.Source() != "arista:sw-a1" || tg.EffectiveTimeout() != DefaultTimeout || tg.EffectiveInterval() != DefaultInterval {
		t.Fatalf("defaults: %+v", tg)
	}
	var opts struct {
		VRF   string `yaml:"vrf"`
		Ports []int  `yaml:"ports"`
	}
	if err := tg.DecodeOptions(&opts); err != nil || opts.VRF != "mgmt" || len(opts.Ports) != 2 {
		t.Fatalf("options: %+v, %v", opts, err)
	}
	var strict struct {
		VRF string `yaml:"vrf"`
	}
	if err := tg.DecodeOptions(&strict); err == nil {
		t.Fatal("unknown option key accepted")
	}
	if err := (Target{}).DecodeOptions(&strict); err != nil {
		t.Fatalf("no options: %v", err)
	}
	tg.Timeout, tg.Interval = time.Second, time.Minute
	if tg.EffectiveTimeout() != time.Second || tg.EffectiveInterval() != time.Minute {
		t.Fatal("explicit timeout or interval ignored")
	}
}

func TestHTTPClient(t *testing.T) {
	for _, v := range []string{"", "1.0", "1.1", "1.2", "1.3"} {
		if _, err := (Target{Name: "x", TLS: TLS{MinVersion: v}}).HTTPClient(); err != nil {
			t.Errorf("min_version %q: %v", v, err)
		}
	}
	if _, err := (Target{Name: "x", TLS: TLS{MinVersion: "0.9"}}).HTTPClient(); err == nil {
		t.Error("unknown min_version accepted")
	}
	if _, err := (Target{Name: "x", TLS: TLS{CAFile: "/nonexistent"}}).HTTPClient(); err == nil {
		t.Error("missing CA file accepted")
	}
	bad := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Target{Name: "x", TLS: TLS{CAFile: bad}}).HTTPClient(); err == nil {
		t.Error("CA file without certificate accepted")
	}
}

func TestFailedAndSucceeded(t *testing.T) {
	tg := Target{Kind: "arista", Name: "sw-a1"}
	start := time.Now()
	f := Failed(tg, "arista", "1", start, errors.New("connection refused"))
	if f.Scope != sdk.ScopePartial || f.Health.OK || f.Health.Error != "connection refused" || f.Source != "arista:sw-a1" {
		t.Fatalf("failed batch: %+v %+v", f, f.Health)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	b := sdk.NewBatch(tg.Source(), sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "X")
	Succeeded(b, tg, "arista", "1", start, []string{"no LLDP"})
	if !b.Health.OK || b.Health.Items != 1 || len(b.Health.Warnings) != 1 {
		t.Fatalf("succeeded: %+v", b.Health)
	}
	ctx, cancel := Context(context.Background(), tg)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("no deadline")
	}
}
