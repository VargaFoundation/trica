package kubemetrics_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/internal/collect/kubemetrics"
	"github.com/VargaFoundation/trica/internal/collect/kubernetes"
	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// These tests submit the two batches that describe the dev cluster to the real
// engine: the Argo CD view of the kubernetes collector (workloads, instances)
// and the kube-state-metrics view of this collector (nodes, pods). The
// fixture is the one recorded on prod, run here under cluster "dev".

// The three servers behind the fixture's nodes (kube-prod-01..03).
var serverUUIDs = []string{
	"32353537-3835-5a43-4a30-303030315331",
	"32353537-3835-5a43-4a30-303030325332",
	"32353537-3835-5a43-4a30-303030335333",
}

const instanceKey = "orion/app/dev/orion"

type world struct {
	t       *testing.T
	st      *store.Store
	reg     *schema.Registry
	results []*reconcile.Result
	engine  *reconcile.Engine
	batches []*sdk.Batch
}

func newWorld(t *testing.T) *world {
	t.Helper()
	reg := schema.Builtin()
	st := testdb.New(t)
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	w := &world{t: t, st: st, reg: reg}
	w.engine = &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Now: func() time.Time { return now }}

	// The servers come first: a node finds its device by hardware UUID.
	dev := sdk.NewBatch("test-devices", sdk.ScopePartial)
	for i, u := range serverUUIDs {
		dev.Object("srv"+string(rune('1'+i)), "device").Identify("hw_uuid", u)
	}
	w.apply(dev)

	// The Argo CD view: a build cluster that deploys to dev and prod.
	kf := kubernetes.FakeKubernetesVersion(kubernetes.Build131)
	ktg := kf.TargetWith("build", map[string]any{
		"cluster": "build", "environment": "build", "argocd": true,
		"argocd_clusters":          map[string]string{"https://203.0.113.1:6443": "dev", "https://198.51.100.1:6443": "prod"},
		"argocd_platform_projects": []string{"infra"},
	})
	kc, err := kf.Collector(ktg)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range collecttest.Run(t, kc) {
		w.apply(b)
	}

	// The metrics view of dev.
	mf, err := kubemetrics.NewFake(kubemetrics.SetRecorded)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mf.Close)
	mtg := mf.Target("dev")
	if err := mtg.Options.Encode(map[string]any{"cluster": "dev"}); err != nil {
		t.Fatal(err)
	}
	mc, err := collect.New(mtg)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range collecttest.Run(t, mc) {
		w.apply(b)
	}
	return w
}

func (w *world) apply(b *sdk.Batch) {
	w.t.Helper()
	res, err := w.engine.Apply(context.Background(), b, "", "")
	if err != nil {
		w.t.Fatalf("apply %s: %v", b.Source, err)
	}
	w.results = append(w.results, res)
	w.batches = append(w.batches, b)
}

func (w *world) objects() []*store.Object {
	w.t.Helper()
	objs, err := w.st.AllObjects(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return objs
}

func (w *world) relations() []store.Relation {
	w.t.Helper()
	rels, err := w.st.CurrentRelations(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return rels
}

// find returns the single object of a type with a display, and fails when
// there are several or none.
func (w *world) find(typ, display string) *store.Object {
	w.t.Helper()
	var got []*store.Object
	for _, o := range w.objects() {
		if o.Type == typ && o.Display == display {
			got = append(got, o)
		}
	}
	if len(got) != 1 {
		w.t.Fatalf("%d %s named %q, want exactly 1", len(got), typ, display)
	}
	return got[0]
}

func (w *world) count(typ, display string) int {
	n := 0
	for _, o := range w.objects() {
		if o.Type == typ && o.Display == display {
			n++
		}
	}
	return n
}

func (w *world) related(typ, from, to string) bool {
	for _, r := range w.relations() {
		if r.Type == typ && r.FromID == from && r.ToID == to {
			return true
		}
	}
	return false
}

func (w *world) device(i int) *store.Object {
	w.t.Helper()
	for _, o := range w.objects() {
		if o.Type == "device" && o.Attrs["hw_uuid"] == serverUUIDs[i] {
			return o
		}
	}
	w.t.Fatalf("no device %s", serverUUIDs[i])
	return nil
}

func (w *world) instance() *store.Object {
	w.t.Helper()
	return w.find("component_instance", instanceKey)
}

// TestBothViewsMeetOnOneWorkload: Argo CD says the instance runs on workload
// orion of namespace orion in cluster dev; kube-state-metrics says which pods
// serve it, on which nodes, on which servers. There must be one workload, one
// namespace and one cluster, not two of each.
func TestBothViewsMeetOnOneWorkload(t *testing.T) {
	w := newWorld(t)
	// The build cluster's own nodes have no device here; only this
	// collector's batch (the last) must be accepted without a warning.
	for _, wn := range w.results[len(w.results)-1].Warnings {
		t.Errorf("kubemetrics batch: engine warning: %s", wn)
	}

	if n := w.count("k8s_cluster", "dev"); n != 1 {
		t.Errorf("%d k8s_cluster dev, want 1", n)
	}
	if n := w.count("namespace", "orion"); n != 1 {
		t.Errorf("%d namespace orion, want 1", n)
	}
	wl := w.find("workload", "orion")
	if wl.Attrs["kind"] != "deployment" {
		t.Errorf("workload kind %v, want deployment", wl.Attrs["kind"])
	}
	if !w.related("runs_on", w.instance().ID, wl.ID) {
		t.Error("the instance does not run on the workload")
	}

	for _, pod := range []struct{ name, node string }{
		{"orion-6d788dccb8-xfrbl", "kube-prod-02"},
		{"orion-6d788dccb8-zzcsf", "kube-prod-03"},
	} {
		p := w.find("pod", pod.name)
		if !w.related("served_by", wl.ID, p.ID) {
			t.Errorf("pod %s is not served_by the workload", pod.name)
		}
		if !w.related("hosts", w.find("k8s_node", pod.node).ID, p.ID) {
			t.Errorf("pod %s is not hosted by %s", pod.name, pod.node)
		}
	}
	for i, n := range []string{"kube-prod-01", "kube-prod-02", "kube-prod-03"} {
		if !w.related("hosts", w.device(i).ID, w.find("k8s_node", n).ID) {
			t.Errorf("device %d does not host node %s", i, n)
		}
	}
}

// TestSecondRunChangesNothing: the same two views again are a no-op, so the
// identities of both collectors really coincide.
func TestSecondRunChangesNothing(t *testing.T) {
	w := newWorld(t)
	before := len(w.objects())
	rels := len(w.relations())
	for _, b := range w.batches {
		res, err := w.engine.Apply(context.Background(), b, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
			t.Errorf("%s: second apply changed things: %+v", b.Source, res)
		}
	}
	if n := len(w.objects()); n != before {
		t.Errorf("%d objects after, %d before", n, before)
	}
	if n := len(w.relations()); n != rels {
		t.Errorf("%d relations after, %d before", n, rels)
	}
}

func (w *world) impact(failed ...*store.Object) map[string]string {
	w.t.Helper()
	snap := graph.Build(w.objects(), w.relations(), w.reg)
	ids := make([]string, len(failed))
	for i, f := range failed {
		ids[i] = f.ID
	}
	res, err := snap.Impact(ids, graph.ImpactOptions{})
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range res.Affected {
		out[a.ID] = a.State
	}
	return out
}

// TestImpactOfTheServers: when both servers that carry the pods of orion fail
// while a third node of the cluster is up, Kubernetes starts the pods again
// there: the workload and its instance are degraded, not down (ADR 0011
// amendment). When every server fails, they go down. When one of the two
// fails, the other pod still serves: degraded at most, never down.
func TestImpactOfTheServers(t *testing.T) {
	w := newWorld(t)
	wl, inst := w.find("workload", "orion"), w.instance()

	both := w.impact(w.device(1), w.device(2))
	if both[wl.ID] != "degraded" {
		t.Errorf("workload state %q with both servers down and a node left, want degraded", both[wl.ID])
	}
	if both[inst.ID] != "degraded" {
		t.Errorf("instance state %q with both servers down and a node left, want degraded", both[inst.ID])
	}

	all := w.impact(w.device(0), w.device(1), w.device(2))
	if all[wl.ID] != "down" {
		t.Errorf("workload state %q with every server down, want down", all[wl.ID])
	}
	if all[inst.ID] != "down" {
		t.Errorf("instance state %q with every server down, want down", all[inst.ID])
	}

	one := w.impact(w.device(1))
	if one[w.find("pod", "orion-6d788dccb8-xfrbl").ID] != "down" {
		t.Error("the pod on the failed server is not down")
	}
	if s := one[inst.ID]; s == "down" {
		t.Errorf("instance is down with one of two servers down")
	}
	if s := one[wl.ID]; s == "down" {
		t.Errorf("workload is down with one of two servers down")
	}

	// A server that hosts none of orion's pods does not touch it.
	idle := w.impact(w.device(0))
	if _, hit := idle[inst.ID]; hit && idle[inst.ID] == "down" {
		t.Error("server 01 hosts no orion pod but takes the instance down")
	}
	if !strings.HasPrefix(inst.Display, "orion") {
		t.Errorf("instance display %q", inst.Display)
	}
}
