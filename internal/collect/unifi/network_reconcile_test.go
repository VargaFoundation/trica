package unifi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/internal/deduce"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const (
	cloudSource = "unifi:office-cloud"
	lanSource   = "unifi:office-lan"
)

// lan runs the cloud collector (the recorded office) and the console's
// Network Integration API (the synthesised office) as two targets of one
// domain, through the runner into the real reconcile engine on PostgreSQL: the
// two sources describe the same ten devices and must make one device each.
type lan struct {
	t     *testing.T
	cloud *FakeSiteManager
	net   *FakeNetwork
	e     *reconcile.Engine
	st    *store.Store
	clock *clock
}

func newLAN(t *testing.T) *lan {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	reg := schema.Builtin()
	st := testdb.New(t)
	return &lan{
		t: t, st: st, clock: clk,
		cloud: newFake(t, SetRecorded),
		net:   newFakeNet(t, SetNetwork),
		e:     &reconcile.Engine{Store: st, Schema: func() *schema.Registry { return reg }, Now: clk.now},
	}
}

func (l *lan) target(source string) collect.Target {
	var tg collect.Target
	switch source {
	case cloudSource:
		tg = l.cloud.Target("office-cloud")
	case lanSource:
		tg = withOptions(l.t, l.net.Target("office-lan"), "api: network\n")
	default:
		l.t.Fatalf("unknown source %s", source)
	}
	tg.Domain = "office-a" // the same address space: ADR 0036
	return tg
}

// run performs one collection of one target and returns what the engine said.
func (l *lan) run(source string) *reconcile.Result {
	l.t.Helper()
	var results []*reconcile.Result
	r, err := runner.New(runner.Config{
		Targets: []collect.Target{l.target(source)},
		Emitter: sdk.EmitterFunc(func(ctx context.Context, b *sdk.Batch) error {
			res, err := l.e.Apply(ctx, b, "", "")
			if err != nil {
				return err
			}
			results = append(results, res)
			return nil
		}),
		Build: func(tg collect.Target) (sdk.Collector, error) {
			c, err := New(tg)
			if err == nil {
				c.(*collector).now = l.clock.now
			}
			return c, err
		},
		Jitter: -1,
	})
	if err != nil {
		l.t.Fatal(err)
	}
	r.RunOnce(context.Background())
	if len(results) != 1 {
		l.t.Fatalf("%s: %d results, want 1", source, len(results))
	}
	if len(results[0].Warnings) != 0 {
		// An attribute or relation the schema does not know is dropped with a
		// warning: each one is a gap between the collector and the schema.
		l.t.Fatalf("%s: engine warnings: %v", source, results[0].Warnings)
	}
	return results[0]
}

func (l *lan) objects(typ string) []*store.Object {
	l.t.Helper()
	all, err := l.st.AllObjects(context.Background())
	if err != nil {
		l.t.Fatal(err)
	}
	var out []*store.Object
	for _, o := range all {
		if o.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

func (l *lan) relations() map[string]int {
	l.t.Helper()
	rels, err := l.st.CurrentRelations(context.Background())
	if err != nil {
		l.t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rels {
		out[r.Type]++
	}
	return out
}

// device returns the stored device of a name.
func (l *lan) device(name string) *store.Object {
	l.t.Helper()
	for _, d := range l.objects("device") {
		if d.Display == name {
			return d
		}
	}
	l.t.Fatalf("no device %s", name)
	return nil
}

// sources returns the sources that currently claim anything on an object.
func (l *lan) sources(id string) map[string]bool {
	l.t.Helper()
	claims, err := l.st.Claims(context.Background(), id)
	if err != nil {
		l.t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range claims {
		out[c.Source] = true
	}
	return out
}

func (l *lan) healthOK(source string) bool {
	l.t.Helper()
	var ok bool
	if err := l.st.Pool.QueryRow(context.Background(), `SELECT ok FROM source_health WHERE source = $1`, source).Scan(&ok); err != nil {
		l.t.Fatal(err)
	}
	return ok
}

func TestNetworkEngineAcceptsTheBatch(t *testing.T) {
	l := newLAN(t)
	l.run(lanSource)

	if n := len(l.objects("device")); n != 10 {
		t.Fatalf("%d devices, want 10", n)
	}
	if n := len(l.objects("interface")); n != 45 {
		t.Errorf("%d interfaces, want 45", n)
	}
	for _, ip := range l.objects("ip_address") {
		if ip.Attrs["domain"] != "office-a" {
			t.Errorf("address %s in domain %v: the runner stamps the target's domain", ip.Display, ip.Attrs["domain"])
		}
	}
	rels := l.relations()
	if rels["uplinked_to"] != 9 || rels["managed_by"] != 0 || rels["neighbor_of"] != 0 || rels["cabled_to"] != 0 {
		t.Errorf("relations %v", rels)
	}
	// The ports carry their PoE through the engine.
	var port1 *store.Object
	for _, o := range l.objects("interface") {
		if o.Display == "Port 1" && o.ParentID != nil && *o.ParentID == l.device("sw-01").ID {
			port1 = o
		}
	}
	if port1 == nil || port1.Attrs["poe_status"] != "delivering" || port1.Attrs["poe_standard"] != "802.3at" || port1.Attrs["connector"] != "RJ45" {
		t.Errorf("sw-01 Port 1: %+v", port1)
	}
	if !l.healthOK(lanSource) {
		t.Error("source_health is not OK")
	}

	// Nothing changed upstream: a second run writes nothing.
	res := l.run(lanSource)
	if res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
		t.Errorf("second run: %+v", res)
	}
}

// The recorded cloud and the console's own API describe the same ten devices:
// one object each, claimed by both, and no value flips when the two take turns.
func TestCloudAndLocalMergeIntoOneDevicePerMAC(t *testing.T) {
	l := newLAN(t)
	l.run(cloudSource)
	l.run(lanSource)

	devices := l.objects("device")
	if len(devices) != 10 {
		t.Fatalf("%d devices after the two sources, want 10", len(devices))
	}
	macs := map[string]bool{}
	for _, d := range devices {
		mac, _ := d.Attrs["base_mac"].(string)
		macs[mac] = true
		if src := l.sources(d.ID); !src[cloudSource] || !src[lanSource] {
			t.Errorf("%s is claimed by %v, want both sources", d.Display, src)
		}
		// What only the cloud knows is still there, what only the console knows too.
		if d.Attrs["model"] == nil || d.Attrs["part_number"] == nil {
			t.Errorf("%s: model %v, part number %v", d.Display, d.Attrs["model"], d.Attrs["part_number"])
		}
	}
	for n := 1; n <= 10; n++ {
		if mac := strings.ToLower(devRef(n)[4:]); !macs[mac] {
			t.Errorf("no device with MAC %s", mac)
		}
	}
	if n := len(l.objects("ip_address")); n != 11 {
		t.Errorf("%d ip_address objects, want the cloud's 11: the console's addresses are the same objects", n)
	}
	ips := 0
	for _, ip := range l.objects("ip_address") {
		if ip.Display == "192.0.2.11" {
			ips++
		}
	}
	if ips != 1 {
		t.Errorf("192.0.2.11 is %d objects", ips)
	}

	// Rounds 3 and 4: neither source changes anything the other sets.
	for _, source := range []string{cloudSource, lanSource} {
		res := l.run(source)
		if res.Created != 0 || res.ValuesChanged != 0 || res.RelationsOpened != 0 || res.RelationsClosed != 0 {
			t.Errorf("%s, second round: %+v: the two sources make a value flip", source, res)
		}
	}
	if n := len(l.objects("device")); n != 10 {
		t.Errorf("%d devices after the second round", n)
	}
	// A cloud run does not withdraw what only the console says.
	rels := l.relations()
	if rels["uplinked_to"] != 9 || rels["managed_by"] != 9 {
		t.Errorf("relations %v after the cloud ran again", rels)
	}
	for _, o := range l.objects("interface") {
		if o.Display == "Port 25" && o.Attrs["connector"] != "SFP+" {
			t.Errorf("the cloud run withdrew the console's port values: %+v", o.Attrs)
		}
	}
	for _, d := range devices {
		if d.Attrs["role"] == nil {
			t.Errorf("%s has no role", d.Display)
		}
	}
	// 10 devices x 2 sources: the console's 11 ports and the cloud's 2 WAN
	// ports are different interfaces (documented: the API names no port role).
	if n := len(l.objects("interface")); n != 47 {
		t.Errorf("%d interfaces, want 45 + the cloud's 2 WAN ports", n)
	}
	if !l.healthOK(cloudSource) || !l.healthOK(lanSource) {
		t.Error("a source is not OK")
	}

	// The deduction has nothing to work with: the API names the uplink device,
	// not the ports, so no cable may be deduced (ADR 0010: no guessing).
	d := &deduce.Deducer{Engine: l.e, Store: l.st, Now: l.clock.now}
	rep, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Plan.Cables) != 0 || len(rep.Plan.Skipped) != 0 || len(rep.Plan.Findings) != 0 {
		doc, _ := json.Marshal(rep.Plan)
		t.Errorf("the deduction made something of the uplinks: %s", doc)
	}
	rels = l.relations()
	if rels["cabled_to"] != 0 || rels["neighbor_of"] != 0 || rels["uplinked_to"] != 9 {
		t.Errorf("relations %v after the deduction", rels)
	}
	// The uplinks are the real ones.
	ids := map[string]string{}
	for _, dv := range devices {
		ids[dv.Display] = dv.ID
	}
	all, err := l.st.CurrentRelations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	uplinks := map[string]string{}
	for _, r := range all {
		if r.Type == "uplinked_to" {
			uplinks[r.FromID] = r.ToID
		}
	}
	for _, ap := range []string{"ap-01", "ap-02", "ap-03", "ap-04", "ap-05", "ap-06", "door-hub-01", "reader-01"} {
		if uplinks[ids[ap]] != ids["sw-01"] {
			t.Errorf("%s is not uplinked to sw-01", ap)
		}
	}
	if uplinks[ids["sw-01"]] != ids["Office A"] {
		t.Error("sw-01 is not uplinked to the console")
	}
}

// What the console's run no longer says is withdrawn by a full run and kept by
// a partial one.
func TestNetworkWithdrawalOnlyOnFullRuns(t *testing.T) {
	l := newLAN(t)
	l.run(lanSource)
	ap := "/v1/sites/" + officeSite + "/devices/" + uidOf(0x102) // ap-01
	if got := l.relations()["uplinked_to"]; got != 9 {
		t.Fatalf("uplinked_to %d", got)
	}

	// A failed detail call: partial, nothing closed, the port and the uplink stay.
	l.clock.set(l.clock.now().Add(5 * time.Minute))
	l.net.Fail(ap, 500)
	res := l.run(lanSource)
	if res.RelationsClosed != 0 {
		t.Errorf("a partial run closed %d relations", res.RelationsClosed)
	}
	if got := l.relations()["uplinked_to"]; got != 9 {
		t.Errorf("uplinked_to %d after a partial run, want 9", got)
	}
	if l.healthOK(lanSource) {
		t.Error("a partial run must show on the health board")
	}

	// A full run in which ap-01 says it has no uplink and no port: the
	// uplink closes, and the port is absent once nobody has seen it.
	l.net.Fail(ap, 0)
	l.net.SetBody(ap, `{"id":"`+uidOf(0x102)+`","interfaces":{"ports":[]}}`)
	l.clock.set(l.clock.now().Add(time.Hour))
	res = l.run(lanSource)
	if res.RelationsClosed == 0 {
		t.Fatal("no relation closed for an uplink that is gone")
	}
	if got := l.relations()["uplinked_to"]; got != 8 {
		t.Errorf("uplinked_to %d, want 8", got)
	}
	if _, err := l.e.SweepAbsent(context.Background()); err != nil {
		t.Fatal(err)
	}
	var gone *store.Object
	ap1 := l.device("ap-01")
	for _, o := range l.objects("interface") {
		if o.ParentID != nil && *o.ParentID == ap1.ID {
			gone = o
		}
	}
	if gone == nil || gone.Status != "absent" {
		t.Errorf("ap-01's port after its removal: %+v", gone)
	}
	if st := ap1.Status; st == "absent" {
		t.Error("the device itself is still seen and must not be absent")
	}
}
