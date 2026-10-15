package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect/runner"
)

func TestParseTargets(t *testing.T) {
	ok := `
targets:
  - {kind: arista, name: sw-a1, address: "https://192.0.2.10", username: monitor, password: "file:/run/secrets/arista/password", interval: 5m}
  - {kind: redfish, name: bmc-1, address: "https://198.51.100.20", username: monitor, password: "env:BMC_PASSWORD"}
`
	got, err := runner.ParseTargets([]byte(ok), "t.yaml")
	if err != nil || len(got) != 2 || got[0].Source() != "arista:sw-a1" {
		t.Fatalf("ok: %v %v", got, err)
	}
	if empty, err := runner.ParseTargets(nil, "t.yaml"); err != nil || len(empty) != 0 {
		t.Fatalf("empty file: %v %v", empty, err)
	}

	for name, body := range map[string]string{
		"unknown key":     "targets:\n  - {kind: arista, name: a, addr_typo: x}\n",
		"unknown section": "target: []\n",
		"no kind":         "targets:\n  - {name: a}\n",
		"no name":         "targets:\n  - {kind: arista}\n",
		"duplicate":       "targets:\n  - {kind: arista, name: a}\n  - {kind: arista, name: a}\n",
		"plain password":  "targets:\n  - {kind: arista, name: a, password: hunter2-not-a-reference}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runner.ParseTargets([]byte(body), "t.yaml")
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("the error echoes the password: %v", err)
			}
		})
	}
}

func TestLoadTargets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "targets.yaml")
	if err := os.WriteFile(p, []byte("targets:\n  - {kind: arista, name: a}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := runner.LoadTargets(p); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := runner.LoadTargets(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestParseTargetsReadsTheDomain(t *testing.T) {
	ts, err := runner.ParseTargets([]byte("targets:\n  - {kind: arista, name: sw-a1, address: 192.0.2.10, domain: site-a}\n"), "t.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 1 || ts[0].Domain != "site-a" {
		t.Fatalf("targets %+v", ts)
	}
}
