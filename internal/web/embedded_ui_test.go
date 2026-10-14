//go:build webui

package web_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/web"
)

// These tests run against the real build (make web-embed, then
// go test -tags webui ./internal/web): they check that what Vite produced can
// work under the strict CSP the handler sends, and that the handler treats it
// as designed.

func builtFS(t *testing.T) fs.FS {
	t.Helper()
	sub, err := web.EmbeddedForTest()
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

var (
	scriptTag    = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	inlineStyle  = regexp.MustCompile(`(?is)<style\b`)
	styleAttr    = regexp.MustCompile(`(?is)\sstyle\s*=`)
	eventAttr    = regexp.MustCompile(`(?is)\son[a-z]+\s*=`)
	assetRef     = regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)
	hashedName   = regexp.MustCompile(`^assets/.+-[A-Za-z0-9_-]{8}\.[a-z0-9]+$`)
)

func TestBuiltIndexWorksUnderTheCSP(t *testing.T) {
	if !web.Built {
		t.Fatal("this file is built only with -tags webui")
	}
	b, err := fs.ReadFile(builtFS(t), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if strings.Contains(html, `content="placeholder"`) {
		t.Fatal("the embedded index.html is the placeholder: run make web-embed")
	}
	for _, tag := range scriptTag.FindAllString(html, -1) {
		if !strings.Contains(strings.ToLower(tag), "src=") {
			t.Errorf("index.html has an inline <script> (%s): script-src 'self' would block it", tag)
		}
	}
	if inlineStyle.MatchString(html) || styleAttr.MatchString(html) {
		t.Error("index.html has inline style: style-src 'self' would block it")
	}
	if eventAttr.MatchString(html) {
		t.Error("index.html has an inline event handler")
	}
	if !strings.Contains(html, `<div id="root">`) {
		t.Error("no #root element")
	}
	refs := assetRef.FindAllStringSubmatch(html, -1)
	if len(refs) < 2 {
		t.Fatalf("index.html references %d assets, want at least a script and a stylesheet: %s", len(refs), html)
	}
	h, err := web.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range refs {
		name := strings.TrimPrefix(m[1], "/")
		if !hashedName.MatchString(name) {
			t.Errorf("%s: not a content-hashed name, so \"immutable\" would be wrong", name)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, m[1], nil))
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("%s: status %d, cache %q", m[1], rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
}

// Nothing that the strict CSP blocks may be needed by the stylesheet: a data:
// URL (img-src and font-src allow 'self' only) or a remote import.
func TestBuiltAssetsNeedNothingTheCSPForbids(t *testing.T) {
	sub := builtFS(t)
	err := fs.WalkDir(sub, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".css") {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		css := string(b)
		if strings.Contains(css, "url(data:") || strings.Contains(css, `url("data:`) {
			t.Errorf("%s uses a data: URL, which img-src and font-src forbid", p)
		}
		if strings.Contains(css, "@import") || strings.Contains(css, "http://") || strings.Contains(css, "https://") {
			t.Errorf("%s loads something from another origin", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
