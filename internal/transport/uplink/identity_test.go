package uplink

import (
	"crypto/ecdsa"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/VargaFoundation/trica/internal/pki"
)

// provisionMismatch writes an identity file whose key belongs to a, whose
// certificate belongs to b.
func provisionMismatch(t *testing.T, dir string, a, b tls.Certificate) {
	t.Helper()
	keyPEM, err := pki.MarshalKeyPEM(a.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, IdentityFile), append(keyPEM, pki.CertPEM(b.Leaf)...), 0o600); err != nil {
		t.Fatal(err)
	}
}
