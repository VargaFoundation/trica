package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The agent runs on every host: it links the transport client only, never the
// server side, whose registry pulls in the PostgreSQL driver (#64).
func TestAgentDoesNotLinkTheServer(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go tool is needed to list the agent's dependencies: %v", err)
	}
	out, err := exec.Command(gobin, "list", "-deps", ".").Output() // #nosec G204 -- fixed arguments
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	forbidden := []string{
		"github.com/jackc/pgx/",
		"github.com/VargaFoundation/trica/internal/transport",
		"github.com/VargaFoundation/trica/internal/store",
	}
	linked := false
	for _, pkg := range strings.Fields(string(out)) {
		if pkg == "github.com/VargaFoundation/trica/internal/transport/uplink" {
			linked = true
			continue
		}
		if pkg == "github.com/VargaFoundation/trica/internal/transport/wire" {
			continue
		}
		for _, f := range forbidden {
			if pkg == f || strings.HasPrefix(pkg, strings.TrimSuffix(f, "/")+"/") {
				t.Errorf("the agent links %s", pkg)
			}
		}
	}
	if !linked {
		t.Error("the agent does not link the transport client: the list is not what this test expects")
	}
}
