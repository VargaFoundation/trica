package search_test

import (
	"context"
	"fmt"
	"sort"
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
// runs of each query, so that one slow run on a loaded CI machine does not
// decide.
const (
	latencyRacks   = 50
	latencyPerRack = 40
	latencyBudget  = 200 * time.Millisecond
)

func TestSearchLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("loads 2,000 servers")
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
		for range 5 {
			r, err := sr.Search(ctx, q, search.Page{})
			if err != nil {
				t.Fatalf("%q: %v", q, err)
			}
			res = r
			runs = append(runs, r.Took)
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
		median := runs[len(runs)/2]
		t.Logf("%-62q total %5d fuzzy %-5v median %s", q, res.Total, res.Fuzzy, median.Round(100*time.Microsecond))
		if res.Total == 0 {
			t.Errorf("%q finds nothing: the timing proves nothing", q)
		}
		if median > latencyBudget {
			t.Errorf("%q: median %s over the %s budget", q, median, latencyBudget)
		}
	}
}
