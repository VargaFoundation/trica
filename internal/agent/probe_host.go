package agent

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const dmiDir = "/sys/class/dmi/id/"

var (
	uuidRe      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	machineIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// placeholders are what firmware writes when nobody filled the field in. A
// shared placeholder must never become an identifier: two servers would merge.
var placeholders = map[string]bool{
	"": true, "none": true, "n/a": true, "na": true, "unknown": true, "not specified": true,
	"not available": true, "not applicable": true, "to be filled by o.e.m.": true,
	"to be filled by oem": true, "default string": true, "system serial number": true,
	"system product name": true, "system manufacturer": true, "serial number": true,
	"chassis serial number": true, "0": true, "123456789": true, "0123456789": true,
}

// meaningful reports whether a DMI string identifies something: not a firmware
// placeholder and not a run of identical 0 or f characters.
func meaningful(s string) bool {
	s = strings.TrimSpace(s)
	if placeholders[strings.ToLower(s)] {
		return false
	}
	t := strings.ReplaceAll(strings.ToLower(s), "-", "")
	if t == "" {
		return false
	}
	return strings.Trim(t, "0") != "" && strings.Trim(t, "f") != ""
}

// dmi reads one DMI field. Most of them are root-only (product_serial,
// product_uuid) or absent (virtual machines, ARM): that is not worth a warning,
// the field is simply not reported.
func (c *collector) dmi(field string) string {
	s := readOptional(c.fs, dmiDir+field)
	if !meaningful(s) {
		return ""
	}
	return s
}

// probeHost reads the identity of the machine: hostname, machine-id and the
// DMI fields. A host without any name is a failure: nothing can be reported.
func (c *collector) probeHost() error {
	h := &c.inv.Host
	h.Hostname = strings.ToLower(firstNonEmpty(readOptional(c.fs, "/proc/sys/kernel/hostname"), readOptional(c.fs, "/etc/hostname")))
	if id := strings.ToLower(readOptional(c.fs, "/etc/machine-id")); machineIDRe.MatchString(id) && meaningful(id) {
		h.MachineID = id
	}
	h.Vendor = c.dmi("sys_vendor")
	h.Model = c.dmi("product_name")
	h.BIOSVersion = c.dmi("bios_version")
	h.Serial = c.dmi("product_serial")
	if u := strings.ToLower(c.dmi("product_uuid")); uuidRe.MatchString(u) {
		h.UUID = u
	}
	if h.Hostname == "" {
		return errors.New("no hostname in /proc/sys/kernel/hostname or /etc/hostname")
	}
	return nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// parseKV parses KEY=VALUE lines with optional single or double quotes
// (os-release(5)).
func parseKV(data string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		m[strings.TrimSpace(k)] = v
	}
	return m
}

// probeOS reads /etc/os-release (or /usr/lib/os-release) and the kernel release.
func (c *collector) probeOS() error {
	h := &c.inv.Host
	h.Kernel = readOptional(c.fs, "/proc/sys/kernel/osrelease")
	b, err := readFile(c.fs, "/etc/os-release")
	if err != nil {
		b, err = readFile(c.fs, "/usr/lib/os-release")
	}
	if err != nil {
		return skipf("no os-release file")
	}
	kv := parseKV(string(b))
	h.OSName = firstNonEmpty(kv["NAME"], kv["PRETTY_NAME"], kv["ID"])
	h.OSVersion = firstNonEmpty(kv["VERSION_ID"], kv["BUILD_ID"])
	return nil
}

// probeCPU counts sockets, cores and threads from /proc/cpuinfo. Fields that
// only some architectures have (physical id, cpu cores) default sensibly: one
// socket whose cores equal its threads.
func (c *collector) probeCPU() error {
	r, err := c.fs.Open("/proc/cpuinfo")
	if err != nil {
		if skippable(err) {
			return skipf("/proc/cpuinfo not readable")
		}
		return err
	}
	defer r.Close()
	type blk struct{ model, phys, cores string }
	sockets := map[string]*CPUSocket{}
	cur := blk{}
	threads := 0
	flush := func(seen bool) {
		if !seen {
			return
		}
		id := cur.phys
		if id == "" {
			id = "0"
		}
		s := sockets[id]
		if s == nil {
			s = &CPUSocket{ID: id, Model: cur.model}
			if n, err := strconv.Atoi(cur.cores); err == nil && n > 0 {
				s.Cores = n
			}
			sockets[id] = s
		}
		s.Threads++
		threads++
		cur = blk{}
	}
	sc := bufio.NewScanner(r)
	seen := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush(seen)
			seen = false
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "processor":
			seen = true
		case "model name":
			cur.model = v
		case "physical id":
			cur.phys = v
		case "cpu cores":
			cur.cores = v
		}
	}
	flush(seen)
	if err := sc.Err(); err != nil {
		return err
	}
	ids := make([]string, 0, len(sockets))
	for id := range sockets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := &c.inv.Host
	h.Sockets = h.Sockets[:0]
	for _, id := range ids {
		s := sockets[id]
		if s.Cores == 0 {
			s.Cores = s.Threads
		}
		h.Sockets = append(h.Sockets, *s)
	}
	h.Threads = threads
	if threads == 0 {
		return errors.New("no processor in /proc/cpuinfo")
	}
	return nil
}

// probeMemory reads MemTotal from /proc/meminfo.
func (c *collector) probeMemory() error {
	r, err := c.fs.Open("/proc/meminfo")
	if err != nil {
		if skippable(err) {
			return skipf("/proc/meminfo not readable")
		}
		return err
	}
	defer r.Close()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 1 {
			break
		}
		kb, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			return fmt.Errorf("MemTotal %q: %w", f[0], err)
		}
		c.inv.Host.MemoryBytes = kb * 1024
		return nil
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("no MemTotal in /proc/meminfo")
}

// vmVendors maps DMI strings (system vendor, product name, BIOS vendor) to a
// virtualisation technology, in the spirit of systemd-detect-virt's DMI table.
var vmVendors = []struct{ match, tech string }{
	{"kvm", "kvm"}, {"qemu", "qemu"}, {"vmware", "vmware"}, {"virtualbox", "oracle"},
	{"innotek", "oracle"}, {"xen", "xen"}, {"bochs", "bochs"}, {"parallels", "parallels"},
	{"bhyve", "bhyve"}, {"amazon ec2", "amazon"}, {"google compute engine", "google"},
	{"openstack", "openstack"}, {"alibaba cloud", "alibaba"}, {"microsoft corporation", "microsoft"},
	{"red hat", "kvm"},
}

// detectVMVendor classifies DMI strings. "Microsoft Corporation" alone is a
// Surface laptop; only with "Virtual Machine" is it Hyper-V. "Red Hat" alone
// is a server vendor; only RHEV/KVM products are VMs.
func detectVMVendor(vendor, product, bios string) string {
	all := strings.ToLower(vendor + "|" + product + "|" + bios)
	for _, v := range vmVendors {
		if !strings.Contains(all, v.match) {
			continue
		}
		switch v.match {
		case "microsoft corporation":
			if !strings.Contains(all, "virtual machine") {
				continue
			}
		case "red hat":
			if !strings.Contains(all, "kvm") && !strings.Contains(all, "rhev") {
				continue
			}
		}
		return v.tech
	}
	return ""
}

// probeVirt tells whether the host is a virtual machine or a container, from
// files only (no exec): DMI vendor strings, /sys/hypervisor, the "hypervisor"
// CPU flag, and the container markers systemd and the runtimes leave.
func (c *collector) probeVirt() error {
	h := &c.inv.Host
	h.Virt = Virt{Kind: VirtNone}
	// Containers first: a container inside a VM is a container for the agent.
	if t := readOptional(c.fs, "/run/systemd/container"); t != "" {
		h.Virt = Virt{Kind: VirtContainer, Tech: t}
		return nil
	}
	if exists(c.fs, "/.dockerenv") {
		h.Virt = Virt{Kind: VirtContainer, Tech: "docker"}
		return nil
	}
	if exists(c.fs, "/run/.containerenv") {
		h.Virt = Virt{Kind: VirtContainer, Tech: "podman"}
		return nil
	}
	if tech := detectVMVendor(c.dmi("sys_vendor"), c.dmi("product_name"), readOptional(c.fs, dmiDir+"bios_vendor")); tech != "" {
		h.Virt = Virt{Kind: VirtVM, Tech: tech}
		return nil
	}
	if readOptional(c.fs, "/sys/hypervisor/type") == "xen" {
		h.Virt = Virt{Kind: VirtVM, Tech: "xen"}
		return nil
	}
	if cpuHasFlag(c, "hypervisor") {
		h.Virt = Virt{Kind: VirtVM, Tech: "unknown"}
	}
	return nil
}

// cpuHasFlag looks for a flag on the first "flags" line of /proc/cpuinfo.
func cpuHasFlag(c *collector, flag string) bool {
	r, err := c.fs.Open("/proc/cpuinfo")
	if err != nil {
		return false
	}
	defer r.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(k) != "flags" {
			continue
		}
		for _, f := range strings.Fields(v) {
			if f == flag {
				return true
			}
		}
		return false
	}
	return false
}
