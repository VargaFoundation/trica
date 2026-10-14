package mcp_test

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// reading is one observation of a disk: how long before "now" it was made, the
// wear it reported and, when not zero, the spare blocks left.
type reading struct {
	ago         time.Duration
	wear, spare float64
}

func days(n float64) time.Duration { return time.Duration(n * float64(24*time.Hour)) }

// disk records the readings of one drive, oldest first, as its host's agent
// would: each reading is a batch observed at its own time.
func (e *env) disk(now time.Time, host, serial, media string, rs ...reading) {
	e.t.Helper()
	for _, r := range rs {
		e.clk.set(now.Add(-r.ago))
		b := sdk.NewBatch("redfish:"+serial, sdk.ScopePartial)
		b.Object("host", "device").Identify(sdk.IDKey, e.ids[host])
		d := b.Object(serial, "disk").Identify("serial", serial).In("host").Set("name", serial).Set("media", media).Set("wear_pct", r.wear)
		if r.spare != 0 {
			d.Set("spare_pct", r.spare)
		}
		e.apply(b)
	}
	e.clk.set(now)
}

// wearWorld seeds drives whose wear follows invented trends. Wear is "used"
// (rising), spare blocks are "left" (falling). Dates are relative to now.
//
//	ssd-fast     40 -> 50 -> 60 over 100 days: +0.2 %/day, reaches 80 % in 100 days
//	ssd-soon     70 -> 74 -> 78 over 40 days:  +0.2 %/day, reaches 80 % in 10 days
//	ssd-bent     slow for 80 days, then +1 %/day over the last 20: 5 points
//	ssd-slow     10 -> 12 over 100 days: reaches 80 % in about 9 years
//	ssd-flat     30 -> 31 -> 30: a flat trend
//	ssd-down     50 -> 45 -> 40: a trend that moves away
//	ssd-crossed  70 -> 85: past 80 %
//	ssd-one      one reading, 60 days ago
//	ssd-short    two readings 12 hours apart
//	hdd-fast     same trend as ssd-fast, but a hard disk
func wearWorld(e *env) time.Time {
	now := t0.Add(120 * 24 * time.Hour)
	e.disk(now, "srv-01", "ssd-fast", "ssd", reading{days(100), 40, 90}, reading{days(50), 50, 80}, reading{0, 60, 70})
	e.disk(now, "srv-02", "ssd-slow", "ssd", reading{days(100), 10, 0}, reading{0, 12, 0})
	e.disk(now, "srv-03", "ssd-flat", "ssd", reading{days(100), 30, 0}, reading{days(50), 31, 0}, reading{0, 30, 0})
	e.disk(now, "srv-04", "ssd-down", "ssd", reading{days(100), 50, 0}, reading{days(50), 45, 0}, reading{0, 40, 0})
	e.disk(now, "srv-05", "ssd-crossed", "ssd", reading{days(100), 70, 0}, reading{0, 85, 0})
	e.disk(now, "srv-06", "ssd-one", "ssd", reading{days(60), 20, 0})
	e.disk(now, "srv-07", "ssd-short", "ssd", reading{12 * time.Hour, 10, 0}, reading{0, 12, 0})
	e.disk(now, "srv-08", "ssd-soon", "ssd", reading{days(40), 70, 0}, reading{days(20), 74, 0}, reading{0, 78, 0})
	e.disk(now, "srv-09", "ssd-bent", "ssd", reading{days(100), 10, 0}, reading{days(90), 12, 0}, reading{days(20), 50, 0}, reading{days(10), 60, 0}, reading{0, 70, 0})
	e.disk(now, "srv-10", "hdd-fast", "hdd", reading{days(100), 40, 0}, reading{days(50), 50, 0}, reading{0, 60, 0})
	return now
}

func fnum(t testing.TB, v any, path ...any) float64 {
	t.Helper()
	f, ok := dig(t, v, path...).(float64)
	if !ok {
		t.Fatalf("%v is not a number: %v", path, dig(t, v, path...))
	}
	return f
}

func near(t testing.TB, what string, got, want, eps float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Errorf("%s = %v, want %v (+/- %v)", what, got, want, eps)
	}
}

func TestForecastThreshold(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	now := wearWorld(e)
	cs := e.connect(e.as(tokReader))

	// ssdWear is the question of the specification: SSDs that reach 80 % wear within six months.
	ssdWear := func(over m) m {
		a := m{"type": "disk", "attribute": "wear_pct", "threshold": 80, "horizon_days": 183,
			"filters": []m{{"attribute": "media", "op": "in", "value": "ssd,nvme"}}}
		for k, v := range over {
			a[k] = v
		}
		return a
	}
	forecast := func(over m) map[string]any { return ok(t, cs, "forecast_threshold", ssdWear(over)).Data }

	t.Run("the summary accounts for every drive scanned, then the ones that cross, soonest first", func(t *testing.T) {
		d := forecast(nil)
		for k, want := range map[string]int{"matching": 9, "scanned": 9, "total": 3, "returned": 3, "will_cross": 3,
			"beyond_horizon": 1, "no_trend": 2, "already_crossed": 1, "insufficient_history": 2} {
			if got := num(t, d, "summary", k); got != want {
				t.Errorf("summary.%s = %d, want %d", k, got, want)
			}
		}
		if dig(t, d, "summary", "truncated") != false || dig(t, d, "summary", "scan_truncated") != nil {
			t.Errorf("truncation flags: %v", d["summary"])
		}
		if got := num(t, d, "summary", "by_type", "disk"); got != 3 {
			t.Errorf("by_type: %d", got)
		}
		if got := displays(t, d, "items"); !slices.Equal(got, []string{"ssd-soon", "ssd-bent", "ssd-fast"}) {
			t.Fatalf("items %v", got)
		}
		if got := str(t, d, "summary", "direction"); got != "rising" {
			t.Errorf("direction %q", got)
		}
		// The line through ssd-soon is exact: 70 + 0.2 x, 80 reached on day 50 of its history, 10 days from now.
		near(t, "soon slope", fnum(t, d, "items", 0, "slope_per_day"), 0.2, 1e-9)
		near(t, "soon days", fnum(t, d, "items", 0, "days_to_cross"), 10, 0.05)
		near(t, "soon value", fnum(t, d, "items", 0, "value"), 78, 0)
		if got := num(t, d, "items", 0, "points"); got != 3 {
			t.Errorf("soon points %d", got)
		}
		want := now.Add(days(10))
		if got, err := time.Parse(time.RFC3339, str(t, d, "items", 0, "crosses_at")); err != nil || got.Sub(want).Abs() > time.Minute {
			t.Errorf("soon crosses_at %v (%v), want %v", got, err, want)
		}
		if got := str(t, d, "items", 0, "window", "from"); !strings.HasPrefix(got, now.Add(-days(40)).Format("2006-01-02T15:04")) {
			t.Errorf("soon window.from %q", got)
		}
		if got := str(t, d, "items", 0, "window", "to"); !strings.HasPrefix(got, now.Format("2006-01-02T15:04")) {
			t.Errorf("soon window.to %q", got)
		}
		// A line fitted to all five points of ssd-bent, not the last two, and not the first and last.
		if got := num(t, d, "items", 1, "points"); got != 5 {
			t.Errorf("bent points %d", got)
		}
		near(t, "bent slope", fnum(t, d, "items", 1, "slope_per_day"), 5208.0/8920.0, 1e-5)
		near(t, "bent days", fnum(t, d, "items", 1, "days_to_cross"), 23.8, 0.05)
		near(t, "fast slope", fnum(t, d, "items", 2, "slope_per_day"), 0.2, 1e-9)
		near(t, "fast days", fnum(t, d, "items", 2, "days_to_cross"), 100, 0.05)
	})

	t.Run("a drive already past the threshold is reported as such, not as a forecast", func(t *testing.T) {
		d := forecast(nil)
		if got := displays(t, d, "already_crossed"); !slices.Equal(got, []string{"ssd-crossed"}) {
			t.Fatalf("already_crossed %v", got)
		}
		near(t, "crossed value", fnum(t, d, "already_crossed", 0, "value"), 85, 0)
		if !strings.Contains(str(t, d, "already_crossed", 0, "reason"), "already") {
			t.Errorf("reason %q", str(t, d, "already_crossed", 0, "reason"))
		}
		// At the threshold itself counts as reached.
		at := forecast(m{"threshold": 85})
		if got := displays(t, at, "already_crossed"); !slices.Equal(got, []string{"ssd-crossed"}) {
			t.Errorf("a value equal to the threshold: %v", got)
		}
	})

	t.Run("too little history is listed and counted, never dropped", func(t *testing.T) {
		d := forecast(nil)
		got := displays(t, d, "insufficient_history")
		slices.Sort(got)
		if !slices.Equal(got, []string{"ssd-one", "ssd-short"}) {
			t.Fatalf("insufficient_history %v", got)
		}
		for i, it := range dig(t, d, "insufficient_history").([]any) {
			if str(t, it, "reason") == "" {
				t.Errorf("entry %d does not say why", i)
			}
		}
		// The one-point drive says it has one value, the short one that its history is under a day.
		for i, name := range displays(t, d, "insufficient_history") {
			reason := str(t, d, "insufficient_history", i, "reason")
			if name == "ssd-one" && (!strings.Contains(reason, "one value") || num(t, d, "insufficient_history", i, "points") != 1) {
				t.Errorf("ssd-one: %q", reason)
			}
			if name == "ssd-short" && (!strings.Contains(reason, "under one day") || num(t, d, "insufficient_history", i, "points") != 2) {
				t.Errorf("ssd-short: %q", reason)
			}
		}
	})

	t.Run("a flat trend, or one that moves away, never crosses", func(t *testing.T) {
		d := forecast(map[string]any{"horizon_days": 3650})
		// flat and down are counted as no_trend and appear nowhere else.
		if got := num(t, d, "summary", "no_trend"); got != 2 {
			t.Errorf("no_trend %d", got)
		}
		for _, list := range []string{"items", "already_crossed", "insufficient_history"} {
			for _, n := range displays(t, d, list) {
				if n == "ssd-flat" || n == "ssd-down" {
					t.Errorf("%s is in %s", n, list)
				}
			}
		}
		// With a 10-year horizon the slow one (it reaches 80 % in about 9 years) is in.
		if got := num(t, d, "summary", "beyond_horizon"); got != 0 {
			t.Errorf("beyond_horizon with a 10-year horizon: %d", got)
		}
		if got := displays(t, d, "items"); !slices.Contains(got, "ssd-slow") || slices.Contains(got, "ssd-flat") {
			t.Errorf("items %v", got)
		}
	})

	t.Run("the horizon decides what is reported", func(t *testing.T) {
		d := forecast(m{"horizon_days": 30})
		if got := displays(t, d, "items"); !slices.Equal(got, []string{"ssd-soon", "ssd-bent"}) {
			t.Errorf("30 days: %v", got)
		}
		if got := num(t, d, "summary", "beyond_horizon"); got != 2 {
			t.Errorf("30 days: beyond_horizon %d", got)
		}
		if got := displays(t, forecast(m{"horizon_days": 9}), "items"); len(got) != 0 {
			t.Errorf("9 days: %v", got)
		}
		if got := displays(t, forecast(m{"horizon_days": 11}), "items"); !slices.Equal(got, []string{"ssd-soon"}) {
			t.Errorf("11 days: %v", got)
		}
		// The default is about six months.
		a := ssdWear(nil)
		delete(a, "horizon_days")
		d = ok(t, cs, "forecast_threshold", a).Data
		if num(t, d, "summary", "horizon_days") != 180 || num(t, d, "summary", "will_cross") != 3 {
			t.Errorf("default horizon: %v", d["summary"])
		}
	})

	t.Run("the window restricts the history the line is fitted to", func(t *testing.T) {
		d := forecast(m{"window_days": 30})
		// Only ssd-soon (2 points) and ssd-bent (3 points) have two points in the last 30 days.
		if got := displays(t, d, "items"); !slices.Equal(got, []string{"ssd-bent", "ssd-soon"}) {
			t.Fatalf("items %v", got)
		}
		if got := num(t, d, "items", 0, "points"); got != 3 {
			t.Errorf("bent points in the window: %d", got)
		}
		near(t, "bent slope", fnum(t, d, "items", 0, "slope_per_day"), 1, 1e-9)
		near(t, "bent days", fnum(t, d, "items", 0, "days_to_cross"), 10, 0.05)
		if got := num(t, d, "summary", "insufficient_history"); got != 6 {
			t.Errorf("insufficient with a 30-day window: %d", got)
		}
		if got := num(t, d, "summary", "window_days"); got != 30 {
			t.Errorf("window_days %d", got)
		}
		if got := str(t, d, "items", 0, "window", "from"); !strings.HasPrefix(got, now.Add(-days(20)).Format("2006-01-02T15:04")) {
			t.Errorf("window.from %q", got)
		}
	})

	t.Run("falling is for life left: the direction flips what counts as crossing", func(t *testing.T) {
		falling := func(threshold float64) map[string]any {
			return ok(t, cs, "forecast_threshold", m{"type": "disk", "attribute": "spare_pct", "threshold": threshold, "direction": "falling"}).Data
		}
		// 90 -> 80 -> 70 over 100 days: -0.2 per day, 40 left in 150 days.
		d := falling(40)
		if got := displays(t, d, "items"); !slices.Equal(got, []string{"ssd-fast"}) {
			t.Fatalf("falling to 40: %v (%v)", got, d["summary"])
		}
		near(t, "slope", fnum(t, d, "items", 0, "slope_per_day"), -0.2, 1e-9)
		near(t, "days", fnum(t, d, "items", 0, "days_to_cross"), 150, 0.05)
		if str(t, d, "summary", "direction") != "falling" {
			t.Errorf("direction %q", str(t, d, "summary", "direction"))
		}
		// Falling to 100: the drive is already below it.
		if got := displays(t, falling(100), "already_crossed"); !slices.Equal(got, []string{"ssd-fast"}) {
			t.Errorf("falling to 100: %v", got)
		}
		// Equal to the threshold counts as reached, in this direction too.
		if got := displays(t, falling(70), "already_crossed"); !slices.Equal(got, []string{"ssd-fast"}) {
			t.Errorf("falling to 70: %v", got)
		}
		// The same attribute read as rising: 70 is already past 40, and a falling line never reaches 90.
		rising := func(threshold float64) map[string]any {
			return ok(t, cs, "forecast_threshold", m{"type": "disk", "attribute": "spare_pct", "threshold": threshold}).Data
		}
		if got := displays(t, rising(40), "already_crossed"); !slices.Equal(got, []string{"ssd-fast"}) {
			t.Errorf("rising to 40: %v", got)
		}
		r := rising(90)
		if num(t, r, "summary", "no_trend") != 1 || num(t, r, "summary", "will_cross") != 0 {
			t.Errorf("rising to 90 on a falling line: %v", r["summary"])
		}
		// A wear that only falls reaches a low threshold when asked falling.
		w := ok(t, cs, "forecast_threshold", m{"type": "disk", "attribute": "wear_pct", "threshold": 10, "direction": "falling", "horizon_days": 365,
			"filters": []m{{"attribute": "media", "op": "eq", "value": "ssd"}}}).Data
		if got := displays(t, w, "items"); !slices.Equal(got, []string{"ssd-down"}) {
			t.Errorf("a falling wear toward 10: %v", got)
		}
	})

	t.Run("the population is selected like filter_objects", func(t *testing.T) {
		all := ok(t, cs, "forecast_threshold", m{"type": "disk", "attribute": "wear_pct", "threshold": 80}).Data
		if got := displays(t, all, "items"); !slices.Contains(got, "hdd-fast") || num(t, all, "summary", "matching") != 10 {
			t.Errorf("without the media filter: %v %v", got, all["summary"])
		}
		hdd := ok(t, cs, "forecast_threshold", m{"type": "disk", "attribute": "wear_pct", "threshold": 80, "query": "media:hdd"}).Data
		if got := displays(t, hdd, "items"); !slices.Equal(got, []string{"hdd-fast"}) {
			t.Errorf("TQL narrowing: %v", got)
		}
		// Objects without the attribute are not part of the population.
		none := ok(t, cs, "forecast_threshold", m{"type": "disk", "attribute": "temperature_c", "threshold": 80}).Data
		if num(t, none, "summary", "matching") != 0 || num(t, none, "summary", "scanned") != 0 {
			t.Errorf("an attribute no drive has: %v", none["summary"])
		}
		// A text attribute has no trend: the drives are listed with the reason, never forecast.
		txt := ok(t, cs, "forecast_threshold", m{"type": "disk", "attribute": "media", "threshold": 1}).Data
		if num(t, txt, "summary", "insufficient_history") != 10 || num(t, txt, "summary", "will_cross") != 0 ||
			!strings.Contains(str(t, txt, "insufficient_history", 0, "reason"), "not a number") {
			t.Errorf("a text attribute: %v %v", txt["summary"], dig(t, txt, "insufficient_history", 0))
		}
		// Other attributes can be returned with each drive.
		f := forecast(m{"fields": []string{"media", "serial"}})
		if str(t, f, "items", 0, "attributes", "media") != "ssd" || str(t, f, "items", 0, "attributes", "serial") != "ssd-soon" {
			t.Errorf("fields: %v", dig(t, f, "items", 0))
		}
		if dig(t, f, "items", 0, "parent_id") != e.ids["srv-08"] {
			t.Errorf("parent_id %v", dig(t, f, "items", 0, "parent_id"))
		}
	})

	t.Run("the answer is paginated, summary first", func(t *testing.T) {
		d := forecast(m{"limit": 1, "offset": 1})
		if got := displays(t, d, "items"); !slices.Equal(got, []string{"ssd-bent"}) {
			t.Fatalf("page %v", got)
		}
		if num(t, d, "summary", "total") != 3 || num(t, d, "summary", "returned") != 1 || dig(t, d, "summary", "truncated") != true || num(t, d, "summary", "next_offset") != 2 {
			t.Errorf("summary %v", d["summary"])
		}
		if num(t, d, "limit") != 1 || num(t, d, "offset") != 1 {
			t.Errorf("limit/offset %v %v", d["limit"], d["offset"])
		}
		// The lists beside the page are paged too, and the summary still counts them whole.
		if got := len(dig(t, d, "insufficient_history").([]any)); got != 1 {
			t.Errorf("insufficient_history page: %d", got)
		}
		if got := len(dig(t, d, "already_crossed").([]any)); got != 0 {
			t.Errorf("already_crossed page at offset 1: %d", got)
		}
		if got := num(t, d, "summary", "insufficient_history"); got != 2 {
			t.Errorf("insufficient_history count %d", got)
		}
		last := forecast(m{"limit": 3})
		if dig(t, last, "summary", "truncated") != false {
			t.Errorf("a page holding everything is not truncated: %v", last["summary"])
		}
		// Nothing crosses within 9 days, but two drives lack history: with a page of one,
		// the summary still says there is more to read.
		side := forecast(m{"limit": 1, "horizon_days": 9})
		if num(t, side, "summary", "total") != 0 || dig(t, side, "summary", "truncated") != true || num(t, side, "summary", "next_offset") != 1 {
			t.Errorf("a side list with more to read: %v", side["summary"])
		}
		// Threshold 40 is already past by five drives and out of reach of none that crosses:
		// the already_crossed list alone has more than a page of 2 (the history list holds 2).
		crossed := forecast(m{"limit": 2, "threshold": 40})
		if num(t, crossed, "summary", "total") != 0 || num(t, crossed, "summary", "already_crossed") != 5 ||
			dig(t, crossed, "summary", "truncated") != true || num(t, crossed, "summary", "next_offset") != 2 {
			t.Errorf("already_crossed with more to read: %v", crossed["summary"])
		}
		if got := len(dig(t, crossed, "already_crossed").([]any)); got != 2 {
			t.Errorf("already_crossed page: %d", got)
		}
	})

	t.Run("bad arguments are refused with what to do", func(t *testing.T) {
		for _, c := range []struct {
			name string
			args m
			slug string
		}{
			{"a null threshold", m{"attribute": "wear_pct", "threshold": nil}, "invalid-request"},
			{"direction", m{"attribute": "wear_pct", "threshold": 80, "direction": "sideways"}, "invalid-request"},
			{"horizon too long", m{"attribute": "wear_pct", "threshold": 80, "horizon_days": 4000}, "invalid-request"},
			{"negative horizon", m{"attribute": "wear_pct", "threshold": 80, "horizon_days": -1}, "invalid-request"},
			{"negative window", m{"attribute": "wear_pct", "threshold": 80, "window_days": -3}, "invalid-request"},
			{"window too long", m{"attribute": "wear_pct", "threshold": 80, "window_days": 4000}, "invalid-request"},
			{"not a field name", m{"attribute": "Wear %", "threshold": 80}, "invalid-request"},
			{"a secret", m{"attribute": "snmp_community", "threshold": 1}, "forbidden"},
			{"a secret filter", m{"attribute": "wear_pct", "threshold": 1, "filters": []m{{"attribute": "password", "op": "exists"}}}, "forbidden"},
			{"a bad query", m{"attribute": "wear_pct", "threshold": 1, "query": "((("}, "invalid-query"},
			{"a bad page", m{"attribute": "wear_pct", "threshold": 1, "limit": 5000}, "invalid-request"},
		} {
			t.Run(c.name, func(t *testing.T) { denied(t, cs, "forecast_threshold", c.args, c.slug) })
		}
	})

	t.Run("a missing threshold is refused by the input schema", func(t *testing.T) {
		a := call(t, cs, "forecast_threshold", m{"attribute": "wear_pct"})
		if !a.IsError || !strings.Contains(a.Text, "threshold") {
			t.Fatalf("got %q", a.Text)
		}
	})

	t.Run("nothing is written and every call is audited", func(t *testing.T) {
		before := len(e.aud.Entries())
		forecast(nil)
		entries := e.aud.Entries()[before:]
		if len(entries) != 1 || entries[0].Action != "mcp.tool.forecast_threshold" || entries[0].Outcome != api.OutcomeOK || entries[0].Target != "disk/wear_pct" {
			t.Fatalf("audit %+v", entries)
		}
		if args, _ := entries[0].Details["args"].(string); !strings.Contains(args, "wear_pct") || !strings.Contains(args, "80") {
			t.Errorf("the arguments are not audited: %v", entries[0].Details)
		}
	})

	t.Run("the caller's rights bound the population", func(t *testing.T) {
		// Read on devices only: the disks are not readable, whatever the question.
		devices := e.connect(e.as(tokDevices))
		denied(t, devices, "forecast_threshold", ssdWear(nil), "forbidden")
		d := ok(t, devices, "forecast_threshold", m{"attribute": "wear_pct", "threshold": 80}).Data
		if num(t, d, "summary", "matching") != 0 || num(t, d, "summary", "scanned") != 0 {
			t.Errorf("a grant limited to devices reaches disks: %v", d["summary"])
		}
		// Read limited to a site: listing needs a type readable everywhere.
		denied(t, e.connect(e.as(tokSiteParis)), "forecast_threshold", ssdWear(nil), "forbidden")
		// No read right at all: refused before anything is read, and audited as denied.
		before := len(e.aud.Entries())
		denied(t, e.connect(e.as(tokProposeNR)), "forecast_threshold", ssdWear(nil), "forbidden")
		denied(t, e.connect(e.as(tokNone)), "forecast_threshold", ssdWear(nil), "forbidden")
		for _, en := range e.aud.Entries()[before:] {
			if en.Action != "mcp.tool.forecast_threshold" || en.Outcome != api.OutcomeDenied {
				t.Errorf("entry %+v", en)
			}
		}
	})
}

// The scan reads the population a page at a time, up to a bound, and says so
// when the bound cut it short.
func TestForecastScanBound(t *testing.T) {
	ssd := m{"type": "disk", "attribute": "wear_pct", "threshold": 80, "horizon_days": 183,
		"filters": []m{{"attribute": "media", "op": "in", "value": "ssd,nvme"}}}

	t.Run("paging reads every drive", func(t *testing.T) {
		e := newEnv(t, proposeMCP, false)
		wearWorld(e)
		mcp.SetForecastScan(e.srv, 4, 100)
		d := ok(t, e.connect(e.as(tokReader)), "forecast_threshold", ssd).Data
		for k, want := range map[string]int{"matching": 9, "scanned": 9, "will_cross": 3, "no_trend": 2, "already_crossed": 1, "insufficient_history": 2} {
			if got := num(t, d, "summary", k); got != want {
				t.Errorf("summary.%s = %d, want %d", k, got, want)
			}
		}
		if dig(t, d, "summary", "scan_truncated") != nil {
			t.Errorf("nothing was cut: %v", d["summary"])
		}
	})

	t.Run("a bound short of the population is said, and only what was read is classified", func(t *testing.T) {
		e := newEnv(t, proposeMCP, false)
		wearWorld(e)
		mcp.SetForecastScan(e.srv, 4, 6)
		d := ok(t, e.connect(e.as(tokReader)), "forecast_threshold", ssd).Data
		// By display: ssd-bent, ssd-crossed, ssd-down, ssd-fast, ssd-flat, ssd-one are read; short, slow, soon are not.
		for k, want := range map[string]int{"matching": 9, "scanned": 6, "will_cross": 2, "no_trend": 2, "already_crossed": 1, "insufficient_history": 1, "beyond_horizon": 0} {
			if got := num(t, d, "summary", k); got != want {
				t.Errorf("summary.%s = %d, want %d", k, got, want)
			}
		}
		if dig(t, d, "summary", "scan_truncated") != true {
			t.Errorf("the summary must say the scan was cut: %v", d["summary"])
		}
		if got := displays(t, d, "items"); !slices.Equal(got, []string{"ssd-bent", "ssd-fast"}) {
			t.Errorf("items %v", got)
		}
	})
}

// A line that already crosses today is reported as crossing now, not in the past.
func TestForecastCrossingIsNeverInThePast(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	now := t0.Add(120 * 24 * time.Hour)
	// 70 -> 85 -> 79: the fitted line is above 80 today, yet the drive reads 79.
	e.disk(now, "srv-01", "ssd-late", "ssd", reading{days(100), 70, 0}, reading{days(50), 85, 0}, reading{0, 79, 0})
	d := ok(t, e.connect(e.as(tokReader)), "forecast_threshold", m{"type": "disk", "attribute": "wear_pct", "threshold": 80}).Data
	if got := displays(t, d, "items"); !slices.Equal(got, []string{"ssd-late"}) {
		t.Fatalf("items %v (%v)", got, d["summary"])
	}
	if got := fnum(t, d, "items", 0, "days_to_cross"); got != 0 {
		t.Errorf("days_to_cross %v, want 0", got)
	}
	if got, err := time.Parse(time.RFC3339, str(t, d, "items", 0, "crosses_at")); err != nil || got.Sub(now).Abs() > time.Second {
		t.Errorf("crosses_at %v (%v), want now %v", got, err, now)
	}
	if got := fnum(t, d, "items", 0, "value"); got != 79 {
		t.Errorf("value %v", got)
	}
}
