// Package airflow checks that the devices of a rack take their air in from the
// cold aisle (ADR 0049).
//
// Three facts meet here. The rack says which aisle its front faces
// (rack.front_aisle: cold or hot; the rear faces the other). The mounting says
// which rack face the device's own front is on (mounted_in.face). The device
// says which way air goes through it, relative to its own front: its model's
// airflow (device_type.airflow), or something more specific the inventory
// holds about this device (device.airflow, configured or observed, or the
// airflow of its fans and power supplies). A front-to-rear device mounted on
// the rear face takes its air in at the rack's rear.
//
// The package is pure: it reads values, never the inventory, so the API, the
// MCP tools and the browser tests' mock all compute the same answer.
package airflow

import "sort"

// Status of a device's check.
const (
	// OK: the device takes its air in from the cold aisle (or blows it into
	// the hot one, or has no fan).
	OK = "ok"
	// Mismatch: the device, or one of its fans or power supplies, takes its
	// air in from the hot aisle (or blows into the cold one): hot air is
	// recirculated.
	Mismatch = "mismatch"
	// Unknown: a fact the check needs is missing or says nothing usable.
	Unknown = "unknown"
)

// Reasons, one per result, said in words by the page.
const (
	ReasonIntakeCold  = "intake_on_cold_aisle"     // ok
	ReasonExhaustHot  = "exhaust_into_hot_aisle"   // ok, side intake blowing into the hot aisle
	ReasonPassive     = "passive"                  // ok, no fan
	ReasonIntakeHot   = "intake_on_hot_aisle"      // mismatch
	ReasonExhaustCold = "exhaust_into_cold_aisle"  // mismatch, side intake blowing into the cold aisle
	ReasonPartHot     = "part_intake_on_hot_aisle" // mismatch, a fan or a power supply turned the other way
	ReasonAisleNotSet = "aisle_not_set"            // unknown, the rack's front_aisle is not said
	ReasonNoAirflow   = "airflow_unknown"          // unknown, nothing says the device's airflow
	ReasonNoFace      = "face_unknown"             // unknown, the mounting has no face
	ReasonSideways    = "side_airflow"             // unknown, left-to-right or right-to-left
	ReasonMixed       = "mixed_airflow"            // unknown, the model says mixed or the parts disagree
	ReasonNotMounted  = "not_mounted"              // unknown, the device is in no rack

	// WarnContradiction: the device or its parts say another airflow than its model.
	WarnContradiction = "airflow_contradicts_model"
	// WarnPartsDisagree: fans or power supplies turned another way than the device.
	WarnPartsDisagree = "parts_disagree"
)

// Where the airflow the check used comes from.
const (
	FromDevice = "device" // device.airflow (configured or observed)
	FromParts  = "parts"  // all of its directional fans and power supplies agree
	FromModel  = "model"  // device_type.airflow
)

const (
	frontToRear = "front_to_rear"
	rearToFront = "rear_to_front"
	front       = "front"
	rear        = "rear"
	side        = "side"
)

// Part is a fan or a power supply of the device and the airflow it reports.
type Part struct {
	ID      string
	Name    string
	Type    string
	Airflow string
}

// Input is what the check reads about one mounted device.
type Input struct {
	// FrontAisle is the rack's front_aisle: cold, hot or empty (not said).
	FrontAisle string
	// Face is the rack face the device's front is on: front, rear or empty.
	Face string
	// Mounted is false for a device in no rack.
	Mounted bool
	// Model is the airflow of the device's model, empty when unknown.
	Model string
	// ModelDeclared says a person chose the model (ADR 0048): a contradiction
	// then points at the choice, not at the library.
	ModelDeclared bool
	// Device is the device's own airflow attribute, empty when not said.
	Device string
	// Parts are the device's fans and power supplies; only front_to_rear and
	// rear_to_front count.
	Parts []Part
}

// Warning is something the check noticed besides its status.
type Warning struct {
	Code string
	// Airflow is what the device or its parts say; Model what its model says.
	Airflow string
	From    string
	Model   string
	// ModelDeclared: the model a person chose is contradicted.
	ModelDeclared bool
	// Parts are the parts turned another way (parts_disagree).
	Parts []Part
}

// Result is the check of one device.
type Result struct {
	Status string
	Reason string
	// Airflow is the airflow the check used and From where it came from.
	Airflow string
	From    string
	// Intake is the rack face the device takes its air in from (front, rear,
	// side), empty when unknown.
	Intake string
	// ColdFace is the rack face on the cold aisle, empty when not said.
	ColdFace string
	Warnings []Warning
}

// directional reports whether a part's or a device's airflow says which of
// the two faces takes the air in.
func directional(a string) bool { return a == frontToRear || a == rearToFront }

// other is the opposite rack face.
func other(face string) string {
	if face == front {
		return rear
	}
	return front
}

// ColdFace is the rack face on the cold aisle for a rack's front_aisle:
// front when the front faces the cold aisle, rear when it faces the hot one,
// empty when not said.
func ColdFace(frontAisle string) string {
	switch frontAisle {
	case "cold":
		return front
	case "hot":
		return rear
	}
	return ""
}

// IntakeFace is the rack face a device takes its air in from, given its
// airflow (relative to its own front) and the rack face its front is on:
// front, rear, side, or empty when the airflow says nothing about it.
func IntakeFace(airflow, face string) string {
	if face != front && face != rear {
		return ""
	}
	switch airflow {
	case frontToRear:
		return face
	case rearToFront:
		return other(face)
	case "side_to_rear", "left_to_right", "right_to_left":
		return side
	}
	return ""
}

// partsAirflow is the airflow every directional part agrees on, or empty
// when they disagree or none says.
func partsAirflow(parts []Part) string {
	agreed := ""
	for _, p := range parts {
		if !directional(p.Airflow) {
			continue
		}
		if agreed == "" {
			agreed = p.Airflow
		} else if agreed != p.Airflow {
			return ""
		}
	}
	return agreed
}

// effective is the airflow the check uses and where it comes from: the
// device's own attribute, else what all of its parts agree on, else its
// model's.
func effective(in Input) (string, string) {
	if in.Device != "" {
		return in.Device, FromDevice
	}
	if a := partsAirflow(in.Parts); a != "" {
		return a, FromParts
	}
	if in.Model != "" {
		return in.Model, FromModel
	}
	return "", ""
}

// Check computes the airflow check of one device.
func Check(in Input) Result {
	r := Result{ColdFace: ColdFace(in.FrontAisle)}
	r.Airflow, r.From = effective(in)
	r.Warnings = warnings(in, r.Airflow)
	set := func(status, reason string) Result {
		r.Status, r.Reason = status, reason
		return r
	}
	if !in.Mounted {
		return set(Unknown, ReasonNotMounted)
	}
	r.Intake = IntakeFace(r.Airflow, in.Face)
	switch {
	case r.Airflow == "":
		if partsDisagree(in.Parts) {
			return set(Unknown, ReasonMixed)
		}
		return set(Unknown, ReasonNoAirflow)
	case r.Airflow == "passive":
		return set(OK, ReasonPassive)
	case r.Airflow == "mixed":
		return set(Unknown, ReasonMixed)
	case in.Face != front && in.Face != rear:
		return set(Unknown, ReasonNoFace)
	case r.Airflow == "left_to_right" || r.Airflow == "right_to_left":
		return set(Unknown, ReasonSideways)
	case r.ColdFace == "":
		return set(Unknown, ReasonAisleNotSet)
	case r.Airflow == "side_to_rear":
		if other(in.Face) == r.ColdFace {
			return set(Mismatch, ReasonExhaustCold)
		}
		return set(OK, ReasonExhaustHot)
	case r.Intake == "":
		return set(Unknown, ReasonNoAirflow)
	case r.Intake != r.ColdFace:
		return set(Mismatch, ReasonIntakeHot)
	}
	// The device takes its air in from the cold aisle; a fan or a power
	// supply turned the other way still blows hot air back into it.
	for _, p := range in.Parts {
		if directional(p.Airflow) && IntakeFace(p.Airflow, in.Face) != r.ColdFace {
			return set(Mismatch, ReasonPartHot)
		}
	}
	return set(OK, ReasonIntakeCold)
}

// partsDisagree reports whether two directional parts disagree.
func partsDisagree(parts []Part) bool {
	seen := ""
	for _, p := range parts {
		if !directional(p.Airflow) {
			continue
		}
		if seen != "" && seen != p.Airflow {
			return true
		}
		seen = p.Airflow
	}
	return false
}

// warnings are the contradictions between what the device says, what its
// parts say and what its model says.
func warnings(in Input, used string) []Warning {
	var out []Warning
	// What the device itself says (its attribute, else its parts together)
	// against its model.
	said, from := in.Device, FromDevice
	if said == "" {
		said, from = partsAirflow(in.Parts), FromParts
	}
	if said != "" && in.Model != "" && said != in.Model {
		out = append(out, Warning{Code: WarnContradiction, Airflow: said, From: from, Model: in.Model, ModelDeclared: in.ModelDeclared})
	}
	// Parts turned another way than the device: against the airflow used
	// when it is directional, else against each other.
	var odd []Part
	if directional(used) {
		for _, p := range in.Parts {
			if directional(p.Airflow) && p.Airflow != used {
				odd = append(odd, p)
			}
		}
	} else if partsDisagree(in.Parts) {
		for _, p := range in.Parts {
			if directional(p.Airflow) {
				odd = append(odd, p)
			}
		}
	}
	if len(odd) > 0 {
		sort.SliceStable(odd, func(i, j int) bool { return odd[i].Name < odd[j].Name })
		out = append(out, Warning{Code: WarnPartsDisagree, Airflow: used, Parts: odd})
	}
	return out
}
