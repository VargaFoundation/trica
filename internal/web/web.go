// Package web serves the single-page application built in web/ (ADR 0018).
//
// The built assets are embedded in the binary. Two builds exist so that
// `go build` and `make ci` work on a machine without Node:
//
//   - the default build embeds placeholder/index.html, a committed page that
//     says the UI is not built in (Built is false);
//   - `go build -tags webui` embeds dist/, the output of `make web` (Built is
//     true). dist/ is a build output and is not committed.
//
// The handler returned by New is what the server mounts at "/", next to the
// API under /api/. It never answers a request for the API or for a missing
// file with the application shell.
package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// ContentSecurityPolicy is sent with every response when the single sign-on
// is not configured (see Config for the case where it is). The application loads
// nothing from another origin, runs no inline script and sets no inline
// style: a template that needs one of these is a bug (a web test checks the
// built index.html). The API is same-origin, so connect-src is 'self'.
const ContentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self'; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"manifest-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// configPath is where the application reads its Config. It is outside /api
// on purpose: the page needs it before it holds any credential.
const configPath = "ui-config.json"

// Cache-Control values. Vite names every file under assets/ after a hash of
// its content, so those never change; everything else is revalidated.
const (
	cacheImmutable  = "public, max-age=31536000, immutable"
	cacheRevalidate = "no-cache"
)

const indexName = "index.html"

// extraTypes covers what a minimal image (no /etc/mime.types) does not know.
var extraTypes = map[string]string{
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".webmanifest": "application/manifest+json",
	".map":         "application/json",
}

type handler struct {
	fsys   fs.FS
	index  []byte
	etag   string
	csp    string
	config []byte
}

// New returns the handler that serves the application from fsys (the
// directory that holds index.html), without single sign-on. It fails when
// index.html is missing: a server that mounts it would answer every page with
// an error.
func New(fsys fs.FS) (http.Handler, error) {
	return NewWithConfig(fsys, Config{})
}

// NewWithConfig is New with the settings the page needs at start-up.
func NewWithConfig(fsys fs.FS, cfg Config) (http.Handler, error) {
	idx, err := fs.ReadFile(fsys, indexName)
	if err != nil {
		return nil, fmt.Errorf("web: %s: %w", indexName, err)
	}
	cfg, err = cfg.normalised()
	if err != nil {
		return nil, err
	}
	doc, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("web: config: %w", err)
	}
	return &handler{fsys: fsys, index: idx, etag: etagOf(idx), csp: cfg.csp(), config: doc}, nil
}

// Default returns the handler for the embedded assets (see Built).
func Default() (http.Handler, error) {
	return DefaultWithConfig(Config{})
}

// DefaultWithConfig is Default with the settings the page needs at start-up.
func DefaultWithConfig(cfg Config) (http.Handler, error) {
	sub, err := embedded()
	if err != nil {
		return nil, err
	}
	return NewWithConfig(sub, cfg)
}

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

func setSecurityHeaders(h http.Header, csp string) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header(), h.csp)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	switch {
	case name == "" || name == indexName:
		h.serveIndex(w, r)
		return
	case name == configPath:
		h.serveConfig(w, r)
		return
	case name == "api" || strings.HasPrefix(name, "api/"):
		// The API is mounted beside this handler; a request that reaches us
		// is for an endpoint that does not exist. Never answer it with HTML.
		http.NotFound(w, r)
		return
	}
	if fi, err := fs.Stat(h.fsys, name); err == nil && fi.Mode().IsRegular() {
		h.serveFile(w, r, name)
		return
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrInvalid) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// A client route ("/objects/<id>") has no extension and is answered by the
	// shell; a file that does not exist ("/assets/old-3f2a.js", "/logo.png")
	// is a 404, so a stale reference fails loudly instead of loading HTML as
	// a script.
	if path.Ext(name) != "" || name == "assets" || strings.HasPrefix(name, "assets/") {
		http.NotFound(w, r)
		return
	}
	h.serveIndex(w, r)
}

// serveConfig answers the start-up settings. They are public (an issuer and a
// public client id) and are never cached: a change of identity provider must
// reach the next page load.
func (h *handler) serveConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	http.ServeContent(w, r, configPath, time.Time{}, bytes.NewReader(h.config))
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheRevalidate)
	w.Header().Set("ETag", h.etag)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, indexName, time.Time{}, bytes.NewReader(h.index))
}

func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := h.fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ext := path.Ext(name)
	ct := extraTypes[ext]
	if ct == "" {
		ct = mime.TypeByExtension(ext)
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheImmutable)
	} else {
		w.Header().Set("Cache-Control", cacheRevalidate)
		w.Header().Set("ETag", etagOf(body))
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}
