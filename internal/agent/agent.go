package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// MinInterval is the shortest interval between two runs. It is what keeps the
// agent under 1 % of a core: a run takes tens of milliseconds (see
// TestFootprint), so thirty seconds leaves a wide margin.
const MinInterval = 30 * time.Second

// DefaultInterval is the interval between runs when none is configured.
const DefaultInterval = 5 * time.Minute

// Config configures an Agent. The zero value observes the machine it runs on.
type Config struct {
	// Root is the directory the probes read, default "/". Any other root is a
	// tree to describe (a test fixture, a mounted disk image): the agent then
	// runs no command and asks no netlink, since both would describe the
	// machine it runs on instead.
	Root string
	// FS replaces the file system view (tests); Root is then ignored.
	FS FS
	// Runner runs the allow-listed commands. Default: ExecRunner on the real
	// root, none on any other.
	Runner Runner
	// Addrs lists interface addresses. Default: netlink on the real root, the
	// /addrs.txt file of the tree on any other.
	Addrs AddrSource
	// Now is the clock; default time.Now.
	Now func() time.Time
	// Version is reported in Health.Version.
	Version string
	// Logger receives the run summaries; default slog.Default().
	Logger *slog.Logger
	// Domain is the routing domain (VRF, site, network) the host's addresses
	// and the remote addresses it talks to are unique in. It becomes part of
	// their identity (ADR 0036); empty is the global domain.
	Domain string
}

// Agent observes one host. It implements sdk.Collector.
type Agent struct {
	fs      FS
	closer  func() error
	run     Runner
	addrs   AddrSource
	now     func() time.Time
	version string
	log     *slog.Logger
	domain  string
}

var _ sdk.Collector = (*Agent)(nil)

// builtin is the registry the domain is stamped against: the agent only emits
// built-in types.
var builtin = sync.OnceValue(schema.Builtin)

// New builds an Agent. Close it when done.
func New(cfg Config) (*Agent, error) {
	domain, err := schema.NormalizeScope(cfg.Domain)
	if err != nil {
		return nil, fmt.Errorf("agent domain: %w", err)
	}
	a := &Agent{fs: cfg.FS, run: cfg.Runner, addrs: cfg.Addrs, now: cfg.Now, version: cfg.Version, log: cfg.Logger, domain: domain, closer: func() error { return nil }}
	onHost := cfg.FS == nil && (cfg.Root == "" || cfg.Root == "/")
	if a.fs == nil {
		root := cfg.Root
		if root == "" {
			root = "/"
		}
		r, err := NewRootFS(root)
		if err != nil {
			return nil, fmt.Errorf("agent root %q: %w", root, err)
		}
		a.fs, a.closer = r, r.Close
	}
	if a.run == nil {
		a.run = noRunner{}
		if onHost {
			a.run = ExecRunner{}
		}
	}
	if a.addrs == nil {
		a.addrs = fsAddrs{path: "/addrs.txt"}
		if onHost {
			a.addrs = NetlinkAddrs{}
		}
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.log == nil {
		a.log = slog.Default()
	}
	return a, nil
}

// Close releases the root directory.
func (a *Agent) Close() error { return a.closer() }

// Source is the source the batches of this host carry, "agent:<hostname>": the
// binding the agent asks for at enrolment (ADR 0035). It reads the hostname as
// a collection does, so the two cannot disagree.
func (a *Agent) Source() (string, error) {
	h := hostname(a.fs)
	if h == "" {
		return "", errors.New("agent: no hostname in /proc/sys/kernel/hostname or /etc/hostname")
	}
	return SourcePrefix + h, nil
}

// Kind implements sdk.Collector.
func (a *Agent) Kind() string { return CollectorName }

// Collect observes the host once and emits one full-scope batch. A probe that
// fails is a warning in the batch's Health; only the lack of any host
// identity, or a failing Emitter, makes Collect return an error.
func (a *Agent) Collect(ctx context.Context, emit sdk.Emitter) error {
	start := a.now()
	inv := observe(ctx, a.fs, a.run, a.addrs)
	b, err := inv.Batch(start, a.version, a.now().Sub(start))
	if err != nil {
		return err
	}
	if err := builtin().ScopeBatch(b, a.domain); err != nil {
		return err
	}
	if err := b.Validate(); err != nil {
		return fmt.Errorf("agent built an invalid batch: %w", err)
	}
	log := a.log.With("source", b.Source, "collector", CollectorName)
	if err := emit.Emit(ctx, b); err != nil {
		log.Error("emit failed", "error", err)
		return fmt.Errorf("emit: %w", err)
	}
	log.Info("collected", "objects", len(b.Objects), "relations", len(b.Relations),
		"failed", len(inv.Failed), "skipped", len(inv.Skipped), "duration", b.Health.Duration.String())
	for _, f := range inv.Failed {
		log.Warn("probe failed", "probe", f)
	}
	return nil
}

// Run collects immediately, then every interval until ctx ends. An interval
// below MinInterval is refused. A failed run is logged and the loop goes on:
// the next one may succeed.
func (a *Agent) Run(ctx context.Context, interval time.Duration, emit sdk.Emitter) error {
	if interval < MinInterval {
		return fmt.Errorf("interval %s is below the minimum %s", interval, MinInterval)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := a.Collect(ctx, emit); err != nil && !errors.Is(err, context.Canceled) {
			a.log.Error("run failed", "collector", CollectorName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
