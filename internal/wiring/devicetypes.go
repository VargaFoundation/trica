package wiring

import (
	"context"
	"time"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/server"
)

// runDeviceTypes matches the inventory's devices against the embedded device
// type library at start, after the migrations, then every deduce.interval, so
// that a device discovered after the start gets its device type without a
// restart (ADR 0020, 0025, #55). A failed pass is not fatal: the library
// enriches the inventory, and a failure here (a conflicting schema overlay, a
// database hiccup) must not keep the API down. It is logged and the next tick
// retries. Every replica runs it: the batch is the same full declaration
// whoever submits it.
func (w *Wiring) runDeviceTypes(ctx context.Context, d server.Deps) error {
	models, err := devicetypes.Default()
	if err != nil {
		d.Log.Error("device type library: not loaded", "error", err.Error())
		return nil
	}
	ix := devicetypes.NewIndex(models)
	t := time.NewTicker(w.cfg.Deduce.Interval)
	defer t.Stop()
	for {
		syncDeviceTypes(ctx, d, ix, len(models))
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func syncDeviceTypes(ctx context.Context, d server.Deps, ix *devicetypes.Index, models int) {
	res, links, err := devicetypes.Sync(ctx, d.Engine, ix, "devicetypes")
	if err != nil {
		if ctx.Err() == nil {
			d.Log.Error("device type library: sync failed", "error", err.Error())
		}
		return
	}
	d.Log.Info("device type library synced", "models", models, "devices_matched", len(links), "created", res.Created)
}
