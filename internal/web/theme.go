package web

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ThemeAsset is a file a theme ships (a logo, a font, an icon).
type ThemeAsset struct {
	ContentType string
	Body        []byte
}

// ThemeProvider is the theme in force (ADR 0054). The handler asks it on every
// request, so a theme that changes while the server runs is served at once;
// only the link to the stylesheet is fixed in the page.
type ThemeProvider interface {
	// Stylesheet is the generated stylesheet and its ETag, quoted.
	Stylesheet() (css []byte, etag string)
	// Favicon is the theme's icon, when it ships one.
	Favicon() (ThemeAsset, bool)
	// Asset is a file of the theme with this identity that its stylesheet or
	// manifest names.
	Asset(sha, file string) (ThemeAsset, bool)
}

const (
	themeStylesheetPath = "/theme/theme.css"
	themeFaviconPath    = "/theme/favicon"
	// themeAssetCSP is sent with the files of a theme: they are images and
	// fonts, loaded as such by the page. Opened on their own, an SVG is a
	// document in a sandbox with no script and no network.
	themeAssetCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
)

var reIconLink = regexp.MustCompile(`(?i)<link\b[^>]*\brel\s*=\s*["']?(?:shortcut )?icon["']?[^>]*>`)

// linkTheme returns the page with the theme's stylesheet linked last in the
// head, so that it comes after the application's own, and the icon pointing at
// the theme. A page with no </head> cannot carry the link: that is an error at
// start, not a theme that silently does nothing.
func linkTheme(index []byte) ([]byte, error) {
	i := bytes.Index(bytes.ToLower(index), []byte("</head>"))
	if i < 0 {
		return nil, errors.New("web: index.html has no </head> to link the theme stylesheet into")
	}
	icon := []byte(`<link rel="icon" href="` + themeFaviconPath + `">`)
	head, tail := index[:i], index[i:]
	if reIconLink.Match(head) {
		replaced := false
		head = reIconLink.ReplaceAllFunc(head, func([]byte) []byte {
			if replaced {
				return nil
			}
			replaced = true
			return icon
		})
	} else {
		head = append(append([]byte(nil), head...), icon...)
	}
	link := []byte(`<link rel="stylesheet" href="` + themeStylesheetPath + `">`)
	out := make([]byte, 0, len(head)+len(link)+len(tail))
	out = append(out, head...)
	out = append(out, link...)
	return append(out, tail...), nil
}

// serveTheme answers everything under /theme/. It is public: the sign-in page
// is themed too. It never answers with the application shell.
func (h *handler) serveTheme(w http.ResponseWriter, r *http.Request, name string) {
	if h.theme == nil {
		http.NotFound(w, r)
		return
	}
	switch rest := strings.TrimPrefix(name, "theme/"); {
	case rest == "theme.css":
		css, etag := h.theme.Stylesheet()
		w.Header().Set("Cache-Control", cacheRevalidate)
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(w, r, "theme.css", time.Time{}, bytes.NewReader(css))
	case rest == "favicon":
		a, ok := h.theme.Favicon()
		if !ok {
			// The theme ships none: the application's own.
			body, err := fs.ReadFile(h.fsys, "favicon.svg")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			a = ThemeAsset{ContentType: "image/svg+xml", Body: body}
		}
		w.Header().Set("Cache-Control", cacheRevalidate)
		w.Header().Set("ETag", etagOf(a.Body))
		serveAsset(w, r, a)
	case strings.HasPrefix(rest, "a/"):
		sha, file, ok := strings.Cut(strings.TrimPrefix(rest, "a/"), "/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		a, found := h.theme.Asset(sha, file)
		if !found {
			http.NotFound(w, r)
			return
		}
		// The identity is in the address: the content behind it never changes.
		w.Header().Set("Cache-Control", cacheImmutable)
		w.Header().Set("Content-Security-Policy", themeAssetCSP)
		serveAsset(w, r, a)
	default:
		http.NotFound(w, r)
	}
}

func serveAsset(w http.ResponseWriter, r *http.Request, a ThemeAsset) {
	w.Header().Set("Content-Type", a.ContentType)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.Body))
}
