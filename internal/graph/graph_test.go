package graph

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

func TestBuild(t *testing.T) {
	w := lab()
	w.add("orphan", "interface", "gone", nil)
	w.rel("cabled_to", "sw1/e1", "gone").rel("unknown_rel", "sw1", "sw2")
	s := w.snapshot()

	if s.Node("orphan") == nil || s.Node("gone") != nil {
		t.Fatal("nodes")
	}
	if len(s.Children["gone"]) != 0 {
		t.Error("a missing parent gets no children list")
	}
	if c := s.Children["srv-dual"]; len(c) != 4 || c[0] != "srv-dual/e1" {
		t.Errorf("children are sorted: %v", c)
	}
	for _, e := range s.Out["sw1/e1"] {
		if e.To == "gone" {
			t.Error("an edge to an unknown object is dropped")
		}
	}
	var impact = map[string]string{}
	for _, e := range s.Out["sw1"] {
		impact[e.Type] = e.Impact
	}
	if impact["unknown_rel"] != "" {
		t.Error("an unknown relation type has no impact mode")
	}
	if e := s.Out["srv-single"][0]; e.Type != "hosts" || e.Impact != schema.ImpactFromTo {
		t.Errorf("impact mode comes from the registry: %+v", e)
	}
	if len(s.In["vm1"]) != 2 {
		t.Error("in edges")
	}
	if Build(w.objs, w.rels, nil).Out["srv-single"][0].Impact != "" {
		t.Error("without a registry, no impact mode")
	}
}

type fakeLoader struct {
	w       *world
	loads   atomic.Int32
	objErr  error
	relErr  error
	release chan struct{}
}

func (f *fakeLoader) AllObjects(context.Context) ([]*store.Object, error) {
	f.loads.Add(1)
	if f.release != nil {
		<-f.release
	}
	return f.w.objs, f.objErr
}

func (f *fakeLoader) CurrentRelations(context.Context) ([]store.Relation, error) {
	return f.w.rels, f.relErr
}

func TestCacheReloadsOnlyWhenInvalidated(t *testing.T) {
	l := &fakeLoader{w: lab()}
	c := NewCache(l, schema.Builtin)
	ctx := context.Background()

	s1, err := c.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := c.Get(ctx)
	if s1 != s2 || l.loads.Load() != 1 {
		t.Fatalf("a clean cache does not reload (loads=%d)", l.loads.Load())
	}
	if s1.Out["srv-single"][0].Impact != schema.ImpactFromTo {
		t.Error("the cache passes the registry to Build")
	}
	c.Invalidate()
	s3, _ := c.Get(ctx)
	if s3 == s1 || l.loads.Load() != 2 {
		t.Fatalf("an invalidated cache reloads (loads=%d)", l.loads.Load())
	}
}

func TestCacheStaysDirtyOnError(t *testing.T) {
	ctx := context.Background()
	for _, l := range []*fakeLoader{
		{w: lab(), objErr: errors.New("boom")},
		{w: lab(), relErr: errors.New("boom")},
	} {
		c := NewCache(l, nil)
		if _, err := c.Get(ctx); err == nil {
			t.Fatal("error expected")
		}
		l.objErr, l.relErr = nil, nil
		s, err := c.Get(ctx)
		if err != nil || s == nil {
			t.Fatalf("a failed load is retried: %v", err)
		}
		if s.Out["srv-single"][0].Impact != "" {
			t.Error("no registry function, no impact modes")
		}
	}
}

func TestCacheCoalescesConcurrentReloads(t *testing.T) {
	l := &fakeLoader{w: lab(), release: make(chan struct{})}
	c := NewCache(l, schema.Builtin)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	close(l.release)
	wg.Wait()
	if n := l.loads.Load(); n != 1 {
		t.Errorf("8 concurrent readers of a stale cache cost %d loads, want 1", n)
	}
}

func TestCacheInvalidatedDuringLoadReloadsAgain(t *testing.T) {
	l := &fakeLoader{w: lab(), release: make(chan struct{})}
	c := NewCache(l, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Refresh(context.Background())
	}()
	for l.loads.Load() == 0 {
		runtime.Gosched() // wait for the load to start
	}
	c.Invalidate() // a commit lands while loading
	close(l.release)
	<-done
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := l.loads.Load(); n != 2 {
		t.Errorf("a commit during a load must trigger another load, got %d loads", n)
	}
}

// fakeClock is a settable clock for the staleness tests.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func TestRecentServesWithinMaxStale(t *testing.T) {
	l := &fakeLoader{w: lab()}
	clk := &fakeClock{t: time.Unix(1_000, 0)}
	c := NewCache(l, nil)
	c.now = clk.now
	ctx := context.Background()
	s1, err := c.Recent(ctx, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Invalidate() // a commit
	clk.t = clk.t.Add(9 * time.Second)
	s2, _ := c.Recent(ctx, 10*time.Second)
	if s2 != s1 || l.loads.Load() != 1 {
		t.Fatalf("a snapshot 9 s into its staleness is served as it is (loads=%d)", l.loads.Load())
	}
	// Get never serves a dirty snapshot: the MCP tools keep their contract.
	if s3, _ := c.Get(ctx); s3 == s1 || l.loads.Load() != 2 {
		t.Fatalf("Get reloads a dirty cache (loads=%d)", l.loads.Load())
	}
}

func TestRecentReloadsPastMaxStale(t *testing.T) {
	l := &fakeLoader{w: lab()}
	clk := &fakeClock{t: time.Unix(1_000, 0)}
	c := NewCache(l, nil)
	c.now = clk.now
	ctx := context.Background()
	s1, _ := c.Recent(ctx, 10*time.Second)
	c.Invalidate()
	clk.t = clk.t.Add(5 * time.Second)
	c.Invalidate() // a later commit does not restart the clock
	clk.t = clk.t.Add(6 * time.Second)
	s2, err := c.Recent(ctx, 10*time.Second)
	if err != nil || s2 == s1 || l.loads.Load() != 2 {
		t.Fatalf("a snapshot stale for 11 s is reloaded (loads=%d, err=%v)", l.loads.Load(), err)
	}
	// Clean again: no reload, whatever the age.
	clk.t = clk.t.Add(time.Hour)
	if s3, _ := c.Recent(ctx, 10*time.Second); s3 != s2 || l.loads.Load() != 2 {
		t.Fatalf("a clean snapshot is never reloaded (loads=%d)", l.loads.Load())
	}
}

func TestBuildCarriesConfidenceAndAttrs(t *testing.T) {
	w := lab()
	w.rels = append(w.rels, store.Relation{Type: "mounted_in", FromID: "srv-single", ToID: "sw1", Confidence: "declared", Attrs: map[string]any{"position": 3.0}})
	s := w.snapshot()
	var got *Edge
	for i, e := range s.Out["srv-single"] {
		if e.Type == "mounted_in" {
			got = &s.Out["srv-single"][i]
		}
	}
	if got == nil || got.Confidence != "declared" || got.Attrs["position"] != 3.0 {
		t.Fatalf("out edge: %+v", got)
	}
	if in := s.In["sw1"]; in[len(in)-1].Confidence != "declared" {
		t.Fatalf("in edge: %+v", in)
	}
}

// MCP serves hops as JSON: the topology map's extra fields must not leak.
func TestHopJSONUnchanged(t *testing.T) {
	e := Edge{Type: "cabled_to", From: "a", To: "b", Impact: "link", Confidence: "declared", Attrs: map[string]any{"x": 1}}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"cabled_to","from":"a","to":"b","impact":"link"}`; string(raw) != want {
		t.Fatalf("%s, want %s", raw, want)
	}
}
