package graph

import (
	"errors"
	"fmt"
	"sort"

	"github.com/VargaFoundation/trica/internal/schema"
)

// ErrUnknownObject is returned when a query names an object the snapshot does
// not hold.
var ErrUnknownObject = errors.New("graph: unknown object")

// StatusAbsent is the status of an object nobody sees any more. Absent
// objects take no part in impact: they are already gone.
const StatusAbsent = "absent"

// Impact states.
const (
	StateDown     = "down"
	StateDegraded = "degraded"
)

// Rules that explain why an object is affected.
const (
	RuleFailed      = "failed"      // named in the query
	RuleParent      = "parent"      // its container is down
	RulePower       = "power"       // every power supply it contains is down
	RuleUnreachable = "unreachable" // cut from every anchor by the failure
	// RuleRescheduled: every pod of a workload is down but a node of its
	// cluster is up, so Kubernetes starts them again there: degraded, not down.
	RuleRescheduled = "rescheduled"
	// RuleInstances: an application is down when all its instances are, and
	// degraded when one of them is down or degraded.
	RuleInstances = "instances"
)

// ImpactOptions tunes an impact query.
type ImpactOptions struct {
	// Anchors are the objects connectivity is measured from. Empty means the
	// defaults: every internet_access, and every device whose role is
	// firewall or router.
	Anchors []string
}

// Affected is one object the failure reaches.
type Affected struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Display string `json:"display"`
	State   string `json:"state"`
	// Rule is RuleFailed, RuleParent, RulePower, RuleUnreachable, or the
	// relation type the failure travelled along.
	Rule string `json:"rule"`
	// Cause is the object the failure came from (empty for RuleFailed and
	// RuleUnreachable).
	Cause string `json:"cause,omitempty"`
	// Depth is the number of propagation steps from the failed objects.
	Depth int `json:"depth"`
}

// ImpactResult answers "what breaks if these objects go down".
type ImpactResult struct {
	Failed   []string       `json:"failed"`
	Affected []Affected     `json:"affected"` // failed objects excluded
	Down     map[string]int `json:"down"`     // per object type
	Degraded map[string]int `json:"degraded"` // per object type
}

// Impact computes what fails when the given objects fail. It is a pure
// function of the snapshot (ADR 0011):
//
//   - containment: a parent down takes its children down; so does a from_to
//     relation (hosts, contains) from its From to its To;
//   - to_from relations (runs_on, depends_on): the To down takes the From down;
//   - supply relations (powered_by, served_by, routes_via): the From goes
//     down when every target it has of that relation type is down, and is
//     degraded while only some are (its redundancy is gone);
//   - a workload whose pods are all down is only degraded while a node of its
//     cluster is up (Kubernetes starts them again there), down when none is;
//   - a device whose power supplies (psu children) are all down is down;
//   - degrade relations (calls): the From is degraded;
//   - a degraded object degrades what runs on or depends on it (to_from
//     relations), nothing else;
//   - an application is down when all its instances are, degraded when one of
//     them is down or degraded;
//   - link relations (cabled_to, maps_to): an object that could reach an
//     anchor before the failure and cannot any more is down.
func (s *Snapshot) Impact(failed []string, opt ImpactOptions) (*ImpactResult, error) {
	for _, id := range failed {
		if s.Nodes[id] == nil {
			return nil, fmt.Errorf("%w: %s", ErrUnknownObject, id)
		}
	}
	p := &propagation{s: s, state: map[string]*Affected{}}
	for _, id := range failed {
		p.down(id, RuleFailed, "", 0)
	}
	anchors := s.anchors(opt.Anchors)
	var before map[string]bool
	if len(anchors) > 0 {
		before = s.reachable(anchors, nil)
	}
	for {
		p.drain()
		if p.settle() > 0 {
			continue
		}
		if before == nil {
			break
		}
		after := s.reachable(anchors, p.isDown)
		depth := p.maxDepth() + 1
		cut := 0
		for _, id := range sortedKeys(before) {
			if !after[id] && !p.isDown(id) {
				p.down(id, RuleUnreachable, "", depth)
				cut++
			}
		}
		if cut == 0 {
			break
		}
	}
	return p.result(failed), nil
}

type propagation struct {
	s     *Snapshot
	state map[string]*Affected
	queue []string
	// degraded holds the objects newly degraded, whose to_from dependents
	// are degraded in turn.
	degraded []string
}

func (p *propagation) isDown(id string) bool {
	a := p.state[id]
	return a != nil && a.State == StateDown
}

func (p *propagation) maxDepth() int {
	m := 0
	for _, a := range p.state {
		m = max(m, a.Depth)
	}
	return m
}

func (p *propagation) down(id, rule, cause string, depth int) {
	n := p.s.Nodes[id]
	if n == nil || n.Status == StatusAbsent || p.isDown(id) {
		return
	}
	p.state[id] = &Affected{ID: id, Type: n.Type, Display: n.Display, State: StateDown, Rule: rule, Cause: cause, Depth: depth}
	p.queue = append(p.queue, id)
}

func (p *propagation) degrade(id, rule, cause string, depth int) {
	n := p.s.Nodes[id]
	if n == nil || n.Status == StatusAbsent || p.state[id] != nil {
		return
	}
	p.state[id] = &Affected{ID: id, Type: n.Type, Display: n.Display, State: StateDegraded, Rule: rule, Cause: cause, Depth: depth}
	p.degraded = append(p.degraded, id)
}

// drain propagates every queued failure until nothing changes. Each object
// enters the queue at most once, so it runs in O(nodes + edges).
func (p *propagation) drain() {
	for len(p.queue) > 0 {
		id := p.queue[0]
		p.queue = p.queue[1:]
		d := p.state[id].Depth + 1
		for _, c := range p.s.Children[id] {
			p.down(c, RuleParent, id, d)
		}
		for _, e := range p.s.Out[id] {
			if e.Impact == schema.ImpactFromTo {
				p.down(e.To, e.Type, id, d)
			}
		}
		for _, e := range p.s.In[id] {
			switch e.Impact {
			case schema.ImpactToFrom:
				p.down(e.From, e.Type, id, d)
			case schema.ImpactDegrade:
				p.degrade(e.From, e.Type, id, d)
			case schema.ImpactSupply:
				switch {
				case !p.allSuppliersDown(e.From, e.Type):
					p.degrade(e.From, e.Type, id, d)
				case p.reschedulable(e.From):
					p.degrade(e.From, RuleRescheduled, id, d)
				default:
					p.down(e.From, e.Type, id, d)
				}
			}
		}
		if n := p.s.Nodes[id]; n.Type == "psu" && n.ParentID != "" && p.allPSUsDown(n.ParentID) {
			p.down(n.ParentID, RulePower, id, d)
		}
		p.drainDegraded()
	}
	p.drainDegraded()
}

// drainDegraded degrades what runs on or depends on a degraded object.
func (p *propagation) drainDegraded() {
	for len(p.degraded) > 0 {
		id := p.degraded[0]
		p.degraded = p.degraded[1:]
		a := p.state[id]
		if a == nil || a.State != StateDegraded {
			continue
		}
		for _, e := range p.s.In[id] {
			if e.Impact == schema.ImpactToFrom {
				p.degrade(e.From, e.Type, id, a.Depth+1)
			}
		}
	}
}

// settle applies the rules that look at a whole set once the propagation is
// still: a workload left degraded because its cluster had a live node goes
// down when no node of that cluster is up any more; an application takes the
// state of its instances. It returns how many objects it took down or
// degraded, so that the caller propagates again.
func (p *propagation) settle() int {
	changed := 0
	for _, id := range sortedKeys(p.s.Nodes) {
		n := p.s.Nodes[id]
		switch n.Type {
		case "workload":
			if a := p.state[id]; a != nil && a.State == StateDegraded && p.allSuppliersDown(id, "served_by") && len(p.s.Out[id]) > 0 && !p.reschedulable(id) {
				p.down(id, "served_by", a.Cause, a.Depth)
				changed++
			}
		case "application":
			if p.state[id] != nil && p.state[id].State == StateDown {
				continue
			}
			down, hit, total, cause, depth := 0, 0, 0, "", 0
			for _, inst := range p.instancesOf(id) {
				total++
				if a := p.state[inst]; a != nil {
					hit++
					if a.State == StateDown {
						down++
					}
					if cause == "" || a.Depth > depth {
						cause, depth = inst, a.Depth
					}
				}
			}
			switch {
			case total > 0 && down == total:
				p.down(id, RuleInstances, cause, depth+1)
				changed++
			case hit > 0 && p.state[id] == nil:
				p.degrade(id, RuleInstances, cause, depth+1)
				p.drainDegraded()
				changed++
			}
		}
	}
	return changed
}

// reschedulable says whether a workload whose pods are down can have them
// started again: it is in a cluster that still has a node up.
func (p *propagation) reschedulable(id string) bool {
	n := p.s.Nodes[id]
	if n == nil || n.Type != "workload" {
		return false
	}
	cluster := n.ParentID
	for cluster != "" && p.s.Nodes[cluster] != nil && p.s.Nodes[cluster].Type != "k8s_cluster" {
		cluster = p.s.Nodes[cluster].ParentID
	}
	if cluster == "" || p.s.Nodes[cluster] == nil || p.isDown(cluster) {
		return false
	}
	for _, c := range p.s.Children[cluster] {
		if k := p.s.Nodes[c]; k.Type == "k8s_node" && k.Status != StatusAbsent && !p.isDown(c) {
			return true
		}
	}
	return false
}

// instancesOf lists the component instances below an application (in its
// components), absent ones left out.
func (p *propagation) instancesOf(app string) []string {
	var out []string
	for _, c := range p.s.Children[app] {
		for _, i := range p.s.Children[c] {
			if n := p.s.Nodes[i]; n.Type == "component_instance" && n.Status != StatusAbsent {
				out = append(out, i)
			}
		}
	}
	return out
}

func (p *propagation) allSuppliersDown(id, relType string) bool {
	for _, e := range p.s.Out[id] {
		if e.Type == relType && !p.isDown(e.To) {
			return false
		}
	}
	return true
}

func (p *propagation) allPSUsDown(device string) bool {
	for _, c := range p.s.Children[device] {
		if n := p.s.Nodes[c]; n.Type == "psu" && n.Status != StatusAbsent && !p.isDown(c) {
			return false
		}
	}
	return true
}

func (p *propagation) result(failed []string) *ImpactResult {
	r := &ImpactResult{Failed: append([]string(nil), failed...), Affected: []Affected{}, Down: map[string]int{}, Degraded: map[string]int{}}
	sort.Strings(r.Failed)
	for _, a := range p.state {
		if a.Rule == RuleFailed {
			continue
		}
		r.Affected = append(r.Affected, *a)
		if a.State == StateDown {
			r.Down[a.Type]++
		} else {
			r.Degraded[a.Type]++
		}
	}
	sort.Slice(r.Affected, func(i, j int) bool {
		a, b := r.Affected[i], r.Affected[j]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Display != b.Display {
			return a.Display < b.Display
		}
		return a.ID < b.ID
	})
	return r
}

// anchors returns the explicit anchors that exist, or the default ones.
func (s *Snapshot) anchors(explicit []string) []string {
	var out []string
	if len(explicit) > 0 {
		for _, id := range explicit {
			if s.Nodes[id] != nil {
				out = append(out, id)
			}
		}
		return out
	}
	for id, n := range s.Nodes {
		if n.Status == StatusAbsent {
			continue
		}
		if n.Type == "internet_access" {
			out = append(out, id)
			continue
		}
		if n.Type == "device" {
			if role, _ := n.Attrs["role"].(string); role == "firewall" || role == "router" {
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

// reachable walks the link layer from the anchors: link relations in both
// directions, and an object to and from its children, so that a device is
// reached through its ports. Objects for which skip returns true block the
// walk. Only objects on that layer are returned.
func (s *Snapshot) reachable(anchors []string, skip func(string) bool) map[string]bool {
	blocked := func(id string) bool {
		n := s.Nodes[id]
		return n == nil || n.Status == StatusAbsent || (skip != nil && skip(id))
	}
	seen := map[string]bool{}
	var stack []string
	visit := func(id string) {
		if !seen[id] && !blocked(id) {
			seen[id] = true
			stack = append(stack, id)
		}
	}
	for _, a := range anchors {
		visit(a)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := s.Nodes[id]
		if n.ParentID != "" && s.hasLinks(id) {
			visit(n.ParentID)
		}
		for _, c := range s.Children[id] {
			if s.hasLinks(c) {
				visit(c)
			}
		}
		for _, e := range s.Out[id] {
			if e.Impact == schema.ImpactLink {
				visit(e.To)
			}
		}
		for _, e := range s.In[id] {
			if e.Impact == schema.ImpactLink {
				visit(e.From)
			}
		}
	}
	return seen
}

// hasLinks reports whether id ends at least one link relation: only such
// children (ports) connect their parent to the rest of the network.
func (s *Snapshot) hasLinks(id string) bool {
	for _, e := range s.Out[id] {
		if e.Impact == schema.ImpactLink {
			return true
		}
	}
	for _, e := range s.In[id] {
		if e.Impact == schema.ImpactLink {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
