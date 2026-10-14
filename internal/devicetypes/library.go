package devicetypes

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// The vendored library. Directories named after a manufacturer hold files
// copied unchanged from netbox-community/devicetype-library (CC0-1.0);
// the directory "trica" holds models written by this project.
//
//go:embed library
var embedded embed.FS

// localDir is the directory of project-written models.
const localDir = "trica"

// Load parses every .yaml file under root of fsys, one directory per vendor.
// The first invalid file fails the load: a half-loaded library would silently
// miss devices. Slugs must be unique.
func Load(fsys fs.FS, root string) ([]*Model, error) {
	var out []*Model
	seen := map[string]string{}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (!strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml")) {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		origin := OriginUpstream
		if strings.HasPrefix(p, path.Join(root, localDir)+"/") {
			origin = OriginLocal
		}
		m, err := Parse(data, origin, strings.TrimPrefix(p, root+"/"))
		if err != nil {
			return err
		}
		if prev, dup := seen[m.Slug]; dup {
			return fmt.Errorf("%s: slug %q already used by %s", m.Path, m.Slug, prev)
		}
		seen[m.Slug] = m.Path
		out = append(out, m)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// Default returns the library embedded in the binary.
func Default() ([]*Model, error) { return Load(embedded, "library") }
