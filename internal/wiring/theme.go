package wiring

import (
	"context"
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/theme"
	"github.com/VargaFoundation/trica/internal/web"
)

// themeService builds the theme service once (ADR 0054). It always exists: the
// configuration is the deployment's default, and an administrator may select
// another theme whatever the configuration says. A theme that cannot be read
// is not an error here: the service keeps the previous one (the built-in
// default at start) and says so (see theme.Service).
func (w *Wiring) themeService(d server.Deps) (*theme.Service, error) {
	w.themeOnce.Do(func() {
		cfg := theme.Config{Store: &theme.Store{DB: d.Store}, Client: w.themeClient}
		if t := w.cfg.Web.Theme; t != nil {
			cfg.Builtin, cfg.Path, cfg.URL, cfg.SHA256, cfg.Locked, cfg.Reload = t.Builtin, t.Path, t.URL, t.SHA256, t.Locked, t.Reload
		}
		m := newThemeMetrics(d.Metrics)
		s, err := theme.New(cfg, d.Log)
		if err != nil {
			w.themeErr = err
			return
		}
		s.OnLoad = m.loaded
		s.OnError = func(source string, _ error) { m.errors.WithLabelValues(source).Inc() }
		m.loaded(s.Active())
		w.theme = s
	})
	return w.theme, w.themeErr
}

// runTheme keeps the theme current until the server stops: a path is read
// again, a URL downloaded, and the selection follows the database, across
// replicas.
func (w *Wiring) runTheme(ctx context.Context, d server.Deps) error {
	s, err := w.themeService(d)
	if err != nil {
		return err
	}
	return s.Run(ctx)
}

type themeMetrics struct {
	info   *prometheus.GaugeVec
	errors *prometheus.CounterVec
}

func newThemeMetrics(reg prometheus.Registerer) *themeMetrics {
	m := &themeMetrics{
		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trica_ui_theme_info",
			Help: "The UI theme in force: 1 for the theme named by the labels.",
		}, []string{"name", "version", "sha256", "source"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trica_ui_theme_load_errors_total",
			Help: "Reads of a UI theme that failed (the previous theme stays in force); source is path, url, stored or selection.",
		}, []string{"source"}),
	}
	reg.MustRegister(m.info, m.errors)
	return m
}

func (m *themeMetrics) loaded(t *theme.Theme) {
	m.info.Reset()
	m.info.WithLabelValues(t.Name, t.Version, t.SHA, t.Source).Set(1)
}

// themeProvider adapts the service to what the web handler asks.
type themeProvider struct{ s *theme.Service }

func (p themeProvider) Stylesheet() ([]byte, string) {
	t := p.s.Active()
	return t.CSS(), t.ETag()
}

func (p themeProvider) Favicon() (web.ThemeAsset, bool) {
	a, ok := p.s.Active().Favicon()
	return web.ThemeAsset(a), ok
}

func (p themeProvider) Asset(sha, file string) (web.ThemeAsset, bool) {
	a, ok := p.s.Lookup(sha, file)
	return web.ThemeAsset(a), ok
}

// themeAdmin adapts the service to the API: its types, its errors (ADR 0025).
type themeAdmin struct{ s *theme.Service }

var _ api.ThemeAdmin = themeAdmin{}

func apiRef(r theme.Ref) api.ThemeRef {
	return api.ThemeRef{Source: r.Source, Name: r.Name, Title: r.Title, Version: r.Version, SHA256: r.SHA256}
}

func apiStored(i theme.Info) api.StoredTheme {
	return api.StoredTheme{SHA256: i.SHA256, Name: i.Name, Title: i.Title, Version: i.Version, Size: i.Size,
		Origin: i.Origin, UploadedBy: i.UploadedBy, UploadedAt: i.UploadedAt}
}

// apiErr maps what the theme package refuses to what the API says.
func apiErr(err error) error {
	var refused *theme.RefusedError
	switch {
	case errors.As(err, &refused):
		return &api.ThemeInvalidError{Detail: refused.Error()}
	case errors.Is(err, theme.ErrLocked):
		return api.ErrThemeLocked
	case errors.Is(err, theme.ErrActive):
		return api.ErrThemeActive
	case errors.Is(err, theme.ErrNotFound):
		return api.ErrThemeNotFound
	}
	return err
}

func (a themeAdmin) Themes(ctx context.Context) (api.ThemeState, error) {
	st, err := a.s.State(ctx)
	if err != nil {
		return api.ThemeState{}, err
	}
	out := api.ThemeState{Active: apiRef(st.Active), DeploymentDefault: apiRef(st.Default), Locked: st.Locked,
		Builtins: []api.ThemeRef{}, Themes: []api.StoredTheme{}}
	for _, b := range st.Builtins {
		out.Builtins = append(out.Builtins, apiRef(b))
	}
	for _, i := range st.Themes {
		out.Themes = append(out.Themes, apiStored(i))
	}
	return out, nil
}

func (a themeAdmin) Upload(ctx context.Context, archive []byte, by string) (api.StoredTheme, bool, error) {
	i, created, err := a.s.Upload(ctx, archive, by)
	return apiStored(i), created, apiErr(err)
}

func (a themeAdmin) Activate(ctx context.Context, sel api.ThemeSelection, by string) error {
	return apiErr(a.s.Activate(ctx, theme.Selection{Builtin: sel.Builtin, SHA256: sel.SHA256, SetBy: by}))
}

func (a themeAdmin) Reset(ctx context.Context, _ string) error { return apiErr(a.s.Reset(ctx)) }

func (a themeAdmin) Delete(ctx context.Context, sha string) error {
	return apiErr(a.s.Delete(ctx, sha))
}

// SetThemeClient sets the client that downloads a theme URL; it is meant for
// tests, which serve the archive from a server of their own.
func (w *Wiring) SetThemeClient(c *http.Client) { w.themeClient = c }
