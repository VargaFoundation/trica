package redfish

import (
	"strings"
)

// chassis reads the chassis the system sits in: power supplies and power draw
// from Power, fans and temperatures from Thermal.
func (r *run) chassis(root serviceRoot, s system) {
	var paths []string
	for _, l := range s.Links.Chassis {
		if p := l.path(); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		if p := root.Chassis.path(); p != "" {
			all, err := r.members(p)
			if err != nil {
				r.fail("chassis", err)
			}
			if len(all) > 0 {
				paths = all[:1]
			}
		}
	}
	for _, p := range paths {
		var c chassis
		if err := r.cl.get(r.ctx, p, &c); err != nil {
			r.fail("chassis", err)
			continue
		}
		if pp := c.Power.path(); pp != "" {
			r.power(pp)
		}
		if tp := c.Thermal.path(); tp != "" {
			r.thermal(tp)
		}
	}
}

func (r *run) power(path string) {
	var p power
	if err := r.cl.get(r.ctx, path, &p); err != nil {
		r.fail("power", err)
		return
	}
	if _, set := r.dev.Attributes["power_w"]; !set && len(p.PowerControl) > 0 {
		w := p.PowerControl[0].PowerConsumedWatts
		if w == nil {
			w = p.PowerControl[0].PowerMetrics.AverageConsumedWatts
		}
		if w != nil {
			// Redfish's consumed watts are what the chassis draws.
			r.dev.Set("power_w", *w).Set("power_basis", "input")
		}
	}
	names, ids := make([]string, len(p.PowerSupplies)), make([]string, len(p.PowerSupplies))
	for i, ps := range p.PowerSupplies {
		names[i], ids[i] = first(ps.Name, ps.MemberID), strings.TrimSpace(ps.MemberID)
	}
	for i, name := range uniqueNames(names, ids) {
		ps := p.PowerSupplies[i]
		o := r.batch.Object("psu:"+name, "psu").In("dev").
			Identify("name", name).
			Identify("serial", strings.TrimSpace(ps.SerialNumber))
		o.Set("model", strings.TrimSpace(ps.Model)).
			Set("firmware_version", strings.TrimSpace(ps.FirmwareVersion)).
			Set("health", partHealth(ps.Status))
		if c := round(ps.PowerCapacityWatts); c != nil {
			o.Set("capacity_w", *c)
		}
		if ps.PowerInputWatts != nil {
			o.Set("input_w", *ps.PowerInputWatts)
		}
	}
}

func (r *run) thermal(path string) {
	var t thermal
	if err := r.cl.get(r.ctx, path, &t); err != nil {
		r.fail("thermal", err)
		return
	}

	var temps []temperature
	for _, tc := range t.Temperatures {
		if tc.ReadingCelsius != nil {
			temps = append(temps, tc)
		}
	}
	names, ids := make([]string, len(temps)), make([]string, len(temps))
	for i, tc := range temps {
		names[i], ids[i] = first(tc.Name, tc.MemberID), strings.TrimSpace(tc.MemberID)
	}
	for i, name := range uniqueNames(names, ids) {
		tc := temps[i]
		placement := sensorPlacement(tc)
		o := r.batch.Object("sensor:"+name, "sensor").In("dev").Identify("name", name)
		o.Set("kind", "temperature").
			Set("placement", placement).
			Set("reading", *tc.ReadingCelsius).
			Set("unit", "°C").
			Set("health", partHealth(tc.Status))
		if tc.UpperThresholdNonCritical != nil {
			o.Set("warning_threshold", *tc.UpperThresholdNonCritical)
		}
		if tc.UpperThresholdCritical != nil {
			o.Set("critical_threshold", *tc.UpperThresholdCritical)
		}
		switch placement {
		case "inlet":
			if _, set := r.dev.Attributes["inlet_temp_c"]; !set {
				r.dev.Set("inlet_temp_c", *tc.ReadingCelsius)
			}
		case "outlet":
			if _, set := r.dev.Attributes["outlet_temp_c"]; !set {
				r.dev.Set("outlet_temp_c", *tc.ReadingCelsius)
			}
		}
	}

	fnames, fids := make([]string, len(t.Fans)), make([]string, len(t.Fans))
	for i, f := range t.Fans {
		fnames[i], fids[i] = first(f.Name, f.FanName, f.MemberID), strings.TrimSpace(f.MemberID)
	}
	for i, name := range uniqueNames(fnames, fids) {
		f := t.Fans[i]
		o := r.batch.Object("fan:"+name, "fan").In("dev").Identify("name", name)
		o.Set("health", partHealth(f.Status))
		reading := f.Reading
		if reading == nil {
			reading = f.CurrentReading
		}
		if reading == nil {
			continue
		}
		switch units := strings.ToLower(first(f.ReadingUnits, f.Units)); {
		case strings.Contains(units, "rpm"):
			o.Set("speed_rpm", *round(reading))
		case strings.Contains(units, "percent"):
			o.Set("speed_pct", *reading)
		}
	}
}

// sensorPlacement says where a temperature sensor sits. The standard
// PhysicalContext decides when the firmware sets it; the sensor's name decides
// otherwise (iDRAC 8 leaves PhysicalContext out of some readings).
func sensorPlacement(t temperature) string {
	switch strings.ToLower(strings.TrimSpace(t.PhysicalContext)) {
	case "intake":
		return "inlet"
	case "exhaust":
		return "outlet"
	case "cpu":
		return "cpu"
	case "powersupply":
		return "psu"
	case "memory", "memorydevice":
		return "memory"
	case "room", "ambient":
		return "ambient"
	}
	n := strings.ToLower(t.Name)
	switch {
	case strings.Contains(n, "inlet"):
		return "inlet"
	case strings.Contains(n, "exhaust"):
		return "outlet"
	case strings.Contains(n, "cpu"):
		return "cpu"
	case strings.Contains(n, "dimm") || strings.Contains(n, "memory"):
		return "memory"
	case strings.Contains(n, "p/s") || strings.Contains(n, "power supply") || strings.Contains(n, "psu"):
		return "psu"
	}
	if strings.EqualFold(strings.TrimSpace(t.PhysicalContext), "systemboard") {
		return "system"
	}
	return "other"
}
