package wiring

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/theme"
	"github.com/VargaFoundation/trica/internal/web"
)

// themeService builds the theme service once (ADR 0054). It returns nil when
// the configuration names no theme: the console is then served as built, with
// no link and no /theme. A theme that cannot be read is not an error here:
// the service keeps the built-in default and says so (see theme.Service).
func (w *Wiring) themeService(d server.Deps) (*theme.Service, error) {
	w.themeOnce.Do(func() {
		t := w.cfg.Web.Theme
		if t == nil {
			return
		}
		m := newThemeMetrics(d.Metrics)
		s, err := theme.New(theme.Config{Builtin: t.Builtin, Path: t.Path, Reload: t.Reload}, d.Log)
		if err != nil {
			w.themeErr = err
			return
		}
		s.OnLoad = m.loaded
		s.OnError = func(error) { m.errors.WithLabelValues("path").Inc() }
		m.loaded(s.Active())
		w.theme = s
	})
	return w.theme, w.themeErr
}

// runTheme re-reads a path source until the server stops.
func (w *Wiring) runTheme(ctx context.Context, d server.Deps) error {
	if w.cfg.Web.Theme == nil || w.cfg.Web.Theme.Path == "" {
		return nil
	}
	s, err := w.themeService(d)
	if err != nil || s == nil {
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
			Help: "Reads of the configured UI theme that failed (the previous theme stays in force).",
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
