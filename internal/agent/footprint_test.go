package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The agent's budget is under 50 MB resident and under 1 % of one core. These
// tests watch what a Go test can watch: the heap after collection and the
// allocation and time of a run, over a fake root as big as a real server's
// package database. The real RSS and CPU of the binary on a real host are not
// measurable here (a fake root has no live /proc, and the runtime's own
// overhead is not in a heap figure): they are measured on a host in the
// release pipeline, later.
const (
	footprintRuns     = 100
	footprintPackages = 2000 // a busy Debian server has 1500 to 3000 installed packages

	// Measured on the development machine (amd64, Go 1.26) over this root: heap in
	// use after GC 1.7 MB, 4.7 MB allocated per run, 5 ms per run. The ceilings
	// are several times that, to catch a leak or a quadratic blow-up, not to
	// track noise.
	maxHeapInUse    = 20 << 20          // the budget; the measured figure is a tenth of it
	maxAllocPerRun  = 16 << 20          // 3.4x the measured figure
	maxMeanDuration = MinInterval / 100 // 1 % of one core at the shortest interval
)

// bigRoot copies the Debian fixture and gives it a package database of n packages.
func bigRoot(tb testing.TB, n int) string {
	tb.Helper()
	dst := tb.TempDir()
	copyTree(tb, filepath.Join("testdata", "debian12", "root"), dst)
	p := filepath.Join(dst, "var", "lib", "dpkg", "status")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "\nPackage: lib-test-%04d\nStatus: install ok installed\nPriority: optional\nSection: libs\nInstalled-Size: %d\n"+
			"Maintainer: Example Maintainer <maint@example.invalid>\nArchitecture: amd64\nVersion: 1.%d.%d-1\n"+
			"Depends: libc6 (>= 2.34), lib-test-%04d (= 1.0)\nDescription: synthetic package %d\n a long description line to give the stanza a realistic size,\n"+
			" as the real ones have, repeated a couple of times to the same effect.\n", i, 100+i, i%50, i%7, (i+1)%n, i)
	}
	if _, err := f.WriteString(sb.String()); err != nil {
		tb.Fatal(err)
	}
	return dst
}

func footprintAgent(tb testing.TB, root string) *Agent {
	tb.Helper()
	fsys, err := NewRootFS(root)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { fsys.Close() })
	a, err := New(Config{FS: fsys, Runner: newFakeRunner("debian12"), Version: "test", Logger: quietLogger()})
	if err != nil {
		tb.Fatal(err)
	}
	return a
}

var discard = sdk.EmitterFunc(func(context.Context, *sdk.Batch) error { return nil })

func TestFootprint(t *testing.T) {
	a := footprintAgent(t, bigRoot(t, footprintPackages))

	// A first run warms the runtime up (caches, goroutine stacks); it is not what a long-lived agent costs.
	var probe *sdk.Batch
	if err := a.Collect(context.Background(), sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error { probe = b; return nil })); err != nil {
		t.Fatal(err)
	}
	if n := len(objectsOf(probe, "software")); n < footprintPackages {
		t.Fatalf("only %d software objects: the big root is not read", n)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i := 0; i < footprintRuns; i++ {
		if err := a.Collect(context.Background(), discard); err != nil {
			t.Fatal(err)
		}
	}
	mean := time.Since(start) / footprintRuns
	runtime.GC()
	runtime.ReadMemStats(&after)

	perRun := (after.TotalAlloc - before.TotalAlloc) / footprintRuns
	t.Logf("%d packages, %d runs: heap in use after GC %.1f MB, %.1f MB allocated per run, %v per run (%.3f %% of a core at the %v minimum interval)",
		footprintPackages, footprintRuns, float64(after.HeapInuse)/(1<<20), float64(perRun)/(1<<20), mean.Round(time.Millisecond),
		100*float64(mean)/float64(MinInterval), MinInterval)

	if after.HeapInuse > maxHeapInUse {
		t.Errorf("heap in use after GC %d MB, budget %d MB: something is retained between runs", after.HeapInuse>>20, maxHeapInUse>>20)
	}
	if perRun > maxAllocPerRun {
		t.Errorf("%d MB allocated per run, ceiling %d MB", perRun>>20, maxAllocPerRun>>20)
	}
	if mean > maxMeanDuration {
		t.Errorf("a run takes %v on average, above 1 %% of the %v minimum interval (%v)", mean, MinInterval, maxMeanDuration)
	}
}

func TestFootprintHeapDoesNotGrowWithRuns(t *testing.T) {
	// A leak shows as growth between the first and the last runs, whatever the absolute figure.
	a := footprintAgent(t, bigRoot(t, 500))
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapInuse
	}
	for i := 0; i < 5; i++ {
		_ = a.Collect(context.Background(), discard)
	}
	first := heap()
	for i := 0; i < 50; i++ {
		_ = a.Collect(context.Background(), discard)
	}
	last := heap()
	if last > first+2<<20 {
		t.Fatalf("heap in use grew from %d KB to %d KB over 50 runs", first>>10, last>>10)
	}
}

func BenchmarkCollect(b *testing.B) {
	a := footprintAgent(b, bigRoot(b, footprintPackages))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.Collect(context.Background(), discard); err != nil {
			b.Fatal(err)
		}
	}
}
