// Package graph holds the current inventory graph in memory and answers the
// questions that walk it: neighbourhoods, paths and impact (ADR 0011). Past
// states are read from SQL by the store; this package only knows "now".
//
// A Snapshot is immutable. The Cache swaps in a new one after the
// reconciliation engine commits, so readers never see a half-applied batch.
package graph

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// Node is one object of the current graph.
type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	ParentID string         `json:"parent_id,omitempty"`
	Display  string         `json:"display"`
	Status   string         `json:"status"`
	Attrs    map[string]any `json:"attributes,omitempty"`
}

// Edge is one current relation.
type Edge struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	To     string `json:"to"`
	Impact string `json:"impact,omitempty"`
}

// Snapshot is an immutable view of the current graph.
type Snapshot struct {
	Nodes    map[string]*Node
	Out      map[string][]Edge
	In       map[string][]Edge
	Children map[string][]string
	Built    time.Time
}

// Node returns the node with id, or nil.
func (s *Snapshot) Node(id string) *Node { return s.Nodes[id] }

// Build assembles a snapshot. Edges whose ends are unknown are dropped (an
// absent object's relations stay in the store, not in the live graph).
func Build(objects []*store.Object, relations []store.Relation, reg *schema.Registry) *Snapshot {
	s := &Snapshot{
		Nodes:    make(map[string]*Node, len(objects)),
		Out:      map[string][]Edge{},
		In:       map[string][]Edge{},
		Children: map[string][]string{},
		Built:    time.Now(),
	}
	for _, o := range objects {
		n := &Node{ID: o.ID, Type: o.Type, Display: o.Display, Status: o.Status, Attrs: o.Attrs}
		if o.ParentID != nil {
			n.ParentID = *o.ParentID
		}
		s.Nodes[o.ID] = n
	}
	for _, n := range s.Nodes {
		if n.ParentID != "" {
			if _, ok := s.Nodes[n.ParentID]; ok {
				s.Children[n.ParentID] = append(s.Children[n.ParentID], n.ID)
			}
		}
	}
	for _, c := range s.Children {
		sort.Strings(c)
	}
	for _, r := range relations {
		if s.Nodes[r.FromID] == nil || s.Nodes[r.ToID] == nil {
			continue
		}
		e := Edge{Type: r.Type, From: r.FromID, To: r.ToID}
		if reg != nil {
			if rt, err := reg.Relation(r.Type); err == nil {
				e.Impact = rt.Impact
			}
		}
		s.Out[r.FromID] = append(s.Out[r.FromID], e)
		s.In[r.ToID] = append(s.In[r.ToID], e)
	}
	return s
}

// Loader reads the current objects and relations.
type Loader interface {
	AllObjects(ctx context.Context) ([]*store.Object, error)
	CurrentRelations(ctx context.Context) ([]store.Relation, error)
}

// Cache keeps the latest snapshot and rebuilds it when told the inventory
// changed. Rebuilds are coalesced: many commits in a burst cost one reload.
type Cache struct {
	loader Loader
	schema func() *schema.Registry

	cur   atomic.Pointer[Snapshot]
	dirty atomic.Bool
	mu    sync.Mutex // serialises reloads
}

// NewCache returns an empty cache; call Refresh or Get to load it.
func NewCache(l Loader, reg func() *schema.Registry) *Cache {
	c := &Cache{loader: l, schema: reg}
	c.dirty.Store(true)
	return c
}

// Invalidate marks the snapshot stale. It is cheap and safe to call from the
// engine's OnCommit hook.
func (c *Cache) Invalidate() { c.dirty.Store(true) }

// Get returns a current snapshot, reloading it first if it is stale.
func (c *Cache) Get(ctx context.Context) (*Snapshot, error) {
	if !c.dirty.Load() {
		if s := c.cur.Load(); s != nil {
			return s, nil
		}
	}
	return c.Refresh(ctx)
}

// Refresh reloads the snapshot unconditionally (unless a concurrent caller
// just did it).
func (c *Cache) Refresh(ctx context.Context) (*Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty.Load() {
		if s := c.cur.Load(); s != nil {
			return s, nil
		}
	}
	// Clear first: a commit landing during the load marks it dirty again.
	c.dirty.Store(false)
	objs, err := c.loader.AllObjects(ctx)
	if err != nil {
		c.dirty.Store(true)
		return nil, err
	}
	rels, err := c.loader.CurrentRelations(ctx)
	if err != nil {
		c.dirty.Store(true)
		return nil, err
	}
	var reg *schema.Registry
	if c.schema != nil {
		reg = c.schema()
	}
	s := Build(objs, rels, reg)
	c.cur.Store(s)
	return s, nil
}
