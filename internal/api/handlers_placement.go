package api

import (
	"time"

	"github.com/VargaFoundation/trica/internal/store"
)

// placementDefaultLimit is the page size of the two placement reads: they feed
// a short list ("where it was", "recently taken out"), not a table.
const placementDefaultLimit = 20

// PlacedRack is the rack of a placement, with its status (a rack nobody sees
// any more is absent: the UI warns before putting a device back in it).
type PlacedRack struct {
	ObjectRef
	Status string `json:"status"`
}

// PlacementRecord is one period during which a device was mounted in a rack.
type PlacementRecord struct {
	Rack     PlacedRack `json:"rack"`
	Position *float64   `json:"position,omitempty"`
	Face     string     `json:"face,omitempty"`
	// Since is when the device was mounted there; Until when it was taken
	// out, and is absent for the current placement.
	Since time.Time  `json:"since"`
	Until *time.Time `json:"until,omitempty"`
	// Source is the source whose placement it was.
	Source string `json:"source"`
}

// RemovedItem is a device taken out of a rack: what the elevation knew of it
// (RackItem) and when, by which source's placement, and in which state.
type RemovedItem struct {
	RackItem
	// RemovedAt is when the placement ended.
	RemovedAt time.Time `json:"removed_at"`
	// RemovedBy is the source whose placement ended.
	RemovedBy string `json:"removed_by"`
	// Status is the device's status (an unmounted spare may be absent).
	Status string `json:"status"`
}

func placementPage(c *call) (pageParams, error) {
	pp, err := parsePage(c.r)
	if err != nil {
		return pp, err
	}
	if c.r.URL.Query().Get("limit") == "" {
		pp.Limit = placementDefaultLimit
	}
	return pp, nil
}

// hPlacementHistory lists where a device was mounted, the current placement
// first and then the closed ones, the most recently closed first (ADR 0053).
func (s *Server) hPlacementHistory(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	pp, err := placementPage(c)
	if err != nil {
		return err
	}
	dev, _, err := c.loadObject(id)
	if err != nil {
		return err
	}
	if dev.Type != "device" {
		return notFound("%s is a %s, not a device", id, dev.Type)
	}
	rels, err := s.st.MountHistory(c.ctx(), id, pp.Offset+pp.Limit+1)
	if err != nil {
		return err
	}
	truncated := len(rels) > pp.Offset+pp.Limit
	racks := make([]string, 0, len(rels))
	for _, r := range rels {
		racks = append(racks, r.ToID)
	}
	objs, _, err := c.visibleObjects(racks)
	if err != nil {
		return err
	}
	out := []PlacementRecord{}
	for _, r := range redactRelations(rels) {
		rack, ok := objs[r.ToID]
		if !ok {
			continue
		}
		rec := PlacementRecord{
			Rack:     PlacedRack{ObjectRef: ObjectRef{ID: rack.ID, Type: rack.Type, Display: rack.Display}, Status: rack.Status},
			Position: number(r.Attrs["position"]),
			Since:    r.ValidFrom,
			Until:    r.ValidTo,
			Source:   r.Source,
		}
		if f, _ := r.Attrs["face"].(string); f == "front" || f == "rear" {
			rec.Face = f
		}
		out = append(out, rec)
	}
	return c.json(200, pageOf(out, pp, truncated))
}

// hRackRemoved lists the devices taken out of a rack and not mounted since,
// the most recent first (ADR 0053).
func (s *Server) hRackRemoved(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	pp, err := placementPage(c)
	if err != nil {
		return err
	}
	since, err := optTime(c, "since")
	if err != nil {
		return err
	}
	rack, _, err := c.loadObject(id)
	if err != nil {
		return err
	}
	if rack.Type != "rack" {
		return notFound("%s is a %s, not a rack", id, rack.Type)
	}
	rels, err := s.st.RemovedFrom(c.ctx(), id, since, pp.Offset+pp.Limit+1)
	if err != nil {
		return err
	}
	truncated := len(rels) > pp.Offset+pp.Limit
	rels = redactRelations(rels)
	items, err := c.rackItems(rack, rels)
	if err != nil {
		return err
	}
	byDevice := map[string]store.Relation{}
	for _, r := range rels {
		byDevice[r.FromID] = r
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.Device.ID)
	}
	objs, _, err := c.visibleObjects(ids)
	if err != nil {
		return err
	}
	out := []RemovedItem{}
	for _, it := range items {
		r := byDevice[it.Device.ID]
		if r.ValidTo == nil {
			continue
		}
		it.Airflow = nil // it is not in the rack: nothing flows
		out = append(out, RemovedItem{RackItem: it, RemovedAt: *r.ValidTo, RemovedBy: r.Source, Status: objs[it.Device.ID].Status})
	}
	return c.json(200, pageOf(out, pp, truncated))
}
