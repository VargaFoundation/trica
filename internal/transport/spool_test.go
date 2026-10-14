package transport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpoolOrderAtomicityAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	frozen := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s, err := newSpool(dir, 1<<20, func() time.Time { return frozen })
	if err != nil {
		t.Fatal(err)
	}
	// A leftover of a crash between write and rename is not a batch.
	stale := filepath.Join(dir, spoolTmp+"crash")
	if err := os.WriteFile(stale, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"a", "b", "c"} {
		if err := s.add([]byte(b)); err != nil { // the clock does not move: the order must still hold
			t.Fatal(err)
		}
	}
	list, err := s.list()
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %v, %v", list, err)
	}
	var got []string
	for _, e := range list {
		b, err := s.read(e.name)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(b))
	}
	if strings.Join(got, "") != "abc" {
		t.Fatalf("order = %v", got)
	}
	// Reopening removes the stale temp file, keeps the batches, and continues the sequence.
	s2, err := newSpool(dir, 1<<20, func() time.Time { return frozen.Add(-time.Hour) }) // a clock gone backwards
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the stale temporary file must be removed")
	}
	if err := s2.add([]byte("d")); err != nil {
		t.Fatal(err)
	}
	list, _ = s2.list()
	last, _ := s2.read(list[len(list)-1].name)
	if len(list) != 4 || string(last) != "d" {
		t.Fatalf("a batch added after a restart must sort last: %v", list)
	}
	// Only complete batch files in the directory.
	for _, e := range spoolFiles(t, dir) {
		if !strings.HasSuffix(e, spoolSuffix) {
			t.Fatalf("unexpected file %q in the spool", e)
		}
	}
	n, size := s2.stats()
	if n != 4 || size != 4 {
		t.Fatalf("stats = %d, %d", n, size)
	}
	if err := s2.remove(list[0].name); err != nil {
		t.Fatal(err)
	}
	if err := s2.remove(list[0].name); err != nil {
		t.Fatalf("removing twice must be harmless: %v", err)
	}
}

func TestSpoolEvictsOldestFirst(t *testing.T) {
	s, err := newSpool(t.TempDir(), 10, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"1111", "2222", "3333"} { // 12 bytes into 10: "1111" goes
		if err := s.add([]byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	if s.dropped != 1 {
		t.Fatalf("dropped = %d", s.dropped)
	}
	list, _ := s.list()
	first, _ := s.read(list[0].name)
	if len(list) != 2 || string(first) != "2222" {
		t.Fatalf("the oldest must be evicted, not the newest: %v %s", list, first)
	}
	if err := s.add([]byte(strings.Repeat("x", 11))); !errors.Is(err, ErrSpoolFull) || s.dropped != 2 {
		t.Fatalf("err = %v, dropped = %d", err, s.dropped)
	}
	if n, _ := s.stats(); n != 2 {
		t.Fatal("an oversized batch must not evict the others")
	}
}

func TestSpoolDirectoryErrors(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSpool(filepath.Join(f, "sub"), 10, time.Now); err == nil {
		t.Fatal("a spool under a regular file must fail")
	}
}
