package deduce_test

import (
	"context"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/deduce"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

type lab struct {
	t   *testing.T
	ctx context.Context
	e   *reconcile.Engine
	s   *store.Store
	d   *deduce.Deducer
	now time.Time
	ids map[string]string
}

func newLab(t *testing.T, p reconcile.Policy) *lab {
	t.Helper()
	s := testdb.New(t)
	reg := schema.Builtin()
	l := &lab{t: t, ctx: context.Background(), s: s, now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	clock := func() time.Time { return l.now }
	l.e = &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }, Policy: p, Now: clock}
	l.d = &deduce.Deducer{Engine: l.e, Store: s, Now: clock}
	return l
}

// observe applies what the switches report: two switches sw-a1 and sw-a2 with
// two ports each, a server with one NIC, the LLDP adjacencies given as
// "a1:Ethernet1>a2:Ethernet1" (from > to), and the MAC learned on sw-a1
// Ethernet2 when mac is set.
func (l *lab) observe(lldp []string, mac bool) {
	l.t.Helper()
	l.now = l.now.Add(5 * time.Minute)
	b := sdk.NewBatch("lab", sdk.ScopeFull)
	b.ObservedAt = l.now
	for _, sw := range []string{"a1", "a2"} {
		b.Object(sw, "device").Identify("serial", "SN-"+sw).Identify("name", "sw-"+sw).Set("role", "switch")
		for _, p := range []string{"Ethernet1", "Ethernet2"} {
			b.Object(sw+":"+p, "interface").Identify("name", p).In(sw)
		}
	}
	b.Object("h1", "device").Identify("serial", "SN-h1").Identify("name", "srv-01").Set("role", "server")
	b.Object("h1:eno1", "interface").Identify("name", "eno1").In("h1").Set("mac", "02:00:00:00:01:01")
	if mac {
		o, _ := b.Lookup("a1:Ethernet2")
		o.Set("learned_macs", []any{"10/02:00:00:00:01:01"})
	}
	for _, a := range lldp {
		var from, to string
		for i := range a {
			if a[i] == '>' {
				from, to = a[:i], a[i+1:]
			}
		}
		b.Relate("neighbor_of", from, to)
	}
	res, err := l.e.Apply(l.ctx, b, "", "")
	if err != nil {
		l.t.Fatalf("observe: %v", err)
	}
	l.ids = res.Objects
}

// declare documents a cable between two lab ports.
func (l *lab) declare(label, a, b string) {
	l.t.Helper()
	bt := sdk.NewBatch("file:site", sdk.ScopeFull)
	bt.ObservedAt = l.now
	bt.Confidence = sdk.Declared
	bt.Object("c", "cable").Identify("label", label)
	bt.RelateEndpoints("connects", sdk.Ref("c"), sdk.ByID("interface", l.ids[a])).Attributes["end"] = "a"
	bt.RelateEndpoints("connects", sdk.Ref("c"), sdk.ByID("interface", l.ids[b])).Attributes["end"] = "b"
	if _, err := l.e.Apply(l.ctx, bt, "", ""); err != nil {
		l.t.Fatalf("declare: %v", err)
	}
}

func (l *lab) run() *deduce.Report {
	l.t.Helper()
	l.now = l.now.Add(time.Minute)
	r, err := l.d.Run(l.ctx)
	if err != nil {
		l.t.Fatalf("run: %v", err)
	}
	return r
}

// cabled returns the current cabled_to relations as "a|b evidence level",
// with lab names.
func (l *lab) cabled() []string {
	l.t.Helper()
	rels, err := l.s.CurrentRelations(l.ctx)
	if err != nil {
		l.t.Fatal(err)
	}
	name := map[string]string{}
	for k, v := range l.ids {
		name[v] = k
	}
	var out []string
	for _, r := range rels {
		if r.Type == "cabled_to" {
			out = append(out, name[r.FromID]+"|"+name[r.ToID]+" "+r.Attrs["evidence"].(string)+" "+r.Attrs["level"].(string)+" "+r.Confidence)
		}
	}
	return out
}

func (l *lab) open(kind string) []reconcile.Discrepancy {
	l.t.Helper()
	ds, _, err := reconcile.ListDiscrepancies(l.ctx, l.s, reconcile.DiscrepancyFilter{Status: "open", Kind: kind})
	if err != nil {
		l.t.Fatal(err)
	}
	return ds
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// order returns the pair in the order the deducer stores it (smaller id
// first), as lab names.
func (l *lab) pair(a, b, rest string) string {
	if l.ids[b] < l.ids[a] {
		a, b = b, a
	}
	return a + "|" + b + " " + rest
}

func TestMovedCableIsFollowed(t *testing.T) {
	l := newLab(t, reconcile.Policy{})
	l.observe([]string{"a1:Ethernet1>a2:Ethernet1", "a2:Ethernet1>a1:Ethernet1"}, false)
	r := l.run()
	want := []string{l.pair("a1:Ethernet1", "a2:Ethernet1", "lldp high deduced")}
	if got := l.cabled(); !same(got, want) || r.Result.RelationsOpened != 1 {
		t.Fatalf("first pass: %v (opened %d), want %v", got, r.Result.RelationsOpened, want)
	}

	// Same observation: nothing changes.
	if r := l.run(); r.Result.RelationsOpened != 0 || r.Result.RelationsClosed != 0 {
		t.Errorf("idempotent pass changed relations: %+v", r.Result)
	}

	// The cable moves from sw-a2 Ethernet1 to Ethernet2, seen from sw-a1 only
	// for now.
	l.observe([]string{"a1:Ethernet1>a2:Ethernet2"}, false)
	r = l.run()
	want = []string{l.pair("a1:Ethernet1", "a2:Ethernet2", "lldp_one_sided medium deduced")}
	if got := l.cabled(); !same(got, want) || r.Result.RelationsClosed != 1 || r.Result.RelationsOpened != 1 {
		t.Fatalf("after move: %v (%+v), want %v", got, r.Result, want)
	}

	// The old relation is closed, not deleted.
	hist, err := l.s.RelationHistory(l.ctx, l.ids["a2:Ethernet1"], 10)
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	for _, h := range hist {
		if h.Type == "cabled_to" && h.ValidTo != nil {
			closed++
		}
	}
	if closed != 1 {
		t.Errorf("history of the old port: %d closed cabled_to, want 1", closed)
	}
}

func TestForwardingTableCable(t *testing.T) {
	l := newLab(t, reconcile.Policy{})
	l.observe(nil, true)
	l.run()
	want := []string{l.pair("a1:Ethernet2", "h1:eno1", "mac_table low deduced")}
	if got := l.cabled(); !same(got, want) {
		t.Fatalf("cables %v, want %v", got, want)
	}
	// The address ages out of the table: the cable is withdrawn.
	l.observe(nil, false)
	l.run()
	if got := l.cabled(); len(got) != 0 {
		t.Errorf("cables after ageing out: %v", got)
	}
}

func TestDocumentedCableDiscrepancies(t *testing.T) {
	l := newLab(t, reconcile.Policy{})
	l.observe([]string{"a1:Ethernet1>a2:Ethernet1", "a2:Ethernet1>a1:Ethernet1"}, false)
	l.declare("C-001", "a1:Ethernet1", "a2:Ethernet1")
	l.run()
	if ds := l.open(""); len(ds) != 0 {
		t.Fatalf("agreement opened discrepancies: %+v", ds)
	}

	// Moved without updating the documentation.
	l.observe([]string{"a1:Ethernet1>a2:Ethernet2", "a2:Ethernet2>a1:Ethernet1"}, false)
	l.run()
	ds := l.open(deduce.KindConflict)
	if len(ds) != 1 || ds[0].Key != "cabled_to" {
		t.Fatalf("conflicts %+v, want 1", ds)
	}
	if n := len(l.open("")); n != 1 {
		t.Errorf("open discrepancies %d, want only the conflict", n)
	}

	// Same state: refreshed, not duplicated.
	l.run()
	if n := len(l.open(deduce.KindConflict)); n != 1 {
		t.Errorf("conflicts after a second pass: %d", n)
	}

	// Moved back: the conflict is resolved by the deducer.
	l.observe([]string{"a1:Ethernet1>a2:Ethernet1", "a2:Ethernet1>a1:Ethernet1"}, false)
	r := l.run()
	if n := len(l.open("")); n != 0 || r.Resolved != 1 {
		t.Errorf("after moving back: %d open, %d resolved", n, r.Resolved)
	}
	all, _, err := reconcile.ListDiscrepancies(l.ctx, l.s, reconcile.DiscrepancyFilter{Status: "resolved"})
	if err != nil || len(all) != 1 || all[0].ClosedBy != deduce.Source {
		t.Errorf("resolved %+v (%v)", all, err)
	}

	// Unplugged: documented but no longer observed.
	l.observe(nil, false)
	l.run()
	if n := len(l.open(deduce.KindNotObserved)); n != 0 {
		// No LLDP evidence at all on either switch: silence, not a gap.
		t.Errorf("not observed without any evidence: %d", n)
	}
	l.observe([]string{"a1:Ethernet2>a2:Ethernet2", "a2:Ethernet2>a1:Ethernet2"}, false)
	l.run()
	if n := len(l.open(deduce.KindNotObserved)); n != 1 {
		t.Errorf("not observed: %d, want 1", n)
	}
	if n := len(l.open(deduce.KindNotDeclared)); n != 1 {
		t.Errorf("not declared: %d, want 1", n)
	}
}

func TestProposePolicyQueuesOnce(t *testing.T) {
	l := newLab(t, reconcile.Policy{Propose: []string{"deduce:*"}})
	l.observe([]string{"a1:Ethernet1>a2:Ethernet1", "a2:Ethernet1>a1:Ethernet1"}, false)
	first := l.run().Result.ProposalID
	second := l.run().Result.ProposalID
	if first == 0 || first != second {
		t.Errorf("proposals %d then %d, want one pending proposal", first, second)
	}
	if got := l.cabled(); len(got) != 0 {
		t.Errorf("proposal applied without approval: %v", got)
	}
	if _, err := l.e.Approve(l.ctx, first, "user:ops"); err != nil {
		t.Fatal(err)
	}
	if got := l.cabled(); len(got) != 1 {
		t.Errorf("after approval: %v", got)
	}
}

func TestLoopRunsUntilCancelled(t *testing.T) {
	l := newLab(t, reconcile.Policy{})
	l.observe([]string{"a1:Ethernet1>a2:Ethernet1"}, false)
	ctx, cancel := context.WithCancel(l.ctx)
	done := make(chan struct{})
	go func() { l.d.Loop(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for len(l.cabled()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if len(l.cabled()) != 1 {
		t.Errorf("loop did not run a pass")
	}
}
