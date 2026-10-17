package reconcile_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newEngine(t *testing.T, p reconcile.Policy) (*reconcile.Engine, *store.Store, *clock) {
	t.Helper()
	s := testdb.New(t)
	c := &clock{t: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	reg := schema.Builtin()
	return &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }, Policy: p, Now: c.now}, s, c
}

func batch(c *clock, source string, scope sdk.Scope) *sdk.Batch {
	b := sdk.NewBatch(source, scope)
	b.ObservedAt = c.t
	return b
}

func apply(t *testing.T, e *reconcile.Engine, b *sdk.Batch) *reconcile.Result {
	t.Helper()
	res, err := e.Apply(context.Background(), b, "", "")
	if err != nil {
		t.Fatalf("apply %s: %v", b.Source, err)
	}
	return res
}

// switchBatch describes a switch with two interfaces and an LLDP neighbour.
func switchBatch(c *clock, name, ip string) *sdk.Batch {
	b := batch(c, "arista:sw1", sdk.ScopeFull)
	b.Object("sw", "device").Identify("serial", "JPE111").Identify("name", name).Set("role", "switch").Set("mgmt_ip", ip).Set("os_version", "4.20.7M")
	b.Object("e1", "interface").Identify("name", "Ethernet1").In("sw").Set("oper_status", "up").Set("speed_mbps", 10000)
	b.Object("e2", "interface").Identify("name", "Ethernet2").In("sw").Set("oper_status", "down")
	return b
}

func TestCreateThenIdempotent(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	r1 := apply(t, e, switchBatch(c, "sw-a1", "192.0.2.10"))
	if r1.Created != 3 {
		t.Fatalf("created %d, want 3", r1.Created)
	}
	c.advance(5 * time.Minute)
	r2 := apply(t, e, switchBatch(c, "sw-a1", "192.0.2.10"))
	if r2.Created != 0 || r2.ValuesChanged != 0 || len(r2.Changes) != 0 {
		t.Fatalf("second identical batch changed things: %+v", r2)
	}
	if r1.Objects["sw"] != r2.Objects["sw"] || r1.Objects["e1"] != r2.Objects["e1"] {
		t.Fatal("identity not stable across runs")
	}
	sw, err := s.Object(ctx, r1.Objects["sw"])
	if err != nil {
		t.Fatal(err)
	}
	if sw.Display != "sw-a1" || sw.Attrs["mgmt_ip"] != "192.0.2.10" || sw.Status != "active" {
		t.Fatalf("unexpected switch state %+v", sw)
	}
	if !sw.LastSeen.Equal(c.t) {
		t.Fatalf("last_seen %v, want %v", sw.LastSeen, c.t)
	}
	prov, err := s.Provenance(ctx, sw.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range prov {
		if v.Key == "os_version" {
			found = true
			if v.Source != "arista:sw1" || v.Confidence != "observed" || v.LastSeen == nil || !v.LastSeen.Equal(c.t) {
				t.Fatalf("bad provenance %+v", v)
			}
		}
	}
	if !found {
		t.Fatal("no provenance for os_version")
	}
}

func TestRenameAndReaddressKeepIdentity(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	before := apply(t, e, switchBatch(c, "sw-a1", "192.0.2.10"))
	t0 := c.t
	c.advance(time.Hour)
	after := apply(t, e, switchBatch(c, "core-sw-1", "192.0.2.20"))
	id := before.Objects["sw"]
	if after.Objects["sw"] != id {
		t.Fatal("renamed switch got a new identity")
	}
	if after.Objects["e1"] != before.Objects["e1"] {
		t.Fatal("interfaces of a renamed switch got new identities")
	}
	if after.ValuesChanged != 2 {
		t.Fatalf("values changed %d, want 2 (name, mgmt_ip)", after.ValuesChanged)
	}
	old, err := s.AsOf(ctx, id, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if old["name"] != "sw-a1" || old["mgmt_ip"] != "192.0.2.10" {
		t.Fatalf("time travel: %v", old)
	}
	vals, ids, err := s.WhoHad(ctx, "mgmt_ip", "192.0.2.10", &t0)
	if err != nil || len(ids) != 1 || ids[0] != id || vals[0].ValidTo == nil {
		t.Fatalf("who had the old IP: %v %v %v", vals, ids, err)
	}
	changes, err := s.Changes(ctx, t0, c.t, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, ch := range changes {
		keys[ch.Key] = true
	}
	if !keys["name"] || !keys["mgmt_ip"] || keys["os_version"] {
		t.Fatalf("diff keys %v", keys)
	}
}

func TestNICChangeKeepsIdentity(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{})
	b := batch(c, "redfish:srv1", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "CZ1").Identify("base_mac", "aa:bb:cc:00:00:01")
	first := apply(t, e, b)
	c.advance(time.Hour)
	b = batch(c, "redfish:srv1", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "CZ1").Identify("base_mac", "aa:bb:cc:00:00:99")
	if second := apply(t, e, b); second.Objects["d"] != first.Objects["d"] || second.Created != 0 {
		t.Fatal("new NIC created a new device")
	}
}

func TestWeakMatchMergesAcrossSources(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	b := batch(c, "redfish:cont-svr1", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "CZJ1").Identify("name", "cont-svr1").Set("vendor", "HPE")
	ilo := apply(t, e, b)
	b = batch(c, "agent:cont-svr1", sdk.ScopeFull)
	b.Object("d", "device").Identify("hw_uuid", "36383150-3630-5a43-4a31").Identify("name", "CONT-SVR1").Set("kernel", "6.8.0")
	agent := apply(t, e, b)
	if agent.Objects["d"] != ilo.Objects["d"] {
		t.Fatal("agent and BMC views of the same host were not merged")
	}
	o, _ := s.Object(ctx, ilo.Objects["d"])
	if o.Attrs["vendor"] != "HPE" || o.Attrs["kernel"] != "6.8.0" {
		t.Fatalf("merged attributes %v", o.Attrs)
	}
}

func TestWeakMatchRefusesConflictingSerial(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{})
	b := batch(c, "redfish:a", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "AAA").Identify("name", "srv")
	first := apply(t, e, b)
	b = batch(c, "redfish:b", sdk.ScopeFull)
	b.Object("d", "device").Identify("serial", "BBB").Identify("name", "srv")
	if second := apply(t, e, b); second.Objects["d"] == first.Objects["d"] {
		t.Fatal("two serial numbers merged on a shared name")
	}
}

func TestWeakIdentityIsScopedByParent(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{})
	b := batch(c, "arista:sw1", sdk.ScopeFull)
	b.Object("a", "device").Identify("serial", "A")
	b.Object("b", "device").Identify("serial", "B")
	b.Object("ea", "interface").Identify("name", "Ethernet1").In("a")
	b.Object("eb", "interface").Identify("name", "Ethernet1").In("b")
	r := apply(t, e, b)
	if r.Objects["ea"] == r.Objects["eb"] {
		t.Fatal("Ethernet1 of two switches merged")
	}
}

func TestAbsenceAndReturn(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{AbsentAfter: 30 * time.Minute})
	ctx := context.Background()
	r := apply(t, e, switchBatch(c, "sw-a1", "192.0.2.10"))
	// The switch keeps reporting but Ethernet2 vanished (optic removed).
	c.advance(20 * time.Minute)
	b := switchBatch(c, "sw-a1", "192.0.2.10")
	b.Objects = b.Objects[:2]
	apply(t, e, b)
	c.advance(20 * time.Minute)
	gone, err := e.SweepAbsent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0] != r.Objects["e2"] {
		t.Fatalf("absent %v, want only Ethernet2", gone)
	}
	o, _ := s.Object(ctx, r.Objects["e2"])
	if o.Status != "absent" {
		t.Fatalf("status %s", o.Status)
	}
	if hist, _ := s.History(ctx, o.ID, "_status", 0); len(hist) != 2 || hist[0].Value != "absent" {
		t.Fatalf("status history %+v", hist)
	}
	c.advance(time.Minute)
	apply(t, e, switchBatch(c, "sw-a1", "192.0.2.10"))
	if o, _ = s.Object(ctx, r.Objects["e2"]); o.Status != "active" {
		t.Fatal("object did not come back")
	}
}

func TestDeclaredObjectsNeverGoAbsent(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{AbsentAfter: time.Minute})
	b := batch(c, "declare:site", sdk.ScopeFull)
	b.Confidence = sdk.Declared
	b.Object("r", "rack").Identify("name", "R1")
	apply(t, e, b)
	c.advance(24 * time.Hour)
	if gone, err := e.SweepAbsent(context.Background()); err != nil || len(gone) != 0 {
		t.Fatalf("declared rack swept: %v %v", gone, err)
	}
}

func TestMovedDiskChangesParent(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	b := batch(c, "redfish:a", sdk.ScopeFull)
	b.Object("a", "device").Identify("serial", "A")
	b.Object("d", "disk").Identify("serial", "DISK1").In("a")
	r1 := apply(t, e, b)
	c.advance(time.Hour)
	b = batch(c, "redfish:b", sdk.ScopeFull)
	b.Object("b", "device").Identify("serial", "B")
	b.Object("d", "disk").Identify("serial", "DISK1").In("b")
	r2 := apply(t, e, b)
	if r1.Objects["d"] != r2.Objects["d"] {
		t.Fatal("moved disk got a new identity")
	}
	o, _ := s.Object(ctx, r2.Objects["d"])
	if o.ParentID == nil || *o.ParentID != r2.Objects["b"] {
		t.Fatal("parent not updated")
	}
	if h, _ := s.History(ctx, o.ID, "_parent", 0); len(h) != 2 {
		t.Fatalf("parent history %+v", h)
	}
}

func TestPriorityRuleAndValueMismatch(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{Priorities: []reconcile.PriorityRule{{Attribute: "*.description", Sources: []string{"user:*"}}}})
	ctx := context.Background()
	b := batch(c, "arista:sw1", sdk.ScopeFull)
	b.Object("sw", "device").Identify("serial", "S").Set("description", "from the switch").Set("os_version", "4.20")
	r := apply(t, e, b)
	id := r.Objects["sw"]
	c.advance(time.Minute)
	u := batch(c, "user:vincent", sdk.ScopePartial)
	u.Confidence = sdk.Declared
	u.Object("x", "device").Identify(sdk.IDKey, id).Set("description", "core switch, room 1").Set("os_version", "4.28")
	apply(t, e, u)
	o, _ := s.Object(ctx, id)
	if o.Attrs["description"] != "core switch, room 1" {
		t.Fatalf("priority rule ignored: %v", o.Attrs["description"])
	}
	if o.Attrs["os_version"] != "4.20" {
		t.Fatalf("observed value should win by default: %v", o.Attrs["os_version"])
	}
	ds, n, err := reconcile.ListDiscrepancies(ctx, s, reconcile.DiscrepancyFilter{Status: "open", Kind: "value_mismatch"})
	if err != nil || n != 2 {
		t.Fatalf("mismatches %d %v %+v", n, err, ds)
	}
	// The user aligns the declared version: that gap closes.
	c.advance(time.Minute)
	u = batch(c, "user:vincent", sdk.ScopePartial)
	u.Confidence = sdk.Declared
	u.Object("x", "device").Identify(sdk.IDKey, id).Set("os_version", "4.20")
	apply(t, e, u)
	if _, n, _ = reconcile.ListDiscrepancies(ctx, s, reconcile.DiscrepancyFilter{Status: "open", Kind: "value_mismatch"}); n != 1 {
		t.Fatalf("open mismatches %d, want 1", n)
	}
}

func TestRelationsWithdrawnByFullScope(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	lldp := func(withNeighbour bool) *sdk.Batch {
		b := switchBatch(c, "sw-a1", "192.0.2.10")
		b.Object("peer", "device").Identify("serial", "PEER")
		b.Object("pe1", "interface").Identify("name", "ether1").In("peer")
		if withNeighbour {
			b.Relate("neighbor_of", "e1", "pe1").With("protocol", "lldp")
		}
		return b
	}
	r := apply(t, e, lldp(true))
	if r.RelationsOpened != 1 {
		t.Fatalf("opened %d", r.RelationsOpened)
	}
	c.advance(5 * time.Minute)
	if r2 := apply(t, e, lldp(true)); r2.RelationsOpened+r2.RelationsClosed != 0 {
		t.Fatal("unchanged relation reopened")
	}
	c.advance(5 * time.Minute)
	moved := c.t
	if r3 := apply(t, e, lldp(false)); r3.RelationsClosed != 1 {
		t.Fatalf("closed %d, want 1", r3.RelationsClosed)
	}
	rels, _ := s.RelationsOf(ctx, r.Objects["e1"], nil)
	if len(rels) != 0 {
		t.Fatalf("relation still current: %+v", rels)
	}
	at := moved.Add(-time.Minute)
	if rels, _ = s.RelationsOf(ctx, r.Objects["e1"], &at); len(rels) != 1 {
		t.Fatal("relation missing from the past")
	}
}

func TestFailedRunWithdrawsNothing(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	b := switchBatch(c, "sw-a1", "192.0.2.10")
	b.Object("p", "device").Identify("serial", "P")
	b.Object("pe", "interface").Identify("name", "x").In("p")
	b.Relate("neighbor_of", "e1", "pe")
	r := apply(t, e, b)
	c.advance(time.Minute)
	f := batch(c, "arista:sw1", sdk.ScopeFull)
	f.Health = &sdk.Health{Target: "192.0.2.10", OK: false, Error: "connection refused", Collector: "arista"}
	apply(t, e, f)
	if rels, _ := s.RelationsOf(ctx, r.Objects["e1"], nil); len(rels) != 1 {
		t.Fatal("a failed run withdrew relations")
	}
	var failures int
	if err := s.Pool.QueryRow(ctx, `SELECT failures FROM source_health WHERE source = 'arista:sw1'`).Scan(&failures); err != nil || failures != 1 {
		t.Fatalf("health %d %v", failures, err)
	}
}

func TestUnresolvedNeighbourOpensThenResolves(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	b := switchBatch(c, "sw-a1", "192.0.2.10")
	peer := sdk.Endpoint{Type: "interface", Identity: map[string]string{"name": "eno1"},
		Parent: &sdk.Endpoint{Type: "device", Identity: map[string]string{"name": "cont-svr1"}}}
	b.RelateEndpoints("neighbor_of", sdk.Ref("e1"), peer)
	r := apply(t, e, b)
	if len(r.Warnings) == 0 {
		t.Fatal("no warning for an unknown neighbour")
	}
	if _, n, _ := reconcile.ListDiscrepancies(ctx, s, reconcile.DiscrepancyFilter{Status: "open", Kind: "unresolved_neighbor"}); n != 1 {
		t.Fatalf("unresolved neighbours %d", n)
	}
	a := batch(c, "agent:cont-svr1", sdk.ScopeFull)
	a.Object("h", "device").Identify("hw_uuid", "U1").Identify("name", "cont-svr1")
	a.Object("n", "interface").Identify("name", "eno1").In("h")
	apply(t, e, a)
	c.advance(5 * time.Minute)
	b = switchBatch(c, "sw-a1", "192.0.2.10")
	b.RelateEndpoints("neighbor_of", sdk.Ref("e1"), peer)
	if r = apply(t, e, b); r.RelationsOpened != 1 {
		t.Fatalf("neighbour relation not created once resolvable: %+v", r)
	}
	if _, n, _ := reconcile.ListDiscrepancies(ctx, s, reconcile.DiscrepancyFilter{Status: "open", Kind: "unresolved_neighbor"}); n != 0 {
		t.Fatal("unresolved neighbour still open")
	}
}

func TestProposalFlow(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{Propose: []string{"mcp:*"}})
	ctx := context.Background()
	b := batch(c, "mcp:assistant", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("srv", "device").Identify("name", "new-srv").Set("role", "server")
	res, err := e.Submit(ctx, b, "claude", "document the new server in rack 2")
	if err != nil {
		t.Fatal(err)
	}
	if res.ProposalID == 0 || res.Created != 1 {
		t.Fatalf("not queued with a preview: %+v", res)
	}
	if _, n, _ := s.List(ctx, store.ListFilter{Type: "device"}); n != 0 {
		t.Fatal("proposal applied before approval")
	}
	again, _ := e.Submit(ctx, b, "claude", "same")
	if again.ProposalID != res.ProposalID {
		t.Fatal("identical proposal duplicated")
	}
	p, err := e.GetProposal(ctx, res.ProposalID)
	if err != nil || p.Preview == nil || p.Preview.Created != 1 {
		t.Fatalf("get proposal %+v %v", p, err)
	}
	applied, err := e.Approve(ctx, res.ProposalID, "vincent")
	if err != nil || applied.Created != 1 {
		t.Fatalf("approve %+v %v", applied, err)
	}
	if _, err := e.Approve(ctx, res.ProposalID, "vincent"); !errors.Is(err, reconcile.ErrConflict) {
		t.Fatalf("double approval: %v", err)
	}
	if _, n, _ := s.List(ctx, store.ListFilter{Type: "device"}); n != 1 {
		t.Fatal("approved proposal not applied")
	}
	b2 := batch(c, "mcp:assistant", sdk.ScopePartial)
	b2.Confidence = sdk.Declared
	b2.Object("x", "device").Identify("name", "other")
	r2, _ := e.Submit(ctx, b2, "claude", "")
	if err := e.Reject(ctx, r2.ProposalID, "vincent"); err != nil {
		t.Fatal(err)
	}
	ps, _ := e.ListProposals(ctx, "rejected", 0)
	if len(ps) != 1 {
		t.Fatalf("rejected proposals %d", len(ps))
	}
}

func TestInvalidBatches(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	b := batch(c, "x", sdk.ScopeFull)
	b.Object("a", "starship").Identify("name", "a")
	if _, err := e.Apply(ctx, b, "", ""); !errors.Is(err, reconcile.ErrInvalid) {
		t.Fatalf("unknown type: %v", err)
	}
	b = batch(c, "x", sdk.ScopeFull)
	b.Object("a", "device").Identify("base_mac", "not-a-mac")
	if _, err := e.Apply(ctx, b, "", ""); !errors.Is(err, reconcile.ErrInvalid) {
		t.Fatalf("bad identity: %v", err)
	}
	b = batch(c, "x", sdk.ScopeFull)
	b.Object("a", "device").Identify("serial", "Z").Set("warp_factor", 9).Set("cpu_count", "two")
	r := apply(t, e, b)
	if len(r.Warnings) != 2 {
		t.Fatalf("warnings %v", r.Warnings)
	}
}

func TestPreviewDoesNotPersist(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	res, err := e.Preview(ctx, switchBatch(c, "sw-a1", "192.0.2.10"))
	if err != nil || res.Created != 3 {
		t.Fatalf("preview %+v %v", res, err)
	}
	if _, n, _ := s.List(ctx, store.ListFilter{}); n != 0 {
		t.Fatal("preview persisted objects")
	}
}

func TestWithdrawAndUnrelate(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	b := batch(c, "user:v", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("r", "rack").Identify("name", "R1")
	b.Object("d", "device").Identify("name", "srv").Set("description", "temp")
	b.Relate("mounted_in", "d", "r").With("position", 10).With("face", "front")
	r := apply(t, e, b)
	c.advance(time.Minute)
	u := batch(c, "user:v", sdk.ScopePartial)
	u.Confidence = sdk.Declared
	u.Object("d", "device").Identify(sdk.IDKey, r.Objects["d"]).Unset("description")
	u.Unrelate("mounted_in", sdk.Ref("d"), sdk.ByID("rack", r.Objects["r"]))
	res := apply(t, e, u)
	if res.RelationsClosed != 1 || res.ValuesChanged != 1 {
		t.Fatalf("withdraw: %+v", res)
	}
	o, _ := s.Object(ctx, r.Objects["d"])
	if _, ok := o.Attrs["description"]; ok {
		t.Fatal("description still set")
	}
}

// Moving a device to another U of the same rack changes the attributes of a
// relation that stays open. The preview must list it: a person who previews
// such a move from the UI would otherwise read "nothing would change".
func TestPreviewShowsARelationWhoseAttributesChange(t *testing.T) {
	e, _, c := newEngine(t, reconcile.Policy{})
	ctx := context.Background()
	mount := func(position int) *sdk.Batch {
		b := batch(c, "user:v", sdk.ScopePartial)
		b.Confidence = sdk.Declared
		b.Object("r", "rack").Identify("name", "R1")
		b.Object("d", "device").Identify("name", "pdu-1")
		b.Relate("mounted_in", "d", "r").With("position", position).With("face", "rear")
		return b
	}
	r := apply(t, e, mount(10))
	c.advance(time.Minute)
	same, err := e.Preview(ctx, mount(10))
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range same.Changes {
		if ch.Op == "relate" {
			t.Fatalf("the same mount again is reported as a change: %+v", ch)
		}
	}
	moved, err := e.Preview(ctx, mount(12))
	if err != nil {
		t.Fatal(err)
	}
	var got *reconcile.PlannedChange
	for i, ch := range moved.Changes {
		if ch.Op == "relate" && ch.Key == "mounted_in" {
			got = &moved.Changes[i]
		}
	}
	if got == nil || got.ObjectID != r.Objects["d"] || got.OtherID != r.Objects["r"] {
		t.Fatalf("changes %+v, want the mount of pdu-1 in R1", moved.Changes)
	}
	before, _ := got.Before.(map[string]any)
	after, _ := got.After.(map[string]any)
	if before["position"] != float64(10) || after["position"] != float64(12) || after["face"] != "rear" {
		t.Fatalf("mount change %v -> %v, want position 10 -> 12 on the rear", got.Before, got.After)
	}
	if moved.RelationsOpened != 0 {
		t.Fatalf("relations opened %d, want 0: the link stays, its attributes change", moved.RelationsOpened)
	}
}
