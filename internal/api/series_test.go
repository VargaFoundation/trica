package api_test

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func at(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

func held(v any, from, to float64) store.Value {
	val := store.Value{Key: "power_w", Value: v, ValidFrom: at(from)}
	if to >= 0 {
		end := at(to)
		val.ValidTo = &end
	}
	return val
}

func TestBucketWeighsValuesByHowLongTheyHeld(t *testing.T) {
	vals := []store.Value{
		held(100.0, -5, 1),     // before the range: counted from its start
		held(400.0, 1, 1.25),   // a quarter of the second bucket
		held(200.0, 1.25, 3),   // then 200 until the third bucket ends
		held("n/a", 3, 3.5),    // not a number: no reading
		held(int64(50), 5, -1), // still current: holds until now, inside the last bucket
	}
	step, pts := api.Bucket(vals, at(0), at(6), 6, at(5.5))
	if step != time.Hour {
		t.Fatalf("step = %s", step)
	}
	want := []api.SeriesPoint{
		{T: at(0), Avg: 100, Min: 100, Max: 100},
		{T: at(1), Avg: 250, Min: 200, Max: 400},
		{T: at(2), Avg: 200, Min: 200, Max: 200},
		// 3 to 4 and 4 to 5: no reading, no point.
		{T: at(5), Avg: 50, Min: 50, Max: 50},
	}
	if len(pts) != len(want) {
		t.Fatalf("points: %+v", pts)
	}
	for i, p := range pts {
		w := want[i]
		if !p.T.Equal(w.T) || math.Abs(p.Avg-w.Avg) > 1e-9 || p.Min != w.Min || p.Max != w.Max {
			t.Errorf("point %d = %+v, want %+v", i, p, w)
		}
	}
}

func TestBucketStepCoversTheRange(t *testing.T) {
	// 7 days in 120 buckets is 5040 s exactly; 1 hour in 7 is not whole seconds.
	if step, _ := api.Bucket(nil, at(0), at(168), 120, at(168)); step != 5040*time.Second {
		t.Errorf("week step = %s", step)
	}
	step, pts := api.Bucket(nil, at(0), at(1), 7, at(1))
	if step*7 < time.Hour || step%time.Second != 0 {
		t.Errorf("hour step = %s", step)
	}
	if len(pts) != 0 {
		t.Errorf("no value, points %+v", pts)
	}
}

func TestSeries(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.fixtures()
	sw := e.ids["sw"]
	reading := func(w float64) {
		b := sdk.NewBatch("redfish:sw-a1", sdk.ScopePartial)
		b.Object("sw", "device").Identify("serial", "JPE-SW-A1").Set("power_w", w)
		e.apply(b)
	}
	start := e.clk.now()
	reading(100)
	e.clk.advance(time.Hour)
	reading(300)
	e.clk.advance(time.Hour)

	var s api.Series
	r := e.get("/api/v1/objects/"+sw+"/series?key=power_w&points=4&from="+start.Format(time.RFC3339)+"&to="+start.Add(4*time.Hour).Format(time.RFC3339), tokReader)
	wantStatus(t, r, 200)
	r.json(t, &s)
	if s.Unit != "W" || s.StepSeconds != 3600 || s.Truncated || len(s.Points) != 2 {
		t.Fatalf("series: %s", r.Body)
	}
	if s.Points[0].Avg != 100 || s.Points[1].Avg != 300 || !s.Points[1].T.Equal(start.Add(time.Hour)) {
		t.Fatalf("points: %+v", s.Points)
	}

	// By default: the last 24 hours, 120 buckets.
	r = e.get("/api/v1/objects/"+sw+"/series?key=power_w", tokReader)
	wantStatus(t, r, 200)
	r.json(t, &s)
	if !s.To.Equal(e.clk.now()) || s.To.Sub(s.From) != 24*time.Hour || s.StepSeconds != 720 || len(s.Points) != 10 {
		t.Fatalf("default series: from %s to %s step %d, %d points", s.From, s.To, s.StepSeconds, len(s.Points))
	}

	for _, c := range []struct {
		query  string
		status int
	}{
		{"", 400},
		{"?key=os_version", 400},
		{"?key=no_such", 400},
		{"?key=power_w&points=0", 400},
		{"?key=power_w&points=1001", 400},
		{"?key=power_w&from=2026-03-02&to=2026-03-01", 400},
		{"?key=power_w&from=2025-01-01&to=2026-03-01", 400},
		{"?key=power_w&from=yesterday", 400},
		{"?key=bmc_password", 403},
	} {
		if r := e.get("/api/v1/objects/"+sw+"/series"+c.query, tokReader); r.Status != c.status {
			t.Errorf("%s: %d, want %d: %s", c.query, r.Status, c.status, r.Body)
		}
	}
	wantStatus(t, e.get("/api/v1/objects/0a000000-0000-4000-8000-00000000ffff/series?key=power_w", tokReader), 404)
}

func TestSeriesKeys(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.fixtures()
	sw := e.ids["sw"]
	start := e.clk.now()
	b := sdk.NewBatch("redfish:sw-a1", sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1").Set("power_w", 100.0).Set("inlet_temp_c", 20.0)
	e.apply(b)
	e.clk.advance(time.Hour)
	b = sdk.NewBatch("redfish:sw-a1", sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1").Unset("power_w")
	e.apply(b)
	e.clk.advance(time.Hour)

	// A real withdrawal: the object no longer holds power_w.
	var o struct {
		Attributes map[string]any `json:"attributes"`
	}
	r := e.get("/api/v1/objects/"+sw, tokReader)
	wantStatus(t, r, 200)
	r.json(t, &o)
	if _, ok := o.Attributes["power_w"]; ok {
		t.Fatalf("power_w is still held: %s", r.Body)
	}

	keys := func(query string) []string {
		t.Helper()
		var k api.SeriesKeys
		r := e.get("/api/v1/objects/"+sw+"/series/keys"+query, tokReader)
		wantStatus(t, r, 200)
		r.json(t, &k)
		if k.Keys == nil {
			t.Fatalf("keys is not an array: %s", r.Body)
		}
		return k.Keys
	}
	full := "?from=" + start.Format(time.RFC3339) + "&to=" + e.clk.now().Format(time.RFC3339)
	// outlet_temp_c never had a row; os_version has history but is not a series.
	if got := keys(full); !slices.Equal(got, []string{"inlet_temp_c", "power_w"}) {
		t.Errorf("whole range: %v", got)
	}
	// The closed row of power_w ended an hour ago.
	if got := keys("?from=" + e.clk.now().Add(-30*time.Minute).Format(time.RFC3339)); !slices.Equal(got, []string{"inlet_temp_c"}) {
		t.Errorf("last half hour: %v", got)
	}
	if got := keys("?from=" + start.Add(-48*time.Hour).Format(time.RFC3339) + "&to=" + start.Add(-24*time.Hour).Format(time.RFC3339)); len(got) != 0 {
		t.Errorf("before any reading: %v", got)
	}
	var k api.SeriesKeys
	r = e.get("/api/v1/objects/"+sw+"/series/keys", tokReader)
	wantStatus(t, r, 200)
	r.json(t, &k)
	if !k.To.Equal(e.clk.now()) || k.To.Sub(k.From) != 24*time.Hour || !slices.Equal(k.Keys, []string{"inlet_temp_c", "power_w"}) {
		t.Errorf("default range: %+v", k)
	}

	for _, q := range []string{"?from=2026-03-02&to=2026-03-01", "?from=2025-01-01&to=2026-03-01", "?from=yesterday"} {
		if r := e.get("/api/v1/objects/"+sw+"/series/keys"+q, tokReader); r.Status != 400 {
			t.Errorf("%s: %d: %s", q, r.Status, r.Body)
		}
	}
	wantStatus(t, e.get("/api/v1/objects/0a000000-0000-4000-8000-00000000ffff/series/keys", tokReader), 404)
	r = e.get("/api/v1/objects/"+e.ids["rack-p"]+"/series/keys", tokReader)
	wantStatus(t, r, 200)
	if !strings.Contains(string(r.Body), `"keys":[]`) {
		t.Errorf("a rack has no measurement: %s", r.Body)
	}
}

func TestSeriesAttributes(t *testing.T) {
	typ := &schema.Type{Attributes: map[string]*schema.Attribute{
		"power_w":     {Kind: schema.Float, Series: true},
		"uptime_s":    {Kind: schema.Int},
		"token_level": {Kind: schema.Float, Series: true},
	}}
	if got := api.SeriesAttributes(typ); !slices.Equal(got, []string{"power_w"}) {
		t.Errorf("series attributes = %v", got)
	}
}
