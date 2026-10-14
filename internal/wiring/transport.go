package wiring

import (
	"context"
	"errors"
	"fmt"
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
// when the listener is closed and reopened on the same address (agents spool
// and retry, ADR 0008). Failing to start the transport stops the server: an
// operator who configured it expects agents to get through.
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

	addr := cfg.Addr
	for {
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
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("transport listen: %w", err)
		}
		addr = ln.Addr().String() // a port chosen by the system stays the same on renewal
		w.transportAddr.Store(addr)
		d.Log.Info("transport listening", "addr", addr, "server_cert_expires", cert.Leaf.NotAfter.UTC().Format(time.RFC3339))

		rctx, cancel := context.WithTimeout(ctx, cfg.ServerCertTTL*2/3)
		err = srv.Serve(rctx, ln)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("transport: %w", err)
		}
		d.Log.Info("transport: renewing the server certificate")
	}
}
