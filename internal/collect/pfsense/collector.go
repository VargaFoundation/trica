// Package pfsense collects the inventory of a pfSense firewall through the
// pfSense REST API package v2 (pfrest, https://pfrest.org).
//
// The collector only issues GET requests, authenticated by an API key. It
// reports the device, its interfaces and VLANs, addresses, gateways, firewall
// and NAT rules and VPN tunnels. See README.md for the mapping, the account to
// create on the firewall and what is left out.
package pfsense

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector family and the Target.Kind value that selects it.
const Kind = "pfsense"

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "pfSense",
	Vendors:    []string{"Netgate"},
	Protocol:   "pfSense REST API package v2 (pfrest) over HTTPS",
	Summary:    schema.Label{EN: "pfSense firewalls: interfaces, VLANs, addresses, gateways, routes, firewall and NAT rules, VPN tunnels.", FR: "Pare-feu pfSense : interfaces, VLAN, adresses, passerelles, routes, règles de filtrage et de NAT, tunnels VPN."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"device", "firewall_rule", "gateway", "interface", "ip_address", "prefix", "tunnel", "vlan"},
	ReadOnly:   "The client has one method, GET; redirects are never followed, so the key cannot leave the configured address.",
	Address:    "host[:port], https only; the REST API is served by the web GUI.",
	Username:   collect.Credential{Use: collect.UseUnused},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "API key"},
	Options:    Options{},
	Tested:     []string{"pfSense 2.7.2", "pfSense 2.8.1"},
	Evidence:   collect.EvidenceSynthesised,
	Docs:       "internal/collect/pfsense/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Sections an operator may switch off with Options.Skip, typically because
// the API key's user lacks the read privilege for them.
const (
	SectionFirewallRules = "firewall_rules"
	SectionNAT           = "nat"
	SectionVPN           = "vpn"
	SectionRoutes        = "routes"
)

// Options are the collector-specific settings (target.options).
type Options struct {
	// BasePath is the API prefix; "/api/v2" unless a reverse proxy rewrites it.
	BasePath string `yaml:"base_path" default:"/api/v2" doc:"API prefix; change it only behind a reverse proxy that rewrites it."`
	// Skip lists sections not to collect: firewall_rules, nat, vpn, routes.
	Skip []string `yaml:"skip" enum:"firewall_rules,nat,vpn,routes" doc:"Sections not to collect, typically because the API key user lacks the read privilege."`
}

type collector struct {
	t    collect.Target
	opts Options
	base *url.URL
	host string // host part of the address, the management IP when it is one
}

// New builds a pfSense collector for t. It checks everything that can be
// checked without a network: address, options, TLS settings.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	base, host, err := parseAddress(t, opts)
	if err != nil {
		return nil, err
	}
	if _, err := t.HTTPClient(); err != nil {
		return nil, err
	}
	return &collector{t: t, opts: opts, base: base, host: host}, nil
}

// parseAddress turns "fw.example:8443", "192.0.2.1" or "https://fw/" into the
// API base URL. Only https is accepted: the key travels in a header.
func parseAddress(t collect.Target, opts Options) (*url.URL, string, error) {
	addr := strings.TrimSpace(t.Address)
	if addr == "" {
		return nil, "", fmt.Errorf("target %s: no address", t.Source())
	}
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil, "", fmt.Errorf("target %s: invalid address %q", t.Source(), t.Address)
	}
	if u.Scheme != "https" {
		return nil, "", fmt.Errorf("target %s: address must be https (the API key is sent in a header), got %q", t.Source(), u.Scheme)
	}
	bp := opts.BasePath
	if bp == "" {
		bp = "/api/v2"
	}
	if !strings.HasPrefix(bp, "/") {
		return nil, "", fmt.Errorf("target %s: options.base_path must start with /", t.Source())
	}
	base := &url.URL{Scheme: "https", Host: u.Host, Path: strings.TrimRight(bp, "/")}
	for _, s := range opts.Skip {
		switch s {
		case SectionFirewallRules, SectionNAT, SectionVPN, SectionRoutes:
		default:
			return nil, "", fmt.Errorf("target %s: options.skip: unknown section %q (known: %s, %s, %s, %s)",
				t.Source(), s, SectionFirewallRules, SectionNAT, SectionVPN, SectionRoutes)
		}
	}
	return base, u.Hostname(), nil
}

// Kind implements sdk.Collector.
func (c *collector) Kind() string { return Kind }

func (c *collector) skipped(section string) bool {
	for _, s := range c.opts.Skip {
		if s == section {
			return true
		}
	}
	return false
}

// Collect implements sdk.Collector: one run, one batch.
func (c *collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	ctx, cancel := collect.Context(ctx, c.t)
	defer cancel()

	fail := func(err error) error {
		return emit.Emit(ctx, collect.Failed(c.t, Kind, "", started, err))
	}
	key, err := c.t.Password.Resolve()
	if err != nil {
		return fail(fmt.Errorf("API key: %w", err))
	}
	hc, err := c.t.HTTPClient()
	if err != nil {
		return fail(err)
	}
	g := &gatherer{ctx: ctx, cl: newClient(hc, c.base, key)}
	snap, fatal := c.gather(g)
	if fatal == nil && g.ok == 0 {
		fatal = g.firstErr
	}
	if fatal != nil {
		return fail(fatal)
	}

	b := sdk.NewBatch(c.t.Source(), sdk.ScopeFull)
	osVersion, mapWarnings := c.build(b, snap)
	g.warnings = append(g.warnings, mapWarnings...)
	if g.partial {
		b.Scope = sdk.ScopePartial
	}
	collect.Succeeded(b, c.t, Kind, osVersion, started, g.warnings)
	if len(g.errs) > 0 {
		b.Health.OK = false
		b.Health.Error = "partial collection: " + strings.Join(g.errs, "; ")
	}
	return emit.Emit(ctx, b)
}

// snapshot holds what one run read.
type snapshot struct {
	version     versionInfo
	system      systemStatus
	hostname    hostnameInfo
	ifaces      []ifaceConfig
	ifStatus    []ifaceStatus
	vlans       []vlanConfig
	gateways    []gatewayConfig
	gwStatus    []gatewayStatus
	routes      []staticRoute
	filter      []rule
	portForward []rule
	outbound    []rule
	oneToOne    []rule
	ovpnServers []openvpnConfig
	ovpnClients []openvpnConfig
	ipsec       []ipsecPhase1
	ovpnSrvStat []openvpnStatus
	ovpnCliStat []openvpnStatus
	ipsecSAs    []ipsecSA
	// have records which endpoints answered, so that "no status entry" can be
	// told from "status not read".
	have map[string]bool
}

// Endpoints, relative to the base path (pfrest v2 names).
const (
	epVersion     = "system/version"
	epSystem      = "status/system"
	epHostname    = "system/hostname"
	epInterfaces  = "interfaces"
	epIfStatus    = "status/interfaces"
	epVLANs       = "interface/vlans"
	epGateways    = "routing/gateways"
	epGwStatus    = "status/gateways"
	epRoutes      = "routing/static_routes"
	epRules       = "firewall/rules"
	epPortFwd     = "firewall/nat/port_forwards"
	epOutbound    = "firewall/nat/outbound/mappings"
	epOneToOne    = "firewall/nat/one_to_one/mappings"
	epOvpnServers = "vpn/openvpn/servers"
	epOvpnClients = "vpn/openvpn/clients"
	epIPsec       = "vpn/ipsec/phase1s"
	epOvpnSrvStat = "status/openvpn/servers"
	epOvpnCliStat = "status/openvpn/clients"
	epIPsecSAs    = "status/ipsec/sas"
)

// gather reads every endpoint. The returned error is a total failure: the
// firewall is unreachable or refuses the key.
func (c *collector) gather(g *gatherer) (*snapshot, error) {
	s := &snapshot{have: map[string]bool{}}
	g.snap = s

	// The first call doubles as the reachability and credentials probe.
	g.fetch(epVersion, false, &s.version)
	if g.firstErr != nil {
		if k := kindOf(g.firstErr); k == errAuth || k == errTransport {
			return nil, g.firstErr
		}
	}
	g.fetch(epSystem, false, &s.system)
	g.fetch(epHostname, false, &s.hostname)
	g.fetch(epInterfaces, true, &s.ifaces)
	g.fetch(epIfStatus, false, &s.ifStatus)
	g.fetch(epVLANs, true, &s.vlans)
	g.fetch(epGateways, true, &s.gateways)
	g.fetch(epGwStatus, false, &s.gwStatus)
	if !c.skipped(SectionRoutes) {
		g.fetch(epRoutes, true, &s.routes)
	}
	if !c.skipped(SectionFirewallRules) {
		g.fetch(epRules, true, &s.filter)
	}
	if !c.skipped(SectionNAT) {
		g.fetch(epPortFwd, true, &s.portForward)
		g.fetch(epOutbound, true, &s.outbound)
		g.fetch(epOneToOne, true, &s.oneToOne)
	}
	if !c.skipped(SectionVPN) {
		g.fetch(epOvpnServers, true, &s.ovpnServers)
		g.fetch(epOvpnClients, true, &s.ovpnClients)
		g.fetch(epIPsec, true, &s.ipsec)
		g.fetch(epOvpnSrvStat, false, &s.ovpnSrvStat)
		g.fetch(epOvpnCliStat, false, &s.ovpnCliStat)
		g.fetch(epIPsecSAs, false, &s.ipsecSAs)
	}
	return s, nil
}

// gatherer performs the requests of a run and sorts failures into the three
// outcomes of the collector contract.
type gatherer struct {
	ctx      context.Context
	cl       *client
	snap     *snapshot
	ok       int
	firstErr error
	errs     []string // real failures: health not OK
	warnings []string // version gaps: reported, not a fault
	partial  bool     // some claim could not be refreshed: nothing is withdrawn
}

// fetch reads endpoint into dst. core says the endpoint feeds objects; a
// missing optional status endpoint only costs attributes.
//
//   - 404: the REST API version has no such endpoint. A warning; for a core
//     endpoint the run is partial (the objects cannot be claimed complete),
//     health stays OK because nothing is broken.
//   - any other failure (403 missing privilege, 5xx, malformed, timeout): the
//     run is partial and health is not OK.
func (g *gatherer) fetch(endpoint string, core bool, dst any) bool {
	data, err := g.cl.get(g.ctx, endpoint)
	if err == nil && len(data) > 0 {
		if jerr := json.Unmarshal(data, dst); jerr != nil {
			err = &requestError{Kind: errMalformed, Path: endpoint, Err: jerr}
		}
	}
	if err != nil {
		if g.firstErr == nil {
			g.firstErr = err
		}
		var re *requestError
		if errors.As(err, &re) && re.Kind == errNotFound {
			g.warnings = append(g.warnings, endpoint+": endpoint not available on this REST API version")
			if core {
				g.partial = true
			}
			return false
		}
		g.partial = true
		g.errs = append(g.errs, err.Error())
		return false
	}
	g.ok++
	g.snap.have[endpoint] = true
	return true
}
