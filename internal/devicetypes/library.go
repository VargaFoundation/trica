package devicetypes

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// The vendored library. Directories named after a manufacturer hold files
// copied unchanged from netbox-community/devicetype-library (CC0-1.0);
// the directory "trica" holds models written by this project, "overlays" the
// shortnames this project adds to upstream models (ADR 0056), and
// "elevation-images" the face illustrations (see library/README.md).
//
//go:embed library
var embedded embed.FS

// localDir is the directory of project-written models.
const localDir = "trica"

// Load parses every .yaml file under root of fsys, one directory per vendor.
// The first invalid file fails the load: a half-loaded library would silently
// miss devices. Slugs must be unique. Images are not read: see Open.
func Load(fsys fs.FS, root string) ([]*Model, error) {
	models, err := loadModels(fsys, root, func(p string) string {
		if strings.HasPrefix(p, path.Join(root, localDir)+"/") {
			return OriginLocal
		}
		return OriginUpstream
	})
	if err != nil {
		return nil, err
	}
	if err := applyOverlays(fsys, root, models); err != nil {
		return nil, err
	}
	return models, nil
}

func loadModels(fsys fs.FS, root string, originOf func(string) string) ([]*Model, error) {
	var out []*Model
	seen := map[string]string{}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root && (strings.HasPrefix(d.Name(), ".") || (d.IsDir() && (d.Name() == ImageDir || d.Name() == DrawingDir || d.Name() == LayoutDir || d.Name() == OverlayDir))) {
			// Hidden entries are skipped: a Kubernetes volume holds its files
			// twice, under a hidden ..<timestamp> directory and as links.
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || (!strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml")) {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		m, err := Parse(data, originOf(p), strings.TrimPrefix(p, root+"/"))
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

// Default returns the library embedded in the binary, images included.
func Default() ([]*Model, error) {
	lib, err := Open("")
	if err != nil {
		return nil, err
	}
	return lib.Models, nil
}

// Library is the loaded device-type library: the embedded models and images,
// with the operator's directory laid over them.
type Library struct {
	Models []*Model
	// Warnings are what the operator's directory holds that is not used (an
	// image no model has, a file type that is not accepted). They do not fail
	// the load: an extra file must not take the library down.
	Warnings []string
}

// Open loads the embedded library and, when extraDir is not empty, the
// operator's directory over it (config devicetypes.extra_dir). That
// directory has the layout of the library: model files under any directory
// but elevation-images and layouts, images under
// elevation-images/<manufacturer>/<slug>.<face>.<ext>, the spots of a face
// under layouts/<manufacturer>/<slug>.<face>.json.
// An operator model replaces the embedded model with the same slug, and an
// operator image replaces the embedded image of the same model and face,
// so a photograph of one's own equipment wins over a vendor drawing.
func Open(extraDir string) (*Library, error) {
	var extra fs.FS
	if extraDir != "" {
		st, err := os.Stat(extraDir)
		if err != nil {
			return nil, fmt.Errorf("devicetypes.extra_dir: %w", err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("devicetypes.extra_dir: %s is not a directory", extraDir)
		}
		extra = os.DirFS(extraDir)
	}
	return OpenFS(embedded, "library", extra)
}

// OpenFS is Open over any file systems: the library under root of lib, the
// operator's directory at the top of extra (nil: none).
func OpenFS(lib fs.FS, root string, extra fs.FS) (*Library, error) {
	models, err := Load(lib, root)
	if err != nil {
		return nil, err
	}
	// The embedded images first. Every one belongs to a model: a misnamed
	// file is a bug of this repository and fails the load (and the tests).
	used := map[string]bool{}
	for _, m := range models {
		for _, face := range []string{Front, Rear} {
			img, err := readImage(lib, root, m, face, embeddedImageOrigin(lib, root, m, face))
			if err != nil {
				return nil, err
			}
			if img != nil {
				used[img.Path] = true
			}
			m.setImage(face, img)
		}
	}
	files, err := imageFiles(lib, root)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if !used[f] {
			return nil, fmt.Errorf("%s: no model has this slug and face, or its type is not one of png, jpg, jpeg, webp", f)
		}
	}
	// The embedded layouts: each belongs to a model and to an image of the
	// same face, or the load fails.
	for _, m := range models {
		for _, face := range []string{Front, Rear} {
			l, err := readLayout(lib, root, m, face, OriginLocal)
			if err != nil {
				return nil, err
			}
			if l == nil {
				continue
			}
			if m.Image(face) == nil {
				return nil, fmt.Errorf("%s: %s has no %s image to lay it over", l.Path, m.Slug, face)
			}
			used[l.Path] = true
			m.setLayout(face, l)
		}
	}
	if files, err = layoutFiles(lib, root); err != nil {
		return nil, err
	}
	for _, f := range files {
		if !used[f] {
			return nil, fmt.Errorf("%s: no model has this manufacturer, slug and face, or the name does not end in .json", f)
		}
	}
	out := &Library{Models: models}
	if extra == nil {
		return out, nil
	}

	// Then the operator's directory: its models over the embedded ones (an
	// operator model keeps the embedded images of the slug it replaces:
	// replacing a model file must not lose its drawing), then its images
	// over every model's.
	own, err := loadModels(extra, ".", func(string) string { return OriginOperator })
	if err != nil {
		return nil, fmt.Errorf("devicetypes.extra_dir: %w", err)
	}
	embeddedBySlug := map[string]*Model{}
	for _, m := range models {
		embeddedBySlug[m.Slug] = m
	}
	for _, m := range own {
		if e := embeddedBySlug[m.Slug]; e != nil {
			m.FrontImage, m.RearImage = e.FrontImage, e.RearImage
			m.FrontLayout, m.RearLayout = e.FrontLayout, e.RearLayout
		}
	}
	out.Models = overlay(models, own)
	used = map[string]bool{}
	for _, m := range out.Models {
		for _, face := range []string{Front, Rear} {
			img, err := readImage(extra, ".", m, face, OriginOperator)
			if err != nil {
				return nil, fmt.Errorf("devicetypes.extra_dir: %w", err)
			}
			if img != nil {
				used[img.Path] = true
				m.setImage(face, img)
				// The spots of a drawing do not fit a photograph: a
				// replaced image loses its layout, unless the operator
				// gives one (below).
				m.setLayout(face, nil)
			}
			l, err := readLayout(extra, ".", m, face, OriginOperator)
			if err != nil {
				return nil, fmt.Errorf("devicetypes.extra_dir: %w", err)
			}
			if l != nil && m.Image(face) != nil {
				used[l.Path] = true
				m.setLayout(face, l)
			}
		}
	}
	// An operator file nobody uses is reported, not fatal: an extra file
	// must not take the library down.
	if files, err = imageFiles(extra, "."); err != nil {
		return nil, fmt.Errorf("devicetypes.extra_dir: %w", err)
	}
	for _, f := range files {
		if !used[f] {
			out.Warnings = append(out.Warnings, f+": not used (no model has this manufacturer, slug and face, "+
				"or the type is not png, jpg, jpeg or webp)")
		}
	}
	if files, err = layoutFiles(extra, "."); err != nil {
		return nil, fmt.Errorf("devicetypes.extra_dir: %w", err)
	}
	for _, f := range files {
		if !used[f] {
			out.Warnings = append(out.Warnings, f+": not used (no model has this manufacturer, slug and face, "+
				"the face has no image, or the name does not end in .json)")
		}
	}
	return out, nil
}

// embeddedImageOrigin is the origin of an embedded image. An image drawn by
// the project has its source beside it, drawings/<manufacturer>/<slug>.<face>.svg,
// and comes from the project even when its model is an upstream file (the
// CRS354 upstream has no picture of). Otherwise an image of an upstream model
// comes from upstream, an image of a project model from the project.
func embeddedImageOrigin(lib fs.FS, root string, m *Model, face string) string {
	if _, err := fs.Stat(lib, path.Join(root, drawingPath(m, face))); err == nil {
		return OriginLocal
	}
	if m.Origin == OriginLocal {
		return OriginLocal
	}
	return OriginUpstream
}

// overlay returns base with the models of own added, a model of own replacing
// the base model with the same slug.
func overlay(base, own []*Model) []*Model {
	bySlug := map[string]int{}
	out := append([]*Model(nil), base...)
	for i, m := range out {
		bySlug[m.Slug] = i
	}
	for _, m := range own {
		if i, ok := bySlug[m.Slug]; ok {
			out[i] = m
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}
