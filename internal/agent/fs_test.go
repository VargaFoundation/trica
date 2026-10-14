package agent

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootFSConfinesSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "etc"), 0o755))
	must(os.WriteFile(filepath.Join(root, "etc", "hostname"), []byte("host-01\n"), 0o644))
	must(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "etc", "abs-link")))
	must(os.Symlink("../../"+filepath.Base(outside)+"/secret", filepath.Join(root, "etc", "rel-link")))
	must(os.Symlink("hostname", filepath.Join(root, "etc", "inner-link")))

	f, err := NewRootFS(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{"plain file", "/etc/hostname", "host-01", false},
		{"link inside the root", "/etc/inner-link", "host-01", false},
		{"absolute link out of the root", "/etc/abs-link", "", true},
		{"relative link out of the root", "/etc/rel-link", "", true},
		{"dot-dot out of the root", "/../" + filepath.Base(outside) + "/secret", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readString(f, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("read %q succeeded with %q: the root was escaped", tc.path, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("read %q = %q, %v; want %q", tc.path, got, err, tc.want)
			}
		})
	}

	// A link's target is reported as written, never followed.
	if got, err := f.Readlink("/etc/abs-link"); err != nil || got != filepath.Join(outside, "secret") {
		t.Fatalf("Readlink = %q, %v", got, err)
	}
	if st, err := f.Lstat("/etc/abs-link"); err != nil || st.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("Lstat followed the link: %v %v", st, err)
	}
}

func TestRootFSReadDirIsSorted(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"c", "a", "b"} {
		if err := os.WriteFile(filepath.Join(root, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := NewRootFS(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ents, err := f.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("order %v: probes would emit objects in a different order at each run", got)
	}
}

func TestNewRootFSMissingRoot(t *testing.T) {
	if _, err := NewRootFS(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a missing root was accepted")
	}
	if _, err := New(Config{Root: filepath.Join(t.TempDir(), "absent")}); err == nil {
		t.Fatal("New accepted a missing root")
	}
}

func TestReadFileIsBounded(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("x", maxFile+4096)
	if err := os.WriteFile(filepath.Join(root, "big"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := NewRootFS(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := readFile(f, "/big")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != maxFile {
		t.Fatalf("read %d bytes, the bound is %d", len(b), maxFile)
	}
}

func TestSkippable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{fs.ErrNotExist, true},
		{fs.ErrPermission, true},
		{&fs.PathError{Op: "open", Path: "x", Err: fs.ErrPermission}, true},
		{io.ErrUnexpectedEOF, false},
		{errors.New("boom"), false},
	}
	for _, tc := range tests {
		if got := skippable(tc.err); got != tc.want {
			t.Errorf("skippable(%v) = %v", tc.err, got)
		}
	}
}
