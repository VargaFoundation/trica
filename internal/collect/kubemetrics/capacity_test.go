package kubemetrics

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// valued is a series with a given value.
func valued(v string, kv ...string) sample {
	s := smp(kv...)
	s.Value = []json.RawMessage{json.RawMessage("1"), json.RawMessage(`"` + v + `"`)}
	return s
}

func capacityWorld() *snapshot {
	s := world()
	s.allocatable = []sample{
		valued("8", "node", "n1", "resource", "cpu", "unit", "core"),
		valued("16000000000", "node", "n1", "resource", "memory", "unit", "byte"),
		valued("4", "node", "n2", "resource", "cpu", "unit", "core"),
		// n2 has no memory series: the attribute is left unset, not 0.
	}
	s.cordoned = []sample{valued("0", "node", "n1"), valued("1", "node", "n2")}
	s.requests = []sample{
		// web-1: two containers, and a second kube-state-metrics replica that
		// reports the first one again (counted once).
		valued("0.1", "namespace", "a", "pod", "web-1", "container", "app", "resource", "cpu"),
		valued("0.1", "namespace", "a", "pod", "web-1", "container", "app", "resource", "cpu"),
		valued("0.05", "namespace", "a", "pod", "web-1", "container", "side", "resource", "cpu"),
		valued("1000", "namespace", "a", "pod", "web-1", "container", "app", "resource", "memory"),
		valued("500", "namespace", "a", "pod", "web-1", "container", "side", "resource", "memory"),
		// db-0 asks for CPU only; memory is 0.
		valued("2", "namespace", "a", "pod", "db-0", "container", "db", "resource", "cpu"),
		// other resources are never added up.
		valued("5", "namespace", "a", "pod", "db-0", "container", "db", "resource", "ephemeral_storage"),
	}
	s.haveAllocatable, s.haveCordoned, s.haveRequests = true, true, true
	return s
}

func TestCapacityAttributes(t *testing.T) {
	b, _ := built(capacityWorld(), false)
	n1, n2 := obj(t, b, "k8s_node", "name", "n1"), obj(t, b, "k8s_node", "name", "n2")
	if n1.Attributes["cpu_capacity"] != 8.0 || n1.Attributes["memory_capacity_bytes"] != int64(16000000000) || n1.Attributes["unschedulable"] != false {
		t.Errorf("n1 %+v", n1.Attributes)
	}
	if n2.Attributes["cpu_capacity"] != 4.0 || n2.Attributes["unschedulable"] != true {
		t.Errorf("n2 %+v", n2.Attributes)
	}
	if _, ok := n2.Attributes["memory_capacity_bytes"]; ok {
		t.Error("a node with no allocatable memory series gets no memory_capacity_bytes")
	}
	web := collectPods(b, "web-1")[0]
	if web.Attributes["cpu_request"] != 0.15 || web.Attributes["memory_request_bytes"] != int64(1500) {
		t.Errorf("web-1 %+v: want the sum over its containers, a container reported twice counted once", web.Attributes)
	}
	db := collectPods(b, "db-0")[0]
	if db.Attributes["cpu_request"] != 2.0 || db.Attributes["memory_request_bytes"] != int64(0) {
		t.Errorf("db-0 %+v", db.Attributes)
	}
}

// A running pod with no request series has asked for nothing: 0 (BestEffort).
// A pod whose phase is unknown and that has no series gets no attribute.
func TestRunningPodWithoutRequestsAsksForZero(t *testing.T) {
	s := capacityWorld()
	s.requests = []sample{valued("2", "namespace", "a", "pod", "db-0", "container", "db", "resource", "cpu")}
	b, _ := built(s, false)
	web := collectPods(b, "web-1")[0] // Running in the phase series
	if web.Attributes["cpu_request"] != 0.0 || web.Attributes["memory_request_bytes"] != int64(0) {
		t.Errorf("a running pod with no request series: %+v, want 0 and 0", web.Attributes)
	}
	if v, ok := collectPods(b, "agent-x")[0].Attributes["cpu_request"]; ok {
		t.Errorf("a pod of unknown phase with no series carries cpu_request %v", v)
	}
}

// A server with none of the families (or an empty one) must not make every pod
// ask for 0: the attributes are left out.
func TestNoRequestFamilyNoAttribute(t *testing.T) {
	for name, mut := range map[string]func(*snapshot){
		"not read": func(s *snapshot) { s.requests, s.haveRequests = nil, false },
		"empty":    func(s *snapshot) { s.requests = nil },
	} {
		t.Run(name, func(t *testing.T) {
			s := capacityWorld()
			mut(s)
			b, _ := built(s, false)
			for _, o := range b.Objects {
				if _, ok := o.Attributes["cpu_request"]; ok && o.Type == "pod" {
					t.Errorf("pod %v carries cpu_request", o.Identity)
				}
			}
		})
	}
}

// The recorded cluster: allocatable, cordoned and requests as served by the fake.
func TestRecordedCapacity(t *testing.T) {
	f := newFake(t)
	b := runOne(t, f, "")
	n := obj(t, b, "k8s_node", "name", "kube-prod-02")
	if n.Attributes["cpu_capacity"] != 72.0 || n.Attributes["memory_capacity_bytes"] != int64(270165610496) || n.Attributes["unschedulable"] != false {
		t.Errorf("node %+v", n.Attributes)
	}
	want := map[string][2]any{
		"keycloak-keycloakx-0":            {4.0, int64(8589934592)},
		"netbird-router-7bc9b46584-klsx2": {0.06, int64(100663296)}, // three containers
		"orion-postgres-0":                {0.255, int64(570425344)},
		"cilium-cg6fk":                    {0.0, int64(0)}, // running, no request series
	}
	for name, w := range want {
		p := obj(t, b, "pod", "name", name)
		if p.Attributes["cpu_request"] != w[0] || p.Attributes["memory_request_bytes"] != w[1] {
			t.Errorf("%s: %v / %v, want %v / %v", name, p.Attributes["cpu_request"], p.Attributes["memory_request_bytes"], w[0], w[1])
		}
	}
	// Init containers are not read: their family is not asked.
	for _, m := range f.Metrics() {
		if strings.Contains(m, "init_container") {
			t.Errorf("queried %s", m)
		}
	}
	if !slices.Contains(f.Metrics(), metricPodRequests) {
		t.Error("the requests family was not read")
	}
}

// Only cpu and memory are asked of the per-resource families, by the query.
func TestPerResourceFamiliesAskCPUAndMemoryOnly(t *testing.T) {
	f := newFake(t)
	runOne(t, f, "")
	for _, r := range f.Requests() {
		q := r.Params["query"][0]
		if needsResource(r.Metric) != strings.Contains(q, `resource=~"cpu|memory"`) {
			t.Errorf("query %q", q)
		}
	}
}

// A failed capacity family leaves the rest of the picture and makes the run partial.
func TestCapacityFamilyFailureIsPartial(t *testing.T) {
	f := newFake(t)
	f.FailStatus(metricNodeAllocatable, 503)
	b := runOne(t, f, "")
	if b.Scope != sdk.ScopePartial || b.Health.OK {
		t.Errorf("scope %v health %+v", b.Scope, b.Health)
	}
	n := obj(t, b, "k8s_node", "name", "kube-prod-01")
	if _, ok := n.Attributes["cpu_capacity"]; ok {
		t.Error("capacity set although the family could not be read")
	}
	if _, ok := n.Attributes["unschedulable"]; !ok {
		t.Error("the other families are still read")
	}
}
