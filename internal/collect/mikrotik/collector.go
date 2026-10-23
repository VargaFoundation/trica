// Package mikrotik collects switches and routers running MikroTik RouterOS
// through the RouterOS API (the binary word protocol on TCP 8728, or 8729 with
// TLS). It only sends /login and "print" commands.
package mikrotik

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector kind used in target configuration.
const Kind = "mikrotik"

// Default API ports: plain and api-ssl.
const (
	defaultPort    = "8728"
	defaultTLSPort = "8729"
)

// DefaultMaxEdgeMACs is the default of the max_edge_macs option.
const DefaultMaxEdgeMACs = 4

// Sections that the skip option can switch off. Each one is a command that
// some devices do not have (no health sensors on a CHR, no MNDP on a locked
// down router) and whose permanent failure would otherwise keep the target
// marked as unhealthy.
const (
	sectionHealth   = "health"
	sectionPackage  = "package"
	sectionNeighbor = "neighbor"
	sectionHost     = "host"
)

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "MikroTik RouterOS",
	Vendors:    []string{"MikroTik"},
	Protocol:   "RouterOS API: binary word protocol over TCP 8728, or 8729 with TLS",
	Summary:    schema.Label{EN: "MikroTik switches and routers: interfaces, bridges, VLANs, addresses, neighbours, packages and health sensors.", FR: "Commutateurs et routeurs MikroTik : interfaces, bridges, VLAN, adresses, voisins, paquets et sondes de santé."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"device", "fan", "interface", "ip_address", "psu", "sensor", "software", "vlan"},
	ReadOnly:   "Sends /login and print commands only; an allowlist refuses any other command before it reaches the wire.",
	Address:    "Host or host:port; port 8728, or 8729 when options.tls is set.",
	Username:   collect.Credential{Use: collect.UseRequired, Meaning: "Account in a read-only group"},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "Password of that account"},
	Options:    Options{},
	Tested:     []string{"RouterOS 7.8", "RouterOS 6.49"},
	Evidence:   collect.EvidenceSynthesised,
	Docs:       "internal/collect/mikrotik/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Options are the collector-specific settings of a target (`options:`).
type Options struct {
	// TLS connects to the api-ssl service (port 8729 unless the address has
	// a port). Certificate verification follows the target's tls settings.
	TLS bool `yaml:"tls" default:"false" doc:"Connect to the api-ssl service (port 8729 unless the address has a port); certificate verification follows the target tls settings."`
	// MaxEdgeMACs: learned_macs is reported only on ports with at most this
	// many addresses in the bridge host table. Default DefaultMaxEdgeMACs.
	MaxEdgeMACs *int `yaml:"max_edge_macs" default:"4" doc:"learned_macs is reported only on ports with at most this many bridge host table addresses."`
	// VLANDomain is the layer-2 domain VLAN keys are built in
	// ("<domain>:<vid>"). Default "default".
	VLANDomain string `yaml:"vlan_domain" default:"default" doc:"Layer-2 domain VLAN keys are built in (domain:vid)."`
	// Role overrides the guessed device role: "switch" or "router".
	// By default CRS/CSS models are switches and everything else a router.
	Role string `yaml:"role" enum:"switch,router" doc:"Overrides the guessed device role; by default CRS and CSS models are switches and everything else a router."`
	// Skip lists sections not to collect: health, package, neighbor, host.
	Skip []string `yaml:"skip" enum:"health,package,neighbor,host" doc:"Sections not to collect."`
}

type config struct {
	target      collect.Target
	opts        Options
	maxEdgeMACs int
	domain      string
	skip        map[string]bool
}

// Collector reads one RouterOS device.
type Collector struct{ cfg config }

// New builds the collector of a target. It validates the configuration but
// does not touch the network.
func New(t collect.Target) (sdk.Collector, error) {
	var o Options
	if err := t.DecodeOptions(&o); err != nil {
		return nil, err
	}
	cfg := config{target: t, opts: o, maxEdgeMACs: DefaultMaxEdgeMACs, domain: "default", skip: map[string]bool{}}
	if strings.TrimSpace(t.Address) == "" {
		return nil, fmt.Errorf("target %s: address is required", t.Source())
	}
	if t.Username == "" {
		return nil, fmt.Errorf("target %s: username is required", t.Source())
	}
	if o.MaxEdgeMACs != nil {
		if *o.MaxEdgeMACs < 0 {
			return nil, fmt.Errorf("target %s: options: max_edge_macs must not be negative", t.Source())
		}
		cfg.maxEdgeMACs = *o.MaxEdgeMACs
	}
	if d := strings.TrimSpace(o.VLANDomain); d != "" {
		cfg.domain = d
	}
	switch o.Role {
	case "", "switch", "router":
	default:
		return nil, fmt.Errorf("target %s: options: role must be \"switch\" or \"router\" (got %q)", t.Source(), o.Role)
	}
	for _, s := range o.Skip {
		switch s {
		case sectionHealth, sectionPackage, sectionNeighbor, sectionHost:
			cfg.skip[s] = true
		default:
			return nil, fmt.Errorf("target %s: options: unknown skip section %q (health, package, neighbor, host)", t.Source(), s)
		}
	}
	return &Collector{cfg: cfg}, nil
}

// Kind implements sdk.Collector.
func (c *Collector) Kind() string { return Kind }

// fetch is one command of a run and the section name that can switch it off.
type fetch struct {
	cmd     string
	section string
}

// The order is the order of the run. Nothing depends on it except that the
// cheap, identifying commands come first.
var fetches = []fetch{
	{cmdIdentity, ""}, {cmdRouterboard, ""}, {cmdResource, ""}, {cmdPackage, sectionPackage},
	{cmdInterface, ""}, {cmdEthernet, ""}, {cmdBonding, ""}, {cmdVLAN, ""}, {cmdBridge, ""},
	{cmdBridgePort, ""}, {cmdBridgeVLAN, ""}, {cmdBridgeHost, sectionHost},
	{cmdAddress, ""}, {cmdNeighbor, sectionNeighbor}, {cmdHealth, sectionHealth},
}

// Collect performs one run. Cannot connect or log in: a Failed batch (data
// for the health board, not an error). One command failing: the rest is kept,
// the scope is partial and the health is not OK, so nothing is withdrawn.
func (c *Collector) Collect(parent context.Context, emit sdk.Emitter) error {
	started := time.Now()
	t := c.cfg.target
	ctx, cancel := collect.Context(parent, t)
	defer cancel()

	fail := func(err error) error {
		return emit.Emit(parent, collect.Failed(t, Kind, "", started, err))
	}

	password, err := t.Password.Resolve()
	if err != nil {
		return fail(fmt.Errorf("credentials: %w", err))
	}
	address, tlsCfg, err := c.connection()
	if err != nil {
		return fail(err)
	}
	s, err := dial(ctx, address, t.Username, password, tlsCfg)
	if err != nil {
		return fail(err)
	}
	defer s.close()

	data := map[string][]row{}
	var errs []string
	for _, f := range fetches {
		if f.section != "" && c.cfg.skip[f.section] {
			continue
		}
		rows, err := s.print(ctx, f.cmd)
		if err != nil {
			errs = append(errs, err.Error())
			if !isDeviceError(err) {
				// The connection is gone (timeout, reset): the remaining
				// commands cannot succeed and would each wait for the deadline.
				errs = append(errs, "connection lost, remaining commands not sent")
				break
			}
			continue
		}
		data[f.cmd] = rows
	}

	m := newMapper(c.cfg, address, data)
	b, err := m.build()
	if err != nil {
		if len(errs) > 0 {
			err = fmt.Errorf("%w; %s", err, strings.Join(errs, "; "))
		}
		return fail(err)
	}
	collect.Succeeded(b, t, Kind, m.osVersion, started, m.warnings())
	if len(errs) > 0 {
		b.Scope = sdk.ScopePartial
		b.Health.OK = false
		b.Health.Error = strings.Join(errs, "; ")
	}
	return emit.Emit(parent, b)
}

// connection returns the host:port to dial and the TLS settings (nil for
// plain TCP). The TLS settings reuse the target's tls block (CA file, minimum
// version, insecure) through the shared HTTP client builder, so that every
// collector verifies certificates the same way.
func (c *Collector) connection() (string, *tls.Config, error) {
	port := defaultPort
	if c.cfg.opts.TLS {
		port = defaultTLSPort
	}
	address := hostPort(c.cfg.target.Address, port)
	if !c.cfg.opts.TLS {
		return address, nil, nil
	}
	hc, err := c.cfg.target.HTTPClient()
	if err != nil {
		return "", nil, err
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		return "", nil, errors.New("target TLS settings are unavailable")
	}
	return address, tr.TLSClientConfig.Clone(), nil
}

// hostPort adds the default port to an address that has none. Brackets around
// an IPv6 literal are accepted.
func hostPort(address, port string) string {
	address = strings.TrimSpace(address)
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(address, "["), "]"), port)
}
