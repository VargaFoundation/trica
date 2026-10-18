package devicetypes

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Source is the batch source of every claim the library makes.
const Source = "library:devicetypes"

// Device is what matching needs to know about an inventory device.
type Device struct {
	ID, Vendor, Model, PartNumber string
}

// Link is one device matched to one model.
type Link struct {
	Device Device
	Match  Match
}

// Batch builds the declared batch for devices: one device_type object per
// model that matched at least one device (identity: slug) and one instance_of
// relation per matched device. The batch is full-scope: when a device stops
// matching (its model changed) the engine withdraws the relation, and a model
// nobody uses any more loses the library's claims. History keeps both.
//
// The confidence is Declared, the lowest: the library never overrides what a
// collector observed (ADR 0020).
func (ix *Index) Batch(devices []Device, observedAt time.Time) (*sdk.Batch, []Link) {
	b := sdk.NewBatch(Source, sdk.ScopeFull)
	b.Confidence = sdk.Declared
	b.ObservedAt = observedAt
	var links []Link
	sorted := append([]Device(nil), devices...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, d := range sorted {
		m := ix.Find(d.Vendor, d.Model, d.PartNumber)
		if m == nil {
			continue
		}
		links = append(links, Link{d, *m})
		ref := "dt:" + m.Model.Slug
		if _, ok := b.Lookup(ref); !ok {
			o := b.Object(ref, "device_type").Identify("slug", m.Model.Slug)
			o.Set("vendor", m.Model.Vendor).Set("model", m.Model.Model).Set("part_number", m.Model.PartNumber)
			o.Set("u_height", m.Model.UHeight).Set("full_depth", m.Model.FullDepth).Set("airflow", m.Model.Airflow)
			if m.Model.WeightKg > 0 {
				o.Set("weight_kg", m.Model.WeightKg)
			}
			if n := m.Model.PSUCount(); n > 0 {
				o.Set("psu_count", n)
			}
			if len(m.Model.Ports) > 0 {
				o.Set("ports", m.Model.Ports)
			}
			// Where the face illustrations are, when there are any; the
			// bytes are served by GET /api/v1/device-types/{slug}/{face}.
			if img := m.Model.FrontImage; img != nil {
				o.Set("front_image", img.Path)
			}
			if img := m.Model.RearImage; img != nil {
				o.Set("rear_image", img.Path)
			}
		}
		b.RelateEndpoints("instance_of", sdk.ByID("device", d.ID), sdk.Ref(ref))
	}
	return b, links
}

// Sync matches every device of the inventory against ix and submits the
// result to the engine. It reads the inventory and writes nothing itself.
func Sync(ctx context.Context, e *reconcile.Engine, ix *Index, author string) (*reconcile.Result, []Link, error) {
	objs, err := e.Store.AllObjects(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("devicetypes: read devices: %w", err)
	}
	var devices []Device
	for _, o := range objs {
		if o.Type != "device" {
			continue
		}
		str := func(k string) string { s, _ := o.Attrs[k].(string); return s }
		devices = append(devices, Device{ID: o.ID, Vendor: str("vendor"), Model: str("model"), PartNumber: str("part_number")})
	}
	at := time.Now()
	if e.Now != nil {
		at = e.Now()
	}
	b, links := ix.Batch(devices, at)
	res, err := e.Submit(ctx, b, author, "device-type library")
	return res, links, err
}
