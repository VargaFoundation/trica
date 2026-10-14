// Command trica-agent observes the Linux host it runs on and reports its
// inventory as sdk.Batch values: one full-scope batch per run, from the source
// "agent:<hostname>".
//
// The agent is read-only. It reads /proc, /sys and a few files below --root,
// and runs, when installed, only allow-listed read-only commands (rpm,
// systemctl, smartctl, lldpctl). It writes nothing but its own --output.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/VargaFoundation/trica/internal/agent"
	"github.com/VargaFoundation/trica/internal/agent/update"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// execFn replaces the process with a staged binary; tests replace it.
var execFn = update.Exec

// errUnhealthy ends a run that stayed on probation past its health timeout.
var errUnhealthy = errors.New("no healthy collection before the health timeout")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

const usage = `trica-agent reports the inventory of the Linux host it runs on.

Each run emits one full-scope batch (source "agent:<hostname>"): host identity,
OS, kernel, CPU and memory, virtualisation, network interfaces and addresses,
listening sockets and outbound connections, installed packages, systemd
services, disks with SMART/NVMe wear, and LLDP neighbours.

It is read-only: it reads /proc, /sys and a few files, and runs only allow-listed
read-only commands (rpm, systemctl, smartctl, lldpctl) when they are installed.
Run as root to see everything; without root the root-only data (DMI serial,
other users' sockets, SMART) is skipped and said so in the batch's health.

With --state-dir the agent can run a staged version of itself (remote update,
docs/reference/agent.md): the first start of a new version must complete a
collection within --health-timeout, or the previous version is restored.

Transport is not wired yet: batches go to --output only. Sending them to the
server over mTLS, with an on-disk spool, comes in a later release.

Usage:
  trica-agent [flags]

Flags:
`

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("trica-agent", flag.ContinueOnError)
	fl.SetOutput(stderr)
	root := fl.String("root", "/", "directory to observe; any root other than / is a tree to describe, and runs no command")
	interval := fl.Duration("interval", agent.DefaultInterval, "time between runs (minimum "+agent.MinInterval.String()+")")
	once := fl.Bool("once", false, "run once and exit")
	output := fl.String("output", "-", `where batches go: "-" is stdout, one JSON object per line; a file path keeps the latest batch, rewritten atomically`)
	stateDir := fl.String("state-dir", "", "directory of staged versions and update state; empty disables running staged versions")
	healthTimeout := fl.Duration("health-timeout", 2*time.Minute, "how long a newly staged version has to complete a collection before it is rolled back")
	fl.Usage = func() {
		fmt.Fprint(stderr, usage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fl.Arg(0))
		return 2
	}
	if !*once && *interval < agent.MinInterval {
		fmt.Fprintf(stderr, "--interval %s is below the minimum %s\n", *interval, agent.MinInterval)
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	var store *update.Store
	if *stateDir != "" {
		store = update.NewStore(*stateDir)
		if os.Getenv(update.BootedEnv) == "" {
			if code, done := boot(store, *healthTimeout, args, logger); done {
				return code
			}
		}
	}
	a, err := agent.New(agent.Config{Root: *root, Version: version, Logger: logger})
	if err != nil {
		logger.Error("cannot start", "collector", agent.CollectorName, "error", err)
		return 1
	}
	defer a.Close()

	emit := emitter(*output, stdout)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if store != nil {
		emit = probation(cancel, store, version, *healthTimeout, emit, logger)
	}
	if *once {
		if err := a.Collect(ctx, emit); err != nil {
			return 1
		}
		return 0
	}
	if err := a.Run(ctx, *interval, emit); err != nil {
		logger.Error("stopped", "collector", agent.CollectorName, "error", err)
		return 1
	}
	if errors.Is(context.Cause(ctx), errUnhealthy) {
		logger.Error("stopped", "collector", agent.CollectorName, "error", errUnhealthy)
		return 1
	}
	return 0
}

// boot decides which binary runs: this one, or a staged version it replaces
// itself with. done is true when run must return code.
func boot(store *update.Store, healthTimeout time.Duration, args []string, logger *slog.Logger) (code int, done bool) {
	d, err := store.Boot(time.Now(), healthTimeout)
	if err != nil {
		logger.Error("cannot read the update state", "collector", agent.CollectorName, "error", err)
		return 1, true
	}
	if d.RolledBack {
		logger.Warn("staged version rolled back", "collector", agent.CollectorName, "reason", d.Reason, "running", d.Version)
	}
	if d.Exec == "" {
		return 0, false
	}
	// Exec returns only on failure: this binary then runs, and the staged one
	// loses a start, so a binary that cannot be executed is rolled back too.
	err = execFn(d.Exec, append([]string{d.Exec}, args...), os.Environ())
	logger.Error("cannot start the staged version", "collector", agent.CollectorName, "version", d.Version, "error", err)
	return 0, false
}

// probation wraps emit for a version that is on probation: its first emitted
// batch confirms it, and if none comes within healthTimeout the run is
// cancelled with errUnhealthy, so that the next start rolls back. A version
// that is not on probation is returned unchanged.
func probation(cancel context.CancelCauseFunc, store *update.Store, version string, healthTimeout time.Duration, emit sdk.Emitter, logger *slog.Logger) sdk.Emitter {
	st, err := store.Load()
	if err != nil || st.Pending == nil || st.Pending.Version != version {
		return emit
	}
	t := time.AfterFunc(healthTimeout, func() { cancel(errUnhealthy) })
	var once sync.Once
	return sdk.EmitterFunc(func(c context.Context, b *sdk.Batch) error {
		if err := emit.Emit(c, b); err != nil {
			return err
		}
		once.Do(func() {
			t.Stop()
			if ok, err := store.Confirm(version); err != nil {
				logger.Error("cannot confirm the new version", "collector", agent.CollectorName, "error", err)
			} else if ok {
				logger.Info("new version confirmed", "collector", agent.CollectorName, "version", version)
			}
		})
		return nil
	})
}

// emitter writes batches to stdout as JSON lines, or to a file that always
// holds the latest batch.
func emitter(output string, stdout io.Writer) sdk.Emitter {
	if output == "-" {
		enc := json.NewEncoder(stdout)
		return sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error { return enc.Encode(b) })
	}
	return sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error { return writeFileAtomic(output, b) })
}

// writeFileAtomic replaces path with the batch as one JSON line: a temporary
// file next to it (mode 0600), then a rename, so a reader never sees half a batch.
func writeFileAtomic(path string, b *sdk.Batch) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".trica-agent-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// Cleanup of the temporary file is best effort: the error that matters is the one returned.
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
