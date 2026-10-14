package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner answers allow-listed commands from the recorded output under
// testdata/<name>/exec. A command with no recording is "not installed". Like
// ExecRunner it refuses what the allow-list refuses, so a probe that asks for
// something it may not run fails its test.
type fakeRunner struct {
	dir string

	mu    sync.Mutex
	calls []string
}

func newFakeRunner(name string) *fakeRunner {
	return &fakeRunner{dir: filepath.Join("testdata", name, "exec")}
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.mu.Unlock()
	if !allowedCommand(name, args) {
		return nil, fmt.Errorf("%s: %w", line, ErrNotAllowed)
	}
	var file string
	switch name {
	case "rpm":
		file = "rpm-qa.txt"
	case "systemctl":
		file = "systemctl.json"
	case "lldpctl":
		file = "lldpctl.json"
	case "smartctl":
		file = "smartctl-" + strings.TrimPrefix(args[2], "/dev/") + ".json"
	}
	b, err := os.ReadFile(filepath.Join(f.dir, file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	return b, err
}

func (f *fakeRunner) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// runnerFunc adapts a function to Runner.
type runnerFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

// fakeRoots are the two fixture hosts: a bare-metal Debian 12 server and a KVM guest running RHEL 9.
var fakeRoots = []string{"debian12", "rhel9"}

func openRoot(t testing.TB, name string) *RootFS {
	t.Helper()
	r, err := NewRootFS(filepath.Join("testdata", name, "root"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// newTestAgent builds an Agent over a fixture host, with the fake runner and the
// text address source of the fixture root.
func newTestAgent(t testing.TB, name string) (*Agent, *fakeRunner) {
	t.Helper()
	run := newFakeRunner(name)
	a, err := New(Config{FS: openRoot(t, name), Runner: run, Now: fixedNow, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return a, run
}

var epoch = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return epoch }

// observeRoot runs every probe over a fixture host.
func observeRoot(t testing.TB, name string) *Inventory {
	t.Helper()
	return observe(context.Background(), openRoot(t, name), newFakeRunner(name), fsAddrs{path: "/addrs.txt"})
}

// denyFS wraps an FS and refuses the listed paths (and what is below them) with
// a permission error, as a non-root agent gets on /sys/class/dmi/id/product_serial.
// Tests may run as root, where the real kernel would not refuse.
type denyFS struct {
	FS
	prefixes []string
}

func (d denyFS) denied(name string) bool {
	for _, p := range d.prefixes {
		if name == p || strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}

func (d denyFS) Open(name string) (io.ReadCloser, error) {
	if d.denied(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.FS.Open(name)
}

func (d denyFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if d.denied(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.FS.ReadDir(name)
}

// copyTree copies a fixture root into a temporary directory so a test can edit it.
// Symbolic links are copied as links.
func copyTree(t testing.TB, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		switch {
		case e.IsDir():
			return os.MkdirAll(dst, 0o755)
		case e.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, b, 0o644)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
