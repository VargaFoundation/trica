package devicetypes

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Source is the batch source of every claim the library makes.
const Source = "library:devicetypes"

// Device is what matching needs to know about an inventory device.
type Device struct {
	ID, Vendor, Model, PartNumber string
	// Declared is the slug of the model another source declared the device
	// to be (a person who chose the chassis, a declaration file), or "".
	// The library then claims no instance_of for the device: a declaration
	// wins over a match by name (ADR 0048).
	Declared string
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
//
// A device another source declared the model of (Device.Declared) gets no
// instance_of from the library, so the full-scope batch withdraws the one it
// made before; the declared model's device_type object is still described,
// so that a model chosen by a person has its height and images (ADR 0048).
func (ix *Index) Batch(devices []Device, observedAt time.Time) (*sdk.Batch, []Link) {
	b := sdk.NewBatch(Source, sdk.ScopeFull)
	b.Confidence = sdk.Declared
	b.ObservedAt = observedAt
	var links []Link
	sorted := append([]Device(nil), devices...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, d := range sorted {
		if d.Declared != "" {
			if m := ix.BySlug(d.Declared); m != nil {
				links = append(links, Link{d, Match{m, ByDeclared}})
				describe(b, m)
			}
			continue
		}
		m := ix.Find(d.Vendor, d.Model, d.PartNumber)
		if m == nil {
			continue
		}
		links = append(links, Link{d, *m})
		ref := describe(b, m.Model)
		b.RelateEndpoints("instance_of", sdk.ByID("device", d.ID), sdk.Ref(ref))
	}
	return b, links
}

// describe adds the device_type object of m to b once, and returns its ref.
func describe(b *sdk.Batch, m *Model) string {
	ref := "dt:" + m.Slug
	if _, ok := b.Lookup(ref); ok {
		return ref
	}
	o := b.Object(ref, "device_type").Identify("slug", m.Slug)
	o.Set("vendor", m.Vendor).Set("model", m.Model).Set("part_number", m.PartNumber)
	o.Set("u_height", m.UHeight).Set("full_depth", m.FullDepth).Set("airflow", m.Airflow)
	if m.WeightKg > 0 {
		o.Set("weight_kg", m.WeightKg)
	}
	if n := m.PSUCount(); n > 0 {
		o.Set("psu_count", n)
	}
	if len(m.Ports) > 0 {
		o.Set("ports", m.Ports)
	}
	// Where the face illustrations are, when there are any; the bytes are
	// served by GET /api/v1/device-types/{slug}/{face}.
	if img := m.FrontImage; img != nil {
		o.Set("front_image", img.Path)
	}
	if img := m.RearImage; img != nil {
		o.Set("rear_image", img.Path)
	}
	return ref
}

// Sync matches every device of the inventory against ix and submits the
// result to the engine. It reads the inventory and writes nothing itself.
func Sync(ctx context.Context, e *reconcile.Engine, ix *Index, author string) (*reconcile.Result, []Link, error) {
	objs, err := e.Store.AllObjects(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("devicetypes: read devices: %w", err)
	}
	declared, err := Declared(ctx, e.Store, objs)
	if err != nil {
		return nil, nil, err
	}
	var devices []Device
	for _, o := range objs {
		if o.Type != "device" {
			continue
		}
		str := func(k string) string { s, _ := o.Attrs[k].(string); return s }
		devices = append(devices, Device{ID: o.ID, Vendor: str("vendor"), Model: str("model"), PartNumber: str("part_number"), Declared: declared[o.ID]})
	}
	at := time.Now()
	if e.Now != nil {
		at = e.Now()
	}
	b, links := ix.Batch(devices, at)
	res, err := e.Submit(ctx, b, author, "device-type library")
	return res, links, err
}

// Declared maps each device whose model a source other than the library
// declared to the slug of that model: the most recently confirmed
// declaration when there are several. objs must hold the device_type objects
// the claims point to (the whole inventory, as Sync reads it). A declaration
// pointing to an object without a slug counts, with the slug "?": the
// library still steps aside.
func Declared(ctx context.Context, st *store.Store, objs []*store.Object) (map[string]string, error) {
	claims, err := st.RelationClaims(ctx, "instance_of", nil)
	if err != nil {
		return nil, fmt.Errorf("devicetypes: read declared models: %w", err)
	}
	slugs := map[string]string{}
	for _, o := range objs {
		if o.Type == "device_type" {
			s, _ := o.Attrs["slug"].(string)
			slugs[o.ID] = s
		}
	}
	out := map[string]string{}
	for _, c := range claims {
		if c.Source == Source {
			continue
		}
		if _, done := out[c.FromID]; done {
			continue // the claims come newest first per device
		}
		slug, isType := slugs[c.ToID]
		if !isType {
			continue
		}
		if slug == "" {
			slug = "?"
		}
		out[c.FromID] = slug
	}
	return out, nil
}
