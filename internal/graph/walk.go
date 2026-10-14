package graph

import (
	"fmt"
	"sort"
)

// ParentRelation names the containment of an object in its parent when a
// walk reports or filters it, as if it were a relation from parent to child.
const ParentRelation = "parent"

// Hop is one object reached by a walk, with the edge that reached it.
type Hop struct {
	Node  *Node `json:"node"`
	Depth int   `json:"depth"`
	// Via is the edge followed to get here; nil for the starting object.
	Via *Edge `json:"via,omitempty"`
}

// step is one undirected move from an object.
type step struct {
	to  string
	via Edge
}

// steps lists the moves from id: relations in both directions, its parent
// and its children. relTypes, when non-empty, keeps only those relation
// types (ParentRelation for containment).
func (s *Snapshot) steps(id string, relTypes map[string]bool) []step {
	keep := func(t string) bool { return len(relTypes) == 0 || relTypes[t] }
	var out []step
	n := s.Nodes[id]
	if keep(ParentRelation) {
		if n.ParentID != "" && s.Nodes[n.ParentID] != nil {
			out = append(out, step{n.ParentID, Edge{Type: ParentRelation, From: n.ParentID, To: id}})
		}
		for _, c := range s.Children[id] {
			out = append(out, step{c, Edge{Type: ParentRelation, From: id, To: c}})
		}
	}
	for _, e := range s.Out[id] {
		if keep(e.Type) {
			out = append(out, step{e.To, e})
		}
	}
	for _, e := range s.In[id] {
		if keep(e.Type) {
			out = append(out, step{e.From, e})
		}
	}
	return out
}

func typeSet(types []string) map[string]bool {
	m := make(map[string]bool, len(types))
	for _, t := range types {
		m[t] = true
	}
	return m
}

// Neighbors returns the objects within depth steps of id, the starting object
// first, then by depth. Relations are followed in both directions.
func (s *Snapshot) Neighbors(id string, depth int, relTypes []string) ([]Hop, error) {
	if s.Nodes[id] == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownObject, id)
	}
	types := typeSet(relTypes)
	out := []Hop{{Node: s.Nodes[id]}}
	seen := map[string]bool{id: true}
	frontier := []string{id}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		var level []Hop
		for _, cur := range frontier {
			for _, st := range s.steps(cur, types) {
				if seen[st.to] {
					continue
				}
				seen[st.to] = true
				via := st.via
				level = append(level, Hop{Node: s.Nodes[st.to], Depth: d, Via: &via})
				next = append(next, st.to)
			}
		}
		sort.Slice(level, func(i, j int) bool {
			a, b := level[i].Node, level[j].Node
			if a.Type != b.Type {
				return a.Type < b.Type
			}
			if a.Display != b.Display {
				return a.Display < b.Display
			}
			return a.ID < b.ID
		})
		out = append(out, level...)
		frontier = next
	}
	return out, nil
}

// ShortestPath returns the hops from one object to another, both included,
// over relations in either direction, or nil when they are not connected.
func (s *Snapshot) ShortestPath(from, to string, relTypes []string) ([]Hop, error) {
	for _, id := range []string{from, to} {
		if s.Nodes[id] == nil {
			return nil, fmt.Errorf("%w: %s", ErrUnknownObject, id)
		}
	}
	types := typeSet(relTypes)
	prev := map[string]step{from: {}}
	queue := []string{from}
	for len(queue) > 0 && !hasKey(prev, to) {
		cur := queue[0]
		queue = queue[1:]
		for _, st := range s.steps(cur, types) {
			if hasKey(prev, st.to) {
				continue
			}
			prev[st.to] = step{to: cur, via: st.via}
			queue = append(queue, st.to)
		}
	}
	if !hasKey(prev, to) {
		return nil, nil
	}
	var rev []Hop
	for id := to; id != from; id = prev[id].to {
		via := prev[id].via
		rev = append(rev, Hop{Node: s.Nodes[id], Via: &via})
	}
	path := make([]Hop, 0, len(rev)+1)
	path = append(path, Hop{Node: s.Nodes[from]})
	for i := len(rev) - 1; i >= 0; i-- {
		path = append(path, rev[i])
	}
	for i := range path {
		path[i].Depth = i
	}
	return path, nil
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}
