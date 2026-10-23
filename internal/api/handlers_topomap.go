package api

import (
	"errors"
	"time"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/topomap"
)

// mapMaxStale bounds how far behind the last commit a map may be when the
// shared graph was invalidated and not yet reloaded (ADR 0051).
// default of Config.MapMaxStale.
const mapMaxStale = 10 * time.Second

// mapSnapshot is the inventory graph the map is computed from: the shared
// cache when wired, else one loaded for the request.
func (s *Server) mapSnapshot(c *call) (*graph.Snapshot, error) {
	if s.graph != nil {
		return s.graph.Recent(c.ctx(), s.mapStale)
	}
	objs, err := s.st.AllObjects(c.ctx())
	if err != nil {
		return nil, err
	}
	rels, err := s.st.CurrentRelations(c.ctx())
	if err != nil {
		return nil, err
	}
	return graph.Build(objs, rels, s.eng.Schema()), nil
}

// hTopologyMap serves one level of the topology map.
func (s *Server) hTopologyMap(c *call) error {
	q := c.r.URL.Query()
	level, id := q.Get("level"), q.Get("id")
	if level == "" {
		return badRequest("level is required (global, site, room, rack or unplaced)")
	}
	if id != "" {
		var err error
		if id, err = parseUUID(id, "id"); err != nil {
			return err
		}
	}
	snap, err := s.mapSnapshot(c)
	if err != nil {
		return err
	}
	opt := topomap.Options{Allow: func(t, site string) bool {
		return s.auth.Allow(c.p, ActionRead, t, site)
	}}
	var ls *live.Snapshot
	if s.live != nil {
		ls = s.live.Snapshot(c.ctx())
		opt.Live = ls.Links
	}
	m, err := topomap.IndexOf(snap).View(level, id, opt)
	switch {
	case errors.Is(err, topomap.ErrLevel), errors.Is(err, topomap.ErrMismatch):
		return badRequest("%s", err.Error())
	case errors.Is(err, topomap.ErrNotFound):
		return notFound("no such place on the map")
	case errors.Is(err, topomap.ErrForbidden):
		return forbidden("the place is not readable")
	case err != nil:
		return err
	}
	if ls != nil {
		m.Live = topomap.MapLive{Enabled: true, GeneratedAt: ls.GeneratedAt, Stale: ls.Stale, RefreshSeconds: int(ls.Refresh / time.Second)}
	}
	return c.json(200, m)
}
