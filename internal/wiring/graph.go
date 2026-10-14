package wiring

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/mcp"
)

// GraphAdapter makes a *graph.Cache an mcp.GraphQuerier (issue #29): the two
// packages define the same result types independently, so that the MCP tools
// are testable with a fake; this converts, field for field, by a JSON round
// trip.
type GraphAdapter struct {
	Cache *graph.Cache
}

var _ mcp.GraphQuerier = GraphAdapter{}

// Impact implements mcp.GraphQuerier on the current snapshot (reloaded first
// when a commit made it stale).
func (a GraphAdapter) Impact(ctx context.Context, failed, anchors []string) (*mcp.ImpactResult, error) {
	snap, err := a.Cache.Get(ctx)
	if err != nil {
		return nil, err
	}
	res, err := snap.Impact(failed, graph.ImpactOptions{Anchors: anchors})
	return convert[mcp.ImpactResult](res, err)
}

// Path implements mcp.GraphQuerier.
func (a GraphAdapter) Path(ctx context.Context, from, to string, relTypes []string) ([]mcp.Hop, error) {
	snap, err := a.Cache.Get(ctx)
	if err != nil {
		return nil, err
	}
	hops, err := snap.ShortestPath(from, to, relTypes)
	if hops == nil && err == nil {
		return nil, nil
	}
	out, err := convert[[]mcp.Hop](hops, err)
	if out == nil {
		return nil, err
	}
	return *out, err
}

// convert maps graph.ErrUnknownObject to mcp.ErrUnknownObject and passes any
// other error through; v is converted by a JSON round trip.
func convert[T any](v any, err error) (*T, error) {
	if errors.Is(err, graph.ErrUnknownObject) {
		return nil, mcp.ErrUnknownObject
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
