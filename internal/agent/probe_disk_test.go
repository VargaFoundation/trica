package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

func TestProbeDisksDebian(t *testing.T) {
	run := newFakeRunner("debian12")
	inv, err := probeAfter(t, openRoot(t, "debian12"), run, nil, "virt", "disks")
	if err != nil {
		t.Fatal(err)
	}
	want := []Disk{
		{
			Name: "nvme0n1", Model: "EXAMPLE NVMe 960GB", Serial: "SN-TEST-NVME-0001", WWN: "00123456789abcdef0",
			Firmware: "EXN10001", Protocol: "NVMe", Media: "nvme", CapacityBytes: 1875385008 * 512,
			Smart: &Smart{Health: "ok", WearPct: f64(7), SparePct: f64(98), PowerOnHours: i64(4320), MediaErrors: i64(0)},
		},
		{
			Name: "sda", Model: "EXAMPLE SSD 480GB", Serial: "SN-TEST-SATA-0001", WWN: "5001234012345678",
			Firmware: "1.02", Media: "ssd", CapacityBytes: 937703088 * 512,
			Smart: &Smart{Health: "warning", WearPct: f64(3), PowerOnHours: i64(12336), ReallocatedSectors: i64(2), PendingSectors: i64(1)},
		},
		// sdb: smartctl cannot open it without root, the drive is listed from sysfs alone.
		{Name: "sdb", Model: "EXAMPLE HDD 4TB", Media: "hdd", CapacityBytes: 7814037168 * 512},
	}
	if !reflect.DeepEqual(inv.Disks, want) {
		for i := range inv.Disks {
			t.Logf("disk %d: %+v smart %+v", i, inv.Disks[i], inv.Disks[i].Smart)
		}
		t.Fatalf("disks differ from the expectation")
	}
	// loop0, dm-0 and sr0 are not drives.
	if len(inv.Skipped) != 1 || !strings.Contains(inv.Skipped[0], "smartctl could not open sdb (not root)") {
		t.Fatalf("skipped %v", inv.Skipped)
	}
}

func TestVirtualMachineDisksAreNotSmartQueried(t *testing.T) {
	run := newFakeRunner("rhel9")
	inv, err := probeAfter(t, openRoot(t, "rhel9"), run, nil, "host", "virt", "disks")
	if err != nil {
		t.Fatal(err)
	}
	if run.called("smartctl") {
		t.Fatalf("smartctl asked about a virtual disk: %v", run.calls)
	}
	if len(inv.Disks) != 1 || inv.Disks[0].Name != "vda" || inv.Disks[0].Smart != nil || inv.Disks[0].Serial != "SN-TEST-VDA-0001" {
		t.Fatalf("%+v", inv.Disks)
	}
}

func TestDisksWithoutSmartctl(t *testing.T) {
	inv := observe(t.Context(), openRoot(t, "debian12"), noRunner{}, fsAddrs{path: "/addrs.txt"})
	if len(inv.Disks) != 3 {
		t.Fatalf("disks %+v", inv.Disks)
	}
	n := 0
	for _, s := range inv.Skipped {
		if strings.Contains(s, "disks: smartctl not available") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the missing smartctl must be said once, not per disk: %v", inv.Skipped)
	}
}

func TestDisksSmartctlFailures(t *testing.T) {
	root := openRoot(t, "debian12")
	t.Run("no output", func(t *testing.T) {
		run := runnerFunc(func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("exit status 2") })
		inv, err := probeAfter(t, root, run, nil, "disks")
		if err == nil || !strings.Contains(err.Error(), "smartctl") {
			t.Fatalf("err %v", err)
		}
		if len(inv.Disks) != 3 {
			t.Fatalf("the drives must still be listed: %+v", inv.Disks)
		}
	})
	t.Run("not json", func(t *testing.T) {
		run := runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Smartctl open device: failed"), nil
		})
		if _, err := probeAfter(t, root, run, nil, "disks"); err == nil {
			t.Fatal("garbage output was accepted")
		}
	})
	t.Run("not allow-listed device name", func(t *testing.T) {
		// cciss!c0d0 and the like: sysfs data only, and no failure.
		run := runnerFunc(func(context.Context, string, ...string) ([]byte, error) { return nil, ErrNotAllowed })
		inv, err := probeAfter(t, root, run, nil, "disks")
		if err != nil || len(inv.Disks) != 3 || len(inv.Skipped) != 0 {
			t.Fatalf("%v %+v %v", err, inv.Disks, inv.Skipped)
		}
	})
}

func TestApplySmart(t *testing.T) {
	tests := []struct {
		name    string
		media   string
		json    string
		want    Smart
		wantErr string
	}{
		{
			name: "ssd, wear from Wear_Leveling_Count", media: "ssd",
			json: `{"model_name":"M","smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":177,"value":90,"raw":{"value":1}}]}}`,
			want: Smart{Health: "ok", WearPct: f64(10)},
		},
		{
			name: "ssd, 233 preferred over 177", media: "ssd",
			json: `{"model_name":"M","smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":177,"value":90,"raw":{"value":1}},{"id":233,"value":80,"raw":{"value":1}}]}}`,
			want: Smart{Health: "ok", WearPct: f64(20)},
		},
		{
			name: "ssd, 231", media: "ssd",
			json: `{"model_name":"M","smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":231,"value":99,"raw":{"value":1}}]}}`,
			want: Smart{Health: "ok", WearPct: f64(1)},
		},
		{
			name: "hdd has no wear even if a vendor reuses an attribute", media: "hdd",
			json: `{"model_name":"M","smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":177,"value":50,"raw":{"value":1}}]}}`,
			want: Smart{Health: "ok"},
		},
		{
			name: "normalised value above 100 clamps wear to 0", media: "ssd",
			json: `{"model_name":"M","ata_smart_attributes":{"table":[{"id":233,"value":120,"raw":{"value":1}}]}}`,
			want: Smart{WearPct: f64(0)},
		},
		{
			name: "failed status is critical", media: "ssd",
			json: `{"model_name":"M","smart_status":{"passed":false}}`,
			want: Smart{Health: "critical"},
		},
		{
			name: "reallocated sectors is a warning", media: "hdd",
			json: `{"model_name":"M","smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":5,"value":100,"raw":{"value":8}}]}}`,
			want: Smart{Health: "warning", ReallocatedSectors: i64(8)},
		},
		{
			name: "zero reallocated and pending is ok", media: "hdd",
			json: `{"model_name":"M","smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":5,"raw":{"value":0}},{"id":197,"raw":{"value":0}}]}}`,
			want: Smart{Health: "ok", ReallocatedSectors: i64(0), PendingSectors: i64(0)},
		},
		{
			name: "nvme media errors is a warning", media: "nvme",
			json: `{"model_name":"M","smart_status":{"passed":true},"nvme_smart_health_information_log":{"percentage_used":3,"available_spare":100,"media_errors":2,"critical_warning":0}}`,
			want: Smart{Health: "warning", WearPct: f64(3), SparePct: f64(100), MediaErrors: i64(2)},
		},
		{
			name: "nvme critical warning is a warning", media: "nvme",
			json: `{"model_name":"M","smart_status":{"passed":true},"nvme_smart_health_information_log":{"percentage_used":3,"critical_warning":4}}`,
			want: Smart{Health: "warning", WearPct: f64(3)},
		},
		{
			name: "nvme percentage used over 100 clamps", media: "nvme",
			json: `{"model_name":"M","nvme_smart_health_information_log":{"percentage_used":104}}`,
			want: Smart{WearPct: f64(100)},
		},
		{
			name: "no smart_status leaves health unknown", media: "ssd",
			json: `{"model_name":"M","power_on_time":{"hours":49}}`,
			want: Smart{PowerOnHours: i64(48)},
		},
		{
			name: "power-on hours of the attribute table when no power_on_time", media: "hdd",
			json: `{"model_name":"M","ata_smart_attributes":{"table":[{"id":9,"raw":{"value":100000}}]}}`,
			want: Smart{PowerOnHours: i64(99984)},
		},
		{
			name: "could not open", media: "ssd",
			json: `{"smartctl":{"messages":[{"string":"Smartctl open device: /dev/sda failed: Permission denied","severity":"error"}],"exit_status":2}}`, wantErr: "could not open",
		},
		{name: "no data and no error message", media: "ssd", json: `{"smartctl":{"exit_status":0}}`, wantErr: "no data"},
		{name: "not json", media: "ssd", json: `nope`, wantErr: "unreadable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Disk{Media: tc.media}
			got, err := applySmart(&d, []byte(tc.json))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*got, tc.want) {
				t.Fatalf("\n got %+v\nwant %+v", *got, tc.want)
			}
		})
	}
}

func TestPowerOnHoursAreQuantised(t *testing.T) {
	// The counter moves every hour; reported exactly, every drive would write a
	// history row per hour. Within the same day the reported value must not change.
	for _, h := range []int64{12336, 12337, 12350, 12359} {
		if got := *quantise(h); got != 12336 {
			t.Errorf("quantise(%d) = %d, want 12336", h, got)
		}
	}
	if got := *quantise(12360); got != 12360 {
		t.Errorf("quantise(12360) = %d", got)
	}
	if got := *quantise(5); got != 0 {
		t.Errorf("quantise(5) = %d", got)
	}
}

func TestSmartDoesNotCollectVolatileReadings(t *testing.T) {
	// Temperature, bytes written and the like change at every run: they would
	// rewrite history every time. The Smart type has no field for them.
	typ := reflect.TypeOf(Smart{})
	for i := 0; i < typ.NumField(); i++ {
		n := strings.ToLower(typ.Field(i).Name)
		for _, banned := range []string{"temp", "written", "read", "uptime", "cycles"} {
			if strings.Contains(n, banned) {
				t.Errorf("Smart.%s is a volatile reading", typ.Field(i).Name)
			}
		}
	}
}

func TestWWNFromSysfs(t *testing.T) {
	tests := map[string]string{
		"naa.5001234012345678":   "5001234012345678",
		"NAA.5001234012345678":   "5001234012345678",
		"eui.00123456789abcdef0": "00123456789abcdef0",
		"t10.ATA     EXAMPLE":    "",
		"uuid.1234":              "",
		"naa.0000000000000000":   "",
		"":                       "",
	}
	for in, want := range tests {
		if got := wwnFromSysfs(in); got != want {
			t.Errorf("wwnFromSysfs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotDiskNames(t *testing.T) {
	tests := map[string]bool{
		"loop0": true, "loop12": true, "ram0": true, "zram0": true, "dm-0": true, "md0": true, "md127": true,
		"sr0": true, "fd0": true, "nbd3": true,
		"sda": false, "nvme0n1": false, "vda": false, "xvda": false, "mmcblk0": false, "sdaa": false,
	}
	for n, want := range tests {
		if got := notDisk.MatchString(n); got != want {
			t.Errorf("notDisk(%q) = %v, want %v", n, got, want)
		}
	}
}

func TestPlaceholderDiskSerialIsDropped(t *testing.T) {
	r := tmpRoot(t, map[string]string{
		"sys/block/sda/size":          "2048\n",
		"sys/block/sda/device/serial": "0000000000000000\n",
	})
	inv, err := probeAfter(t, r, noRunner{}, nil, "disks")
	if err != nil || len(inv.Disks) != 1 || inv.Disks[0].Serial != "" || inv.Disks[0].CapacityBytes != 2048*512 {
		t.Fatalf("%+v %v", inv.Disks, err)
	}
}

func TestDisksNoSysfs(t *testing.T) {
	inv := observe(t.Context(), tmpRoot(t, map[string]string{"etc/hostname": "h\n"}), noRunner{}, fsAddrs{path: "/addrs.txt"})
	if !containsPrefix(inv.Skipped, "disks: ") {
		t.Fatalf("skipped %v failed %v", inv.Skipped, inv.Failed)
	}
}
