package update

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
)

func pemOf(t *testing.T, pub any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func TestParsePublicKey(t *testing.T) {
	ec, ed := newECDSASigner(t), newEd25519Signer(t)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	for name, tc := range map[string]struct {
		in []byte
		ok bool
	}{
		"ecdsa p-256": {pemOf(t, ec.pub), true},
		"ed25519":     {pemOf(t, ed.pub), true},
		"ecdsa p-384": {pemOf(t, &p384.PublicKey), false},
		"not pem":     {[]byte("hello"), false},
		"wrong block": {pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}), false},
		"bad der":     {pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1, 2}}), false},
	} {
		if _, err := ParsePublicKey(tc.in); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestVerifySignature(t *testing.T) {
	msg := []byte("SHA256SUMS content")
	ec, ed := newECDSASigner(t), newEd25519Signer(t)
	for name, s := range map[string]signer{"ecdsa": ec, "ed25519": ed} {
		if err := VerifySignature(s.pub, msg, s.sign(msg)); err != nil {
			t.Errorf("%s: good signature refused: %v", name, err)
		}
		if err := VerifySignature(s.pub, append([]byte("x"), msg...), s.sign(msg)); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: signature over another message accepted: %v", name, err)
		}
	}
	// A key of one type never accepts a signature made for the other.
	if err := VerifySignature(ec.pub, msg, ed.sign(msg)); !errors.Is(err, ErrSignature) {
		t.Errorf("cross-algorithm signature accepted: %v", err)
	}
	if err := VerifySignature("not a key", msg, ec.sign(msg)); !errors.Is(err, ErrSignature) {
		t.Errorf("unsupported key type accepted: %v", err)
	}
}
