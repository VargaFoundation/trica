package graph

import (
	"encoding/json"
	"sort"
)

// This file is the capacity rule of impact analysis (ADR 0011, amendment of
// 2026-11-10): a workload whose pods are all down is degraded while its
// cluster has a node left (RuleRescheduled), unless Kubernetes has nowhere to
// put them. The pods that were on the lost nodes are placed on the nodes left
// the way the scheduler would, by CPU and memory requests against what the
// nodes have allocatable; a workload none of whose pods finds a place is down
// (RuleCapacity).

// number reads a numeric attribute whatever the source decoded it as: the
// store hands back float64 or json.Number, collectors in tests int or int64.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// boolAttr reads a boolean attribute; ok is false when it is missing.
func boolAttr(v any) (value, ok bool) {
	b, ok := v.(bool)
	return b, ok
}

// room is what a surviving node has free.
type room struct {
	id  string
	cpu float64
	mem float64
}

// demand is a pod to place.
type demand struct {
	id       string
	cpu, mem float64
}

// epsilon absorbs the float noise of a sum of requests (CPU in cores).
const epsilon = 1e-9

// clusterOf returns the k8s_cluster above an object, or "".
func (p *propagation) clusterOf(id string) string {
	for cur := id; cur != ""; {
		n := p.s.Nodes[cur]
		if n == nil {
			return ""
		}
		if n.Type == "k8s_cluster" {
			return cur
		}
		cur = n.ParentID
	}
	return ""
}

// capacity applies the capacity rule to every cluster that has a workload
// degraded as rescheduled. It returns how many workloads it took down. It is
// a function of the propagation state alone, so that running it again on an
// unchanged state changes nothing: the workloads it took down were degraded
// and unplaced, and being down they leave the demand, which only frees room.
//
// Every k8s_node child of the cluster that is not absent, not down, not
// cordoned (unschedulable) and not NotReady (ready false) is a survivor. When
// any survivor lacks cpu_capacity or memory_capacity_bytes the capacity is
// unknown and nothing changes (the optimistic behaviour of RuleRescheduled).
// Missing request attributes count as 0. Daemonset pods are not moved and
// take no room; a daemonset workload is left as it is. Workloads that are
// down already (failed, parent, runs_on, capacity) are not
// recreated by Kubernetes either, so their pods are not demand.
//
// Cost: O(pods x survivors) per cluster, plus the sort of the demand.
func (p *propagation) capacity() int {
	// Workloads per cluster, in id order, and which of them are candidates.
	byCluster := map[string][]string{}
	candidates := map[string]bool{}
	for _, id := range sortedKeys(p.s.Nodes) {
		n := p.s.Nodes[id]
		if n.Type != "workload" || n.Status == StatusAbsent {
			continue
		}
		cl := p.clusterOf(id)
		if cl == "" || p.isDown(cl) {
			continue
		}
		byCluster[cl] = append(byCluster[cl], id)
		if a := p.state[id]; a != nil && a.State == StateDegraded && a.Rule == RuleRescheduled && !isDaemonSet(n) {
			candidates[cl] = true
		}
	}
	changed := 0
	for _, cl := range sortedKeys(candidates) {
		changed += p.capacityOf(cl, byCluster[cl])
	}
	return changed
}

func isDaemonSet(n *Node) bool {
	k, _ := n.Attrs["kind"].(string)
	return k == "daemonset"
}

func (p *propagation) capacityOf(cluster string, workloads []string) int {
	free, known := p.survivors(cluster)
	if !known {
		return 0
	}
	// The demand: the pods Kubernetes recreates elsewhere.
	var dem []demand
	seen := map[string]bool{}
	downPods := map[string][]string{} // workload -> its down pods
	for _, w := range workloads {
		n := p.s.Nodes[w]
		if isDaemonSet(n) {
			continue
		}
		if a := p.state[w]; a != nil && a.State == StateDown {
			continue
		}
		for _, e := range p.s.Out[w] {
			if e.Type != "served_by" {
				continue
			}
			pod := p.s.Nodes[e.To]
			if pod == nil || pod.Type != "pod" || pod.Status == StatusAbsent || !p.isDown(e.To) {
				continue
			}
			downPods[w] = append(downPods[w], e.To)
			if !seen[e.To] {
				seen[e.To] = true
				c, _ := number(pod.Attrs["cpu_request"])
				m, _ := number(pod.Attrs["memory_request_bytes"])
				dem = append(dem, demand{id: e.To, cpu: c, mem: m})
			}
		}
	}
	sort.Slice(dem, func(i, j int) bool {
		a, b := dem[i], dem[j]
		if a.cpu != b.cpu {
			return a.cpu > b.cpu
		}
		if a.mem != b.mem {
			return a.mem > b.mem
		}
		return a.id < b.id
	})
	// First fit, the node with the most free CPU first (the scheduler spreads);
	// free is in node id order, so a tie goes to the smaller id.
	placed := map[string]bool{}
	for _, d := range dem {
		best := -1
		for i := range free {
			if free[i].cpu+epsilon < d.cpu || free[i].mem+epsilon < d.mem {
				continue
			}
			if best < 0 || free[i].cpu > free[best].cpu {
				best = i
			}
		}
		if best >= 0 {
			free[best].cpu -= d.cpu
			free[best].mem -= d.mem
			placed[d.id] = true
		}
	}
	changed := 0
	for _, w := range workloads {
		a := p.state[w]
		if a == nil || a.State != StateDegraded || a.Rule != RuleRescheduled || isDaemonSet(p.s.Nodes[w]) {
			continue
		}
		pods := downPods[w]
		if len(pods) == 0 {
			continue
		}
		fits := false
		for _, pod := range pods {
			if placed[pod] {
				fits = true
				break
			}
		}
		if !fits {
			p.down(w, RuleCapacity, a.Cause, a.Depth)
			changed++
		}
	}
	return changed
}

// survivors returns what each node of the cluster that can take a pod has
// free, in node id order, and whether that is known: false when a survivor
// does not report its allocatable CPU or memory.
func (p *propagation) survivors(cluster string) ([]room, bool) {
	var out []room
	for _, c := range p.s.Children[cluster] { // sorted by id
		k := p.s.Nodes[c]
		if k.Type != "k8s_node" || k.Status == StatusAbsent || p.isDown(c) {
			continue
		}
		if cordoned, _ := boolAttr(k.Attrs["unschedulable"]); cordoned {
			continue
		}
		if ready, ok := boolAttr(k.Attrs["ready"]); ok && !ready {
			continue
		}
		cpu, okC := number(k.Attrs["cpu_capacity"])
		mem, okM := number(k.Attrs["memory_capacity_bytes"])
		if !okC || !okM {
			return nil, false
		}
		for _, e := range p.s.Out[c] {
			pod := p.s.Nodes[e.To]
			if e.Type != "hosts" || pod == nil || pod.Type != "pod" || pod.Status == StatusAbsent || p.isDown(e.To) {
				continue
			}
			rc, _ := number(pod.Attrs["cpu_request"])
			rm, _ := number(pod.Attrs["memory_request_bytes"])
			cpu -= rc
			mem -= rm
		}
		out = append(out, room{id: c, cpu: cpu, mem: mem})
	}
	return out, true
}
