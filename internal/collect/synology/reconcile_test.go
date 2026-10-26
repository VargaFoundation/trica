package synology

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
	f      *FakeDSM
	e      *reconcile.Engine
	st     *store.Store
	clock  *clock
	target collect.Target
}

func newPipeline(t *testing.T, set string) *pipeline {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)}
	reg := schema.Builtin()
	st := testdb.New(t)
	f := newFake(t, set)
	tg := f.Target(nas)
	tg.Domain = "office-a"
	return &pipeline{
		t: t, f: f, st: st, clock: clk, target: tg,
		e: &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Now: clk.now},
	}
}

// run performs one collection and returns what the engine said about it.
func (p *pipeline) run() *reconcile.Result {
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
		Jitter: -1,
	})
	if err != nil {
		p.t.Fatal(err)
	}
	r.RunOnce(context.Background())
	if len(results) != 1 {
		p.t.Fatalf("%d results, want 1", len(results))
	}
	return results[0]
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

func (p *pipeline) device(display string) *store.Object {
	p.t.Helper()
	for _, o := range p.objects("device") {
		if o.Display == display {
			return o
		}
	}
	p.t.Fatalf("no device %q", display)
	return nil
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
	res := p.run()
	if len(res.Warnings) != 0 {
		// An attribute or relation the schema does not know is dropped with a
		// warning: each one is a gap between the collector and the schema.
		t.Fatalf("engine warnings: %v", res.Warnings)
	}
	for typ, want := range map[string]int{"device": 2, "disk": 8, "interface": 6, "ip_address": 1, "software": 23} {
		if got := len(p.objects(typ)); got != want {
			t.Errorf("%d %s, want %d", got, typ, want)
		}
	}
	nasObj := p.device("nas-a1")
	if nasObj.Attrs["serial"] != "0000ABCDE0001" || nasObj.Attrs["vendor"] != "Synology" || nasObj.Attrs["health"] != "warning" {
		t.Errorf("NAS attributes %v", nasObj.Attrs)
	}
	if ups := p.device("nas-a1/ups"); ups.Attrs["role"] != "ups" {
		t.Errorf("UPS attributes %v", ups.Attrs)
	}
	ip := p.objects("ip_address")[0]
	if ip.Attrs["domain"] != "office-a" || ip.Attrs["address"] != "192.0.2.57" {
		t.Errorf("address %v: the runner stamps the target's domain", ip.Attrs)
	}
	rels := p.relations()
	if rels["runs"] != 23 || rels["powered_by"] != 1 || rels["assigned_to"] != 1 {
		t.Errorf("relations %v", rels)
	}

	var ok bool
	var lastErr string
	var items int
	if err := p.st.Pool.QueryRow(context.Background(), `SELECT ok, last_error, items FROM source_health WHERE source = 'synology:nas'`).Scan(&ok, &lastErr, &items); err != nil {
		t.Fatal(err)
	}
	if !ok || lastErr != "" || items == 0 {
		t.Errorf("source_health: ok %v, error %q, items %d", ok, lastErr, items)
	}

	// The credentials never reach the store.
	all, err := p.st.AllObjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range all {
		doc, _ := json.Marshal(o)
		for _, secret := range append(p.f.IssuedSIDs(), Password) {
			if strings.Contains(string(doc), secret) {
				t.Errorf("%s %s stores a credential", o.Type, o.Display)
			}
		}
	}
}

// Nothing changed upstream: a second run writes nothing. A collector that
// emitted a clock-dependent or unordered value would churn the history on
// every run.
func TestSecondRunChangesNothing(t *testing.T) {
	for _, set := range []string{SetRecorded, SetSynthesised} {
		p := newPipeline(t, set)
		p.run()
		res := p.run()
		if res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
			t.Errorf("%s, second run: %+v", set, res)
		}
		if len(res.Warnings) != 0 {
			t.Errorf("%s: warnings %v", set, res.Warnings)
		}
	}
}

// The UPS going away from a full run withdraws its relation; a run that could
// not read the UPS withdraws nothing.
func TestWithdrawalOnlyOnFullRuns(t *testing.T) {
	p := newPipeline(t, SetRecorded)
	p.run()

	// A refused UPS call: partial, nothing closed.
	p.clock.set(p.clock.now().Add(5 * time.Minute))
	p.f.Fail("SYNO.Core.ExternalDevice.UPS", "", 105)
	if res := p.run(); res.RelationsClosed != 0 {
		t.Errorf("a partial run closed %d relations", res.RelationsClosed)
	}
	if n := p.relations()["powered_by"]; n != 1 {
		t.Errorf("powered_by %d after a partial run, want 1", n)
	}
	var ok bool
	if err := p.st.Pool.QueryRow(context.Background(), `SELECT ok FROM source_health WHERE source = 'synology:nas'`).Scan(&ok); err != nil || !ok {
		t.Errorf("source_health ok=%v, err %v: a refused optional API is a warning, not a fault", ok, err)
	}

	// A full run that sees the UPS unplugged.
	p.f.Fail("SYNO.Core.ExternalDevice.UPS", "", 0)
	p.f.SetBody("SYNO.Core.ExternalDevice.UPS", "get", `{"success":true,"data":{"enable":true,"usb_ups_connect":false}}`)
	p.clock.set(p.clock.now().Add(time.Hour))
	if res := p.run(); res.RelationsClosed == 0 {
		t.Fatal("no relation closed for a UPS that is gone")
	}
	if n := p.relations()["powered_by"]; n != 0 {
		t.Errorf("powered_by %d, want 0", n)
	}
}

// The disk health reaches the store as a series of values: a drive that
// crashes between two runs changes its health and nothing else.
func TestDiskHealthChangeIsOneValue(t *testing.T) {
	p := newPipeline(t, SetRecorded)
	p.run()
	b, err := fixtures.ReadFile("testdata/" + SetRecorded + "/storage_load_info.json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, d := range env.Data["disks"].([]any) {
		if m := d.(map[string]any); m["id"] == "sata1" {
			m["status"], changed = "crashed", true
		}
	}
	if !changed {
		t.Fatal("no sata1 in the fixture")
	}
	doc, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	p.f.SetBody("SYNO.Storage.CGI.Storage", "load_info", string(doc))
	p.clock.set(p.clock.now().Add(5 * time.Minute))
	res := p.run()
	if res.ValuesChanged < 2 { // the disk's health and the NAS's rollup
		t.Errorf("values changed %d, want at least the disk and the NAS", res.ValuesChanged)
	}
	if got := p.device("nas-a1").Attrs["health"]; got != "critical" {
		t.Errorf("NAS health %v, want critical", got)
	}
}
