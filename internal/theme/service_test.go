package theme_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/theme"
)

func writeTheme(t *testing.T, dir, name string) {
	t.Helper()
	m := `{"schema":1,"name":"` + name + `","version":"1.0.0","favicon":"i.svg"}`
	must(t, os.WriteFile(filepath.Join(dir, "theme.json"), []byte(m), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "i.svg"), []byte(okSVG), 0o600))
}

func TestServiceKeepsServingWhenAThemeIsBad(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "theme.json"), []byte("{"), 0o600))
	var loaded []string
	var errs int
	s, err := theme.New(theme.Config{Path: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.OnLoad = func(th *theme.Theme) { loaded = append(loaded, th.Name) }
	s.OnError = func(string, error) { errs++ }
	if got := s.Active().Name; got != "default" {
		t.Fatalf("a bad theme at start: active is %q, want the built-in default", got)
	}

	s.Reload(context.Background())
	if errs != 1 {
		t.Fatalf("errors %d, want 1", errs)
	}
	writeTheme(t, dir, "one")
	s.Reload(context.Background())
	first := s.Active()
	if first.Name != "one" || first.Source != "path" || len(loaded) != 1 {
		t.Fatalf("active %+v loaded %v", first, loaded)
	}
	s.Reload(context.Background()) // unchanged: no swap, no callback
	if len(loaded) != 1 {
		t.Errorf("an unchanged theme was announced again: %v", loaded)
	}

	must(t, os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"schema":1,"name":"broken"}`), 0o600))
	s.Reload(context.Background())
	if s.Active().Name != "one" || errs != 2 {
		t.Errorf("after a bad edit: active %q, errors %d", s.Active().Name, errs)
	}

	writeTheme(t, dir, "two")
	s.Reload(context.Background())
	if s.Active().Name != "two" {
		t.Fatalf("active %q", s.Active().Name)
	}
	// A page loaded under the previous theme still gets its files.
	if _, ok := s.Lookup(first.SHA, "i.svg"); !ok {
		t.Error("the previous theme's file is gone at once")
	}
	if _, ok := s.Lookup(s.Active().SHA, "i.svg"); !ok {
		t.Error("the active theme's file is not served")
	}
	if _, ok := s.Lookup(s.Active().SHA, "theme.json"); ok {
		t.Error("the manifest is served")
	}
	if _, ok := s.Lookup("0000", "i.svg"); ok {
		t.Error("an unknown identity is served")
	}
}

func TestServiceConfig(t *testing.T) {
	if _, err := theme.New(theme.Config{Builtin: "nope"}, nil); err == nil {
		t.Error("an unknown built-in was accepted")
	}
	if _, err := theme.New(theme.Config{Builtin: "flat", Path: "/x"}, nil); err == nil {
		t.Error("two sources were accepted")
	}
	s, err := theme.New(theme.Config{Builtin: "flat"}, nil)
	if err != nil || s.Active().Name != "flat" {
		t.Fatalf("%v %v", s, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- s.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return")
	}
}

func TestServiceRunReReadsThePath(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "one")
	s, err := theme.New(theme.Config{Path: dir, Reload: 10 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	writeTheme(t, dir, "two")
	deadline := time.Now().Add(5 * time.Second)
	for s.Active().Name != "two" {
		if time.Now().After(deadline) {
			t.Fatal("the new theme was not picked up")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
