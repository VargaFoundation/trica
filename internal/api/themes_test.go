package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
)

// fakeThemes records what reached it and answers what the test says.
type fakeThemes struct {
	calls   []string
	uploads [][]byte
	err     error
}

func (f *fakeThemes) note(s string) { f.calls = append(f.calls, s) }

func (f *fakeThemes) Themes(context.Context) (api.ThemeState, error) {
	f.note("themes")
	return api.ThemeState{Active: api.ThemeRef{Source: "builtin", Name: "default"}}, f.err
}

func (f *fakeThemes) Upload(_ context.Context, b []byte, by string) (api.StoredTheme, bool, error) {
	f.note("upload:" + by)
	f.uploads = append(f.uploads, b)
	return api.StoredTheme{SHA256: strings.Repeat("a", 64), Name: "x"}, len(f.uploads) == 1, f.err
}

func (f *fakeThemes) Activate(_ context.Context, sel api.ThemeSelection, _ string) error {
	f.note("activate:" + sel.Builtin + sel.SHA256)
	return f.err
}

func (f *fakeThemes) Reset(context.Context, string) error { f.note("reset"); return f.err }

func (f *fakeThemes) Delete(_ context.Context, sha string) error {
	f.note("delete:" + sha)
	return f.err
}

func TestThemeEndpointsAreAdminOnly(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sha := strings.Repeat("a", 64)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/ui/themes", ""},
		{"POST", "/api/v1/ui/themes", "zip"},
		{"PUT", "/api/v1/ui/themes/active", `{"builtin":"flat"}`},
		{"DELETE", "/api/v1/ui/themes/active", ""},
		{"DELETE", "/api/v1/ui/themes/" + sha, ""},
	} {
		if r := e.do(c.method, c.path, tokReader, nilIfEmpty(c.body)); r.Status != 403 {
			t.Errorf("a reader on %s %s: %d, want 403", c.method, c.path, r.Status)
		}
	}
	if len(e.themes.calls) != 0 {
		t.Errorf("a refused caller reached the themes: %v", e.themes.calls)
	}
}

func TestThemeUpload(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	r := e.do("POST", "/api/v1/ui/themes", tokAdmin, []byte("PK\x03\x04archive"))
	if r.Status != 201 || len(e.themes.uploads) != 1 || string(e.themes.uploads[0]) != "PK\x03\x04archive" {
		t.Fatalf("%d %s, uploads %q", r.Status, r.Body, e.themes.uploads)
	}
	if r := e.do("POST", "/api/v1/ui/themes", tokAdmin, []byte("PK\x03\x04archive")); r.Status != 200 {
		t.Errorf("the same bytes again: %d, want 200", r.Status)
	}
	if r := e.do("POST", "/api/v1/ui/themes", tokAdmin, make([]byte, api.MaxThemeBody+1)); r.Status != 413 {
		t.Errorf("too large: %d, want 413", r.Status)
	}
	if r := e.do("POST", "/api/v1/ui/themes", tokAdmin, []byte{}); r.Status != 400 {
		t.Errorf("empty: %d, want 400", r.Status)
	}
	e.themes.err = &api.ThemeInvalidError{Detail: "theme.json: missing"}
	r = e.do("POST", "/api/v1/ui/themes", tokAdmin, []byte("x"))
	var p struct{ Title, Detail string }
	_ = json.Unmarshal(r.Body, &p)
	if r.Status != 422 || p.Title != "Invalid theme" || !strings.Contains(p.Detail, "theme.json: missing") {
		t.Errorf("invalid archive: %d %s", r.Status, r.Body)
	}
}

func TestThemeRefusalsAreProblems(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	sha := strings.Repeat("a", 64)
	for _, c := range []struct {
		err    error
		method string
		path   string
		body   string
		status int
	}{
		{api.ErrThemeLocked, "PUT", "/api/v1/ui/themes/active", `{"builtin":"flat"}`, 409},
		{api.ErrThemeLocked, "DELETE", "/api/v1/ui/themes/active", "", 409},
		{api.ErrThemeActive, "DELETE", "/api/v1/ui/themes/" + sha, "", 409},
		{api.ErrThemeNotFound, "DELETE", "/api/v1/ui/themes/" + sha, "", 404},
		{api.ErrThemeNotFound, "PUT", "/api/v1/ui/themes/active", `{"sha256":"` + sha + `"}`, 404},
	} {
		e.themes.err = c.err
		if r := e.do(c.method, c.path, tokAdmin, nilIfEmpty(c.body)); r.Status != c.status {
			t.Errorf("%s %s with %v: %d %s, want %d", c.method, c.path, c.err, r.Status, r.Body, c.status)
		}
	}
	e.themes.err = nil
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/v1/ui/themes/active", `{}`},
		{"PUT", "/api/v1/ui/themes/active", `{"builtin":"flat","sha256":"` + sha + `"}`},
		{"PUT", "/api/v1/ui/themes/active", `{"sha256":"not-a-digest"}`},
		{"DELETE", "/api/v1/ui/themes/not-a-digest", ""},
	} {
		if r := e.do(c.method, c.path, tokAdmin, nilIfEmpty(c.body)); r.Status != 400 {
			t.Errorf("%s %s %s: %d, want 400", c.method, c.path, c.body, r.Status)
		}
	}
}

func TestThemeUploadIsDocumentedAsBinary(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	_, doc := openAPIDoc(t, e)
	op := doc["paths"].(map[string]any)["/ui/themes"].(map[string]any)["post"].(map[string]any)
	content := op["requestBody"].(map[string]any)["content"].(map[string]any)
	if _, ok := content["application/zip"]; !ok {
		t.Errorf("the upload's body is not documented as a zip: %v", content)
	}
}
