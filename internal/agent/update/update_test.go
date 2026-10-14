package update

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"
)

var fakeBinary = []byte("#!/bin/true\nnot a real agent\n")

func (r *rig) offer(s signer, version string, percent float64) {
	r.src.Version, r.src.Percent = version, percent
	r.src.Files = release(s, version, fakeBinary)
}

func TestCheckOnceStagesAVerifiedRelease(t *testing.T) {
	for name, s := range map[string]signer{"ecdsa-p256 (cosign key)": newECDSASigner(t), "ed25519": newEd25519Signer(t)} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, s, "1.0.0", nil)
			r.offer(s, "1.1.0", 100)
			res, err := r.up.CheckOnce(context.Background())
			if err != nil || res.Outcome != Staged || res.Version != "1.1.0" {
				t.Fatalf("CheckOnce = %+v, %v", res, err)
			}
			st, _ := r.store.Load()
			if st.Current != "1.1.0" || st.Previous != "" || st.Pending == nil || st.Pending.Version != "1.1.0" {
				t.Errorf("state = %+v", st)
			}
			got, err := os.ReadFile(r.store.BinaryPath("1.1.0"))
			if err != nil || string(got) != string(fakeBinary) {
				t.Errorf("staged binary: %q, %v", got, err)
			}
			if fi, _ := os.Stat(r.store.BinaryPath("1.1.0")); fi.Mode().Perm() != 0o755 {
				t.Errorf("staged binary mode %v", fi.Mode().Perm())
			}
			q := r.src.Queries[0]
			if q.Identity != testIdentity || q.Current != "1.0.0" || q.OS != "linux" || q.Arch != "amd64" {
				t.Errorf("query = %+v", q)
			}
		})
	}
}

// A release that cannot be verified is never staged: nothing is left on disk,
// and the artifact is not even downloaded when the signature already fails.
func TestUnverifiableReleaseIsNeverStaged(t *testing.T) {
	good := newECDSASigner(t)
	art := ArtifactName("1.1.0", "linux", "amd64")
	for _, tc := range []struct {
		name      string
		tamper    func(files map[string][]byte) *signer // returns a signer to re-sign the sums with
		wantErr   error
		artifactD bool // the artifact may have been downloaded
	}{
		{"signed by another key", func(_ map[string][]byte) *signer { s := newECDSASigner(t); return &s }, ErrSignature, false},
		{"signed by an ed25519 key", func(_ map[string][]byte) *signer { s := newEd25519Signer(t); return &s }, ErrSignature, false},
		{"sums edited after signing", func(f map[string][]byte) *signer { f[SumsName][0] ^= 0x01; return nil }, ErrSignature, false},
		{"signature is garbage", func(f map[string][]byte) *signer { f[SigName] = []byte("%%%"); return nil }, ErrSignature, false},
		{"signature is empty", func(f map[string][]byte) *signer { f[SigName] = nil; return nil }, ErrSignature, false},
		{"artifact swapped after signing", func(f map[string][]byte) *signer { f[art] = []byte("evil"); return nil }, ErrChecksum, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, good, "1.0.0", nil)
			r.offer(good, "1.1.0", 100)
			if other := tc.tamper(r.src.Files); other != nil {
				r.src.Files[SigName] = other.sign(r.src.Files[SumsName])
			}
			res, err := r.up.CheckOnce(context.Background())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if res.Outcome == Staged {
				t.Fatal("an unverifiable release was staged")
			}
			r.untouched(t)
			if !tc.artifactD && slices.Contains(r.src.Opened, art) {
				t.Errorf("artifact downloaded before its checksum list was verified: %v", r.src.Opened)
			}
		})
	}
}

func TestReleaseNotListedOrMalformedIsRefused(t *testing.T) {
	s := newECDSASigner(t)
	for name, sums := range map[string]string{
		"artifact not listed": "0000000000000000000000000000000000000000000000000000000000000000  other\n",
		"malformed line":      "nothex  trica\n",
		"duplicate name":      "0000000000000000000000000000000000000000000000000000000000000000  a\n0000000000000000000000000000000000000000000000000000000000000000  a\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, s, "1.0.0", nil)
			r.offer(s, "1.1.0", 100)
			r.src.Files[SumsName] = []byte(sums)
			r.src.Files[SigName] = s.sign([]byte(sums))
			if _, err := r.up.CheckOnce(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			r.untouched(t)
		})
	}
}

func TestOversizedArtifactIsRefused(t *testing.T) {
	s := newECDSASigner(t)
	r := newRig(t, s, "1.0.0", func(c *Config) { c.MaxArtifact = 8 })
	r.offer(s, "1.1.0", 100)
	if _, err := r.up.CheckOnce(context.Background()); err == nil {
		t.Fatal("a 28 byte artifact passed an 8 byte limit")
	}
	r.untouched(t)
}

// The server decides the wave, and the agent checks again.
func TestAgentOutsideTheWaveKeepsItsVersion(t *testing.T) {
	s := newECDSASigner(t)
	t.Run("the server leaves it out", func(t *testing.T) {
		r := newRig(t, s, "1.0.0", nil)
		r.offer(s, "1.1.0", 0)
		res, err := r.up.CheckOnce(context.Background())
		if err != nil || res.Outcome != NoOffer {
			t.Fatalf("= %+v, %v", res, err)
		}
		r.untouched(t)
	})
	t.Run("the server offers to everybody by mistake", func(t *testing.T) {
		r := newRig(t, s, "1.0.0", nil)
		r.offer(s, "1.1.0", 0)
		r.src.OfferToAll = true
		res, err := r.up.CheckOnce(context.Background())
		if err != nil || res.Outcome != OutsideWave {
			t.Fatalf("= %+v, %v", res, err)
		}
		if len(r.src.Opened) != 0 {
			t.Errorf("downloaded %v while outside the wave", r.src.Opened)
		}
		r.untouched(t)
	})
	t.Run("the wave widens and the agent joins", func(t *testing.T) {
		// Find a percentage that excludes this agent, then widen to 100.
		r := newRig(t, s, "1.0.0", nil)
		b := Bucket(testIdentity, "1.1.0")
		r.offer(s, "1.1.0", float64(b)/100) // threshold == bucket: still outside
		res, _ := r.up.CheckOnce(context.Background())
		if b > 0 && res.Outcome == Staged {
			t.Fatalf("staged at the threshold below its bucket (%d)", b)
		}
		r.offer(s, "1.1.0", 100)
		if res, err := r.up.CheckOnce(context.Background()); err != nil || res.Outcome != Staged {
			t.Fatalf("= %+v, %v", res, err)
		}
	})
}

func TestOffersThatAreNotUpgradesAreIgnored(t *testing.T) {
	s := newECDSASigner(t)
	for _, tc := range []struct {
		current, offered string
		want             Outcome
	}{
		{"1.2.0", "1.2.0", NotNewer},
		{"1.2.0", "1.1.9", NotNewer},
		{"1.2.0", "0.9.9", NotNewer},
		{"1.2.0", "1.10.0", Staged}, // numeric, not lexical
	} {
		r := newRig(t, s, tc.current, nil)
		r.offer(s, tc.offered, 100)
		res, err := r.up.CheckOnce(context.Background())
		if err != nil || res.Outcome != tc.want {
			t.Errorf("%s -> %s: %+v, %v, want %s", tc.current, tc.offered, res, err, tc.want)
		}
	}
}

func TestMalformedOfferedVersionIsRefused(t *testing.T) {
	s := newECDSASigner(t)
	for _, v := range []string{"../../etc", "1.2", "v1.2.3", "1.2.3-rc1", "1.2.3/x", ""} {
		r := newRig(t, s, "1.0.0", nil)
		r.src.Version, r.src.Percent, r.src.OfferToAll = v, 100, true
		if v == "" {
			continue
		}
		if _, err := r.up.CheckOnce(context.Background()); err == nil {
			t.Errorf("version %q accepted", v)
		}
		r.untouched(t)
	}
}

func TestNothingIsStagedWhileAnotherVersionIsOnProbation(t *testing.T) {
	s := newECDSASigner(t)
	r := newRig(t, s, "1.0.0", nil)
	r.offer(s, "1.1.0", 100)
	if res, err := r.up.CheckOnce(context.Background()); err != nil || res.Outcome != Staged {
		t.Fatal(res, err)
	}
	r.offer(s, "1.2.0", 100)
	res, err := r.up.CheckOnce(context.Background())
	if err != nil || res.Outcome != OnProbation {
		t.Fatalf("= %+v, %v", res, err)
	}
	if st, _ := r.store.Load(); st.Current != "1.1.0" {
		t.Errorf("current = %q", st.Current)
	}
	if err := r.store.Install("1.2.0", r.dir+"/missing"); !errors.Is(err, ErrPending) {
		t.Errorf("Install while pending: %v", err)
	}
}

func TestNewRefusesWhatCannotBeTrusted(t *testing.T) {
	s := newECDSASigner(t)
	ok := Config{Source: &FakeSource{}, Store: NewStore(t.TempDir()), Key: s.pub, Identity: "agent:a", Version: "1.0.0", Arch: "amd64"}
	for name, mut := range map[string]func(*Config){
		"no key":         func(c *Config) { c.Key = nil },
		"no source":      func(c *Config) { c.Source = nil },
		"no store":       func(c *Config) { c.Store = nil },
		"no identity":    func(c *Config) { c.Identity = "" },
		"dev build":      func(c *Config) { c.Version = "dev" },
		"no arch":        func(c *Config) { c.Arch = "" },
		"prerelease tag": func(c *Config) { c.Version = "1.0.0-rc1" },
	} {
		c := ok
		mut(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := New(ok); err != nil {
		t.Fatal(err)
	}
}

func TestSourceFailureIsAnErrorAndStagesNothing(t *testing.T) {
	s := newECDSASigner(t)
	r := newRig(t, s, "1.0.0", nil)
	r.offer(s, "1.1.0", 100)
	delete(r.src.Files, SigName)
	if _, err := r.up.CheckOnce(context.Background()); err == nil {
		t.Fatal("a missing signature file was not an error")
	}
	r.untouched(t)
}

func TestRunStagesThenCallsBack(t *testing.T) {
	s := newECDSASigner(t)
	r := newRig(t, s, "1.0.0", nil)
	r.offer(s, "1.1.0", 100)
	called := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go r.up.Run(ctx, time.Hour, func() { called <- struct{}{} })
	select {
	case <-called:
	case <-ctx.Done():
		t.Fatal("onStaged was not called")
	}
}

func TestRunKeepsGoingAfterAFailedCheck(t *testing.T) {
	s := newECDSASigner(t)
	r := newRig(t, s, "1.0.0", nil)
	r.offer(s, "1.1.0", 100)
	delete(r.src.Files, SigName)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.up.Run(ctx, 10*time.Millisecond, func() { t.Error("staged an unverifiable release") })
		close(done)
	}()
	for {
		r.src.mu.Lock()
		n := len(r.src.Queries)
		r.src.mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
