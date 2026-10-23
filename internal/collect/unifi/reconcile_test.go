package unifi

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// These tests run the collector through the runner (which stamps the target's
// domain on the addresses) into the real reconcile engine on PostgreSQL: the
// batches are only worth something if the engine accepts them as they are.

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type pipeline struct {
	t      *testing.T
	f      *FakeSiteManager
	e      *reconcile.Engine
	st     *store.Store
	clock  *clock
	target collect.Target
}

func newPipeline(t *testing.T, set string) *pipeline {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	reg := schema.Builtin()
	st := testdb.New(t)
	f := newFake(t, set)
	tg := f.Target(office)
	tg.Domain = "office-a"
	return &pipeline{
		t: t, f: f, st: st, clock: clk, target: tg,
		e: &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Now: clk.now},
	}
}

// run performs one collection and returns what the engine said about it.
func (p *pipeline) run() []*reconcile.Result {
	p.t.Helper()
	var results []*reconcile.Result
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{p.target},
		Emitter: sdk.EmitterFunc(func(ctx context.Context, b *sdk.Batch) error {
			res, err := p.e.Apply(ctx, b, "", "")
			if err != nil {
				return err
			}
			results = append(results, res)
			return nil
		}),
		Build: func(tg collect.Target) (sdk.Collector, error) {
			c, err := New(tg)
			if err == nil {
				c.(*collector).now = p.clock.now
			}
			return c, err
		},
		Jitter: -1,
	})
	if err != nil {
		p.t.Fatal(err)
	}
	r.RunOnce(context.Background())
	if len(results) != 1 {
		p.t.Fatalf("%d results, want 1", len(results))
	}
	return results
}

func (p *pipeline) objects(typ string) []*store.Object {
	p.t.Helper()
	all, err := p.st.AllObjects(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	var out []*store.Object
	for _, o := range all {
		if o.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

func (p *pipeline) relations() map[string]int {
	p.t.Helper()
	rels, err := p.st.CurrentRelations(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rels {
		out[r.Type]++
	}
	return out
}

func TestEngineAcceptsTheBatch(t *testing.T) {
	p := newPipeline(t, SetRecorded)
	res := p.run()[0]
	if len(res.Warnings) != 0 {
		// An attribute or relation the schema does not know is dropped with a
		// warning: each one is a gap between the collector and the schema.
		t.Fatalf("engine warnings: %v", res.Warnings)
	}

	devices := p.objects("device")
	if len(devices) != 10 {
		t.Fatalf("%d devices, want 10", len(devices))
	}
	for _, d := range devices {
		if d.Attrs["vendor"] != "Ubiquiti" {
			t.Errorf("device %s: vendor %v", d.Display, d.Attrs["vendor"])
		}
	}
	ips := p.objects("ip_address")
	if len(ips) != 11 {
		t.Errorf("%d ip_address, want 11", len(ips))
	}
	for _, ip := range ips {
		if ip.Attrs["domain"] != "office-a" {
			t.Errorf("address %s in domain %v: the runner stamps the target's domain", ip.Display, ip.Attrs["domain"])
		}
	}
	rels := p.relations()
	if rels["managed_by"] != 9 || rels["routes_via"] != 2 || rels["runs"] != 3 {
		t.Errorf("relations %v", rels)
	}
	if n := len(p.objects("internet_access")); n != 2 {
		t.Errorf("%d internet accesses", n)
	}

	// The collection health board.
	var ok bool
	var lastErr string
	var items int
	if err := p.st.Pool.QueryRow(context.Background(), `SELECT ok, last_error, items FROM source_health WHERE source = 'unifi:office'`).Scan(&ok, &lastErr, &items); err != nil {
		t.Fatal(err)
	}
	if !ok || lastErr != "" || items == 0 {
		t.Errorf("source_health: ok %v, error %q, items %d", ok, lastErr, items)
	}

	// No account data got stored, whatever the attribute.
	all, err := p.st.AllObjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range all {
		doc, _ := json.Marshal(o)
		for _, s := range personal {
			if strings.Contains(string(doc), s) {
				t.Errorf("%s %s stores %q", o.Type, o.Display, s)
			}
		}
		if strings.Contains(string(doc), "@") {
			t.Errorf("%s %s stores an '@': %s", o.Type, o.Display, doc)
		}
	}
}

// Nothing changed upstream: a second run writes nothing. A collector that
// emitted a clock-dependent or unordered value would churn the history every
// five minutes.
func TestSecondRunChangesNothing(t *testing.T) {
	p := newPipeline(t, SetRecorded)
	p.run()
	res := p.run()[0]
	if res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
		t.Errorf("second run: %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
}

// A device that is gone from a full run is withdrawn (its relations close, it
// becomes absent), and a run that could not read the devices withdraws
// nothing.
func TestWithdrawalOnlyOnFullRuns(t *testing.T) {
	p := newPipeline(t, SetRecorded)
	p.run()
	before := p.relations()

	// A failed devices call: partial, nothing closed.
	p.clock.set(p.clock.now().Add(5 * time.Minute))
	p.f.Fail("/v1/devices", 500)
	res := p.run()[0]
	if res.RelationsClosed != 0 {
		t.Errorf("a partial run closed %d relations", res.RelationsClosed)
	}
	if after := p.relations(); after["managed_by"] != before["managed_by"] || after["assigned_to"] != before["assigned_to"] {
		t.Errorf("relations %v after a partial run, were %v", after, before)
	}
	var ok bool
	if err := p.st.Pool.QueryRow(context.Background(), `SELECT ok FROM source_health WHERE source = 'unifi:office'`).Scan(&ok); err != nil || ok {
		t.Errorf("source_health ok=%v, err %v: a partial run must show on the board", ok, err)
	}

	// A full run without ap-06.
	p.f.Fail("/v1/devices", 0)
	p.f.SetBody("/v1/devices", withoutDevice(t, "020000000007"))
	p.clock.set(p.clock.now().Add(time.Hour))
	res = p.run()[0]
	if res.RelationsClosed == 0 {
		t.Fatal("no relation closed for a device that is gone")
	}
	after := p.relations()
	if after["managed_by"] != 8 {
		t.Errorf("managed_by %d, want 8", after["managed_by"])
	}
	// Nobody has seen it for longer than the policy allows: absent, not deleted.
	if _, err := p.e.SweepAbsent(context.Background()); err != nil {
		t.Fatal(err)
	}
	var gone *store.Object
	for _, d := range p.objects("device") {
		if d.Display == "ap-06" {
			gone = d
		}
	}
	if gone == nil || gone.Status != "absent" {
		t.Errorf("ap-06 after its removal: %+v", gone)
	}
}

// withoutDevice returns the recorded /v1/devices answer without one device.
func withoutDevice(t *testing.T, id string) string {
	t.Helper()
	b, err := fixtures.ReadFile("testdata/" + SetRecorded + "/v1/devices.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Data           []map[string]any `json:"data"`
		HTTPStatusCode int              `json:"httpStatusCode"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, g := range doc.Data {
		var keep []any
		for _, d := range g["devices"].([]any) {
			if d.(map[string]any)["id"] != id {
				keep = append(keep, d)
			}
		}
		g["devices"] = keep
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// The synthesised account, with a disconnected console and a device that is
// not adopted, is accepted by the engine too.
func TestEngineAcceptsTheSynthesisedAccount(t *testing.T) {
	p := newPipeline(t, SetSynthesised)
	res := p.run()[0]
	if len(res.Warnings) != 0 {
		t.Fatalf("engine warnings: %v", res.Warnings)
	}
	if n := len(p.objects("device")); n != 9 {
		t.Errorf("%d devices, want 9", n)
	}
}
