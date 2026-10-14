// Package agent observes the Linux host it runs on and reports it as one
// full-scope sdk.Batch per run (source "agent:<hostname>").
//
// The agent is read-only: every probe reads files below a configurable root
// (default "/", a fake tree under testdata/ in tests) or runs one command from
// a fixed allow-list through the Runner interface. Nothing is written on the
// host, and no environment variable or process command line is read, so no
// secret can reach a batch.
//
// The flow is probes -> Inventory -> Batch: the probes (probe_*.go) fill a
// plain Inventory, mapping.go turns it into schema objects and relations.
// A probe that fails is a warning on the batch Health, never fatal.
package agent

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// maxFile bounds what ReadFile will read: sysfs and procfs values are tiny,
// and a hostile or broken file must not grow the agent's heap.
const maxFile = 1 << 20

// FS is the read-only view of the host a probe works on. Names are absolute
// host paths ("/etc/os-release"); an implementation maps them below its root.
type FS interface {
	Open(name string) (io.ReadCloser, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	Readlink(name string) (string, error)
	Lstat(name string) (fs.FileInfo, error)
}

// RootFS is an FS confined to a directory: a symbolic link cannot lead out of
// it (os.Root), which matters when the root is a tree someone else produced.
type RootFS struct{ root *os.Root }

// NewRootFS opens dir as the root of the host to observe.
func NewRootFS(dir string) (*RootFS, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &RootFS{root: r}, nil
}

// Close releases the root directory.
func (f *RootFS) Close() error { return f.root.Close() }

func rel(name string) string {
	n := strings.TrimLeft(path.Clean("/"+name), "/")
	if n == "" {
		return "."
	}
	return n
}

// Open opens a file for reading.
func (f *RootFS) Open(name string) (io.ReadCloser, error) { return f.root.Open(rel(name)) }

// ReadDir lists a directory, sorted by name. Symbolic links are listed, not followed.
func (f *RootFS) ReadDir(name string) ([]fs.DirEntry, error) {
	d, err := f.root.Open(rel(name))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	ents, err := d.ReadDir(-1)
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	return ents, err
}

// Readlink returns the raw target of a symbolic link.
func (f *RootFS) Readlink(name string) (string, error) { return f.root.Readlink(rel(name)) }

// Lstat describes a path without following a final symbolic link.
func (f *RootFS) Lstat(name string) (fs.FileInfo, error) { return f.root.Lstat(rel(name)) }

// readFile reads a small file in full. A file over maxFile is truncated, not an error.
func readFile(fsys FS, name string) ([]byte, error) {
	r, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, maxFile))
}

// readString reads a small file and trims surrounding white space.
func readString(fsys FS, name string) (string, error) {
	b, err := readFile(fsys, name)
	return strings.TrimSpace(string(b)), err
}

// readOptional reads a small text file whose absence or unreadability is
// normal (a root-only DMI file, a sysfs attribute some drivers do not have):
// it returns "" instead of an error.
func readOptional(fsys FS, name string) string {
	s, err := readString(fsys, name)
	if err != nil {
		return ""
	}
	return s
}

func exists(fsys FS, name string) bool {
	_, err := fsys.Lstat(name)
	return err == nil
}

// skippable reports whether err means "this host does not have it, or we may
// not look": not a failure of the probe.
func skippable(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission)
}
