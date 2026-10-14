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
//     down when every target it has of that relation type is down;
//   - a device whose power supplies (psu children) are all down is down;
//   - degrade relations (calls): the From is degraded, and it stops there;
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
				if p.allSuppliersDown(e.From, e.Type) {
					p.down(e.From, e.Type, id, d)
				}
			}
		}
		if n := p.s.Nodes[id]; n.Type == "psu" && n.ParentID != "" && p.allPSUsDown(n.ParentID) {
			p.down(n.ParentID, RulePower, id, d)
		}
	}
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

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
