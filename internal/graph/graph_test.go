package graph

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

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
