package unifi

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// metricsNow is two minutes after the last recorded 5-minute period (09:00Z),
// so the whole recording is inside the 30-minute window's reach.
var metricsNow = time.Date(2026, 10, 23, 9, 2, 0, 0, time.UTC)

const (
	recHost = "000000000000000000000000000000000000000000000000000000000001:1234567890"
	recSite = "000000000000000000000001"
)

func recordedMetrics(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + SetRecorded + "/v1/isp-metrics/5m.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wanOf(t *testing.T, b *sdk.Batch, which string) *sdk.Object {
	t.Helper()
	return obj(t, b, "internet_access", "name", "office/office-a/"+which)
}

var seriesKeys = []string{"latency_ms", "latency_max_ms", "loss_pct", "uptime_pct"}

func warned(b *sdk.Batch, sub string) bool {
	return strings.Contains(strings.Join(b.Health.Warnings, "|"), sub)
}

// Each run takes the newest period within the window and never one after its
// own clock.
func TestISPMetricsNewestPeriod(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 23, h, m, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name string
		now  time.Time
		want map[string]any // nil: no series
	}{
		{"08:40 is the newest at 08:42", at(8, 42), map[string]any{"latency_ms": 1.0, "latency_max_ms": 2.0, "loss_pct": 0.0, "uptime_pct": 100.0}},
		{"08:45 is the newest at 08:47", at(8, 47), map[string]any{"latency_ms": 2.0, "latency_max_ms": 3.0}},
		{"nothing in the last 30 minutes at 10:00", at(10, 0), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			b := runAt(t, f, f.Target(office), tc.now)
			wan := wanOf(t, b, "WAN")
			if tc.want == nil {
				absent(t, wan, seriesKeys...)
				if !warned(b, "no ISP metrics for console Office A in the last 30 minutes") {
					t.Errorf("no warning in %v", b.Health.Warnings)
				}
				if b.Scope != sdk.ScopeFull || !b.Health.OK {
					t.Errorf("scope %s, health %+v: stale metrics must not degrade the run", b.Scope, b.Health)
				}
				return
			}
			attrs(t, wan, tc.want)
			if len(b.Health.Warnings) != 0 {
				t.Errorf("warnings %v", b.Health.Warnings)
			}
		})
	}
}

// A server that ignores the window and returns every period must not make the
// collector use one that has not happened yet.
func TestISPMetricsIgnoresPeriodsAfterNow(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.SetBody("/v1/isp-metrics/5m", recordedMetrics(t))
	b := runAt(t, f, f.Target(office), time.Date(2026, 10, 23, 8, 42, 0, 0, time.UTC))
	attrs(t, wanOf(t, b, "WAN"), map[string]any{"latency_ms": 1.0, "latency_max_ms": 2.0})
}

func TestISPMetricsRequestIsBounded(t *testing.T) {
	f := newFake(t, SetRecorded)
	runAt(t, f, f.Target(office), metricsNow)
	var asked []url.Values
	for _, r := range f.Requests() {
		if r.Path == epISPMetrics {
			q, err := url.ParseQuery(r.Query)
			if err != nil {
				t.Fatal(err)
			}
			asked = append(asked, q)
		}
	}
	if len(asked) != 1 {
		t.Fatalf("%d metrics requests, want exactly 1", len(asked))
	}
	q := asked[0]
	if len(q) != 2 {
		t.Errorf("query %v: want beginTimestamp and endTimestamp only (no duration)", q)
	}
	begin, err1 := time.Parse(time.RFC3339, q.Get("beginTimestamp"))
	end, err2 := time.Parse(time.RFC3339, q.Get("endTimestamp"))
	if err1 != nil || err2 != nil {
		t.Fatalf("timestamps %q, %q", q.Get("beginTimestamp"), q.Get("endTimestamp"))
	}
	if !end.Equal(metricsNow) || end.Sub(begin) != 30*time.Minute {
		t.Errorf("window %v to %v, want the 30 minutes before %v", begin, end, metricsNow)
	}
	if !strings.HasSuffix(q.Get("endTimestamp"), "Z") {
		t.Errorf("end %q is not UTC", q.Get("endTimestamp"))
	}
}

func up(v bool) *flag { f := flag(v); return &f }

func TestPrimaryWAN(t *testing.T) {
	mk := func(mode string, wans map[string]wanStat) *site {
		s := &site{}
		s.Statistics.WANLoadBalancingMode = str(mode)
		s.Statistics.WANs = wans
		return s
	}
	for _, tc := range []struct {
		name string
		st   *site
		want string
		why  string
	}{
		{"priority 1 beats 2", mk("FAILOVER_ONLY", map[string]wanStat{"WAN": {PortUp: up(true), FailoverPriority: "1"}, "WAN2": {PortUp: up(true), FailoverPriority: "2"}}), "WAN", ""},
		{"priority 1 beats 2 whatever the name", mk("FAILOVER_ONLY", map[string]wanStat{"WAN": {PortUp: up(true), FailoverPriority: "2"}, "WAN2": {PortUp: up(true), FailoverPriority: "1"}}), "WAN2", ""},
		{"a WAN that is down is skipped", mk("FAILOVER_ONLY", map[string]wanStat{"WAN": {PortUp: up(false), FailoverPriority: "1"}, "WAN2": {PortUp: up(true), FailoverPriority: "2"}}), "WAN2", ""},
		{"a missing priority sorts last", mk("FAILOVER_ONLY", map[string]wanStat{"WAN": {PortUp: up(true)}, "WAN2": {PortUp: up(true), FailoverPriority: "2"}}), "WAN2", ""},
		{"a tie goes by name", mk("FAILOVER_ONLY", map[string]wanStat{"WAN2": {PortUp: up(true), FailoverPriority: "1"}, "WAN": {PortUp: up(true), FailoverPriority: "1"}}), "WAN", ""},
		{"one WAN up under load balancing", mk("WEIGHTED", map[string]wanStat{"WAN": {PortUp: up(true), FailoverPriority: "1"}, "WAN2": {PortUp: up(false)}}), "WAN", ""},
		{"two WANs up under load balancing", mk("WEIGHTED", map[string]wanStat{"WAN": {PortUp: up(true), FailoverPriority: "1"}, "WAN2": {PortUp: up(true), FailoverPriority: "2"}}), "", "load balancing mode"},
		{"two WANs up and no mode", mk("", map[string]wanStat{"WAN": {PortUp: up(true), FailoverPriority: "1"}, "WAN2": {PortUp: up(true), FailoverPriority: "2"}}), "", "load balancing mode"},
		{"none up", mk("FAILOVER_ONLY", map[string]wanStat{"WAN": {PortUp: up(false)}, "WAN2": {}}), "", "no WAN is up"},
		{"no wans", mk("FAILOVER_ONLY", nil), "", "no WAN is up"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := primaryWAN(tc.st)
			if got != tc.want || !strings.Contains(why, tc.why) {
				t.Errorf("got %q (%q), want %q (%q)", got, why, tc.want, tc.why)
			}
		})
	}
}

// swapPorts returns the recorded sites with the ports' state exchanged: WAN is
// down and WAN2 up.
func swapPorts(t *testing.T) string {
	t.Helper()
	b, err := fixtures.ReadFile("testdata/" + SetRecorded + "/v1/sites.json")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Count(s, `"portUp": true`) != 1 || strings.Count(s, `"portUp": false`) != 1 {
		t.Fatal("the recorded sites changed shape")
	}
	s = strings.Replace(s, `"portUp": true`, `"portUp": "tmp"`, 1)
	s = strings.Replace(s, `"portUp": false`, `"portUp": true`, 1)
	return strings.Replace(s, `"portUp": "tmp"`, `"portUp": false`, 1)
}

// After a failover the series follows the active WAN and the other one's
// stops: the full batch withdraws it.
func TestISPMetricsFollowFailover(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.SetBody("/v1/sites", swapPorts(t))
	b := runAt(t, f, f.Target(office), metricsNow)
	attrs(t, wanOf(t, b, "WAN2"), map[string]any{"latency_ms": 2.0, "uptime_pct": 100.0, "status": "online"})
	absent(t, wanOf(t, b, "WAN"), seriesKeys...)
}

func TestISPMetricsNotAttributed(t *testing.T) {
	t.Run("provider mismatch", func(t *testing.T) {
		f := newFake(t, SetRecorded)
		f.SetBody("/v1/isp-metrics/5m", strings.ReplaceAll(recordedMetrics(t), "Example ISP", "Another ISP"))
		b := runAt(t, f, f.Target(office), metricsNow)
		absent(t, wanOf(t, b, "WAN"), seriesKeys...)
		if !warned(b, "not attributed: they are for provider") {
			t.Errorf("warnings %v", b.Health.Warnings)
		}
	})
	t.Run("load balancing with two WANs up", func(t *testing.T) {
		f := newFake(t, SetRecorded)
		s := strings.Replace(swapPorts(t), `"portUp": false`, `"portUp": true`, 1)
		f.SetBody("/v1/sites", strings.Replace(s, "FAILOVER_ONLY", "WEIGHTED", 1))
		b := runAt(t, f, f.Target(office), metricsNow)
		absent(t, wanOf(t, b, "WAN"), seriesKeys...)
		absent(t, wanOf(t, b, "WAN2"), seriesKeys...)
		if !warned(b, "load balancing mode") {
			t.Errorf("warnings %v", b.Health.Warnings)
		}
	})
	t.Run("no WAN up", func(t *testing.T) {
		f := newFake(t, SetRecorded)
		f.SetBody("/v1/sites", strings.ReplaceAll(strings.Replace(swapPorts(t), `"portUp": true`, `"portUp": false`, 1), `"portUp": true`, `"portUp": false`))
		b := runAt(t, f, f.Target(office), metricsNow)
		absent(t, wanOf(t, b, "WAN"), seriesKeys...)
		if !warned(b, "no WAN is up") {
			t.Errorf("warnings %v", b.Health.Warnings)
		}
	})
	t.Run("a site the statistics do not know", func(t *testing.T) {
		f := newFake(t, SetRecorded)
		f.SetBody("/v1/isp-metrics/5m", strings.ReplaceAll(recordedMetrics(t), `"siteId": "`+recSite+`"`, `"siteId": "000000000000000000000099"`))
		b := runAt(t, f, f.Target(office), metricsNow)
		absent(t, wanOf(t, b, "WAN"), seriesKeys...)
		if !warned(b, "no site statistics for site") {
			t.Errorf("warnings %v", b.Health.Warnings)
		}
	})
	t.Run("a host that is not read is skipped silently", func(t *testing.T) {
		f := newFake(t, SetRecorded)
		f.SetBody("/v1/isp-metrics/5m", strings.ReplaceAll(recordedMetrics(t), recHost, "host-elsewhere:9"))
		b := runAt(t, f, f.Target(office), metricsNow)
		absent(t, wanOf(t, b, "WAN"), seriesKeys...)
		if len(b.Health.Warnings) != 0 {
			t.Errorf("warnings %v", b.Health.Warnings)
		}
	})
}

func TestSetMeasurements(t *testing.T) {
	n := func(v float64) num { return num{V: v, OK: true} }
	for _, tc := range []struct {
		name    string
		w       ispWAN
		want    map[string]any
		dropped []string
	}{
		{"all set", ispWAN{AvgLatency: n(3), MaxLatency: n(9), PacketLoss: n(0.5), Uptime: n(100)}, map[string]any{"latency_ms": 3.0, "latency_max_ms": 9.0, "loss_pct": 0.5, "uptime_pct": 100.0}, nil},
		{"not a number: left out silently", ispWAN{AvgLatency: num{}, MaxLatency: n(9), PacketLoss: num{}, Uptime: n(99)}, map[string]any{"latency_max_ms": 9.0, "uptime_pct": 99.0}, nil},
		{"negative", ispWAN{AvgLatency: n(-1), MaxLatency: n(9), PacketLoss: n(-0.1), Uptime: n(100)}, map[string]any{"latency_max_ms": 9.0, "uptime_pct": 100.0}, []string{"latency_ms", "loss_pct"}},
		{"percentage above 100", ispWAN{AvgLatency: n(500), MaxLatency: n(900), PacketLoss: n(101), Uptime: n(100.5)}, map[string]any{"latency_ms": 500.0, "latency_max_ms": 900.0}, []string{"loss_pct", "uptime_pct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := sdk.NewBatch("x", sdk.ScopeFull).Object("a", "internet_access")
			dropped := setMeasurements(o, &tc.w)
			if len(o.Attributes) != len(tc.want) {
				t.Errorf("attributes %v, want %v", o.Attributes, tc.want)
			}
			attrs(t, o, tc.want)
			if strings.Join(dropped, ",") != strings.Join(tc.dropped, ",") {
				t.Errorf("dropped %v, want %v", dropped, tc.dropped)
			}
		})
	}
}

// What the collector sets must be what the schema draws as curves: a reading
// stored as a plain attribute has no history to chart.
func TestISPMetricsAreSeriesInTheSchema(t *testing.T) {
	ia := schema.Builtin().Types["internet_access"]
	o := sdk.NewBatch("x", sdk.ScopeFull).Object("a", "internet_access")
	setMeasurements(o, &ispWAN{AvgLatency: num{1, true}, MaxLatency: num{2, true}, PacketLoss: num{0, true}, Uptime: num{100, true}})
	if len(o.Attributes) != len(seriesKeys) {
		t.Fatalf("attributes %v", o.Attributes)
	}
	for k := range o.Attributes {
		if a, ok := ia.Attributes[k]; !ok || !a.Series {
			t.Errorf("internet_access.%s is set by the collector but is not a series in the schema", k)
		}
	}
}

func TestNumDecoding(t *testing.T) {
	for in, want := range map[string]num{
		`2`: {2, true}, `0.25`: {0.25, true}, `"41"`: {41, true}, `"7.5"`: {7.5, true},
		`null`: {}, `"x"`: {}, `""`: {}, `{}`: {}, `[1]`: {}, `true`: {}, `"NaN"`: {}, `"Inf"`: {},
	} {
		var n num
		if err := n.UnmarshalJSON([]byte(in)); err != nil || n != want {
			t.Errorf("%s: %+v, %v; want %+v", in, n, err, want)
		}
	}
}

// Values that are null, text, or out of range never reach the batch; the
// others do. The period at 08:55 has no data and the one after now is ignored.
func TestISPMetricsBadValues(t *testing.T) {
	f := newFake(t, SetSynthesised)
	b := runAt(t, f, f.Target(office), time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC))
	wan := obj(t, b, "internet_access", "name", "office/office-a/WAN")
	attrs(t, wan, map[string]any{"latency_max_ms": 41.0, "uptime_pct": 99.9})
	absent(t, wan, "latency_ms", "loss_pct")
	absent(t, obj(t, b, "internet_access", "name", "office/office-a/WAN2"), seriesKeys...)
	// The disconnected console's last period is hours old.
	absent(t, obj(t, b, "internet_access", "name", "office/office-b/WAN"), seriesKeys...)
	if !warned(b, "no ISP metrics for console Office B") {
		t.Errorf("warnings %v", b.Health.Warnings)
	}

	f.SetBody("/v1/isp-metrics/5m", `{"data":[{"hostId":"host-office-a:1","siteId":"000000000000000000000011","periods":[
	  {"metricTime":"2026-10-23T08:55:00Z","data":{"wan":{"avgLatency":-4,"maxLatency":5,"packetLoss":150,"uptime":100}}}]}]}`)
	b = runAt(t, f, f.Target(office), time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC))
	wan = obj(t, b, "internet_access", "name", "office/office-a/WAN")
	attrs(t, wan, map[string]any{"latency_max_ms": 5.0, "uptime_pct": 100.0})
	absent(t, wan, "latency_ms", "loss_pct")
	for _, k := range []string{"latency_ms", "loss_pct"} {
		if !warned(b, k+" is out of range") {
			t.Errorf("no out-of-range warning for %s in %v", k, b.Health.Warnings)
		}
	}
}

// A metrics call that fails is a warning: the scope stays full, so absence
// detection keeps working for every device, and the health stays OK.
func TestISPMetricsFailureKeepsTheRunFull(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     func(*FakeSiteManager)
		timeout time.Duration
		want    string
	}{
		{"500", func(f *FakeSiteManager) { f.Fail(epISPMetrics, 500) }, 0, "ISP metrics: GET /v1/isp-metrics/5m: HTTP 500"},
		{"429", func(f *FakeSiteManager) { f.Fail(epISPMetrics, 429) }, 0, "rate limited (HTTP 429), retry after 30"},
		{"400", func(f *FakeSiteManager) { f.Fail(epISPMetrics, 400) }, 0, "HTTP 400"},
		{"403", func(f *FakeSiteManager) { f.Fail(epISPMetrics, 403) }, 0, "forbidden (HTTP 403)"},
		{"malformed", func(f *FakeSiteManager) { f.Malformed(epISPMetrics) }, 0, "malformed response"},
		{"too slow", func(f *FakeSiteManager) { f.Slow(epISPMetrics, 3*time.Second) }, 400 * time.Millisecond, "transport error"},
		{"data of the wrong shape", func(f *FakeSiteManager) { f.SetBody(epISPMetrics, `{"data":{"not":"a list"}}`) }, 0, "ISP metrics:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			tc.set(f)
			tg := f.Target(office)
			if tc.timeout > 0 {
				tg.Timeout = tc.timeout
			}
			b := runAt(t, f, tg, metricsNow)
			if b.Scope != sdk.ScopeFull || !b.Health.OK || b.Health.Error != "" {
				t.Fatalf("scope %s, health %+v: a failing metrics call must not degrade the run", b.Scope, b.Health)
			}
			if !warned(b, "ISP metrics: ") || !warned(b, tc.want) {
				t.Errorf("warnings %v, want one with %q", b.Health.Warnings, tc.want)
			}
			if count(b, "device") != 10 || count(b, "internet_access") != 2 {
				t.Errorf("%d devices, %d internet accesses", count(b, "device"), count(b, "internet_access"))
			}
			for _, o := range collecttest.Objects([]*sdk.Batch{b}, "internet_access") {
				absent(t, o, seriesKeys...)
			}
		})
	}
}

func TestISPMetricsOption(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runAt(t, f, withOptions(t, f.Target(office), "isp_metrics: false\n"), metricsNow)
	for _, r := range f.Requests() {
		if strings.Contains(r.Path, "isp-metrics") {
			t.Errorf("a metrics request was made: %+v", r)
		}
	}
	if len(f.Requests()) != 3 {
		t.Errorf("%d requests, want 3", len(f.Requests()))
	}
	absent(t, wanOf(t, b, "WAN"), seriesKeys...)
	if len(b.Health.Warnings) != 0 {
		t.Errorf("warnings %v", b.Health.Warnings)
	}
	// The default, and an explicit true, ask.
	for _, doc := range []string{"", "isp_metrics: true\n"} {
		f := newFake(t, SetRecorded)
		tg := f.Target(office)
		if doc != "" {
			tg = withOptions(t, tg, doc)
		}
		runAt(t, f, tg, metricsNow)
		if len(f.Requests()) != 4 {
			t.Errorf("options %q: %d requests, want 4", doc, len(f.Requests()))
		}
	}
}

// With no site statistics there is nothing to map a period to a WAN, and with
// no console selected there is nobody to ask for: no call is made.
func TestISPMetricsNotAsked(t *testing.T) {
	asked := func(f *FakeSiteManager) bool {
		for _, r := range f.Requests() {
			if r.Path == epISPMetrics {
				return true
			}
		}
		return false
	}
	f := newFake(t, SetRecorded)
	f.Fail("/v1/sites", 500)
	runAt(t, f, f.Target(office), metricsNow)
	if asked(f) {
		t.Error("the metrics were asked while /v1/sites failed")
	}
	f = newFake(t, SetRecorded)
	f.SetBody("/v1/hosts", `{"data":[]}`)
	runAt(t, f, f.Target(office), metricsNow)
	if asked(f) {
		t.Error("the metrics were asked with no console")
	}
}

func fakeGet(t *testing.T, f *FakeSiteManager, pathQuery string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.URL()+pathQuery, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-KEY", APIKey)
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func periodTimes(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	rest := body
	for {
		_, after, ok := strings.Cut(rest, `"metricTime":"`)
		if !ok {
			return out
		}
		v, next, _ := strings.Cut(after, `"`)
		out = append(out, v)
		rest = next
	}
}

func TestFakeISPMetrics(t *testing.T) {
	f := newFake(t, SetRecorded)
	rec := func(h, m int) string { return time.Date(2026, 10, 23, h, m, 0, 0, time.UTC).Format(time.RFC3339) }

	code, body := fakeGet(t, f, "/v1/isp-metrics/5m?beginTimestamp="+rec(8, 0)+"&endTimestamp="+rec(9, 0))
	if got := periodTimes(t, body); code != 200 || len(got) != 13 || got[0] != rec(8, 0) || got[12] != rec(9, 0) {
		t.Errorf("08:00-09:00: %d, %v (inclusive at both ends: 13 periods)", code, got)
	}
	_, body = fakeGet(t, f, "/v1/isp-metrics/5m?beginTimestamp="+rec(8, 30)+"&endTimestamp="+rec(8, 40))
	if got := periodTimes(t, body); len(got) != 3 {
		t.Errorf("08:30-08:40: %v", got)
	}
	_, body = fakeGet(t, f, "/v1/isp-metrics/5m")
	if got := periodTimes(t, body); len(got) != 13 {
		t.Errorf("no bounds: %d periods, want all 13", len(got))
	}
	_, body = fakeGet(t, f, "/v1/isp-metrics/5m?endTimestamp="+rec(8, 5))
	if got := periodTimes(t, body); len(got) != 2 {
		t.Errorf("only an end: %v", got)
	}
	_, body = fakeGet(t, f, "/v1/isp-metrics/1h?beginTimestamp="+rec(0, 0)+"&endTimestamp="+rec(8, 0))
	if got := periodTimes(t, body); len(got) == 0 || !strings.Contains(body, `"metricType":"1h"`) {
		t.Errorf("1h: %v", got)
	}

	// The 5m type only takes the 24h duration: the recorded refusal.
	want, err := os.ReadFile("testdata/" + SetRecorded + "/errors/400-isp-metrics-duration.json")
	if err != nil {
		t.Fatal(err)
	}
	if code, body = fakeGet(t, f, "/v1/isp-metrics/5m?duration=1h"); code != 400 || body != string(want) {
		t.Errorf("duration=1h: %d %s", code, body)
	}
	if code, _ = fakeGet(t, f, "/v1/isp-metrics/5m?duration=24h"); code != 200 {
		t.Errorf("duration=24h: %d", code)
	}
	if code, body = fakeGet(t, f, "/v1/isp-metrics/15m"); code != 400 || !strings.Contains(body, "parameter_invalid") {
		t.Errorf("15m: %d %s", code, body)
	}
	if code, _ = fakeGet(t, f, "/v1/isp-metrics/5m?beginTimestamp=yesterday"); code != 400 {
		t.Errorf("bad begin: %d", code)
	}
	if code, _ = fakeGet(t, f, "/v1/isp-metrics/5m?endTimestamp=2026-10-23"); code != 400 {
		t.Errorf("bad end: %d", code)
	}
	for _, p := range []string{"/v1/isp-metrics", "/v1/isp-metrics/5m/query", "/v1/isp-metrics/"} {
		if code, _ = fakeGet(t, f, p); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}

	// Replay: the last recorded period moves to the given time, truncated.
	f.ReplayMetricsUntil(time.Date(2026, 10, 23, 12, 7, 33, 0, time.UTC))
	_, body = fakeGet(t, f, "/v1/isp-metrics/5m?beginTimestamp="+rec(11, 55)+"&endTimestamp="+rec(12, 10))
	if got := periodTimes(t, body); len(got) != 3 || got[0] != rec(11, 55) || got[2] != rec(12, 5) {
		t.Errorf("replayed: %v", got)
	}
	f.ReplayMetricsUntil(time.Time{})
	_, body = fakeGet(t, f, "/v1/isp-metrics/5m?beginTimestamp="+rec(11, 55)+"&endTimestamp="+rec(12, 10))
	if got := periodTimes(t, body); len(got) != 0 {
		t.Errorf("after the replay was cleared: %v", got)
	}
}
