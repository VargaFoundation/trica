// Package transport is the server side of the transport that carries batches
// from agents and remote collectors (docs/adr/0008-transport.md): HTTP/2 over
// mutual TLS, an internal CA with one-time enrolment tokens and a revocation
// list in PostgreSQL. The client, with its disk spool, is
// internal/transport/uplink; the contract between the two is
// internal/transport/wire.
//
// The server side hands every accepted batch to a [Sink]; in production that
// is a function calling reconcile.Engine.Submit, so this package never writes
// the inventory (docs/adr/0005-single-writer.md).
package transport

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/transport/wire"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Defaults for [ServerConfig].
const (
	DefaultMaxBodyBytes  = 8 << 20  // compressed request body
	DefaultMaxBatchBytes = 64 << 20 // body once decompressed
	maxSmallBodyBytes    = 64 << 10 // enrol and renew requests
)

// ErrRejected is what a [Sink] returns for a batch it will never accept
// (the engine refused it on a schema rule, for instance). The server answers
// 422, so the client does not retry; any other sink error is a 500 and is
// retried from the client's spool.
var ErrRejected = errors.New("transport: batch rejected")

// Identity is who an authenticated request comes from.
type Identity struct {
	// Binding is the source prefix the client certificate grants.
	Binding string
	// Serial is the certificate serial (lowercase hex).
	Serial string
}

// Sink receives the batches that passed authentication, the binding check and
// validation.
type Sink interface {
	Ingest(ctx context.Context, b *sdk.Batch, id Identity) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(ctx context.Context, b *sdk.Batch, id Identity) error

// Ingest calls f.
func (f SinkFunc) Ingest(ctx context.Context, b *sdk.Batch, id Identity) error {
	return f(ctx, b, id)
}

// EmitterSink adapts an [sdk.Emitter] to a Sink.
func EmitterSink(e sdk.Emitter) Sink {
	return SinkFunc(func(ctx context.Context, b *sdk.Batch, _ Identity) error { return e.Emit(ctx, b) })
}

// ServerConfig configures a [Server].
type ServerConfig struct {
	CA       *pki.CA
	Registry *Registry
	Sink     Sink
	// ServerCert is the certificate presented to clients, for enrolment and
	// for mutual TLS. [pki.CA.IssueServer] makes one; [Server.SetServerCert]
	// replaces it while the server runs.
	ServerCert tls.Certificate
	Logger     *slog.Logger
	// CertTTL is the validity of the certificates issued; pki.DefaultClientTTL
	// (7 days) when zero.
	CertTTL time.Duration
	// MaxBodyBytes bounds a request body as received (compressed);
	// DefaultMaxBodyBytes when zero.
	MaxBodyBytes int64
	// MaxBatchBytes bounds a batch once decompressed, so that a small gzip
	// bomb cannot exhaust memory; DefaultMaxBatchBytes when zero.
	MaxBatchBytes int64
}

// Server serves the transport endpoints.
type Server struct {
	cfg  ServerConfig
	log  *slog.Logger
	cert atomic.Pointer[tls.Certificate]
}

// NewServer checks cfg and returns a Server.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.CA == nil || cfg.Registry == nil || cfg.Sink == nil {
		return nil, errors.New("transport: CA, Registry and Sink are required")
	}
	if len(cfg.ServerCert.Certificate) == 0 {
		return nil, errors.New("transport: ServerCert is required")
	}
	if cfg.CertTTL <= 0 {
		cfg.CertTTL = pki.DefaultClientTTL
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if cfg.MaxBatchBytes <= 0 {
		cfg.MaxBatchBytes = DefaultMaxBatchBytes
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	s := &Server{cfg: cfg, log: log}
	s.cert.Store(&cfg.ServerCert)
	return s, nil
}

// SetServerCert replaces the certificate presented from the next handshake
// on. The listener and the connections already open are left alone, so a
// renewal leaves no moment when agents cannot connect (#57).
func (s *Server) SetServerCert(c tls.Certificate) error {
	if len(c.Certificate) == 0 {
		return errors.New("transport: empty server certificate")
	}
	s.cert.Store(&c)
	return nil
}

// TLSConfig is the server TLS configuration: TLS 1.3, HTTP/2, and a client
// certificate that is optional at the handshake (enrolment has none yet) but
// verified against the CA whenever one is presented. The endpoints that need
// an identity refuse a request without a verified certificate.
func (s *Server) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Read at each handshake, so that SetServerCert takes effect at once.
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return s.cert.Load(), nil },
		ClientAuth:     tls.VerifyClientCertIfGiven,
		Time:           s.cfg.Registry.now, // one clock for the handshake and the per-request check
		ClientCAs:      s.cfg.CA.Pool(),
		NextProtos:     []string{"h2", "http/1.1"},
	}
}

// Handler returns the HTTP handler. It must be served with [Server.TLSConfig]:
// the identity of a client is the certificate verified by the TLS layer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+wire.PathEnrol, s.handleEnrol)
	mux.HandleFunc("POST "+wire.PathRenew, s.handleRenew)
	mux.HandleFunc("POST "+wire.PathBatches, s.handleBatches)
	return mux
}

// Serve serves on ln over TLS until ctx is cancelled, then shuts down
// gracefully.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		TLSConfig:         s.TLSConfig(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       5 * time.Minute,
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		case <-done:
		}
	}()
	err := srv.ServeTLS(ln, "", "")
	close(done)
	// ServeTLS returns as soon as Shutdown starts; requests in flight are
	// still being served. Returning before Shutdown has drained them would
	// let a caller believe the server is down while it still accepts batches.
	<-stopped
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleEnrol(w http.ResponseWriter, r *http.Request) {
	var req wire.EnrolRequest
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "missing token")
		return
	}
	csr, err := pki.ParseCSRPEM([]byte(req.CSR))
	if err == nil {
		_, err = pki.ValidateCSR(csr)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reg := s.cfg.Registry
	cert, err := reg.enrol(r.Context(), req.Token, func(binding string) (*x509.Certificate, error) {
		return s.cfg.CA.IssueClient(csr, binding, s.cfg.CertTTL, reg.now())
	})
	switch {
	case errors.Is(err, ErrInvalidToken):
		s.log.Warn("enrolment refused", "target", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "invalid enrolment token")
		return
	case err != nil:
		s.log.Error("enrolment failed", "target", r.RemoteAddr, "error", err)
		writeError(w, http.StatusInternalServerError, "enrolment failed")
		return
	}
	binding, _ := pki.BindingFromCert(cert)
	s.log.Info("client enrolled", "source", binding, "serial", pki.SerialString(cert))
	writeJSON(w, http.StatusOK, wire.CertResponse{Certificate: string(pki.CertPEM(cert)), CA: string(s.cfg.CA.CertPEM())})
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req wire.RenewRequest
	if !decodeSmall(w, r, &req) {
		return
	}
	csr, err := pki.ParseCSRPEM([]byte(req.CSR))
	if err == nil {
		_, err = pki.ValidateCSR(csr)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reg := s.cfg.Registry
	cert, err := reg.renew(r.Context(), id.Binding, func() (*x509.Certificate, error) {
		return s.cfg.CA.IssueClient(csr, id.Binding, s.cfg.CertTTL, reg.now())
	})
	if err != nil {
		s.log.Error("renewal failed", "source", id.Binding, "error", err)
		writeError(w, http.StatusInternalServerError, "renewal failed")
		return
	}
	s.log.Info("certificate renewed", "source", id.Binding, "serial", pki.SerialString(cert))
	writeJSON(w, http.StatusOK, wire.CertResponse{Certificate: string(pki.CertPEM(cert)), CA: string(s.cfg.CA.CertPEM())})
}

func (s *Server) handleBatches(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if r.Header.Get("Content-Encoding") != "gzip" {
		writeError(w, http.StatusUnsupportedMediaType, "the body must be gzip compressed JSON (Content-Encoding: gzip)")
		return
	}
	b, status, msg := s.decodeBatch(w, r)
	if b == nil {
		writeError(w, status, msg)
		return
	}
	if !pki.Allows(id.Binding, b.Source) {
		s.log.Warn("source refused", "source", id.Binding, "claimed_source", b.Source)
		writeError(w, http.StatusForbidden, fmt.Sprintf("source %q is not allowed for this certificate", b.Source))
		return
	}
	if err := b.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch err := s.cfg.Sink.Ingest(r.Context(), b, id); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrRejected):
		s.log.Warn("batch rejected", "source", b.Source, "error", err)
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.log.Error("batch not ingested", "source", b.Source, "error", err)
		writeError(w, http.StatusInternalServerError, "ingestion failed")
	}
}

// decodeBatch reads the gzip JSON body under both size limits.
func (s *Server) decodeBatch(w http.ResponseWriter, r *http.Request) (*sdk.Batch, int, string) {
	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	gz, err := gzip.NewReader(body)
	if err != nil {
		return nil, bodyErrStatus(err), "invalid gzip body"
	}
	defer gz.Close()
	lim := &io.LimitedReader{R: gz, N: s.cfg.MaxBatchBytes + 1}
	dec := json.NewDecoder(lim)
	var b sdk.Batch
	err = dec.Decode(&b)
	if err == nil {
		// Nothing but whitespace may follow the batch.
		if _, e := dec.Token(); !errors.Is(e, io.EOF) {
			err = errors.New("trailing data after the batch")
		}
	}
	if lim.N <= 0 {
		return nil, http.StatusRequestEntityTooLarge, "batch too large once decompressed"
	}
	if err != nil {
		return nil, bodyErrStatus(err), "invalid batch: " + err.Error()
	}
	return &b, 0, ""
}

func bodyErrStatus(err error) int {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func decodeSmall(w http.ResponseWriter, r *http.Request, v any) bool {
	body := http.MaxBytesReader(w, r.Body, maxSmallBodyBytes)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		writeError(w, bodyErrStatus(err), "invalid request body")
		return false
	}
	return true
}

// authenticate returns the identity of the verified client certificate, or
// answers 401 (and, when the revocation list cannot be read, 503: the check
// fails closed).
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		writeError(w, http.StatusUnauthorized, "client certificate required")
		return Identity{}, false
	}
	leaf := r.TLS.PeerCertificates[0]
	reg := s.cfg.Registry
	// TLS checked the validity period at the handshake; a connection can
	// outlive the certificate, so it is checked on every request too.
	if now := reg.now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		writeError(w, http.StatusUnauthorized, "client certificate expired")
		return Identity{}, false
	}
	binding, err := pki.BindingFromCert(leaf)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "client certificate carries no valid source binding")
		return Identity{}, false
	}
	serial := pki.SerialString(leaf)
	revoked, err := reg.IsRevoked(r.Context(), serial)
	if err != nil {
		s.log.Error("revocation check failed", "source", binding, "error", err)
		writeError(w, http.StatusServiceUnavailable, "cannot check revocation")
		return Identity{}, false
	}
	if revoked {
		s.log.Warn("revoked certificate refused", "source", binding, "serial", serial)
		writeError(w, http.StatusUnauthorized, "client certificate revoked")
		return Identity{}, false
	}
	return Identity{Binding: binding, Serial: serial}, true
}
