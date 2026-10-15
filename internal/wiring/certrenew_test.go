package wiring_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The transport renews its server certificate without closing its listener:
// a connection opened before the renewal still works after it, on the same
// address (#57).
func TestTransportRenewsItsCertificateInPlace(t *testing.T) {
	r := newRig(t, `
transport:
  addr: "127.0.0.1:0"
  ca_dir: "{CA}"
  hosts: [localhost, 127.0.0.1]
  server_cert_ttl: 6s
`)
	addr := r.transportAddr()
	pem, err := os.ReadFile(filepath.Join(r.caDir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	dial := func() *tls.Conn {
		t.Helper()
		c, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: roots, ServerName: "localhost", NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS13})
		if err != nil {
			t.Fatalf("dial %s: %v", addr, err)
		}
		return c
	}
	serial := func(c *tls.Conn) string { return c.ConnectionState().PeerCertificates[0].SerialNumber.String() }

	before := dial()
	defer func() { _ = before.Close() }()
	br := bufio.NewReader(before)
	roundTrip := func() error {
		if _, err := io.WriteString(before, "POST /v1/batches HTTP/1.1\r\nHost: trica\r\nContent-Length: 0\r\n\r\n"); err != nil {
			return err
		}
		res, err := http.ReadResponse(br, nil)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		return res.Body.Close()
	}
	if err := roundTrip(); err != nil {
		t.Fatal(err)
	}

	// Renewal after two thirds of 6 s: a new handshake meets a new certificate.
	// (Not less: NotAfter is truncated to the second, which can take up to a
	// second off the margin between the renewal and the expiry.)
	deadline := time.Now().Add(patience)
	for {
		c := dial()
		renewed := serial(c) != serial(before)
		_ = c.Close()
		if renewed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server certificate was never renewed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := r.w.TransportAddr(); got != addr {
		t.Fatalf("the transport moved from %s to %s", addr, got)
	}
	if err := roundTrip(); err != nil {
		t.Fatalf("the connection opened before the renewal was dropped: %v", err)
	}
}
