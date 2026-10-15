package transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/transport/uplink"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// fakeClock is an injectable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Now().UTC()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// syncBuffer collects log output; tests check that no secret reaches it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// recorder is the Sink of the tests: it remembers what reached the engine's
// door, in order.
type recorder struct {
	mu   sync.Mutex
	got  []string
	ids  []Identity
	fail error
}

func (r *recorder) Ingest(_ context.Context, b *sdk.Batch, id Identity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.got = append(r.got, b.Source)
	r.ids = append(r.ids, id)
	return nil
}

func (r *recorder) sources() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func (r *recorder) setFail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

// env is a CA, a PostgreSQL registry, a server and the means to start and stop
// it on a fixed address.
type env struct {
	t       *testing.T
	ca      *pki.CA
	pool    *pgxpool.Pool
	reg     *Registry
	srv     *Server
	rec     *recorder
	logs    *syncBuffer
	srvCert tls.Certificate
	regTime *fakeClock // the server side clock
	addr    string
	caFile  string
	// hold keeps addr bound while the server is stopped. Without it, a test
	// process of another package (go test ./... runs them side by side) may
	// listen on the freed port during the outage, and the client then talks
	// to that server instead of finding nobody (#67).
	hold net.Listener
}

func newEnv(t *testing.T, mut ...func(*ServerConfig)) *env {
	t.Helper()
	pool := testPool(t)
	ca, err := pki.NewCA("trica test CA", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ca: ca, pool: pool, rec: &recorder{}, logs: &syncBuffer{}, regTime: newClock()}
	e.reg = NewRegistry(pool)
	e.reg.Now = e.regTime.Now
	e.srvCert, err = ca.IssueServer([]string{"127.0.0.1"}, 90*24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{
		CA: ca, Registry: e.reg, Sink: e.rec, ServerCert: e.srvCert,
		Logger: slog.New(slog.NewJSONHandler(e.logs, nil)),
	}
	for _, m := range mut {
		m(&cfg)
	}
	e.srv, err = NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.release)
	e.caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(e.caFile, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

// start serves on the env's address (a free port the first time, the same one
// after a restart) and returns the function that stops the server.
func (e *env) start() func() {
	e.t.Helper()
	e.release()
	listen := "127.0.0.1:0"
	if e.addr != "" {
		listen = e.addr
	}
	var ln net.Listener
	var err error
	for range 50 { // the port of a server stopped a moment ago can take a few ms to free
		if ln, err = net.Listen("tcp", listen); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if e.addr == "" { // a restart keeps it: tests read url() from other goroutines
		e.addr = ln.Addr().String()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.srv.Serve(ctx, ln) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				e.t.Errorf("serve: %v", err)
			}
			e.holdPort()
		})
	}
	e.t.Cleanup(stop)
	return stop
}

// holdPort binds addr again once the server is down, with a listener that
// closes every connection: what a client meets is an outage, on a port no one
// else can take.
func (e *env) holdPort() {
	ln, err := net.Listen("tcp", e.addr)
	if err != nil {
		e.t.Errorf("hold %s during the outage: %v", e.addr, err)
		return
	}
	e.hold = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
}

// release frees the port held by holdPort, if any.
func (e *env) release() {
	if e.hold != nil {
		_ = e.hold.Close()
		e.hold = nil
	}
}

func (e *env) url() string { return "https://" + e.addr }

// clientCfg builds a client configuration with a fresh enrolment token for
// binding.
func (e *env) clientCfg(binding string) uplink.ClientConfig {
	e.t.Helper()
	token, err := e.reg.NewEnrolmentToken(context.Background(), binding, time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	return uplink.ClientConfig{
		ServerURL:  e.url(),
		CAFile:     e.caFile,
		Dir:        e.t.TempDir(),
		EnrolToken: token,
		Logger:     slog.New(slog.NewJSONHandler(e.logs, nil)),
		Timeout:    5 * time.Second,
	}
}

func (e *env) client(binding string, mut ...func(*uplink.ClientConfig)) *uplink.Client {
	e.t.Helper()
	cfg := e.clientCfg(binding)
	for _, m := range mut {
		m(&cfg)
	}
	return e.clientFrom(cfg)
}

// clientFrom builds a client from a configuration the test keeps, to restart
// it later on the same directories.
func (e *env) clientFrom(cfg uplink.ClientConfig) *uplink.Client {
	e.t.Helper()
	c, err := uplink.NewClient(cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(c.Close)
	return c
}

// rawClient is an HTTP client for hand-made requests, presenting cert (or
// none) and trusting the env CA.
func (e *env) rawClient(cert *tls.Certificate) *http.Client {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: e.ca.Pool()}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	tr := &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}
	e.t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

// identityOf loads the identity a client keeps in dir (key and certificate in
// one file), for hand-made requests in that client's name.
func identityOf(t *testing.T, dir string) *tls.Certificate {
	t.Helper()
	path := filepath.Join(dir, uplink.IdentityFile)
	c, err := tls.LoadX509KeyPair(path, path)
	if err != nil {
		t.Fatal(err)
	}
	return &c
}

// identity issues a client certificate straight from the CA, with its key.
func (e *env) identity(binding string, ttl time.Duration, now time.Time) tls.Certificate {
	e.t.Helper()
	return issueIdentity(e.t, e.ca, binding, ttl, now)
}

func issueIdentity(t *testing.T, ca *pki.CA, binding string, ttl time.Duration, now time.Time) tls.Certificate {
	t.Helper()
	key, err := pki.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, err := pki.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := pki.ParseCSRPEM(csrPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueClient(csr, binding, ttl, now)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}
}

// batch is a minimal valid batch of the given source. Sources of equal length
// give batches of equal encoded size.
func batch(source string) *sdk.Batch {
	b := sdk.NewBatch(source, sdk.ScopePartial)
	b.Object("h", "device").Identify("name", "host-01")
	return b
}

// gz compresses the JSON of v.
func gz(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return gzBytes(t, raw)
}

func gzBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// post sends a hand-made request and returns the status and the body.
func post(t *testing.T, hc *http.Client, url string, body []byte, hdr map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

var gzipHdr = map[string]string{"Content-Encoding": "gzip", "Content-Type": "application/json"}

func mustParseCert(t *testing.T, pemData string) *x509.Certificate {
	t.Helper()
	c, err := pki.ParseCertPEM([]byte(pemData))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func srcs(prefix string, from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("%s%d", prefix, i))
	}
	return out
}
