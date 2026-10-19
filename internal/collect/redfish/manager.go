package redfish

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// manager reads the BMC: its firmware, and its network interfaces, which
// become interfaces of kind bmc with their addresses. The first address found
// is the device's bmc_ip and mgmt_ip. It returns the manager document, nil
// when there is none.
func (r *run) manager(root serviceRoot, s system) *manager {
	var mp string
	for _, l := range s.Links.ManagedBy {
		if mp = l.path(); mp != "" {
			break
		}
	}
	if mp == "" {
		if rp := root.Managers.path(); rp != "" {
			all, err := r.members(rp)
			if err != nil {
				r.fail("managers", err)
			}
			if len(all) > 0 {
				mp = all[0]
			}
		}
	}
	if mp == "" {
		return nil
	}
	var m manager
	if err := r.cl.get(r.ctx, mp, &m); err != nil {
		r.fail("manager", err)
		return nil
	}
	r.dev.Set("bmc_version", strings.TrimSpace(m.FirmwareVersion))

	np := m.EthernetInterfaces.path()
	if np == "" {
		return &m
	}
	var nics []ethernet
	for _, n := range fetchAll[ethernet](r, "manager interfaces", np) {
		// A disabled port (the shared LAN of an iLO set to dedicated) is not
		// part of the management plane.
		if strings.TrimSpace(n.MACAddress) != "" && !strings.EqualFold(strings.TrimSpace(n.Status.State), "Disabled") {
			nics = append(nics, n)
		}
	}
	// The interface carrying the management address is "bmc"; the others
	// keep their Redfish id so that a renumbering does not rename it. The
	// order is the id's, not the firmware's, so that the choice is stable
	// when no interface has an address.
	sort.SliceStable(nics, func(i, j int) bool { return nics[i].ID < nics[j].ID })
	primary := -1
	for i, n := range nics {
		for _, a := range n.IPv4 {
			if _, ok := usableIPv4(a.Address); ok {
				primary = i
				break
			}
		}
		if primary >= 0 {
			break
		}
	}
	if primary < 0 && len(nics) > 0 {
		primary = 0
	}
	for i, n := range nics {
		name := "bmc-" + first(n.ID, n.Name, fmt.Sprint(i+1))
		if i == primary {
			name = "bmc"
		}
		ref := "bmc:" + name
		o := r.batch.Object(ref, "interface").In("dev").Identify("name", name)
		r.nicAttrs(o, n)
		o.Set("kind", "bmc")
		for _, a := range n.IPv4 {
			ip, ok := usableIPv4(a.Address)
			if !ok {
				continue
			}
			ipo := r.batch.Object("ip:"+ip, "ip_address").Identify("address", ip).Set("role", "bmc")
			if l, ok := prefixLength(a.SubnetMask); ok {
				ipo.Set("prefix_length", l)
			}
			r.batch.Relate("assigned_to", ipo.Ref, ref)
			if _, set := r.dev.Attributes["bmc_ip"]; !set {
				r.dev.Set("bmc_ip", ip).Identify("mgmt_ip", ip)
			}
		}
	}
	return &m
}

// firmware lists the firmware components twice: as name=version strings
// (firmware_inventory, for search) and as one record per component with its
// release date when the BMC or the version string gives one (firmware, for
// the table on the device's page and the age of each component). HPE iLO
// publishes the inventory under the system (iLO 4: one document keyed by
// category; iLO 5: the standard collection); Dell and the standard model put
// it under UpdateService.
func (r *run) firmware(root serviceRoot, s system) {
	p := s.hpe().Links.FirmwareInventory.path()
	if p == "" {
		if up := root.UpdateService.path(); up != "" {
			var us updateService
			if err := r.cl.get(r.ctx, up, &us); err != nil {
				r.fail("update service", err)
				return
			}
			p = us.FirmwareInventory.path()
		}
	}
	if p == "" {
		return
	}
	var inv firmwareInventory
	if err := r.cl.get(r.ctx, p, &inv); err != nil {
		r.fail("firmware inventory", err)
		return
	}
	set := map[string]bool{}
	records := map[string]map[string]any{}
	add := func(e fwEntry) {
		// Dell keeps the rollback image next to the running one.
		if strings.HasPrefix(strings.ToLower(e.ID), "previous-") || strings.EqualFold(e.Status.State, "StandbyOffline") {
			return
		}
		name, ver := strings.TrimSpace(e.Name), first(e.Version, e.VersionString)
		if name == "" || ver == "" {
			return
		}
		set[name+"="+ver] = true
		// A backup ROM is searchable, but it does not run: its age says
		// nothing about the server.
		if standbyImage(name) {
			return
		}
		rec := map[string]any{"name": name, "version": ver}
		if d := releaseDate(e.ReleaseDate, ver); d != "" {
			rec["released"] = d
		}
		if id := strings.TrimSpace(e.SoftwareID); id != "" {
			rec["software_id"] = id
		}
		if loc := first(fwLocation(e.Location), e.Oem.Hpe.DeviceContext); loc != "" {
			rec["location"] = loc
		}
		if e.Updateable != nil {
			rec["updateable"] = *e.Updateable
		}
		records[name+"="+ver] = rec
	}
	members := inv.Members
	if len(members) > maxFirmware {
		r.warn("firmware inventory has %d components, only the first %d were read", len(members), maxFirmware)
		members = members[:maxFirmware]
	}
	for _, l := range members {
		var e fwEntry
		if err := r.cl.get(r.ctx, l.path(), &e); err != nil {
			r.fail("firmware inventory", err)
			continue
		}
		add(e)
	}
	for _, raw := range inv.Current {
		var entries []fwEntry
		if json.Unmarshal(raw, &entries) == nil {
			for _, e := range entries {
				add(e)
			}
		}
	}
	if len(set) == 0 {
		return
	}
	list := make([]string, 0, len(set))
	for k := range set {
		list = append(list, k)
	}
	sort.Strings(list)
	r.dev.Set("firmware_inventory", list)
	if len(records) == 0 {
		return
	}
	table := make([]map[string]any, 0, len(records))
	for _, k := range list {
		if rec, ok := records[k]; ok {
			table = append(table, rec)
		}
	}
	r.dev.Set("firmware", table)
}

// logIDs are the hardware event logs: the iLO Integrated Management Log and
// the iDRAC System Event Log. The iLO event log (IEL) and the iDRAC
// Lifecycle log describe the controller and its management actions, not the
// hardware, and the latter is large.
var logIDs = map[string]bool{"iml": true, "sel": true}

// logs counts the critical and warning entries of the hardware event log
// within the look-back window. It creates no object per entry: an entry is an
// event, and the inventory keeps state.
func (r *run) logs(s system, mgr *manager) {
	if r.opts.SkipLogs {
		return
	}
	var collections []string
	if p := s.LogServices.path(); p != "" {
		collections = append(collections, p)
	}
	if mgr != nil {
		if p := mgr.LogServices.path(); p != "" {
			collections = append(collections, p)
		}
	}
	// A service listed under both the system and the manager is read once,
	// or its entries would be counted twice.
	var services []string
	listed := map[string]bool{}
	for _, c := range collections {
		paths, err := r.members(c)
		if err != nil {
			r.fail("log services", err)
		}
		for _, p := range paths {
			key := strings.ToLower(strings.TrimRight(normalizePath(p), "/"))
			if logIDs[path.Base(key)] && !listed[key] {
				listed[key] = true
				services = append(services, p)
			}
		}
	}

	cutoff := r.now.Add(-time.Duration(r.opts.LogDays) * 24 * time.Hour)
	var critical, warning, unreadable int
	var lastAt time.Time
	var lastMsg string
	read := 0
	for _, sp := range services {
		var svc logService
		if err := r.cl.get(r.ctx, sp, &svc); err != nil {
			r.fail("event log", err)
			continue
		}
		entries := first(svc.Entries.path(), strings.TrimRight(sp, "/")+"/Entries")
		c, w, u, at, msg, ok := r.scanLog(entries, cutoff)
		if !ok {
			continue
		}
		read++
		critical, warning, unreadable = critical+c, warning+w, unreadable+u
		if at.After(lastAt) {
			lastAt, lastMsg = at, msg
		}
	}
	if read == 0 {
		return
	}
	if unreadable > 0 {
		r.warn("%d event log entries had no readable timestamp and were not counted", unreadable)
	}
	r.dev.Set("log_window_days", r.opts.LogDays).
		Set("log_critical_count", critical).
		Set("log_warning_count", warning)
	if critical > 0 {
		r.dev.Set("log_last_critical", truncate(strings.TrimSpace(lastMsg), 500)).
			Set("log_last_critical_at", lastAt.UTC().Format(time.RFC3339))
	}
}

// scanLog pages through one event log. ok is false when a page could not be
// read: a count over half a log would mislead, so the caller drops it.
func (r *run) scanLog(entries string, cutoff time.Time) (critical, warning, unreadable int, lastAt time.Time, lastMsg string, ok bool) {
	seen := map[string]bool{}
	for p := entries; p != "" && !seen[p]; {
		if len(seen) >= maxLogPages {
			r.warn("event log %s: stopped after %d pages, the counters cover only part of the log", entries, maxLogPages)
			break
		}
		seen[p] = true
		var page logPage
		if err := r.cl.get(r.ctx, p, &page); err != nil {
			r.fail("event log", err)
			return 0, 0, 0, time.Time{}, "", false
		}
		for _, e := range page.Members {
			at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(e.Created))
			if err != nil {
				unreadable++
				continue
			}
			if at.Before(cutoff) {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(e.Severity)) {
			case "critical":
				critical++
				if at.After(lastAt) {
					lastAt, lastMsg = at, e.Message
				}
			case "warning":
				warning++
			}
		}
		p = strings.TrimSpace(page.Next)
	}
	return critical, warning, unreadable, lastAt, lastMsg, true
}
