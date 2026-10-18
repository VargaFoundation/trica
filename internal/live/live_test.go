package live

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/store"
)

// fakeInv is an inventory in memory. Everything is invented.
type fakeInv struct {
	mu   sync.Mutex
	objs []*store.Object
	rels []store.Relation
	err  error
}

func (f *fakeInv) LiveObjects(context.Context) ([]*store.Object, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objs, f.err
}

func (f *fakeInv) LiveRelations(context.Context) ([]store.Relation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rels, f.err
}

func (f *fakeInv) add(id, typ, parent string, attrs map[string]any) {
	o := &store.Object{ID: id, Type: typ, Status: "active", Attrs: attrs}
	if parent != "" {
		o.ParentID = &parent
	}
	f.objs = append(f.objs, o)
}

// lab is the inventory the lab.json series describe.
func lab() *fakeInv {
	f := &fakeInv{}
	f.add("sw", "device", "", map[string]any{"name": "sw-a1", "mgmt_ip": "192.0.2.11"})
	f.add("sw-lc1", "module", "sw", map[string]any{"name": "linecard1"})
	f.add("sw-e1", "interface", "sw", map[string]any{"name": "Ethernet1", "speed_mbps": float64(10000)})
	f.add("sw-e2", "interface", "sw-lc1", map[string]any{"name": "Ethernet2"})
	f.add("sw-e3", "interface", "sw", map[string]any{"name": "Ethernet3", "speed_mbps": float64(1000)})
	f.add("sw-e4", "interface", "sw", map[string]any{"name": "Ethernet4", "speed_mbps": float64(1000)})
	f.add("sw-po10", "interface", "sw", map[string]any{"name": "Port-Channel10", "kind": "lag"})
	f.add("rtr", "device", "", map[string]any{"name": "rtr-01", "mgmt_ip": "192.0.2.21"})
	f.add("rtr-e1", "interface", "rtr", map[string]any{"name": "ether1", "speed_mbps": float64(1000)})
	f.add("fw", "device", "", map[string]any{"name": "fw-01"})
	f.add("fw-igb0", "interface", "fw", map[string]any{"name": "igb0"})
	f.add("srv", "device", "", map[string]any{"name": "srv-01", "primary_ip": "192.0.2.31"})
	f.add("srv-eno1", "interface", "srv", map[string]any{"name": "eno1", "speed_mbps": float64(1000)})
	f.add("srv-lo", "interface", "srv", map[string]any{"name": "lo"})
	f.add("gone", "device", "", map[string]any{"name": "gone", "mgmt_ip": "192.0.2.99"})
	f.objs[len(f.objs)-1].Status = "absent"
	f.add("gone-e1", "interface", "gone", map[string]any{"name": "Ethernet1"})
	f.rels = []store.Relation{
		{Type: "member_of", FromID: "sw-e3", ToID: "sw-po10"},
		{Type: "member_of", FromID: "sw-e4", ToID: "sw-po10"},
		{Type: "member_of", FromID: "sw-e1", ToID: "vlan-10"},
	}
	return f
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func labConfig(url string) *Config {
	return &Config{URL: url, Sources: []Source{
		{Kind: KindArista, Matchers: map[string]string{"job": "arista"}},
		{Kind: KindMikrotik},
		{Kind: KindPfsense},
		{Kind: KindNode},
	}}
}

type rig struct {
	fake *FakePrometheus
	svc  *Service
	clk  *clock
	inv  *fakeInv
	reg  *prometheus.Registry
}

func newRig(t *testing.T, mutate func(c *Config)) *rig {
	t.Helper()
	fake, err := NewFakePrometheus("lab")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	cfg := labConfig(ts.URL + "/prefix/")
	if mutate != nil {
		mutate(cfg)
	}
	r := &rig{fake: fake, clk: &clock{t: t0}, inv: lab(), reg: prometheus.NewRegistry()}
	r.svc, err = New(cfg, Options{Inventory: r.inv, Now: r.clk.now, Metrics: r.reg, Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func near(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: missing, want %g", what, want)
	}
	if math.Abs(*got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Fatalf("%s = %g, want %g", what, *got, want)
	}
}

func TestSnapshotJoinsEverySourceToItsInterfaces(t *testing.T) {
	r := newRig(t, nil)
	s := r.svc.Snapshot(context.Background())
	if s.Stale || s.GeneratedAt == nil || !s.GeneratedAt.Equal(t0) {
		t.Fatalf("stale %v generated %v", s.Stale, s.GeneratedAt)
	}
	l := s.Links
	// Arista, by mgmt_ip in "device"; the series of another job is not read.
	near(t, "sw-e1 rx", l["sw-e1"].RX, 1e9)
	near(t, "sw-e1 tx", l["sw-e1"].TX, 5e8)
	near(t, "sw-e1 util", l["sw-e1"].Util, 0.1)
	if l["sw-e1"].Oper != "up" || l["sw-e1"].Source != "arista" {
		t.Fatalf("sw-e1 %+v", l["sw-e1"])
	}
	// "Et2" meets "Ethernet2", held by a module of the switch.
	near(t, "sw-e2 rx", l["sw-e2"].RX, 8000)
	if l["sw-e2"].Oper != "down" || l["sw-e2"].Util != nil || l["sw-e2"].TX != nil {
		t.Fatalf("sw-e2 %+v", l["sw-e2"])
	}
	// The LAG has no series: the sum of its members.
	po := l["sw-po10"]
	if !po.Aggregated || po.Members != 2 || po.Oper != "up" {
		t.Fatalf("po10 %+v", po)
	}
	near(t, "po10 rx", po.RX, 2e7)
	near(t, "po10 tx", po.TX, 4e7)
	near(t, "po10 util", po.Util, 0.02)
	near(t, "po10 speed, the sum of its members", po.SpeedMbps, 2000)
	near(t, "sw-e1 speed", l["sw-e1"].SpeedMbps, 10000)
	// MikroTik by routerboard_address, pfSense by a fully qualified name
	// whose first label is the device name, node by name in another case.
	near(t, "rtr rx", l["rtr-e1"].RX, 1e5)
	near(t, "rtr util", l["rtr-e1"].Util, 2e-4)
	near(t, "fw rx", l["fw-igb0"].RX, 4000)
	// A duplicate series (another scrape of the same port) keeps the
	// highest, never the sum.
	near(t, "srv tx", l["srv-eno1"].TX, 1.6e6)
	if _, ok := l["srv-lo"]; ok {
		t.Fatal("lo is excluded by the node preset")
	}
	if _, ok := l["gone-e1"]; ok {
		t.Fatal("an absent device is not joined")
	}
	if len(l) != 8 {
		t.Fatalf("%d links: %v", len(l), l)
	}
	byName := map[string]SourceStatus{}
	for _, st := range s.Sources {
		byName[st.Name] = st
	}
	// Arista: 11 series of job arista, 2 of them on nothing known.
	if a := byName["arista"]; a.Series != 13 || a.Matched != 11 || a.Stale || a.LastSuccess == nil {
		t.Fatalf("arista %+v", a)
	}
	if n := byName["node"]; n.Series != 4 || n.Matched != 4 {
		t.Fatalf("node %+v (lo and veth excluded)", n)
	}
	if got := metricValue(t, r.reg, "trica_live_series", "arista", "unmatched"); got != 2 {
		t.Fatalf("unmatched gauge %v", got)
	}
}

func TestQueriesAreTheCompiledAllowList(t *testing.T) {
	r := newRig(t, nil)
	r.svc.Snapshot(context.Background())
	got := map[string]bool{}
	for _, e := range r.fake.Expressions() {
		got[e] = true
	}
	want := []string{
		`rate(arista_port_inOctets{job="arista"}[120s])`,
		`rate(arista_port_outOctets{job="arista"}[120s])`,
		`arista_l2_up{job="arista"}`,
		`rate(mktxp_interface_rx_byte_total[120s])`,
		`rate(mktxp_interface_tx_byte_total[120s])`,
		`mktxp_interface_running`,
		`rate(pfsense_interface_in_bytes[120s])`,
		`rate(pfsense_interface_out_bytes[120s])`,
		`pfsense_interface_up`,
		`rate(node_network_receive_bytes_total[120s])`,
		`rate(node_network_transmit_bytes_total[120s])`,
		`node_network_up`,
	}
	if len(got) != len(want) {
		t.Fatalf("expressions %v", r.fake.Expressions())
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing %s in %v", w, r.fake.Expressions())
		}
	}
}

func TestSnapshotIsCachedForTheRefreshPeriod(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	first := r.svc.Snapshot(ctx)
	n := r.fake.Queries.Load()
	r.clk.add(29 * time.Second)
	if again := r.svc.Snapshot(ctx); again != first || r.fake.Queries.Load() != n {
		t.Fatal("a snapshot within the refresh period is served from the cache")
	}
	r.clk.add(time.Second)
	if next := r.svc.Snapshot(ctx); next == first || r.fake.Queries.Load() != 2*n {
		t.Fatal("past the refresh period the endpoint is queried again")
	}
}

func TestFailingEndpointServesTheLastValuesAsStale(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.svc.Snapshot(ctx)
	r.fake.Fail.Store(true)
	r.clk.add(30 * time.Second)
	s := r.svc.Snapshot(ctx)
	if !s.Stale {
		t.Fatal("a failed refresh is stale")
	}
	near(t, "sw-e1 rx kept", s.Links["sw-e1"].RX, 1e9)
	for _, st := range s.Sources {
		if !st.Stale || st.LastSuccess == nil || !st.LastSuccess.Equal(t0) {
			t.Fatalf("source %+v", st)
		}
	}
	// A failed refresh is not retried by the next caller at once.
	n := r.fake.Queries.Load()
	r.svc.Snapshot(ctx)
	if r.fake.Queries.Load() != n {
		t.Fatal("a failing endpoint is queried at most once per period")
	}
	// Past keepStale periods, old values are dropped.
	r.clk.add(time.Duration(keepStale) * 30 * time.Second)
	s = r.svc.Snapshot(ctx)
	if !s.Stale || len(s.Links) != 0 {
		t.Fatalf("stale %v, %d links", s.Stale, len(s.Links))
	}
	// Label values in label name order: outcome, source.
	if got := metricValue(t, r.reg, "trica_live_upstream_queries_total", "error", "arista"); got == 0 {
		t.Fatal("upstream errors are counted")
	}
	// Back up: fresh again.
	r.fake.Fail.Store(false)
	r.clk.add(30 * time.Second)
	if s = r.svc.Snapshot(ctx); s.Stale || len(s.Links) != 8 {
		t.Fatalf("recovered: stale %v, %d links", s.Stale, len(s.Links))
	}
}

func TestEndpointDownFromTheStartIsEmptyAndStale(t *testing.T) {
	r := newRig(t, nil)
	r.fake.Fail.Store(true)
	s := r.svc.Snapshot(context.Background())
	if !s.Stale || len(s.Links) != 0 || s.GeneratedAt != nil {
		t.Fatalf("%+v", s)
	}
}

func TestInventoryFailure(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.inv.err = errors.New("database down")
	if s := r.svc.Snapshot(ctx); !s.Stale || len(s.Links) != 0 {
		t.Fatalf("no previous: %+v", s)
	}
	r.inv.err = nil
	r.clk.add(time.Minute)
	good := r.svc.Snapshot(ctx)
	r.inv.err = errors.New("database down")
	r.clk.add(time.Minute)
	s := r.svc.Snapshot(ctx)
	if !s.Stale || len(s.Links) != len(good.Links) || good.Stale {
		t.Fatalf("previous kept as stale: %+v", s)
	}
}

func TestBasicAuthFromAFileReference(t *testing.T) {
	pw := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(pw, []byte("s3cret-for-tests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, func(c *Config) { c.Username, c.Password = "trica", collect.Secret("file:"+pw) })
	r.fake.Username, r.fake.Password = "trica", "s3cret-for-tests"
	if s := r.svc.Snapshot(context.Background()); s.Stale || len(s.Links) != 8 {
		t.Fatalf("authenticated: stale %v, %d links", s.Stale, len(s.Links))
	}
	r.fake.Password = "another"
	r.clk.add(time.Minute)
	if s := r.svc.Snapshot(context.Background()); !s.Stale {
		t.Fatal("a refused credential is a failed refresh")
	}
}

func TestBearerToken(t *testing.T) {
	t.Setenv("TRICA_TEST_LIVE_TOKEN", "tok")
	fake, _ := NewFakePrometheus("lab")
	var mu sync.Mutex
	var gotAuth string
	ts := httptest.NewServer(httpFunc(func(h string) { mu.Lock(); gotAuth = h; mu.Unlock() }, fake))
	defer ts.Close()
	cfg := labConfig(ts.URL)
	cfg.BearerToken = "env:TRICA_TEST_LIVE_TOKEN"
	svc, err := New(cfg, Options{Inventory: lab(), Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	svc.Snapshot(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer tok" {
		t.Fatalf("authorization %q", gotAuth)
	}
}

func TestOverridesMakeAnyExporterUsable(t *testing.T) {
	r := newRig(t, func(c *Config) {
		c.RateWindow = 5 * time.Minute
		c.Sources = []Source{{
			Kind: KindGeneric, Name: "snmp",
			// Reuse the MikroTik series under explicit names, as bits.
			Metrics: Metrics{RX: "mktxp_interface_rx_byte_total", TX: "-", Oper: "-"},
			Labels:  Labels{Device: []string{"routerboard_name"}, Interface: "name"},
			Unit:    UnitBits,
		}, {
			Kind: KindNode, ExcludeInterfaces: "-",
			Metrics: Metrics{TX: "-", Oper: "-"},
		}}
	})
	s := r.svc.Snapshot(context.Background())
	near(t, "rtr rx in bits", s.Links["rtr-e1"].RX, 12500)
	if s.Links["rtr-e1"].TX != nil || s.Links["rtr-e1"].Oper != "" || s.Links["rtr-e1"].Source != "snmp" {
		t.Fatalf("%+v", s.Links["rtr-e1"])
	}
	if _, ok := s.Links["srv-lo"]; !ok {
		t.Fatal(`exclude_interfaces "-" keeps lo`)
	}
	for _, e := range r.fake.Expressions() {
		if strings.Contains(e, "transmit") || strings.Contains(e, "tx_byte") || strings.Contains(e, "[120s]") {
			t.Fatalf("disabled or default query sent: %s", e)
		}
	}
}

func TestFirstSourceWins(t *testing.T) {
	r := newRig(t, func(c *Config) {
		c.Sources = []Source{
			{Kind: KindGeneric, Name: "first", Metrics: Metrics{RX: "mktxp_interface_tx_byte_total"}, Labels: Labels{Device: []string{"routerboard_address"}, Interface: "name"}},
			{Kind: KindMikrotik},
		}
	})
	s := r.svc.Snapshot(context.Background())
	if l := s.Links["rtr-e1"]; l.Source != "first" || l.TX != nil {
		t.Fatalf("%+v", l)
	}
	near(t, "first rx", s.Links["rtr-e1"].RX, 2e5)
}

func TestConfigValidation(t *testing.T) {
	ok := func() *Config {
		return &Config{URL: "https://metrics.example.net/select/0/prometheus", Sources: []Source{{Kind: KindNode}}}
	}
	cases := map[string]func(c *Config){
		"no url":             func(c *Config) { c.URL = "" },
		"not http":           func(c *Config) { c.URL = "ftp://x" },
		"credentials in url": func(c *Config) { c.URL = "https://u:p@metrics.example.net" },
		"query in url":       func(c *Config) { c.URL = "https://metrics.example.net/?x=1" },
		"password value":     func(c *Config) { c.Username, c.Password = "u", "hunter2" },
		"username alone":     func(c *Config) { c.Username = "u" },
		"token value":        func(c *Config) { c.BearerToken = "abc" },
		"both auths":         func(c *Config) { c.Username, c.Password, c.BearerToken = "u", "env:P", "env:T" },
		"refresh too short":  func(c *Config) { c.Refresh = time.Second },
		"window too short":   func(c *Config) { c.RateWindow = 10 * time.Second },
		"window fraction":    func(c *Config) { c.RateWindow = 90500 * time.Millisecond },
		"no source":          func(c *Config) { c.Sources = nil },
		"unknown kind":       func(c *Config) { c.Sources[0].Kind = "snmp" },
		"bad metric":         func(c *Config) { c.Sources[0].Metrics.RX = "rate(x[1m])" },
		"bad label":          func(c *Config) { c.Sources[0].Labels.Interface = "a-b" },
		"bad device label":   func(c *Config) { c.Sources[0].Labels.Device = []string{"x}"} },
		"bad matcher":        func(c *Config) { c.Sources[0].Matchers = map[string]string{"__name__": "x"} },
		"bad regexp":         func(c *Config) { c.Sources[0].ExcludeInterfaces = "(" },
		"bad unit":           func(c *Config) { c.Sources[0].Unit = "octets" },
		"bad name":           func(c *Config) { c.Sources[0].Name = "a b" },
		"no metric":          func(c *Config) { c.Sources[0].Metrics = Metrics{RX: "-", TX: "-", Oper: "-"} },
		"generic bare":       func(c *Config) { c.Sources[0] = Source{Kind: KindGeneric, Metrics: Metrics{RX: "x"}} },
		"duplicate":          func(c *Config) { c.Sources = append(c.Sources, Source{Kind: KindNode}) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := ok()
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	c := ok()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Refresh != DefaultRefresh || c.Timeout != DefaultTimeout || c.RateWindow != DefaultRateWindow {
		t.Fatalf("defaults %+v", c)
	}
	// A matcher value is quoted, whatever it holds.
	s, err := Source{Kind: KindNode, Matchers: map[string]string{"job": `a"}or vector(1)`}}.compile()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.selector("up"); got != `up{job="a\"}or vector(1)"}` {
		t.Fatalf("selector %s", got)
	}
}

func TestCanonicalInterface(t *testing.T) {
	for in, want := range map[string]string{
		"Ethernet1/1": "ethernet1/1", "Et1/1": "ethernet1/1", "Po10": "port-channel10", "Port-Channel10": "port-channel10",
		"Ma1": "management1", "eth0": "eth0", "\tether1\n": "ether1", "Vl100": "vlan100", "lo": "lo", "enp1s0": "enp1s0",
	} {
		if got := CanonicalInterface(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestLookupOwner(t *testing.T) {
	inv := lab()
	inv.add("v6", "device", "", map[string]any{"name": "v6-host", "mgmt_ip": "2001:db8::1"})
	inv.add("dup1", "device", "", map[string]any{"name": "dup.a.example"})
	inv.add("dup2", "device", "", map[string]any{"name": "dup.b.example"})
	ix := newIndex(inv.objs, inv.rels)
	for in, want := range map[string]string{
		"192.0.2.11": "sw", "192.0.2.11:9100": "sw", "https://192.0.2.11:443/metrics": "sw",
		"[2001:db8::1]:9100": "v6", "2001:db8::1": "v6", "SW-A1": "sw", "sw-a1.example.net": "sw",
		"dup": "", "dup.a.example": "dup1", "": "", "unknown": "", "192.0.2.99": "",
	} {
		if got := ix.lookupOwner(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestUpstreamAnswers(t *testing.T) {
	cases := map[string]string{
		"not json":  `<html>`,
		"error":     `{"status":"error","errorType":"bad_data","error":"parse error"}`,
		"matrix":    `{"status":"success","data":{"resultType":"matrix","result":[]}}`,
		"too large": "",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(httpBody(body, name == "too large"))
			defer ts.Close()
			c := &Config{URL: ts.URL, Sources: []Source{{Kind: KindNode}}}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			p, err := newPromClient(c)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.query(context.Background(), "up"); !errors.Is(err, errUpstream) {
				t.Fatalf("err %v", err)
			}
		})
	}
	// NaN, infinities and malformed values are dropped.
	ts := httptest.NewServer(httpBody(`{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"a":"1"},"value":[1,"NaN"]},{"metric":{"a":"2"},"value":[1,"+Inf"]},
		{"metric":{"a":"3"},"value":[1,"x"]},{"metric":{"a":"4"},"value":[1,2]},{"metric":{"a":"5"},"value":[1]},
		{"metric":{"a":"6"},"value":[1,"3.5"]}]}}`, false))
	defer ts.Close()
	p, _ := newPromClient(&Config{URL: ts.URL, Timeout: time.Second})
	got, err := p.query(context.Background(), "up")
	if err != nil || len(got) != 1 || got[0].value != 3.5 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestNewRejectsAnInvalidConfig(t *testing.T) {
	if _, err := New(&Config{}, Options{}); err == nil {
		t.Fatal("accepted")
	}
	if _, err := New(&Config{URL: "https://x.example", TLS: collect.TLS{CAFile: "/nonexistent"}, Sources: []Source{{Kind: KindNode}}}, Options{}); err == nil {
		t.Fatal("a missing CA file is an error")
	}
}

// httpFunc records the Authorization header, then serves next.
func httpFunc(record func(string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r.Header.Get("Authorization"))
		next.ServeHTTP(w, r)
	})
}

// httpBody answers every request with body, or with more than maxResponse
// bytes.
func httpBody(body string, huge bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if huge {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[`))
			_, _ = w.Write(bytes.Repeat([]byte(" "), maxResponse))
			return
		}
		_, _ = w.Write([]byte(body))
	})
}

// metricValue reads one gauge or counter of the registry by its label values.
func metricValue(t *testing.T, reg *prometheus.Registry, name string, values ...string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			var got []string
			for _, l := range m.GetLabel() {
				got = append(got, l.GetValue())
			}
			if strings.Join(got, ",") != strings.Join(values, ",") {
				continue
			}
			if m.GetGauge() != nil {
				return m.GetGauge().GetValue()
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

func TestAnAggregateWithItsOwnSeriesIsNotSummed(t *testing.T) {
	r := newRig(t, nil)
	f, _ := NewFakePrometheus("lab")
	series := append([]FakeSeries(nil), f.series...)
	rate := 5e6
	series = append(series, FakeSeries{Metric: "arista_port_inOctets", Labels: map[string]string{"job": "arista", "device": "192.0.2.11", "exported_device": "Po10"}, Rate: &rate})
	r.fake.SetSeries(series)
	po := r.svc.Snapshot(context.Background()).Links["sw-po10"]
	if po.Aggregated || po.Members != 0 {
		t.Fatalf("%+v", po)
	}
	near(t, "po10 own rx", po.RX, 4e7)
}

// The refresh serves every caller: the one that triggers it going away does
// not turn the shared set stale.
func TestACancelledCallerDoesNotSpoilTheRefresh(t *testing.T) {
	r := newRig(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s := r.svc.Snapshot(ctx); s.Stale || len(s.Links) != 8 {
		t.Fatalf("stale %v, %d links", s.Stale, len(s.Links))
	}
}
