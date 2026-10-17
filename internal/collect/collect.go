// Package collect holds what every collector shares: target configuration,
// credential references, HTTP clients and the registry that turns a target
// into a running sdk.Collector. Each collector lives in its own subpackage
// (internal/collect/arista, ...) and registers itself in an init function.
//
// Collectors are read-only by contract (AGENTS.md): nothing here, and nothing
// in a subpackage, may write to the equipment it reads.
package collect

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Secret is a reference to a credential, never the credential itself:
// "file:/run/secrets/arista-password" or "env:ARISTA_PASSWORD". The value is
// read when needed and never kept in configuration, logs or the API.
type Secret string

// ErrNoSecret is returned when a secret reference is empty.
var ErrNoSecret = errors.New("no secret reference")

// Resolve reads the referenced value. A file's trailing newline is trimmed.
func (s Secret) Resolve() (string, error) {
	ref := string(s)
	switch {
	case ref == "":
		return "", ErrNoSecret
	case strings.HasPrefix(ref, "file:"):
		b, err := os.ReadFile(strings.TrimPrefix(ref, "file:"))
		if err != nil {
			return "", fmt.Errorf("secret %s: %w", s.String(), err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		v, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("secret %s: environment variable not set", s.String())
		}
		return v, nil
	default:
		return "", fmt.Errorf("secret reference must start with file: or env: (got %q)", s.String())
	}
}

// String shows the kind of reference and its location, never a value. A
// reference written without a prefix might be a pasted password, so it is
// masked entirely.
func (s Secret) String() string {
	ref := string(s)
	if strings.HasPrefix(ref, "file:") || strings.HasPrefix(ref, "env:") {
		return ref
	}
	if ref == "" {
		return ""
	}
	return "[redacted]"
}

// TLS configures how a collector verifies the equipment it talks to. Many
// management interfaces ship self-signed certificates; Insecure must be set
// explicitly, per target, to accept them.
type TLS struct {
	CAFile   string `yaml:"ca_file" json:"ca_file,omitempty"`
	Insecure bool   `yaml:"insecure" json:"insecure,omitempty"`
	// MinVersion is "1.0", "1.1", "1.2" (default) or "1.3". Old BMC firmware
	// sometimes needs 1.0 or 1.1; the choice is visible in the configuration.
	MinVersion string `yaml:"min_version" json:"min_version,omitempty"`
}

// Target is one configured thing to collect from.
type Target struct {
	// Kind selects the collector ("arista", "redfish", ...).
	Kind string `yaml:"kind" json:"kind"`
	// Name identifies the target in the source name ("<kind>:<name>"), the
	// health board and the logs.
	Name     string        `yaml:"name" json:"name"`
	Address  string        `yaml:"address" json:"address"`
	Username string        `yaml:"username" json:"username,omitempty"`
	Password Secret        `yaml:"password" json:"password,omitempty"`
	TLS      TLS           `yaml:"tls" json:"tls,omitempty"`
	Timeout  time.Duration `yaml:"timeout" json:"timeout,omitempty"`
	Interval time.Duration `yaml:"interval" json:"interval,omitempty"`
	// Domain is the routing domain (VRF, site, network) the addresses the
	// target reports are unique in. It becomes part of their identity
	// (ADR 0036), so that overlapping private ranges stay apart. Empty is the
	// global domain.
	Domain string `yaml:"domain" json:"domain,omitempty"`
	// Options holds collector-specific settings, decoded by the collector.
	Options yaml.Node `yaml:"options" json:"-"`
}

// Source is the batch source name of the target.
func (t Target) Source() string { return t.Kind + ":" + t.Name }

// DefaultTimeout applies to a target without one.
const DefaultTimeout = 30 * time.Second

// DefaultInterval applies to a target without one. Five minutes keeps a moved
// cable well under the 15-minute acceptance criterion.
const DefaultInterval = 5 * time.Minute

// EffectiveTimeout returns Timeout or DefaultTimeout.
func (t Target) EffectiveTimeout() time.Duration {
	if t.Timeout > 0 {
		return t.Timeout
	}
	return DefaultTimeout
}

// EffectiveInterval returns Interval or DefaultInterval.
func (t Target) EffectiveInterval() time.Duration {
	if t.Interval > 0 {
		return t.Interval
	}
	return DefaultInterval
}

// DecodeOptions decodes the collector-specific options into v. Unknown keys
// are an error: a typo in a configuration must not silently do nothing.
func (t Target) DecodeOptions(v any) error {
	if t.Options.Kind == 0 {
		return nil
	}
	b, err := yaml.Marshal(&t.Options)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("target %s: options: %w", t.Source(), err)
	}
	return nil
}

// HTTPClient builds a client for the target's TLS settings and timeout.
func (t Target) HTTPClient() (*http.Client, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	switch t.TLS.MinVersion {
	case "", "1.2":
	case "1.0":
		cfg.MinVersion = tls.VersionTLS10
	case "1.1":
		cfg.MinVersion = tls.VersionTLS11
	case "1.3":
		cfg.MinVersion = tls.VersionTLS13
	default:
		return nil, fmt.Errorf("target %s: unknown tls.min_version %q", t.Source(), t.TLS.MinVersion)
	}
	if t.TLS.CAFile != "" {
		pem, err := os.ReadFile(t.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", t.Source(), err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("target %s: no certificate in %s", t.Source(), t.TLS.CAFile)
		}
		cfg.RootCAs = pool
	}
	// The operator opts in per target, in configuration; see TLS.
	cfg.InsecureSkipVerify = t.TLS.Insecure
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = cfg
	// Old BMCs (HP iLO 4) take more than the default 10 s to complete one TLS
	// handshake, and fail the run with "TLS handshake timeout"; the request
	// timeout still bounds the whole exchange.
	tr.TLSHandshakeTimeout = min(TLSHandshakeTimeout, t.EffectiveTimeout())
	// A run asks one device many things in a row: keep its connections open
	// rather than paying that handshake for every request.
	tr.MaxIdleConnsPerHost = MaxIdleConnsPerHost
	return &http.Client{Transport: tr, Timeout: t.EffectiveTimeout()}, nil
}

// TLSHandshakeTimeout bounds one TLS handshake with a device (never beyond the
// target's timeout).
const TLSHandshakeTimeout = 30 * time.Second

// MaxIdleConnsPerHost is how many idle connections to one device a client keeps.
const MaxIdleConnsPerHost = 4

// Factory builds a collector for a target.
type Factory func(t Target) (sdk.Collector, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes a collector kind available. It panics on a duplicate kind,
// which can only be a programming error.
func Register(kind string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[kind]; dup {
		panic("collect: duplicate collector kind " + kind)
	}
	factories[kind] = f
}

// Kinds lists the registered collector kinds.
func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// New builds the collector for t.
func New(t Target) (sdk.Collector, error) {
	if t.Name == "" {
		return nil, fmt.Errorf("target of kind %q has no name", t.Kind)
	}
	mu.RLock()
	f, ok := factories[t.Kind]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("target %s: unknown collector kind %q (known: %s)", t.Name, t.Kind, strings.Join(Kinds(), ", "))
	}
	return f(t)
}

// Failed is the batch a collector emits when a run fails before it could
// report anything: partial scope, so nothing is withdrawn (ADR 0005), and a
// health record so the failure shows on the collection health board.
func Failed(t Target, kind, version string, started time.Time, err error) *sdk.Batch {
	b := sdk.NewBatch(t.Source(), sdk.ScopePartial)
	b.Health = &sdk.Health{
		Target:    t.Name,
		OK:        false,
		Error:     err.Error(),
		Duration:  time.Since(started),
		Collector: kind,
		Version:   version,
	}
	return b
}

// Succeeded attaches a healthy run's record to b.
func Succeeded(b *sdk.Batch, t Target, kind, version string, started time.Time, warnings []string) *sdk.Batch {
	b.Health = &sdk.Health{
		Target:    t.Name,
		OK:        true,
		Duration:  time.Since(started),
		Items:     len(b.Objects) + len(b.Relations),
		Warnings:  warnings,
		Collector: kind,
		Version:   version,
	}
	return b
}

// Context bounds one run by the target's timeout.
func Context(ctx context.Context, t Target) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, t.EffectiveTimeout())
}
