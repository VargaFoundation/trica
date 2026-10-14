// Package update is the agent's remote update: it asks the server which version
// it should run, downloads it, verifies it, stages it beside the running one,
// and lets the next start either confirm it or throw it away (rollback).
//
// The one rule: the agent never installs code it cannot verify. An artifact is
// staged only if the SHA256SUMS file that lists it carries a signature that
// verifies with a key pinned on the host, and its SHA-256 is the listed one.
// Nothing the server says (a version, a URL, a percentage) is trusted to
// install anything by itself.
//
// The transport to the server is not part of this package: it is the Source
// interface, with FakeSource for tests. ADR-0022 explains the design.
package update

import (
	"context"
	"crypto"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// Offer is what the server wants one agent to run. The server decides, per
// agent, with InWave; the agent checks again, so a server that offers a release
// to every agent by mistake cannot push it outside its wave.
type Offer struct {
	// Version is MAJOR.MINOR.PATCH.
	Version string
	// Percent is the share of the fleet the release is rolled out to now, 0..100.
	Percent float64
}

// Query is what the agent tells the server.
type Query struct {
	// Identity is the agent's stable identity (its source, "agent:<hostname>").
	Identity string
	// Current is the version that runs.
	Current string
	OS      string
	Arch    string
}

// Source is the agent's view of the server's update service. A nil Offer with
// a nil error means: nothing to do.
type Source interface {
	// Desired returns the release this agent should run, if any.
	Desired(ctx context.Context, q Query) (*Offer, error)
	// Open returns one file of a release: the artifact (ArtifactName), SumsName
	// or SigName. The caller closes it.
	Open(ctx context.Context, version, name string) (io.ReadCloser, error)
}

// Names of the files of a release. SHA256SUMS lists the artifacts, and its
// signature is what the pinned key verifies.
const (
	SumsName = "SHA256SUMS"
	SigName  = "SHA256SUMS.sig"
)

// ArtifactName is the file name of the binary of a release.
func ArtifactName(version, goos, goarch string) string {
	return fmt.Sprintf("trica-agent_%s_%s_%s", version, goos, goarch)
}

// Limits on what is read from a Source: a server cannot fill the disk or the
// memory of the host.
const (
	maxSums     = 1 << 20
	maxSig      = 8 << 10
	maxArtifact = 128 << 20
)

// Outcome says what a check did.
type Outcome string

// The possible outcomes of CheckOnce.
const (
	NoOffer     Outcome = "no-offer"     // the server has nothing for this agent
	OutsideWave Outcome = "outside-wave" // a release exists, this agent is not in its wave yet
	NotNewer    Outcome = "not-newer"    // the offer is not newer than what runs
	KnownBad    Outcome = "known-bad"    // the offer failed on this host before
	OnProbation Outcome = "on-probation" // another version is still on probation
	Staged      Outcome = "staged"       // verified and staged: restart to run it
)

// Result is the outcome of one check and the version it concerns.
type Result struct {
	Outcome Outcome
	Version string
}

// Config configures an Updater.
type Config struct {
	Source Source
	Store  *Store
	// Key is the pinned public key (ParsePublicKey). There is no default and no
	// way to update without one.
	Key crypto.PublicKey
	// Identity is the agent's stable identity, the input of the wave hash.
	Identity string
	// Version is the running version, MAJOR.MINOR.PATCH. A development build
	// ("dev") is not managed: New refuses it.
	Version string
	OS      string // default "linux"
	Arch    string // default: the arch the binary was built for, set by the caller
	Logger  *slog.Logger
	// MaxArtifact bounds a download; default 128 MiB.
	MaxArtifact int64
}

// Updater checks for a new version and stages it.
type Updater struct {
	cfg     Config
	current [3]int
}

// New validates cfg.
func New(cfg Config) (*Updater, error) {
	if cfg.Source == nil || cfg.Store == nil || cfg.Key == nil {
		return nil, errors.New("update: a source, a store and a pinned key are required")
	}
	if cfg.Identity == "" {
		return nil, errors.New("update: an identity is required")
	}
	cur, err := parseVersion(cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("update: this build is not a release: %w", err)
	}
	if cfg.OS == "" {
		cfg.OS = "linux"
	}
	if cfg.Arch == "" {
		return nil, errors.New("update: an arch is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxArtifact == 0 {
		cfg.MaxArtifact = maxArtifact
	}
	return &Updater{cfg: cfg, current: cur}, nil
}

// CheckOnce asks the server, and stages the release if every check passes:
// newer than what runs, not known bad, no other version on probation, this
// agent inside the wave, signature of SHA256SUMS verified with the pinned key,
// SHA-256 of the artifact as listed. Any failure leaves nothing staged.
func (u *Updater) CheckOnce(ctx context.Context) (Result, error) {
	cfg := u.cfg
	st, err := cfg.Store.Load()
	if err != nil {
		return Result{}, err
	}
	offer, err := cfg.Source.Desired(ctx, Query{Identity: cfg.Identity, Current: cfg.Version, OS: cfg.OS, Arch: cfg.Arch})
	if err != nil {
		return Result{}, fmt.Errorf("ask the server: %w", err)
	}
	if offer == nil {
		return Result{Outcome: NoOffer}, nil
	}
	res := Result{Version: offer.Version}
	ver, err := parseVersion(offer.Version)
	if err != nil {
		return res, fmt.Errorf("offer: %w", err)
	}
	switch {
	case st.Pending != nil:
		res.Outcome = OnProbation
		return res, nil
	case !newer(ver, u.current):
		res.Outcome = NotNewer
		return res, nil
	case st.IsBad(offer.Version):
		res.Outcome = KnownBad
		return res, nil
	case !InWave(cfg.Identity, offer.Version, offer.Percent):
		res.Outcome = OutsideWave
		return res, nil
	}

	name := ArtifactName(offer.Version, cfg.OS, cfg.Arch)
	sums, err := readAll(ctx, cfg.Source, offer.Version, SumsName, maxSums)
	if err != nil {
		return res, err
	}
	sig, err := readAll(ctx, cfg.Source, offer.Version, SigName, maxSig)
	if err != nil {
		return res, err
	}
	if err := VerifySignature(cfg.Key, sums, sig); err != nil {
		return res, fmt.Errorf("%s: %w", SumsName, err)
	}
	listed, err := ParseSums(sums)
	if err != nil {
		return res, err
	}
	want, ok := listed[name]
	if !ok {
		return res, fmt.Errorf("%s does not list %s", SumsName, name)
	}
	tmp, err := u.download(ctx, offer.Version, name, want)
	if err != nil {
		return res, err
	}
	if err := cfg.Store.Install(offer.Version, tmp); err != nil {
		_ = os.Remove(tmp)
		return res, err
	}
	cfg.Logger.Info("update staged", "collector", "agent", "version", offer.Version, "from", cfg.Version)
	res.Outcome = Staged
	return res, nil
}

// download copies the artifact into a temporary file in the store, hashing as
// it goes, and returns its path only if the hash is the listed one.
func (u *Updater) download(ctx context.Context, version, name string, want [sha256.Size]byte) (string, error) {
	rc, err := u.cfg.Source.Open(ctx, version, name)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()
	f, err := u.cfg.Store.tempFile()
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(rc, u.cfg.MaxArtifact+1))
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err == nil && n > u.cfg.MaxArtifact {
		err = fmt.Errorf("%s is larger than %d bytes", name, u.cfg.MaxArtifact)
	}
	if err == nil && [sha256.Size]byte(h.Sum(nil)) != want {
		err = fmt.Errorf("%s: %w", name, ErrChecksum)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func readAll(ctx context.Context, src Source, version, name string, limit int64) ([]byte, error) {
	rc, err := src.Open(ctx, version, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, limit)
	}
	return b, nil
}

// Run checks every interval, and calls onStaged once a version is staged: the
// caller restarts the agent (exit, and the service manager starts it again,
// which is when Store.Boot decides what runs). A failed check is logged and the
// loop goes on.
func (u *Updater) Run(ctx context.Context, interval time.Duration, onStaged func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		res, err := u.CheckOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			u.cfg.Logger.Warn("update check failed", "collector", "agent", "error", err)
		case err == nil && res.Outcome == Staged:
			onStaged()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
