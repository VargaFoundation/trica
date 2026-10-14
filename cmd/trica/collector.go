package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	_ "github.com/VargaFoundation/trica/internal/collect/all" // every collector registers itself in an init function
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/transport"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// collectorConfig is the configuration of `trica collector`, a collector that
// runs where the server cannot reach: it collects from its own targets and
// sends the batches to the server over the mutual-TLS transport (ADR 0008,
// 0026). It holds references to secrets (file paths), never secrets.
type collectorConfig struct {
	// LogLevel is debug, info, warn or error; info when empty.
	LogLevel string `yaml:"log_level"`
	// ServerURL is the transport endpoint, https://host:port.
	ServerURL string `yaml:"server_url"`
	// ServerName overrides the name checked in the server certificate.
	ServerName string `yaml:"server_name"`
	// CAFile is the server's CA certificate (ca.pem), handed over out of band.
	CAFile string `yaml:"ca_file"`
	// StateDir keeps, per target, the client certificate and key (0600) and
	// the spool of batches the server could not take. A persistent directory:
	// losing it means enrolling again.
	StateDir string `yaml:"state_dir"`
	// TargetsFile lists the targets, in the format of the server's
	// collect.targets_file.
	TargetsFile string `yaml:"targets_file"`
	// EnrolTokenFiles maps a target's source ("arista:sw-a1") to a file holding
	// its one-time enrolment token. Needed only until the target has a
	// certificate. A certificate is bound to one source, so each target is
	// enrolled on its own.
	EnrolTokenFiles map[string]string `yaml:"enrol_token_files"`
	// FlushInterval is how often spooled batches are resent; 30s when zero.
	FlushInterval time.Duration `yaml:"flush_interval"`
	// MetricsAddr, when set, serves /metrics there (":9102").
	MetricsAddr string `yaml:"metrics_addr"`
}

func loadCollectorConfig(path string) (*collectorConfig, error) {
	if path == "" {
		return nil, errors.New("collector: --config is required")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the operator names the configuration file
	if err != nil {
		return nil, fmt.Errorf("collector config: %w", err)
	}
	c := &collectorConfig{}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("collector config %s: %w", path, err)
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 30 * time.Second
	}
	switch {
	case c.ServerURL == "", c.CAFile == "", c.StateDir == "", c.TargetsFile == "":
		return nil, errors.New("collector config: server_url, ca_file, state_dir and targets_file are required")
	case !strings.HasPrefix(c.ServerURL, "https://"):
		return nil, errors.New("collector config: server_url must be an https URL")
	}
	return c, nil
}

// runCollector runs `trica collector --config FILE` until ctx ends.
func runCollector(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("collector", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", getenv("TRICA_COLLECTOR_CONFIG"), "collector configuration file (YAML)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadCollectorConfig(*cfgPath)
	if err != nil {
		return err
	}
	log := newLogger(stderr, cfg.LogLevel)
	targets, err := runner.LoadTargets(cfg.TargetsFile)
	if err != nil {
		return err
	}
	reg := prometheus.NewRegistry()
	out, err := newRemoteEmitter(ctx, cfg, targets, log, reg)
	if err != nil {
		return err
	}
	defer out.close()

	r, err := runner.New(runner.Config{Targets: targets, Emitter: out, Log: log, Registerer: reg})
	if err != nil {
		return err
	}
	if cfg.MetricsAddr != "" {
		stop, err := serveMetrics(ctx, cfg.MetricsAddr, reg, log)
		if err != nil {
			return err
		}
		defer stop()
	}
	log.Info("collector started", "targets", len(targets), "server", cfg.ServerURL)
	return r.Run(ctx)
}

// remoteEmitter sends each batch through the transport client of its source:
// a client certificate is bound to one source, so every target has its own
// client, state directory and spool.
type remoteEmitter struct {
	clients map[string]*transport.Client
}

func newRemoteEmitter(ctx context.Context, cfg *collectorConfig, targets []collect.Target, log *slog.Logger, reg prometheus.Registerer) (*remoteEmitter, error) {
	e := &remoteEmitter{clients: map[string]*transport.Client{}}
	for _, t := range targets {
		src := t.Source()
		if err := pki.ValidBinding(src); err != nil {
			return nil, fmt.Errorf("target %s cannot be enrolled: %w", src, err)
		}
		cl, err := transport.NewClient(transport.ClientConfig{
			ServerURL:      cfg.ServerURL,
			ServerName:     cfg.ServerName,
			CAFile:         cfg.CAFile,
			Dir:            filepath.Join(cfg.StateDir, strings.ReplaceAll(src, ":", "_")),
			EnrolTokenFile: cfg.EnrolTokenFiles[src],
			Logger:         log,
		})
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", src, err)
		}
		e.clients[src] = cl
		go cl.Run(ctx, cfg.FlushInterval)
		registerClientMetrics(reg, src, cl)
	}
	return e, nil
}

// Emit implements sdk.Emitter.
func (e *remoteEmitter) Emit(ctx context.Context, b *sdk.Batch) error {
	cl, ok := e.clients[b.Source]
	if !ok {
		return fmt.Errorf("no transport client for source %q", b.Source)
	}
	return cl.Emit(ctx, b)
}

func (e *remoteEmitter) close() {
	for _, c := range e.clients {
		c.Close()
	}
}

// registerClientMetrics exposes what an operator alerts on: batches waiting in
// the spool, and batches lost to a full one.
func registerClientMetrics(reg prometheus.Registerer, source string, c *transport.Client) {
	l := prometheus.Labels{"source": source}
	reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "trica_collector_spooled_batches", Help: "Batches waiting in the spool for the server.", ConstLabels: l,
		}, func() float64 { return float64(c.Stats().Spooled) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "trica_collector_dropped_batches_total", Help: "Batches lost: evicted from a full spool or too large for it.", ConstLabels: l,
		}, func() float64 { return float64(c.Stats().Dropped) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "trica_collector_sent_batches_total", Help: "Batches the server accepted.", ConstLabels: l,
		}, func() float64 { return float64(c.Stats().Sent) }),
	)
}

// serveMetrics serves /metrics on addr until ctx ends; the returned function
// waits for the listener to close.
func serveMetrics(ctx context.Context, addr string, reg *prometheus.Registry, log *slog.Logger) (func(), error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("collector metrics: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("collector metrics", "error", err.Error())
		}
	}()
	log.Info("collector metrics listening", "addr", ln.Addr().String())
	return func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		<-done
	}, nil
}
