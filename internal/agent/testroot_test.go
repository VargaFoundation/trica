package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func contains(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }

func containsPrefix(ss []string, prefix string) bool {
	for _, s := range ss {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func containsSub(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func asSkip(err error, target *skipError) bool { return errors.As(err, target) }

// writeFile writes a file below dir, creating its directories.
func writeFile(t testing.TB, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// recordFS remembers every file the probes open.
type recordFS struct {
	FS
	mu    sync.Mutex
	names []string
}

func (r *recordFS) Open(name string) (io.ReadCloser, error) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	return r.FS.Open(name)
}

func (r *recordFS) opened() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

// tmpRoot builds a throwaway host from a map of path -> content and opens it.
// A path ending in "/" is an empty directory.
func tmpRoot(t testing.TB, files map[string]string) *RootFS {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if name[len(name)-1] == '/' {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewRootFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// probeAfter runs the named probes in order (a probe may need what an earlier
// one found) and returns the inventory and the error of the last one.
func probeAfter(t testing.TB, fsys FS, run Runner, addrs AddrSource, names ...string) (*Inventory, error) {
	t.Helper()
	if run == nil {
		run = noRunner{}
	}
	if addrs == nil {
		addrs = fsAddrs{path: "/addrs.txt"}
	}
	c := &collector{ctx: context.Background(), fs: fsys, run: run, addrs: addrs, inv: &Inventory{}}
	var last error
	for _, n := range names {
		found := false
		for _, p := range probes {
			if p.name == n {
				found = true
				last = safely(p.fn, c)
			}
		}
		if !found {
			t.Fatalf("no probe %q", n)
		}
	}
	return c.inv, last
}
