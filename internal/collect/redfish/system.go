package redfish

import (
	"regexp"
	"strings"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// system sets what the ComputerSystem resource says about the server.
func (r *run) system(s system) {
	d := r.dev
	d.Identify("serial", strings.TrimSpace(s.SerialNumber)).
		Identify("hw_uuid", strings.TrimSpace(s.UUID)).
		Identify("name", strings.TrimSpace(s.HostName))
	d.Set("role", "server").
		Set("vendor", strings.TrimSpace(s.Manufacturer)).
		Set("model", strings.TrimSpace(s.Model)).
		Set("health", healthOf(s.Status.rollup()))
	// HPE puts the product number in SKU; Dell puts the service tag there,
	// which is not a part number.
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s.Manufacturer)), "HP") {
		d.Set("part_number", strings.TrimSpace(s.SKU))
	}
	d.Set("bios_version", first(s.BiosVersion, s.hpe().Bios.Current.VersionString))

	switch strings.ToLower(strings.TrimSpace(s.PowerState)) {
	case "on":
		d.Set("power_state", "on")
	case "off":
		d.Set("power_state", "off")
	case "":
	default:
		d.Set("power_state", "unknown")
	}
	switch strings.ToLower(strings.TrimSpace(s.IndicatorLED)) {
	case "lit":
		d.Set("location_led", "on")
	case "off":
		d.Set("location_led", "off")
	case "blinking":
		d.Set("location_led", "blinking")
	case "":
	default:
		d.Set("location_led", "unknown")
	}

	// Both spellings mean GiB: HPE iLO 4 writes TotalSystemMemoryGB for it.
	switch {
	case s.MemorySummary.TotalSystemMemoryGiB != nil:
		d.Set("memory_bytes", int64(*s.MemorySummary.TotalSystemMemoryGiB*(1<<30)))
	case s.MemorySummary.TotalSystemMemoryGB != nil:
		d.Set("memory_bytes", int64(*s.MemorySummary.TotalSystemMemoryGB*(1<<30)))
	}
	if s.ProcessorSummary.Count != nil {
		d.Set("cpu_count", *s.ProcessorSummary.Count)
	}
}

func (r *run) processors(s system) {
	p := s.Processors.path()
	if p == "" {
		return
	}
	var cpus []processor
	for _, c := range fetchAll[processor](r, "processors", p) {
		if c.Status.absent() {
			continue
		}
		if t := strings.TrimSpace(c.ProcessorType); t != "" && !strings.EqualFold(t, "CPU") {
			continue
		}
		cpus = append(cpus, c)
	}
	names, ids := make([]string, len(cpus)), make([]string, len(cpus))
	for i, c := range cpus {
		names[i], ids[i] = first(c.Socket, c.Name, c.ID), strings.TrimSpace(c.ID)
	}
	for i, name := range uniqueNames(names, ids) {
		c := cpus[i]
		o := r.batch.Object("cpu:"+name, "cpu").In("dev").Identify("name", name)
		o.Set("model", strings.TrimSpace(c.Model)).Set("health", partHealth(c.Status))
		if c.TotalCores != nil {
			o.Set("cores", *c.TotalCores)
		}
		if c.TotalThreads != nil {
			o.Set("threads", *c.TotalThreads)
		}
	}
	if _, set := r.dev.Attributes["cpu_count"]; !set && len(cpus) > 0 {
		r.dev.Set("cpu_count", len(cpus))
	}
}

func (r *run) memory(s system) {
	p := first(s.Memory.path(), s.hpe().Links.Memory.path())
	if p == "" {
		return
	}
	var dimms []memoryModule
	for _, m := range fetchAll[memoryModule](r, "memory", p) {
		size := first64(mib(m.CapacityMiB), mib(m.SizeMB))
		if m.Status.absent() || strings.EqualFold(strings.TrimSpace(m.DIMMStatus), "NotPresent") ||
			(size == nil && (zero(m.CapacityMiB) || zero(m.SizeMB))) {
			continue
		}
		dimms = append(dimms, m)
	}
	names, ids := make([]string, len(dimms)), make([]string, len(dimms))
	var total int64
	for i, m := range dimms {
		names[i], ids[i] = first(m.DeviceLocator, m.SocketLocator, m.Name, m.ID), strings.TrimSpace(m.ID)
	}
	for i, name := range uniqueNames(names, ids) {
		m := dimms[i]
		o := r.batch.Object("dimm:"+name, "memory").In("dev").
			Identify("name", name).
			Identify("serial", strings.TrimSpace(m.SerialNumber))
		o.Set("model", strings.TrimSpace(m.PartNumber)).Set("health", partHealth(m.Status))
		if tech := memoryTechnology(first(m.MemoryDeviceType, m.DIMMType)); tech != "" {
			o.Set("technology", tech)
		}
		if size := first64(mib(m.CapacityMiB), mib(m.SizeMB)); size != nil {
			o.Set("size_bytes", *size)
			total += *size
		}
		if speed := first64(m.OperatingSpeedMhz, m.MaximumFrequencyMHz); speed != nil && *speed > 0 {
			o.Set("speed_mhz", *speed)
		}
	}
	if _, set := r.dev.Attributes["memory_bytes"]; !set && total > 0 {
		r.dev.Set("memory_bytes", total)
	}
}

var memoryTechRe = regexp.MustCompile(`^(LPDDR|DDR|HBM)(\d+)`)

// memoryTechnology reduces a Redfish MemoryDeviceType (DDR4, DDR4_SDRAM,
// DDR4E_SDRAM, LPDDR5_SDRAM, HBM2...) to the generation: DDR4, LPDDR5, HBM2.
// Values that name no generation (DRAM, Other, Unknown) give "".
func memoryTechnology(s string) string {
	m := memoryTechRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

func first64(values ...*int64) *int64 {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func zero(n *int64) bool { return n != nil && *n == 0 }

// storage reads the controllers and drives: the standard Storage resource when
// the firmware has it (iLO 5, iDRAC), the HPE SmartStorage extension when it
// does not (iLO 4).
func (r *run) storage(s system) {
	if p := s.Storage.path(); p != "" && r.standardStorage(p) > 0 {
		return
	}
	if p := s.hpe().Links.SmartStorage.path(); p != "" {
		r.smartStorage(p)
	}
}

// standardStorage returns how many Storage members it found.
func (r *run) standardStorage(path string) int {
	stores := fetchAll[storage](r, "storage", path)
	for _, st := range stores {
		parent := "dev"
		if len(st.StorageControllers) > 0 {
			c := st.StorageControllers[0]
			name := first(st.ID, st.Name, c.MemberID)
			parent = r.controller("ctl:"+name, name, c)
		}
		var drives []drive
		for _, l := range st.Drives {
			var d drive
			if err := r.cl.get(r.ctx, l.path(), &d); err != nil {
				r.fail("storage drive", err)
				continue
			}
			drives = append(drives, d)
		}
		r.disks(drives, parent)
	}
	return len(stores)
}

func (r *run) smartStorage(path string) {
	var ss smartStorage
	if err := r.cl.get(r.ctx, path, &ss); err != nil {
		r.fail("smart storage", err)
		return
	}
	p := ss.Links.ArrayControllers.path()
	if p == "" {
		return
	}
	for _, ac := range fetchAll[arrayController](r, "array controllers", p) {
		name := first(string(ac.Location), ac.Name, ac.ID)
		parent := r.controller("ctl:"+name, name, ac.controller)
		var drives []drive
		if dp := ac.Links.PhysicalDrives.path(); dp != "" {
			drives = fetchAll[drive](r, "physical drives", dp)
		}
		r.disks(drives, parent)
	}
}

// controller adds a storage controller as a module of the device and returns
// its ref.
func (r *run) controller(ref, name string, c controller) string {
	o := r.batch.Object(ref, "module").In("dev").
		Identify("name", name).
		Identify("serial", strings.TrimSpace(c.SerialNumber))
	o.Set("kind", "storage_controller").
		Set("model", first(c.Model, c.Name)).
		Set("firmware_version", string(c.FirmwareVersion)).
		Set("health", partHealth(c.Status))
	return ref
}

// disks adds the drives of one controller (or of the device) under parent.
func (r *run) disks(drives []drive, parent string) {
	var present []drive
	for _, d := range drives {
		if !d.Status.absent() {
			present = append(present, d)
		}
	}
	names, ids := make([]string, len(present)), make([]string, len(present))
	for i, d := range present {
		names[i], ids[i] = first(d.location(), d.Name, d.ID), strings.TrimSpace(d.ID)
	}
	for i, name := range uniqueNames(names, ids) {
		d := present[i]
		o := r.batch.Object(parent+"/disk:"+name, "disk").In(parent).
			Identify("name", name).
			Identify("serial", strings.TrimSpace(d.SerialNumber))
		for _, id := range d.Identifiers {
			if f := strings.ToUpper(strings.TrimSpace(id.DurableNameFormat)); f == "NAA" || f == "WWN" {
				o.Identify("wwn", id.DurableName)
				break
			}
		}
		protocol := first(d.Protocol, d.InterfaceType)
		o.Set("model", strings.TrimSpace(d.Model)).
			Set("media", mediaOf(d.MediaType, protocol)).
			Set("protocol", protocol).
			Set("firmware_version", first(d.Revision, string(d.FirmwareVersion))).
			Set("slot", d.location()).
			Set("health", partHealth(d.Status))
		if c := first64(d.CapacityBytes, mib(d.CapacityMiB)); c != nil && *c > 0 {
			o.Set("capacity_bytes", *c)
		}
		if w := d.wearPct(); w != nil {
			o.Set("wear_pct", *w)
		}
	}
}

// hostInterfaces reads the network interfaces the host sees.
func (r *run) hostInterfaces(s system) {
	p := s.EthernetInterfaces.path()
	if p == "" {
		return
	}
	nics := fetchAll[ethernet](r, "host interfaces", p)
	names, ids := make([]string, len(nics)), make([]string, len(nics))
	for i, n := range nics {
		names[i], ids[i] = first(n.ID, n.Name), ""
	}
	for i, name := range uniqueNames(names, ids) {
		n := nics[i]
		o := r.batch.Object("nic:"+name, "interface").In("dev").Identify("name", name)
		r.nicAttrs(o, n)
		o.Set("kind", "physical")
		if d := strings.TrimSpace(n.Name); d != "" && d != name {
			o.Set("description", d)
		}
	}
}

// nicAttrs sets the attributes a host or BMC interface shares.
func (r *run) nicAttrs(o *sdk.Object, n ethernet) {
	o.Set("mac", strings.TrimSpace(n.MACAddress)).Set("oper_status", linkState(n.LinkStatus))
	if n.SpeedMbps != nil && *n.SpeedMbps > 0 {
		o.Set("speed_mbps", *n.SpeedMbps)
	}
	// A port without a link has no negotiated duplex; the firmware reports
	// false for it, which would read as half duplex.
	if n.FullDuplex != nil && linkState(n.LinkStatus) != "down" {
		if *n.FullDuplex {
			o.Set("duplex", "full")
		} else {
			o.Set("duplex", "half")
		}
	}
}
