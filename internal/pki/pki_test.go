package pki_test

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func newCA(t *testing.T) *pki.CA {
	t.Helper()
	ca, err := pki.NewCA("trica test CA", t0)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func csrFor(t *testing.T) *x509.CertificateRequest {
	t.Helper()
	key, err := pki.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	p, err := pki.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := pki.ParseCSRPEM(p)
	if err != nil {
		t.Fatal(err)
	}
	return csr
}

func TestBindingRules(t *testing.T) {
	valid := []string{"agent:host-01", "collector:site-a", "agent:host-01:docker", "a.b:c_d"}
	for _, b := range valid {
		if err := pki.ValidBinding(b); err != nil {
			t.Errorf("%q should be valid: %v", b, err)
		}
	}
	invalid := []string{"", "agent", "agent:", ":host", "agent::x", "agent:host 01", "agent:host/01", "agent:*", strings.Repeat("a:", 100)}
	for _, b := range invalid {
		if err := pki.ValidBinding(b); err == nil {
			t.Errorf("%q should be invalid", b)
		}
	}
}

func TestAllows(t *testing.T) {
	cases := []struct {
		binding, source string
		want            bool
	}{
		{"agent:host-01", "agent:host-01", true},
		{"agent:host-01", "agent:host-01:docker", true},
		{"agent:host-01", "agent:host-010", false}, // a longer name is another source
		{"agent:host-01", "agent:host-02", false},
		{"agent:host-01", "agent", false},
		{"agent:host-01", "", false},
		{"agent:host-01", "other:agent:host-01", false},
		{"", "agent:host-01", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := pki.Allows(c.binding, c.source); got != c.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", c.binding, c.source, got, c.want)
		}
	}
}

func TestIssueClientBindsTheServersChoiceNotTheCSRs(t *testing.T) {
	ca := newCA(t)
	// A CSR that asks for another identity: subject and URI SAN of someone else.
	key, _ := pki.GenerateKey()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "agent:victim"},
		URIs:    []*url.URL{{Scheme: "urn", Opaque: "trica:source:agent:victim"}},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueClient(csr, "agent:host-01", 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := pki.BindingFromCert(cert); err != nil || b != "agent:host-01" {
		t.Fatalf("binding = %q, %v", b, err)
	}
	if cert.Subject.CommonName != "agent:host-01" || len(cert.URIs) != 1 {
		t.Fatalf("CSR identity leaked into the certificate: %v %v", cert.Subject, cert.URIs)
	}
	if got := cert.NotAfter.Sub(t0); got != pki.DefaultClientTTL {
		t.Fatalf("default validity = %v", got)
	}
	if cert.IsCA {
		t.Fatal("a client certificate must not be a CA")
	}
	roots := ca.Pool()
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: t0, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: t0.Add(8 * 24 * time.Hour), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("the certificate must expire after its ttl")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: t0, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("a client certificate must not be usable as a server certificate")
	}
	if pki.SerialString(cert) == "" {
		t.Fatal("empty serial")
	}
}

func TestIssueClientRefusals(t *testing.T) {
	ca := newCA(t)
	if _, err := ca.IssueClient(csrFor(t), "agent", time.Hour, t0); err == nil {
		t.Error("an invalid binding must be refused")
	}
	// RSA key: refused, the transport is ECDSA P-256 only.
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, rk)
	rsaCSR, _ := x509.ParseCertificateRequest(der)
	if _, err := ca.IssueClient(rsaCSR, "agent:host-01", time.Hour, t0); err == nil {
		t.Error("an RSA CSR must be refused")
	}
	// Corrupted signature: no proof of possession.
	p, _ := pki.NewCSR(mustKey(t))
	blk, _ := pem.Decode(p)
	blk.Bytes[len(blk.Bytes)-1] ^= 0xff
	bad, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.IssueClient(bad, "agent:host-01", time.Hour, t0); err == nil {
		t.Error("a CSR with a bad signature must be refused")
	}
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := pki.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestServerCertificateIsNotAClientCertificate(t *testing.T) {
	ca := newCA(t)
	sc, err := ca.IssueServer([]string{"127.0.0.1", "trica.example.test"}, time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	leaf := sc.Leaf
	if len(leaf.IPAddresses) != 1 || len(leaf.DNSNames) != 1 {
		t.Fatalf("SANs: %v %v", leaf.IPAddresses, leaf.DNSNames)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: ca.Pool(), CurrentTime: t0, DNSName: "trica.example.test", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("server verify: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: ca.Pool(), CurrentTime: t0, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("the server certificate must not authenticate a client")
	}
	if _, err := ca.IssueServer(nil, time.Hour, t0); err == nil {
		t.Fatal("a server certificate needs a host")
	}
}

func TestBindingFromCertErrors(t *testing.T) {
	ca := newCA(t)
	if _, err := pki.BindingFromCert(ca.Cert()); err == nil {
		t.Error("a certificate without binding must be refused")
	}
	two := &x509.Certificate{URIs: []*url.URL{
		{Scheme: "urn", Opaque: "trica:source:agent:a"},
		{Scheme: "urn", Opaque: "trica:source:agent:b"},
	}}
	if _, err := pki.BindingFromCert(two); err == nil {
		t.Error("two bindings must be refused")
	}
	bad := &x509.Certificate{URIs: []*url.URL{{Scheme: "urn", Opaque: "trica:source:agent"}}}
	if _, err := pki.BindingFromCert(bad); err == nil {
		t.Error("an invalid binding must be refused")
	}
	other := &x509.Certificate{URIs: []*url.URL{{Scheme: "https", Host: "example.test"}}}
	if _, err := pki.BindingFromCert(other); err == nil {
		t.Error("an unrelated URI SAN is not a binding")
	}
}

func TestLoadOrCreateCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	ca, err := pki.LoadOrCreateCA(dir, "trica test CA", t0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("CA key mode = %v, want 0600", info.Mode().Perm())
	}
	again, err := pki.LoadOrCreateCA(dir, "other name", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Cert().Equal(ca.Cert()) {
		t.Fatal("loading must return the same CA, not generate a new one")
	}
	// A certificate signed by the reloaded CA verifies against the original.
	cert, err := again.IssueClient(csrFor(t), "agent:host-01", time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), CurrentTime: t0, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	// One file missing is an error, never a silent regeneration.
	if err := os.Remove(filepath.Join(dir, "ca-key.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.LoadOrCreateCA(dir, "x", t0); err == nil {
		t.Fatal("a CA directory holding only the certificate must be an error")
	}
}

func TestLoadCAByReferenceRefusals(t *testing.T) {
	dir := t.TempDir()
	ca := newCA(t)
	certPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(certPath, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	// Key of another CA.
	otherDir := t.TempDir()
	if _, err := pki.LoadOrCreateCA(otherDir, "other", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.LoadCA(certPath, filepath.Join(otherDir, "ca-key.pem")); err == nil {
		t.Error("a key that does not match the certificate must be refused")
	}
	if _, err := pki.LoadCA(certPath, filepath.Join(dir, "missing.pem")); err == nil {
		t.Error("a missing key must be an error")
	}
	if _, err := pki.LoadCA(filepath.Join(dir, "missing.pem"), certPath); err == nil {
		t.Error("a missing certificate must be an error")
	}
	if _, err := pki.LoadCA(certPath, certPath); err == nil {
		t.Error("a certificate in place of the key must be refused")
	}
	// A leaf certificate is not a CA.
	leaf, err := ca.IssueClient(csrFor(t), "agent:host-01", time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	leafPath := filepath.Join(dir, "leaf.pem")
	if err := os.WriteFile(leafPath, pki.CertPEM(leaf), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.LoadCA(leafPath, filepath.Join(otherDir, "ca-key.pem")); err == nil {
		t.Error("a non-CA certificate must be refused")
	}
}

func TestKeyParsing(t *testing.T) {
	key := mustKey(t)
	p, err := pki.MarshalKeyPEM(key)
	if err != nil {
		t.Fatal(err)
	}
	back, err := pki.ParseKeyPEM(p)
	if err != nil || !back.Equal(key) {
		t.Fatalf("round trip: %v", err)
	}
	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := pki.ParseKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1})); err != nil || !k.Equal(key) {
		t.Fatalf("SEC 1: %v", err)
	}
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	rder, _ := x509.MarshalPKCS8PrivateKey(rk)
	bad := [][]byte{
		nil,
		[]byte("not pem"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rder}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("junk")}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}),
	}
	for i, b := range bad {
		_, err := pki.ParseKeyPEM(b)
		if err == nil {
			t.Errorf("case %d: expected an error", i)
			continue
		}
		if strings.Contains(err.Error(), "junk") {
			t.Errorf("case %d: the error echoes key material: %v", i, err)
		}
	}
	if _, err := pki.ParseCertPEM([]byte("x")); err == nil {
		t.Error("ParseCertPEM must refuse non-PEM")
	}
	if _, err := pki.ParseCSRPEM([]byte("x")); err == nil {
		t.Error("ParseCSRPEM must refuse non-PEM")
	}
}
