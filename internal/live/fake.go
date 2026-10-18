package live

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

//go:embed testdata/*.json
var testdata embed.FS

// FakeSeries is one series of the fake endpoint. Counters are given by their
// rate (per second), gauges by their value: the fake answers rate(m[w]) with
// the rate and m with the value.
type FakeSeries struct {
	Metric string            `json:"metric"`
	Labels map[string]string `json:"labels"`
	Rate   *float64          `json:"rate,omitempty"`
	Value  *float64          `json:"value,omitempty"`
}

// FakePrometheus is a Prometheus HTTP API serving fixed series: the tests and
// the simulated lab use it instead of a real Prometheus. It understands the
// two expression shapes trica sends and nothing else, so an unexpected query
// shape fails loudly.
type FakePrometheus struct {
	// Username and Password, when set, are required as basic authentication.
	Username, Password string

	mu     sync.Mutex
	series []FakeSeries
	// Fail makes every query answer 503.
	Fail atomic.Bool
	// Queries counts the queries received.
	Queries atomic.Int64
	// exprs records the expressions received.
	exprs []string
}

// NewFakePrometheus serves the series of testdata/<fixture>.json.
func NewFakePrometheus(fixture string) (*FakePrometheus, error) {
	raw, err := testdata.ReadFile("testdata/" + fixture + ".json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Series []FakeSeries `json:"series"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", fixture, err)
	}
	return &FakePrometheus{series: doc.Series}, nil
}

// SetSeries replaces the served series.
func (f *FakePrometheus) SetSeries(s []FakeSeries) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.series = s
}

// Expressions returns the expressions received so far.
func (f *FakePrometheus) Expressions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.exprs...)
}

var (
	fakeRateRe = regexp.MustCompile(`^rate\(([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})?\[(\d+)s\]\)$`)
	fakeSelRe  = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})?$`)
	fakeMatch  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)=("(?:[^"\\]|\\.)*")`)
)

func (f *FakePrometheus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.Queries.Add(1)
	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "errorType": "bad_data", "error": msg})
	}
	if !strings.HasSuffix(r.URL.Path, "/api/v1/query") {
		fail(http.StatusNotFound, "not found")
		return
	}
	if f.Username != "" {
		u, p, ok := r.BasicAuth()
		if !ok || u != f.Username || p != f.Password {
			fail(http.StatusUnauthorized, "unauthorized")
			return
		}
	}
	if f.Fail.Load() {
		fail(http.StatusServiceUnavailable, "unavailable")
		return
	}
	expr := r.FormValue("query")
	f.mu.Lock()
	f.exprs = append(f.exprs, expr)
	series := f.series
	f.mu.Unlock()

	var metric, matchers string
	rate := false
	if m := fakeRateRe.FindStringSubmatch(expr); m != nil {
		metric, matchers, rate = m[1], m[2], true
	} else if m := fakeSelRe.FindStringSubmatch(expr); m != nil {
		metric, matchers = m[1], m[2]
	} else {
		fail(http.StatusBadRequest, "unsupported expression")
		return
	}
	want := map[string]string{}
	for _, m := range fakeMatch.FindAllStringSubmatch(matchers, -1) {
		v, err := strconv.Unquote(m[2])
		if err != nil {
			fail(http.StatusBadRequest, "bad matcher")
			return
		}
		want[m[1]] = v
	}
	result := []map[string]any{}
	for _, s := range series {
		if s.Metric != metric {
			continue
		}
		ok := true
		for k, v := range want {
			if s.Labels[k] != v {
				ok = false
			}
		}
		val := s.Value
		if rate {
			val = s.Rate
		}
		if !ok || val == nil {
			continue
		}
		labels := map[string]string{}
		for k, v := range s.Labels {
			labels[k] = v
		}
		if !rate {
			labels["__name__"] = metric
		}
		result = append(result, map[string]any{"metric": labels, "value": []any{1.7e9, strconv.FormatFloat(*val, 'g', -1, 64)}})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": result}})
}
