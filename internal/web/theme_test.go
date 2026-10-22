package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/VargaFoundation/trica/internal/web"
)

type fakeTheme struct{ icon bool }

func (fakeTheme) Stylesheet() ([]byte, string) {
	return []byte(":root:root{--trica-ink:#000000}"), `"abc"`
}
func (f fakeTheme) Favicon() (web.ThemeAsset, bool) {
	return web.ThemeAsset{ContentType: "image/svg+xml", Body: []byte("<svg/>")}, f.icon
}
func (fakeTheme) Asset(sha, file string) (web.ThemeAsset, bool) {
	if sha == "s1" && file == "img/logo.svg" {
		return web.ThemeAsset{ContentType: "image/svg+xml", Body: []byte("<svg/>")}, true
	}
	return web.ThemeAsset{}, false
}

func themed(t *testing.T, fsys fstest.MapFS, th web.ThemeProvider) http.Handler {
	t.Helper()
	h, err := web.NewWithConfig(fsys, web.Config{Theme: th})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(h http.Handler, method, target string, hdr ...string) *http.Response {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestIndexLinksTheThemeLast(t *testing.T) {
	fsys := testFS()
	fsys["index.html"] = &fstest.MapFile{Data: []byte(`<!doctype html><html><head><link rel="icon" type="image/svg+xml" href="/favicon.svg"><link rel="stylesheet" href="/assets/index-77be02.css"></head><body></body></html>`)}
	r := get(themed(t, fsys, fakeTheme{}), "GET", "/")
	page := body(t, r)
	app := strings.Index(page, "/assets/index-77be02.css")
	link := strings.Index(page, `<link rel="stylesheet" href="/theme/theme.css">`)
	if app < 0 || link < app || link > strings.Index(page, "</head>") {
		t.Errorf("the theme is not linked after the application's stylesheet, inside the head:\n%s", page)
	}
	if strings.Contains(page, "/favicon.svg") || strings.Count(page, `rel="icon"`) != 1 || !strings.Contains(page, `<link rel="icon" href="/theme/favicon">`) {
		t.Errorf("the icon is not the theme's:\n%s", page)
	}
	// Without a theme the page is untouched.
	plain := body(t, serve(t, "GET", "/"))
	if strings.Contains(plain, "/theme/") {
		t.Errorf("a page with no theme links one:\n%s", plain)
	}
}

func TestIndexWithoutAnIconGetsOne(t *testing.T) {
	page := body(t, get(themed(t, testFS(), fakeTheme{}), "GET", "/"))
	if !strings.Contains(page, `<link rel="icon" href="/theme/favicon"><link rel="stylesheet" href="/theme/theme.css"></head>`) {
		t.Errorf("page:\n%s", page)
	}
}

func TestAThemeNeedsAHead(t *testing.T) {
	fsys := testFS()
	fsys["index.html"] = &fstest.MapFile{Data: []byte(`<p>no head</p>`)}
	if _, err := web.NewWithConfig(fsys, web.Config{Theme: fakeTheme{}}); err == nil {
		t.Fatal("a page with no </head> was accepted with a theme")
	}
}

func TestThemeStylesheet(t *testing.T) {
	h := themed(t, testFS(), fakeTheme{})
	r := get(h, "GET", "/theme/theme.css")
	if r.StatusCode != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/css") ||
		r.Header.Get("Cache-Control") != "no-cache" || r.Header.Get("ETag") != `"abc"` {
		t.Errorf("status %d, headers %v", r.StatusCode, r.Header)
	}
	if !strings.Contains(body(t, r), "--trica-ink") {
		t.Error("not the stylesheet")
	}
	if r := get(h, "GET", "/theme/theme.css", "If-None-Match", `"abc"`); r.StatusCode != http.StatusNotModified {
		t.Errorf("a matching ETag got %d, want 304", r.StatusCode)
	}
	if r := get(h, "HEAD", "/theme/theme.css"); r.StatusCode != http.StatusOK {
		t.Errorf("HEAD got %d", r.StatusCode)
	}
	if r := get(h, "POST", "/theme/theme.css"); r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST got %d", r.StatusCode)
	}
}

func TestThemeFaviconFallsBackOnTheApplicationsOwn(t *testing.T) {
	own := get(themed(t, testFS(), fakeTheme{}), "GET", "/theme/favicon")
	if own.StatusCode != http.StatusOK || !strings.Contains(body(t, own), "xmlns") {
		t.Errorf("fallback: %d", own.StatusCode)
	}
	theirs := get(themed(t, testFS(), fakeTheme{icon: true}), "GET", "/theme/favicon")
	if theirs.StatusCode != http.StatusOK || body(t, theirs) != "<svg/>" {
		t.Errorf("the theme's icon was not served")
	}
}

func TestThemeFilesAreSandboxedAndImmutable(t *testing.T) {
	h := themed(t, testFS(), fakeTheme{})
	r := get(h, "GET", "/theme/a/s1/img/logo.svg")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status %d", r.StatusCode)
	}
	if got := r.Header.Get("Content-Security-Policy"); got != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
		t.Errorf("CSP %q", got)
	}
	if got := r.Header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control %q", got)
	}
	if r.Header.Get("Content-Type") != "image/svg+xml" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers %v", r.Header)
	}
	for _, target := range []string{
		"/theme/a/s1/other.svg", "/theme/a/unknown/img/logo.svg", "/theme/a/s1", "/theme/a/s1/",
		"/theme/a/s1/../s1/img/logo.svg/x", "/theme/a/", "/theme/a",
	} {
		if r := get(h, "GET", target); r.StatusCode != http.StatusNotFound {
			t.Errorf("%s got %d, want 404", target, r.StatusCode)
		}
	}
}

func TestThemePathsNeverGetTheShell(t *testing.T) {
	for _, th := range []web.ThemeProvider{fakeTheme{}, nil} {
		cfg := web.Config{}
		if th != nil {
			cfg.Theme = th
		}
		h, err := web.NewWithConfig(testFS(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"/theme", "/theme/", "/theme/unknown", "/theme/unknown.css", "/theme/a/x/y"} {
			r := get(h, "GET", target)
			if r.StatusCode != http.StatusNotFound || strings.Contains(body(t, r), "<html") {
				t.Errorf("theme=%v %s got %d", th != nil, target, r.StatusCode)
			}
		}
	}
	// Without a theme the stylesheet is a 404 too.
	if r := serve(t, "GET", "/theme/theme.css"); r.StatusCode != http.StatusNotFound {
		t.Errorf("got %d", r.StatusCode)
	}
}

// A theme changes what the page looks like, never what the page may load.
func TestPageCSPIsUnchangedByATheme(t *testing.T) {
	plain := serve(t, "GET", "/").Header.Get("Content-Security-Policy")
	for _, target := range []string{"/", "/theme/theme.css", "/theme/favicon"} {
		got := get(themed(t, testFS(), fakeTheme{}), "GET", target).Header.Get("Content-Security-Policy")
		if got != plain || got != web.ContentSecurityPolicy {
			t.Errorf("%s: CSP %q, want %q", target, got, plain)
		}
	}
}
