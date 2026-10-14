// Package redfish collects the hardware inventory of a server from its BMC
// over the Redfish REST API: HPE iLO 4 and iLO 5, Dell iDRAC 8 and later.
//
// The collector only issues GET requests, authenticated with HTTP Basic on
// every one of them. It never opens a Redfish session (that is a POST) and
// never touches anything that changes state. It walks the service tree from
// /redfish/v1/ by following @odata.id links instead of hard-coding member
// ids, then falls back to the vendor extensions where the standard resources
// are missing (iLO 4 storage and firmware inventory).
package redfish

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector kind used in configuration.
const Kind = "redfish"

// version is reported in the collection health record.
const version = "0.1.0"

// DefaultLogDays is how far back the event log counters look.
const DefaultLogDays = 30

// Bounds on what one run may walk: a BMC is a small embedded server and the
// collector is a guest on it.
const (
	maxPages    = 20  // pages of one collection
	maxLogPages = 40  // pages of one event log
	maxFirmware = 300 // firmware inventory members
)

const rootPath = "/redfish/v1/"

// Options are the redfish-specific settings of a target.
type Options struct {
	// LogDays is the look-back window of the event log counters (default 30).
	LogDays int `yaml:"log_days"`
	// SkipLogs turns the event log reading off (the IML of an old iLO 4 can
	// be slow to page through).
	SkipLogs bool `yaml:"skip_logs"`
}

// Collector reads one BMC.
type Collector struct {
	target collect.Target
	opts   Options
	base   string
	now    func() time.Time
}

func init() { collect.Register(Kind, New) }

// New builds the collector of a target.
func New(t collect.Target) (sdk.Collector, error) {
	var o Options
	if err := t.DecodeOptions(&o); err != nil {
		return nil, err
	}
	switch {
	case o.LogDays == 0:
		o.LogDays = DefaultLogDays
	case o.LogDays < 0:
		return nil, fmt.Errorf("target %s: options: log_days must be positive", t.Source())
	}
	base, err := baseURL(t.Address)
	if err != nil {
		return nil, fmt.Errorf("target %s: %w", t.Source(), err)
	}
	if _, err := t.HTTPClient(); err != nil {
		return nil, err
	}
	return &Collector{target: t, opts: o, base: base, now: time.Now}, nil
}

// Kind implements sdk.Collector.
func (c *Collector) Kind() string { return Kind }

// Collect implements sdk.Collector: one run, one batch.
func (c *Collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	runCtx, cancel := collect.Context(ctx, c.target)
	defer cancel()
	b, err := c.run(runCtx)
	if err != nil {
		return emit.Emit(ctx, collect.Failed(c.target, Kind, version, started, err))
	}
	collect.Succeeded(b.batch, c.target, Kind, version, started, b.warnings)
	if len(b.errs) > 0 {
		// A sub-request failed: keep what was gathered, withdraw nothing.
		b.batch.Scope = sdk.ScopePartial
		b.batch.Health.OK = false
		b.batch.Health.Error = strings.Join(b.errs, "; ")
	}
	return emit.Emit(ctx, b.batch)
}

// run is the state of one collection.
type run struct {
	ctx      context.Context
	cl       *client
	opts     Options
	now      time.Time
	batch    *sdk.Batch
	dev      *sdk.Object
	errs     []string
	warnings []string
}

// fail records a sub-request that did not work. The run goes on.
func (r *run) fail(what string, err error) {
	r.errs = append(r.errs, what+": "+err.Error())
}

func (r *run) warn(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// run reads the service root, the system, and every section the system
// links to. An error returned here is a total failure (cannot connect,
// credentials refused, no system); a section that fails is recorded in
// run.errs and does not stop the others.
func (c *Collector) run(ctx context.Context) (*run, error) {
	pass, err := c.target.Password.Resolve()
	if err != nil {
		return nil, err
	}
	hc, err := c.target.HTTPClient()
	if err != nil {
		return nil, err
	}
	r := &run{
		ctx:   ctx,
		cl:    &client{base: c.base, http: hc, user: c.target.Username, pass: pass},
		opts:  c.opts,
		now:   c.now().UTC(),
		batch: sdk.NewBatch(c.target.Source(), sdk.ScopeFull),
	}

	var root serviceRoot
	if err := r.cl.get(ctx, rootPath, &root); err != nil {
		return nil, fmt.Errorf("service root: %w", err)
	}
	sysPath, err := r.firstMember(root.Systems, "Systems")
	if err != nil {
		return nil, err
	}
	var sys system
	if err := r.cl.get(ctx, sysPath, &sys); err != nil {
		return nil, fmt.Errorf("system: %w", err)
	}

	r.dev = r.batch.Object("dev", "device")
	r.system(sys)
	r.processors(sys)
	r.memory(sys)
	r.storage(sys)
	r.hostInterfaces(sys)
	r.chassis(root, sys)
	mgr := r.manager(root, sys)
	r.firmware(root, sys)
	r.logs(sys, mgr)

	if len(r.dev.Identity) == 0 {
		return nil, errors.New("the system exposes no serial number, UUID, host name or BMC address to identify it")
	}
	return r, nil
}

// firstMember returns the first member of the collection a link points to.
// Rack servers have one system, one manager; blades and multi-node chassis
// are not covered (see the README).
func (r *run) firstMember(l *link, what string) (string, error) {
	p := l.path()
	if p == "" {
		return "", fmt.Errorf("the service root does not advertise %s", what)
	}
	var col collection
	if err := r.cl.get(r.ctx, p, &col); err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	paths := col.paths()
	if len(paths) == 0 {
		return "", fmt.Errorf("%s: the collection is empty", what)
	}
	return paths[0], nil
}

// members lists the paths of a collection, following its paging links.
func (r *run) members(path string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for p := path; p != "" && !seen[p] && len(seen) < maxPages; {
		seen[p] = true
		var col collection
		if err := r.cl.get(r.ctx, p, &col); err != nil {
			return out, err
		}
		out = append(out, col.paths()...)
		p = strings.TrimSpace(col.Next)
	}
	return out, nil
}

// fetchAll reads every member of the collection at path. A member that fails
// is recorded and skipped; the others are kept.
func fetchAll[T any](r *run, what, path string) []T {
	paths, err := r.members(path)
	if err != nil {
		r.fail(what, err)
	}
	var out []T
	for _, p := range paths {
		var v T
		if err := r.cl.get(r.ctx, p, &v); err != nil {
			r.fail(what, err)
			continue
		}
		out = append(out, v)
	}
	return out
}
