package wiring_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const themeManifest = `{"schema":1,"name":"%s","version":"1.0.0"}`

func writeThemeDir(t *testing.T, dir, name string) {
	t.Helper()
	m := strings.Replace(themeManifest, "%s", name, 1)
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestServerServesNoThemeByDefault(t *testing.T) {
	r := newRig(t, "")
	if code, _ := r.do("GET", "/theme/theme.css", "", ""); code != 404 {
		t.Errorf("/theme/theme.css got %d with no theme configured", code)
	}
	if _, page := r.do("GET", "/", "", ""); strings.Contains(page, "/theme/") {
		t.Errorf("the page links a theme nobody configured:\n%s", page)
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
