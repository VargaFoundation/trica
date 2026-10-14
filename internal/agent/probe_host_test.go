package agent

import (
	"reflect"
	"testing"
)

func TestProbeHostFixtures(t *testing.T) {
	tests := []struct {
		root string
		want Host
	}{
		{"debian12", Host{
			Hostname: "host-01", MachineID: "0123456789abcdef0123456789abcd01",
			Serial: "SN-TEST-HOST01", UUID: "0a1b2c3d-0000-4000-8000-000000000001",
			Vendor: "Example Systems", Model: "ExampleServer X1", BIOSVersion: "2.4.1",
			OSName: "Debian GNU/Linux", OSVersion: "12", Kernel: "6.1.0-28-amd64",
			MemoryBytes: 263921512 * 1024, Threads: 4,
		}},
		{"rhel9", Host{
			Hostname: "host-02.example.invalid", MachineID: "fedcba9876543210fedcba9876543202",
			UUID:   "5e6f7a8b-0000-4000-8000-000000000002",
			Vendor: "QEMU", Model: "Standard PC (Q35 + ICH9, 2009)", BIOSVersion: "1.16.2-1.el9",
			OSName: "Red Hat Enterprise Linux", OSVersion: "9.4", Kernel: "5.14.0-427.el9.x86_64",
			MemoryBytes: 8123456 * 1024, Threads: 2,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.root, func(t *testing.T) {
			inv, err := probeAfter(t, openRoot(t, tc.root), nil, nil, "host", "os", "cpu", "memory")
			if err != nil {
				t.Fatal(err)
			}
			got := inv.Host
			got.Sockets = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("host\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestProbeCPUSockets(t *testing.T) {
	deb, _ := probeAfter(t, openRoot(t, "debian12"), nil, nil, "cpu")
	want := []CPUSocket{
		{ID: "0", Model: "Example(R) Xeon(R) Gold 0000 CPU @ 2.00GHz", Cores: 2, Threads: 2},
		{ID: "1", Model: "Example(R) Xeon(R) Gold 0000 CPU @ 2.00GHz", Cores: 2, Threads: 2},
	}
	if len(deb.Host.Sockets) != 2 || deb.Host.Sockets[0] != want[0] || deb.Host.Sockets[1] != want[1] {
		t.Fatalf("sockets %+v, want %+v", deb.Host.Sockets, want)
	}

	t.Run("no physical id, no cpu cores", func(t *testing.T) {
		// ARM and some virtual CPUs: one socket whose cores equal its threads.
		r := tmpRoot(t, map[string]string{"proc/cpuinfo": "processor\t: 0\nmodel name\t: Example ARM\n\nprocessor\t: 1\nmodel name\t: Example ARM\n\n"})
		inv, err := probeAfter(t, r, nil, nil, "cpu")
		if err != nil {
			t.Fatal(err)
		}
		if len(inv.Host.Sockets) != 1 || inv.Host.Sockets[0].Cores != 2 || inv.Host.Threads != 2 {
			t.Fatalf("%+v", inv.Host)
		}
	})
	t.Run("last block without trailing blank line", func(t *testing.T) {
		r := tmpRoot(t, map[string]string{"proc/cpuinfo": "processor\t: 0\nphysical id\t: 0\ncpu cores\t: 1\n"})
		inv, err := probeAfter(t, r, nil, nil, "cpu")
		if err != nil || inv.Host.Threads != 1 {
			t.Fatalf("%+v %v", inv.Host, err)
		}
	})
	t.Run("empty cpuinfo fails", func(t *testing.T) {
		r := tmpRoot(t, map[string]string{"proc/cpuinfo": "vendor_id\t: Example\n"})
		if _, err := probeAfter(t, r, nil, nil, "cpu"); err == nil {
			t.Fatal("a cpuinfo without processor was accepted")
		}
	})
	t.Run("missing cpuinfo is a skip", func(t *testing.T) {
		inv := observe(t.Context(), tmpRoot(t, map[string]string{"etc/hostname": "host-01\n"}), noRunner{}, fsAddrs{path: "/addrs.txt"})
		if !containsPrefix(inv.Skipped, "cpu: ") {
			t.Fatalf("skipped %v", inv.Skipped)
		}
	})
}

func TestProbeMemory(t *testing.T) {
	tests := []struct {
		name    string
		meminfo string
		want    int64
		wantErr bool
	}{
		{"kB", "MemTotal:       1024 kB\n", 1024 * 1024, false},
		{"not first line", "MemFree: 5 kB\nMemTotal: 2 kB\n", 2048, false},
		{"absent", "MemFree: 5 kB\n", 0, true},
		{"not a number", "MemTotal: lots kB\n", 0, true},
		{"no value", "MemTotal:\n", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv, err := probeAfter(t, tmpRoot(t, map[string]string{"proc/meminfo": tc.meminfo}), nil, nil, "memory")
			if (err != nil) != tc.wantErr || inv.Host.MemoryBytes != tc.want {
				t.Fatalf("memory %d err %v", inv.Host.MemoryBytes, err)
			}
		})
	}
}

func TestMeaningful(t *testing.T) {
	tests := map[string]bool{
		"SN-TEST-HOST01":                       true,
		"0a1b2c3d-0000-4000-8000-000000000001": true,
		"":                                     false,
		"   ":                                  false,
		"To Be Filled By O.E.M.":               false,
		"Default string":                       false,
		"System Serial Number":                 false,
		"None":                                 false,
		"N/A":                                  false,
		"Not Specified":                        false,
		"0":                                    false,
		"123456789":                            false,
		"00000000-0000-0000-0000-000000000000": false,
		"ffffffff-ffff-ffff-ffff-ffffffffffff": false,
		"FFFFFFFFFFFF":                         false,
		"0000000":                              false,
	}
	for in, want := range tests {
		if got := meaningful(in); got != want {
			t.Errorf("meaningful(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDMIPlaceholdersAreNotIdentity(t *testing.T) {
	// Two servers from one batch of cheap boards both say "To Be Filled By O.E.M.":
	// reporting that as a serial would merge them into one device.
	r := tmpRoot(t, map[string]string{
		"etc/hostname":                    "host-03\n",
		"etc/machine-id":                  "00000000000000000000000000000000\n",
		"sys/class/dmi/id/product_serial": "To Be Filled By O.E.M.\n",
		"sys/class/dmi/id/product_uuid":   "00000000-0000-0000-0000-000000000000\n",
		"sys/class/dmi/id/sys_vendor":     "System manufacturer\n",
		"sys/class/dmi/id/product_name":   "System Product Name\n",
		"sys/class/dmi/id/bios_version":   "Default string\n",
	})
	inv, err := probeAfter(t, r, nil, nil, "host")
	if err != nil {
		t.Fatal(err)
	}
	h := inv.Host
	if h.Serial != "" || h.UUID != "" || h.MachineID != "" || h.Vendor != "" || h.Model != "" || h.BIOSVersion != "" {
		t.Fatalf("placeholders became identity: %+v", h)
	}
	if h.Hostname != "host-03" {
		t.Fatalf("hostname %q", h.Hostname)
	}
}

func TestMalformedIdentifiersAreDropped(t *testing.T) {
	r := tmpRoot(t, map[string]string{
		"etc/hostname":                  "host-03\n",
		"etc/machine-id":                "not-a-machine-id\n",
		"sys/class/dmi/id/product_uuid": "not-a-uuid\n",
	})
	inv, _ := probeAfter(t, r, nil, nil, "host")
	if inv.Host.MachineID != "" || inv.Host.UUID != "" {
		t.Fatalf("%+v", inv.Host)
	}
}

func TestRootOnlyDMISilentlySkipped(t *testing.T) {
	// A non-root agent cannot read product_serial and product_uuid: the identity
	// is reported without them and nothing is said, the host is not degraded.
	fsys := denyFS{FS: openRoot(t, "debian12"), prefixes: []string{
		"/sys/class/dmi/id/product_serial", "/sys/class/dmi/id/product_uuid",
	}}
	inv := observe(t.Context(), fsys, newFakeRunner("debian12"), fsAddrs{path: "/addrs.txt"})
	h := inv.Host
	if h.Serial != "" || h.UUID != "" {
		t.Fatalf("root-only fields read despite the denial: %+v", h)
	}
	if h.Hostname != "host-01" || h.MachineID == "" || h.Vendor == "" || h.Model == "" {
		t.Fatalf("world-readable identity lost: %+v", h)
	}
	for _, s := range append(append([]string{}, inv.Skipped...), inv.Failed...) {
		if contains(s, "dmi") || contains(s, "serial") || contains(s, "uuid") || contains(s, "host:") {
			t.Fatalf("a root-only DMI field produced a note: %q", s)
		}
	}
}

func TestHostnameFallbackAndFailure(t *testing.T) {
	t.Run("falls back to /etc/hostname, lower-cased", func(t *testing.T) {
		inv, err := probeAfter(t, tmpRoot(t, map[string]string{"etc/hostname": "HOST-09\n"}), nil, nil, "host")
		if err != nil || inv.Host.Hostname != "host-09" {
			t.Fatalf("%q %v", inv.Host.Hostname, err)
		}
	})
	t.Run("kernel value wins over the file", func(t *testing.T) {
		r := tmpRoot(t, map[string]string{"etc/hostname": "old\n", "proc/sys/kernel/hostname": "new\n"})
		inv, _ := probeAfter(t, r, nil, nil, "host")
		if inv.Host.Hostname != "new" {
			t.Fatalf("%q", inv.Host.Hostname)
		}
	})
	t.Run("no name is a failure", func(t *testing.T) {
		if _, err := probeAfter(t, tmpRoot(t, map[string]string{"etc/": ""}), nil, nil, "host"); err == nil {
			t.Fatal("a host without a name was accepted")
		}
	})
}

func TestParseKV(t *testing.T) {
	got := parseKV("# comment\n\nNAME=\"Debian GNU/Linux\"\nID=debian\nPRETTY_NAME='Debian 12'\nEMPTY=\nBROKEN\nQ=\"unterminated\nSPACED = \"x y\" \n")
	want := map[string]string{
		"NAME": "Debian GNU/Linux", "ID": "debian", "PRETTY_NAME": "Debian 12",
		"EMPTY": "", "Q": "\"unterminated", "SPACED": "x y",
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestProbeOSFallbacks(t *testing.T) {
	t.Run("usr/lib/os-release", func(t *testing.T) {
		r := tmpRoot(t, map[string]string{"usr/lib/os-release": "NAME=Example\nVERSION_ID=1.2\n"})
		inv, err := probeAfter(t, r, nil, nil, "os")
		if err != nil || inv.Host.OSName != "Example" || inv.Host.OSVersion != "1.2" {
			t.Fatalf("%+v %v", inv.Host, err)
		}
	})
	t.Run("rolling release without VERSION_ID", func(t *testing.T) {
		r := tmpRoot(t, map[string]string{"etc/os-release": "NAME=\"Arch Linux\"\nBUILD_ID=rolling\n"})
		inv, _ := probeAfter(t, r, nil, nil, "os")
		if inv.Host.OSVersion != "rolling" {
			t.Fatalf("%+v", inv.Host)
		}
	})
	t.Run("name falls back to PRETTY_NAME then ID", func(t *testing.T) {
		inv, _ := probeAfter(t, tmpRoot(t, map[string]string{"etc/os-release": "ID=example\n"}), nil, nil, "os")
		if inv.Host.OSName != "example" {
			t.Fatalf("%+v", inv.Host)
		}
	})
	t.Run("absent is a skip, kernel still read", func(t *testing.T) {
		r := tmpRoot(t, map[string]string{"proc/sys/kernel/osrelease": "6.1.0\n"})
		inv, err := probeAfter(t, r, nil, nil, "os")
		var skip skipError
		if !asSkip(err, &skip) || inv.Host.Kernel != "6.1.0" {
			t.Fatalf("err %v kernel %q", err, inv.Host.Kernel)
		}
	})
}

func TestDetectVMVendor(t *testing.T) {
	tests := []struct {
		name                  string
		vendor, product, bios string
		want                  string
	}{
		{"qemu vendor", "QEMU", "Standard PC (Q35 + ICH9, 2009)", "SeaBIOS", "qemu"},
		{"kvm product", "Red Hat", "KVM", "", "kvm"},
		{"rhev", "Red Hat", "RHEV Hypervisor", "", "kvm"},
		{"red hat alone is a server vendor", "Red Hat", "Example Server", "", ""},
		{"vmware", "VMware, Inc.", "VMware7,1", "", "vmware"},
		{"virtualbox", "innotek GmbH", "VirtualBox", "", "oracle"},
		{"hyper-v", "Microsoft Corporation", "Virtual Machine", "", "microsoft"},
		{"surface laptop", "Microsoft Corporation", "Surface Laptop 5", "", ""},
		{"amazon", "Amazon EC2", "m5.large", "", "amazon"},
		{"google", "Google", "Google Compute Engine", "", "google"},
		{"xen in bios vendor", "", "HVM domU", "Xen", "xen"},
		{"bare metal", "Example Systems", "ExampleServer X1", "Example BIOS", ""},
		{"nothing", "", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectVMVendor(tc.vendor, tc.product, tc.bios); got != tc.want {
				t.Fatalf("detectVMVendor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProbeVirt(t *testing.T) {
	flags := func(f string) string { return "processor\t: 0\nflags\t\t: " + f + "\n\n" }
	tests := []struct {
		name  string
		files map[string]string
		want  Virt
	}{
		{"fixture bare metal", nil, Virt{Kind: VirtNone}},
		{"systemd container marker", map[string]string{"run/systemd/container": "lxc\n"}, Virt{Kind: VirtContainer, Tech: "lxc"}},
		{"docker", map[string]string{".dockerenv": ""}, Virt{Kind: VirtContainer, Tech: "docker"}},
		{"podman", map[string]string{"run/.containerenv": ""}, Virt{Kind: VirtContainer, Tech: "podman"}},
		{"container wins over a VM vendor", map[string]string{".dockerenv": "", "sys/class/dmi/id/sys_vendor": "QEMU\n"}, Virt{Kind: VirtContainer, Tech: "docker"}},
		{"vendor string", map[string]string{"sys/class/dmi/id/sys_vendor": "VMware, Inc.\n"}, Virt{Kind: VirtVM, Tech: "vmware"}},
		{"xen hypervisor file", map[string]string{"sys/hypervisor/type": "xen\n"}, Virt{Kind: VirtVM, Tech: "xen"}},
		{"hypervisor cpu flag only", map[string]string{"proc/cpuinfo": flags("fpu hypervisor sse")}, Virt{Kind: VirtVM, Tech: "unknown"}},
		{"cpu flags without it", map[string]string{"proc/cpuinfo": flags("fpu sse hypervisor_x")}, Virt{Kind: VirtNone}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var fsys FS
			if tc.files == nil {
				fsys = openRoot(t, "debian12")
			} else {
				fsys = tmpRoot(t, tc.files)
			}
			inv, err := probeAfter(t, fsys, nil, nil, "virt")
			if err != nil || inv.Host.Virt != tc.want {
				t.Fatalf("virt %+v err %v, want %+v", inv.Host.Virt, err, tc.want)
			}
		})
	}

	t.Run("fixture VM", func(t *testing.T) {
		inv, _ := probeAfter(t, openRoot(t, "rhel9"), nil, nil, "virt")
		if inv.Host.Virt != (Virt{Kind: VirtVM, Tech: "qemu"}) {
			t.Fatalf("%+v", inv.Host.Virt)
		}
	})
}

func TestHostProbesDoNotExec(t *testing.T) {
	// Identity, OS, CPU, memory and virtualisation come from files only.
	run := newFakeRunner("rhel9")
	if _, err := probeAfter(t, openRoot(t, "rhel9"), run, nil, "host", "os", "cpu", "memory", "virt", "interfaces", "addresses", "sockets"); err != nil {
		t.Fatal(err)
	}
	if len(run.calls) != 0 {
		t.Fatalf("file-only probes executed %v", run.calls)
	}
}
