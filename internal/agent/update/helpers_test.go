package update

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const testIdentity = "agent:host-a1"

// signer signs like `cosign sign-blob --key` (ECDSA P-256, ASN.1, over SHA-256)
// or with Ed25519.
type signer struct {
	pub  crypto.PublicKey
	sign func(msg []byte) []byte
}

func newECDSASigner(t testing.TB) signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{pub: &k.PublicKey, sign: func(msg []byte) []byte {
		d := sha256.Sum256(msg)
		sig, err := ecdsa.SignASN1(rand.Reader, k, d[:])
		if err != nil {
			t.Fatal(err)
		}
		return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
	}}
}

func newEd25519Signer(t testing.TB) signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{pub: pub, sign: func(msg []byte) []byte {
		return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)))
	}}
}

// release builds the three files of a release for artifact bytes bin.
func release(s signer, version string, bin []byte) map[string][]byte {
	sum := sha256.Sum256(bin)
	name := ArtifactName(version, "linux", "amd64")
	sums := []byte(fmt.Sprintf("%s  %s\n%s  other_file\n", hex.EncodeToString(sum[:]), name, hex.EncodeToString(make([]byte, 32))))
	return map[string][]byte{name: bin, SumsName: sums, SigName: s.sign(sums)}
}

// rig is an Updater on a fake source and a temporary store.
type rig struct {
	src   *FakeSource
	store *Store
	dir   string
	up    *Updater
}

func newRig(t *testing.T, s signer, version string, mut func(*Config)) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{src: &FakeSource{}, store: NewStore(dir), dir: dir}
	cfg := Config{Source: r.src, Store: r.store, Key: s.pub, Identity: testIdentity, Version: version, Arch: "amd64"}
	if mut != nil {
		mut(&cfg)
	}
	up, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.up = up
	return r
}

// untouched fails if the store holds anything: no state, no staged binary, no
// download left over.
func (r *rig) untouched(t *testing.T) {
	t.Helper()
	ents, err := os.ReadDir(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		t.Errorf("store is not empty: %s", filepath.Join(r.dir, e.Name()))
	}
}
