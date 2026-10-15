package wiring

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/transport"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// runTransport serves the mutual-TLS ingestion endpoint of agents and
// collectors on transport.addr, a listener of its own: the API's listener
// takes bearer tokens through ordinary TLS terminators, this one needs the
// client certificate to reach the process (ADR 0025).
//
// The CA is loaded from, or created in, transport.ca_dir; the server
// certificate is issued at start and again after two thirds of its lifetime,
// and swapped in for the next handshakes: the listener stays open and the
// connections in progress are kept (#57). Failing to start the transport
// stops the server: an operator who configured it expects agents to get
// through.
func (w *Wiring) runTransport(ctx context.Context, d server.Deps) error {
	cfg := w.cfg.Transport
	ca, err := pki.LoadOrCreateCA(cfg.CADir, "trica transport CA", time.Now())
	if err != nil {
		return fmt.Errorf("transport CA: %w", err)
	}
	reg := transport.NewRegistry(d.Store.Pool)
	sink := transport.SinkFunc(func(ctx context.Context, b *sdk.Batch, id transport.Identity) error {
		// The batch's source was checked against the certificate's binding by
		// the transport; the engine decides what it does to the inventory.
		_, err := d.Engine.Submit(ctx, b, id.Binding, "transport")
		if errors.Is(err, reconcile.ErrRejected) || errors.Is(err, reconcile.ErrInvalid) {
			return fmt.Errorf("%w: %w", transport.ErrRejected, err)
		}
		return err
	})

	cert, err := ca.IssueServer(cfg.Hosts, cfg.ServerCertTTL, time.Now())
	if err != nil {
		return fmt.Errorf("transport server certificate: %w", err)
	}
	srv, err := transport.NewServer(transport.ServerConfig{
		CA: ca, Registry: reg, Sink: sink, ServerCert: cert, Logger: d.Log, CertTTL: cfg.CertTTL,
	})
	if err != nil {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("transport listen: %w", err)
	}
	w.transportAddr.Store(ln.Addr().String())
	d.Log.Info("transport listening", "addr", ln.Addr().String(), "server_cert_expires", cert.Leaf.NotAfter.UTC().Format(time.RFC3339))

	sctx, stop := context.WithCancel(ctx)
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		renewServerCert(sctx, d.Log, cfg.ServerCertTTL, func() (tls.Certificate, error) {
			return ca.IssueServer(cfg.Hosts, cfg.ServerCertTTL, time.Now())
		}, srv.SetServerCert)
	}()
	err = srv.Serve(sctx, ln)
	stop()
	<-renewed
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("transport: %w", err)
	}
	return nil
}

// renewServerCert issues a new server certificate after two thirds of ttl,
// and again after each renewal, until ctx ends. A failed issue is logged and
// retried well before the current certificate expires.
func renewServerCert(ctx context.Context, log *slog.Logger, ttl time.Duration, issue func() (tls.Certificate, error), set func(tls.Certificate) error) {
	next := ttl * 2 / 3
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(next):
		}
		cert, err := issue()
		if err == nil {
			err = set(cert)
		}
		if err != nil {
			next = min(time.Minute, ttl/10)
			log.Error("transport: renewing the server certificate failed", "error", err, "retry_in", next)
			continue
		}
		next = ttl * 2 / 3
		log.Info("transport: server certificate renewed", "server_cert_expires", cert.Leaf.NotAfter.UTC().Format(time.RFC3339))
	}
}
