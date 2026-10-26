package unifi

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The Network Integration API of a console (options.api: network, ADR 0060).
// It is reached from the LAN, under networkPrefix, with a key created on the
// console, and gives what the cloud does not: the ports of each device with
// their PoE, and the device each one is uplinked to. The collector reads
// nothing else of it: not the clients, the networks, the Wi-Fi or the firewall.

// networkPrefix is where a UniFi OS console serves the Network Integration API.
const networkPrefix = "/proxy/network/integration"

// connectorPath is where the cloud forwards a console's Network Integration
// API (options.console, ADR 0060 amendment); %s is the host id, escaped. The
// connector also forwards writes: the client has no method that sends one.
const connectorPath = "/v1/connector/consoles/%s/proxy/network/integration"

// maxConsoleID bounds options.console.
const maxConsoleID = 200

// consolePattern is what a host id is: hexadecimal digits and colons (UniFi's
// are the console's MAC, then a colon and a number).
var consolePattern = regexp.MustCompile(`^[0-9A-Fa-f:]{1,200}$`)

// Endpoints of the Network Integration API, relative to networkPrefix. The two
// with %s take a server-supplied id that was checked against uuidPattern and
// is escaped.
const (
	epNetInfo    = "/v1/info"
	epNetSites   = "/v1/sites"
	epNetDevices = "/v1/sites/%s/devices"
	epNetDevice  = "/v1/sites/%s/devices/%s"
)

// uuidPattern is what an id of the Network Integration API is: a UUID. An id
// that is anything else is never put in a request path (a server that answered
// "../../" must not choose what the collector asks next).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// collectNetwork is Collect for api: network: one run, one batch.
func (c *collector) collectNetwork(ctx, rctx context.Context, cl *client, emit sdk.Emitter, started, observed time.Time) error {
	snap, errs, warnings, err := c.gatherNetwork(rctx, cl)
	if err != nil {
		return emit.Emit(ctx, collect.Failed(c.t, Kind, "", started, err))
	}
	b := sdk.NewBatch(c.t.Source(), sdk.ScopeFull)
	b.ObservedAt = observed
	res := c.buildNetwork(b, snap)
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

// gatherNetwork reads the console. The returned error is a total failure: the
// console cannot be reached, the key is refused, the sites cannot be listed or
// options.sites matches none. errs are failures of one site's or device's
// calls, which leave a partial run; warnings are for the operator.
func (c *collector) gatherNetwork(ctx context.Context, cl *client) (s *netSnapshot, errs, warnings []string, err error) {
	// The first call doubles as the reachability and credentials probe.
	info, err := getJSON[netInfo](ctx, cl, epNetInfo, nil)
	if err != nil {
		if c.console != "" && kindOf(err) == errNotFound {
			err = fmt.Errorf("console %q is unknown to the cloud (check the host id in Site Manager, /v1/hosts): %w", c.console, err)
		}
		return nil, nil, nil, err
	}
	all, err := listOffset[netSite](ctx, cl, epNetSites, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	s = &netSnapshot{version: string(info.ApplicationVersion)}
	selected, warnings := c.selectSites(all)
	if len(selected) == 0 {
		if len(c.sites) > 0 {
			return nil, nil, nil, fmt.Errorf("options.sites matches no site of the console (%d seen)", len(all))
		}
		warnings = append(warnings, "the console has no site")
	}

	stop := false // a refused key or a rate limit: the next calls would fail the same way
	for _, st := range selected {
		if !uuidPattern.MatchString(string(st.ID)) {
			s.partial = true
			warnings = append(warnings, fmt.Sprintf("site %q has an id that is not a UUID: not read", st.Name))
			continue
		}
		sd := netSiteData{site: st, details: map[string]*netDeviceDetails{}}
		if stop {
			errs = append(errs, fmt.Sprintf("site %s: not read after a refused or rate-limited call", siteLabel(st)))
			s.sites = append(s.sites, sd)
			continue
		}
		devices, err := listOffset[netDeviceOverview](ctx, cl, fmt.Sprintf(epNetDevices, url.PathEscape(string(st.ID))), nil)
		if err != nil {
			errs = append(errs, fmt.Sprintf("site %s: %v", siteLabel(st), err))
			stop = stop || haltsRun(err)
			s.sites = append(s.sites, sd)
			continue
		}
		sd.devices, sd.haveDevices = devices, true
		for _, d := range devices {
			if !uuidPattern.MatchString(string(d.ID)) {
				// The device is kept from its overview; what only the detail
				// call gives (ports, uplink) is not refreshed this run.
				s.partial = true
				warnings = append(warnings, fmt.Sprintf("device %q has an id that is not a UUID: its ports and uplink are not read", d.Name))
				continue
			}
			if stop {
				errs = append(errs, fmt.Sprintf("device %s: not read after a refused or rate-limited call", d.Name))
				continue
			}
			det, err := getJSON[netDeviceDetails](ctx, cl, fmt.Sprintf(epNetDevice, url.PathEscape(string(st.ID)), url.PathEscape(string(d.ID))), nil)
			if err != nil {
				errs = append(errs, fmt.Sprintf("device %s: %v", d.Name, err))
				stop = stop || haltsRun(err)
				continue
			}
			sd.details[string(d.ID)] = &det
		}
		s.sites = append(s.sites, sd)
	}
	return s, errs, warnings, nil
}

func siteLabel(st netSite) string {
	if st.Name != "" {
		return string(st.Name)
	}
	return string(st.InternalReference)
}

// haltsRun reports whether a failed call says that the following ones would
// fail the same way: the key is refused or the console is rate limiting.
func haltsRun(err error) bool {
	k := kindOf(err)
	return k == errAuth || k == errRateLimited
}

// selectSites applies options.sites, by name or internal reference, and warns
// about an entry that matches nothing.
func (c *collector) selectSites(all []netSite) (sel []netSite, warnings []string) {
	if len(c.sites) == 0 {
		return all, nil
	}
	matched := map[string]bool{}
	for _, st := range all {
		for _, n := range []string{string(st.Name), string(st.InternalReference)} {
			n = strings.ToLower(n)
			if n != "" && slices.Contains(c.sites, n) {
				matched[n] = true
				if !slices.ContainsFunc(sel, func(x netSite) bool { return x.ID == st.ID }) {
					sel = append(sel, st)
				}
			}
		}
	}
	for _, n := range c.sites {
		if !matched[n] {
			warnings = append(warnings, fmt.Sprintf("options.sites: %q matches no site", n))
		}
	}
	return sel, warnings
}
