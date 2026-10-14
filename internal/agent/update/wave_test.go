package update

import (
	"fmt"
	"math"
	"testing"
)

func TestInWaveEdges(t *testing.T) {
	for _, tc := range []struct {
		percent float64
		want    bool
	}{{0, false}, {-5, false}, {math.NaN(), false}, {100, true}, {250, true}} {
		if got := InWave(testIdentity, "1.2.3", tc.percent); got != tc.want {
			t.Errorf("InWave(%v) = %v, want %v", tc.percent, got, tc.want)
		}
	}
}

func TestBucketIsStableAndSpansTheRange(t *testing.T) {
	first := Bucket("agent:a", "1.0.0")
	if again := Bucket("agent:a", "1.0.0"); first != again {
		t.Fatal("bucket is not deterministic")
	}
	// A frozen value: if the hash changes, agents change wave mid-rollout.
	if got := Bucket("agent:host-a1", "1.2.3"); got != 3594 {
		t.Errorf("Bucket(host-a1, 1.2.3) = %d: the wave hash changed, which reshuffles every rollout in progress", got)
	}
}

func TestWaveShareFollowsThePercentage(t *testing.T) {
	const n = 20000
	for _, percent := range []float64{1, 5, 25, 50, 90} {
		in := 0
		for i := range n {
			if InWave(fmt.Sprintf("agent:host-%d", i), "2.0.0", percent) {
				in++
			}
		}
		if got := float64(in) / n * 100; math.Abs(got-percent) > 1.5 {
			t.Errorf("%v %% wave selected %.2f %%", percent, got)
		}
	}
}

func TestWidenedWaveOnlyAddsAgents(t *testing.T) {
	for i := range 3000 {
		id := fmt.Sprintf("agent:host-%d", i)
		if InWave(id, "2.0.0", 10) && !InWave(id, "2.0.0", 30) {
			t.Fatalf("%s is in the 10 %% wave but not in the 30 %% one", id)
		}
	}
}

func TestCanariesChangeFromOneReleaseToTheNext(t *testing.T) {
	same := 0
	first := 0
	for i := range 5000 {
		id := fmt.Sprintf("agent:host-%d", i)
		a, b := InWave(id, "2.0.0", 10), InWave(id, "2.0.1", 10)
		if a {
			first++
			if b {
				same++
			}
		}
	}
	if first == 0 || float64(same)/float64(first) > 0.3 {
		t.Errorf("%d of %d first-wave agents of 2.0.0 are also in the first wave of 2.0.1: waves are not reshuffled per release", same, first)
	}
}
