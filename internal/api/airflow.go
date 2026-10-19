package api

import (
	"github.com/VargaFoundation/trica/internal/airflow"
	"github.com/VargaFoundation/trica/internal/store"
)

// AirflowCheck says whether a mounted device takes its air in from the cold
// aisle (ADR 0049).
type AirflowCheck struct {
	// Status is ok, mismatch (hot air recirculated) or unknown.
	Status string `json:"status"`
	// Reason says why: intake_on_cold_aisle, exhaust_into_hot_aisle, passive
	// (ok); intake_on_hot_aisle, exhaust_into_cold_aisle,
	// part_intake_on_hot_aisle (mismatch); aisle_not_set, airflow_unknown,
	// face_unknown, side_airflow, mixed_airflow, not_mounted (unknown).
	Reason string `json:"reason"`
	// Airflow is the airflow the check used, relative to the device's own
	// front (front_to_rear, rear_to_front...), and AirflowFrom where it comes
	// from: device (its own airflow attribute, configured or observed), parts
	// (all of its fans and power supplies that say agree) or model.
	Airflow     string `json:"airflow,omitempty"`
	AirflowFrom string `json:"airflow_from,omitempty"`
	// Intake is the rack face the device takes its air in from (front, rear,
	// side), when known; ColdFace the rack face on the cold aisle, when the
	// rack says which aisle its front faces.
	Intake   string `json:"intake,omitempty"`
	ColdFace string `json:"cold_face,omitempty"`
	// Warnings are contradictions noticed besides the status.
	Warnings []AirflowWarning `json:"warnings,omitempty"`
}

// AirflowWarning is a contradiction between what a device, its parts and its
// model say about its airflow.
type AirflowWarning struct {
	// Code is airflow_contradicts_model (the device or its parts say another
	// airflow than its model: the wrong variant, or a fan tray swapped) or
	// parts_disagree (fans or power supplies turned another way than the
	// device).
	Code string `json:"code"`
	// Airflow is what the device or its parts say, From which of them.
	Airflow string `json:"airflow,omitempty"`
	From    string `json:"from,omitempty"`
	// Model is the airflow of the device's model; ModelDeclared says a person
	// chose that model (ADR 0048).
	Model         string `json:"model,omitempty"`
	ModelDeclared bool   `json:"model_declared,omitempty"`
	// Parts are the parts turned another way.
	Parts []AirflowPart `json:"parts,omitempty"`
}

// AirflowPart is a fan or a power supply and the airflow it reports.
type AirflowPart struct {
	Part    ObjectRef `json:"part"`
	Airflow string    `json:"airflow"`
}

// DeviceAirflow is everything the airflow check of one device read, and its
// result: the device page's card.
type DeviceAirflow struct {
	Device ObjectRef `json:"device"`
	// Rack is the rack the device is mounted in, when it is mounted in one the
	// caller may read; Face the rack face its front is on.
	Rack *ObjectRef `json:"rack,omitempty"`
	Face string     `json:"face,omitempty"`
	// FrontAisle is the rack's front_aisle: cold, hot, or absent when nobody
	// said it.
	FrontAisle string `json:"front_aisle,omitempty"`
	// Model is the device's model and ModelAirflow its airflow; ModelDeclared
	// says a person chose the model (ADR 0048).
	Model         *ObjectRef `json:"model,omitempty"`
	ModelAirflow  string     `json:"model_airflow,omitempty"`
	ModelDeclared bool       `json:"model_declared,omitempty"`
	// DeviceAirflow is the device's own airflow attribute.
	DeviceAirflow string `json:"device_airflow,omitempty"`
	// Parts are its fans and power supplies that report an airflow.
	Parts []AirflowPart `json:"parts"`
	Check AirflowCheck  `json:"check"`
}

func airflowParts(ps []airflow.Part) []AirflowPart {
	out := make([]AirflowPart, 0, len(ps))
	for _, p := range ps {
		out = append(out, AirflowPart{Part: ObjectRef{ID: p.ID, Type: p.Type, Display: p.Name}, Airflow: p.Airflow})
	}
	return out
}

// checkView is the API's view of an airflow result.
func checkView(r airflow.Result) AirflowCheck {
	v := AirflowCheck{Status: r.Status, Reason: r.Reason, Airflow: r.Airflow, AirflowFrom: r.From, Intake: r.Intake, ColdFace: r.ColdFace}
	for _, w := range r.Warnings {
		aw := AirflowWarning{Code: w.Code, Airflow: w.Airflow, From: w.From, Model: w.Model, ModelDeclared: w.ModelDeclared}
		if len(w.Parts) > 0 {
			aw.Parts = airflowParts(w.Parts)
		}
		v.Warnings = append(v.Warnings, aw)
	}
	return v
}

// airflowPartTypes are the parts whose airflow counts: a fan, a power supply.
var airflowPartTypes = map[string]bool{"fan": true, "psu": true}

// partsAirflow reads the airflow of the fans and power supplies below each
// device, among the parts the caller may read, in one query. A part is in
// its device's site.
func (c *call) partsAirflow(devices []string, sites map[string]string) (map[string][]airflow.Part, error) {
	vals, err := c.s.st.DescendantValues(c.ctx(), devices, "airflow", partDepth)
	if err != nil {
		return nil, err
	}
	out := map[string][]airflow.Part{}
	for _, v := range vals {
		if v.Value == "" || v.Status == "absent" || !airflowPartTypes[v.Type] || !c.s.auth.Allow(c.p, ActionRead, v.Type, sites[v.Root]) {
			continue
		}
		out[v.Root] = append(out[v.Root], airflow.Part{ID: v.ID, Name: v.Display, Type: v.Type, Airflow: v.Value})
	}
	return out, nil
}

// modelAirflow is the airflow of a device's model: the inventory's, else the
// library's (a model a person just chose may not be described yet).
func (s *Server) modelAirflow(t *store.Object) string {
	if t == nil {
		return ""
	}
	attrs := redactMap(t.Attrs)
	if a, _ := attrs["airflow"].(string); a != "" {
		return a
	}
	slug, _ := attrs["slug"].(string)
	if lm := s.libraryModel(slug); lm != nil {
		return lm.Airflow
	}
	return ""
}

func strAttr(o *store.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := redactMap(o.Attrs)[key].(string)
	return v
}

// hObjectAirflow answers the airflow check of one device: where it is mounted,
// which aisle that rack face is on, what its model, the device itself and its
// fans and power supplies say about its airflow, and the result.
func (s *Server) hObjectAirflow(c *call) error {
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
	out := DeviceAirflow{Device: ObjectRef{ID: dev.ID, Type: dev.Type, Display: dev.Display}, Parts: []AirflowPart{}}
	in := airflow.Input{Device: strAttr(dev, "airflow")}
	out.DeviceAirflow = in.Device

	rels, err := s.st.RelationsOf(c.ctx(), id, nil)
	if err != nil {
		return err
	}
	var rackID string
	var face string
	for _, r := range redactRelations(rels) {
		if r.Type == "mounted_in" && r.FromID == id {
			rackID = r.ToID
			face, _ = r.Attrs["face"].(string)
			break
		}
	}
	models, err := s.st.CurrentRelationsFrom(c.ctx(), "instance_of", []string{id})
	if err != nil {
		return err
	}
	ids := []string{}
	modelRel, hasModel := modelRelations(models)[id]
	if hasModel {
		ids = append(ids, modelRel.ToID)
	}
	if rackID != "" {
		ids = append(ids, rackID)
	}
	objs, _, err := c.visibleObjects(ids)
	if err != nil {
		return err
	}
	if rack, ok := objs[rackID]; ok {
		out.Rack = &ObjectRef{ID: rack.ID, Type: rack.Type, Display: rack.Display}
		if face == "front" || face == "rear" {
			out.Face = face
		}
		in.Mounted, in.Face = true, out.Face
		in.FrontAisle = strAttr(rack, "front_aisle")
		out.FrontAisle = in.FrontAisle
	}
	if m, ok := objs[modelRel.ToID]; hasModel && ok {
		out.Model = &ObjectRef{ID: m.ID, Type: m.Type, Display: m.Display}
		in.Model = s.modelAirflow(m)
		out.ModelAirflow = in.Model
		if s.lib != nil {
			claims, err := s.st.RelationClaims(c.ctx(), "instance_of", []string{id})
			if err != nil {
				return err
			}
			in.ModelDeclared = declaredDevices(claims)[id]
			out.ModelDeclared = in.ModelDeclared
		}
	}
	parts, err := c.partsAirflow([]string{id}, map[string]string{id: site})
	if err != nil {
		return err
	}
	in.Parts = parts[id]
	out.Parts = airflowParts(in.Parts)
	out.Check = checkView(airflow.Check(in))
	return c.json(200, out)
}
