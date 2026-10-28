package kubemetrics

import (
	"testing"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestRecordedCluster(t *testing.T) {
	f := newFake(t)
	b := runOne(t, f, "")
	if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Collector != Kind || len(b.Health.Warnings) != 1 {
		t.Fatalf("scope %v, health %+v", b.Scope, b.Health)
	}
	// One warning: the KubeVirt pod is controlled by a VirtualMachineInstance.
	if !hasWarning(b, "VirtualMachineInstance: 1") {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
	c := obj(t, b, "k8s_cluster", "name", "dev")
	if c.Parent != "" {
		t.Errorf("cluster has a parent %q", c.Parent)
	}
	if !hasRel(b, "deployed_in", "cluster", "env/dev") {
		t.Error("the cluster is not deployed_in its environment")
	}
	if n := count(b, "k8s_node"); n != 3 {
		t.Fatalf("%d nodes, want 3", n)
	}
	n1 := obj(t, b, "k8s_node", "name", "kube-prod-01")
	for k, want := range map[string]any{
		"system_uuid": "32353537-3835-5a43-4a30-303030315331", "internal_ip": "192.0.2.11",
		"kubelet_version": "v1.31.1+k3s1", "os_image": "Ubuntu 24.04.2 LTS", "kernel": "6.8.0-60-generic",
	} {
		if n1.Attributes[k] != want {
			t.Errorf("node attribute %s = %v, want %v", k, n1.Attributes[k], want)
		}
	}
	if n1.Parent != "cluster" {
		t.Errorf("node parent %q", n1.Parent)
	}
	// The server: one hosts relation by SMBIOS UUID per node, the way the
	// kubernetes collector does it.
	devs := 0
	for _, r := range b.Relations {
		if r.Type == "hosts" && r.From.Type == "device" {
			devs++
			if r.From.Identity["hw_uuid"] == "" || r.To.Ref == "" {
				t.Errorf("relation %+v", r)
			}
		}
	}
	if devs != 3 {
		t.Errorf("%d device -> node relations, want 3", devs)
	}
}

func TestRecordedPods(t *testing.T) {
	b := runOne(t, newFake(t), "")
	// 71 recorded pods: 47 running, 18 succeeded and 6 failed (jobs). The
	// finished ones are left out unless the option keeps them.
	finished := runOne(t, newFake(t), "finished_pods: true")
	if all, kept := count(finished, "pod"), count(b, "pod"); all != 71 || kept != 47 {
		t.Fatalf("%d pods with finished_pods, %d without; want 71 and 47", all, kept)
	}
	for _, o := range b.Objects {
		if o.Type == "pod" && (o.Attributes["phase"] == "Succeeded" || o.Attributes["phase"] == "Failed") {
			t.Errorf("finished pod %v kept", o.Identity)
		}
	}
	p := obj(t, b, "pod", "name", "orion-6d788dccb8-xfrbl")
	if p.Identity["uid"] == "" || p.Attributes["phase"] != "Running" || p.Attributes["node_name"] == "" || p.Attributes["pod_ip"] == "" {
		t.Errorf("pod %+v", p)
	}
	if p.Parent != "ns/orion" {
		t.Errorf("pod parent %q", p.Parent)
	}
	if !hasRel(b, "hosts", "node/"+p.Attributes["node_name"].(string), p.Ref) {
		t.Error("the pod is not hosted by its node")
	}
	// No pod address becomes an ip_address: it overlaps between clusters.
	if count(b, "ip_address") != 0 {
		t.Error("an ip_address was emitted")
	}
}
