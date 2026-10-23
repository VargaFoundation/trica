package theme

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// ReadArchive validates a zip archive and returns the theme it holds. Nothing
// of it is trusted: sizes are counted on the bytes actually read, never on what
// the headers declare. A refusal is a *RefusedError listing every reason it
// could find, or a plain error when the archive cannot be read at all.
func ReadArchive(b []byte) (*Theme, error) {
	if len(b) > MaxArchiveBytes {
		return nil, refuse("the archive is %d bytes, over the %d MiB limit", len(b), MaxArchiveBytes>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, refuse("not a zip archive: %v", err)
	}
	files := map[string][]byte{}
	total := 0
	entries := 0
	for _, f := range zr.File {
		name := f.Name
		if ignorable(name) {
			continue
		}
		if strings.HasSuffix(name, "/") {
			if err := checkName(strings.TrimSuffix(name, "/")); err != nil {
				return nil, refuse("%s: %v", name, err)
			}
			continue
		}
		if entries++; entries > MaxEntries {
			return nil, refuse("the archive has more than %d entries", MaxEntries)
		}
		if err := checkName(name); err != nil {
			return nil, refuse("%s: %v", name, err)
		}
		switch {
		case f.Flags&0x1 != 0:
			return nil, refuse("%s: encrypted entries are not allowed", name)
		case f.Method != zip.Store && f.Method != zip.Deflate:
			return nil, refuse("%s: compression method %d is not allowed", name, f.Method)
		case f.Mode()&os.ModeSymlink != 0:
			return nil, refuse("%s: a symbolic link is not allowed", name)
		case f.UncompressedSize64 > MaxFileBytes:
			return nil, refuse("%s: %d bytes, over the %d MiB limit for a file", name, f.UncompressedSize64, MaxFileBytes>>20)
		}
		if _, dup := files[name]; dup {
			return nil, refuse("%s appears twice", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, refuse("%s: %v", name, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
		_ = rc.Close()
		switch {
		case err != nil:
			return nil, refuse("%s: %v", name, err)
		case len(body) > MaxFileBytes:
			return nil, refuse("%s: over the %d MiB limit for a file", name, MaxFileBytes>>20)
		}
		if total += len(body); total > MaxTotalBytes {
			return nil, refuse("the archive holds more than %d MiB once decompressed", MaxTotalBytes>>20)
		}
		files[name] = body
	}
	files, err = normalise(files)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return build(files, hex.EncodeToString(sum[:]), false)
}

// ReadDir reads a theme laid out as a directory, as a ConfigMap or a Secret
// mounts one. Entries whose name starts with a dot are skipped (that is where
// Kubernetes keeps its ..data links); a link that leads out of the directory is
// refused. The identity of the theme is the sha256 of the lines
// "<path> NUL <sha256 of the content> LF", sorted by path.
func ReadDir(dir string) (*Theme, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := map[string][]byte{}
	total := 0
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		if depth > maxDepth {
			return refuse("%s: deeper than %d levels", rel, maxDepth)
		}
		ents, err := fs.ReadDir(root.FS(), dirArg(rel))
		if err != nil {
			return refuse("%s: %v", dirArg(rel), err)
		}
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			p := name
			if rel != "" {
				p = rel + "/" + name
			}
			if err := checkName(p); err != nil {
				return refuse("%s: %v", p, err)
			}
			st, err := fs.Stat(root.FS(), p)
			if err != nil {
				return refuse("%s: %v", p, err)
			}
			switch {
			case st.IsDir():
				if err := walk(p, depth+1); err != nil {
					return err
				}
			case !st.Mode().IsRegular():
				return refuse("%s: not a regular file", p)
			default:
				if len(files) >= MaxEntries {
					return refuse("the directory has more than %d files", MaxEntries)
				}
				f, err := root.Open(p)
				if err != nil {
					return refuse("%s: %v", p, err)
				}
				body, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
				_ = f.Close()
				switch {
				case err != nil:
					return refuse("%s: %v", p, err)
				case len(body) > MaxFileBytes:
					return refuse("%s: over the %d MiB limit for a file", p, MaxFileBytes>>20)
				}
				if total += len(body); total > MaxTotalBytes {
					return refuse("the directory holds more than %d MiB", MaxTotalBytes>>20)
				}
				files[p] = body
			}
		}
		return nil
	}
	if err := walk("", 1); err != nil {
		return nil, err
	}
	return build(files, dirIdentity(files), false)
}

func dirArg(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// dirIdentity is the sha256 of the sorted "<path> NUL <sha256> LF" lines.
func dirIdentity(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(h, "%s\x00%s\n", n, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReadPath reads a theme from a zip file or a directory.
func ReadPath(p string) (*Theme, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return ReadDir(p)
	}
	if st.Size() > MaxArchiveBytes {
		return nil, refuse("%s is %d bytes, over the %d MiB limit", p, st.Size(), MaxArchiveBytes>>20)
	}
	b, err := os.ReadFile(p) //nolint:gosec // p is the operator-configured theme path
	if err != nil {
		return nil, err
	}
	return ReadArchive(b)
}
