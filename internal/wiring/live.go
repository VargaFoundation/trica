package wiring

import (
	"context"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/store"
)

// liveInventory is the read the live overlay joins measurements to.
type liveInventory struct{ st *store.Store }

func (l liveInventory) LiveObjects(ctx context.Context) ([]*store.Object, error) {
	return l.st.ObjectsOfTypes(ctx, live.InventoryTypes)
}

func (l liveInventory) LiveRelations(ctx context.Context) ([]store.Relation, error) {
	return l.st.CurrentRelationsBetween(ctx, "member_of", "interface", "interface")
}

// liveSource builds the live overlay when the configuration has a live
// block; nil otherwise, and the API answers enabled: false.
func (w *Wiring) liveSource(d server.Deps) (api.LiveSource, error) {
	if w.cfg.Live == nil {
		return nil, nil
	}
	svc, err := live.New(w.cfg.Live, live.Options{Inventory: liveInventory{st: d.Store}, Log: d.Log, Metrics: d.Metrics})
	if err != nil {
		return nil, err
	}
	return svc, nil
}
