package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/VargaFoundation/trica/internal/web"
)

const (
	indexHTML = `<!doctype html><html><head><script type="module" src="/assets/index-3fa91c.js"></script></head><body><div id="root"></div></body></html>`
	appJS     = `console.log("app")`
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                      {Data: []byte(indexHTML)},
		"assets/index-3fa91c.js":          {Data: []byte(appJS)},
		"assets/index-77be02.css":         {Data: []byte("body{margin:0}")},
		"favicon.svg":                     {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"assets/fonts/inter-1a2b3c.woff2": {Data: []byte("wOF2")},
	}
}

func serve(t *testing.T, method, target string, hdr ...string) *http.Response {
	t.Helper()
	h, err := web.New(testFS())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every response carries the strict CSP: a page, an asset, an error.
func TestEveryResponseCarriesTheStrictCSP(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{"GET", "/"}, {"GET", "/index.html"}, {"GET", "/objects/7c9e6679-7425-40de-944b-e07fc1f90ae7"},
		{"GET", "/assets/index-3fa91c.js"}, {"GET", "/favicon.svg"},
		{"GET", "/assets/missing.js"}, {"GET", "/api/v1/nothing"}, {"POST", "/"}, {"HEAD", "/"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			r := serve(t, tc.method, tc.target)
			csp := r.Header.Get("Content-Security-Policy")
			if csp == "" {
				t.Fatal("no Content-Security-Policy")
			}
			for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'",
				"base-uri 'none'", "frame-ancestors 'none'", "form-action 'none'"} {
				if !strings.Contains(csp, want) {
					t.Errorf("CSP lacks %q: %s", want, csp)
				}
			}
			for _, bad := range []string{"unsafe-inline", "unsafe-eval", "*", "http:", "https:", "data:"} {
				if strings.Contains(csp, bad) {
					t.Errorf("CSP contains %q: %s", bad, csp)
				}
			}
			if r.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Error("no nosniff")
			}
			if r.Header.Get("X-Frame-Options") != "DENY" || r.Header.Get("Referrer-Policy") != "no-referrer" {
				t.Error("framing or referrer policy missing")
			}
		})
	}
}

// A client route is answered by the shell, so that a deep link and a reload work.
func TestClientRoutesGetTheShell(t *testing.T) {
	for _, target := range []string{"/", "/search", "/objects/7c9e6679-7425-40de-944b-e07fc1f90ae7", "/proposals/12", "/some/deep/route/", "/index.html"} {
		r := serve(t, "GET", target)
		if r.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200", target, r.StatusCode)
			continue
		}
		if got := body(t, r); got != indexHTML {
			t.Errorf("%s: not the shell: %.60s", target, got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: content type %q", target, ct)
		}
	}
}

// A file that does not exist is a 404, never the shell: a stale script URL
// must fail loudly instead of loading HTML as JavaScript. The API's own
// namespace is never answered with HTML either.
func TestMissingFilesAndAPIPathsAre404(t *testing.T) {
	for _, target := range []string{"/assets/old-123456.js", "/assets", "/assets/", "/logo.png", "/robots.txt", "/api", "/api/v1/objects", "/api/v1/", "/assets/fonts"} {
		r := serve(t, "GET", target)
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, r.StatusCode)
		}
		if strings.Contains(body(t, r), "<!doctype") {
			t.Errorf("%s: answered with the shell", target)
		}
	}
}

// index.html must never be cached (it names the hashed assets of this build);
// hashed assets can be cached for good.
func TestCacheControl(t *testing.T) {
	for _, target := range []string{"/", "/index.html", "/objects/1", "/proposals"} {
		if got := serve(t, "GET", target).Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", target, got)
		}
	}
	for _, target := range []string{"/assets/index-3fa91c.js", "/assets/index-77be02.css", "/assets/fonts/inter-1a2b3c.woff2"} {
		if got := serve(t, "GET", target).Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("%s: Cache-Control %q, want immutable", target, got)
		}
	}
	// A file outside assets/ has no hash in its name: it is revalidated.
	if got := serve(t, "GET", "/favicon.svg").Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("favicon: Cache-Control %q, want no-cache", got)
	}
}

func TestRevalidationWithETag(t *testing.T) {
	first := serve(t, "GET", "/")
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("index.html has no ETag")
	}
	if r := serve(t, "GET", "/objects/1", "If-None-Match", etag); r.StatusCode != http.StatusNotModified {
		t.Errorf("revalidating the shell: status %d, want 304", r.StatusCode)
	}
	if r := serve(t, "GET", "/", "If-None-Match", `"other"`); r.StatusCode != http.StatusOK {
		t.Errorf("a different ETag: status %d, want 200", r.StatusCode)
	}
	fav := serve(t, "GET", "/favicon.svg").Header.Get("ETag")
	if r := serve(t, "GET", "/favicon.svg", "If-None-Match", fav); fav == "" || r.StatusCode != http.StatusNotModified {
		t.Errorf("revalidating a static file: etag %q status %d", fav, r.StatusCode)
	}
}

func TestContentTypes(t *testing.T) {
	for target, want := range map[string]string{
		"/assets/index-3fa91c.js":          "javascript",
		"/assets/index-77be02.css":         "text/css",
		"/favicon.svg":                     "image/svg+xml",
		"/assets/fonts/inter-1a2b3c.woff2": "font/woff2",
	} {
		if got := serve(t, "GET", target).Header.Get("Content-Type"); !strings.Contains(got, want) {
			t.Errorf("%s: content type %q, want %q", target, got, want)
		}
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		r := serve(t, m, "/")
		if r.StatusCode != http.StatusMethodNotAllowed || r.Header.Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: status %d allow %q", m, r.StatusCode, r.Header.Get("Allow"))
		}
	}
	r := serve(t, "HEAD", "/")
	if r.StatusCode != http.StatusOK || body(t, r) != "" {
		t.Errorf("HEAD: status %d, body must be empty", r.StatusCode)
	}
}

// Paths that try to leave the asset directory get the shell or a 404, never a file from elsewhere.
func TestPathTraversalStaysInside(t *testing.T) {
	for _, target := range []string{"/../go.mod", "/assets/../../go.mod", "/%2e%2e/go.mod", "/..%2fgo.mod", "/assets/%2e%2e/%2e%2e/etc/passwd"} {
		r := serve(t, "GET", target)
		b := body(t, r)
		if strings.Contains(b, "module ") || strings.Contains(b, "root:") {
			t.Errorf("%s: leaked a file", target)
		}
		if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d", target, r.StatusCode)
		}
	}
}

func TestNewRefusesAnAssetDirectoryWithoutIndex(t *testing.T) {
	if _, err := web.New(fstest.MapFS{"assets/a.js": {Data: []byte("x")}}); err == nil {
		t.Fatal("expected an error: there is no index.html")
	}
}

// The default build embeds the placeholder, which works as an application
// directory and says that the UI is not built in. (Under -tags webui the
// embedded_ui_test.go checks the real build instead.)
func TestDefaultServesTheEmbeddedPage(t *testing.T) {
	h, err := web.Default()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/objects/1", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>trica</title>") {
		t.Fatalf("status %d: %.80s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("no CSP on the default handler")
	}
	if isPlaceholder := strings.Contains(rec.Body.String(), `content="placeholder"`); web.Built == isPlaceholder {
		t.Errorf("Built=%v does not match the embedded page (placeholder: %v)", web.Built, isPlaceholder)
	}
}
