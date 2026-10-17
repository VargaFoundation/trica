package api_test

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// fakeLive serves a fixed snapshot.
type fakeLive struct{ snap *live.Snapshot }

func (f fakeLive) Snapshot(context.Context) *live.Snapshot { return f.snap }

type liveBody struct {
	Enabled        bool                 `json:"enabled"`
	Stale          bool                 `json:"stale"`
	GeneratedAt    *time.Time           `json:"generated_at"`
	RefreshSeconds int                  `json:"refresh_seconds"`
	Links          map[string]live.Link `json:"links"`
	Sources        []live.SourceStatus  `json:"sources"`
}

func (e *env) withLive(src api.LiveSource) {
	e.t.Helper()
	srv, err := api.New(api.Config{Engine: e.eng, Authorizer: e.auth, Auditor: e.aud, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test", Now: e.clk.now, Live: src})
	if err != nil {
		e.t.Fatal(err)
	}
	e.srv, e.h = srv, srv.Handler()
}

func (e *env) live(token string) liveBody {
	e.t.Helper()
	r := e.get("/api/v1/topology/live", token)
	wantStatus(e.t, r, 200)
	var b liveBody
	r.json(e.t, &b)
	return b
}

func keys(m map[string]live.Link) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestLiveTopologyDisabledWithoutLiveBlock(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := e.live(tokReader)
	if b.Enabled || b.Links == nil || len(b.Links) != 0 || b.Sources == nil || b.GeneratedAt != nil {
		t.Fatalf("%+v", b)
	}
	wantProblem(t, e.get("/api/v1/topology/live", ""), 401, "unauthenticated")
	wantProblem(t, e.get("/api/v1/topology/live", tokProposeNR), 403, "forbidden")
}

func TestLiveTopologyIsFilteredByTheCallersRights(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.cabling() // eno1 on srv-01, in Lyon
	rx, util := 1e9, 0.1
	at := t0.Add(time.Minute)
	e.withLive(fakeLive{&live.Snapshot{GeneratedAt: &at, Stale: true, Refresh: 30 * time.Second,
		Links: map[string]live.Link{
			e.ids["e1"]:   {RX: &rx, Util: &util, Oper: "up", Source: "arista"},
			e.ids["e2"]:   {Oper: "down", Source: "arista"},
			e.ids["eno1"]: {RX: &rx, Source: "node"},
		},
		Sources: []live.SourceStatus{{Name: "arista", Kind: "arista", Series: 3, Matched: 2}},
	}})

	all := e.live(tokReader)
	if !all.Enabled || !all.Stale || all.RefreshSeconds != 30 || all.GeneratedAt == nil || !all.GeneratedAt.Equal(at) {
		t.Fatalf("%+v", all)
	}
	if len(all.Links) != 3 || *all.Links[e.ids["e1"]].RX != 1e9 || len(all.Sources) != 1 {
		t.Fatalf("reader of everything: %+v", all)
	}
	// Limited to devices: no interface is readable, and the sources are not
	// described.
	if d := e.live(tokDevices); !d.Enabled || len(d.Links) != 0 || len(d.Sources) != 0 {
		t.Fatalf("devices only: %+v", d)
	}
	// Limited to Paris: the switch's ports, not the Lyon server's.
	p := e.live(tokSiteParis)
	want := []string{e.ids["e1"], e.ids["e2"]}
	sort.Strings(want)
	if got := keys(p.Links); len(got) != 2 || got[0] != want[0] || got[1] != want[1] || len(p.Sources) != 0 {
		t.Fatalf("Paris: %v, want %v", got, want)
	}
}

// The UI groups the members of an aggregate from GET /relations: member_of
// from an interface to an interface, both ends with their device as parent.
func TestRelationsListAggregateMembership(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := sdk.NewBatch("arista:sw-a1", sdk.ScopePartial)
	b.Object("sw", "device").Identify("serial", "JPE-SW-A1")
	b.Object("po", "interface").Identify("name", "Port-Channel10").In("sw").Set("kind", "lag")
	b.Object("e1", "interface").Identify("name", "Ethernet1").In("sw")
	b.Object("e2", "interface").Identify("name", "Ethernet2").In("sw")
	b.Object("v10", "vlan").Identify("key", "default:10").Set("vid", 10)
	b.Relate("member_of", "e1", "po")
	b.Relate("member_of", "e2", "po")
	b.Relate("member_of", "po", "v10")
	e.apply(b)

	var lag []link
	for _, l := range links(t, e.page("/api/v1/relations?type=member_of", tokReader)) {
		if l.From.Type == "interface" && l.To.Type == "interface" {
			lag = append(lag, l)
		}
	}
	if len(lag) != 2 {
		t.Fatalf("aggregate memberships: %+v", lag)
	}
	for _, l := range lag {
		if l.ToID != e.ids["po"] || l.From.ParentID != e.ids["sw"] || l.To.ParentID != e.ids["sw"] || l.To.Display == "" {
			t.Fatalf("membership %+v", l)
		}
	}
}
