// Package live overlays live link measurements on the topology (ADR 0041):
// throughput and link state per interface, read from a Prometheus-compatible
// query endpoint (Prometheus, VictoriaMetrics, Thanos, Mimir...) and joined to
// trica's interfaces.
//
// The overlay is optional and off unless the server configuration has a live
// block. The server alone talks to the endpoint; the browser only calls the
// trica API. The queries are built here from a fixed template and validated
// metric and label names: no client ever sends an expression. Nothing read
// here is written to the inventory: measurements are not inventory facts.
package live

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// Defaults of the live block.
const (
	DefaultRefresh    = 30 * time.Second
	DefaultTimeout    = 5 * time.Second
	DefaultRateWindow = 2 * time.Minute
	minRefresh        = 5 * time.Second
	minRateWindow     = 30 * time.Second
)

// Source kinds. Each kind but generic carries default metric and label names
// (see presets); every one of them can be overridden.
const (
	KindArista   = "arista"
	KindMikrotik = "mikrotik"
	KindPfsense  = "pfsense"
	KindNode     = "node"
	KindGeneric  = "generic"
)

// Units of the rx and tx counters.
const (
	UnitBytes = "bytes"
	UnitBits  = "bits"
)

// Config is the "live" block of the server configuration. A nil Config means
// the overlay is disabled.
type Config struct {
	// URL is the base of the Prometheus HTTP API: the query is sent to
	// URL + "/api/v1/query". A path prefix is kept (a VictoriaMetrics cluster
	// select path, a reverse proxy prefix).
	URL string `yaml:"url"`
	// Username and Password are HTTP basic authentication; the password is a
	// reference (file: or env:), never a value.
	Username string         `yaml:"username"`
	Password collect.Secret `yaml:"password"`
	// BearerToken is a reference to a bearer token, for endpoints behind a
	// token-checking proxy. Not combined with basic authentication.
	BearerToken collect.Secret `yaml:"bearer_token"`
	// Refresh is how long a measurement set is served before it is queried
	// again; every caller shares it.
	Refresh time.Duration `yaml:"refresh"`
	// Timeout bounds one query.
	Timeout time.Duration `yaml:"timeout"`
	// RateWindow is the range of rate() over the counters.
	RateWindow time.Duration `yaml:"rate_window"`
	// TLS verifies the endpoint (ca_file, insecure, min_version).
	TLS collect.TLS `yaml:"tls"`
	// Sources are the exporters to read, each joined to the inventory its
	// own way. Order matters: when two sources measure one interface, the
	// first wins.
	Sources []Source `yaml:"sources"`
}

// Source is one exporter family.
type Source struct {
	// Kind picks the defaults: arista, mikrotik, pfsense, node or generic
	// (no defaults).
	Kind string `yaml:"kind"`
	// Name identifies the source in the response and the metrics; the kind
	// when empty. Unique.
	Name    string  `yaml:"name"`
	Metrics Metrics `yaml:"metrics"`
	Labels  Labels  `yaml:"labels"`
	// Matchers restrict every query of the source to series with these
	// label values (job: arista-exporter). Equality only.
	Matchers map[string]string `yaml:"matchers"`
	// ExcludeInterfaces is a regular expression; series whose interface label
	// matches it are ignored. Empty keeps the kind's default; "-" excludes
	// nothing.
	ExcludeInterfaces string `yaml:"exclude_interfaces"`
	// Unit of the rx and tx counters: bytes (default, multiplied by 8) or
	// bits.
	Unit string `yaml:"unit"`
}

// Metrics names the series of a source. An empty name keeps the kind's
// default; "-" disables that measurement.
type Metrics struct {
	// RX and TX are counters of received and sent bytes (or bits).
	RX string `yaml:"rx"`
	TX string `yaml:"tx"`
	// Oper is a gauge, 1 when the link is up and 0 when it is down.
	Oper string `yaml:"oper"`
}

// Labels says how a series names its device and its interface.
type Labels struct {
	// Device are the labels holding the device, tried in order: an address
	// (with or without a port) or a name, matched against the device's
	// mgmt_ip, bmc_ip, primary_ip and name (case-insensitive, short or
	// fully qualified).
	Device []string `yaml:"device"`
	// Interface is the label holding the interface name.
	Interface string `yaml:"interface"`
}

// preset is the default naming of a kind.
type preset struct {
	metrics Metrics
	labels  Labels
	exclude string
}

// presets are defaults for well-known exporters; they assume nothing about a
// deployment beyond the exporter's own metric and label names.
var presets = map[string]preset{
	// arista-eos-exporter style: the scraped device in "device" (or the
	// target in "instance"), the port in "exported_device".
	KindArista: {
		metrics: Metrics{RX: "arista_port_inOctets", TX: "arista_port_outOctets", Oper: "arista_l2_up"},
		labels:  Labels{Device: []string{"device", "instance"}, Interface: "exported_device"},
	},
	// mktxp (MikroTik RouterOS).
	KindMikrotik: {
		metrics: Metrics{RX: "mktxp_interface_rx_byte_total", TX: "mktxp_interface_tx_byte_total", Oper: "mktxp_interface_running"},
		labels:  Labels{Device: []string{"routerboard_address", "routerboard_name"}, Interface: "name"},
	},
	// pfsense-exporter.
	KindPfsense: {
		metrics: Metrics{RX: "pfsense_interface_in_bytes", TX: "pfsense_interface_out_bytes", Oper: "pfsense_interface_up"},
		labels:  Labels{Device: []string{"host", "instance"}, Interface: "hwif"},
	},
	// Prometheus node-exporter. "node" is the label Kubernetes service
	// discovery commonly adds; "instance" is the scrape target.
	KindNode: {
		metrics: Metrics{RX: "node_network_receive_bytes_total", TX: "node_network_transmit_bytes_total", Oper: "node_network_up"},
		labels:  Labels{Device: []string{"node", "instance"}, Interface: "device"},
		exclude: `^(lo|veth.*|lxc.*|cilium.*)$`,
	},
	KindGeneric: {},
}

var (
	metricNameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRe  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// disabled marks a measurement or an exclusion turned off.
const disabled = "-"

// source is a validated Source with its defaults applied.
type source struct {
	name, kind   string
	rx, tx, oper string
	deviceLabels []string
	ifLabel      string
	matchers     map[string]string
	exclude      *regexp.Regexp
	scale        float64
}

// Validate checks the block and applies the defaults. It is called when the
// server configuration is loaded: a wrong live block stops the start rather
// than serving nothing.
func (c *Config) Validate() error {
	if c.URL == "" {
		return errors.New("live.url is required")
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("live.url %q: want an http or https URL", c.URL)
	}
	if u.User != nil {
		return errors.New("live.url must not carry credentials: use live.username and live.password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("live.url must not carry a query or a fragment")
	}
	if c.Password != "" && !isRef(c.Password) {
		return errors.New("live.password must be a reference (file:/path or env:NAME), not a value")
	}
	if c.BearerToken != "" && !isRef(c.BearerToken) {
		return errors.New("live.bearer_token must be a reference (file:/path or env:NAME), not a value")
	}
	if c.Username != "" && c.Password == "" {
		return errors.New("live.username needs live.password")
	}
	if c.BearerToken != "" && c.Username != "" {
		return errors.New("live: choose basic authentication or a bearer token, not both")
	}
	if c.Refresh == 0 {
		c.Refresh = DefaultRefresh
	}
	if c.Refresh < minRefresh {
		return fmt.Errorf("live.refresh %s: at least %s", c.Refresh, minRefresh)
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.RateWindow == 0 {
		c.RateWindow = DefaultRateWindow
	}
	if c.RateWindow < minRateWindow || c.RateWindow%time.Second != 0 {
		return fmt.Errorf("live.rate_window %s: a whole number of seconds, at least %s", c.RateWindow, minRateWindow)
	}
	if len(c.Sources) == 0 {
		return errors.New("live.sources: at least one source is required")
	}
	seen := map[string]bool{}
	for i := range c.Sources {
		s, err := c.Sources[i].compile()
		if err != nil {
			return fmt.Errorf("live.sources[%d]: %w", i, err)
		}
		if seen[s.name] {
			return fmt.Errorf("live.sources[%d]: the name %q is used twice", i, s.name)
		}
		seen[s.name] = true
	}
	return nil
}

func isRef(s collect.Secret) bool {
	v := string(s)
	return strings.HasPrefix(v, "file:") || strings.HasPrefix(v, "env:")
}

// compile applies the kind's defaults and checks every name that goes into
// a query.
func (s Source) compile() (*source, error) {
	p, ok := presets[s.Kind]
	if !ok {
		return nil, fmt.Errorf("kind %q: want arista, mikrotik, pfsense, node or generic", s.Kind)
	}
	out := &source{name: s.Name, kind: s.Kind, scale: 8}
	if out.name == "" {
		out.name = s.Kind
	}
	if !labelNameRe.MatchString(strings.ReplaceAll(out.name, "-", "_")) {
		return nil, fmt.Errorf("name %q: letters, digits, _ and - only", out.name)
	}
	pick := func(v, def string) string {
		switch v {
		case "":
			return def
		case disabled:
			return ""
		}
		return v
	}
	out.rx, out.tx, out.oper = pick(s.Metrics.RX, p.metrics.RX), pick(s.Metrics.TX, p.metrics.TX), pick(s.Metrics.Oper, p.metrics.Oper)
	for _, m := range []string{out.rx, out.tx, out.oper} {
		if m != "" && !metricNameRe.MatchString(m) {
			return nil, fmt.Errorf("metric %q is not a metric name", m)
		}
	}
	if out.rx == "" && out.tx == "" && out.oper == "" {
		return nil, errors.New("no metric: set metrics.rx, metrics.tx or metrics.oper")
	}
	out.deviceLabels = s.Labels.Device
	if len(out.deviceLabels) == 0 {
		out.deviceLabels = p.labels.Device
	}
	out.ifLabel = s.Labels.Interface
	if out.ifLabel == "" {
		out.ifLabel = p.labels.Interface
	}
	if len(out.deviceLabels) == 0 || out.ifLabel == "" {
		return nil, errors.New("labels.device and labels.interface are required")
	}
	for _, l := range append([]string{out.ifLabel}, out.deviceLabels...) {
		if !labelNameRe.MatchString(l) {
			return nil, fmt.Errorf("label %q is not a label name", l)
		}
	}
	for k := range s.Matchers {
		if !labelNameRe.MatchString(k) || strings.HasPrefix(k, "__") {
			return nil, fmt.Errorf("matcher %q is not a label name", k)
		}
	}
	out.matchers = s.Matchers
	ex := pick(s.ExcludeInterfaces, p.exclude)
	if ex != "" {
		re, err := regexp.Compile(ex)
		if err != nil {
			return nil, fmt.Errorf("exclude_interfaces: %w", err)
		}
		out.exclude = re
	}
	switch s.Unit {
	case "", UnitBytes:
	case UnitBits:
		out.scale = 1
	default:
		return nil, fmt.Errorf("unit %q: want bytes or bits", s.Unit)
	}
	return out, nil
}

// compiled returns the validated sources; Validate must have succeeded.
func (c *Config) compiled() ([]*source, error) {
	out := make([]*source, 0, len(c.Sources))
	for i := range c.Sources {
		s, err := c.Sources[i].compile()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
