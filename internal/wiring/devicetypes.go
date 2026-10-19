package wiring

import (
	"context"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/server"
)

// library loads the device type library once: the embedded models and images
// with the operator's devicetypes.extra_dir over them (ADR 0020, 0042). The
// API serves its images and runDeviceTypes matches its models, so both see
// the same files. A library that does not load is logged once and leaves
// both without it: the library enriches the inventory, it must not keep the
// API down.
func (w *Wiring) library(d server.Deps) *devicetypes.Index {
	w.libOnce.Do(func() {
		lib, err := devicetypes.Open(w.cfg.DeviceTypes.ExtraDir)
		if err != nil {
			d.Log.Error("device type library: not loaded", "error", err.Error())
			return
		}
		for _, warn := range lib.Warnings {
			d.Log.Warn("device type library: operator directory", "detail", warn)
		}
		images, layouts := 0, 0
		for _, m := range lib.Models {
			for _, face := range []string{devicetypes.Front, devicetypes.Rear} {
				if m.Image(face) != nil {
					images++
				}
				if m.Layout(face) != nil {
					layouts++
				}
			}
		}
		d.Log.Info("device type library loaded", "models", len(lib.Models), "images", images, "layouts", layouts,
			"extra_dir", w.cfg.DeviceTypes.ExtraDir)
		w.lib = devicetypes.NewIndex(lib.Models)
	})
	return w.lib
}

// Images serves the face images of a device type library index to the API.
type Images struct{ Index *devicetypes.Index }

// DeviceTypeImage implements api.DeviceTypeImages.
func (i Images) DeviceTypeImage(slug, face string) (api.DeviceImage, bool) {
	if i.Index == nil {
		return api.DeviceImage{}, false
	}
	m := i.Index.BySlug(slug)
	if m == nil {
		return api.DeviceImage{}, false
	}
	img := m.Image(face)
	if img == nil {
		return api.DeviceImage{}, false
	}
	out := api.DeviceImage{ContentType: img.ContentType, ETag: img.ETag, Data: img.Data}
	if l := m.Layout(face); l != nil {
		out.Layout = &api.FaceLayout{Width: l.WidthMM, Height: l.HeightMM, Origin: l.Origin, Spots: make([]api.FaceSpot, len(l.Spots))}
		for i, sp := range l.Spots {
			out.Layout.Spots[i] = api.FaceSpot{Kind: sp.Kind, Name: sp.Name, X: sp.X, Y: sp.Y, W: sp.W, H: sp.H}
		}
	}
	return out, true
}

// DeviceTypeChoices implements api.DeviceTypeLibrary.
func (i Images) DeviceTypeChoices(vendor, model, partNumber string) devicetypes.Choice {
	if i.Index == nil {
		return devicetypes.Choice{Reason: devicetypes.UncertainNone}
	}
	return i.Index.Choices(vendor, model, partNumber)
}

// DeviceTypeModel implements api.DeviceTypeLibrary.
func (i Images) DeviceTypeModel(slug string) *devicetypes.Model {
	if i.Index == nil {
		return nil
	}
	return i.Index.BySlug(slug)
}

// runDeviceTypes matches the inventory's devices against the device type
// library at start, after the migrations, then every deduce.interval, so
// that a device discovered after the start gets its device type without a
// restart (ADR 0020, 0025, #55). A failed pass is not fatal: the library
// enriches the inventory, and a failure here (a conflicting schema overlay, a
// database hiccup) must not keep the API down. It is logged and the next tick
// retries. Every replica runs it: the batch is the same full declaration
// whoever submits it.
func (w *Wiring) runDeviceTypes(ctx context.Context, d server.Deps) error {
	ix := w.library(d)
	if ix == nil {
		return nil
	}
	models := len(ix.Models())
	t := time.NewTicker(w.cfg.Deduce.Interval)
	defer t.Stop()
	for {
		syncDeviceTypes(ctx, d, ix, models)
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
