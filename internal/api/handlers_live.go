package api

import (
	"time"

	"github.com/VargaFoundation/trica/internal/live"
)

// LiveTopology is the live overlay of the topology (ADR 0041).
type LiveTopology struct {
	// Enabled is false when the server has no live block: the UI shows the
	// inventory topology only.
	Enabled bool `json:"enabled"`
	// GeneratedAt is when the measurements were taken; absent before the
	// first successful refresh.
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	// Stale is true when the last refresh failed in part or in full.
	Stale bool `json:"stale"`
	// RefreshSeconds is how often the server refreshes: polling faster
	// returns the same set.
	RefreshSeconds int `json:"refresh_seconds"`
	// Links maps an interface id to its live state. An aggregate without
	// series of its own is the sum of its members (aggregated: true).
	Links map[string]live.Link `json:"links"`
	// Sources says what each configured source contributed; listed only to a
	// caller who reads interfaces in every site.
	Sources []live.SourceStatus `json:"sources"`
}

// hLiveTopology serves the cached overlay, restricted to the interfaces the
// caller may read. The endpoint behind it is never named and never reached
// by the browser.
func (s *Server) hLiveTopology(c *call) error {
	out := LiveTopology{Links: map[string]live.Link{}, Sources: []live.SourceStatus{}}
	if s.live == nil {
		return c.json(200, out)
	}
	snap := s.live.Snapshot(c.ctx())
	out.Enabled, out.GeneratedAt, out.Stale = true, snap.GeneratedAt, snap.Stale
	out.RefreshSeconds = int(snap.Refresh / time.Second)
	if s.auth.Allow(c.p, ActionRead, "interface", "") {
		for id, l := range snap.Links {
			out.Links[id] = l
		}
		out.Sources = append(out.Sources, snap.Sources...)
		return c.json(200, out)
	}
	ids := make([]string, 0, len(snap.Links))
	for id := range snap.Links {
		ids = append(ids, id)
	}
	visible, _, err := c.visibleObjects(ids)
	if err != nil {
		return err
	}
	for id := range visible {
		out.Links[id] = snap.Links[id]
	}
	return c.json(200, out)
}
