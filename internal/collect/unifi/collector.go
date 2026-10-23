// Package unifi collects the UniFi consoles of an account and the devices
// they adopt, through the UniFi Site Manager API v1 (https://api.ui.com).
//
// It is a cloud collector: it sees what the vendor's cloud knows of a
// console (hardware, versions, WAN ports, addresses, the devices it manages
// and their state), not the console's local configuration. It only issues
// GET requests to /v1/hosts, /v1/sites and /v1/devices, authenticated by an
// API key that goes in the X-API-KEY header and nowhere else. The answers
// carry personal data; the decode structs of types.go leave it out. See
// README.md for the mapping, the key to create and what is left out.
package unifi

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
const Kind = "unifi"

// DefaultAddress is the Site Manager API, used when a target has no address.
const DefaultAddress = "https://api.ui.com"

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "UniFi Site Manager",
	Vendors:    []string{"Ubiquiti"},
	Protocol:   "UniFi Site Manager API v1: JSON over HTTPS, X-API-KEY",
	Summary:    schema.Label{EN: "UniFi consoles and the devices they manage (access points, switches, readers, cameras) through the Site Manager cloud API: models, firmware, health, WAN ports, addresses and Internet accesses.", FR: "Consoles UniFi et équipements qu'elles gèrent (bornes Wi-Fi, commutateurs, contrôle d'accès, caméras) par l'API cloud Site Manager : modèles, firmware, santé, ports WAN, adresses et accès Internet."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"device", "interface", "internet_access", "ip_address", "software"},
	ReadOnly:   "The client has one method, GET, on three endpoints (/v1/hosts, /v1/sites, /v1/devices); redirects are never followed and the key goes in one header, so it cannot leave the configured address.",
	Address:    "empty for https://api.ui.com, or the https base URL of the API.",
	Username:   collect.Credential{Use: collect.UseUnused},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "Site Manager API key"},
	Options:    Options{},
	Tested:     []string{"Site Manager API v1, UDM Pro Max UniFi OS 5.1 (recorded)", "Site Manager API v1, several consoles and pages (synthesised)"},
	Evidence:   collect.EvidenceRecorded,
	Docs:       "internal/collect/unifi/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Options are the collector-specific settings (target.options).
type Options struct {
	// Hosts restricts the run to some consoles of the account.
	Hosts []string `yaml:"hosts" doc:"Consoles to read, by host name, console name or host id (case-insensitive); empty reads every console the API key can see."`
	// Roles overrides the device role of a model.
	Roles []string `yaml:"roles" doc:"Device role overrides, one 'shortname-or-model=role' per entry (UAH-DOOR=access_control); the role is a value of the schema's device role."`
}

type collector struct {
	t     collect.Target
	opts  Options
	base  *url.URL
	hosts []string          // lower-cased options.hosts
	roles map[string]string // normalised shortname or model -> role
	now   func() time.Time  // test hook: uptime is relative to it
}

// New builds a UniFi collector for t. It checks everything that can be
// checked without a network: address, options, TLS settings.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	base, err := parseAddress(t)
	if err != nil {
		return nil, err
	}
	c := &collector{t: t, opts: opts, base: base, roles: map[string]string{}, now: time.Now}
	for _, h := range opts.Hosts {
		if strings.TrimSpace(h) == "" {
			return nil, fmt.Errorf("target %s: options.hosts: empty host name", t.Source())
		}
		c.hosts = append(c.hosts, strings.ToLower(strings.TrimSpace(h)))
	}
	roles := schema.Builtin().Types["device"].Attributes["role"].Values
	for _, e := range opts.Roles {
		k, role, ok := strings.Cut(e, "=")
		k, role = key(k), strings.TrimSpace(role)
		if !ok || k == "" {
			return nil, fmt.Errorf("target %s: options.roles: %q is not shortname-or-model=role", t.Source(), e)
		}
		if !slices.Contains(roles, role) {
			return nil, fmt.Errorf("target %s: options.roles: %q is not a device role (known: %s)", t.Source(), role, strings.Join(roles, ", "))
		}
		c.roles[k] = role
	}
	if _, err := t.HTTPClient(); err != nil {
		return nil, err
	}
	return c, nil
}

// parseAddress returns the API base URL: DefaultAddress when the target has
// none. Only https is accepted: the key travels in a header.
func parseAddress(t collect.Target) (*url.URL, error) {
	addr := strings.TrimSpace(t.Address)
	if addr == "" {
		addr = DefaultAddress
	}
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("target %s: invalid address %q", t.Source(), t.Address)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("target %s: address must be https (the API key is sent in a header), got %q", t.Source(), u.Scheme)
	}
	return &url.URL{Scheme: "https", Host: u.Host, Path: strings.TrimRight(u.Path, "/")}, nil
}

// Kind implements sdk.Collector.
func (c *collector) Kind() string { return Kind }

// Endpoints the collector reads, and nothing else.
const (
	epHosts   = "/v1/hosts"
	epSites   = "/v1/sites"
	epDevices = "/v1/devices"
)

// Collect implements sdk.Collector: one run, one batch.
func (c *collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	rctx, cancel := collect.Context(ctx, c.t)
	defer cancel()

	fail := func(err error) error {
		return emit.Emit(ctx, collect.Failed(c.t, Kind, "", started, err))
	}
	apiKey, err := c.t.Password.Resolve()
	if err != nil {
		return fail(fmt.Errorf("API key: %w", err))
	}
	if apiKey == "" {
		return fail(errors.New("API key: the secret is empty"))
	}
	hc, err := c.t.HTTPClient()
	if err != nil {
		return fail(err)
	}
	observed := c.now().UTC()
	snap, errs, warnings, err := c.gather(rctx, newClient(hc, c.base, apiKey))
	if err != nil {
		return fail(err)
	}

	b := sdk.NewBatch(c.t.Source(), sdk.ScopeFull)
	b.ObservedAt = observed
	res := c.build(b, snap)
	warnings = append(warnings, res.warnings...)
	if res.partial || len(errs) > 0 {
		b.Scope = sdk.ScopePartial
	}
	collect.Succeeded(b, c.t, Kind, res.version, started, warnings)
	if len(errs) > 0 {
		b.Health.OK = false
		b.Health.Error = "partial collection: " + strings.Join(errs, "; ")
	}
	return emit.Emit(ctx, b)
}

// gather reads the three endpoints. The returned error is a total failure:
// the hosts could not be read (the key is refused, the API is unreachable),
// and nothing else was asked. errs are failures of the other two calls, which
// leave a partial run; warnings are for the operator.
func (c *collector) gather(ctx context.Context, cl *client) (s *snapshot, errs, warnings []string, err error) {
	// The first call doubles as the reachability and credentials probe.
	hosts, err := listAll[host](ctx, cl, epHosts, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	s = &snapshot{}
	s.hosts, warnings = c.selectHosts(hosts)
	if len(s.hosts) == 0 {
		if len(c.hosts) > 0 {
			return nil, nil, nil, fmt.Errorf("options.hosts matches no console the API key can see (%d seen)", len(hosts))
		}
		warnings = append(warnings, "the API key sees no console")
	}

	if s.sites, err = listAll[site](ctx, cl, epSites, nil); err != nil {
		errs = append(errs, err.Error())
		s.sites = nil
	} else {
		s.haveSites = true
	}
	q := url.Values{}
	if len(c.hosts) > 0 {
		for _, h := range s.hosts {
			q.Add("hostIds[]", string(h.ID))
		}
	}
	if len(s.hosts) == 0 {
		return s, errs, warnings, nil
	}
	if s.groups, err = listAll[deviceGroup](ctx, cl, epDevices, q); err != nil {
		errs = append(errs, err.Error())
		s.groups = nil
	} else {
		s.haveDevices = true
	}
	return s, errs, warnings, nil
}

// selectHosts applies options.hosts, and warns about a name that matches
// nothing.
func (c *collector) selectHosts(all []host) (sel []host, warnings []string) {
	if len(c.hosts) == 0 {
		return all, nil
	}
	matched := map[string]bool{}
	for _, h := range all {
		names := []string{string(h.ID)}
		if rs := h.ReportedState; rs != nil {
			names = append(names, string(rs.Hostname), string(rs.Name))
		}
		for _, n := range names {
			n = strings.ToLower(n)
			if n != "" && slices.Contains(c.hosts, n) {
				matched[n] = true
				if !slices.ContainsFunc(sel, func(x host) bool { return x.ID == h.ID }) {
					sel = append(sel, h)
				}
			}
		}
	}
	for _, n := range c.hosts {
		if !matched[n] {
			warnings = append(warnings, fmt.Sprintf("options.hosts: %q matches no console", n))
		}
	}
	return sel, warnings
}
