package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

func hexOf(b []byte) string { return hex.EncodeToString(b) }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want a StatusError, got %T: %v", err, err)
	}
	return se.Code
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEndToEndHTTP2(t *testing.T) {
	e := newEnv(t)
	e.start()
	c := e.client("agent:host-01")
	real := c.main.Transport
	var proto string
	c.main.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := real.RoundTrip(r)
		if resp != nil {
			proto = resp.Proto
		}
		return resp, err
	})

	if err := c.Emit(context.Background(), batch("agent:host-01")); err != nil {
		t.Fatal(err)
	}
	if got := e.rec.sources(); !slices.Equal(got, []string{"agent:host-01"}) {
		t.Fatalf("sink got %v", got)
	}
	if proto != "HTTP/2.0" {
		t.Fatalf("protocol = %q, want HTTP/2.0", proto)
	}
	e.rec.mu.Lock()
	id := e.rec.ids[0]
	e.rec.mu.Unlock()
	if id.Binding != "agent:host-01" || id.Serial == "" {
		t.Fatalf("identity = %+v", id)
	}
	if st := c.Stats(); st.Sent != 1 || st.Enrolments != 1 || st.Spooled != 0 {
		t.Fatalf("stats = %+v", st)
	}
	// The server recorded the certificate it issued.
	serials, err := e.reg.IssuedTo(context.Background(), "agent:host-01")
	if err != nil || len(serials) != 1 || serials[0] != id.Serial {
		t.Fatalf("issued = %v, %v (want %s)", serials, err, id.Serial)
	}
	// A second client built on the same directory needs no token: it reuses
	// the identity enrolled by the first.
	cfg := e.clientCfg("agent:other-01")
	token := c.cfg.EnrolToken
	cfg.Dir, cfg.EnrolToken = c.cfg.Dir, ""
	c2, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Emit(context.Background(), batch("agent:host-01:docker")); err != nil {
		t.Fatal(err)
	}
	if st := c2.Stats(); st.Enrolments != 0 || st.Sent != 1 {
		t.Fatalf("reused identity stats = %+v", st)
	}
	// No secret in the logs: not the token, not key material.
	logs := e.logs.String()
	if strings.Contains(logs, token) || strings.Contains(logs, "PRIVATE KEY") {
		t.Fatal("secret in logs")
	}
}

func TestEnrolmentTokenIsStoredHashed(t *testing.T) {
	e := newEnv(t)
	token, err := e.reg.NewEnrolmentToken(context.Background(), "agent:host-01", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var hash []byte
	var binding string
	if err := e.pool.QueryRow(context.Background(), `SELECT token_hash, binding FROM enrol_tokens`).Scan(&hash, &binding); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(token))
	if string(hash) != string(want[:]) || binding != "agent:host-01" {
		t.Fatalf("stored hash/binding = %x / %s", hash, binding)
	}
	if _, err := e.reg.NewEnrolmentToken(context.Background(), "agent", time.Hour); err == nil {
		t.Fatal("an invalid binding must not get a token")
	}
}

func TestTokenWorksOnce(t *testing.T) {
	e := newEnv(t)
	e.start()
	first := e.clientCfg("agent:host-01")
	c1, err := NewClient(first)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	if err := c1.Emit(context.Background(), batch("agent:host-01")); err != nil {
		t.Fatal(err)
	}
	// Another machine replays the same token.
	second := e.clientCfg("agent:host-99")
	second.EnrolToken = first.EnrolToken
	c2, err := NewClient(second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	err = c2.Emit(context.Background(), batch("agent:host-01"))
	if code := statusOf(t, err); code != http.StatusUnauthorized {
		t.Fatalf("reused token: status %d, want 401", code)
	}
	if st := c2.Stats(); st.Spooled != 0 || st.Enrolments != 0 {
		t.Fatalf("a refused enrolment must neither spool nor count: %+v", st)
	}
	if c2.currentCert() != nil {
		t.Fatal("no certificate must be issued for a used token")
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	e := newEnv(t)
	e.start()
	cfg := e.clientCfg("agent:host-01") // valid one hour
	e.regTime.Advance(time.Hour + time.Second)
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if code := statusOf(t, c.Emit(context.Background(), batch("agent:host-01"))); code != http.StatusUnauthorized {
		t.Fatalf("expired token: status %d, want 401", code)
	}
	if got := e.rec.sources(); len(got) != 0 {
		t.Fatalf("nothing may reach the sink: %v", got)
	}
}

func TestUnknownAndMissingToken(t *testing.T) {
	e := newEnv(t)
	e.start()
	hc := e.rawClient(nil)
	key, _ := pki.GenerateKey()
	csr, _ := pki.NewCSR(key)
	body, _ := json.Marshal(enrolRequest{Token: "not-a-token", CSR: string(csr)})
	if code, _ := post(t, hc, e.url()+PathEnrol, body, nil); code != http.StatusUnauthorized {
		t.Fatalf("unknown token: %d", code)
	}
	body, _ = json.Marshal(enrolRequest{CSR: string(csr)})
	if code, _ := post(t, hc, e.url()+PathEnrol, body, nil); code != http.StatusBadRequest {
		t.Fatalf("missing token: %d", code)
	}
	if code, _ := post(t, hc, e.url()+PathEnrol, []byte("{"), nil); code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: %d", code)
	}
}

func TestBadCSRDoesNotBurnTheToken(t *testing.T) {
	e := newEnv(t)
	e.start()
	token, err := e.reg.NewEnrolmentToken(context.Background(), "agent:host-01", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hc := e.rawClient(nil)
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, rk)
	rsaCSR := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	for name, csr := range map[string]string{"rsa": string(rsaCSR), "garbage": "nope"} {
		body, _ := json.Marshal(enrolRequest{Token: token, CSR: csr})
		if code, _ := post(t, hc, e.url()+PathEnrol, body, nil); code != http.StatusBadRequest {
			t.Fatalf("%s CSR: status %d, want 400", name, code)
		}
	}
	// The token still works with a good CSR.
	key, _ := pki.GenerateKey()
	csr, _ := pki.NewCSR(key)
	body, _ := json.Marshal(enrolRequest{Token: token, CSR: string(csr)})
	code, out := post(t, hc, e.url()+PathEnrol, body, nil)
	if code != http.StatusOK {
		t.Fatalf("good CSR after bad ones: %d %s", code, out)
	}
	var resp certResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	cert := mustParseCert(t, resp.Certificate)
	if b, _ := pki.BindingFromCert(cert); b != "agent:host-01" {
		t.Fatalf("binding = %q", b)
	}
	if !strings.Contains(resp.CA, "BEGIN CERTIFICATE") {
		t.Fatal("the CA chain is missing from the answer")
	}
	// The CSR's own claim to another identity is ignored: the token's binding wins.
	token2, _ := e.reg.NewEnrolmentToken(context.Background(), "agent:host-02", time.Hour)
	der, _ = x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "agent:host-01"},
		URIs:    []*url.URL{{Scheme: "urn", Opaque: "trica:source:agent:host-01"}},
	}, key)
	body, _ = json.Marshal(enrolRequest{Token: token2, CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))})
	code, out = post(t, hc, e.url()+PathEnrol, body, nil)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	_ = json.Unmarshal([]byte(out), &resp)
	if b, _ := pki.BindingFromCert(mustParseCert(t, resp.Certificate)); b != "agent:host-02" {
		t.Fatalf("binding = %q, want agent:host-02", b)
	}
}

func TestSourceSpoofingRejected(t *testing.T) {
	e := newEnv(t)
	e.start()
	c := e.client("agent:host-01")
	ctx := context.Background()
	for _, src := range []string{"agent:host-02", "agent:host-010", "agent", "collector:site-a", "agent:host-0"} {
		err := c.Emit(ctx, batch(src))
		if code := statusOf(t, err); code != http.StatusForbidden {
			t.Errorf("source %q: status %d, want 403", src, code)
		}
	}
	if got := e.rec.sources(); len(got) != 0 {
		t.Fatalf("spoofed batches reached the sink: %v", got)
	}
	if st := c.Stats(); st.Spooled != 0 {
		t.Fatalf("a 403 must not be spooled: %+v", st)
	}
	// What the binding does allow: itself and anything below it.
	for _, src := range []string{"agent:host-01", "agent:host-01:docker"} {
		if err := c.Emit(ctx, batch(src)); err != nil {
			t.Errorf("source %q: %v", src, err)
		}
	}
	if got := e.rec.sources(); !slices.Equal(got, []string{"agent:host-01", "agent:host-01:docker"}) {
		t.Fatalf("sink got %v", got)
	}
}

func TestRevokedCertificateRejected(t *testing.T) {
	e := newEnv(t)
	e.start()
	c := e.client("agent:host-01")
	other := e.client("agent:host-02")
	ctx := context.Background()
	if err := c.Emit(ctx, batch("agent:host-01")); err != nil {
		t.Fatal(err)
	}
	if err := other.Emit(ctx, batch("agent:host-02")); err != nil {
		t.Fatal(err)
	}
	serial := pki.SerialString(c.currentCert())
	if err := e.reg.Revoke(ctx, serial, "lost laptop"); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.Revoke(ctx, serial, "again"); err != nil {
		t.Fatalf("revoking twice must be harmless: %v", err)
	}
	err := c.Emit(ctx, batch("agent:host-01"))
	if code := statusOf(t, err); code != http.StatusUnauthorized {
		t.Fatalf("revoked: status %d, want 401", code)
	}
	if got := e.rec.sources(); !slices.Equal(got, []string{"agent:host-01", "agent:host-02"}) {
		t.Fatalf("sink got %v", got)
	}
	// Only that certificate is revoked.
	if err := other.Emit(ctx, batch("agent:host-02")); err != nil {
		t.Fatalf("an unrelated client must keep working: %v", err)
	}
	// A revoked certificate cannot renew itself either.
	if code, _ := post(t, e.rawClient(c.tlsc), e.url()+PathRenew, []byte(`{}`), nil); code != http.StatusUnauthorized {
		t.Fatalf("renew with a revoked certificate: %d", code)
	}
	// Revoking by binding.
	n, err := e.reg.RevokeBinding(ctx, "agent:host-02", "decommissioned")
	if err != nil || n != 1 {
		t.Fatalf("RevokeBinding = %d, %v", n, err)
	}
	if code := statusOf(t, other.Emit(ctx, batch("agent:host-02"))); code != http.StatusUnauthorized {
		t.Fatalf("revoked by binding: %d", code)
	}
	if rev, err := e.reg.IsRevoked(ctx, "deadbeef"); err != nil || rev {
		t.Fatalf("an unknown serial is not revoked: %v %v", rev, err)
	}
}

func TestRevocationCheckFailsClosed(t *testing.T) {
	e := newEnv(t)
	// The server gets its own pool, closed after the client enrolled.
	pool, err := pgxpool.New(context.Background(), e.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	e.reg.pool = pool
	e.start()
	c := e.client("agent:host-01")
	if err := c.Emit(context.Background(), batch("agent:host-01")); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	err = c.Emit(context.Background(), batch("agent:host-01"))
	// 503 is transient: the batch waits in the spool instead of being lost.
	if err != nil {
		t.Fatalf("a 503 must be spooled, not returned: %v", err)
	}
	if st := c.Stats(); st.Spooled != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if got := e.rec.sources(); len(got) != 1 {
		t.Fatalf("nothing may be ingested while revocation cannot be checked: %v", got)
	}
}

func TestNoClientCertificateRejected(t *testing.T) {
	e := newEnv(t)
	e.start()
	hc := e.rawClient(nil)
	body := gz(t, batch("agent:host-01"))
	if code, _ := post(t, hc, e.url()+PathBatches, body, gzipHdr); code != http.StatusUnauthorized {
		t.Fatalf("batches without certificate: %d, want 401", code)
	}
	if code, _ := post(t, hc, e.url()+PathRenew, []byte(`{}`), nil); code != http.StatusUnauthorized {
		t.Fatalf("renew without certificate: %d, want 401", code)
	}
	if got := e.rec.sources(); len(got) != 0 {
		t.Fatal("reached the sink")
	}
}

func TestForeignCAClientCertificateRejected(t *testing.T) {
	e := newEnv(t)
	e.start()
	other, err := pki.NewCA("someone else", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id := issueIdentity(t, other, "agent:host-01", time.Hour, time.Now())
	hc := e.rawClient(&id)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.url()+PathBatches, strings.NewReader(""))
	// Go clients do not even offer a certificate the server's CA list does
	// not cover: the request then arrives anonymous and gets a 401. Either
	// way it must not be served.
	resp, err := hc.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a certificate from another CA got an answer: %d", resp.StatusCode)
		}
	}
	if got := e.rec.sources(); len(got) != 0 {
		t.Fatal("reached the sink")
	}
}

func TestServerCertificateCannotBeUsedAsClientCertificate(t *testing.T) {
	e := newEnv(t)
	e.start()
	hc := e.rawClient(&e.srvCert)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.url()+PathBatches, strings.NewReader(""))
	if resp, err := hc.Do(req); err == nil {
		defer resp.Body.Close()
		t.Fatalf("the server certificate authenticated a client: %d", resp.StatusCode)
	}
}

func TestExpiredCertificateRejected(t *testing.T) {
	e := newEnv(t)
	e.start()
	// Issued 8 days ago for 7: expired at the handshake.
	old := e.identity("agent:host-01", 7*24*time.Hour, time.Now().Add(-8*24*time.Hour))
	hc := e.rawClient(&old)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.url()+PathBatches, strings.NewReader(""))
	if resp, err := hc.Do(req); err == nil {
		defer resp.Body.Close()
		t.Fatalf("an expired certificate got an answer: %d", resp.StatusCode)
	}

	// A connection can outlive its certificate: the check is also made per
	// request, against the server's clock. The handshake passes (real clock),
	// the server clock is then moved past NotAfter.
	live := e.identity("agent:host-01", time.Hour, time.Now())
	hc = e.rawClient(&live)
	body := gz(t, batch("agent:host-01"))
	if code, out := post(t, hc, e.url()+PathBatches, body, gzipHdr); code != http.StatusNoContent {
		t.Fatalf("valid certificate: %d %s", code, out)
	}
	e.regTime.Advance(2 * time.Hour)
	if code, _ := post(t, hc, e.url()+PathBatches, body, gzipHdr); code != http.StatusUnauthorized {
		t.Fatalf("expired on a live connection: %d, want 401", code)
	}
}

func TestRenewKeepsTheBinding(t *testing.T) {
	e := newEnv(t)
	e.start()
	c := e.client("agent:host-01")
	ctx := context.Background()
	if err := c.Emit(ctx, batch("agent:host-01")); err != nil {
		t.Fatal(err)
	}
	old := c.currentCert()
	c.mu.Lock()
	err := c.renewLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	cur := c.currentCert()
	if cur.SerialNumber.Cmp(old.SerialNumber) == 0 {
		t.Fatal("renewal must produce a new certificate")
	}
	if b, _ := pki.BindingFromCert(cur); b != "agent:host-01" {
		t.Fatalf("binding after renewal = %q", b)
	}
	if cur.PublicKey.(*ecdsa.PublicKey).Equal(old.PublicKey) {
		t.Fatal("renewal must rotate the key")
	}
	// The new certificate works; the spoofing rule still applies to it.
	if err := c.Emit(ctx, batch("agent:host-01")); err != nil {
		t.Fatal(err)
	}
	if code := statusOf(t, c.Emit(ctx, batch("agent:host-02"))); code != http.StatusForbidden {
		t.Fatalf("after renewal: %d", code)
	}
	if serials, _ := e.reg.IssuedTo(ctx, "agent:host-01"); len(serials) != 2 {
		t.Fatalf("issued = %v", serials)
	}
	// A bad CSR on renew is a 400.
	if code, _ := post(t, e.rawClient(c.tlsc), e.url()+PathRenew, []byte(`{"csr":"nope"}`), nil); code != http.StatusBadRequest {
		t.Fatalf("renew with a bad CSR: %d", code)
	}
	if code, _ := post(t, e.rawClient(c.tlsc), e.url()+PathRenew, []byte(`{`), nil); code != http.StatusBadRequest {
		t.Fatalf("renew with malformed JSON: %d", code)
	}
}

func TestIngestGuards(t *testing.T) {
	e := newEnv(t, func(c *ServerConfig) { c.MaxBodyBytes = 2048; c.MaxBatchBytes = 16384 })
	e.start()
	id := e.identity("agent:host-01", time.Hour, time.Now())
	hc := e.rawClient(&id)
	url := e.url() + PathBatches

	invalid := batch("agent:host-01")
	invalid.Scope = "weekly" // fails Validate
	big := batch("agent:host-01")
	big.Object("pad", "device").Identify("name", "pad").Set("blob", strings.Repeat("x", 3000)) // ~random enough? no: compressible
	randBlob := make([]byte, 3000)                                                             // ~6 KB of hex: over 2 KiB compressed, under 16 KiB decompressed
	_, _ = rand.Read(randBlob)
	incompressible := batch("agent:host-01")
	incompressible.Object("pad", "device").Identify("name", "pad").Set("blob", hexOf(randBlob))
	bomb := batch("agent:host-01")
	bomb.Object("pad", "device").Identify("name", "pad").Set("blob", strings.Repeat("a", 100000))

	cases := []struct {
		name string
		body []byte
		hdr  map[string]string
		want int
	}{
		{"valid", gz(t, batch("agent:host-01")), gzipHdr, http.StatusNoContent},
		{"invalid batch", gz(t, invalid), gzipHdr, http.StatusBadRequest},
		{"spoofed and invalid: 403 comes first", gz(t, func() *sdk.Batch { b := batch("agent:host-02"); b.Scope = "x"; return b }()), gzipHdr, http.StatusForbidden},
		{"empty source", gz(t, func() *sdk.Batch { b := batch(""); return b }()), gzipHdr, http.StatusForbidden},
		{"not gzip header", gz(t, batch("agent:host-01")), map[string]string{"Content-Type": "application/json"}, http.StatusUnsupportedMediaType},
		{"gzip header, plain body", []byte(`{"source":"agent:host-01"}`), gzipHdr, http.StatusBadRequest},
		{"not json", gzBytes(t, []byte("hello")), gzipHdr, http.StatusBadRequest},
		{"trailing data", gzBytes(t, append(mustJSON(t, batch("agent:host-01")), []byte(`{"x":1}`)...)), gzipHdr, http.StatusBadRequest},
		{"compressed body over the limit", gz(t, incompressible), gzipHdr, http.StatusRequestEntityTooLarge},
		{"decompressed batch over the limit (gzip bomb)", gz(t, bomb), gzipHdr, http.StatusRequestEntityTooLarge},
		{"big but compressible, under both limits", gz(t, big), gzipHdr, http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, out := post(t, hc, url, c.body, c.hdr); code != c.want {
				t.Fatalf("status %d (%s), want %d", code, out, c.want)
			}
		})
	}
	if got := e.rec.sources(); len(got) != 2 {
		t.Fatalf("exactly the two good batches must reach the sink, got %v", got)
	}
	// GET is not a thing.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
}

func TestSinkErrors(t *testing.T) {
	e := newEnv(t)
	e.start()
	id := e.identity("agent:host-01", time.Hour, time.Now())
	hc := e.rawClient(&id)
	body := gz(t, batch("agent:host-01"))

	e.rec.setFail(errors.Join(ErrRejected, errors.New("schema says no")))
	if code, _ := post(t, hc, e.url()+PathBatches, body, gzipHdr); code != http.StatusUnprocessableEntity {
		t.Fatalf("ErrRejected: %d, want 422", code)
	}
	e.rec.setFail(errors.New("database is down"))
	code, out := post(t, hc, e.url()+PathBatches, body, gzipHdr)
	if code != http.StatusInternalServerError {
		t.Fatalf("sink failure: %d, want 500", code)
	}
	if strings.Contains(out, "database") {
		t.Fatalf("internal error detail leaked to the client: %s", out)
	}
}

func TestEmitterSink(t *testing.T) {
	var got string
	s := EmitterSink(sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error { got = b.Source; return nil }))
	if err := s.Ingest(context.Background(), batch("agent:host-01"), Identity{}); err != nil || got != "agent:host-01" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestServerConfigValidation(t *testing.T) {
	if _, err := NewServer(ServerConfig{}); err == nil {
		t.Error("an empty configuration must be refused")
	}
	ca, _ := pki.NewCA("x", time.Now())
	if _, err := NewServer(ServerConfig{CA: ca, Registry: &Registry{}, Sink: &recorder{}}); err == nil {
		t.Error("a server without certificate must be refused")
	}
}

func TestRegistryDefaults(t *testing.T) {
	e := newEnv(t)
	r := NewRegistry(e.pool) // real clock
	if _, err := r.NewEnrolmentToken(context.Background(), "agent:host-01", 0); err != nil {
		t.Fatal(err)
	}
	var created, expires time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT created_at, expires_at FROM enrol_tokens`).Scan(&created, &expires); err != nil {
		t.Fatal(err)
	}
	if d := expires.Sub(created); d != DefaultTokenTTL {
		t.Fatalf("default token lifetime = %v", d)
	}
}

// blockingSink holds every batch until release is closed.
type blockingSink struct {
	in      chan struct{}
	release chan struct{}
}

func (b *blockingSink) Ingest(context.Context, *sdk.Batch, Identity) error {
	b.in <- struct{}{}
	<-b.release
	return nil
}

func TestServeReturnsOnlyOnceRequestsAreDrained(t *testing.T) {
	sink := &blockingSink{in: make(chan struct{}, 1), release: make(chan struct{})}
	e := newEnv(t, func(c *ServerConfig) { c.Sink = sink })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.addr = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- e.srv.Serve(ctx, ln) }()

	c := e.client("agent:host-01")
	emitted := make(chan error, 1)
	go func() { emitted <- c.Emit(context.Background(), batch("agent:host-01")) }()
	<-sink.in // the batch is in the sink
	cancel()
	select {
	case err := <-served:
		close(sink.release)
		t.Fatalf("Serve returned (%v) while a batch was still being ingested", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(sink.release)
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return once the batch was ingested")
	}
	if err := <-emitted; err != nil {
		t.Fatalf("the batch in flight at shutdown must be answered: %v", err)
	}
}
