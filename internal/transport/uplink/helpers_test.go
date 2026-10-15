package uplink

import (
	"crypto/ecdsa"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The client is tested here against servers the tests script (fakeServer)
// or against no server at all. Its behaviour against the real server, with
// enrolment, outages and renewal, is tested in internal/transport, which can
// link both sides (client_integration_test.go).

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

// provision writes an identity file as a client that enrolled earlier would
// have, so that tests of the client alone need no enrolment.
func provision(t *testing.T, ca *pki.CA, dir, binding string, ttl time.Duration, now time.Time) {
	t.Helper()
	id := issueIdentity(t, ca, binding, ttl, now)
	keyPEM, err := pki.MarshalKeyPEM(id.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	data := slices.Concat(keyPEM, pki.CertPEM(id.Leaf))
	if err := os.WriteFile(filepath.Join(dir, IdentityFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// batch is a minimal valid batch of the given source.
func batch(source string) *sdk.Batch {
	b := sdk.NewBatch(source, sdk.ScopePartial)
	b.Object("h", "device").Identify("name", "host-01")
	return b
}

// fakeServer is a TLS server whose behaviour the test controls, for failure
// injection that the real server never produces (5xx, 429, ...). It
// requests but does not require client certificates.
func fakeServer(t *testing.T, ca *pki.CA, h http.HandlerFunc) (url string, caFile string) {
	t.Helper()
	sc, err := ca.IssueServer([]string{"127.0.0.1"}, 90*24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{sc},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    ca.Pool(),
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv.URL, caFile
}

func spoolFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func srcs(prefix string, from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("%s%d", prefix, i))
	}
	return out
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }

// statusOf is the HTTP status carried by err, which must be a StatusError.
func statusOf(t *testing.T, err error) int {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want a StatusError, got %T: %v", err, err)
	}
	return se.Code
}
