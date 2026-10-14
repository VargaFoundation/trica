package mcp

import (
	"context"
	"errors"
)

// GraphQuerier answers the two questions the dependency graph serves: what
// breaks when objects fail, and how two objects are connected. The server
// depends on this interface, not on a graph implementation, so that the MCP
// tools are testable with a fake and the graph can evolve on its own.
//
// The types below mirror, field for field and JSON tag for JSON tag, the
// results of internal/graph (Snapshot.Impact, Snapshot.ShortestPath). The
// adapter that connects a *graph.Cache is a few lines (see ADR 0017):
// fetch the snapshot with Cache.Get, call Impact or ShortestPath, and convert
// the result by a JSON round trip.
type GraphQuerier interface {
	// Impact returns what the failure of the objects (ids) reaches. anchors
	// are the objects connectivity is measured from; empty means the graph's
	// defaults.
	Impact(ctx context.Context, failed, anchors []string) (*ImpactResult, error)
	// Path returns the hops from one object to another, both included, over
	// relations in either direction (restricted to relTypes when not empty),
	// or nil when they are not connected.
	Path(ctx context.Context, from, to string, relTypes []string) ([]Hop, error)
}

// ErrUnknownObject is what a GraphQuerier returns for an id it does not know
// (the graph is a cached snapshot and may lag the store).
var ErrUnknownObject = errors.New("object not in the dependency graph")

// Impact states.
const (
	StateDown     = "down"
	StateDegraded = "degraded"
)

// Affected is one object a failure reaches.
type Affected struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Display string `json:"display"`
	State   string `json:"state"`
	// Rule is why: "failed", "parent", "power", "unreachable", or the
	// relation type the failure travelled along.
	Rule string `json:"rule"`
	// Cause is the object the failure came from.
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

// GraphNode is an object in a path.
type GraphNode struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Display string `json:"display"`
}

// GraphEdge is the relation a hop travelled along.
type GraphEdge struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	To     string `json:"to"`
	Impact string `json:"impact,omitempty"`
}

// Hop is one object reached by a walk, with the edge that reached it.
type Hop struct {
	Node  *GraphNode `json:"node"`
	Depth int        `json:"depth"`
	// Via is nil for the starting object.
	Via *GraphEdge `json:"via,omitempty"`
}
