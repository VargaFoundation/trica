package update

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ErrSignature means the signature over SHA256SUMS does not verify with the pinned key.
var ErrSignature = errors.New("signature does not verify")

// ErrChecksum means the artifact is not the one SHA256SUMS lists.
var ErrChecksum = errors.New("checksum mismatch")

// ParsePublicKey reads the pinned key: a PEM "PUBLIC KEY" (PKIX), either an
// ECDSA P-256 key, which is what `cosign generate-key-pair` makes, or Ed25519.
func ParsePublicKey(pemBytes []byte) (crypto.PublicKey, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil || blk.Type != "PUBLIC KEY" {
		return nil, errors.New("update key: not a PEM PUBLIC KEY")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("update key: %w", err)
	}
	switch pk := k.(type) {
	case ed25519.PublicKey:
		return pk, nil
	case *ecdsa.PublicKey:
		if pk.Curve != elliptic.P256() {
			return nil, errors.New("update key: ECDSA keys must be P-256")
		}
		return pk, nil
	}
	return nil, fmt.Errorf("update key: unsupported key type %T", k)
}

// VerifySignature checks sig, the base64 text `cosign sign-blob --key` writes
// (ASN.1 ECDSA over the SHA-256 of msg), or a base64 Ed25519 signature of msg.
func VerifySignature(pub crypto.PublicKey, msg, sig []byte) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil {
		return fmt.Errorf("%w: not base64", ErrSignature)
	}
	switch pk := pub.(type) {
	case ed25519.PublicKey:
		if ed25519.Verify(pk, msg, raw) {
			return nil
		}
	case *ecdsa.PublicKey:
		d := sha256.Sum256(msg)
		if ecdsa.VerifyASN1(pk, d[:], raw) {
			return nil
		}
	default:
		return fmt.Errorf("%w: unsupported key type %T", ErrSignature, pub)
	}
	return ErrSignature
}

// ParseSums reads a SHA256SUMS file: "<64 hex>  <name>" per line. A name listed
// twice is refused, since which line counts would be ambiguous.
func ParseSums(b []byte) (map[string][sha256.Size]byte, error) {
	out := map[string][sha256.Size]byte{}
	for i, line := range bytes.Split(b, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" {
			continue
		}
		hexSum, name, ok := strings.Cut(s, " ")
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		raw, err := hex.DecodeString(hexSum)
		if !ok || err != nil || len(raw) != sha256.Size || name == "" {
			return nil, fmt.Errorf("SHA256SUMS line %d is malformed", i+1)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("SHA256SUMS lists %q twice", name)
		}
		out[name] = [sha256.Size]byte(raw)
	}
	return out, nil
}
