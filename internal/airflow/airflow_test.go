package airflow

import (
	"reflect"
	"testing"
)

func TestColdFaceAndIntake(t *testing.T) {
	for aisle, want := range map[string]string{"cold": "front", "hot": "rear", "": "", "lukewarm": ""} {
		if got := ColdFace(aisle); got != want {
			t.Errorf("ColdFace(%q) = %q, want %q", aisle, got, want)
		}
	}
	cases := []struct{ airflow, face, want string }{
		{"front_to_rear", "front", "front"},
		{"front_to_rear", "rear", "rear"}, // mounted from the rear: its front is at the rack's rear
		{"rear_to_front", "front", "rear"},
		{"rear_to_front", "rear", "front"},
		{"side_to_rear", "front", "side"},
		{"left_to_right", "rear", "side"},
		{"passive", "front", ""},
		{"front_to_rear", "", ""},
		{"", "front", ""},
	}
	for _, c := range cases {
		if got := IntakeFace(c.airflow, c.face); got != c.want {
			t.Errorf("IntakeFace(%q, %q) = %q, want %q", c.airflow, c.face, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	psu := func(name, a string) Part { return Part{ID: name, Name: name, Type: "psu", Airflow: a} }
	fan := func(name, a string) Part { return Part{ID: name, Name: name, Type: "fan", Airflow: a} }
	cases := []struct {
		name               string
		in                 Input
		status, reason     string
		airflow, from, in2 string // in2: the intake face
	}{
		{"server, front-to-rear, front face, front on the cold aisle",
			Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "front_to_rear"},
			OK, ReasonIntakeCold, "front_to_rear", FromModel, "front"},
		{"switch -F mounted on the rear: port-side intake on the hot aisle",
			Input{FrontAisle: "cold", Face: "rear", Mounted: true, Model: "front_to_rear"},
			Mismatch, ReasonIntakeHot, "front_to_rear", FromModel, "rear"},
		{"switch -R mounted on the rear: takes its air in at the rack's front",
			Input{FrontAisle: "cold", Face: "rear", Mounted: true, Model: "rear_to_front"},
			OK, ReasonIntakeCold, "rear_to_front", FromModel, "front"},
		{"a rack turned the other way: front on the hot aisle",
			Input{FrontAisle: "hot", Face: "front", Mounted: true, Model: "front_to_rear"},
			Mismatch, ReasonIntakeHot, "front_to_rear", FromModel, "front"},
		{"a rack turned the other way, a rear-to-front device on its front",
			Input{FrontAisle: "hot", Face: "front", Mounted: true, Model: "rear_to_front"},
			OK, ReasonIntakeCold, "rear_to_front", FromModel, "rear"},
		{"aisle not said: unknown, the intake is still told",
			Input{Face: "front", Mounted: true, Model: "front_to_rear"},
			Unknown, ReasonAisleNotSet, "front_to_rear", FromModel, "front"},
		{"no airflow anywhere",
			Input{FrontAisle: "cold", Face: "front", Mounted: true},
			Unknown, ReasonNoAirflow, "", "", ""},
		{"no face on the mounting",
			Input{FrontAisle: "cold", Mounted: true, Model: "front_to_rear"},
			Unknown, ReasonNoFace, "front_to_rear", FromModel, ""},
		{"not mounted",
			Input{Model: "front_to_rear"},
			Unknown, ReasonNotMounted, "front_to_rear", FromModel, ""},
		{"passive (a patch panel): nothing to check",
			Input{FrontAisle: "cold", Face: "rear", Mounted: true, Model: "passive"},
			OK, ReasonPassive, "passive", FromModel, ""},
		{"mixed",
			Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "mixed"},
			Unknown, ReasonMixed, "mixed", FromModel, ""},
		{"left-to-right",
			Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "left_to_right"},
			Unknown, ReasonSideways, "left_to_right", FromModel, "side"},
		{"side-to-rear blowing into the hot aisle",
			Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "side_to_rear"},
			OK, ReasonExhaustHot, "side_to_rear", FromModel, "side"},
		{"side-to-rear mounted on the rear blows into the cold aisle",
			Input{FrontAisle: "cold", Face: "rear", Mounted: true, Model: "side_to_rear"},
			Mismatch, ReasonExhaustCold, "side_to_rear", FromModel, "side"},
		{"the device's own airflow wins over its model",
			Input{FrontAisle: "cold", Face: "rear", Mounted: true, Model: "front_to_rear", Device: "rear_to_front"},
			OK, ReasonIntakeCold, "rear_to_front", FromDevice, "front"},
		{"parts that agree win over the model",
			Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "front_to_rear", Parts: []Part{psu("PSU1", "rear_to_front"), fan("Fan1", "rear_to_front"), fan("Fan2", "unknown")}},
			Mismatch, ReasonIntakeHot, "rear_to_front", FromParts, "rear"},
		{"one power supply turned the other way recirculates",
			Input{FrontAisle: "cold", Face: "front", Mounted: true, Device: "front_to_rear", Parts: []Part{psu("PSU1", "front_to_rear"), psu("PSU2", "rear_to_front")}},
			Mismatch, ReasonPartHot, "front_to_rear", FromDevice, "front"},
		{"parts disagree and nothing else says: mixed",
			Input{FrontAisle: "cold", Face: "front", Mounted: true, Parts: []Part{psu("PSU1", "front_to_rear"), psu("PSU2", "rear_to_front")}},
			Unknown, ReasonMixed, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Check(c.in)
			if r.Status != c.status || r.Reason != c.reason || r.Airflow != c.airflow || r.From != c.from || r.Intake != c.in2 {
				t.Errorf("Check = %s/%s airflow %q from %q intake %q, want %s/%s %q %q %q",
					r.Status, r.Reason, r.Airflow, r.From, r.Intake, c.status, c.reason, c.airflow, c.from, c.in2)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	t.Run("observed contradicts the chosen model", func(t *testing.T) {
		r := Check(Input{FrontAisle: "cold", Face: "rear", Mounted: true, Model: "front_to_rear", ModelDeclared: true, Device: "rear_to_front"})
		want := []Warning{{Code: WarnContradiction, Airflow: "rear_to_front", From: FromDevice, Model: "front_to_rear", ModelDeclared: true}}
		if !reflect.DeepEqual(r.Warnings, want) {
			t.Fatalf("warnings = %+v, want %+v", r.Warnings, want)
		}
	})
	t.Run("parts contradict the model", func(t *testing.T) {
		r := Check(Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "front_to_rear", Parts: []Part{{Name: "FanTray1", Type: "fan", Airflow: "rear_to_front"}}})
		if len(r.Warnings) != 1 || r.Warnings[0].Code != WarnContradiction || r.Warnings[0].From != FromParts {
			t.Fatalf("warnings = %+v", r.Warnings)
		}
	})
	t.Run("a part turned another way than the device", func(t *testing.T) {
		r := Check(Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "front_to_rear", Parts: []Part{
			{Name: "PSU2", Type: "psu", Airflow: "rear_to_front"}, {Name: "PSU1", Type: "psu", Airflow: "front_to_rear"}, {Name: "Fan", Type: "fan", Airflow: "unknown"}}})
		if len(r.Warnings) != 1 || r.Warnings[0].Code != WarnPartsDisagree || len(r.Warnings[0].Parts) != 1 || r.Warnings[0].Parts[0].Name != "PSU2" {
			t.Fatalf("warnings = %+v", r.Warnings)
		}
	})
	t.Run("parts disagree among themselves: all listed, sorted", func(t *testing.T) {
		r := Check(Input{Mounted: true, Face: "front", Parts: []Part{{Name: "PSU2", Airflow: "rear_to_front"}, {Name: "PSU1", Airflow: "front_to_rear"}}})
		if len(r.Warnings) != 1 || len(r.Warnings[0].Parts) != 2 || r.Warnings[0].Parts[0].Name != "PSU1" {
			t.Fatalf("warnings = %+v", r.Warnings)
		}
	})
	t.Run("agreement: no warning", func(t *testing.T) {
		r := Check(Input{FrontAisle: "cold", Face: "front", Mounted: true, Model: "front_to_rear", Device: "front_to_rear", Parts: []Part{{Name: "PSU1", Airflow: "front_to_rear"}}})
		if len(r.Warnings) != 0 || r.Status != OK {
			t.Fatalf("result = %+v", r)
		}
	})
}
