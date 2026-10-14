package graph

import (
	"errors"
	"testing"
)

func ids(hops []Hop) []string {
	out := make([]string, len(hops))
	for i, h := range hops {
		out[i] = h.Node.ID
	}
	return out
}

func TestNeighbors(t *testing.T) {
	s := lab().snapshot()

	h, err := s.Neighbors("srv-single", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(h)
	want := []string{"srv-single", "srv-single/e1", "srv-single/e2", "vm1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if h[0].Via != nil || h[0].Depth != 0 {
		t.Error("the start has no edge")
	}
	if h[3].Via.Type != "hosts" || h[1].Via.Type != ParentRelation {
		t.Errorf("edges: %+v %+v", h[3].Via, h[1].Via)
	}

	h, _ = s.Neighbors("srv-single", 3, []string{"hosts", "runs_on", "depends_on"})
	if got := ids(h); len(got) != 4 || got[3] != "app" || h[3].Depth != 3 {
		t.Errorf("filtered walk: %v", got)
	}

	h, _ = s.Neighbors("vm1", 1, []string{ParentRelation})
	if len(h) != 1 {
		t.Errorf("vm1 has no parent nor child: %v", ids(h))
	}
	h, _ = s.Neighbors("srv-dual/e1", 1, []string{ParentRelation})
	if len(h) != 2 || h[1].Node.ID != "srv-dual" {
		t.Errorf("parent: %v", ids(h))
	}

	if _, err := s.Neighbors("nope", 1, nil); !errors.Is(err, ErrUnknownObject) {
		t.Errorf("got %v", err)
	}
}

func TestShortestPath(t *testing.T) {
	s := lab().snapshot()

	p, err := s.ShortestPath("srv-single", "fw1", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"srv-single", "srv-single/e1", "sw2/e2", "sw2", "sw2/e1", "sw1/e3", "sw1", "sw1/e1", "fw1/e1", "fw1"}
	got := ids(p)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] || p[i].Depth != i {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if p[0].Via != nil || p[2].Via.Type != "cabled_to" {
		t.Errorf("edges: %+v", p[2].Via)
	}

	p, _ = s.ShortestPath("vm1", "vm1", nil)
	if len(p) != 1 {
		t.Errorf("a path to itself: %v", ids(p))
	}
	p, _ = s.ShortestPath("srv-lonely", "fw1", nil)
	if p != nil {
		t.Errorf("no path expected: %v", ids(p))
	}
	p, _ = s.ShortestPath("srv-single", "fw1", []string{"cabled_to"})
	if p != nil {
		t.Errorf("without containment the server cannot reach its ports: %v", ids(p))
	}
	for _, pair := range [][2]string{{"nope", "fw1"}, {"fw1", "nope"}} {
		if _, err := s.ShortestPath(pair[0], pair[1], nil); !errors.Is(err, ErrUnknownObject) {
			t.Errorf("%v: got %v", pair, err)
		}
	}
}
