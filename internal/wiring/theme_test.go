package wiring_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/store"
)

const themeManifest = `{"schema":1,"name":"%s","version":"1.0.0"}`

func writeThemeDir(t *testing.T, dir, name string) {
	t.Helper()
	m := strings.Replace(themeManifest, "%s", name, 1)
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
}

// With no web.theme the built-in default is in force, and the page links it:
// an administrator may select another theme at any time (ADR 0054).
func TestServerServesTheDefaultThemeWhenNoneIsConfigured(t *testing.T) {
	r := newRig(t, "")
	code, css := r.do("GET", "/theme/theme.css", "", "")
	if code != 200 || !strings.Contains(css, "trica theme default") {
		t.Fatalf("got %d:\n%.200s", code, css)
	}
	if _, page := r.do("GET", "/", "", ""); !strings.Contains(page, `href="/theme/theme.css"`) {
		t.Errorf("the page does not link the theme:\n%s", page)
	}
}

func TestServerServesTheConfiguredTheme(t *testing.T) {
	r := newRig(t, "web: {theme: {builtin: flat}}")
	// Public: the sign-in page is themed too, so no token is sent.
	code, css := r.do("GET", "/theme/theme.css", "", "")
	if code != 200 || !strings.Contains(css, "--trica-radius-control:0px") {
		t.Fatalf("got %d:\n%.300s", code, css)
	}
	if _, page := r.do("GET", "/", "", ""); !strings.Contains(page, `href="/theme/theme.css"`) {
		t.Errorf("the page does not link the theme:\n%s", page)
	}
	_, metrics := r.do("GET", "/metrics", "", "")
	if !strings.Contains(metrics, `trica_ui_theme_info{name="flat"`) {
		t.Errorf("the theme in force is not in the metrics")
	}
}

func TestABadThemeDoesNotStopTheServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, "web: {theme: {path: "+dir+", reload: 50ms}}")
	// The built-in default serves, and the failure is counted.
	code, css := r.do("GET", "/theme/theme.css", "", "")
	if code != 200 || !strings.Contains(css, "trica theme default") {
		t.Fatalf("got %d:\n%.200s", code, css)
	}
	waitFor(t, func() bool {
		_, m := r.do("GET", "/metrics", "", "")
		return strings.Contains(m, `trica_ui_theme_load_errors_total{source="path"}`)
	})
}

func TestThemeDirectoryIsReReadWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	writeThemeDir(t, dir, "one")
	r := newRig(t, "web: {theme: {path: "+dir+", reload: 50ms}}")
	if _, css := r.do("GET", "/theme/theme.css", "", ""); !strings.Contains(css, "trica theme one") {
		t.Fatalf("first theme not served:\n%.200s", css)
	}
	writeThemeDir(t, dir, "two")
	waitFor(t, func() bool {
		_, css := r.do("GET", "/theme/theme.css", "", "")
		return strings.Contains(css, "trica theme two")
	})
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// zipTheme builds a valid theme archive; the colour makes two themes differ
// even under one name.
func zipTheme(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("theme.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`{"schema":1,"name":"` + name + `","title":"` + name + ` theme","version":"1.0.0","radius":{"control":"0px","surface":"0px"}}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// send sends a request with a raw body (an archive), as the console does.
func (r *rig) send(method, path, token, contentType string, body []byte) (int, string) {
	r.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, r.base+path, bytes.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func (r *rig) themeCSS() string {
	r.t.Helper()
	_, css := r.do("GET", "/theme/theme.css", "", "")
	return css
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (r *rig) auditActions() []string {
	r.t.Helper()
	st, err := store.Open(context.Background(), r.url)
	if err != nil {
		r.t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.Pool.Query(context.Background(), `SELECT action || ':' || outcome FROM audit_log ORDER BY id`)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func problemSlug(t *testing.T, body string) (slug, title string) {
	t.Helper()
	var p struct{ Type, Title string }
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("not a problem: %s", body)
	}
	return strings.TrimPrefix(p.Type, "urn:trica:problem:"), p.Title
}

func TestAdminUploadsActivatesResetsAndDeletes(t *testing.T) {
	r := newRig(t, "")
	const themes = "/api/v1/ui/themes"
	zipA := zipTheme(t, "alpha")
	shaA := shaOf(zipA)

	t.Run("only an administrator", func(t *testing.T) {
		for _, c := range []struct{ method, path, body string }{
			{"GET", themes, ""}, {"PUT", themes + "/active", `{"builtin":"flat"}`},
			{"DELETE", themes + "/active", ""}, {"DELETE", themes + "/" + shaA, ""},
		} {
			if code, _ := r.do(c.method, c.path, r.readToken, c.body); code != 403 {
				t.Errorf("reader %s %s: %d, want 403", c.method, c.path, code)
			}
			if code, _ := r.do(c.method, c.path, r.token, c.body); code != 403 {
				t.Errorf("writer %s %s: %d, want 403", c.method, c.path, code)
			}
			if code, _ := r.do(c.method, c.path, "", c.body); code != 401 {
				t.Errorf("anonymous %s %s: %d, want 401", c.method, c.path, code)
			}
		}
		if code, _ := r.send("POST", themes, r.readToken, "application/zip", zipA); code != 403 {
			t.Errorf("reader upload: %d, want 403", code)
		}
		var n int
		_, body := r.do("GET", themes, r.adminToken, "")
		var st struct{ Themes []any }
		_ = json.Unmarshal([]byte(body), &st)
		if n = len(st.Themes); n != 0 {
			t.Errorf("a refused upload stored %d theme(s)", n)
		}
	})

	t.Run("upload validates and keeps", func(t *testing.T) {
		if code, body := r.send("POST", themes, r.adminToken, "application/zip", zipA); code != 201 || !strings.Contains(body, shaA) {
			t.Fatalf("first upload: %d %s", code, body)
		}
		if code, _ := r.send("POST", themes, r.adminToken, "application/zip", zipA); code != 200 {
			t.Errorf("same bytes again: %d, want 200", code)
		}
		code, body := r.send("POST", themes, r.adminToken, "application/zip", []byte("not a zip"))
		if slug, title := problemSlug(t, body); code != 422 || slug != "invalid-theme" || title != "Invalid theme" {
			t.Errorf("bad archive: %d %s", code, body)
		}
		if code, _ := r.send("POST", themes, r.adminToken, "application/zip", make([]byte, 3<<20)); code != 413 {
			t.Errorf("3 MiB body: %d, want 413", code)
		}
		if code, _ := r.send("POST", themes, r.adminToken, "application/zip", nil); code != 400 {
			t.Errorf("empty body: %d, want 400", code)
		}
	})

	t.Run("activation changes the theme and the active one stays", func(t *testing.T) {
		if strings.Contains(r.themeCSS(), "trica theme alpha") {
			t.Fatal("uploaded is not selected")
		}
		if code, body := r.do("PUT", themes+"/active", r.adminToken, `{"sha256":"`+shaA+`"}`); code != 200 {
			t.Fatalf("activate: %d %s", code, body)
		}
		if !strings.Contains(r.themeCSS(), "trica theme alpha") {
			t.Fatal("the activated theme is not served")
		}
		code, body := r.do("DELETE", themes+"/"+shaA, r.adminToken, "")
		if slug, _ := problemSlug(t, body); code != 409 || slug != "theme-active" {
			t.Fatalf("delete the active theme: %d %s", code, body)
		}
		if code, _ := r.do("PUT", themes+"/active", r.adminToken, `{"sha256":"`+strings.Repeat("0", 64)+`"}`); code != 404 {
			t.Errorf("unknown archive: %d, want 404", code)
		}
		if code, _ := r.do("PUT", themes+"/active", r.adminToken, `{"builtin":"neon"}`); code != 404 {
			t.Errorf("unknown built-in: %d, want 404", code)
		}
		if code, _ := r.do("PUT", themes+"/active", r.adminToken, `{"builtin":"flat","sha256":"`+shaA+`"}`); code != 400 {
			t.Errorf("two choices: %d, want 400", code)
		}
	})

	t.Run("reset returns to the default and delete works", func(t *testing.T) {
		if code, _ := r.do("DELETE", themes+"/active", r.adminToken, ""); code != 204 {
			t.Fatalf("reset: %d", code)
		}
		if !strings.Contains(r.themeCSS(), "trica theme default") {
			t.Fatal("the default is not back")
		}
		if code, body := r.do("PUT", themes+"/active", r.adminToken, `{"builtin":"flat"}`); code != 200 || !strings.Contains(body, `"source":"builtin"`) {
			t.Fatalf("select a built-in: %d %s", code, body)
		}
		if !strings.Contains(r.themeCSS(), "trica theme flat") {
			t.Fatal("the built-in is not served")
		}
		if code, _ := r.do("DELETE", themes+"/"+shaA, r.adminToken, ""); code != 204 {
			t.Errorf("delete: %d, want 204", code)
		}
		if code, _ := r.do("DELETE", themes+"/"+shaA, r.adminToken, ""); code != 404 {
			t.Errorf("delete again: %d, want 404", code)
		}
	})

	t.Run("every change is in the audit log", func(t *testing.T) {
		got := strings.Join(r.auditActions(), " ")
		for _, want := range []string{"theme.upload:success", "theme.activate:success", "theme.reset:success", "theme.delete:success", "theme.delete:failure", "theme.upload:denied"} {
			if !strings.Contains(got, want) {
				t.Errorf("no %s in the audit log: %s", want, got)
			}
		}
	})
}

func TestLockedConfigurationAlwaysWins(t *testing.T) {
	r := newRig(t, "web: {theme: {builtin: flat, locked: true}}")
	const themes = "/api/v1/ui/themes"
	if code, _ := r.send("POST", themes, r.adminToken, "application/zip", zipTheme(t, "alpha")); code != 201 {
		t.Fatalf("an upload is only storage, and stays possible: %d", code)
	}
	for _, c := range []struct{ method, path, body string }{
		{"PUT", themes + "/active", `{"builtin":"default"}`}, {"DELETE", themes + "/active", ""},
	} {
		code, body := r.do(c.method, c.path, r.adminToken, c.body)
		if slug, _ := problemSlug(t, body); code != 409 || slug != "theme-locked" {
			t.Errorf("%s %s: %d %s", c.method, c.path, code, body)
		}
	}
	if !strings.Contains(r.themeCSS(), "trica theme flat") {
		t.Error("the locked theme is not the one served")
	}
	if _, body := r.do("GET", themes, r.adminToken, ""); !strings.Contains(body, `"locked":true`) {
		t.Errorf("the state does not say locked: %s", body)
	}
}

// A selection made through one replica reaches another that re-reads nothing
// on a timer: it is the NOTIFY that carries it (ADR 0032).
func TestAnotherReplicaSeesTheActivation(t *testing.T) {
	first := newRig(t, "web: {theme: {builtin: default, reload: 1h}}")
	second := &rig{t: t, url: first.url, caDir: first.caDir, adminToken: first.adminToken}
	second.start("web: {theme: {builtin: default, reload: 1h}}")

	// Both listeners are in place: what follows can only travel by NOTIFY.
	st, err := store.Open(context.Background(), first.url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	waitFor(t, func() bool {
		var n int
		err := st.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND query LIKE 'LISTEN%trica_theme%'`).Scan(&n)
		return err == nil && n >= 2
	})

	if code, body := first.do("PUT", "/api/v1/ui/themes/active", first.adminToken, `{"builtin":"flat"}`); code != 200 {
		t.Fatalf("activate: %d %s", code, body)
	}
	waitFor(t, func() bool { return strings.Contains(second.themeCSS(), "trica theme flat") })

	if code, _ := first.do("DELETE", "/api/v1/ui/themes/active", first.adminToken, ""); code != 204 {
		t.Fatalf("reset: %d", code)
	}
	waitFor(t, func() bool { return strings.Contains(second.themeCSS(), "trica theme default") })
}

func TestURLThemeIsFetchedOnceAndKept(t *testing.T) {
	archive := zipTheme(t, "remote")
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	useClient := func(r *rig) { r.themeClient = srv.Client() }
	cfg := func(sum string) string {
		return "web: {theme: {url: " + srv.URL + "/t.zip, sha256: " + sum + ", reload: 50ms}}"
	}

	t.Run("a wrong digest applies nothing and keeps nothing", func(t *testing.T) {
		r := newRig(t, cfg(strings.Repeat("0", 64)), useClient)
		waitFor(t, func() bool {
			_, m := r.do("GET", "/metrics", "", "")
			return strings.Contains(m, `trica_ui_theme_load_errors_total{source="url"}`)
		})
		if !strings.Contains(r.themeCSS(), "trica theme default") {
			t.Error("a download that does not match its digest was applied")
		}
		_, body := r.do("GET", "/api/v1/ui/themes", r.adminToken, "")
		if !strings.Contains(body, `"themes":[]`) {
			t.Errorf("a download that does not match its digest was kept: %s", body)
		}
	})

	t.Run("a right digest is fetched once and then kept", func(t *testing.T) {
		hits.Store(0)
		r := newRig(t, cfg(shaOf(archive)), useClient)
		waitFor(t, func() bool { return strings.Contains(r.themeCSS(), "trica theme remote") })
		time.Sleep(300 * time.Millisecond) // several reloads
		if n := hits.Load(); n != 1 {
			t.Errorf("the archive was downloaded %d times, want once", n)
		}
		if _, body := r.do("GET", "/api/v1/ui/themes", r.adminToken, ""); !strings.Contains(body, `"origin":"url"`) {
			t.Errorf("the archive is not kept: %s", body)
		}
		// The bucket is gone: a restart still has the theme, at once.
		srv.Close()
		r.start(cfg(shaOf(archive)))
		if !strings.Contains(r.themeCSS(), "trica theme remote") {
			t.Error("after a restart with the source gone, the kept theme is not served")
		}
		if n := hits.Load(); n != 1 {
			t.Errorf("a restart downloaded again (%d)", n)
		}
	})
}
