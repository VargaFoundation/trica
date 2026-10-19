package api

import (
	"time"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/store"
)

// DeviceTypeLibrary is the device-type library as the API asks it which
// models a device may be (internal/devicetypes, ADR 0048).
type DeviceTypeLibrary interface {
	// DeviceTypeChoices is devicetypes.Index.Choices.
	DeviceTypeChoices(vendor, model, partNumber string) devicetypes.Choice
	// DeviceTypeModel is the model with this slug, or nil.
	DeviceTypeModel(slug string) *devicetypes.Model
}

// DeviceTypeOption names one model of the library.
type DeviceTypeOption struct {
	Slug  string `json:"slug"`
	Model string `json:"model"`
	// How is how the library matched it (sku, model, mounting, part_number,
	// variant).
	How string `json:"how,omitempty"`
}

// DeviceTypeCurrent is the model the inventory gives the device now: the
// instance_of relation that decides it.
type DeviceTypeCurrent struct {
	Model     ObjectRef `json:"model"`
	Slug      string    `json:"slug,omitempty"`
	ModelName string    `json:"model_name,omitempty"`
	// Source is the source the relation resolved to.
	Source string `json:"source"`
}

// DeviceTypeDeclaration is a source other than the library saying which
// model the device is: a person who chose the chassis, a declaration file.
type DeviceTypeDeclaration struct {
	Model    ObjectRef `json:"model"`
	Slug     string    `json:"slug,omitempty"`
	Source   string    `json:"source"`
	LastSeen time.Time `json:"last_seen"`
}

// DeviceTypeCandidate is one model the device may be.
type DeviceTypeCandidate struct {
	Slug      string  `json:"slug"`
	Model     string  `json:"model"`
	Vendor    string  `json:"vendor"`
	UHeight   float64 `json:"u_height"`
	FullDepth bool    `json:"full_depth"`
	Airflow   string  `json:"airflow,omitempty"`
	// FrontImage and RearImage are the API paths of the faces' images, when
	// the library has them.
	FrontImage string `json:"front_image,omitempty"`
	RearImage  string `json:"rear_image,omitempty"`
	// How is how it was found: name (its name starts with the device's) or
	// variant_of (its model file links it to the family).
	How string `json:"how"`
	// Current: the inventory gives the device this model now.
	Current bool `json:"current"`
	// LibraryMatch: the library matches the device to this model.
	LibraryMatch bool `json:"library_match"`
}

// DeviceTypeChoice is what the object page needs to say how sure the
// inventory is of a device's model, and to let a person choose it.
type DeviceTypeChoice struct {
	Device     ObjectRef `json:"device"`
	Vendor     string    `json:"vendor,omitempty"`
	Model      string    `json:"model,omitempty"`
	PartNumber string    `json:"part_number,omitempty"`
	// Current is the model the inventory gives the device, when it has one
	// the caller may read.
	Current *DeviceTypeCurrent `json:"current,omitempty"`
	// LibraryMatch is the model the library matches, whatever was declared.
	LibraryMatch *DeviceTypeOption `json:"library_match,omitempty"`
	// Certain says whether the model is the exact chassis; Reason says what
	// decided it (declared, sku, model, mounting, part_number) or why it is
	// not certain (base, airflow, ambiguous, none).
	Certain bool   `json:"certain"`
	Reason  string `json:"reason"`
	// DeclaredBy is the source of the declaration that decides, when Reason
	// is declared.
	DeclaredBy string `json:"declared_by,omitempty"`
	// SKUConflict is the model the device's part number names when it is not
	// the declared one: the declaration stands, and the page says so.
	SKUConflict *DeviceTypeOption `json:"sku_conflict,omitempty"`
	// Declarations are every declaration of the device's model, newest first.
	Declarations []DeviceTypeDeclaration `json:"declarations"`
	// Candidates are the models the device may be, sorted by slug.
	Candidates []DeviceTypeCandidate `json:"candidates"`
	// CanChoose says whether the caller may declare the model: propose on
	// devices in its site and on device models.
	CanChoose bool `json:"can_choose"`
}

// modelRelations picks, per device, the instance_of relation that decides
// its model among the current ones (ordered by id): a relation that a source
// other than the library resolved to (a person's choice, a declaration file)
// wins over the library's match by name, the most recent first; else the
// first (ADR 0048). A device has two relations between a choice and the next
// library pass, which then withdraws its own.
func modelRelations(rels []store.Relation) map[string]store.Relation {
	out := map[string]store.Relation{}
	for _, r := range rels {
		cur, seen := out[r.FromID]
		switch {
		case !seen:
		case cur.Source == devicetypes.Source && r.Source != devicetypes.Source:
		case cur.Source != devicetypes.Source && r.Source != devicetypes.Source && r.ValidFrom.After(cur.ValidFrom):
		default:
			continue
		}
		out[r.FromID] = r
	}
	return out
}

// declaredDevices is the set of devices whose model a source other than the
// library declares.
func declaredDevices(claims []store.RelationClaim) map[string]bool {
	out := map[string]bool{}
	for _, c := range claims {
		if c.Source != devicetypes.Source {
			out[c.FromID] = true
		}
	}
	return out
}

// libraryModel is the library's model with this slug, or nil.
func (s *Server) libraryModel(slug string) *devicetypes.Model {
	if s.lib == nil || slug == "" {
		return nil
	}
	return s.lib.DeviceTypeModel(slug)
}

// uncertainVariant reports whether the library is unsure which model a device
// whose model nobody declared is, and how many candidates it has.
func (s *Server) uncertainVariant(attrs map[string]any) (bool, int) {
	if s.lib == nil {
		return false, 0
	}
	str := func(k string) string { v, _ := attrs[k].(string); return v }
	ch := s.lib.DeviceTypeChoices(str("vendor"), str("model"), str("part_number"))
	if ch.Certain || len(ch.Candidates) == 0 {
		return false, 0
	}
	return true, len(ch.Candidates)
}

// hDeviceTypeCandidates answers how sure the inventory is of a device's model
// and which models it may be: the page offers the choice when it is not sure
// (ADR 0048). Reading it changes nothing; the choice is a batch.
func (s *Server) hDeviceTypeCandidates(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	dev, site, err := c.loadObject(id)
	if err != nil {
		return err
	}
	if dev.Type != "device" {
		return notFound("%s is a %s, not a device", id, dev.Type)
	}
	attrs := redactMap(dev.Attrs)
	str := func(k string) string { v, _ := attrs[k].(string); return v }
	out := DeviceTypeChoice{
		Device: ObjectRef{ID: dev.ID, Type: dev.Type, Display: dev.Display},
		Vendor: str("vendor"), Model: str("model"), PartNumber: str("part_number"),
		Declarations: []DeviceTypeDeclaration{}, Candidates: []DeviceTypeCandidate{},
		CanChoose: s.auth.Allow(c.p, ActionPropose, dev.Type, site) && s.auth.Allow(c.p, ActionPropose, "device_type", ""),
	}
	rels, err := s.st.CurrentRelationsFrom(c.ctx(), "instance_of", []string{dev.ID})
	if err != nil {
		return err
	}
	claims, err := s.st.RelationClaims(c.ctx(), "instance_of", []string{dev.ID})
	if err != nil {
		return err
	}
	var ids []string
	for _, r := range rels {
		ids = append(ids, r.ToID)
	}
	for _, cl := range claims {
		ids = append(ids, cl.ToID)
	}
	models, _, err := c.visibleObjects(ids)
	if err != nil {
		return err
	}
	slugOf := func(o *store.Object) string { v, _ := redactMap(o.Attrs)["slug"].(string); return v }
	if r, ok := modelRelations(rels)[dev.ID]; ok {
		if m, ok := models[r.ToID]; ok {
			name, _ := redactMap(m.Attrs)["model"].(string)
			out.Current = &DeviceTypeCurrent{Model: ObjectRef{ID: m.ID, Type: m.Type, Display: m.Display}, Slug: slugOf(m), ModelName: name, Source: r.Source}
		}
	}
	for _, cl := range claims {
		m, ok := models[cl.ToID]
		if cl.Source == devicetypes.Source || !ok || m.Type != "device_type" {
			continue
		}
		out.Declarations = append(out.Declarations, DeviceTypeDeclaration{
			Model: ObjectRef{ID: m.ID, Type: m.Type, Display: m.Display}, Slug: slugOf(m), Source: cl.Source, LastSeen: cl.LastSeen})
	}

	ch := devicetypes.Choice{Reason: devicetypes.UncertainNone}
	if s.lib != nil {
		ch = s.lib.DeviceTypeChoices(out.Vendor, out.Model, out.PartNumber)
	}
	if ch.Match != nil {
		out.LibraryMatch = &DeviceTypeOption{Slug: ch.Match.Model.Slug, Model: ch.Match.Model.Model, How: ch.Match.How}
	}
	if len(out.Declarations) > 0 {
		d := out.Declarations[0]
		out.Certain, out.Reason, out.DeclaredBy = true, devicetypes.ByDeclared, d.Source
		if ch.Match != nil && ch.Match.How == devicetypes.BySKU && ch.Match.Model.Slug != d.Slug {
			out.SKUConflict = out.LibraryMatch
		}
	} else {
		out.Certain, out.Reason = ch.Certain, ch.Reason
	}
	for _, cand := range ch.Candidates {
		m := cand.Model
		out.Candidates = append(out.Candidates, DeviceTypeCandidate{
			Slug: m.Slug, Model: m.Model, Vendor: m.Vendor, UHeight: m.UHeight, FullDepth: m.FullDepth, Airflow: m.Airflow,
			FrontImage: s.faceImage(m.Slug, "front"), RearImage: s.faceImage(m.Slug, "rear"), How: cand.How,
			Current:      out.Current != nil && out.Current.Slug == m.Slug,
			LibraryMatch: ch.Match != nil && ch.Match.Model.Slug == m.Slug,
		})
	}
	return c.json(200, out)
}
