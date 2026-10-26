// Package synology collects a Synology NAS (DiskStation, DSM 7) through the
// DSM Web API: the device, its network interfaces and addresses, its drives,
// the installed packages and running containers, and the USB UPS it watches.
//
// Every call is a POST of a form to /webapi/entry.cgi; the account, the
// password and the session id travel in the body and never in a URL. A run
// opens a session, reads, and always closes it (client.go). The client only
// sends the calls of an allow-list (login, logout and eight read calls), so the
// collector cannot reboot, shut down or configure the NAS. DSM answers these
// APIs to an account of the administrators group only: see README.md for the
// account to create, the mapping and what is left out.
package synology

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector family and the Target.Kind value that selects it.
const Kind = "synology"

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "Synology DSM",
	Vendors:    []string{"Synology"},
	Protocol:   "DSM Web API: form POSTs to /webapi/entry.cgi over HTTPS, one session per run",
	Summary:    schema.Label{EN: "Synology NAS (DSM 7): model, serial, DSM version, health, network interfaces and addresses, every drive with its temperature, health and wear, the installed packages and running container images, and the USB UPS it watches.", FR: "NAS Synology (DSM 7) : modèle, numéro de série, version de DSM, santé, interfaces et adresses réseau, chaque disque avec sa température, sa santé et son usure, les paquets installés, les images de containers en cours d'exécution et l'onduleur USB qu'il surveille."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"device", "disk", "interface", "ip_address", "software"},
	ReadOnly:   "The client sends form POSTs to /webapi/entry.cgi and only for the calls of an allow-list: login, logout and eight read calls (info, getinfo, load_info, get, list); anything else is refused before it is sent. The credentials and the session id are in the body, never in a URL; the session is closed at the end of every run, failed or not; redirects are never followed.",
	Address:    "Required: the https address of the NAS with the DSM HTTPS port, no path (https://192.0.2.10:5001).",
	Username:   collect.Credential{Use: collect.UseRequired, Meaning: "DSM account of the administrators group, without 2-step verification, used only by trica"},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "password of that account"},
	Options:    Options{},
	Tested:     []string{"Synology DS1821+, DSM 7.4.1-90080 Update 1 (recorded)", "A DS923+-like NAS with a crashed pool, a failed drive, no UPS and no Container Manager (synthesised, never run against a NAS)"},
	Evidence:   collect.EvidenceRecorded,
	Docs:       "internal/collect/synology/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Optional parts of a run, the values of options.skip.
const (
	partStorage    = "storage"
	partNetwork    = "network"
	partPackages   = "packages"
	partUPS        = "ups"
	partContainers = "containers"
)

var parts = []string{partStorage, partNetwork, partPackages, partUPS, partContainers}

// Options are the collector-specific settings (target.options).
type Options struct {
	// Role is the device role of the NAS.
	Role string `yaml:"role" default:"nas" doc:"Device role of the NAS: a value of the schema's device role (nas, storage, server, ...)."`
	// Skip leaves optional parts of the run out.
	Skip []string `yaml:"skip" doc:"Parts of the run to leave out: storage, network, packages, ups, containers. A run that skips a part is partial: nothing it would have reported is withdrawn."`
}

type collector struct {
	t    collect.Target
	opts Options
	base *url.URL
	role string
	skip map[string]bool
}

// New builds a Synology collector for t. It checks everything that can be
// checked without a network: address, username, options, TLS settings.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	base, err := parseAddress(t)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(t.Username) == "" {
		return nil, fmt.Errorf("target %s: username is required (a DSM account of the administrators group)", t.Source())
	}
	role := strings.TrimSpace(opts.Role)
	if role == "" {
		role = "nas"
	}
	roles := schema.Builtin().Types["device"].Attributes["role"].Values
	if !slices.Contains(roles, role) {
		return nil, fmt.Errorf("target %s: options.role: %q is not a device role (known: %s)", t.Source(), role, strings.Join(roles, ", "))
	}
	skip := map[string]bool{}
	for _, s := range opts.Skip {
		s = strings.ToLower(strings.TrimSpace(s))
		if !slices.Contains(parts, s) {
			return nil, fmt.Errorf("target %s: options.skip: %q is not one of %s", t.Source(), s, strings.Join(parts, ", "))
		}
		skip[s] = true
	}
	if _, err := t.HTTPClient(); err != nil {
		return nil, err
	}
	return &collector{t: t, opts: opts, base: base, role: role, skip: skip}, nil
}

// parseAddress returns the base URL. Only https is accepted: the password
// travels in a request body.
func parseAddress(t collect.Target) (*url.URL, error) {
	addr := strings.TrimSpace(t.Address)
	if addr == "" {
		return nil, fmt.Errorf("target %s: address is required (https://192.0.2.10:5001)", t.Source())
	}
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("target %s: invalid address %q", t.Source(), t.Address)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("target %s: address must be https (the password is sent in a request body), got %q", t.Source(), u.Scheme)
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return nil, fmt.Errorf("target %s: address %q has a path: give the address and port of the NAS only", t.Source(), t.Address)
	}
	return &url.URL{Scheme: "https", Host: u.Host}, nil
}

// Kind implements sdk.Collector.
func (c *collector) Kind() string { return Kind }

// Collect implements sdk.Collector: one run, one batch.
func (c *collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	rctx, cancel := collect.Context(ctx, c.t)
	defer cancel()

	fail := func(err error) error {
		return emit.Emit(ctx, collect.Failed(c.t, Kind, "", started, err))
	}
	passwd, err := c.t.Password.Resolve()
	if err != nil {
		return fail(fmt.Errorf("password: %w", err))
	}
	if passwd == "" {
		return fail(errors.New("password: the secret is empty"))
	}
	hc, err := c.t.HTTPClient()
	if err != nil {
		return fail(err)
	}
	cl := newClient(hc, c.base, strings.TrimSpace(c.t.Username), passwd)
	snap, err := c.gather(rctx, ctx, cl)
	if err != nil {
		return fail(err)
	}

	b := sdk.NewBatch(c.t.Source(), sdk.ScopeFull)
	res := c.build(b, snap)
	warnings := append([]string(nil), snap.warnings...)
	warnings = append(warnings, res.warnings...)
	if snap.partial || len(c.skip) > 0 {
		b.Scope = sdk.ScopePartial
	}
	collect.Succeeded(b, c.t, Kind, res.version, started, warnings)
	if len(snap.errs) > 0 {
		b.Health.OK = false
		b.Health.Error = "partial collection: " + strings.Join(snap.errs, "; ")
	}
	return emit.Emit(ctx, b)
}

// snapshot holds what one run read.
type snapshot struct {
	sys systemInfo
	dsm dsmInfo

	haveNetwork bool
	netInfo     networkInfo
	hostname    string
	nics        []nic

	haveStorage bool
	storage     storageInfo

	havePackages bool
	packages     []pkg

	haveUPS bool
	ups     upsInfo

	haveContainers bool
	containers     []container

	warnings []string // reported, not a fault
	errs     []string // an optional call failed for another reason: the run is not healthy
	partial  bool     // something could not be read: nothing is withdrawn
}

// nic is one physical interface, whichever call it was read from.
type nic struct {
	ID, MAC, Addr, Mask, Status string
	MTU, Speed                  num
}

// gather logs in, reads and logs out. The returned error is a total failure:
// the login, or one of the two core calls (the system and DSM information).
// The optional calls only add warnings, or errs when they fail for a reason
// other than a missing API or a missing privilege. ctx is the run's context,
// parent the caller's, from which the logout takes its values.
func (c *collector) gather(ctx, parent context.Context, cl *client) (s *snapshot, err error) {
	if err := cl.login(ctx); err != nil {
		return nil, err
	}
	s = &snapshot{}
	defer func() {
		// The session is closed whatever happened, with its own timeout.
		if lerr := cl.logout(parent); lerr != nil && s != nil {
			s.warnings = append(s.warnings, "logout: "+lerr.Error())
		}
	}()

	if s.sys, err = get[systemInfo](ctx, cl, epSystem, nil); err != nil {
		return nil, err
	}
	if s.dsm, err = get[dsmInfo](ctx, cl, epDSM, nil); err != nil {
		return nil, err
	}

	if !c.skip[partNetwork] {
		c.gatherNetwork(ctx, cl, s)
	}
	if !c.skip[partStorage] && ctx.Err() == nil {
		s.storage, s.haveStorage = optional(s, partStorage, func() (storageInfo, error) {
			return get[storageInfo](ctx, cl, epStorage, nil)
		})
	}
	if !c.skip[partPackages] && ctx.Err() == nil {
		var pl packageList
		pl, s.havePackages = optional(s, partPackages, func() (packageList, error) {
			return get[packageList](ctx, cl, epPackages, url.Values{"additional": {`["description","status"]`}})
		})
		s.packages = pl.Packages
	}
	if !c.skip[partUPS] && ctx.Err() == nil {
		s.ups, s.haveUPS = optional(s, partUPS, func() (upsInfo, error) {
			return get[upsInfo](ctx, cl, epUPS, nil)
		})
	}
	if !c.skip[partContainers] && ctx.Err() == nil {
		var cl2 containerList
		cl2, s.haveContainers = optional(s, partContainers, func() (containerList, error) {
			return get[containerList](ctx, cl, epDocker, url.Values{"limit": {"-1"}, "offset": {"0"}, "type": {"all"}})
		})
		s.containers = cl2.Containers
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("the run ran out of time: %w", err)
	}
	return s, nil
}

// gatherNetwork reads the interfaces (with their MAC addresses) and the
// server name and gateway. When the system call lists no interface, the
// interface list stands in for it (without MAC addresses).
func (c *collector) gatherNetwork(ctx context.Context, cl *client, s *snapshot) {
	sn, ok1 := optional(s, partNetwork, func() (systemNetwork, error) {
		return get[systemNetwork](ctx, cl, epSystem, url.Values{"type": {"network"}})
	})
	s.hostname = string(sn.Hostname)
	for _, n := range sn.Nif {
		s.nics = append(s.nics, nic{string(n.ID), string(n.MAC), string(n.Addr), string(n.Mask), string(n.Status), n.MTU, n.Speed})
	}
	if ok1 && len(s.nics) == 0 && ctx.Err() == nil {
		if list, ok := optional(s, partNetwork, func() ([]ifaceEntry, error) {
			return get[[]ifaceEntry](ctx, cl, epIfaces, nil)
		}); ok {
			for _, e := range list {
				s.nics = append(s.nics, nic{ID: string(e.IfName), Addr: string(e.IP), Mask: string(e.Mask), Status: string(e.Status), Speed: e.Speed})
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	ni, ok2 := optional(s, partNetwork, func() (networkInfo, error) {
		return get[networkInfo](ctx, cl, epNetwork, nil)
	})
	s.netInfo = ni
	s.haveNetwork = ok1 || ok2
}

// optional runs one optional call. A DSM without the API, or an account
// without the privilege (codes 102 to 105), is a warning: the part is missing
// and the rest is still reported. 102 and 103 say the feature is not there, so
// the run stays complete; 104 and 105 say nothing about it, so the run is
// partial. Any other failure is a partial, unhealthy run.
func optional[T any](s *snapshot, name string, f func() (T, error)) (T, bool) {
	v, err := f()
	if err == nil {
		return v, true
	}
	var zero T
	if ae, ok := optionalFailure(err); ok {
		s.warnings = append(s.warnings, name+": "+ae.Error())
		if ae.Code == 104 || ae.Code == 105 {
			s.partial = true
		}
		return zero, false
	}
	s.errs = append(s.errs, name+": "+err.Error())
	s.partial = true
	return zero, false
}
