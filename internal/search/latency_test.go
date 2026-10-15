package search_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Acceptance criterion of lot 1: a search answers in under 200 ms on a site
// of 50 racks and 2,000 servers. The site goes through the engine like any
// collector's batches; the threshold is checked on the median of several
// runs of each query, after one warm-up run that is not counted, so that one
// slow run on a loaded CI machine does not decide.
const (
	latencyRacks   = 50
	latencyPerRack = 40
	latencyBudget  = 200 * time.Millisecond
)

// EnvLatencyScale multiplies the budget on a loaded developer machine
// (TRICA_LATENCY_SCALE=2 gives 400 ms). CI must hold the criterion itself, so
// the scale is refused when CI is set (GitHub Actions sets CI=true).
const EnvLatencyScale = "TRICA_LATENCY_SCALE"

// budgetFrom returns the budget for a scale read from the environment.
func budgetFrom(getenv func(string) string) (time.Duration, error) {
	raw := getenv(EnvLatencyScale)
	if raw == "" {
		return latencyBudget, nil
	}
	if getenv("CI") != "" {
		return 0, fmt.Errorf("%s=%s is set on CI: the %s criterion is not scaled there", EnvLatencyScale, raw, latencyBudget)
	}
	scale, err := strconv.ParseFloat(raw, 64)
	if err != nil || scale < 1 {
		return 0, fmt.Errorf("%s=%q: want a number of at least 1", EnvLatencyScale, raw)
	}
	return time.Duration(float64(latencyBudget) * scale), nil
}

func TestLatencyBudgetScale(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want time.Duration
		err  bool
	}{
		{"default", nil, 200 * time.Millisecond, false},
		{"default on CI", map[string]string{"CI": "true"}, 200 * time.Millisecond, false},
		{"scaled locally", map[string]string{EnvLatencyScale: "2"}, 400 * time.Millisecond, false},
		{"refused on CI", map[string]string{EnvLatencyScale: "2", "CI": "true"}, 0, true},
		{"below 1 would tighten silently", map[string]string{EnvLatencyScale: "0.5"}, 0, true},
		{"not a number", map[string]string{EnvLatencyScale: "fast"}, 0, true},
	}
	for _, tc := range cases {
		got, err := budgetFrom(func(k string) string { return tc.env[k] })
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("%s: budget %s, err %v", tc.name, got, err)
		}
	}
}

func TestSearchLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("loads 2,000 servers")
	}
	budget, err := budgetFrom(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	s := testdb.New(t)
	reg := schema.Builtin()
	e := &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }, Now: func() time.Time { return t0 }}
	ctx := context.Background()
	start := time.Now()
	for r := range latencyRacks {
		b := sdk.NewBatch(fmt.Sprintf("redfish:rack%02d", r), sdk.ScopeFull)
		rack := b.Object("rack", "rack").Identify("name", fmt.Sprintf("rack %02d", r))
		_ = rack
		for i := range latencyPerRack {
			n := r*latencyPerRack + i
			d := fmt.Sprintf("d%d", i)
			b.Object(d, "device").Identify("serial", fmt.Sprintf("CZ%06d", n)).Identify("name", fmt.Sprintf("srv-%04d", n)).
				Set("role", "server").Set("vendor", []string{"HPE", "Dell"}[n%2]).
				Set("os_version", fmt.Sprintf("2.%d.%d", n%7, n%13)).
				Set("mgmt_ip", fmt.Sprintf("198.51.%d.%d", 100+n/250, n%250+1)).
				Set("power_w", float64(200+n%300))
			b.Relate("mounted_in", d, "rack")
			for k := range 2 {
				b.Object(fmt.Sprintf("%s-nic%d", d, k), "interface").Identify("name", fmt.Sprintf("eno%d", k+1)).In(d).
					Set("mac", fmt.Sprintf("02:00:%02x:%02x:%02x:%02x", n>>8, n&0xff, k, r))
				b.Object(fmt.Sprintf("%s-disk%d", d, k), "disk").Identify("serial", fmt.Sprintf("S%06d%d", n, k)).In(d).
					Set("media", "ssd").Set("wear_pct", float64((n*7+k*31)%100))
			}
		}
		b.ObservedAt = t0
		if _, err := e.Apply(ctx, b, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Pool.Exec(ctx, "ANALYZE"); err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded %d servers in %s", latencyRacks*latencyPerRack, time.Since(start).Round(time.Second))

	sr := &search.Searcher{Store: s, Now: func() time.Time { return t0 }}
	for _, q := range []string{
		"srv-1234",
		"198.51.104.235",
		"02-00-04-d2-01-1e",
		"CZ001234",
		"srv1234",
		"rack 07",
		"type:device vendor:hpe os_version<2.3 sort:-power_w limit:10",
		`type:disk wear_pct>70 rel.mounted_in:"rack 02"`,
		"type:device mgmt_ip<<198.51.100.0/24",
		"type:device sort:-power_w limit:10",
		"",
	} {
		var runs []time.Duration
		var res *search.Result
		// The first run warms the plan and page caches and is not counted.
		for i := range 6 {
			r, err := sr.Search(ctx, q, search.Page{})
			if err != nil {
				t.Fatalf("%q: %v", q, err)
			}
			res = r
			if i > 0 {
				runs = append(runs, r.Took)
			}
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
		median := runs[len(runs)/2]
		t.Logf("%-62q total %5d fuzzy %-5v median %s", q, res.Total, res.Fuzzy, median.Round(100*time.Microsecond))
		if res.Total == 0 {
			t.Errorf("%q finds nothing: the timing proves nothing", q)
		}
		if median > budget {
			t.Errorf("%q: median %s over the %s budget", q, median, budget)
		}
	}
}
