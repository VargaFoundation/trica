package unifi

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// modeFailoverOnly is the value of statistics.wanLoadBalancingMode under which
// one WAN carries the traffic and the others wait.
const modeFailoverOnly = "FAILOVER_ONLY"

// ispMetrics sets the newest ISP period of each site on the Internet access of
// the site's primary WAN (ADR 0059). The metrics are per site and describe the
// WAN that carries the traffic, so they are attributed to one WAN only, and to
// none when that cannot be told: the full batch then withdraws the series and
// the curve has a gap, instead of a number that belongs to another uplink.
func (x *builder) ispMetrics() {
	s := x.s
	if !s.haveMetrics {
		return
	}
	done := map[string]bool{}    // refs that already got a reading: the first site wins
	visited := map[string]bool{} // hosts with an Internet access that an entry was examined for
	for i := range s.metrics {
		m := &s.metrics[i]
		hid := string(m.HostID)
		if !x.hasAccess(hid) {
			continue // a host that is filtered out, blocked or without WAN: nothing to attach to
		}
		visited[hid] = true
		label := x.hostName(hid)
		st := x.siteOf(m)
		if st == nil {
			x.warn("ISP metrics of console %s: no site statistics for site %s, not attributed", label, m.SiteID)
			continue
		}
		wanType, why := primaryWAN(st)
		if wanType == "" {
			x.warn("ISP metrics of console %s not attributed: %s", label, why)
			continue
		}
		ref := x.access[hid+"\x00"+wanType]
		if ref == "" {
			x.warn("ISP metrics of console %s not attributed: the active WAN %s is not an enabled port of the console", label, wanType)
			continue
		}
		p, ok := newestPeriod(m.Periods, s.metricsFrom, s.metricsTo)
		if !ok {
			x.warnNoMetrics(label)
			continue
		}
		stat := st.Statistics.WANs[wanType]
		isp := string(firstOf(stat.ISPInfo.Name, st.Statistics.ISPInfo.Name))
		if name := string(p.Data.WAN.ISPName); name != "" && isp != "" && !strings.EqualFold(name, isp) {
			x.warn("ISP metrics of console %s not attributed: they are for provider %q, the active WAN %s is on %q", label, name, wanType, isp)
			continue
		}
		if done[ref] {
			continue
		}
		done[ref] = true
		o, found := x.b.Lookup(ref)
		if !found {
			continue
		}
		for _, k := range setMeasurements(o, p.Data.WAN) {
			x.warn("ISP metrics of console %s: %s is out of range, not stored", label, k)
		}
	}
	// A console with an Internet access that no entry of the answer was about
	// (an empty answer, or entries of other hosts only) has no reading either.
	for i := range s.hosts {
		hid := string(s.hosts[i].ID)
		if !visited[hid] && x.hasAccess(hid) {
			x.warnNoMetrics(hostLabel(&s.hosts[i]))
		}
	}
}

// warnNoMetrics warns that console label has no ISP reading in the window.
func (x *builder) warnNoMetrics(label string) {
	x.warn("no ISP metrics for console %s in the last %d minutes", label, int(metricsWindow/time.Minute))
}

// hasAccess reports whether an Internet access was made for host hid.
func (x *builder) hasAccess(hid string) bool {
	for k := range x.access {
		if strings.HasPrefix(k, hid+"\x00") {
			return true
		}
	}
	return false
}

func (x *builder) hostName(hid string) string {
	for i := range x.s.hosts {
		if string(x.s.hosts[i].ID) == hid {
			return hostLabel(&x.s.hosts[i])
		}
	}
	return hid
}

// siteOf is the /v1/sites entry of a metrics entry, matched on host and site.
func (x *builder) siteOf(m *metricSite) *site {
	for i := range x.s.sites {
		if x.s.sites[i].HostID == m.HostID && x.s.sites[i].SiteID == m.SiteID {
			return &x.s.sites[i]
		}
	}
	return nil
}

// primaryWAN returns the WAN the site's traffic uses: the one that is up with
// the lowest failover priority (1 beats 2; a missing priority sorts last, a tie
// goes by name). It returns "" and the reason when the metrics cannot be
// attributed to one WAN: none is up, or several are up under a load-balancing
// mode that mixes them.
func primaryWAN(st *site) (wanType, why string) {
	var up []string
	for name, w := range st.Statistics.WANs {
		if w.PortUp != nil && bool(*w.PortUp) {
			up = append(up, name)
		}
	}
	if len(up) == 0 {
		return "", "no WAN is up"
	}
	if mode := string(st.Statistics.WANLoadBalancingMode); len(up) > 1 && !strings.EqualFold(mode, modeFailoverOnly) {
		return "", fmt.Sprintf("%d WANs are up and the load balancing mode is %q, so the figures mix them", len(up), mode)
	}
	prio := func(name string) int {
		n, err := strconv.Atoi(string(st.Statistics.WANs[name].FailoverPriority))
		if err != nil || n < 0 {
			return 1 << 30
		}
		return n
	}
	slices.SortFunc(up, func(a, b string) int {
		if d := prio(a) - prio(b); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	return up[0], ""
}

// newestPeriod is the latest period with measurements whose time is within
// [from, to]: a period after the run's own clock is never used, even when the
// server returns it. Periods with an unreadable time are ignored.
func newestPeriod(ps []metricPeriod, from, to time.Time) (metricPeriod, bool) {
	var best metricPeriod
	var bestAt time.Time
	for _, p := range ps {
		at, err := time.Parse(time.RFC3339, string(p.MetricTime))
		if err != nil || p.Data.WAN == nil || at.Before(from) || at.After(to) {
			continue
		}
		if best.Data.WAN == nil || at.After(bestAt) {
			best, bestAt = p, at
		}
	}
	return best, best.Data.WAN != nil
}

// setMeasurements sets the readings of w as the series attributes of an
// Internet access. A value that is not a number (absent, null, text) is not
// set; one out of range (negative, or above 100 for a percentage) is not set
// either and is returned, so that the operator hears of it.
func setMeasurements(o *sdk.Object, w *ispWAN) (dropped []string) {
	for _, m := range []struct {
		key string
		v   num
		max float64 // 0: no upper bound
	}{
		{"latency_ms", w.AvgLatency, 0},
		{"latency_max_ms", w.MaxLatency, 0},
		{"loss_pct", w.PacketLoss, 100},
		{"uptime_pct", w.Uptime, 100},
	} {
		if !m.v.OK {
			continue
		}
		if m.v.V < 0 || (m.max > 0 && m.v.V > m.max) {
			dropped = append(dropped, m.key)
			continue
		}
		o.Set(m.key, m.v.V)
	}
	return dropped
}
