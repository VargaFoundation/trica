// Package arista collects Arista switches through eAPI (JSON-RPC 2.0 over
// HTTP, POST /command-api, method runCmds). It reads with "show" commands
// only: the account it uses should hold the network-operator role, which EOS
// restricts to them, and the client refuses to send anything else.
package arista

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector family name used in target configuration.
const Kind = "arista"

// version is the collector's own version, reported in its health record.
const version = "0.1.0"

// DefaultMaxEdgeMACs is the default of the max_edge_macs option.
const DefaultMaxEdgeMACs = 4

// DefaultVLANDomain is the layer-2 domain VLANs are filed under when the
// vlan_domain option is not set.
const DefaultVLANDomain = "default"

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "Arista EOS",
	Vendors:    []string{"Arista Networks"},
	Protocol:   "eAPI: JSON-RPC 2.0 over HTTP(S)",
	Summary:    schema.Label{EN: "Arista switches: interfaces, VLANs, transceivers, power and cooling, learned MACs.", FR: "Commutateurs Arista : interfaces, VLAN, transceivers, alimentation et refroidissement, MAC apprises."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"device", "fan", "interface", "ip_address", "psu", "sensor", "transceiver", "vlan"},
	ReadOnly:   "Sends show commands only, through an account limited to the network-operator role.",
	Address:    "A bare host (https), http://host or https://host[:port]; a path replaces /command-api.",
	Username:   collect.Credential{Use: collect.UseRequired, Meaning: "Account with the network-operator role"},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "Password of that account"},
	Options:    Options{},
	Tested:     []string{"EOS 4.14.2F", "EOS 4.20.7M"},
	Evidence:   collect.EvidenceSynthesised,
	Docs:       "internal/collect/arista/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Options are the collector-specific settings of a target ("options:").
type Options struct {
	// MaxEdgeMACs: learned_macs is reported only on ports with at most this
	// many entries in the MAC table (edge ports); trunks and uplinks, which
	// learn the whole network, are left out. Default 4; 0 turns it off.
	MaxEdgeMACs *int `yaml:"max_edge_macs" default:"4" doc:"learned_macs is reported only on ports with at most this many MAC table entries (edge ports); 0 turns it off."`
	// VLANDomain names the layer-2 domain the switch's VLANs belong to; VLAN
	// 100 of two switches of the same domain is one VLAN object. Default
	// "default".
	VLANDomain string `yaml:"vlan_domain" default:"default" doc:"Layer-2 domain the switch VLANs belong to: VLAN 100 of two switches of the same domain is one VLAN object."`
}

// Collector reads one Arista switch.
type Collector struct {
	target  collect.Target
	client  *client
	pass    collect.Secret
	maxEdge int
	domain  string
	// now is the clock uptime is computed against when the switch reports
	// only its boot time; tests pin it.
	now func() time.Time
}

// New builds the collector of a target. It reads no credential yet.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	maxEdge := DefaultMaxEdgeMACs
	if opts.MaxEdgeMACs != nil {
		maxEdge = *opts.MaxEdgeMACs
	}
	if maxEdge < 0 {
		return nil, fmt.Errorf("target %s: options: max_edge_macs must not be negative", t.Source())
	}
	domain := strings.TrimSpace(opts.VLANDomain)
	if domain == "" {
		domain = DefaultVLANDomain
	}
	endpoint, err := endpointURL(t.Address)
	if err != nil {
		return nil, fmt.Errorf("target %s: %w", t.Source(), err)
	}
	if t.Username == "" {
		return nil, fmt.Errorf("target %s: no username", t.Source())
	}
	hc, err := t.HTTPClient()
	if err != nil {
		return nil, err
	}
	return &Collector{
		target:  t,
		client:  &client{http: hc, endpoint: endpoint, user: t.Username},
		pass:    t.Password,
		maxEdge: maxEdge,
		domain:  domain,
		now:     time.Now,
	}, nil
}

// Kind implements sdk.Collector.
func (c *Collector) Kind() string { return Kind }

// The commands, in the order they are asked. Each group is one eAPI request;
// when a request is refused because of one command, that group is retried
// command by command.
const (
	cmdVersion       = "show version"
	cmdHostname      = "show hostname"
	cmdInventory     = "show inventory"
	cmdUptime        = "show uptime"
	cmdInterfaces    = "show interfaces"
	cmdStatus        = "show interfaces status"
	cmdIPInterface   = "show ip interface"
	cmdVLAN          = "show vlan"
	cmdPortChannel   = "show port-channel"
	cmdMLAG          = "show mlag"
	cmdLLDPLocal     = "show lldp local-info"
	cmdLLDPNeighbors = "show lldp neighbors detail"
	// cmdLLDPBrief is asked only when cmdLLDPNeighbors is not available on
	// the switch's release (EOS 4.14 has no JSON model for the detail).
	cmdLLDPBrief   = "show lldp neighbors"
	cmdMACTable    = "show mac address-table"
	cmdTemperature = "show environment temperature"
	cmdCooling     = "show environment cooling"
	cmdPower       = "show environment power"
	cmdTransceiver = "show interfaces transceiver"
)

var groups = [][]string{
	{cmdVersion, cmdHostname, cmdInventory, cmdUptime},
	{cmdInterfaces, cmdStatus, cmdIPInterface, cmdVLAN, cmdPortChannel, cmdMLAG, cmdLLDPLocal, cmdLLDPNeighbors, cmdMACTable},
	{cmdTemperature, cmdCooling, cmdPower, cmdTransceiver},
}

// fallbacks maps a command to the one asked instead when the first is not
// available on the switch's release. Only LLDP has a replacement that is a
// command of its own; the others are derived from what is already asked (see
// builder.device and builder.portChannels).
var fallbacks = map[string]string{cmdLLDPNeighbors: cmdLLDPBrief}

// Commands lists every command the collector may send, for documentation and
// tests; all of them are show commands. The fallbacks are asked only when the
// command they replace is not available.
func Commands() []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	for _, cmd := range sdk.SortedKeys(fallbacks) {
		out = append(out, fallbacks[cmd])
	}
	return out
}

// data holds what one run gathered: the raw result of each command that
// worked, the error of each that did not, and the commands the switch's
// release has no JSON model for. Those are not failures: the switch answered,
// and it will answer the same at every run.
type data struct {
	raw         map[string]json.RawMessage
	failed      map[string]error
	unavailable map[string]bool
}

// add files the outcome of one request group.
func (d *data) add(results map[string]json.RawMessage, failed map[string]error) {
	for k, v := range results {
		d.raw[k] = v
	}
	for k, err := range failed {
		if isUnconverted(err) {
			d.unavailable[k] = true
			continue
		}
		d.failed[k] = err
	}
}

// decode returns the typed result of cmd, or nil when the command failed or
// its result is not what the collector expects (recorded as a failure of that
// command, so the batch turns partial instead of reporting wrong data).
func decode[T any](d *data, cmd string) *T {
	raw, ok := d.raw[cmd]
	if !ok {
		return nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		d.failed[cmd] = fmt.Errorf("unexpected result: %w", err)
		return nil
	}
	return &v
}

// Collect implements sdk.Collector: one run, one batch.
func (c *Collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	runCtx, cancel := collect.Context(ctx, c.target)
	defer cancel()
	b, err := c.collect(runCtx, started)
	if err != nil {
		return emit.Emit(ctx, collect.Failed(c.target, Kind, version, started, err))
	}
	return emit.Emit(ctx, b)
}

func (c *Collector) collect(ctx context.Context, started time.Time) (*sdk.Batch, error) {
	pass, err := c.pass.Resolve()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	cl := *c.client
	cl.pass = pass

	d := &data{raw: map[string]json.RawMessage{}, failed: map[string]error{}, unavailable: map[string]bool{}}
	for i, g := range groups {
		results, failed, fatal := cl.runGroup(ctx, g)
		if fatal != nil {
			return nil, fatal
		}
		if i == 0 && len(results) == 0 {
			// Nothing came back from the first request: the switch is not
			// answering eAPI, and the rest would fail the same way.
			return nil, firstError(g, failed)
		}
		d.add(results, failed)
	}
	for _, cmd := range sdk.SortedKeys(fallbacks) {
		if !d.unavailable[cmd] {
			continue
		}
		results, failed, fatal := cl.runGroup(ctx, []string{fallbacks[cmd]})
		if fatal != nil {
			return nil, fatal
		}
		d.add(results, failed)
	}
	ver := decode[showVersion](d, cmdVersion)
	if ver == nil {
		return nil, fmt.Errorf("%s: %w", cmdVersion, errOf(d, cmdVersion))
	}

	b, warnings := c.build(d, ver, c.now())
	if len(d.unavailable) > 0 {
		warnings = append(warnings, fmt.Sprintf("not available on this EOS release (no JSON output): %s",
			strings.Join(sdk.SortedKeys(d.unavailable), ", ")))
	}
	collect.Succeeded(b, c.target, Kind, version, started, warnings)
	if len(d.failed) > 0 {
		b.Scope = sdk.ScopePartial
		b.Health.OK = false
		b.Health.Error = fmt.Sprintf("partial collection: %d command(s) failed (%s)", len(d.failed), strings.Join(failedList(d), "; "))
	}
	return b, nil
}

func errOf(d *data, cmd string) error {
	if err := d.failed[cmd]; err != nil {
		return err
	}
	return errors.New("no result")
}

func firstError(cmds []string, failed map[string]error) error {
	for _, cmd := range cmds {
		if err := failed[cmd]; err != nil {
			return err
		}
	}
	return errors.New("no answer from eAPI")
}

// failedList renders the failed commands and their errors, sorted.
func failedList(d *data) []string {
	out := make([]string, 0, len(d.failed))
	for _, cmd := range sdk.SortedKeys(d.failed) {
		out = append(out, fmt.Sprintf("%s: %v", cmd, d.failed[cmd]))
	}
	return out
}

// hostIP returns the host of a target address when it is an IP literal.
func hostIP(address string) string {
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	if a, err := netip.ParseAddr(u.Hostname()); err == nil {
		return a.String()
	}
	return ""
}
