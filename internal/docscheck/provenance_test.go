package docscheck

import (
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// The device-type library is trica's own data (ADR 0063) and the owner wants no
// tie to the repository it was first imported from. The name of that project
// is written in the decision records that tell the history, and nowhere else.
var provenanceAllowed = map[string]bool{
	"docs/adr/0020-device-type-library.md":                    true,
	"docs/adr/0042-device-type-images.md":                     true,
	"docs/adr/0049-hot-and-cold-aisles.md":                    true,
	"docs/adr/0056-growing-the-device-type-library.md":        true,
	"docs/adr/0063-the-device-type-library-is-tricas-data.md": true,
	"docs/plan/STATUS.md":                                     true,
	// This test names the project in its own title.
	"internal/docscheck/provenance_test.go": true,
}

var (
	provenanceSkippedDirs = map[string]bool{"node_modules": true, "dist": true, "test-results": true, "playwright-report": true}
	provenanceTextExts    = map[string]bool{
		".go": true, ".md": true, ".ts": true, ".tsx": true, ".mjs": true, ".js": true, ".json": true,
		".yaml": true, ".yml": true, ".sh": true, ".txt": true, ".toml": true, ".tsv": true, ".css": true, ".html": true,
	}
	provenanceTextNames = map[string]bool{"NOTICE": true, "Makefile": true, "Dockerfile": true}
)

// netboxHits lists the text files of fsys that name the project, apart from the
// allowed ones. Dot-directories (but .github), dependency and build directories,
// and binary files are not read.
func netboxHits(fsys fs.FS, allowed map[string]bool) ([]string, error) {
	needle := "net" + "box"
	var hits []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != "." && ((strings.HasPrefix(name, ".") && name != ".github") || provenanceSkippedDirs[name]) {
				return fs.SkipDir
			}
			return nil
		}
		if allowed[p] || (!provenanceTextExts[path.Ext(p)] && !provenanceTextNames[path.Base(p)]) {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(string(data)), needle) {
			hits = append(hits, p)
		}
		return nil
	})
	sort.Strings(hits)
	return hits, err
}

// TestNetBoxIsNamedOnlyInTheRecord fails when the repository names the project the
// library was first imported from anywhere but in the history files (ADR 0063).
func TestNetBoxIsNamedOnlyInTheRecord(t *testing.T) {
	hits, err := netboxHits(os.DirFS("../.."), provenanceAllowed)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range hits {
		t.Errorf("%s names the project the library was first imported from: ADR 0063 keeps that in the decision records only", p)
	}
}

// TestNetBoxHitsFindsWhatItShould proves the scan reports a hit in code and ignores
// the places it must ignore.
func TestNetBoxHitsFindsWhatItShould(t *testing.T) {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	fsys := fstest.MapFS{
		"internal/x/a.go":                      file("// like NetBox does\n"),
		"docs/adr/0020-device-type-library.md": file("the NETBOX devicetype-library\n"),
		"web/node_modules/dep/index.js":        file("netbox\n"),
		"docs/img/logo.png":                    file("netbox\n"),
		".claude/worktrees/w/internal/x/b.go":  file("netbox\n"),
		".github/workflows/ci.yml":             file("name: netbox\n"),
		"NOTICE":                               file("netbox\n"),
		"internal/x/clean.go":                  file("package x\n"),
	}
	got, err := netboxHits(fsys, map[string]bool{"docs/adr/0020-device-type-library.md": true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".github/workflows/ci.yml", "NOTICE", "internal/x/a.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("hits %v, want %v", got, want)
	}
}
