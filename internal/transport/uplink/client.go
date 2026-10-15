// Package uplink is the client side of the transport (docs/adr/0008-transport.md):
// it enrols with a one-time token, renews its certificate before expiry, and
// sends batches over HTTP/2 and mutual TLS, keeping them in a bounded disk
// spool while the server is unreachable. The agent and the remote collector
// link it; it does not link the server nor its PostgreSQL registry
// (internal/transport), and TestAgentDoesNotLinkTheServer guards that (#64).
package uplink

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/transport/wire"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Defaults for [ClientConfig].
const (
	DefaultMaxSpoolBytes = 64 << 20
	DefaultTimeout       = 30 * time.Second
	// IdentityFile, in [ClientConfig.Dir], holds the key and the certificate.
	IdentityFile = "identity.pem"
)

// StatusError is a non-2xx answer from the server.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("transport: server answered %d: %s", e.Code, e.Message)
}

// permanentError marks a failure that retrying cannot fix (a missing token, an
// invalid batch): it is returned to the caller and never spooled.
type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// transient reports whether err is worth retrying later: a network or TLS
// failure, a 5xx, a 408 or a 429. Any other 4xx says the request itself is at
// fault and would fail again.
func transient(err error) bool {
	var perm permanentError
	if errors.As(err, &perm) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code >= 500 || se.Code == http.StatusRequestTimeout || se.Code == http.StatusTooManyRequests
	}
	return true
}

// ClientConfig configures a [Client].
type ClientConfig struct {
	// ServerURL is the base URL of the server, https://host:port.
	ServerURL string
	// ServerName overrides the name checked in the server certificate
	// (default: the host of ServerURL).
	ServerName string
	// CAFile is the PEM file of the CA certificate that signs the server
	// certificate, given to the machine together with its enrolment token. It
	// is required: a client that trusted whatever the server presented on first
	// contact would make enrolment pointless.
	CAFile string
	// Dir holds the client identity (key and certificate, one 0600 file).
	Dir string
	// SpoolDir holds the pending batches; Dir/spool when empty.
	SpoolDir string
	// EnrolToken or EnrolTokenFile (a path, preferred: a reference rather than
	// a value in a configuration) provide the one-time token, used only while
	// there is no certificate.
	EnrolToken     string
	EnrolTokenFile string
	// MaxSpoolBytes bounds the spool; DefaultMaxSpoolBytes when zero.
	MaxSpoolBytes int64
	// Timeout bounds each request; DefaultTimeout when zero.
	Timeout time.Duration
	// Now is the clock; time.Now when nil. It decides when to renew and orders
	// the spool, nothing else.
	Now    func() time.Time
	Logger *slog.Logger
}

// Stats are the client counters since it was created, plus the spool state.
type Stats struct {
	Sent       uint64 // batches the server accepted
	Spooled    int    // batches waiting in the spool
	SpoolBytes int64
	// Dropped counts batches evicted from a full spool, oldest first, or too
	// large for it. They are lost: this is the number to alert on.
	Dropped uint64
	// Rejected counts spooled batches the server refused with a 4xx on resend;
	// they are discarded, resending them would never succeed.
	Rejected   uint64
	Enrolments uint64
	Renewals   uint64
}

// Client sends batches to a server over mutual TLS and spools them while the
// server is unreachable. It implements [sdk.Emitter].
type Client struct {
	cfg    ClientConfig
	log    *slog.Logger
	now    func() time.Time
	base   string
	pool   *x509.CertPool
	main   *http.Client // presents the client certificate
	enrol  *http.Client // presents none: used before there is one
	mu     sync.Mutex   // serialises sending, flushing, enrolment and renewal
	sp     *spool
	stats  Stats
	certMu sync.RWMutex
	cert   *x509.Certificate
	tlsc   *tls.Certificate
}

var _ sdk.Emitter = (*Client)(nil)

// NewClient prepares a client: it reads the CA and the identity if present
// and opens the spool. It does not touch the network.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.ServerURL == "" || cfg.CAFile == "" || cfg.Dir == "" {
		return nil, errors.New("transport: ServerURL, CAFile and Dir are required")
	}
	if cfg.MaxSpoolBytes <= 0 {
		cfg.MaxSpoolBytes = DefaultMaxSpoolBytes
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.SpoolDir == "" {
		cfg.SpoolDir = filepath.Join(cfg.Dir, "spool")
	}
	c := &Client{cfg: cfg, base: strings.TrimRight(cfg.ServerURL, "/"), now: cfg.Now, log: cfg.Logger}
	if c.now == nil {
		c.now = time.Now
	}
	if c.log == nil {
		c.log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("transport: read CA file: %w", err)
	}
	c.pool = x509.NewCertPool()
	if !c.pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("transport: the CA file holds no certificate")
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("transport: client directory: %w", err)
	}
	if err := c.loadIdentity(); err != nil {
		return nil, err
	}
	c.main = c.httpClient(func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		c.certMu.RLock()
		defer c.certMu.RUnlock()
		if c.tlsc == nil {
			return &tls.Certificate{}, nil
		}
		return c.tlsc, nil
	})
	c.enrol = c.httpClient(nil)
	if c.sp, err = newSpool(cfg.SpoolDir, cfg.MaxSpoolBytes, c.now); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) httpClient(getCert func(*tls.CertificateRequestInfo) (*tls.Certificate, error)) *http.Client {
	return &http.Client{
		Timeout: c.cfg.Timeout,
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				MinVersion:           tls.VersionTLS13,
				Time:                 c.now,
				RootCAs:              c.pool,
				ServerName:           c.cfg.ServerName,
				GetClientCertificate: getCert,
			},
		},
	}
}

// Close releases idle connections. Pending batches stay in the spool.
func (c *Client) Close() {
	c.main.CloseIdleConnections()
	c.enrol.CloseIdleConnections()
}

// Stats returns a snapshot of the counters and the spool state.
func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.stats
	st.Spooled, st.SpoolBytes = c.sp.stats()
	st.Dropped = c.sp.dropped
	return st
}

// Emit sends b. Delivery is best effort but loss-averse:
//   - a batch that fails local validation, or that the server refuses with a
//     4xx (other than 408 and 429), is returned as an error and not spooled:
//     it would never succeed;
//   - a network failure, a 5xx, a 408 or a 429 puts the batch in the spool and
//     Emit returns nil: the batch is accepted for delivery, [Client.Flush] or
//     [Client.Run] resends it. Order is kept: nothing is sent ahead of what is
//     already spooled.
func (c *Client) Emit(ctx context.Context, b *sdk.Batch) error {
	if err := b.Validate(); err != nil {
		return permanentError{err}
	}
	body, err := encodeBatch(b)
	if err != nil {
		return permanentError{err}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	err = c.prepareLocked(ctx)
	if err == nil {
		err = c.flushLocked(ctx)
	}
	if err == nil {
		if err = c.postBatch(ctx, body); err == nil {
			c.stats.Sent++
			return nil
		}
	}
	if !transient(err) {
		return err
	}
	c.log.Warn("server unreachable or failing, batch spooled", "source", b.Source, "target", c.base, "error", err)
	return c.sp.add(body)
}

// Flush resends the spooled batches, oldest first, stopping at the first
// transient failure (which it returns). A spooled batch the server refuses
// with a 4xx is discarded and counted as rejected; a 401 stops the flush and
// keeps the spool, since it says something about the credentials, not the
// batch.
func (c *Client) Flush(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.prepareLocked(ctx); err != nil {
		return err
	}
	return c.flushLocked(ctx)
}

// Run flushes the spool and renews the certificate every interval until ctx
// is cancelled. Errors are logged: the next tick tries again.
func (c *Client) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := c.Flush(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn("spool flush failed", "target", c.base, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Client) flushLocked(ctx context.Context) error {
	list, err := c.sp.list()
	if err != nil {
		return err
	}
	for _, e := range list {
		body, err := c.sp.read(e.name)
		if err != nil {
			c.log.Error("unreadable spool file discarded", "target", e.name, "error", err)
			c.stats.Rejected++
			_ = c.sp.remove(e.name)
			continue
		}
		err = c.postBatch(ctx, body)
		var se *StatusError
		switch {
		case err == nil:
			c.stats.Sent++
		case errors.As(err, &se) && se.Code == http.StatusUnauthorized:
			return err
		case !transient(err):
			c.log.Warn("spooled batch refused by the server, discarded", "target", e.name, "error", err)
			c.stats.Rejected++
		default:
			return err
		}
		if err := c.sp.remove(e.name); err != nil {
			return fmt.Errorf("transport: remove sent batch: %w", err)
		}
	}
	return nil
}

func encodeBatch(b *sdk.Batch) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(b); err != nil {
		return nil, fmt.Errorf("transport: encode batch: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("transport: compress batch: %w", err)
	}
	return buf.Bytes(), nil
}

func (c *Client) postBatch(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+wire.PathBatches, bytes.NewReader(body))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	return do(c.main, req, nil)
}

// do sends req and decodes a 2xx JSON answer into out (when not nil); any
// other status becomes a StatusError.
func do(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		_ = json.Unmarshal(b, &e)
		return &StatusError{Code: resp.StatusCode, Message: e.Error}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, hc *http.Client, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return permanentError{err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, req, out)
}

// prepareLocked makes sure there is a usable certificate: it enrols when there
// is none, and renews when less than a third of the validity remains. A failed
// renewal is not an error while the current certificate is still valid.
func (c *Client) prepareLocked(ctx context.Context) error {
	cert := c.Certificate()
	if cert == nil {
		return c.enrolLocked(ctx)
	}
	if !c.needsRenewal(cert) {
		return nil
	}
	err := c.renewLocked(ctx)
	if err == nil {
		return nil
	}
	if c.now().Before(cert.NotAfter) {
		c.log.Warn("certificate renewal failed, keeping the current one", "target", c.base, "error", err)
		return nil
	}
	return fmt.Errorf("certificate expired and renewal failed: %w", err)
}

// needsRenewal: less than a third of the validity period is left.
func (c *Client) needsRenewal(cert *x509.Certificate) bool {
	total := cert.NotAfter.Sub(cert.NotBefore)
	return cert.NotAfter.Sub(c.now()) < total/3
}

// Certificate is the client certificate in use, nil before enrolment.
func (c *Client) Certificate() *x509.Certificate {
	c.certMu.RLock()
	defer c.certMu.RUnlock()
	return c.cert
}

func (c *Client) enrolLocked(ctx context.Context) error {
	token := strings.TrimSpace(c.cfg.EnrolToken)
	if token == "" && c.cfg.EnrolTokenFile != "" {
		b, err := os.ReadFile(c.cfg.EnrolTokenFile)
		if err != nil {
			return permanentError{fmt.Errorf("transport: read enrolment token file: %w", err)}
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return permanentError{errors.New("transport: no certificate and no enrolment token")}
	}
	key, err := pki.GenerateKey()
	if err != nil {
		return permanentError{err}
	}
	csr, err := pki.NewCSR(key)
	if err != nil {
		return permanentError{err}
	}
	var resp wire.CertResponse
	if err := c.postJSON(ctx, c.enrol, wire.PathEnrol, wire.EnrolRequest{Token: token, CSR: string(csr)}, &resp); err != nil {
		return err
	}
	if err := c.install(key, resp.Certificate); err != nil {
		return err
	}
	c.stats.Enrolments++
	c.log.Info("enrolled", "source", c.binding(), "target", c.base)
	return nil
}

func (c *Client) renewLocked(ctx context.Context) error {
	key, err := pki.GenerateKey()
	if err != nil {
		return permanentError{err}
	}
	csr, err := pki.NewCSR(key)
	if err != nil {
		return permanentError{err}
	}
	var resp wire.CertResponse
	if err := c.postJSON(ctx, c.main, wire.PathRenew, wire.RenewRequest{CSR: string(csr)}, &resp); err != nil {
		return err
	}
	if err := c.install(key, resp.Certificate); err != nil {
		return err
	}
	c.stats.Renewals++
	c.log.Info("certificate renewed", "source", c.binding(), "target", c.base)
	return nil
}

func (c *Client) binding() string {
	if cert := c.Certificate(); cert != nil {
		b, _ := pki.BindingFromCert(cert)
		return b
	}
	return ""
}

// install checks the certificate the server returned (it certifies our key,
// chains to our CA, carries a binding, and is valid now), persists key and
// certificate in one atomic file, then uses them.
func (c *Client) install(key *ecdsa.PrivateKey, certPEM string) error {
	cert, err := pki.ParseCertPEM([]byte(certPEM))
	if err != nil {
		return permanentError{err}
	}
	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return permanentError{errors.New("transport: the server returned a certificate for another key")}
	}
	if _, err := pki.BindingFromCert(cert); err != nil {
		return permanentError{err}
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:       c.pool,
		CurrentTime: c.now(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return permanentError{fmt.Errorf("transport: the server returned a certificate we cannot verify: %w", err)}
	}
	keyPEM, err := pki.MarshalKeyPEM(key)
	if err != nil {
		return permanentError{err}
	}
	file := slices.Concat(keyPEM, pki.CertPEM(cert))
	if err := atomicWrite(filepath.Join(c.cfg.Dir, IdentityFile), file, 0o600); err != nil {
		return permanentError{err}
	}
	c.setIdentity(key, cert)
	return nil
}

func (c *Client) setIdentity(key *ecdsa.PrivateKey, cert *x509.Certificate) {
	c.certMu.Lock()
	defer c.certMu.Unlock()
	c.cert = cert
	c.tlsc = &tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}
}

// loadIdentity reads the identity file when it exists.
func (c *Client) loadIdentity() error {
	data, err := os.ReadFile(filepath.Join(c.cfg.Dir, IdentityFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("transport: read identity: %w", err)
	}
	var keyPEM, certPEM []byte
	for rest := data; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type == "CERTIFICATE" {
			certPEM = pem.EncodeToMemory(blk)
		} else {
			keyPEM = pem.EncodeToMemory(blk)
		}
	}
	key, err := pki.ParseKeyPEM(keyPEM)
	if err != nil {
		return fmt.Errorf("transport: identity file: %w", err)
	}
	cert, err := pki.ParseCertPEM(certPEM)
	if err != nil {
		return fmt.Errorf("transport: identity file: %w", err)
	}
	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return errors.New("transport: identity file: key and certificate do not match")
	}
	c.setIdentity(key, cert)
	return nil
}
