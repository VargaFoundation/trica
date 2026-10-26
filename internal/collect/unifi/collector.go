// Package unifi collects the UniFi consoles of an account and the devices
// they adopt, through the UniFi Site Manager API v1 (https://api.ui.com), or
// the ports, PoE and uplinks of one console's devices through its own
// Network Integration API (options.api: network, ADR 0060).
//
// By default it is a cloud collector: it sees what the vendor's cloud knows of
// a console (hardware, versions, WAN ports, addresses, the devices it manages
// and their state), not the console's local configuration. It only issues
// GET requests to /v1/hosts, /v1/sites, /v1/devices and /v1/isp-metrics/5m,
// authenticated by an API key that goes in the X-API-KEY header and nowhere
// else. With api: network it reads, from the LAN, the console's sites and
// devices (four GET endpoint shapes, network.go). The answers carry personal
// data; the decode structs of types.go and types_network.go leave it out. See
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
	Title:      "UniFi",
	Vendors:    []string{"Ubiquiti"},
	Protocol:   "UniFi Site Manager API v1 (cloud) or a console's Network Integration API v1 (LAN): JSON over HTTPS, X-API-KEY",
	Summary:    schema.Label{EN: "UniFi consoles and the devices they manage (access points, switches, readers, cameras) through the Site Manager cloud API: models, firmware, health, WAN ports, addresses and Internet accesses, with the ISP latency, loss and uptime of the active one. With options.api network, read from the LAN instead: the ports of each device (state, speed, connector, PoE) and the uplink each device reaches the network through.", FR: "Consoles UniFi et équipements qu'elles gèrent (bornes Wi-Fi, commutateurs, contrôle d'accès, caméras) par l'API cloud Site Manager : modèles, firmware, santé, ports WAN, adresses et accès Internet, avec la latence, la perte et la disponibilité de l'opérateur sur celui qui est actif. Avec options.api network, lu depuis le réseau local : les ports de chaque équipement (état, débit, connecteur, PoE) et l'équipement par lequel chacun remonte vers le réseau."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"device", "interface", "internet_access", "ip_address", "software"},
	ReadOnly:   "The client has one method, GET. Site Manager: four endpoints (/v1/hosts, /v1/sites, /v1/devices, /v1/isp-metrics/5m bounded to the last 30 minutes). Network (api: network): four shapes (/v1/info, /v1/sites, /v1/sites/{id}/devices, /v1/sites/{id}/devices/{id}) under /proxy/network/integration, never the clients; with options.console the same four shapes under /v1/connector/consoles/{hostId}/proxy/network/integration on api.ui.com. Redirects are never followed and the key goes in one header, so it cannot leave the configured address.",
	Address:    "Site Manager: empty for https://api.ui.com, or the https base URL of the API. Network (options.api: network): required, the https address of the console, no path (https://192.0.2.1); with options.console it is empty or https://api.ui.com (the cloud connector).",
	Username:   collect.Credential{Use: collect.UseUnused},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "Site Manager API key, or the console's Network API key (created on the console)"},
	Options:    Options{},
	Tested:     []string{"Site Manager API v1, UDM Pro Max UniFi OS 5.1 (recorded)", "Site Manager API v1 ISP metrics 5m (recorded)", "Site Manager API v1, several consoles and pages (synthesised)", "UniFi Network Integration API v1, Network 10.6.106 schema (synthesised, never run against a console)"},
	Evidence:   collect.EvidenceRecorded,
	Docs:       "internal/collect/unifi/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Options are the collector-specific settings (target.options).
type Options struct {
	// API selects the API the target reads.
	API string `yaml:"api" default:"site-manager" enum:"site-manager,network" doc:"The API to read: site-manager (the cloud, https://api.ui.com) or network (one console's own Network Integration API, from the LAN, with address set): the ports, PoE and uplinks of its devices."`
	// Sites restricts an api: network run to some sites of the console.
	Sites []string `yaml:"sites" doc:"api: network only. Sites of the console to read, by name or internal reference (case-insensitive); empty reads every site."`
	// Hosts restricts the run to some consoles of the account.
	Hosts []string `yaml:"hosts" doc:"api: site-manager only. Consoles to read, by host name, console name or host id (case-insensitive); empty reads every console the API key can see."`
	// Roles overrides the device role of a model.
	Roles []string `yaml:"roles" doc:"Device role overrides, one 'shortname-or-model=role' per entry (UAH-DOOR=access_control); the role is a value of the schema's device role."`
	// ISPMetrics turns the ISP metrics call off; nil means on.
	ISPMetrics *bool `yaml:"isp_metrics" default:"true" doc:"api: site-manager only. Read each site's ISP metrics (latency, packet loss, uptime) every run and keep the newest as measurements of the active Internet access; false makes no such call."`
	// Console reads a console through the cloud connector.
	Console string `yaml:"console" doc:"api: network only. The host id of a console (the id of /v1/hosts on https://unifi.ui.com): read its Network Integration API through the cloud connector of https://api.ui.com instead of from the LAN. The key must be created by the OWNER of the console (Site Manager, API); address is empty or https://api.ui.com."`
}

// ispMetrics reports whether the ISP metrics are read (the default).
func (o Options) ispMetrics() bool { return o.ISPMetrics == nil || *o.ISPMetrics }

type collector struct {
	t       collect.Target
	opts    Options
	base    *url.URL
	api     string            // APISiteManager or APINetwork
	console string            // options.console: the host id, reached through the cloud connector
	hosts   []string          // lower-cased options.hosts
	sites   []string          // lower-cased options.sites
	roles   map[string]string // normalised shortname or model -> role
	now     func() time.Time  // test hook: uptime is relative to it
}

// New builds a UniFi collector for t. It checks everything that can be
// checked without a network: address, options, TLS settings.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	api, err := apiOf(t, opts)
	if err != nil {
		return nil, err
	}
	console := strings.TrimSpace(opts.Console)
	base, err := parseAddress(t, api, console)
	if err != nil {
		return nil, err
	}
	c := &collector{t: t, opts: opts, api: api, console: console, base: base, roles: map[string]string{}, now: time.Now}
	for _, s := range opts.Sites {
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("target %s: options.sites: empty site name", t.Source())
		}
		c.sites = append(c.sites, strings.ToLower(strings.TrimSpace(s)))
	}
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

// The values of options.api.
const (
	APISiteManager = "site-manager"
	APINetwork     = "network"
)

// apiOf returns the API a target reads and refuses the options that belong to
// the other one: an option that is silently ignored is a setting that does
// not do what it says.
func apiOf(t collect.Target, o Options) (string, error) {
	switch api := strings.TrimSpace(o.API); api {
	case "", APISiteManager:
		if strings.TrimSpace(o.Console) != "" {
			return "", fmt.Errorf("target %s: options.console applies to api: network (it reads one console through the cloud connector)", t.Source())
		}
		if len(o.Sites) > 0 {
			return "", fmt.Errorf("target %s: options.sites applies to api: network (the Site Manager API is filtered with options.hosts)", t.Source())
		}
		return APISiteManager, nil
	case APINetwork:
		if len(o.Hosts) > 0 {
			return "", fmt.Errorf("target %s: options.hosts applies to the Site Manager API; with api: network the target is one console (its address), filter its sites with options.sites", t.Source())
		}
		if o.ISPMetrics != nil {
			return "", fmt.Errorf("target %s: options.isp_metrics applies to the Site Manager API only", t.Source())
		}
		return APINetwork, nil
	default:
		return "", fmt.Errorf("target %s: options.api: %q is not site-manager or network", t.Source(), o.API)
	}
}

// parseAddress returns the API base URL. For the Site Manager it is
// DefaultAddress when the target has none; for api: network the address of
// the console is required, without a path, and the Network Integration API
// prefix is added. Only https is accepted: the key travels in a header.
func parseAddress(t collect.Target, api, console string) (*url.URL, error) {
	addr := strings.TrimSpace(t.Address)
	if addr == "" {
		if api == APINetwork && console == "" {
			return nil, fmt.Errorf("target %s: api: network needs the address of the console (https://192.0.2.1)", t.Source())
		}
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
	path := strings.TrimRight(u.Path, "/")
	if console != "" {
		// Through the connector the key goes to the cloud, never to another
		// host: a target that names another one is a mistake or a leak.
		if !strings.EqualFold(u.Host, cloudHost) || path != "" {
			return nil, fmt.Errorf("target %s: with options.console the address must be https://%s (the key is sent to the cloud connector only), got %q", t.Source(), cloudHost, t.Address)
		}
		if !consolePattern.MatchString(console) {
			return nil, fmt.Errorf("target %s: options.console %q is not a console host id (hexadecimal digits and colons, at most %d characters)", t.Source(), console, maxConsoleID)
		}
		return &url.URL{Scheme: "https", Host: u.Host, Path: fmt.Sprintf(connectorPath, url.PathEscape(console))}, nil
	}
	if api == APINetwork {
		if path != "" {
			return nil, fmt.Errorf("target %s: address %q has a path: give the console's address only, trica adds %s", t.Source(), t.Address, networkPrefix)
		}
		path = networkPrefix
	}
	return &url.URL{Scheme: "https", Host: u.Host, Path: path}, nil
}

// cloudHost is the only host a connector target may name. A variable so that
// tests can aim it at a fake; nothing else assigns it.
var cloudHost = "api.ui.com"

// Kind implements sdk.Collector.
func (c *collector) Kind() string { return Kind }

// Endpoints the collector reads, and nothing else.
const (
	epHosts   = "/v1/hosts"
	epSites   = "/v1/sites"
	epDevices = "/v1/devices"
	// epISPMetrics is read once per run, bounded to metricsWindow: for the 5m
	// metrics the API otherwise only accepts a 24 hour duration (ADR 0059).
	epISPMetrics = "/v1/isp-metrics/5m"
)

// metricsWindow is how far back the ISP metrics are asked: a few periods, so
// that a period the API publishes late is still seen.
const metricsWindow = 30 * time.Minute

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
	cl := newClient(hc, c.base, apiKey, c.api)
	if c.api == APINetwork {
		return c.collectNetwork(ctx, rctx, cl, emit, started, observed)
	}
	snap, errs, warnings, err := c.gather(rctx, cl, observed)
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

// gather reads the endpoints. The returned error is a total failure:
// the hosts could not be read (the key is refused, the API is unreachable),
// and nothing else was asked. errs are failures of the sites and devices
// calls, which leave a partial run; warnings are for the operator, and are
// all a failed ISP metrics call produces (ADR 0059).
func (c *collector) gather(ctx context.Context, cl *client, now time.Time) (s *snapshot, errs, warnings []string, err error) {
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
	if c.opts.ispMetrics() && s.haveSites {
		if w := c.gatherMetrics(ctx, cl, s, now); w != "" {
			warnings = append(warnings, w)
		}
	}
	return s, errs, warnings, nil
}

// gatherMetrics reads the ISP metrics of the last metricsWindow. It never
// fails the run: a permanently failing endpoint (the key's role, an API
// change) would otherwise make every run partial and switch off absence
// detection for every device. The warning it returns is the whole outcome.
func (c *collector) gatherMetrics(ctx context.Context, cl *client, s *snapshot, now time.Time) string {
	from := now.Add(-metricsWindow)
	metrics, err := listAll[metricSite](ctx, cl, epISPMetrics, metricsQuery(from, now))
	if err != nil {
		return "ISP metrics: " + err.Error()
	}
	s.metrics, s.haveMetrics, s.metricsFrom, s.metricsTo = metrics, true, from, now
	return ""
}

// metricsQuery is the bounded window: begin and end, RFC 3339 in UTC to the
// second, and no duration.
func metricsQuery(from, to time.Time) url.Values {
	q := url.Values{}
	q.Set("beginTimestamp", from.UTC().Format(time.RFC3339))
	q.Set("endTimestamp", to.UTC().Format(time.RFC3339))
	return q
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
