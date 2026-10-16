package kubernetes

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// canary is the value of the Secret in the fixture: it must never appear in
// anything the collector emits.
const canary = "Y2FuYXJ5LW5ldmVyLXJlYWQ="

// collectOne runs the collector against f and returns the one batch it must
// emit. Every run also proves the contract: nothing but get, list and watch,
// never on Secrets, and the Secret's content is nowhere in the batch.
func collectOne(t *testing.T, f *Fake, mutate ...func(*collect.Target)) *sdk.Batch {
	t.Helper()
	target := f.Target("lab", "lab-cluster")
	for _, m := range mutate {
		m(&target)
	}
	c, err := f.Collector(target)
	if err != nil {
		t.Fatal(err)
	}
	batches := collecttest.Run(t, c)
	if len(batches) != 1 {
		t.Fatalf("%d batches, want 1", len(batches))
	}
	b := batches[0]
	if b.Source != "kubernetes:lab" {
		t.Errorf("source %q", b.Source)
	}
	if reqs := f.Requests(); len(reqs) == 0 {
		t.Error("the fake saw no request: nothing proves the contract")
	}
	if err := f.ReadOnly(); err != nil {
		t.Error(err)
	}
	if raw, _ := json.Marshal(b); strings.Contains(string(raw), canary) || strings.Contains(string(raw), "canary-never-read") {
		t.Error("the Secret's content reached the batch")
	}
	return b
}

func object(t *testing.T, b *sdk.Batch, ref string) *sdk.Object {
	t.Helper()
	o, ok := b.Lookup(ref)
	if !ok {
		t.Fatalf("no object %s", ref)
	}
	return o
}

func hasRelation(b *sdk.Batch, typ string, from, to sdk.Endpoint) bool {
	for _, r := range b.Relations {
		if r.Type == typ && reflect.DeepEqual(r.From, from) && reflect.DeepEqual(r.To, to) {
			return true
		}
	}
	return false
}

func relate(b *sdk.Batch, typ, from, to string) bool {
	return hasRelation(b, typ, sdk.Ref(from), sdk.Ref(to))
}

func count(b *sdk.Batch, typ string) int {
	n := 0
	for _, o := range b.Objects {
		if o.Type == typ {
			n++
		}
	}
	return n
}

// TestFinishedPods: a pod that has ended is left out by default, filtered by
// the API server (the selector is sent) and again on the way back (the fake,
// like an old server, ignores the selector); finished_pods brings it back.
func TestFinishedPods(t *testing.T) {
	const backup = "pod/shop/shop-backup-28812340-x7b2d"

	t.Run("left out by default", func(t *testing.T) {
		f := FakeKubernetes()
		b := collectOne(t, f)
		if _, ok := b.Lookup(backup); ok {
			t.Errorf("the Succeeded pod %s was collected", backup)
		}
		if !relate(b, "served_by", "workload/shop/cronjob/shop-backup", "workload/shop/job/shop-backup-28812340") {
			t.Error("the job of the finished pod must stay")
		}
		var selectors []string
		for _, r := range f.Requests() {
			if r.Verb == "list" && r.Resource == "pods" {
				selectors = append(selectors, r.FieldSelector)
			}
		}
		if len(selectors) == 0 {
			t.Fatal("pods were not listed")
		}
		for _, s := range selectors {
			req := strings.Split(s, ",")
			sort.Strings(req)
			if !reflect.DeepEqual(req, []string{"status.phase!=Failed", "status.phase!=Succeeded"}) {
				t.Errorf("pods listed with field selector %q, want the live pods only", s)
			}
		}
	})

	t.Run("finished_pods", func(t *testing.T) {
		f := FakeKubernetes()
		b := collectOne(t, f, func(tg *collect.Target) {
			if err := tg.Options.Encode(map[string]any{"cluster": "lab-cluster", "finished_pods": true}); err != nil {
				t.Fatal(err)
			}
		})
		if n := count(b, "pod"); n != 17 {
			t.Errorf("%d pods, want 17", n)
		}
		wantAttrs(t, object(t, b, backup), map[string]any{"phase": "Succeeded"})
		if !relate(b, "served_by", "workload/shop/job/shop-backup-28812340", backup) {
			t.Error("the finished pod is not served by its job")
		}
		for _, r := range f.Requests() {
			if r.Resource == "pods" && r.FieldSelector != "" {
				t.Errorf("pods listed with field selector %q, want none", r.FieldSelector)
			}
		}
	})
}

func TestK3sCluster(t *testing.T) {
	f := FakeKubernetes()
	b := collectOne(t, f)

	if b.Scope != sdk.ScopeFull {
		t.Errorf("scope %s, want full", b.Scope)
	}
	if b.Health == nil || !b.Health.OK || len(b.Health.Warnings) != 0 || b.Health.Version != "v1.31.4+k3s1" {
		t.Errorf("health %+v", b.Health)
	}

	t.Run("cluster", func(t *testing.T) {
		o := object(t, b, "cluster")
		wantAttrs(t, o, map[string]any{
			"version": "v1.31.4+k3s1", "distribution": "k3s", "api_endpoint": "https://192.0.2.10:6443",
		})
		if o.Identity["name"] != "lab-cluster" {
			t.Errorf("identity %v", o.Identity)
		}
	})

	t.Run("nodes", func(t *testing.T) {
		if n := count(b, "k8s_node"); n != 3 {
			t.Fatalf("%d nodes, want 3", n)
		}
		cp := object(t, b, "node/kube-cp-01")
		if cp.Parent != "cluster" || cp.Identity["uid"] != "00000000-0000-4000-8000-0000000a0001" || cp.Identity["name"] != "kube-cp-01" {
			t.Errorf("node identity/parent: %+v", cp)
		}
		wantAttrs(t, cp, map[string]any{
			"system_uuid": "00000000-0000-4000-9000-000000000011", "kubelet_version": "v1.31.4+k3s1",
			"os_image": "Ubuntu 24.04.1 LTS", "kernel": "6.8.0-48-generic",
			"roles": []string{"control-plane", "etcd", "master"}, "ready": true, "internal_ip": "192.0.2.11",
			"cpu_capacity": float64(4), "memory_capacity_bytes": int64(16367044) * 1024,
		})
		wk := object(t, b, "node/kube-wk-01")
		wantAttrs(t, wk, map[string]any{"roles": []string{"worker"}, "cpu_capacity": 7.95})
		if _, ok := object(t, b, "node/kube-wk-02").Attributes["roles"]; ok {
			t.Error("a node without role label must not get an empty roles list")
		}
		// The device running the node is found by the SMBIOS UUID.
		dev := sdk.Endpoint{Type: "device", Identity: map[string]string{"hw_uuid": "00000000-0000-4000-9000-000000000012"}}
		if !hasRelation(b, "hosts", dev, sdk.Ref("node/kube-wk-01")) {
			t.Error("no device -> node hosts relation by hw_uuid")
		}
	})

	t.Run("namespaces", func(t *testing.T) {
		if n := count(b, "namespace"); n != 8 {
			t.Fatalf("%d namespaces, want 8", n)
		}
		shop := object(t, b, "ns/shop")
		if shop.Parent != "cluster" || shop.Identity["uid"] != "00000000-0000-4000-8000-000000000007" {
			t.Errorf("namespace: %+v", shop)
		}
		labels, _ := shop.Attributes["labels"].(map[string]string)
		if labels["team"] != "retail" || labels["environment"] != "prod" {
			t.Errorf("labels %v", shop.Attributes["labels"])
		}
	})

	t.Run("workloads", func(t *testing.T) {
		if n := count(b, "workload"); n != 11 {
			t.Fatalf("%d workloads, want 11", n)
		}
		web := object(t, b, "workload/shop/deployment/shop-web")
		if web.Parent != "ns/shop" || web.Identity["uid"] != "00000000-0000-4000-8000-000000000020" {
			t.Errorf("workload: %+v", web)
		}
		wantAttrs(t, web, map[string]any{
			"kind": "deployment", "replicas": 2, "ready_replicas": 2,
			"images": []string{"registry.example.test/shop/web:1.8.2", "registry.example.test/shop/metrics-sidecar:0.4.0"},
		})
		wantAttrs(t, object(t, b, "workload/shop/statefulset/shop-db"), map[string]any{"kind": "statefulset", "replicas": 1, "ready_replicas": 1})
		wantAttrs(t, object(t, b, "workload/kube-system/daemonset/cilium"), map[string]any{"kind": "daemonset", "replicas": 3, "ready_replicas": 3})
		cron := object(t, b, "workload/shop/cronjob/shop-backup")
		wantAttrs(t, cron, map[string]any{"kind": "cronjob", "images": []string{"registry.example.test/shop/backup:2.1.0"}})
		if _, ok := cron.Attributes["replicas"]; ok {
			t.Error("a cronjob has no replicas")
		}
		if !relate(b, "served_by", "workload/shop/cronjob/shop-backup", "workload/shop/job/shop-backup-28812340") {
			t.Error("the job is not served by its cronjob")
		}
		if _, ok := b.Lookup("workload/shop/replicaset/shop-web-7d9f8c6b5"); ok {
			t.Error("a ReplicaSet is an implementation detail of its Deployment, not a workload")
		}
	})

	t.Run("pods", func(t *testing.T) {
		// 17 in the fixture, one of which has ended (TestFinishedPods).
		if n := count(b, "pod"); n != 16 {
			t.Errorf("%d pods, want 16", n)
		}
		p := object(t, b, "pod/shop/shop-web-7d9f8c6b5-q8m4z")
		if p.Parent != "ns/shop" || p.Identity["uid"] != "00000000-0000-4000-8000-000000000023" {
			t.Errorf("pod: %+v", p)
		}
		wantAttrs(t, p, map[string]any{"phase": "Running", "pod_ip": "203.0.113.17", "node_name": "kube-wk-02", "restarts": 2})
		for _, c := range []struct{ typ, from, to string }{
			{"hosts", "node/kube-wk-02", "pod/shop/shop-web-7d9f8c6b5-q8m4z"},
			// The pod belongs to the Deployment, through its ReplicaSet.
			{"served_by", "workload/shop/deployment/shop-web", "pod/shop/shop-web-7d9f8c6b5-q8m4z"},
			{"served_by", "workload/shop/statefulset/shop-db", "pod/shop/shop-db-0"},
			{"served_by", "workload/kube-system/daemonset/cilium", "pod/kube-system/cilium-m2p6s"},
		} {
			if !relate(b, c.typ, c.from, c.to) {
				t.Errorf("missing %s %s -> %s", c.typ, c.from, c.to)
			}
		}
		for _, r := range b.Relations {
			if r.Type == "served_by" && r.To.Ref == "pod/vms/virt-launcher-app-vm-01-9t4xk" && r.From.Ref != "" && strings.HasPrefix(r.From.Ref, "workload/") {
				t.Errorf("a virt-launcher pod is the VM's, not a workload's: %v", r.From)
			}
		}
	})

	t.Run("services", func(t *testing.T) {
		if n := count(b, "k8s_service"); n != 6 {
			t.Fatalf("%d services, want 6", n)
		}
		web := object(t, b, "service/shop/shop-web")
		if web.Parent != "ns/shop" || web.Identity["uid"] != "00000000-0000-4000-8000-00000000002d" {
			t.Errorf("service: %+v", web)
		}
		wantAttrs(t, web, map[string]any{
			"type": "ClusterIP", "cluster_ip": "198.51.100.40",
			"selector": map[string]string{"app": "shop-web"},
			"ports":    []map[string]any{{"name": "http", "port": int32(80), "protocol": "TCP", "target_port": "8080"}},
		})
		wantAttrs(t, object(t, b, "service/kube-system/traefik"), map[string]any{"type": "LoadBalancer"})
		wantAttrs(t, object(t, b, "service/shop/shop-db"), map[string]any{"cluster_ip": "None"})
		if _, ok := object(t, b, "service/default/kubernetes").Attributes["selector"]; ok {
			t.Error("the kubernetes service has no selector")
		}
		for _, c := range []struct{ typ, from, to string }{
			{"served_by", "service/shop/shop-web", "pod/shop/shop-web-7d9f8c6b5-xk2lp"},
			{"served_by", "service/shop/shop-web", "pod/shop/shop-web-7d9f8c6b5-q8m4z"},
			{"exposed_by", "workload/shop/deployment/shop-web", "service/shop/shop-web"},
			{"exposed_by", "workload/shop/statefulset/shop-db", "service/shop/shop-db"},
		} {
			if !relate(b, c.typ, c.from, c.to) {
				t.Errorf("missing %s %s -> %s", c.typ, c.from, c.to)
			}
		}
	})

	t.Run("ingress", func(t *testing.T) {
		if n := count(b, "ingress"); n != 1 {
			t.Fatalf("%d ingresses, want 1", n)
		}
		ing := object(t, b, "ingress/shop/shop")
		if ing.Parent != "ns/shop" || ing.Identity["uid"] != "00000000-0000-4000-8000-00000000002f" {
			t.Errorf("ingress: %+v", ing)
		}
		wantAttrs(t, ing, map[string]any{
			"hosts": []string{"api.shop.lab.example", "shop.lab.example"},
			"rules": []map[string]any{
				{"host": "shop.lab.example", "path": "/", "path_type": "Prefix", "service": "shop-web", "port": int32(80)},
				{"host": "api.shop.lab.example", "path": "/v1", "path_type": "Prefix", "service": "shop-web", "port": "http"},
			},
		})
		if !relate(b, "exposed_by", "service/shop/shop-web", "ingress/shop/shop") {
			t.Error("the backend service is not exposed by the ingress")
		}
		n := 0
		for _, r := range b.Relations {
			if r.Type == "exposed_by" && r.To.Ref == "ingress/shop/shop" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%d exposed_by to the ingress, want 1 (two rules, one service)", n)
		}
	})

	t.Run("virtual machines", func(t *testing.T) {
		if n := count(b, "vm"); n != 2 {
			t.Fatalf("%d vms, want 2", n)
		}
		app := object(t, b, "vm/vms/app-vm-01")
		if app.Identity["uuid"] != "00000000-0000-4000-a000-000000000101" || app.Identity["name"] != "app-vm-01" || app.Parent != "" {
			t.Errorf("vm identity: %+v", app)
		}
		wantAttrs(t, app, map[string]any{
			"namespace": "vms", "vcpus": 2, "memory_bytes": int64(4 << 30), "power_state": "running", "os_name": "Ubuntu 24.04.1 LTS",
		})
		// Without a firmware UUID the instance UID identifies the machine; 2x2
		// cores, the request as memory, no guest agent.
		db := object(t, b, "vm/vms/db-vm-01")
		if db.Identity["uuid"] != "00000000-0000-4000-8000-000000000033" {
			t.Errorf("uuid fallback: %v", db.Identity)
		}
		wantAttrs(t, db, map[string]any{"vcpus": 4, "memory_bytes": int64(8 << 30), "power_state": "running"})
		if _, ok := db.Attributes["os_name"]; ok {
			t.Error("os_name set without a guest agent")
		}

		lan := object(t, b, "vm/vms/app-vm-01/if/lan")
		if lan.Type != "interface" || lan.Parent != "vm/vms/app-vm-01" || lan.Identity["name"] != "lan" {
			t.Errorf("interface: %+v", lan)
		}
		wantAttrs(t, lan, map[string]any{"kind": "virtual", "mac": "02:00:00:aa:01:02"})
		if o := object(t, b, "ip/192.0.2.51"); o.Type != "ip_address" || o.Identity["address"] != "192.0.2.51" {
			t.Errorf("ip: %+v", o)
		}
		for _, c := range []struct{ typ, from, to string }{
			{"hosts", "node/kube-wk-01", "vm/vms/app-vm-01"},
			{"hosts", "node/kube-wk-02", "vm/vms/db-vm-01"},
			{"assigned_to", "ip/192.0.2.51", "vm/vms/app-vm-01/if/lan"},
			{"assigned_to", "ip/203.0.113.71", "vm/vms/app-vm-01/if/default"},
			{"assigned_to", "ip/203.0.113.72", "vm/vms/db-vm-01/if/default"},
		} {
			if !relate(b, c.typ, c.from, c.to) {
				t.Errorf("missing %s %s -> %s", c.typ, c.from, c.to)
			}
		}
	})

	t.Run("requests", func(t *testing.T) {
		// What a run reads: every resource once, KubeVirt through the dynamic client.
		seen := map[string]int{}
		for _, r := range f.Requests() {
			seen[r.Verb+" "+r.Resource]++
		}
		for _, want := range []string{
			"get version", "list nodes", "list namespaces", "list pods", "list services", "list deployments",
			"list statefulsets", "list daemonsets", "list replicasets", "list jobs", "list cronjobs", "list ingresses",
			"list virtualmachineinstances",
		} {
			if seen[want] != 1 {
				t.Errorf("%q issued %d times, want 1", want, seen[want])
			}
		}
	})
}

func TestKubeadmCluster(t *testing.T) {
	f := FakeKubernetesVersion(Kubeadm129)
	b := collectOne(t, f)

	if b.Scope != sdk.ScopeFull || !b.Health.OK {
		t.Fatalf("scope %s health %+v", b.Scope, b.Health)
	}
	// No KubeVirt on this cluster: its API answers "not found", which is not a
	// failure and not even a warning.
	if len(b.Health.Warnings) != 0 {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
	wantAttrs(t, object(t, b, "cluster"), map[string]any{"version": "v1.29.9", "distribution": "kubernetes"})
	if count(b, "k8s_node") != 2 || count(b, "vm") != 0 || count(b, "ingress") != 0 {
		t.Errorf("nodes %d vms %d ingresses %d", count(b, "k8s_node"), count(b, "vm"), count(b, "ingress"))
	}
	// kubeadm labels the control plane with an empty value.
	wantAttrs(t, object(t, b, "node/node-a1"), map[string]any{"roles": []string{"control-plane"}, "ready": true, "internal_ip": "198.51.100.101"})
	wantAttrs(t, object(t, b, "node/node-a2"), map[string]any{"ready": false})
	wantAttrs(t, object(t, b, "workload/kube-system/deployment/coredns"), map[string]any{"replicas": 2, "ready_replicas": 1})
	wantAttrs(t, object(t, b, "service/tools/wiki"), map[string]any{"type": "NodePort"})
	if !relate(b, "served_by", "workload/kube-system/deployment/coredns", "pod/kube-system/coredns-76f75df574-xk2lp") {
		t.Error("pod not attached to its Deployment")
	}
}

// TestKubeVirtAbsent covers the one error that is not one: an API server that
// has no kubevirt.io group.
func TestKubeVirtAbsent(t *testing.T) {
	f := FakeKubernetes().WithoutKubeVirt()
	b := collectOne(t, f)
	if b.Scope != sdk.ScopeFull || !b.Health.OK || len(b.Health.Warnings) != 0 {
		t.Errorf("scope %s health %+v", b.Scope, b.Health)
	}
	if count(b, "vm") != 0 || count(b, "k8s_node") != 3 {
		t.Errorf("vms %d nodes %d", count(b, "vm"), count(b, "k8s_node"))
	}
}

// TestDeterministic: two runs on an unchanged cluster emit the same objects and
// relations, in the same order, so that nothing flaps in the history.
func TestDeterministic(t *testing.T) {
	f := FakeKubernetes()
	a, b := collectOne(t, f), collectOne(t, f)
	ja, _ := json.Marshal([]any{a.Objects, a.Relations})
	jb, _ := json.Marshal([]any{b.Objects, b.Relations})
	if string(ja) != string(jb) {
		t.Error("two runs on the same cluster differ")
	}
}

func TestServiceSelectors(t *testing.T) {
	// One service selects only the pod of a finished Job (it receives no
	// traffic: no relation); another has no selector at all (an ExternalName
	// service selects nothing, not everything).
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "shop", UID: "uid-svc-backup"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "shop-backup"}, ClusterIP: "198.51.100.77"},
	}
	headless := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "shop", UID: "uid-svc-external"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "db.example.test"},
	}
	b := collectOne(t, FakeKubernetes(svc, headless))
	for _, r := range b.Relations {
		if r.From.Ref == "service/shop/backup" || r.From.Ref == "service/shop/external" {
			t.Errorf("unexpected relation %s from %s to %s", r.Type, r.From.Ref, r.To.Ref)
		}
	}
	wantAttrs(t, object(t, b, "service/shop/backup"), map[string]any{"selector": map[string]string{"app": "shop-backup"}})
	if _, ok := object(t, b, "service/shop/external").Attributes["selector"]; ok {
		t.Error("selector set on a service without one")
	}
}

func TestExtraObjects(t *testing.T) {
	// A pod in a namespace the list did not return, owned by nothing, on an
	// unknown node: reported, with its namespace created on demand.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "stray", Namespace: "ghost", UID: "uid-stray"},
		Spec:       corev1.PodSpec{NodeName: "gone", Containers: []corev1.Container{{Name: "c", Image: "busybox:1"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	b := collectOne(t, FakeKubernetes(pod))
	p := object(t, b, "pod/ghost/stray")
	if p.Parent != "ns/ghost" {
		t.Errorf("parent %q", p.Parent)
	}
	ns := object(t, b, "ns/ghost")
	if ns.Parent != "cluster" || ns.Identity["name"] != "ghost" {
		t.Errorf("namespace: %+v", ns)
	}
	for _, r := range b.Relations {
		if r.To.Ref == "pod/ghost/stray" {
			t.Errorf("relation to an orphan pod: %+v", r)
		}
	}
	wantAttrs(t, p, map[string]any{"phase": "Pending", "node_name": "gone", "images": []string{"busybox:1"}})
}

// TestFailures covers what goes wrong: the run must say so, in the health,
// without ever claiming a full observation it does not have.
func TestFailures(t *testing.T) {
	boom := errors.New("etcdserver: request timed out")
	for _, tc := range []struct {
		name      string
		fake      func() *Fake
		mutate    func(*collect.Target)
		scope     sdk.Scope
		objects   bool   // some data is kept
		errHas    string // in Health.Error
		wantCount map[string]int
	}{
		{
			name:   "credentials refused",
			fake:   func() *Fake { return FakeKubernetes().Unauthorized() },
			scope:  sdk.ScopePartial,
			errHas: "Unauthorized",
		},
		{
			name:   "version call fails",
			fake:   func() *Fake { return FakeKubernetes().FailVersion(errors.New("connection refused")) },
			scope:  sdk.ScopePartial,
			errHas: "server version: connection refused",
		},
		{
			name: "every list fails",
			fake: func() *Fake {
				f := FakeKubernetes()
				for _, r := range []string{"nodes", "namespaces", "pods", "services", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs", "ingresses"} {
					f.FailList(r, boom)
				}
				return f.FailList("virtualmachineinstances", boom)
			},
			scope:  sdk.ScopePartial,
			errHas: "every list failed",
		},
		{
			name:   "credentials refused halfway",
			fake:   func() *Fake { return FakeKubernetes().FailList("pods", apierrors.NewUnauthorized("token expired")) },
			scope:  sdk.ScopePartial,
			errHas: "credentials refused",
		},
		{
			name:      "RBAC forgot pods",
			fake:      func() *Fake { return FakeKubernetes().Forbid("pods") },
			scope:     sdk.ScopePartial,
			objects:   true,
			errHas:    "pods:",
			wantCount: map[string]int{"k8s_node": 3, "pod": 0, "workload": 11, "k8s_service": 6, "vm": 2},
		},
		{
			name: "list cannot be decoded",
			fake: func() *Fake {
				return FakeKubernetes().FailList("ingresses", errors.New("couldn't get version/kind; json parse error"))
			},
			scope:     sdk.ScopePartial,
			objects:   true,
			errHas:    "ingresses: couldn't get version/kind",
			wantCount: map[string]int{"ingress": 0, "k8s_node": 3},
		},
		{
			name:      "KubeVirt refused",
			fake:      func() *Fake { return FakeKubernetes().FailList("virtualmachineinstances", errors.New("forbidden")) },
			scope:     sdk.ScopePartial,
			objects:   true,
			errHas:    "virtualmachineinstances: forbidden",
			wantCount: map[string]int{"vm": 0, "pod": 16},
		},
		{
			name:      "one instance is malformed",
			fake:      func() *Fake { return FakeKubernetes().AddMalformedVMI() },
			scope:     sdk.ScopePartial,
			objects:   true,
			errHas:    "virtualmachineinstances vms/broken-vm",
			wantCount: map[string]int{"vm": 2, "k8s_node": 3},
		},
		{
			name:   "slow server",
			fake:   func() *Fake { return FakeKubernetes().Slow(150 * time.Millisecond) },
			mutate: func(t *collect.Target) { t.Timeout = 50 * time.Millisecond },
			scope:  sdk.ScopePartial,
			errHas: "deadline exceeded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fake()
			var mutate []func(*collect.Target)
			if tc.mutate != nil {
				mutate = append(mutate, tc.mutate)
			}
			b := collectOne(t, f, mutate...)
			if b.Scope != tc.scope {
				t.Errorf("scope %s, want %s: a run that did not see everything must not withdraw", b.Scope, tc.scope)
			}
			if b.Health == nil || b.Health.OK {
				t.Fatalf("health %+v, want a failure", b.Health)
			}
			if !strings.Contains(b.Health.Error, tc.errHas) {
				t.Errorf("health error %q, want it to contain %q", b.Health.Error, tc.errHas)
			}
			if tc.objects && len(b.Objects) == 0 {
				t.Error("what was read must be kept")
			}
			if !tc.objects && len(b.Objects) != 0 {
				t.Errorf("a failed run reports %d objects", len(b.Objects))
			}
			for typ, n := range tc.wantCount {
				if got := count(b, typ); got != n {
					t.Errorf("%d %s, want %d", got, typ, n)
				}
			}
		})
	}
}

func TestPartialHealthListsWarnings(t *testing.T) {
	b := collectOne(t, FakeKubernetes().Forbid("pods").Forbid("ingresses"))
	if len(b.Health.Warnings) != 2 {
		t.Errorf("warnings %v, want one per failed list", b.Health.Warnings)
	}
	if !strings.Contains(b.Health.Error, "2 of the reads failed") {
		t.Errorf("error %q", b.Health.Error)
	}
	// Without the pods, nothing links a service to a pod, and nothing breaks.
	if count(b, "pod") != 0 || count(b, "k8s_service") != 6 {
		t.Errorf("pods %d services %d", count(b, "pod"), count(b, "k8s_service"))
	}
}

func TestFakeRecordsWritesAsViolations(t *testing.T) {
	// The read-only guard must be able to fail: a fake that saw a write or a
	// Secret read reports it.
	f := FakeKubernetes()
	kube, _ := f.Clients()
	ctx := t.Context()
	if _, err := kube.CoreV1().Secrets("shop").List(ctx, metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.ReadOnly(); err == nil || !strings.Contains(err.Error(), "list secrets") {
		t.Errorf("a Secret read went unnoticed: %v", err)
	}
	f = FakeKubernetes()
	kube, _ = f.Clients()
	if err := kube.CoreV1().Pods("shop").Delete(ctx, "shop-db-0", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.ReadOnly(); err == nil || !strings.Contains(err.Error(), "delete pods") {
		t.Errorf("a delete went unnoticed: %v", err)
	}
}

func TestFakeFixtureErrors(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("an unknown fixture must panic: it is a bug in the test")
		}
	}()
	FakeKubernetesVersion("nope")
}

func TestParseDocumentsErrors(t *testing.T) {
	if docs, err := parseDocuments([]byte("# only a comment\n---\n---\n")); err != nil || len(docs) != 0 {
		t.Errorf("empty stream: %v, %d docs", err, len(docs))
	}
	if _, err := parseDocuments([]byte("a: [unclosed")); err == nil {
		t.Error("invalid YAML accepted")
	}
	if _, err := parseDocuments([]byte("kind: Pod\n")); err == nil {
		t.Error("a document without apiVersion accepted")
	}
}

func wantAttrs(t *testing.T, o *sdk.Object, want map[string]any) {
	t.Helper()
	for k, v := range want {
		got, ok := o.Attributes[k]
		if !ok {
			t.Errorf("%s: attribute %s missing", o.Ref, k)
			continue
		}
		if !reflect.DeepEqual(sortedCopy(got), sortedCopy(v)) {
			t.Errorf("%s.%s = %#v, want %#v", o.Ref, k, got, v)
		}
	}
}

// sortedCopy sorts string lists: the schema sorts them on ingest, the order
// the collector emits them in is not part of the contract.
func sortedCopy(v any) any {
	if l, ok := v.([]string); ok {
		out := append([]string(nil), l...)
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j] < out[j-1]; j-- {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
		return out
	}
	return v
}
