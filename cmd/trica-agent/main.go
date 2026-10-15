// Command trica-agent observes the Linux host it runs on and reports its
// inventory as sdk.Batch values: one full-scope batch per run, from the source
// "agent:<hostname>".
//
// The agent is read-only. It reads /proc, /sys and a few files below --root,
// and runs, when installed, only allow-listed read-only commands (rpm,
// systemctl, smartctl, lldpctl). It writes nothing but its own --output and its
// state directory (client identity, spool, staged versions).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/VargaFoundation/trica/internal/agent"
	"github.com/VargaFoundation/trica/internal/agent/update"
	"github.com/VargaFoundation/trica/internal/transport/uplink"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// execFn replaces the process with a staged binary; tests replace it.
var execFn = update.Exec

// flushEvery is how often the transport retries the spool and checks the
// certificate between two collections.
var flushEvery = time.Minute

// Environment variables that stand for the transport flags, so that a packaged
// unit or a pod sets them without editing the command line. There is none for
// the enrolment token itself: only a reference to a file is accepted.
const (
	envServer         = "TRICA_AGENT_SERVER"
	envCAFile         = "TRICA_AGENT_CA_FILE"
	envEnrolTokenFile = "TRICA_AGENT_ENROL_TOKEN_FILE" //nolint:gosec // G101: the name of a variable holding a file path, not a credential
)

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

With --server the batches are sent to a trica server over mutual TLS. The agent
enrols once with a one-time token (--enrol-token-file), keeps its client
certificate and an on-disk spool in --state-dir, and spools batches while the
server is unreachable. --output then only adds a local copy when given.
With --metrics-file the agent writes the state of its spool, in the Prometheus
text format, for the node exporter's textfile collector: it opens no port.
Environment: TRICA_AGENT_SERVER, TRICA_AGENT_CA_FILE, TRICA_AGENT_ENROL_TOKEN_FILE,
TRICA_AGENT_METRICS_FILE.

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
	server := fl.String("server", os.Getenv(envServer), "https://host:port of the trica transport listener; batches are sent there over mTLS (needs --ca-file and --state-dir) [$"+envServer+"]")
	caFile := fl.String("ca-file", os.Getenv(envCAFile), "PEM file of the CA certificate that signs the server certificate [$"+envCAFile+"]")
	tokenFile := fl.String("enrol-token-file", os.Getenv(envEnrolTokenFile), "file holding the enrolment token (one-time, or a class token shared by a fleet), read only while the agent has no certificate [$"+envEnrolTokenFile+"]")
	metricsFile := fl.String("metrics-file", os.Getenv(envMetricsFile), "file the transport metrics are written to, every minute, in the Prometheus text format for the node exporter's textfile collector (name it *.prom; needs --server) [$"+envMetricsFile+"]")
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

	if *metricsFile != "" && *server == "" {
		fmt.Fprintln(stderr, "--metrics-file needs --server: the metrics are those of the transport")
		return 2
	}
	if msg := checkServerFlags(*server, *caFile, *stateDir); msg != "" {
		fmt.Fprintln(stderr, msg)
		return 2
	}
	outputSet := false
	fl.Visit(func(f *flag.Flag) { outputSet = outputSet || f.Name == "output" })

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

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var emit sdk.Emitter
	var delivery deliveryStats // nil without a server: there is nothing to deliver to
	if *server == "" {
		emit = emitter(*output, stdout)
	} else {
		source, err := a.Source()
		if err != nil {
			logger.Error("cannot start the transport", "collector", agent.CollectorName, "error", err)
			return 1
		}
		client, err := uplink.NewClient(uplink.ClientConfig{
			ServerURL: *server, CAFile: *caFile, Dir: *stateDir, EnrolTokenFile: *tokenFile, Binding: source, Logger: logger,
		})
		if err != nil {
			logger.Error("cannot start the transport", "collector", agent.CollectorName, "error", err)
			return 1
		}
		var wg sync.WaitGroup
		defer func() { // runs before the cancel above: stop the flush loop, then release connections
			cancel(nil)
			wg.Wait()
			client.Close()
		}()
		if !*once {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client.Run(ctx, flushEvery)
			}()
		}
		if *metricsFile != "" {
			if *once { // after the collection, before the client is closed
				defer func() {
					if err := writeMetrics(*metricsFile, version, client.Stats(), time.Now()); err != nil {
						logger.Warn("cannot write the metrics file", "collector", agent.CollectorName, "target", *metricsFile, "error", err)
					}
				}()
			} else {
				wg.Add(1)
				go func() {
					defer wg.Done()
					metricsWriter(ctx, *metricsFile, flushEvery, client.Stats, logger)
				}()
			}
		}
		emit = client
		delivery = func() (uint64, int) { st := client.Stats(); return st.Sent, st.Spooled }
		if outputSet { // a local copy, for whoever wants to look at what was sent
			emit = both(client, emitter(*output, stdout))
		}
	}
	if store != nil {
		emit = probation(cancel, store, version, *healthTimeout, emit, delivery, logger)
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

// checkServerFlags returns what is wrong with the transport flags, or "".
func checkServerFlags(server, caFile, stateDir string) string {
	if server == "" {
		return ""
	}
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Sprintf("--server %q must be https://host:port", server)
	}
	if caFile == "" {
		return "--server needs --ca-file: the agent only trusts a server whose CA it was given"
	}
	if stateDir == "" {
		return "--server needs --state-dir: the client certificate and the spool live there"
	}
	return ""
}

// both emits to every emitter, even when one fails, and returns all the errors.
func both(emitters ...sdk.Emitter) sdk.Emitter {
	return sdk.EmitterFunc(func(ctx context.Context, b *sdk.Batch) error {
		var errs []error
		for _, e := range emitters {
			errs = append(errs, e.Emit(ctx, b))
		}
		return errors.Join(errs...)
	})
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

// deliveryStats reports the batches the server accepted so far and the number
// waiting in the spool.
type deliveryStats func() (sent uint64, spooled int)

// probation wraps emit for a version that is on probation, and returns emit
// unchanged for a version that is not.
//
// The version is confirmed by its first healthy collection: an emitted batch
// and, when the server was reachable before the update, an accepted delivery.
// An empty spool at start is that evidence (the previous version delivered all
// it collected), and then a batch that is only spooled does not count: a
// version that can no longer reach the server must not pass (#65). With a
// spool already waiting, the server was down before the update, and an emitted
// batch is enough, as without a server.
//
// A delivery can come after the emit, when the transport flushes its spool:
// at healthTimeout the version is confirmed if it is healthy by then, and
// otherwise the run is cancelled with errUnhealthy, so that the next start
// rolls back.
func probation(cancel context.CancelCauseFunc, store *update.Store, version string, healthTimeout time.Duration, emit sdk.Emitter, delivery deliveryStats, logger *slog.Logger) sdk.Emitter {
	st, err := store.Load()
	if err != nil || st.Pending == nil || st.Pending.Version != version {
		return emit
	}
	mustDeliver, sent0 := false, uint64(0)
	if delivery != nil {
		var spooled int
		sent0, spooled = delivery()
		mustDeliver = spooled == 0
	}
	var emitted atomic.Bool
	healthy := func() bool {
		if !emitted.Load() {
			return false
		}
		if !mustDeliver {
			return true
		}
		sent, _ := delivery()
		return sent > sent0
	}
	var decided sync.Once
	confirm := func() {
		decided.Do(func() {
			if ok, err := store.Confirm(version); err != nil {
				logger.Error("cannot confirm the new version", "collector", agent.CollectorName, "error", err)
			} else if ok {
				logger.Info("new version confirmed", "collector", agent.CollectorName, "version", version)
			}
		})
	}
	t := time.AfterFunc(healthTimeout, func() {
		if healthy() {
			confirm()
			return
		}
		decided.Do(func() { cancel(errUnhealthy) })
	})
	return sdk.EmitterFunc(func(c context.Context, b *sdk.Batch) error {
		if err := emit.Emit(c, b); err != nil {
			return err
		}
		emitted.Store(true)
		if healthy() {
			t.Stop()
			confirm()
		}
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
