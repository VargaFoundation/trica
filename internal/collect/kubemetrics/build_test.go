package kubemetrics

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// smp builds a series from label pairs; the value is 1.
func smp(kv ...string) sample {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return sample{Metric: m, Value: []json.RawMessage{json.RawMessage("1"), json.RawMessage(`"1"`)}}
}

func owned(ns, pod, kind, name string) sample {
	return smp("namespace", ns, "pod", pod, "owner_kind", kind, "owner_name", name, "owner_is_controller", "true")
}

func phaseOf(ns, pod, phase, value string) sample {
	s := smp("namespace", ns, "pod", pod, "phase", phase)
	s.Value = []json.RawMessage{json.RawMessage("1"), json.RawMessage(`"` + value + `"`)}
	return s
}

func world() *snapshot {
	return &snapshot{
		nodes: []sample{smp("node", "n1", "system_uuid", " U-1 ", "internal_ip", "192.0.2.1"), smp("node", "n1", "system_uuid", "U-1"), smp("node", "n2")},
		pods: []sample{
			smp("namespace", "a", "pod", "web-1", "uid", "u1", "node", "n1", "pod_ip", "198.51.100.1"),
			smp("namespace", "a", "pod", "web-1", "uid", "u1", "node", "n1"), // a second kube-state-metrics replica
			smp("namespace", "a", "pod", "db-0", "uid", "u2", "node", "n2"),
			smp("namespace", "a", "pod", "agent-x", "uid", "u3", "node", "n1"),
			smp("namespace", "a", "pod", "cron-1-p", "uid", "u4", "node", "n1"),
			smp("namespace", "a", "pod", "once-p", "uid", "u5", "node", "n1"),
			smp("namespace", "a", "pod", "bare", "uid", "u6", "node", "n1"),
			smp("namespace", "a", "pod", "stray-p", "uid", "u7", "node", "n1"),
			smp("namespace", "a", "pod", "vm-p", "uid", "u8", "node", "n1"),
			smp("namespace", "a", "pod", "pending", "uid", "u9"),
			smp("namespace", "b", "pod", "done-p", "uid", "u10", "node", "n1"),
		},
		owners: []sample{
			owned("a", "web-1", "ReplicaSet", "web-abc"),
			owned("a", "db-0", "StatefulSet", "db"),
			owned("a", "agent-x", "DaemonSet", "agent"),
			owned("a", "cron-1-p", "Job", "cron-1"),
			owned("a", "once-p", "Job", "once"),
			owned("a", "stray-p", "ReplicaSet", "stray-1"),
			owned("a", "vm-p", "VirtualMachineInstance", "vm"),
			owned("b", "done-p", "Job", "done"),
			smp("namespace", "a", "pod", "bare"),
			smp("namespace", "a", "pod", "pending", "owner_kind", "<none>", "owner_name", "<none>"),
		},
		replicaSets: []sample{
			{Metric: map[string]string{"namespace": "a", "replicaset": "web-abc", "owner_kind": "Deployment", "owner_name": "web", "owner_is_controller": "true"}},
			// The KSM scrape labels (pod, namespace of kube-state-metrics itself) must not be read.
			{Metric: map[string]string{"namespace": "a", "replicaset": "stray-1", "pod": "web-1"}},
		},
		jobs: []sample{
			{Metric: map[string]string{"namespace": "a", "job_name": "cron-1", "owner_kind": "CronJob", "owner_name": "cron", "owner_is_controller": "true", "pod": "ksm-0"}},
			{Metric: map[string]string{"namespace": "a", "job_name": "once"}},
			{Metric: map[string]string{"namespace": "b", "job_name": "done"}},
		},
		phases: []sample{
			phaseOf("a", "web-1", "Running", "1"), phaseOf("a", "web-1", "Pending", "0"),
			phaseOf("b", "done-p", "Succeeded", "1"), phaseOf("b", "done-p", "Running", "0"),
		},
		haveOwners: true, haveRS: true, haveJob: true, havePhase: true,
	}
}

func built(snap *snapshot, finished bool) (*sdk.Batch, []string) {
	b := sdk.NewBatch("kubemetrics:t", sdk.ScopeFull)
	w := build(b, snap, "c1", "e1", finished)
	return b, w
}

func TestWorkloadResolution(t *testing.T) {
	b, warnings := built(world(), false)
	for pod, want := range map[string]string{
		"web-1":    "deployment/web",  // ReplicaSet -> Deployment
		"db-0":     "statefulset/db",  // direct
		"agent-x":  "daemonset/agent", // direct
		"cron-1-p": "cronjob/cron",    // Job -> CronJob
		"once-p":   "job/once",        // a Job no CronJob owns
		"bare":     "",                // no owner at all
		"stray-p":  "",                // a ReplicaSet no Deployment owns
		"vm-p":     "",                // a controller that is no workload
		"pending":  "",                // owner <none>
	} {
		if got := workloadOfPod(b, "pod/a/"+pod); got != want {
			t.Errorf("pod %s: workload %q, want %q", pod, got, want)
		}
	}
	if !hasWarningIn(warnings, "1 pods are owned by a ReplicaSet that has no Deployment") || !hasWarningIn(warnings, "VirtualMachineInstance: 1") {
		t.Errorf("warnings %v", warnings)
	}
	// One workload per controller, not per pod.
	if n := count(b, "workload"); n != 5 {
		t.Errorf("%d workloads, want 5", n)
	}
	w := obj(t, b, "workload", "name", "web")
	if w.Attributes["kind"] != "deployment" || w.Parent != "ns/a" {
		t.Errorf("workload %+v", w)
	}
}

func hasWarningIn(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestNodesAndPlacement(t *testing.T) {
	b, _ := built(world(), false)
	if n := count(b, "k8s_node"); n != 2 {
		t.Errorf("%d nodes, want 2 (a node seen twice is one)", n)
	}
	n1 := obj(t, b, "k8s_node", "name", "n1")
	if n1.Attributes["system_uuid"] != "U-1" || n1.Attributes["internal_ip"] != "192.0.2.1" {
		t.Errorf("node %+v", n1.Attributes)
	}
	// The pod seen by two replicas is one pod; a pod with no node is hosted by none.
	if n := len(collectPods(b, "web-1")); n != 1 {
		t.Errorf("%d web-1 pods", n)
	}
	if !hasRel(b, "hosts", "node/n1", "pod/a/web-1") || !hasRel(b, "hosts", "node/n2", "pod/a/db-0") {
		t.Error("pods are not hosted by their node")
	}
	for _, r := range b.Relations {
		if r.Type == "hosts" && r.To.Ref == "pod/a/pending" {
			t.Error("a pod with no node is hosted")
		}
	}
	// The device link is by SMBIOS UUID, only for a node that reports one.
	var devs []string
	for _, r := range b.Relations {
		if r.Type == "hosts" && r.From.Type == "device" {
			devs = append(devs, r.From.Identity["hw_uuid"]+">"+r.To.Ref)
		}
	}
	if len(devs) != 1 || devs[0] != "U-1>node/n1" {
		t.Errorf("device links %v", devs)
	}
}

func collectPods(b *sdk.Batch, name string) []*sdk.Object {
	var out []*sdk.Object
	for _, o := range b.Objects {
		if o.Type == "pod" && o.Identity["name"] == name {
			out = append(out, o)
		}
	}
	return out
}

func TestFinishedPods(t *testing.T) {
	b, _ := built(world(), false)
	if len(collectPods(b, "done-p")) != 0 {
		t.Error("a Succeeded pod was kept")
	}
	// Its namespace has no pod left: it is not emitted either.
	for _, o := range b.Objects {
		if o.Type == "namespace" && o.Identity["name"] == "b" {
			t.Error("an empty namespace was emitted")
		}
	}
	b, _ = built(world(), true)
	p := collectPods(b, "done-p")
	if len(p) != 1 || p[0].Attributes["phase"] != "Succeeded" {
		t.Errorf("pods %+v", p)
	}
	if got := workloadOfPod(b, "pod/b/done-p"); got != "job/done" {
		t.Errorf("workload %q", got)
	}
	if p := collectPods(b, "web-1"); p[0].Attributes["phase"] != "Running" {
		t.Errorf("phase %v: the series whose value is 1 is the current one", p[0].Attributes["phase"])
	}
}

// The batch is the same for the same input, whatever the order the server
// returned the series in.
func TestBuildIsDeterministic(t *testing.T) {
	a, _ := built(world(), true)
	snap := world()
	for _, s := range [][]sample{snap.nodes, snap.pods, snap.owners, snap.replicaSets, snap.jobs, snap.phases} {
		for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
			s[i], s[j] = s[j], s[i]
		}
	}
	b, _ := built(snap, true)
	ja, _ := json.Marshal(a.Objects)
	jb, _ := json.Marshal(b.Objects)
	if string(ja) != string(jb) {
		t.Error("objects differ with the order of the series")
	}
	ra, _ := json.Marshal(a.Relations)
	rb, _ := json.Marshal(b.Relations)
	if string(ra) != string(rb) {
		t.Error("relations differ with the order of the series")
	}
}

// A pod with several owners keeps its controller.
func TestControllerWins(t *testing.T) {
	snap := world()
	snap.owners = append(snap.owners,
		smp("namespace", "a", "pod", "db-0", "owner_kind", "Node", "owner_name", "n2", "owner_is_controller", "false"),
		smp("namespace", "a", "pod", "agent-x", "owner_kind", "ConfigMap", "owner_name", "c"),
	)
	b, _ := built(snap, false)
	if got := workloadOfPod(b, "pod/a/db-0"); got != "statefulset/db" {
		t.Errorf("db-0: %q", got)
	}
	if got := workloadOfPod(b, "pod/a/agent-x"); got != "daemonset/agent" {
		t.Errorf("agent-x: %q", got)
	}
}
