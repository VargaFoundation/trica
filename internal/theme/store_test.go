package theme_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/internal/theme"
)

// namedArchive is a valid archive that differs from another by its name.
func namedArchive(t *testing.T, name string) []byte {
	t.Helper()
	return zipOf(t, map[string]string{"theme.json": manifestWith(t, func(m map[string]any) { m["name"] = name })})
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newStore(t *testing.T) *theme.Store { return &theme.Store{DB: testdb.New(t)} }

func TestStoreKeepsSelectsAndDeletes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := namedArchive(t, "alpha")
	th, err := theme.ReadArchive(a)
	if err != nil {
		t.Fatal(err)
	}

	if created, err := s.Put(ctx, th, a, theme.OriginUpload, "user:alice"); err != nil || !created {
		t.Fatalf("first put: %v %v", created, err)
	}
	if created, err := s.Put(ctx, th, a, theme.OriginUpload, "user:bob"); err != nil || created {
		t.Fatalf("same bytes: %v %v", created, err)
	}
	got, err := s.Archive(ctx, th.SHA)
	if err != nil || digest(got) != th.SHA {
		t.Fatalf("archive back: %v", err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "alpha" || list[0].UploadedBy != "user:alice" || list[0].Size != len(a) {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := s.Archive(ctx, strings.Repeat("0", 64)); !errors.Is(err, theme.ErrNotFound) {
		t.Errorf("unknown archive: %v", err)
	}

	if sel, err := s.Selected(ctx); err != nil || sel != nil {
		t.Fatalf("nothing selected yet: %v %v", sel, err)
	}
	if err := s.Select(ctx, theme.Selection{SHA256: strings.Repeat("0", 64), SetBy: "x"}); !errors.Is(err, theme.ErrNotFound) {
		t.Errorf("selecting an archive nobody has: %v", err)
	}
	if err := s.Select(ctx, theme.Selection{SHA256: th.SHA, SetBy: "user:alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, th.SHA); !errors.Is(err, theme.ErrActive) {
		t.Errorf("deleting the selected archive: %v", err)
	}
	if err := s.Select(ctx, theme.Selection{Builtin: "flat", SetBy: "user:alice"}); err != nil {
		t.Fatal(err)
	}
	if sel, err := s.Selected(ctx); err != nil || sel == nil || sel.Builtin != "flat" || sel.SHA256 != "" {
		t.Fatalf("selected: %+v %v", sel, err)
	}
	if err := s.Delete(ctx, th.SHA); err != nil {
		t.Fatalf("delete once unselected: %v", err)
	}
	if err := s.Delete(ctx, th.SHA); !errors.Is(err, theme.ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if sel, _ := s.Selected(ctx); sel != nil {
		t.Errorf("still selected after Clear: %+v", sel)
	}
}

func TestStoreRefusesABadDigest(t *testing.T) {
	s := newStore(t)
	for _, sha := range []string{"", "abc", strings.Repeat("g", 64)} {
		if _, err := s.Archive(context.Background(), sha); err == nil {
			t.Errorf("%q: %v", sha, err)
		}
	}
}

func TestFetch(t *testing.T) {
	archive := namedArchive(t, "remote")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.zip":
			_, _ = w.Write(archive)
		case "/big.zip":
			_, _ = w.Write(make([]byte, theme.MaxArchiveBytes+10))
		case "/gone.zip":
			http.NotFound(w, r)
		case "/to-http.zip":
			http.Redirect(w, r, "http://example.invalid/x.zip", http.StatusFound)
		case "/to-https.zip":
			http.Redirect(w, r, "/ok.zip", http.StatusFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	want := digest(archive)

	got, err := theme.Fetch(ctx, srv.Client(), srv.URL+"/ok.zip", want)
	if err != nil || digest(got) != want {
		t.Fatalf("a good download: %v", err)
	}
	if _, err := theme.Fetch(ctx, srv.Client(), srv.URL+"/to-https.zip", want); err != nil {
		t.Errorf("a redirect inside https: %v", err)
	}
	for name, c := range map[string]struct{ path, sha string }{
		"wrong digest":        {"/ok.zip", strings.Repeat("0", 64)},
		"too large":           {"/big.zip", want},
		"not found":           {"/gone.zip", want},
		"redirect to http":    {"/to-http.zip", want},
		"not a digest at all": {"/ok.zip", "nope"},
	} {
		if b, err := theme.Fetch(ctx, srv.Client(), srv.URL+c.path, c.sha); err == nil || b != nil {
			t.Errorf("%s: got %d bytes, %v", name, len(b), err)
		}
	}
	if _, err := theme.Fetch(ctx, srv.Client(), srv.URL+"/ok.zip", strings.Repeat("0", 64)); !errors.Is(err, theme.ErrDigest) {
		t.Errorf("the digest error is not ErrDigest: %v", err)
	}
	for _, bad := range []string{"http://example.com/t.zip", "https://", "https://user:pw@example.com/t.zip", "ftp://x/y", "://"} {
		if err := theme.ValidateURL(bad); err == nil {
			t.Errorf("%q passed", bad)
		}
	}
	if err := theme.ValidateURL("https://example.com/t.zip"); err != nil {
		t.Error(err)
	}
}

func newService(t *testing.T, cfg theme.Config) (*theme.Service, *theme.Store) {
	t.Helper()
	st := newStore(t)
	cfg.Store = st
	s, err := theme.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestServicePrecedence(t *testing.T) {
	ctx := context.Background()
	a := namedArchive(t, "alpha")
	s, _ := newService(t, theme.Config{Builtin: "flat"})
	if s.Active().Name != "flat" {
		t.Fatalf("the deployment default: %s", s.Active().Name)
	}
	info, created, err := s.Upload(ctx, a, "user:alice")
	if err != nil || !created {
		t.Fatal(err)
	}
	if s.Active().Name != "flat" {
		t.Fatal("an upload alone must not change the theme")
	}
	if err := s.Activate(ctx, theme.Selection{SHA256: info.SHA256, SetBy: "user:alice"}); err != nil {
		t.Fatal(err)
	}
	if s.Active().Name != "alpha" {
		t.Fatalf("the selection wins over the default: %s", s.Active().Name)
	}
	if err := s.Delete(ctx, info.SHA256); !errors.Is(err, theme.ErrActive) {
		t.Fatalf("the theme in force must not be deleted: %v", err)
	}
	if err := s.Activate(ctx, theme.Selection{Builtin: "neon"}); !errors.Is(err, theme.ErrNotFound) {
		t.Errorf("unknown built-in: %v", err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Active().Name != "flat" {
		t.Fatalf("reset returns to the default: %s", s.Active().Name)
	}
	if err := s.Delete(ctx, info.SHA256); err != nil {
		t.Fatalf("delete after reset: %v", err)
	}
	st, err := s.State(ctx)
	if err != nil || st.Active.Source != "config" || st.Default.Name != "flat" || len(st.Themes) != 0 || len(st.Builtins) < 2 {
		t.Errorf("state: %+v %v", st, err)
	}
}

func TestServiceLockedIgnoresTheSelection(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	a := namedArchive(t, "alpha")
	th, _ := theme.ReadArchive(a)
	if _, err := st.Put(ctx, th, a, theme.OriginUpload, "x"); err != nil {
		t.Fatal(err)
	}
	// A selection made before the lock stays in the database, and is ignored.
	if err := st.Select(ctx, theme.Selection{SHA256: th.SHA, SetBy: "x"}); err != nil {
		t.Fatal(err)
	}
	s, err := theme.New(theme.Config{Builtin: "flat", Locked: true, Store: st}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Active().Name != "flat" {
		t.Fatalf("locked: the selection applied (%s)", s.Active().Name)
	}
	if err := s.Activate(ctx, theme.Selection{Builtin: "default"}); !errors.Is(err, theme.ErrLocked) {
		t.Errorf("activate: %v", err)
	}
	if err := s.Reset(ctx); !errors.Is(err, theme.ErrLocked) {
		t.Errorf("reset: %v", err)
	}
	if !s.Locked() {
		t.Error("Locked() is false")
	}
	if _, err := theme.New(theme.Config{Locked: true, Store: st}, nil); err == nil {
		t.Error("locked with nothing to lock to was accepted")
	}
}

func TestServiceLookupReadsAThemeAnotherReplicaSelected(t *testing.T) {
	ctx := context.Background()
	a := zipOf(t, map[string]string{
		"theme.json": manifestWith(t, func(m map[string]any) { m["name"] = "alpha"; m["favicon"] = "icon.svg" }),
		"icon.svg":   okSVG,
	})
	one, st := newService(t, theme.Config{})
	two, err := theme.New(theme.Config{Store: st}, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, _, err := one.Upload(ctx, a, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := two.Lookup(info.SHA256, "icon.svg"); !ok {
		t.Error("a page built by another replica asks this one for its stylesheet: not served")
	}
	if _, ok := two.Lookup(strings.Repeat("1", 64), "theme.css"); ok {
		t.Error("an unknown theme was served")
	}
	if _, ok := two.Lookup("../etc", "theme.css"); ok {
		t.Error("a malformed identity was served")
	}
}

func TestServiceURLSource(t *testing.T) {
	ctx := context.Background()
	archive := namedArchive(t, "remote")
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	st := newStore(t)
	cfg := theme.Config{URL: srv.URL + "/t.zip", SHA256: digest(archive), Store: st, Client: srv.Client()}

	s, err := theme.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 0 || s.Active().Name == "remote" {
		t.Fatal("New must not download: the first Reload does")
	}
	s.Reload(ctx)
	s.Reload(ctx)
	if s.Active().Name != "remote" || hits != 1 {
		t.Fatalf("active %s after %d downloads", s.Active().Name, hits)
	}
	list, _ := st.List(ctx)
	if len(list) != 1 || list[0].Origin != theme.OriginURL {
		t.Fatalf("kept: %+v", list)
	}
	if err := s.Delete(ctx, digest(archive)); !errors.Is(err, theme.ErrActive) {
		t.Fatalf("deleting the configured theme in force: %v", err)
	}
	srv.Close()
	again, err := theme.New(cfg, nil)
	if err != nil || again.Active().Name != "remote" {
		t.Fatalf("restart with the source gone: %v", err)
	}

	for name, bad := range map[string]theme.Config{
		"no digest":    {URL: cfg.URL, Store: st},
		"bad digest":   {URL: cfg.URL, SHA256: "zz", Store: st},
		"http":         {URL: "http://example.com/t.zip", SHA256: cfg.SHA256, Store: st},
		"no database":  {URL: cfg.URL, SHA256: cfg.SHA256},
		"two sources":  {URL: cfg.URL, SHA256: cfg.SHA256, Builtin: "flat", Store: st},
		"url and path": {URL: cfg.URL, SHA256: cfg.SHA256, Path: t.TempDir(), Store: st},
	} {
		if _, err := theme.New(bad, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestServiceRunFollowsTheDatabaseOnItsTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := namedArchive(t, "alpha")
	s, st := newService(t, theme.Config{Reload: 30 * time.Millisecond})
	go func() { _ = s.Run(ctx) }()
	th, _ := theme.ReadArchive(a)
	if _, err := st.Put(ctx, th, a, theme.OriginUpload, "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.Select(ctx, theme.Selection{SHA256: th.SHA, SetBy: "other replica"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Active().Name != "alpha" {
		if time.Now().After(deadline) {
			t.Fatal("a selection made elsewhere never reached this replica")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
