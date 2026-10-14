package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// Deps is what a component receives: the shared store, engine and schema.
type Deps struct {
	Store   *store.Store
	Engine  *reconcile.Engine
	Schema  func() *schema.Registry
	Log     *slog.Logger
	Metrics prometheus.Registerer
}

// Component is a part of the server: the API, the MCP server, the
// transport, the deducer. Mount registers its routes; Run, when set, is a
// background loop that runs until the context ends.
type Component struct {
	Name  string
	Mount func(mux *http.ServeMux, d Deps) error
	Run   func(ctx context.Context, d Deps) error
}

// Server is a running trica server.
type Server struct {
	cfg        *Config
	log        *slog.Logger
	components []Component
	version    string

	ready   atomic.Bool
	addr    atomic.Value // string, the bound address once listening
	metrics *metrics
}

type metrics struct {
	reg         *prometheus.Registry
	sweeps      prometheus.Counter
	sweepErrors prometheus.Counter
	absent      prometheus.Counter
}

func newMetrics(version string) *metrics {
	reg := prometheus.NewRegistry()
	m := &metrics{
		reg:         reg,
		sweeps:      prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_absence_sweeps_total", Help: "Absence sweeps run."}),
		sweepErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_absence_sweep_errors_total", Help: "Absence sweeps that failed."}),
		absent:      prometheus.NewCounter(prometheus.CounterOpts{Name: "trica_objects_marked_absent_total", Help: "Objects marked absent by the sweeper."}),
	}
	info := prometheus.NewGauge(prometheus.GaugeOpts{Name: "trica_build_info", Help: "Build information.", ConstLabels: prometheus.Labels{"version": version}})
	info.Set(1)
	reg.MustRegister(m.sweeps, m.sweepErrors, m.absent, info,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// New prepares a server; nothing is opened until Run.
func New(cfg *Config, log *slog.Logger, version string, components ...Component) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, log: log, components: components, version: version, metrics: newMetrics(version)}
}

// Addr returns the address the server listens on, once it does.
func (s *Server) Addr() string {
	a, _ := s.addr.Load().(string)
	return a
}

// LoadSchema returns the built-in schema with the configured overlays merged.
func LoadSchema(overlays []string) (*schema.Registry, error) {
	reg := schema.Builtin()
	for _, path := range overlays {
		raw, err := os.ReadFile(path) //nolint:gosec // the operator lists the overlay files
		if err != nil {
			return nil, fmt.Errorf("schema overlay: %w", err)
		}
		ov, err := schema.Parse(raw, true)
		if err != nil {
			return nil, fmt.Errorf("schema overlay %s: %w", path, err)
		}
		if reg, err = reg.Merge(ov); err != nil {
			return nil, fmt.Errorf("schema overlay %s: %w", path, err)
		}
	}
	return reg, nil
}

// Run opens the store, migrates when configured, mounts the components,
// serves HTTP and runs the background loops until ctx ends, then shuts down
// gracefully. It returns the first fatal error.
func (s *Server) Run(ctx context.Context) error {
	reg, err := LoadSchema(s.cfg.SchemaOverlays)
	if err != nil {
		return err
	}
	url, err := s.cfg.DatabaseURL()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer st.Close()
	if *s.cfg.MigrateOnStart {
		if err := st.Migrate(ctx); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		s.log.Info("migrations applied")
	}
	schemaFn := func() *schema.Registry { return reg }
	deps := Deps{
		Store:   st,
		Engine:  &reconcile.Engine{Store: st, Schema: schemaFn, Policy: s.cfg.Policy},
		Schema:  schemaFn,
		Log:     s.log,
		Metrics: s.metrics.reg,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !s.ready.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Pool.Ping(ctx); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.metrics.reg, promhttp.HandlerOpts{}))
	for _, c := range s.components {
		if c.Mount == nil {
			continue
		}
		if err := c.Mount(mux, deps); err != nil {
			return fmt.Errorf("mount %s: %w", c.Name, err)
		}
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.addr.Store(ln.Addr().String())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(s.components)+2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.sweepLoop(ctx, deps.Engine)
	}()
	for _, c := range s.components {
		if c.Run == nil {
			continue
		}
		wg.Add(1)
		go func(c Component) {
			defer wg.Done()
			if err := c.Run(ctx, deps); err != nil && ctx.Err() == nil {
				errs <- fmt.Errorf("%s: %w", c.Name, err)
			}
		}(c)
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("http: %w", err)
		}
	}()
	s.ready.Store(true)
	s.log.Info("trica server started", "addr", s.Addr(), "version", s.version, "components", len(s.components))

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errs:
		s.log.Error("component failed, shutting down", "error", runErr)
	}
	s.ready.Store(false)
	cancel()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), s.cfg.HTTP.ShutdownTimeout)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown: %w", err)
	}
	wg.Wait()
	s.log.Info("trica server stopped")
	return runErr
}

// sweepLoop marks unseen objects absent every SweepInterval (ADR 0007).
func (s *Server) sweepLoop(ctx context.Context, e *reconcile.Engine) {
	t := time.NewTicker(s.cfg.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ids, err := e.SweepAbsent(ctx)
		s.metrics.sweeps.Inc()
		if err != nil {
			if ctx.Err() == nil {
				s.metrics.sweepErrors.Inc()
				s.log.Error("absence sweep failed", "error", err)
			}
			continue
		}
		if len(ids) > 0 {
			s.metrics.absent.Add(float64(len(ids)))
			s.log.Info("objects marked absent", "count", len(ids))
		}
	}
}
