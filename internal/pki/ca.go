// Package pki is the internal certificate authority of the transport
// (docs/adr/0008-transport.md): an ECDSA P-256 CA that signs short-lived
// client certificates, each bound to the source prefix its holder may report
// as, and the server certificate.
//
// Key material is never logged and never part of an error message.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultClientTTL is the validity of a client certificate.
	DefaultClientTTL = 7 * 24 * time.Hour
	// DefaultCATTL is the validity of a newly generated CA.
	DefaultCATTL = 10 * 365 * 24 * time.Hour
	// clockSkew backdates NotBefore so that a client whose clock is a little
	// behind does not see a certificate that is not yet valid.
	clockSkew = time.Minute

	caCertFile = "ca.pem"
	caKeyFile  = "ca-key.pem"
)

// CACertFile is the name, inside the CA directory, of the public CA
// certificate a client needs to trust the server.
const CACertFile = caCertFile

// CA signs certificates. It is safe for concurrent use.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// NewCA generates a CA valid from now for DefaultCATTL.
func NewCA(commonName string, now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pki: generate CA key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"trica"}},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(DefaultCATTL),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("pki: self-sign CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("pki: parse CA: %w", err)
	}
	return &CA{cert: cert, key: key}, nil
}

// LoadCA reads a CA from PEM files given by reference (a mounted secret, for
// instance). The key may be PKCS#8 or SEC 1; it must be ECDSA P-256 and match
// the certificate, which must be a CA.
func LoadCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath) // #nosec G304 -- path chosen by the operator
	if err != nil {
		return nil, fmt.Errorf("pki: read CA certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath) // #nosec G304 -- path chosen by the operator
	if err != nil {
		return nil, fmt.Errorf("pki: read CA key: %w", err)
	}
	cert, err := ParseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := ParseKeyPEM(keyPEM)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, errors.New("pki: the CA certificate is not a CA")
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("pki: the CA key does not match the CA certificate")
	}
	return &CA{cert: cert, key: key}, nil
}

// LoadOrCreateCA loads ca.pem and ca-key.pem from dir, or generates and saves
// them there (directory 0700, key 0600) when neither exists. Having only one
// of the two files is an error, never a reason to regenerate: replacing a CA
// silently would invalidate every enrolled client.
func LoadOrCreateCA(dir, commonName string, now time.Time) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile)
	_, errC := os.Stat(certPath)
	_, errK := os.Stat(keyPath)
	switch {
	case errC == nil && errK == nil:
		return LoadCA(certPath, keyPath)
	case errC == nil || errK == nil:
		return nil, fmt.Errorf("pki: %s holds only one of %s and %s", dir, caCertFile, caKeyFile)
	case !errors.Is(errC, os.ErrNotExist) || !errors.Is(errK, os.ErrNotExist):
		return nil, fmt.Errorf("pki: inspect %s: %w", dir, errors.Join(errC, errK))
	}
	ca, err := NewCA(commonName, now)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("pki: create %s: %w", dir, err)
	}
	keyPEM, err := MarshalKeyPEM(ca.key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("pki: write CA key: %w", err)
	}
	if err := os.WriteFile(certPath, ca.CertPEM(), 0o644); err != nil { // #nosec G306 -- public certificate
		return nil, fmt.Errorf("pki: write CA certificate: %w", err)
	}
	return ca, nil
}

// Cert returns the CA certificate.
func (ca *CA) Cert() *x509.Certificate { return ca.cert }

// CertPEM returns the CA certificate, PEM encoded: the trust anchor clients
// are given.
func (ca *CA) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

// Pool returns a pool holding only this CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// IssueClient signs a client certificate for the public key of csr, valid
// ttl from now (DefaultClientTTL when ttl <= 0), bound to binding. Whatever
// subject or extension the CSR asks for is ignored: the binding is the only
// identity the CA ever grants. The CSR signature (proof of possession of the
// key) and the key type (ECDSA P-256) are checked.
func (ca *CA) IssueClient(csr *x509.CertificateRequest, binding string, ttl time.Duration, now time.Time) (*x509.Certificate, error) {
	if err := ValidBinding(binding); err != nil {
		return nil, err
	}
	pub, err := ValidateCSR(csr)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = DefaultClientTTL
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: binding},
		URIs:                  []*url.URL{bindingURI(binding)},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	return ca.sign(tmpl, pub)
}

// ValidateCSR checks the proof of possession (the CSR signature) and the key
// type (ECDSA P-256), and returns the public key to certify.
func ValidateCSR(csr *x509.CertificateRequest) (*ecdsa.PublicKey, error) {
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("pki: CSR signature: %w", err)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New("pki: CSR key must be ECDSA P-256")
	}
	return pub, nil
}

// IssueServer generates a key and a certificate for the server, valid for the
// given host names and IP addresses (loopback and documentation addresses in
// tests; the public name of the server in production).
func (ca *CA) IssueServer(hosts []string, ttl time.Duration, now time.Time) (tls.Certificate, error) {
	if len(hosts) == 0 {
		return tls.Certificate{}, errors.New("pki: a server certificate needs at least one host")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("pki: generate server key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hosts[0]},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	cert, err := ca.sign(tmpl, &key.PublicKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}, nil
}

func (ca *CA) sign(tmpl *x509.Certificate, pub *ecdsa.PublicKey) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, fmt.Errorf("pki: sign certificate: %w", err)
	}
	return x509.ParseCertificate(der)
}

func newSerial() (*big.Int, error) {
	// 127 bits: positive, unguessable, unique with overwhelming probability.
	s, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("pki: serial: %w", err)
	}
	return s.Add(s, big.NewInt(1)), nil
}

// SerialString is the canonical text form of a certificate serial number
// (lowercase hex), the one stored in the revocation list.
func SerialString(c *x509.Certificate) string { return c.SerialNumber.Text(16) }

// CertPEM encodes a certificate.
func CertPEM(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}

// ParseCertPEM parses the first certificate of a PEM input.
func ParseCertPEM(p []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(p)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("pki: no CERTIFICATE PEM block")
	}
	return x509.ParseCertificate(blk.Bytes)
}

// ParseCSRPEM parses a PEM encoded certificate request.
func ParseCSRPEM(p []byte) (*x509.CertificateRequest, error) {
	blk, _ := pem.Decode(p)
	if blk == nil || blk.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("pki: no CERTIFICATE REQUEST PEM block")
	}
	return x509.ParseCertificateRequest(blk.Bytes)
}

// GenerateKey creates a client key (ECDSA P-256).
func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// NewCSR builds a PEM certificate request for key. The request carries no
// identity: the server decides the binding.
func NewCSR(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return nil, fmt.Errorf("pki: create CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// MarshalKeyPEM encodes a private key as PKCS#8 PEM. The result is key
// material: write it with mode 0600 and never log it.
func MarshalKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("pki: marshal key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParseKeyPEM reads an ECDSA P-256 private key (PKCS#8 or SEC 1). Errors never
// include the input.
func ParseKeyPEM(p []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(p)
	if blk == nil {
		return nil, errors.New("pki: no PEM block in key")
	}
	var key *ecdsa.PrivateKey
	switch blk.Type {
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return nil, errors.New("pki: cannot parse PKCS#8 key")
		}
		ek, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("pki: key is not ECDSA")
		}
		key = ek
	case "EC PRIVATE KEY":
		k, err := x509.ParseECPrivateKey(blk.Bytes)
		if err != nil {
			return nil, errors.New("pki: cannot parse SEC 1 key")
		}
		key = k
	default:
		return nil, fmt.Errorf("pki: unsupported key PEM type %q", blk.Type)
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("pki: key must be ECDSA P-256")
	}
	return key, nil
}
