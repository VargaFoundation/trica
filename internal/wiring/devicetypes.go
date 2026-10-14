package wiring

import (
	"context"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/server"
)

// runDeviceTypes matches the inventory's devices against the embedded device
// type library once, at start, after the migrations (ADR 0020, 0025). It is
// not fatal: the library enriches the inventory, and a failure here (a
// conflicting schema overlay, a database hiccup) must not keep the API down.
// The error is logged and the next start retries.
func (w *Wiring) runDeviceTypes(ctx context.Context, d server.Deps) error {
	models, err := devicetypes.Default()
	if err != nil {
		d.Log.Error("device type library: not loaded", "error", err.Error())
		return nil
	}
	res, links, err := devicetypes.Sync(ctx, d.Engine, devicetypes.NewIndex(models), "devicetypes")
	if err != nil {
		if ctx.Err() == nil {
			d.Log.Error("device type library: sync failed", "error", err.Error())
		}
		return nil
	}
	d.Log.Info("device type library synced", "models", len(models), "devices_matched", len(links), "created", res.Created)
	return nil
}
