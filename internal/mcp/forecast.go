package mcp

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
)

const descForecast = `Which objects will cross a threshold within a horizon, by linear trend: "the SSDs that will reach 80 % wear within six months" is ` +
	`forecast_threshold{type:"disk", attribute:"wear_pct", threshold:80, horizon_days:183, filters:[{attribute:"media", op:"in", value:"ssd,nvme"}]}. ` +
	`For each object selected (same selector as filter_objects) a least-squares line is fitted to the attribute's history, the current value included, ` +
	`and extrapolated. direction is rising (the default: wear used, temperature) or falling (life left, spare blocks): use the one that matches the attribute. ` +
	`items are the objects whose trend crosses within horizon_days, soonest first, with crosses_at, slope_per_day, the number of points and the window they span. ` +
	`Objects already past the threshold are in already_crossed. An object with fewer than 2 points or a history spanning under one day is in insufficient_history ` +
	`(history records changes: a value that never changed is one point). A flat trend, or one that moves away, never crosses. ` +
	`The summary counts every object scanned in one of these classes, so none is dropped silently. It is a straight-line estimate, not a lifetime guarantee.`

const (
	defaultHorizonDays = 180
	maxHorizonDays     = 3650
	// maxForecastObjects bounds how many objects one call fits a trend to: a
	// history read per object. Beyond it the summary says scan_truncated.
	maxForecastObjects = 5000
	// maxForecastPoints bounds the history read per object.
	maxForecastPoints = 1000
	minSpan           = 24 * time.Hour
	day               = 24 * time.Hour
)

// ForecastInput is the input of the forecast_threshold tool.
type ForecastInput struct {
	Attribute   string   `json:"attribute" jsonschema:"the numeric attribute to extrapolate, e.g. wear_pct (see describe_schema)"`
	Threshold   *float64 `json:"threshold" jsonschema:"the value to reach, in the attribute's unit, e.g. 80 for 80 % wear"`
	Direction   string   `json:"direction,omitempty" jsonschema:"rising (the default: the value grows toward the threshold, e.g. wear used) or falling (it shrinks toward it, e.g. life left)"`
	HorizonDays int      `json:"horizon_days,omitempty" jsonschema:"report the objects that cross within this many days from now, 1 to 3650 (default 180, about six months)"`
	WindowDays  int      `json:"window_days,omitempty" jsonschema:"fit the trend to the values that appeared in the last N days only, 1 to 3650 (default: the whole history)"`
	Type        string   `json:"type,omitempty" jsonschema:"object type to examine, e.g. disk"`
	Filters     []Filter `json:"filters,omitempty" jsonschema:"conditions narrowing the population, all of which must hold"`
	Query       string   `json:"query,omitempty" jsonschema:"optional TQL query narrowing the population"`
	Fields      []string `json:"fields,omitempty" jsonschema:"other attributes to return for each object (e.g. model, slot)"`
	PageArgs
}

// ForecastWindow is the time span of the points a trend was fitted to.
type ForecastWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// ForecastItem is an object whose trend crosses the threshold within the horizon.
type ForecastItem struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Display  string  `json:"display"`
	ParentID *string `json:"parent_id,omitempty"`
	// Value is the current value.
	Value float64 `json:"value"`
	// SlopePerDay is the fitted change per day (positive: growing).
	SlopePerDay float64 `json:"slope_per_day"`
	// CrossesAt is where the fitted line meets the threshold, never before now.
	CrossesAt   time.Time      `json:"crosses_at"`
	DaysToCross float64        `json:"days_to_cross"`
	Points      int            `json:"points"`
	Window      ForecastWindow `json:"window"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// ForecastOther is an object that is not in items, with the reason.
type ForecastOther struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Display  string   `json:"display"`
	ParentID *string  `json:"parent_id,omitempty"`
	Value    *float64 `json:"value,omitempty"`
	Points   int      `json:"points"`
	Reason   string   `json:"reason"`
}

// ForecastSummary says first how many objects were scanned and in which class
// each one fell. Total, ByType, Returned and Truncated describe items.
type ForecastSummary struct {
	Attribute   string    `json:"attribute"`
	Threshold   float64   `json:"threshold,omitempty"`
	Direction   string    `json:"direction"`
	HorizonDays int       `json:"horizon_days"`
	WindowDays  int       `json:"window_days,omitempty"`
	At          time.Time `json:"at"`
	Matching    int       `json:"matching"`
	Scanned     int       `json:"scanned"`
	// ScanTruncated: more objects match than one call examines.
	ScanTruncated       bool `json:"scan_truncated,omitempty"`
	WillCross           int  `json:"will_cross"`
	BeyondHorizon       int  `json:"beyond_horizon"`
	NoTrend             int  `json:"no_trend"`
	AlreadyCrossed      int  `json:"already_crossed"`
	InsufficientHistory int  `json:"insufficient_history"`
	Summary
}

// ForecastAnswer is the answer of forecast_threshold: the summary first, then
// the page of items and the two lists the objects that need another reading.
type ForecastAnswer struct {
	Summary             ForecastSummary `json:"summary"`
	Limit               int             `json:"limit"`
	Offset              int             `json:"offset"`
	Items               []ForecastItem  `json:"items"`
	AlreadyCrossed      []ForecastOther `json:"already_crossed"`
	InsufficientHistory []ForecastOther `json:"insufficient_history"`
}

type point struct {
	at time.Time
	v  float64
}

// asFloat reads a JSON value as a float.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// trend is the least-squares line through points: value = intercept + slope*x,
// x in days since origin.
type trend struct {
	origin           time.Time
	slope, intercept float64
}

// fit fits a line through the points, which must not all share one instant.
func fit(pts []point) trend {
	origin := pts[0].at
	n := float64(len(pts))
	var sx, sy float64
	for _, p := range pts {
		sx += float64(p.at.Sub(origin)) / float64(day)
		sy += p.v
	}
	mx, my := sx/n, sy/n
	var sxx, sxy float64
	for _, p := range pts {
		dx := float64(p.at.Sub(origin))/float64(day) - mx
		sxx += dx * dx
		sxy += dx * (p.v - my)
	}
	slope := sxy / sxx
	return trend{origin: origin, slope: slope, intercept: my - slope*mx}
}

// round keeps the answer readable: no 15-digit floats.
func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}

// reached tells whether v is at or past the threshold in the given direction.
func reached(v, threshold float64, rising bool) bool {
	if rising {
		return v >= threshold
	}
	return v <= threshold
}

func (s *Server) toolForecast(t *tc, in ForecastInput) (any, error) {
	attr := strings.TrimSpace(in.Attribute)
	t.target = in.Type + "/" + attr
	if !fieldRe.MatchString(attr) {
		return nil, api.BadRequest("attribute %q is not a field name", in.Attribute)
	}
	if api.Sensitive(attr) {
		return nil, api.Forbidden("the attribute %q is not exposed and cannot be queried", attr)
	}
	if in.Threshold == nil {
		return nil, api.BadRequest("threshold is required (the value to reach, e.g. 80)")
	}
	threshold := *in.Threshold
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return nil, api.BadRequest("threshold must be a finite number")
	}
	rising := true
	switch strings.ToLower(in.Direction) {
	case "", "rising":
	case "falling":
		rising = false
	default:
		return nil, api.BadRequest("direction must be rising or falling")
	}
	horizon := in.HorizonDays
	if horizon == 0 {
		horizon = defaultHorizonDays
	}
	if horizon < 1 || horizon > maxHorizonDays {
		return nil, api.BadRequest("horizon_days must be between 1 and %d", maxHorizonDays)
	}
	if in.WindowDays < 0 || in.WindowDays > maxHorizonDays {
		return nil, api.BadRequest("window_days must be between 1 and %d (or left out for the whole history)", maxHorizonDays)
	}
	limit, offset, err := in.bounds()
	if err != nil {
		return nil, err
	}

	q, err := t.g.Query(in.Query, "display", in.Type, false)
	if err != nil {
		return nil, err
	}
	nodes, err := filterNodes(in.Filters)
	if err != nil {
		return nil, err
	}
	nodes = append(nodes, tql.Pred{Field: "has", Op: tql.OpEq, Values: []string{attr}})
	and(q, nodes...)
	objs, matching, err := t.scan(q, "forecast "+attr)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	var windowStart time.Time
	if in.WindowDays > 0 {
		windowStart = now.Add(-time.Duration(in.WindowDays) * day)
	}
	sum := ForecastSummary{Attribute: attr, Threshold: threshold, Direction: map[bool]string{true: "rising", false: "falling"}[rising],
		HorizonDays: horizon, WindowDays: in.WindowDays, At: now, Matching: matching, Scanned: len(objs), ScanTruncated: matching > len(objs)}
	var crossing []ForecastItem
	var crossed, short []ForecastOther

	for _, o := range objs {
		if err := t.ctx.Err(); err != nil {
			return nil, err
		}
		other := ForecastOther{ID: o.ID, Type: o.Type, Display: o.Display, ParentID: o.ParentID}
		cur, isNum := asFloat(o.Attrs[attr])
		if !isNum {
			other.Reason = "the current value is not a number"
			short = append(short, other)
			continue
		}
		other.Value = &cur
		if reached(cur, threshold, rising) {
			other.Reason = "the current value is already at or past the threshold"
			crossed = append(crossed, other)
			continue
		}
		vals, err := s.st.History(t.ctx, o.ID, attr, maxForecastPoints)
		if err != nil {
			return nil, err
		}
		var pts []point
		for _, v := range vals {
			if f, ok := asFloat(v.Value); ok && !v.ValidFrom.Before(windowStart) {
				pts = append(pts, point{at: v.ValidFrom.UTC(), v: f})
			}
		}
		sort.Slice(pts, func(i, j int) bool { return pts[i].at.Before(pts[j].at) })
		other.Points = len(pts)
		if len(pts) < 2 || pts[len(pts)-1].at.Sub(pts[0].at) < minSpan {
			other.Reason = insufficientReason(len(pts), pts)
			short = append(short, other)
			continue
		}
		tr := fit(pts)
		if (rising && tr.slope <= 0) || (!rising && tr.slope >= 0) {
			sum.NoTrend++
			continue
		}
		// Days from the origin at which the line meets the threshold, then from now.
		cross := tr.origin.Add(time.Duration((threshold - tr.intercept) / tr.slope * float64(day)))
		daysFromNow := math.Max(0, float64(cross.Sub(now))/float64(day))
		if daysFromNow > float64(horizon) {
			sum.BeyondHorizon++
			continue
		}
		at := now
		if cross.After(now) {
			at = cross
		}
		crossing = append(crossing, ForecastItem{ID: o.ID, Type: o.Type, Display: o.Display, ParentID: o.ParentID, Value: cur,
			SlopePerDay: round(tr.slope, 6), CrossesAt: at.Truncate(time.Second), DaysToCross: round(daysFromNow, 1), Points: len(pts),
			Window: ForecastWindow{From: pts[0].at, To: pts[len(pts)-1].at}, Attributes: forecastFields(o, in.Fields)})
	}
	sort.SliceStable(crossing, func(i, j int) bool {
		if !crossing[i].CrossesAt.Equal(crossing[j].CrossesAt) {
			return crossing[i].CrossesAt.Before(crossing[j].CrossesAt)
		}
		return crossing[i].Display < crossing[j].Display
	})

	byType := map[string]int{}
	for _, it := range crossing {
		byType[it.Type]++
	}
	sum.WillCross, sum.AlreadyCrossed, sum.InsufficientHistory = len(crossing), len(crossed), len(short)
	items := pageOf(crossing, offset, limit)
	sum.Summary = newSummary(len(crossing), offset, len(items), byType)
	if offset+limit < len(crossed) || offset+limit < len(short) {
		next := offset + limit
		sum.Truncated, sum.NextOffset = true, &next
	}
	return &ForecastAnswer{Summary: sum, Limit: limit, Offset: offset, Items: items,
		AlreadyCrossed: pageOf(crossed, offset, limit), InsufficientHistory: pageOf(short, offset, limit)}, nil
}

func insufficientReason(n int, pts []point) string {
	switch n {
	case 0:
		return "no numeric value in the history window"
	case 1:
		return "one value since " + pts[0].at.Format(time.RFC3339) + ": a trend needs at least 2 points"
	}
	return "the history spans under one day: a trend needs at least 2 points one day apart"
}

// forecastFields projects the other attributes asked for, never a sensitive one.
func forecastFields(o *store.Object, fields []string) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	items := objectItems([]*store.Object{o}, fields)
	if len(items[0].Attributes) == 0 {
		return nil
	}
	return items[0].Attributes
}

// scan reads every object a query matches, a page at a time, up to the bound
// of one call. It returns them with the number that matched.
func (t *tc) scan(q *tql.Query, label string) (objs []*store.Object, matching int, err error) {
	page, bound := t.s.scanPage, t.s.scanMax
	for off := 0; off < bound; off += page {
		res, err := t.s.searcher.SearchQuery(t.ctx, q, label, search.Page{Limit: page, Offset: off})
		if err != nil {
			return nil, 0, err
		}
		matching = res.Total
		objs = append(objs, res.Items...)
		if len(res.Items) == 0 || off+len(res.Items) >= res.Total {
			break
		}
	}
	if len(objs) > bound {
		objs = objs[:bound]
	}
	return objs, matching, nil
}

// pageOf returns xs[offset:offset+limit], never nil.
func pageOf[T any](xs []T, offset, limit int) []T {
	if offset >= len(xs) {
		return []T{}
	}
	end := offset + limit
	if end > len(xs) {
		end = len(xs)
	}
	return xs[offset:end]
}
