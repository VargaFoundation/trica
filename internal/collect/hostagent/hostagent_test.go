package hostagent_test

import (
	"testing"

	"github.com/VargaFoundation/trica/internal/agent"
	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/hostagent"
)

// The descriptor's kind is the prefix of the agent's sources and the collector
// name of its health records; the two must not drift.
func TestKindIsTheAgentsCollectorName(t *testing.T) {
	if hostagent.Descriptor.Kind != agent.CollectorName {
		t.Fatalf("descriptor kind %q, agent collector %q", hostagent.Descriptor.Kind, agent.CollectorName)
	}
	d, ok := collect.Lookup(hostagent.Kind)
	if !ok || d.Deployment != collect.DeploymentAgent {
		t.Fatalf("registered: %+v, %v", d, ok)
	}
}
