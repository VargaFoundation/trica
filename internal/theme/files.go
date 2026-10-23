package theme

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The limits of an archive. A ConfigMap holds about 1 MiB, so a theme meant
// for one stays under that; the limits below are what any source may carry.
const (
	MaxArchiveBytes  = 2 << 20 // the archive as stored
	MaxEntries       = 64
	MaxTotalBytes    = 4 << 20 // every file, decompressed
	MaxFileBytes     = 1 << 20
	MaxManifestBytes = 64 << 10
	MaxStylesheet    = 128 << 10
	ManifestName     = "theme.json"
)

// RefusedError is a theme that does not pass validation: every reason found,
// one per line, so that its author fixes them in one go.
type RefusedError struct{ Violations []string }

func (e *RefusedError) Error() string { return strings.Join(e.Violations, "; ") }

// checker collects the violations of one validation.
type checker struct{ v []string }

func (c *checker) add(format string, args ...any) { c.v = append(c.v, fmt.Sprintf(format, args...)) }

func (c *checker) err() error {
	if len(c.v) == 0 {
		return nil
	}
	return &RefusedError{Violations: c.v}
}

func refuse(format string, args ...any) error {
	return &RefusedError{Violations: []string{fmt.Sprintf(format, args...)}}
}

var (
	// reSegment is one segment of a path in an archive.
	reSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	maxDepth  = 3 // segments, the file name included
)

// checkName validates one path of an archive (never one that is ignored).
func checkName(name string) error {
	switch {
	case name == "":
		return errors.New("empty path")
	case !utf8.ValidString(name):
		return errors.New("path is not UTF-8")
	case strings.ContainsRune(name, 0):
		return errors.New("path holds a NUL")
	case strings.Contains(name, `\`):
		return errors.New("path holds a backslash")
	case strings.HasPrefix(name, "/"):
		return errors.New("path is absolute")
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "." || seg == ".." {
			return errors.New("path holds a . or .. segment")
		}
		if !reSegment.MatchString(seg) {
			return errors.New("path holds a character outside letters, digits, . _ -")
		}
	}
	return nil
}

// ignorable reports the entries archives acquire on a Mac: dropped, not refused.
func ignorable(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store"
}

// normalise strips the single top-level directory an archive made with `zip -r
// dir/` carries, when there is no theme.json at the root, then checks depth,
// duplicates and case-insensitive duplicates.
func normalise(files map[string][]byte) (map[string][]byte, error) {
	if _, ok := files[ManifestName]; !ok {
		top := ""
		for name := range files {
			first, _, found := strings.Cut(name, "/")
			if !found || (top != "" && first != top) {
				top = ""
				break
			}
			top = first
		}
		if top != "" {
			stripped := make(map[string][]byte, len(files))
			for name, b := range files {
				stripped[strings.TrimPrefix(name, top+"/")] = b
			}
			files = stripped
		}
	}
	seen := map[string]string{}
	for name := range files {
		if n := strings.Count(name, "/") + 1; n > maxDepth {
			return nil, refuse("%s: deeper than %d levels", name, maxDepth)
		}
		low := strings.ToLower(name)
		if other, dup := seen[low]; dup {
			return nil, refuse("%s and %s differ only by case", other, name)
		}
		seen[low] = name
	}
	return files, nil
}

// kind of a file, by extension.
func isLicenceName(name string) bool {
	return strings.HasPrefix(strings.ToUpper(path.Base(name)), "LICENSE")
}

var magic = map[string][]byte{
	".png":   []byte("\x89PNG\r\n\x1a\n"),
	".jpg":   []byte("\xff\xd8\xff"),
	".jpeg":  []byte("\xff\xd8\xff"),
	".ico":   []byte("\x00\x00\x01\x00"),
	".woff2": []byte("wOF2"),
}

// checkFile validates one file of the archive by its type; the stylesheet is
// scanned later, when the CSS is generated (it needs the whole file set).
func checkFile(name string, b []byte, c *checker) {
	if name == ManifestName {
		if len(b) > MaxManifestBytes {
			c.add("%s is over %d KiB", name, MaxManifestBytes>>10)
		}
		return
	}
	ext := strings.ToLower(path.Ext(name))
	switch {
	case ext == ".css":
		if len(b) > MaxStylesheet {
			c.add("%s is over %d KiB", name, MaxStylesheet>>10)
		}
	case ext == ".svg":
		if err := checkSVG(b); err != nil {
			c.add("%s: %v", name, err)
		}
	case ext == ".webp":
		if len(b) < 12 || !bytes.HasPrefix(b, []byte("RIFF")) || !bytes.Equal(b[8:12], []byte("WEBP")) {
			c.add("%s: not a WebP image (bad signature)", name)
		}
	case magic[ext] != nil:
		if !bytes.HasPrefix(b, magic[ext]) {
			c.add("%s: the content is not a %s file (bad signature)", name, strings.TrimPrefix(ext, "."))
		}
	case ext == ".txt" || ext == ".md" || isLicenceName(name):
		if !utf8.Valid(b) {
			c.add("%s: not UTF-8 text", name)
		}
	default:
		c.add("%s: a file of this type is not allowed in a theme (allowed: css, svg, png, jpg, webp, ico, woff2, txt, md)", name)
	}
}

// contentType of a referenced file, by extension.
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	}
	return "application/octet-stream"
}
