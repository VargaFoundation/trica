package transport

import (
	"bufio"
	"crypto/tls"
	"io"
	"net/http"
	"testing"
	"time"
)

// dialTLS opens a TLS connection to the env's server speaking HTTP/1.1. The
// caller closes it before stopping the server, whose shutdown otherwise waits
// for it.
func dialTLS(t *testing.T, e *env) (*tls.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := tls.Dial("tcp", e.addr, &tls.Config{RootCAs: e.ca.Pool(), ServerName: "127.0.0.1", NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	return conn, bufio.NewReader(conn)
}

// roundTrip sends one request on conn and reads the answer.
func roundTrip(conn *tls.Conn, br *bufio.Reader) error {
	if _, err := io.WriteString(conn, "POST /v1/batches HTTP/1.1\r\nHost: trica\r\nContent-Length: 0\r\n\r\n"); err != nil {
		return err
	}
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return res.Body.Close()
}

// A new server certificate is presented from the next handshake on, on the
// same listener, and a connection opened before keeps working (#57).
func TestSetServerCertTakesEffectAtTheNextHandshake(t *testing.T) {
	e := newEnv(t)
	defer e.start()()

	before, br := dialTLS(t, e)
	defer func() { _ = before.Close() }()
	if err := roundTrip(before, br); err != nil {
		t.Fatal(err)
	}
	if got := before.ConnectionState().PeerCertificates[0].SerialNumber; got.Cmp(e.srvCert.Leaf.SerialNumber) != 0 {
		t.Fatalf("serial %v, want the first certificate", got)
	}

	renewed, err := e.ca.IssueServer([]string{"127.0.0.1"}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.srv.SetServerCert(renewed); err != nil {
		t.Fatal(err)
	}
	after, _ := dialTLS(t, e)
	_ = after.Close()
	if got := after.ConnectionState().PeerCertificates[0].SerialNumber; got.Cmp(renewed.Leaf.SerialNumber) != 0 {
		t.Fatalf("serial %v after SetServerCert, want the renewed certificate %v", got, renewed.Leaf.SerialNumber)
	}
	if err := roundTrip(before, br); err != nil {
		t.Fatalf("the connection opened before the renewal was dropped: %v", err)
	}

	if err := e.srv.SetServerCert(tls.Certificate{}); err == nil {
		t.Fatal("an empty certificate was accepted")
	}
}
