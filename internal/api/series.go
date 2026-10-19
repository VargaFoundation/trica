package api

import (
	"strconv"
	"time"

	"github.com/VargaFoundation/trica/internal/store"
)

const (
	// DefaultSeriesPoints is how many buckets a series has unless the caller asks.
	DefaultSeriesPoints = 120
	// MaxSeriesPoints bounds the buckets of one series.
	MaxSeriesPoints = 1000
	// maxSeriesSpan bounds the range of one series.
	maxSeriesSpan = 366 * 24 * time.Hour
	// seriesScan bounds the history rows read for one series: a month of
	// readings every five minutes is about 8 600.
	seriesScan = 50000
)

// SeriesPoint is one bucket of a measurement series: what the attribute was
// during [t, t + step). A bucket where it had no value is left out, so a gap
// in the points is a gap in the readings.
type SeriesPoint struct {
	T time.Time `json:"t"`
	// Avg is weighted by how long each value held within the bucket.
	Avg float64 `json:"avg"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Series is a measurement attribute of one object over a range, in buckets.
type Series struct {
	ObjectID    string        `json:"object_id"`
	Key         string        `json:"key"`
	Unit        string        `json:"unit,omitempty"`
	From        time.Time     `json:"from"`
	To          time.Time     `json:"to"`
	StepSeconds int64         `json:"step_seconds"`
	Points      []SeriesPoint `json:"points"`
	// Truncated says that the range held more readings than one answer reads:
	// the end of the range is missing. Ask for a shorter range.
	Truncated bool `json:"truncated"`
}

func (s *Server) hSeries(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	q := c.r.URL.Query()
	key := q.Get("key")
	if key == "" {
		return badRequest("key is required")
	}
	if Sensitive(key) {
		return newErr(403, "sensitive-attribute", "the attribute %q is not exposed", key)
	}
	points := DefaultSeriesPoints
	if v := q.Get("points"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxSeriesPoints {
			return badRequest("points must be an integer between 1 and %d", MaxSeriesPoints)
		}
		points = n
	}
	now := s.now()
	to, err := optTime(c, "to")
	if err != nil {
		return err
	}
	if to == nil {
		to = &now
	}
	from, err := optTime(c, "from")
	if err != nil {
		return err
	}
	if from == nil {
		f := to.Add(-24 * time.Hour)
		from = &f
	}
	if !from.Before(*to) {
		return badRequest("from must be before to")
	}
	if to.Sub(*from) > maxSeriesSpan {
		return badRequest("the range must not exceed %d days", int(maxSeriesSpan/(24*time.Hour)))
	}
	o, _, err := c.loadObject(id)
	if err != nil {
		return err
	}
	unit := ""
	if t, err := s.registry().Type(o.Type); err == nil {
		a := t.Attributes[key]
		if a == nil || !a.Series {
			return newErr(400, "not-a-series", "%s has no measurement %q", o.Type, key)
		}
		unit = a.Unit
	}
	vals, truncated, err := s.st.HistoryBetween(c.ctx(), id, key, *from, *to, seriesScan)
	if err != nil {
		return err
	}
	step, pts := bucket(vals, from.UTC(), to.UTC(), points, now)
	return c.json(200, Series{ObjectID: id, Key: key, Unit: unit, From: from.UTC(), To: to.UTC(),
		StepSeconds: int64(step / time.Second), Points: pts, Truncated: truncated})
}

// bucket cuts [from, to) into n buckets of whole seconds and folds the values
// into them. A value still current holds until now.
func bucket(vals []store.Value, from, to time.Time, n int, now time.Time) (time.Duration, []SeriesPoint) {
	step := to.Sub(from) / time.Duration(n)
	step = step.Truncate(time.Second)
	if step*time.Duration(n) < to.Sub(from) {
		step += time.Second
	}
	type acc struct {
		sum, weight, min, max float64
		seen                  bool
	}
	accs := make([]acc, n)
	for _, v := range vals {
		num := number(v.Value)
		if num == nil {
			continue
		}
		x := *num
		start, end := v.ValidFrom, now
		if v.ValidTo != nil {
			end = *v.ValidTo
		}
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if !start.Before(end) {
			continue
		}
		for i := int(start.Sub(from) / step); i < n; i++ {
			b0 := from.Add(time.Duration(i) * step)
			if !b0.Before(end) {
				break
			}
			lo, hi := maxTime(start, b0), minTime(end, b0.Add(step))
			w := hi.Sub(lo).Seconds()
			if w <= 0 {
				continue
			}
			a := &accs[i]
			if !a.seen || x < a.min {
				a.min = x
			}
			if !a.seen || x > a.max {
				a.max = x
			}
			a.seen = true
			a.sum += x * w
			a.weight += w
		}
	}
	pts := []SeriesPoint{}
	for i, a := range accs {
		if a.seen {
			pts = append(pts, SeriesPoint{T: from.Add(time.Duration(i) * step), Avg: a.sum / a.weight, Min: a.min, Max: a.max})
		}
	}
	return step, pts
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
