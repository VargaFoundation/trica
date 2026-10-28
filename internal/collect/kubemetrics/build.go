package kubemetrics

import (
	"fmt"
	"sort"
	"strings"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

const (
	clusterRef = "cluster"
	phaseDone  = "Succeeded"
	phaseFail  = "Failed"
)

// controllerKinds maps an owner kind to the workload kind of the schema.
var controllerKinds = map[string]string{
	"Deployment":  "deployment",
	"StatefulSet": "statefulset",
	"DaemonSet":   "daemonset",
	"CronJob":     "cronjob",
	"Job":         "job",
}

// workloadOf is the workload a pod is served by.
type workloadOf struct{ kind, name string }

// build maps snap into b and returns the warnings of the mapping. Everything
// is sorted, so that two runs on an unchanged cluster emit identical batches.
func build(b *sdk.Batch, snap *snapshot, cluster, env string, finishedPods bool) []string {
	var warnings []string

	b.Object(clusterRef, "k8s_cluster").Identify("name", cluster)
	envRef := "env/" + env
	b.Object(envRef, "environment").Identify("name", env)
	b.Relate("deployed_in", clusterRef, envRef)

	// Nodes. A node reported twice (two kube-state-metrics replicas) is one.
	nodes := map[string]string{}
	ns := sortedSamples(snap.nodes, "node")
	for _, s := range ns {
		name := s.label("node")
		if name == "" || nodes[name] != "" {
			continue
		}
		ref := "node/" + name
		nodes[name] = ref
		uuid := s.label("system_uuid")
		b.Object(ref, "k8s_node").In(clusterRef).
			Identify("name", name).
			Set("system_uuid", uuid).
			Set("kubelet_version", s.label("kubelet_version")).
			Set("os_image", s.label("os_image")).
			Set("kernel", s.label("kernel_version")).
			Set("internal_ip", s.label("internal_ip"))
		// The node runs on a device; the engine finds it by the SMBIOS UUID
		// and records the link as unresolved when no device has it.
		if uuid != "" {
			b.RelateEndpoints("hosts",
				sdk.Endpoint{Type: "device", Identity: map[string]string{"hw_uuid": uuid}},
				sdk.Ref(ref))
		}
	}

	phase := map[string]string{}
	for _, s := range snap.phases {
		if s.isOne() {
			phase[s.label("namespace")+"/"+s.label("pod")] = s.label("phase")
		}
	}

	res := newResolver(snap)
	unmapped, orphans := map[string]int{}, 0

	seenNS := map[string]bool{}
	seenPod := map[string]bool{}
	for _, s := range sortedSamples(snap.pods, "namespace", "pod") {
		nsName, name := s.label("namespace"), s.label("pod")
		key := nsName + "/" + name
		if nsName == "" || name == "" || seenPod[key] {
			continue
		}
		seenPod[key] = true
		ph := phase[key]
		if !finishedPods && (ph == phaseDone || ph == phaseFail) {
			continue
		}
		nsRef := "ns/" + nsName
		if !seenNS[nsName] {
			seenNS[nsName] = true
			b.Object(nsRef, "namespace").In(clusterRef).Identify("name", nsName)
		}
		ref := "pod/" + key
		node := s.label("node")
		b.Object(ref, "pod").In(nsRef).
			Identify("name", name).
			Identify("uid", s.label("uid")).
			Set("phase", ph).
			Set("pod_ip", s.label("pod_ip")).
			Set("node_name", node)
		if nref, ok := nodes[node]; ok {
			b.Relate("hosts", nref, ref)
		}
		w, outcome := res.workload(nsName, name)
		switch outcome {
		case resolvedOK:
			wref := nsRef + "/" + w.kind + "/" + w.name
			b.Object(wref, "workload").In(nsRef).Identify("name", w.name).Set("kind", w.kind)
			b.Relate("served_by", wref, ref)
		case resolvedOrphan:
			orphans++
		case resolvedUnmapped:
			unmapped[w.kind]++
		}
	}
	if orphans > 0 {
		warnings = append(warnings, fmt.Sprintf("%d pods are owned by a ReplicaSet that has no Deployment: they have no workload", orphans))
	}
	if len(unmapped) > 0 {
		var kinds []string
		for k, n := range unmapped {
			kinds = append(kinds, fmt.Sprintf("%s: %d", k, n))
		}
		sort.Strings(kinds)
		warnings = append(warnings, "pods controlled by a kind that is no workload have none ("+strings.Join(kinds, ", ")+")")
	}
	return warnings
}

// outcome of resolving the workload of a pod.
type outcome int

const (
	resolvedNone     outcome = iota // a bare pod, or the owners are unknown
	resolvedOK                      // a workload
	resolvedOrphan                  // a ReplicaSet with no Deployment
	resolvedUnmapped                // a controller of a kind that is no workload
)

type owner struct{ kind, name string }

// ownerSet maps an object ("ns/name") to its owner. An object with several
// owners keeps its controller, else the first one in the order of the calls.
type ownerSet struct {
	owner map[string]owner
	ctl   map[string]bool
}

func newOwnerSet(series []sample, nameLabel string) ownerSet {
	set := ownerSet{owner: map[string]owner{}, ctl: map[string]bool{}}
	for _, s := range sortedSamples(series, "namespace", nameLabel, "owner_kind", "owner_name") {
		kind, name := s.label("owner_kind"), s.label("owner_name")
		if kind == "" || kind == "<none>" || name == "" {
			continue
		}
		key := s.label("namespace") + "/" + s.label(nameLabel)
		isCtl := s.label("owner_is_controller") == "true"
		if _, ok := set.owner[key]; ok && (set.ctl[key] || !isCtl) {
			continue
		}
		set.owner[key], set.ctl[key] = owner{kind, name}, isCtl
	}
	return set
}

// resolver follows pod -> ReplicaSet -> Deployment and pod -> Job -> CronJob
// through the owner series.
type resolver struct {
	pods, rs, jobs  ownerSet
	haveRS, haveJob bool // a family that could not be read resolves nothing
}

func newResolver(snap *snapshot) *resolver {
	return &resolver{
		pods:   newOwnerSet(snap.owners, "pod"),
		rs:     newOwnerSet(snap.replicaSets, "replicaset"),
		jobs:   newOwnerSet(snap.jobs, "job_name"),
		haveRS: snap.haveRS, haveJob: snap.haveJob,
	}
}

// workload returns the workload of a pod. For an outcome of resolvedUnmapped
// the kind field carries the owner's kind.
func (r *resolver) workload(ns, pod string) (workloadOf, outcome) {
	o, ok := r.pods.owner[ns+"/"+pod]
	if !ok {
		return workloadOf{}, resolvedNone
	}
	switch o.kind {
	case "StatefulSet", "DaemonSet":
		return workloadOf{controllerKinds[o.kind], o.name}, resolvedOK
	case "ReplicaSet":
		if !r.haveRS {
			return workloadOf{}, resolvedNone
		}
		if up, ok := r.rs.owner[ns+"/"+o.name]; ok && up.kind == "Deployment" {
			return workloadOf{"deployment", up.name}, resolvedOK
		}
		return workloadOf{}, resolvedOrphan
	case "Job":
		if !r.haveJob {
			return workloadOf{}, resolvedNone
		}
		if up, ok := r.jobs.owner[ns+"/"+o.name]; ok && up.kind == "CronJob" {
			return workloadOf{"cronjob", up.name}, resolvedOK
		}
		return workloadOf{"job", o.name}, resolvedOK
	}
	return workloadOf{kind: o.kind}, resolvedUnmapped
}

// sortedSamples returns ss ordered by the given labels.
func sortedSamples(ss []sample, labels ...string) []sample {
	out := append([]sample(nil), ss...)
	sort.SliceStable(out, func(i, j int) bool {
		for _, l := range labels {
			if a, b := out[i].label(l), out[j].label(l); a != b {
				return a < b
			}
		}
		// Two series that agree on the sort labels (two kube-state-metrics
		// replicas): the whole label set decides, so the result does not
		// depend on the order the server answered in.
		return signature(out[i]) < signature(out[j])
	})
	return out
}

// signature is the series' labels as one comparable text.
func signature(s sample) string {
	keys := make([]string, 0, len(s.Metric))
	for k := range s.Metric {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k + "=" + s.Metric[k] + "\x00")
	}
	return sb.String()
}
