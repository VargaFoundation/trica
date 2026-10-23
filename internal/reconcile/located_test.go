package reconcile_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// placed loads a site with two rooms and an access point, and returns their ids.
func placed(t *testing.T) (*reconcile.Engine, *store.Store, *clock, map[string]string) {
	t.Helper()
	e, s, c := newEngine(t, reconcile.Policy{})
	b := batch(c, "declaration:test", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("site", "site").Identify("code", "X").Set("name", "x")
	b.Object("r1", "room").Identify("name", "salle-1").In("site")
	b.Object("r2", "room").Identify("name", "salle-2").In("site")
	b.Object("ap", "device").Identify("name", "ap-1").Set("role", "access_point")
	res := apply(t, e, b)
	return e, s, c, res.Objects
}

func locate(c *clock, source string, conf sdk.Confidence, ids map[string]string, rooms ...string) *sdk.Batch {
	b := batch(c, source, sdk.ScopePartial)
	b.Confidence = conf
	for _, r := range rooms {
		b.RelateEndpoints("located_in", sdk.ByID("device", ids["ap"]), sdk.ByID("room", ids[r]))
	}
	return b
}

// open returns the rooms the device is currently located in, with the source.
func open(t *testing.T, s *store.Store, id string) map[string]string {
	t.Helper()
	rels, err := s.RelationsOf(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rels {
		if r.Type == "located_in" && r.ValidTo == nil && r.FromID == id {
			out[r.ToID] = r.Source
		}
	}
	return out
}

func TestOnePlacePerSource(t *testing.T) {
	e, s, c, ids := placed(t)
	apply(t, e, locate(c, "user:a", sdk.Declared, ids, "r1"))
	c.advance(time.Minute)
	res := apply(t, e, locate(c, "user:a", sdk.Declared, ids, "r2"))
	if res.RelationsClosed != 1 || res.RelationsOpened != 1 {
		t.Fatalf("moving: %+v, want one relation closed and one opened", res)
	}
	got := open(t, s, ids["ap"])
	if len(got) != 1 || got[ids["r2"]] != "user:a" {
		t.Fatalf("open places %v, want only r2", got)
	}
	// The history keeps the closed one.
	var closed int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM relations WHERE type = 'located_in' AND from_id = $1 AND to_id = $2 AND NOT upper_inf(valid)`, ids["ap"], ids["r1"]).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("closed relations kept in history: %d, want 1", closed)
	}
}

func TestMovingLeavesOtherSourcesAlone(t *testing.T) {
	e, s, c, ids := placed(t)
	apply(t, e, locate(c, "user:b", sdk.Declared, ids, "r1"))
	c.advance(time.Minute)
	apply(t, e, locate(c, "user:a", sdk.Declared, ids, "r1"))
	c.advance(time.Minute)
	res := apply(t, e, locate(c, "user:a", sdk.Declared, ids, "r2"))
	// user:b still says r1, so the relation stays open: only user:a's claim went.
	if res.RelationsClosed != 0 {
		t.Fatalf("moving closed %d relations while another source still claims r1", res.RelationsClosed)
	}
	got := open(t, s, ids["ap"])
	if got[ids["r2"]] != "user:a" || len(got) != 2 {
		t.Fatalf("open places %v: want r1 (user:b's) and r2 (user:a's)", got)
	}
	var n int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM rel_claims WHERE type = 'located_in' AND source = 'user:b' AND to_id = $1`, ids["r1"]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("user:b's claim: %d, %v", n, err)
	}
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM rel_claims WHERE type = 'located_in' AND source = 'user:a' AND to_id = $1`, ids["r1"]).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user:a's old claim: %d, %v", n, err)
	}
}

func TestPreviewShowsTheMoveAsAnUnrelation(t *testing.T) {
	e, _, c, ids := placed(t)
	apply(t, e, locate(c, "user:a", sdk.Declared, ids, "r1"))
	c.advance(time.Minute)
	res, err := e.Preview(context.Background(), locate(c, "user:a", sdk.Declared, ids, "r2"))
	if err != nil {
		t.Fatal(err)
	}
	unrelates := 0
	for _, ch := range res.Changes {
		if ch.Op == "unrelate" && ch.Key == "located_in" {
			unrelates++
		}
	}
	if res.RelationsClosed != 1 || unrelates != 1 {
		t.Fatalf("preview of a move: %+v", res)
	}
}

func TestTwoPlacesInOneBatch(t *testing.T) {
	cases := []struct {
		source string
		reject bool
	}{{"user:a", true}, {"declaration:file", false}}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			e, s, c, ids := placed(t)
			res, err := e.Apply(context.Background(), locate(c, tc.source, sdk.Declared, ids, "r1", "r2"), "", "")
			if tc.reject {
				if !errors.Is(err, reconcile.ErrRejected) {
					t.Fatalf("err = %v, want ErrRejected", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Warnings) != 1 || len(open(t, s, ids["ap"])) != 1 {
				t.Fatalf("warnings %v, places %v", res.Warnings, open(t, s, ids["ap"]))
			}
		})
	}
}

func TestCollectorsDoNotPlaceDevices(t *testing.T) {
	e, s, c, ids := placed(t)
	res := apply(t, e, locate(c, "unifi:console", sdk.Observed, ids, "r1"))
	if len(res.Warnings) != 1 || len(open(t, s, ids["ap"])) != 0 {
		t.Fatalf("an observed located_in must be dropped with a warning: warnings %v, places %v", res.Warnings, open(t, s, ids["ap"]))
	}
	// A person's token claiming a weaker confidence is refused outright.
	if _, err := e.Apply(context.Background(), locate(c, "user:a", sdk.Observed, ids, "r1"), "", ""); !errors.Is(err, reconcile.ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
}
