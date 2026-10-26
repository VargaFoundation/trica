package redfish

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// build makes a collector for tg whose clock is the fake's.
func build(t *testing.T, tg collect.Target) *Collector {
	t.Helper()
	c, err := New(tg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rc := c.(*Collector)
	rc.now = func() time.Time { return FakeNow }
	return rc
}

// collectOnce runs one collection and returns its single batch.
func collectOnce(t *testing.T, tg collect.Target) *sdk.Batch {
	t.Helper()
	bs := collecttest.Run(t, build(t, tg))
	if len(bs) != 1 {
		t.Fatalf("a run must emit one batch, got %d", len(bs))
	}
	return bs[0]
}

// withOptions sets the target's options from a YAML mapping.
func withOptions(t *testing.T, tg collect.Target, doc string) collect.Target {
	t.Helper()
	if err := yaml.Unmarshal([]byte(doc), &tg.Options); err != nil {
		t.Fatal(err)
	}
	return tg
}

func object(t *testing.T, b *sdk.Batch, ref string) *sdk.Object {
	t.Helper()
	for _, o := range b.Objects {
		if o.Ref == ref {
			return o
		}
	}
	t.Fatalf("no object %q in the batch", ref)
	return nil
}

func hasObject(b *sdk.Batch, ref string) bool {
	for _, o := range b.Objects {
		if o.Ref == ref {
			return true
		}
	}
	return false
}

func countType(b *sdk.Batch, typ string) int {
	n := 0
	for _, o := range b.Objects {
		if o.Type == typ {
			n++
		}
	}
	return n
}

// wantObj is what a test expects of one object: its type, parent, identity
// and the attributes it must carry (compared by their printed value).
type wantObj struct {
	ref    string
	typ    string
	parent string
	id     map[string]string
	attrs  map[string]any
}

func checkObject(t *testing.T, b *sdk.Batch, w wantObj) {
	t.Helper()
	o := object(t, b, w.ref)
	if o.Type != w.typ {
		t.Errorf("%s: type %q, want %q", w.ref, o.Type, w.typ)
	}
	if w.parent != "" && o.Parent != w.parent {
		t.Errorf("%s: parent %q, want %q", w.ref, o.Parent, w.parent)
	}
	for k, v := range w.id {
		if o.Identity[k] != v {
			t.Errorf("%s: identity %s = %q, want %q", w.ref, k, o.Identity[k], v)
		}
	}
	for k, v := range w.attrs {
		got, ok := o.Attributes[k]
		if !ok {
			t.Errorf("%s: attribute %s missing (want %v)", w.ref, k, v)
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(v) {
			t.Errorf("%s: %s = %v, want %v", w.ref, k, got, v)
		}
	}
}

func checkAbsent(t *testing.T, b *sdk.Batch, ref string, attrs ...string) {
	t.Helper()
	o := object(t, b, ref)
	for _, a := range attrs {
		if v, ok := o.Attributes[a]; ok {
			t.Errorf("%s: attribute %s = %v, want none", ref, a, v)
		}
	}
}

type profileCase struct {
	name    string
	profile Profile
	objects []wantObj
	counts  map[string]int
	// absent device attributes
	absent  []string
	warns   []string
	mustGet []string // request path prefixes that must have been read
	mustNot []string // request path prefixes that must never be read
}

var profileCases = []profileCase{
	{
		name:    "HPE DL360 Gen9, iLO 4",
		profile: ILO4,
		counts: map[string]int{
			"device": 1, "cpu": 2, "memory": 3, "module": 1, "disk": 2, "interface": 3,
			"psu": 2, "sensor": 5, "fan": 4, "ip_address": 1,
		},
		absent: []string{"outlet_temp_c"},
		warns:  []string{"1 event log entries had no readable timestamp"},
		mustGet: []string{
			"/redfish/v1/Systems/1/SmartStorage", "/redfish/v1/Systems/1/FirmwareInventory",
			"/redfish/v1/Systems/1/LogServices/IML/Entries", "/redfish/v1/Chassis/1/Thermal",
		},
		mustNot: []string{"/redfish/v1/Managers/1/LogServices/IEL/Entries", "/redfish/v1/SessionService", "/redfish/v1/Systems/1/Storage"},
		objects: []wantObj{
			{ref: "dev", typ: "device", id: map[string]string{
				"serial": "SYNTH-ILO4-0001", "hw_uuid": "11111111-2222-4333-8444-5555555500a1",
				"name": "srv-01", "mgmt_ip": "192.0.2.11",
			}, attrs: map[string]any{
				"role": "server", "vendor": "HP", "model": "ProLiant DL360 Gen9", "part_number": "755258-B21",
				"bios_version": "P89 v2.60 (05/21/2018)", "bmc_version": "iLO 4 v2.78", "bmc_ip": "192.0.2.11",
				"power_state": "on", "health": "ok", "cpu_count": 2, "memory_bytes": int64(48) << 30,
				"power_w": 181, "power_basis": "input", "inlet_temp_c": 21, "location_led": "off",
				"firmware_inventory": []string{
					"Backup System ROM=P89 v2.40 (02/17/2017)", "HP Smart Array P440ar Controller=6.06",
					"Power Management Controller Firmware=1.0.4", "System ROM=P89 v2.60 (05/21/2018)", "iLO 4=2.78 Dec 08 2020",
				},
				// The backup ROM is searchable but does not run: not in the table.
				"firmware": []map[string]any{
					{"name": "HP Smart Array P440ar Controller", "version": "6.06", "location": "Slot 0"},
					{"name": "Power Management Controller Firmware", "version": "1.0.4", "location": "System Board"},
					{"name": "System ROM", "version": "P89 v2.60 (05/21/2018)", "released": "2018-05-21", "location": "System Board"},
					{"name": "iLO 4", "version": "2.78 Dec 08 2020", "released": "2020-12-08", "location": "System Board"},
				},
				"log_window_days": 30, "log_critical_count": 2, "log_warning_count": 2,
				"log_last_critical": "Power Supply 2 failure (Bay 2).", "log_last_critical_at": "2026-10-13T12:00:00Z",
			}},
			{ref: "cpu:Proc 1", typ: "cpu", parent: "dev", id: map[string]string{"name": "Proc 1"},
				attrs: map[string]any{"cores": 8, "threads": 16, "health": "ok", "model": "Intel(R) Xeon(R) CPU E5-2640 v3 @ 2.60GHz"}},
			// The NotPresent DIMM is skipped; the one in Warning keeps its health.
			{ref: "dimm:PROC 2 DIMM 1", typ: "memory", parent: "dev", id: map[string]string{"name": "PROC 2 DIMM 1"},
				attrs: map[string]any{"size_bytes": int64(16) << 30, "speed_mhz": 2133, "health": "warning", "model": "HMA42GR7MFR4N-TF", "technology": "DDR4"}},
			// The controller serial is trimmed: the firmware pads it with spaces.
			{ref: "ctl:Slot 0", typ: "module", parent: "dev", id: map[string]string{"name": "Slot 0", "serial": "SYNTH-P440AR-01"},
				attrs: map[string]any{"kind": "storage_controller", "model": "HP Smart Array P440ar Controller", "firmware_version": "6.06", "health": "ok"}},
			{ref: "ctl:Slot 0/disk:1I:1:1", typ: "disk", parent: "ctl:Slot 0", id: map[string]string{"serial": "SYNTH-HDD-0001", "name": "1I:1:1"},
				attrs: map[string]any{"media": "hdd", "protocol": "SAS", "capacity_bytes": int64(572325) << 20, "firmware_version": "HPD4", "slot": "1I:1:1", "health": "ok", "model": "EG0600FBVFP"}},
			{ref: "ctl:Slot 0/disk:1I:1:2", typ: "disk", parent: "ctl:Slot 0", id: map[string]string{"serial": "SYNTH-SSD-0001"},
				attrs: map[string]any{"media": "ssd", "wear_pct": 7.5, "health": "warning"}},
			{ref: "nic:1", typ: "interface", parent: "dev", id: map[string]string{"name": "1"},
				attrs: map[string]any{"kind": "physical", "mac": "02:00:5e:10:01:01", "oper_status": "up", "speed_mbps": 1000, "duplex": "full"}},
			{ref: "nic:2", typ: "interface", parent: "dev", attrs: map[string]any{"oper_status": "down"}},
			{ref: "bmc:bmc", typ: "interface", parent: "dev", id: map[string]string{"name": "bmc"},
				attrs: map[string]any{"kind": "bmc", "mac": "02:00:5e:10:00:01", "speed_mbps": 1000}},
			{ref: "ip:192.0.2.11", typ: "ip_address", id: map[string]string{"address": "192.0.2.11"},
				attrs: map[string]any{"role": "bmc", "prefix_length": 24}},
			// Two supplies with the same name get their member id appended.
			{ref: "psu:HpServerPowerSupply 1", typ: "psu", parent: "dev", id: map[string]string{"name": "HpServerPowerSupply 1", "serial": "SYNTH-PSU-A002"},
				attrs: map[string]any{"model": "656363-B21", "capacity_w": 800, "firmware_version": "1.00", "health": "ok"}},
			{ref: "sensor:01-Inlet Ambient", typ: "sensor", parent: "dev",
				attrs: map[string]any{"kind": "temperature", "placement": "inlet", "reading": 21, "unit": "°C", "warning_threshold": 38, "critical_threshold": 42}},
			{ref: "sensor:05-P1 DIMM 1-3", typ: "sensor", attrs: map[string]any{"placement": "memory"}},
			{ref: "sensor:07-HD Max", typ: "sensor", parent: "dev", attrs: map[string]any{"placement": "system"}},
			{ref: "fan:Fan 1", typ: "fan", parent: "dev", attrs: map[string]any{"speed_pct": 20, "health": "ok"}},
			{ref: "fan:Fan 3", typ: "fan", parent: "dev", attrs: map[string]any{"health": "warning"}},
			{ref: "fan:Fan 4", typ: "fan", parent: "dev", attrs: map[string]any{"health": "absent"}},
		},
	},
	{
		name:    "HPE DL380 Gen10, iLO 5",
		profile: ILO5,
		counts: map[string]int{
			"device": 1, "cpu": 2, "memory": 2, "module": 1, "disk": 2, "interface": 4,
			"psu": 2, "sensor": 4, "fan": 3, "ip_address": 1,
		},
		absent: []string{"outlet_temp_c"},
		mustGet: []string{
			"/redfish/v1/Systems/1/Storage", "/redfish/v1/Systems/1/Memory", "/redfish/v1/UpdateService/FirmwareInventory/1",
			"/redfish/v1/Systems/1/LogServices/IML/Entries",
		},
		// The standard Storage answered: the OEM tree is not walked.
		mustNot: []string{"/redfish/v1/Systems/1/SmartStorage", "/redfish/v1/Managers/1/LogServices/IEL/Entries", "/redfish/v1/SessionService"},
		objects: []wantObj{
			{ref: "dev", typ: "device", id: map[string]string{
				"serial": "SYNTH-ILO5-0002", "hw_uuid": "11111111-2222-4333-8444-5555555500b2",
				"name": "srv-02.example.invalid", "mgmt_ip": "198.51.100.21",
			}, attrs: map[string]any{
				"vendor": "HPE", "model": "ProLiant DL380 Gen10", "part_number": "868703-B21", "bios_version": "U30 v2.72 (07/09/2021)",
				"bmc_version": "iLO 5 v2.72", "bmc_ip": "198.51.100.21", "health": "warning", "location_led": "on",
				"cpu_count": 2, "memory_bytes": int64(64) << 30, "power_w": 214, "power_basis": "input", "inlet_temp_c": 24,
				"firmware_inventory": []string{"Smart Array P408i-a SR Gen10=2.62", "System ROM=U30 v2.72 (07/09/2021)", "iLO 5=2.72 Aug 24 2022"},
				"firmware": []map[string]any{
					{"name": "Smart Array P408i-a SR Gen10", "version": "2.62", "location": "Slot 0", "updateable": true},
					{"name": "System ROM", "version": "U30 v2.72 (07/09/2021)", "released": "2021-07-09", "location": "System Board", "updateable": true},
					{"name": "iLO 5", "version": "2.72 Aug 24 2022", "released": "2022-08-24", "location": "System Board", "updateable": true},
				},
				"log_critical_count": 1, "log_warning_count": 1, "log_last_critical": "Drive Failure (Bay 2).", "log_last_critical_at": "2026-10-10T12:00:00Z",
			}},
			{ref: "dimm:PROC 1 DIMM 3", typ: "memory", parent: "dev", id: map[string]string{"name": "PROC 1 DIMM 3", "serial": "SYNTH-DIMM-0001"},
				attrs: map[string]any{"size_bytes": int64(32) << 30, "speed_mhz": 2666, "model": "HMA84GR7CJR4N-WM", "technology": "DDR4"}},
			{ref: "ctl:DE00A000", typ: "module", parent: "dev", id: map[string]string{"serial": "SYNTH-P408IA-02"},
				attrs: map[string]any{"kind": "storage_controller", "model": "HPE Smart Array P408i-a SR Gen10", "firmware_version": "2.62"}},
			// Wear from the HPE OEM endurance counter; the empty bay 3 is not a disk.
			{ref: "ctl:DE00A000/disk:Port=1I:Box=1:Bay=1", typ: "disk", parent: "ctl:DE00A000", id: map[string]string{"serial": "SYNTH-SSD-0002"},
				attrs: map[string]any{"media": "ssd", "wear_pct": 12, "capacity_bytes": int64(480103981056), "slot": "Port=1I:Box=1:Bay=1"}},
			{ref: "ctl:DE00A000/disk:Port=1I:Box=1:Bay=2", typ: "disk", parent: "ctl:DE00A000", id: map[string]string{"serial": "SYNTH-HDD-0002"},
				attrs: map[string]any{"media": "hdd", "health": "critical"}},
			{ref: "psu:HpeServerPowerSupply 1", typ: "psu", parent: "dev", id: map[string]string{"serial": "SYNTH-PSU-B002"},
				attrs: map[string]any{"health": "warning", "capacity_w": 800}},
			{ref: "fan:Fan 3", typ: "fan", attrs: map[string]any{"speed_rpm": 4800}},
			{ref: "fan:Fan 1", typ: "fan", attrs: map[string]any{"speed_pct": 21}},
			// The iLO answering on its dedicated port is "bmc"; the shared port,
			// enabled but without an address, keeps its Redfish id.
			{ref: "bmc:bmc", typ: "interface", parent: "dev", id: map[string]string{"name": "bmc"},
				attrs: map[string]any{"kind": "bmc", "mac": "02:00:5e:20:00:01", "oper_status": "up"}},
			{ref: "bmc:bmc-2", typ: "interface", parent: "dev", id: map[string]string{"name": "bmc-2"},
				attrs: map[string]any{"kind": "bmc", "mac": "02:00:5e:20:00:02", "oper_status": "down"}},
			{ref: "ip:198.51.100.21", typ: "ip_address", attrs: map[string]any{"role": "bmc", "prefix_length": 25}},
		},
	},
	{
		name:    "Dell R330, iDRAC 8",
		profile: IDRAC8,
		counts: map[string]int{
			"device": 1, "cpu": 1, "memory": 2, "module": 1, "disk": 2, "interface": 3,
			"psu": 2, "sensor": 3, "fan": 2, "ip_address": 1,
		},
		// Dell's SKU is the service tag, not a part number.
		absent: []string{"part_number"},
		mustGet: []string{
			"/redfish/v1/Systems/System.Embedded.1/Storage/Drives/", "/redfish/v1/UpdateService/FirmwareInventory/Installed-0",
			"/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel/Entries",
		},
		mustNot: []string{"/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Lclog/Entries", "/redfish/v1/SessionService"},
		objects: []wantObj{
			{ref: "dev", typ: "device", id: map[string]string{
				"serial": "SYNTH-IDRAC8-0003", "hw_uuid": "33333333-4444-4555-8666-777777770003",
				"name": "srv-03.example.invalid", "mgmt_ip": "203.0.113.31",
			}, attrs: map[string]any{
				"vendor": "Dell Inc.", "model": "PowerEdge R330", "bios_version": "2.10.5", "bmc_version": "2.83.83.83",
				"bmc_ip": "203.0.113.31", "health": "critical", "location_led": "blinking", "power_state": "on",
				"cpu_count": 1, "memory_bytes": int64(16) << 30, "power_w": 98, "power_basis": "input", "inlet_temp_c": 22, "outlet_temp_c": 31,
				// The rollback image (Previous-*) is not what runs.
				"firmware_inventory": []string{"BIOS=2.10.5", "Integrated Dell Remote Access Controller=2.83.83.83"},
				// "00:00:00Z" is Dell's placeholder, not a date.
				"firmware": []map[string]any{
					{"name": "BIOS", "version": "2.10.5", "released": "2021-03-05", "software_id": "159", "updateable": true},
					{"name": "Integrated Dell Remote Access Controller", "version": "2.83.83.83", "software_id": "25227", "updateable": true},
				},
				"log_critical_count": 1, "log_warning_count": 1, "log_last_critical": "Fan 2 has failed.", "log_last_critical_at": "2026-10-14T00:00:00Z",
			}},
			{ref: "cpu:CPU.Socket.1", typ: "cpu", parent: "dev", attrs: map[string]any{"cores": 4, "threads": 8}},
			{ref: "dimm:DIMM A2", typ: "memory", parent: "dev", id: map[string]string{"serial": "SYNTH-DIMM-0302"},
				attrs: map[string]any{"health": "critical", "size_bytes": int64(8) << 30}},
			{ref: "ctl:RAID.Integrated.1-1", typ: "module", parent: "dev", id: map[string]string{"name": "RAID.Integrated.1-1"},
				attrs: map[string]any{"kind": "storage_controller", "model": "PERC H330 Mini", "firmware_version": "25.5.5.0005"}},
			{ref: "ctl:RAID.Integrated.1-1/disk:Physical Disk 0:1:0", typ: "disk", parent: "ctl:RAID.Integrated.1-1",
				id:    map[string]string{"serial": "SYNTH-HDD-0301", "wwn": "5000C500A1B2C301"},
				attrs: map[string]any{"media": "hdd", "protocol": "SAS"}},
			// iDRAC reports the life left; trica stores the wear.
			{ref: "ctl:RAID.Integrated.1-1/disk:Solid State Disk 0:1:1", typ: "disk", parent: "ctl:RAID.Integrated.1-1",
				id:    map[string]string{"wwn": "5000C500A1B2C302"},
				attrs: map[string]any{"media": "ssd", "wear_pct": 9}},
			{ref: "nic:NIC.Integrated.1-1-1", typ: "interface", parent: "dev",
				attrs: map[string]any{"oper_status": "up", "speed_mbps": 1000, "description": "System Ethernet Interface"}},
			{ref: "bmc:bmc", typ: "interface", parent: "dev", id: map[string]string{"name": "bmc"},
				attrs: map[string]any{"kind": "bmc", "mac": "02:00:5e:30:00:01"}},
			{ref: "ip:203.0.113.31", typ: "ip_address", attrs: map[string]any{"role": "bmc", "prefix_length": 24}},
			{ref: "psu:PS1 Status", typ: "psu", parent: "dev", id: map[string]string{"serial": "SYNTH-PSU-D001"},
				attrs: map[string]any{"capacity_w": 350, "input_w": 95, "health": "ok"}},
			{ref: "psu:PS2 Status", typ: "psu", attrs: map[string]any{"health": "absent"}},
			// No PhysicalContext on these readings: the name says where they sit.
			{ref: "sensor:System Board Inlet Temp", typ: "sensor", attrs: map[string]any{"placement": "inlet", "reading": 22, "warning_threshold": 42, "critical_threshold": 47}},
			{ref: "sensor:System Board Exhaust Temp", typ: "sensor", attrs: map[string]any{"placement": "outlet"}},
			{ref: "sensor:CPU1 Temp", typ: "sensor", attrs: map[string]any{"placement": "cpu"}},
			{ref: "fan:System Board Fan2A", typ: "fan", attrs: map[string]any{"speed_rpm": 4200, "health": "critical"}},
		},
	},
}

func TestProfiles(t *testing.T) {
	for _, tc := range profileCases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFakeRedfish(t, tc.profile)
			b := collectOnce(t, f.Target("srv"))

			if b.Source != "redfish:srv" || b.Scope != sdk.ScopeFull {
				t.Errorf("source %q scope %q", b.Source, b.Scope)
			}
			if h := b.Health; h == nil || !h.OK || h.Collector != Kind || h.Version == "" || h.Items == 0 {
				t.Errorf("health: %+v", h)
			}
			for typ, n := range tc.counts {
				if got := countType(b, typ); got != n {
					t.Errorf("%s objects: %d, want %d", typ, got, n)
				}
			}
			for _, w := range tc.objects {
				checkObject(t, b, w)
			}
			checkAbsent(t, b, "dev", tc.absent...)

			// Every address of the BMC lands on its interface.
			rels := collecttest.Relations([]*sdk.Batch{b}, "assigned_to")
			if len(rels) != 1 || rels[0].To.Ref != "bmc:bmc" || !strings.HasPrefix(rels[0].From.Ref, "ip:") {
				t.Errorf("assigned_to relations: %+v", rels)
			}
			warn := ""
			if b.Health != nil {
				warn = strings.Join(b.Health.Warnings, "|")
			}
			for _, w := range tc.warns {
				if !strings.Contains(warn, w) {
					t.Errorf("warnings %q lack %q", warn, w)
				}
			}
			if len(tc.warns) == 0 && warn != "" {
				t.Errorf("unexpected warnings: %q", warn)
			}

			// The collector only reads, and walks only what it needs.
			if bad := f.NonReads(); len(bad) > 0 {
				t.Errorf("non-read requests: %v", bad)
			}
			if len(f.Requests()) == 0 {
				t.Fatal("the fake saw no request")
			}
			for _, p := range tc.mustGet {
				if f.Count(p) == 0 {
					t.Errorf("%s was never read", p)
				}
			}
			for _, p := range tc.mustNot {
				if n := f.Count(p); n != 0 {
					t.Errorf("%s was read %d times, it must not be", p, n)
				}
			}
			for _, r := range f.Requests() {
				if r.Method != http.MethodGet {
					t.Errorf("request %s", r)
				}
			}
		})
	}
}

// TestPagingIsFollowed proves the paged event logs are read to the end: the
// iLO 4 IML pages with ?page=, the iDRAC SEL with ?$skip=.
func TestPagingIsFollowed(t *testing.T) {
	for _, tc := range []struct {
		profile Profile
		path    string
		query   string
	}{
		{ILO4, "/redfish/v1/Systems/1/LogServices/IML/Entries", "page=2"},
		{IDRAC8, "/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel/Entries", "$skip=2"},
	} {
		f := NewFakeRedfish(t, tc.profile)
		collectOnce(t, f.Target("srv"))
		found := false
		for _, r := range f.Requests() {
			if r.Path == tc.path && r.RawQuery == tc.query {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: page %s?%s never requested", tc.profile, tc.path, tc.query)
		}
	}
}

func TestRegisteredAndBareHostAddress(t *testing.T) {
	found := false
	for _, k := range collect.Kinds() {
		found = found || k == Kind
	}
	if !found {
		t.Fatalf("kind %q is not registered", Kind)
	}
	f := NewFakeRedfish(t, ILO5)
	tg := f.Target("srv")
	tg.Address = strings.TrimPrefix(f.URL(), "https://") // host:port, no scheme: HTTPS
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != Kind {
		t.Errorf("kind %q", c.Kind())
	}
	b := collecttest.Run(t, c)[0]
	if b.Scope != sdk.ScopeFull || b.Health == nil || !b.Health.OK {
		t.Errorf("health: %+v", b.Health)
	}
}

// TestTotalFailures: the run cannot report anything, so the batch carries only
// a failing health record, in partial scope so that nothing is withdrawn.
func TestTotalFailures(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(f *FakeRedfish) collect.Target
		wantErr string
	}{
		{"credentials refused", func(f *FakeRedfish) collect.Target {
			return f.TargetWithPassword("srv", "wrong-password")
		}, "HTTP 401"},
		{"root unavailable", func(f *FakeRedfish) collect.Target {
			f.FailPath("/redfish/v1/", http.StatusServiceUnavailable)
			return f.Target("srv")
		}, "service root"},
		{"root not json", func(f *FakeRedfish) collect.Target {
			f.MalformPath("/redfish/v1")
			return f.Target("srv")
		}, "malformed JSON"},
		{"no systems link", func(f *FakeRedfish) collect.Target {
			f.Edit("/redfish/v1", func(d map[string]any) { delete(d, "Systems") })
			return f.Target("srv")
		}, "does not advertise Systems"},
		{"systems collection missing", func(f *FakeRedfish) collect.Target {
			f.Delete("/redfish/v1/Systems")
			return f.Target("srv")
		}, "HTTP 404"},
		{"systems collection empty", func(f *FakeRedfish) collect.Target {
			f.Set("/redfish/v1/Systems", map[string]any{"Members": []any{}})
			return f.Target("srv")
		}, "collection is empty"},
		{"systems collection malformed", func(f *FakeRedfish) collect.Target {
			f.MalformPath("/redfish/v1/Systems")
			return f.Target("srv")
		}, "malformed JSON"},
		{"system malformed", func(f *FakeRedfish) collect.Target {
			f.MalformPath("/redfish/v1/Systems/1")
			return f.Target("srv")
		}, "malformed JSON"},
		{"system refused", func(f *FakeRedfish) collect.Target {
			f.FailPath("/redfish/v1/Systems/1", http.StatusForbidden)
			return f.Target("srv")
		}, "HTTP 403"},
		{"timeout", func(f *FakeRedfish) collect.Target {
			f.SlowPath("/redfish/v1/Systems/1", 5*time.Second)
			tg := f.Target("srv")
			tg.Timeout = 200 * time.Millisecond
			return tg
		}, "/redfish/v1/Systems/1"},
		{"unreachable", func(f *FakeRedfish) collect.Target {
			f.srv.Close()
			return f.Target("srv")
		}, "service root"},
		{"secret unset", func(f *FakeRedfish) collect.Target {
			tg := f.Target("srv")
			tg.Password = "env:TRICA_REDFISH_TEST_UNSET_PASSWORD"
			return tg
		}, "environment variable not set"},
		{"no secret", func(f *FakeRedfish) collect.Target {
			tg := f.Target("srv")
			tg.Password = ""
			return tg
		}, "no secret"},
		{"nothing identifies the server", func(f *FakeRedfish) collect.Target {
			f.Set("/redfish/v1/Systems/1", map[string]any{"Name": "anonymous"})
			f.Delete("/redfish/v1/Managers")
			return f.Target("srv")
		}, "identify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFakeRedfish(t, ILO4)
			tg := tc.setup(f)
			b := collectOnce(t, tg)
			if b.Scope != sdk.ScopePartial || len(b.Objects) != 0 || len(b.Relations) != 0 {
				t.Errorf("scope %q with %d objects, want an empty partial batch", b.Scope, len(b.Objects))
			}
			h := b.Health
			if h == nil || h.OK || !strings.Contains(h.Error, tc.wantErr) {
				t.Fatalf("health %+v, want an error containing %q", h, tc.wantErr)
			}
			if h.Target != "srv" || h.Collector != Kind {
				t.Errorf("health %+v", h)
			}
			for _, secret := range []string{FakePassword, "wrong-password"} {
				if strings.Contains(h.Error, secret) {
					t.Errorf("the error leaks a password: %s", h.Error)
				}
			}
			if bad := f.NonReads(); len(bad) > 0 {
				t.Errorf("non-read requests: %v", bad)
			}
		})
	}
}

// TestPartialFailures: one sub-request fails, the rest of the inventory is
// kept, the scope is partial (nothing is withdrawn) and the health says what
// failed.
func TestPartialFailures(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		inject  func(f *FakeRedfish)
		wantErr string
		check   func(t *testing.T, b *sdk.Batch)
	}{
		{"thermal fails", ILO4, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Chassis/1/Thermal", 500) }, "thermal",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "sensor")+countType(b, "fan") != 0 {
					t.Error("sensors or fans reported without the thermal resource")
				}
				checkAbsent(t, b, "dev", "inlet_temp_c")
				checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{"power_w": 181, "bmc_ip": "192.0.2.11"}})
				if countType(b, "psu") != 2 {
					t.Error("the power supplies must survive")
				}
			}},
		{"power malformed", ILO5, func(f *FakeRedfish) { f.MalformPath("/redfish/v1/Chassis/1/Power") }, "malformed JSON",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "psu") != 0 {
					t.Error("psu reported without the power resource")
				}
				checkAbsent(t, b, "dev", "power_w", "power_basis")
				checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{"inlet_temp_c": 24}})
			}},
		{"one log page fails", ILO4, func(f *FakeRedfish) {
			f.FailPath("/redfish/v1/Systems/1/LogServices/IML/Entries", 500)
		}, "event log",
			func(t *testing.T, b *sdk.Batch) {
				// Half a log would mislead: no counter at all.
				checkAbsent(t, b, "dev", "log_critical_count", "log_warning_count", "log_window_days", "log_last_critical")
			}},
		{"one firmware component fails", ILO5, func(f *FakeRedfish) { f.FailPath("/redfish/v1/UpdateService/FirmwareInventory/2", 500) }, "firmware inventory",
			func(t *testing.T, b *sdk.Batch) {
				checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{
					"firmware_inventory": []string{"Smart Array P408i-a SR Gen10=2.62", "iLO 5=2.72 Aug 24 2022"},
					"firmware": []map[string]any{
						{"name": "Smart Array P408i-a SR Gen10", "version": "2.62", "location": "Slot 0", "updateable": true},
						{"name": "iLO 5", "version": "2.72 Aug 24 2022", "released": "2022-08-24", "location": "System Board", "updateable": true},
					},
				}})
			}},
		{"manager unavailable", IDRAC8, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Managers/iDRAC.Embedded.1", 500) }, "manager",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "ip_address") != 0 || hasObject(b, "bmc:bmc") {
					t.Error("BMC objects reported without the manager")
				}
				checkAbsent(t, b, "dev", "bmc_ip", "bmc_version", "log_critical_count")
				if countType(b, "disk") != 2 {
					t.Error("disks must survive")
				}
			}},
		{"one drive fails", IDRAC8, func(f *FakeRedfish) {
			f.FailPath("/redfish/v1/Systems/System.Embedded.1/Storage/Drives/Disk.Bay.1:Enclosure.Internal.0-1:RAID.Integrated.1-1", 404)
		}, "storage drive",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "disk") != 1 || countType(b, "module") != 1 {
					t.Errorf("want the controller and one disk, got %d modules %d disks", countType(b, "module"), countType(b, "disk"))
				}
			}},
		{"one dimm fails", IDRAC8, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Systems/System.Embedded.1/Memory/DIMM.Socket.A2", 500) }, "memory",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "memory") != 1 {
					t.Errorf("memory objects: %d", countType(b, "memory"))
				}
				// The summary, not the sum of the DIMMs that could be read.
				checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{"memory_bytes": int64(16) << 30}})
			}},
		{"host interfaces fail", ILO4, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Systems/1/EthernetInterfaces", 500) }, "host interfaces",
			func(t *testing.T, b *sdk.Batch) {
				if hasObject(b, "nic:1") || !hasObject(b, "bmc:bmc") {
					t.Error("host interfaces must be missing and the BMC interface present")
				}
			}},
		{"processors fail", ILO5, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Systems/1/Processors/2", 500) }, "processors",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "cpu") != 1 {
					t.Errorf("cpus: %d", countType(b, "cpu"))
				}
				checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{"cpu_count": 2}})
			}},
		{"smart storage fails", ILO4, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Systems/1/SmartStorage", 500) }, "smart storage",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "module")+countType(b, "disk") != 0 {
					t.Error("storage reported without the SmartStorage resource")
				}
			}},
		{"update service fails", IDRAC8, func(f *FakeRedfish) { f.FailPath("/redfish/v1/UpdateService", 500) }, "update service",
			func(t *testing.T, b *sdk.Batch) { checkAbsent(t, b, "dev", "firmware_inventory", "firmware") }},
		{"chassis fails", IDRAC8, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Chassis/System.Embedded.1", 500) }, "chassis",
			func(t *testing.T, b *sdk.Batch) {
				if countType(b, "psu")+countType(b, "sensor")+countType(b, "fan") != 0 {
					t.Error("chassis parts reported without the chassis")
				}
			}},
		{"log services fail", IDRAC8, func(f *FakeRedfish) { f.FailPath("/redfish/v1/Managers/iDRAC.Embedded.1/LogServices", 500) }, "log services",
			func(t *testing.T, b *sdk.Batch) { checkAbsent(t, b, "dev", "log_critical_count") }},
		{"log service document fails", IDRAC8, func(f *FakeRedfish) {
			f.FailPath("/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel", 500)
		}, "event log",
			func(t *testing.T, b *sdk.Batch) { checkAbsent(t, b, "dev", "log_critical_count") }},
		{"network interface of the BMC fails", ILO5, func(f *FakeRedfish) {
			f.FailPath("/redfish/v1/Managers/1/EthernetInterfaces/1", 500)
		}, "manager interfaces",
			func(t *testing.T, b *sdk.Batch) {
				if hasObject(b, "ip:198.51.100.21") {
					t.Error("an address reported for an interface that could not be read")
				}
				// The disabled-or-unaddressed port is still there; it is the one left.
				if !hasObject(b, "bmc:bmc") {
					t.Error("the remaining BMC interface must be named bmc")
				}
				checkAbsent(t, b, "dev", "bmc_ip")
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFakeRedfish(t, tc.profile)
			tc.inject(f)
			b := collectOnce(t, f.Target("srv"))
			if b.Scope != sdk.ScopePartial {
				t.Errorf("scope %q, want partial", b.Scope)
			}
			h := b.Health
			if h == nil || h.OK || !strings.Contains(h.Error, tc.wantErr) {
				t.Fatalf("health %+v, want an error containing %q", h, tc.wantErr)
			}
			if !hasObject(b, "dev") || len(object(t, b, "dev").Identity) == 0 {
				t.Fatal("the device must survive a partial failure")
			}
			tc.check(t, b)
			if bad := f.NonReads(); len(bad) > 0 {
				t.Errorf("non-read requests: %v", bad)
			}
		})
	}
}

// TestSlowSubRequest: a section that outlasts the target's timeout leaves a
// partial batch (what was read before it), not an empty one.
func TestSlowSubRequest(t *testing.T) {
	f := NewFakeRedfish(t, ILO5)
	f.SlowPath("/redfish/v1/Chassis/1/Thermal", 5*time.Second)
	tg := f.Target("srv")
	tg.Timeout = 300 * time.Millisecond
	b := collectOnce(t, tg)
	if b.Scope != sdk.ScopePartial || b.Health == nil || b.Health.OK {
		t.Fatalf("scope %q health %+v", b.Scope, b.Health)
	}
	checkObject(t, b, wantObj{ref: "dev", typ: "device", id: map[string]string{"serial": "SYNTH-ILO5-0002"}})
	if countType(b, "cpu") != 2 {
		t.Error("what was read before the timeout must be kept")
	}
}

// TestFakeBehaviour keeps the fake honest: a collector that forgot the
// credentials, or that wrote, would otherwise pass every test.
func TestFakeBehaviour(t *testing.T) {
	f := NewFakeRedfish(t, ILO4)
	hc, err := f.Target("srv").HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, auth bool) int {
		req, err := http.NewRequest(method, f.URL()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth {
			req.SetBasicAuth(FakeUsername, FakePassword)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := do(http.MethodGet, "/redfish/v1/", false); got != http.StatusOK {
		t.Errorf("service root without credentials: %d (BMCs serve it openly)", got)
	}
	if got := do(http.MethodGet, "/redfish/v1/Systems/", false); got != http.StatusUnauthorized {
		t.Errorf("systems without credentials: %d", got)
	}
	if got := do(http.MethodGet, "/redfish/v1/Systems/", true); got != http.StatusOK {
		t.Errorf("systems with credentials: %d", got)
	}
	if got := do(http.MethodGet, "/redfish/v1/Nothing", true); got != http.StatusNotFound {
		t.Errorf("unknown path: %d", got)
	}
	for _, m := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		if got := do(m, "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", true); got != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, got)
		}
	}
	if n := len(f.NonReads()); n != 4 {
		t.Errorf("the fake recorded %d non-read requests, want 4", n)
	}
	if got := f.Requests()[0].String(); got != "GET /redfish/v1" {
		t.Errorf("request rendered as %q", got)
	}
	f.Set("/redfish/v1/x?a=1", map[string]any{"k": 1})
	if got := do(http.MethodGet, "/redfish/v1/x/?a=1", true); got != http.StatusOK {
		t.Errorf("paged document: %d", got)
	}
}

func TestExpandTimes(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"{{now}}":             "2026-10-14T12:00:00Z",
		"{{now-1d}}":          "2026-10-13T12:00:00Z",
		"{{now+3h}}":          "2026-10-14T15:00:00Z",
		"{{now-2d@-05:00}}":   "2026-10-12T07:00:00-05:00",
		"{{now@+02:30}}":      "2026-10-14T14:30:00+02:30",
		"a {{now-12h}} b":     "a 2026-10-14T00:00:00Z b",
		"no template at all.": "no template at all.",
	} {
		if got := expandTimes(in, now); got != want {
			t.Errorf("expandTimes(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFallbacks exercises the paths a real BMC takes when its documents are
// sparser than the fixtures: links missing from the system, paged collections,
// summary figures absent.
func TestFallbacks(t *testing.T) {
	t.Run("chassis and manager found from the service root", func(t *testing.T) {
		f := NewFakeRedfish(t, ILO4)
		f.Set("/redfish/v1/Systems/1", map[string]any{"SerialNumber": "SYNTH-MIN-0001", "Manufacturer": "Acme", "SKU": "ignored"})
		b := collectOnce(t, f.Target("srv"))
		if b.Health == nil || !b.Health.OK {
			t.Fatalf("health: %+v", b.Health)
		}
		checkObject(t, b, wantObj{ref: "dev", typ: "device", id: map[string]string{"serial": "SYNTH-MIN-0001", "mgmt_ip": "192.0.2.11"},
			attrs: map[string]any{"power_w": 181, "bmc_ip": "192.0.2.11", "bmc_version": "iLO 4 v2.78"}})
		checkAbsent(t, b, "dev", "part_number", "power_state", "location_led", "cpu_count", "memory_bytes", "health", "bios_version")
		// Neither HPE nor the logs: no part number, no log service link.
		checkAbsent(t, b, "dev", "log_critical_count")
	})
	t.Run("paged collection, with a link that loops", func(t *testing.T) {
		f := NewFakeRedfish(t, ILO5)
		f.Set("/redfish/v1/Systems/1/Processors", map[string]any{
			"Members":                []any{map[string]any{"@odata.id": "/redfish/v1/Systems/1/Processors/1/"}},
			"Members@odata.nextLink": "/redfish/v1/Systems/1/Processors?page=2",
		})
		f.Set("/redfish/v1/Systems/1/Processors?page=2", map[string]any{
			"Members":                []any{map[string]any{"@odata.id": "/redfish/v1/Systems/1/Processors/2/"}},
			"Members@odata.nextLink": "/redfish/v1/Systems/1/Processors?page=2",
		})
		b := collectOnce(t, f.Target("srv"))
		if countType(b, "cpu") != 2 {
			t.Errorf("cpus: %d", countType(b, "cpu"))
		}
	})
	t.Run("absolute odata ids", func(t *testing.T) {
		f := NewFakeRedfish(t, ILO5)
		f.Edit("/redfish/v1/Systems/1", func(d map[string]any) {
			d["Processors"] = map[string]any{"@odata.id": f.URL() + "/redfish/v1/Systems/1/Processors/"}
		})
		if got := countType(collectOnce(t, f.Target("srv")), "cpu"); got != 2 {
			t.Errorf("cpus: %d", got)
		}
	})
	t.Run("memory total from the DIMMs when the summary is absent", func(t *testing.T) {
		f := NewFakeRedfish(t, IDRAC8)
		f.Edit("/redfish/v1/Systems/System.Embedded.1", func(d map[string]any) { delete(d, "MemorySummary"); delete(d, "ProcessorSummary") })
		b := collectOnce(t, f.Target("srv"))
		checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{"memory_bytes": int64(16) << 30, "cpu_count": 1}})
	})
	t.Run("memory total from TotalSystemMemoryGB", func(t *testing.T) {
		f := NewFakeRedfish(t, IDRAC8)
		f.Edit("/redfish/v1/Systems/System.Embedded.1", func(d map[string]any) {
			d["MemorySummary"] = map[string]any{"TotalSystemMemoryGB": 16}
		})
		checkObject(t, collectOnce(t, f.Target("srv")), wantObj{ref: "dev", typ: "device", attrs: map[string]any{"memory_bytes": int64(16) << 30}})
	})
	t.Run("log service without an entries link", func(t *testing.T) {
		f := NewFakeRedfish(t, IDRAC8)
		f.Set("/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel", map[string]any{"Id": "Sel"})
		checkObject(t, collectOnce(t, f.Target("srv")), wantObj{ref: "dev", typ: "device", attrs: map[string]any{"log_critical_count": 1}})
	})
	t.Run("log service listed under the system", func(t *testing.T) {
		f := NewFakeRedfish(t, IDRAC8)
		f.Edit("/redfish/v1/Systems/System.Embedded.1", func(d map[string]any) {
			d["LogServices"] = map[string]any{"@odata.id": "/redfish/v1/Systems/System.Embedded.1/LogServices"}
		})
		f.Set("/redfish/v1/Systems/System.Embedded.1/LogServices", map[string]any{"Members": []any{
			map[string]any{"@odata.id": "/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel"},
		}})
		// Listed under the system and under the manager: read once.
		b := collectOnce(t, f.Target("srv"))
		if b.Health == nil || !b.Health.OK {
			t.Fatalf("health %+v", b.Health)
		}
		checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{"log_critical_count": 1, "log_warning_count": 1}})
	})
	t.Run("BMC interfaces without any address", func(t *testing.T) {
		f := NewFakeRedfish(t, ILO5)
		// Neither port has an address: the first is the BMC interface.
		f.Edit("/redfish/v1/Managers/1/EthernetInterfaces/1", func(d map[string]any) { d["IPv4Addresses"] = []any{} })
		b := collectOnce(t, f.Target("srv"))
		if !hasObject(b, "bmc:bmc") || !hasObject(b, "bmc:bmc-2") || countType(b, "ip_address") != 0 {
			t.Errorf("bmc interfaces: %v", refs(b, "interface"))
		}
		checkAbsent(t, b, "dev", "bmc_ip")
		if len(object(t, b, "dev").Identity) == 0 {
			t.Error("the device is still identified by its serial")
		}
	})
	t.Run("storage falls back to the OEM tree when the standard one is empty", func(t *testing.T) {
		f := NewFakeRedfish(t, ILO5)
		f.Set("/redfish/v1/Systems/1/Storage", map[string]any{"Members": []any{}})
		f.Edit("/redfish/v1/Systems/1/SmartStorage/ArrayControllers/0", func(d map[string]any) {
			d["Model"] = "OEM controller"
			d["SerialNumber"] = "SYNTH-OEM-0001"
			d["Location"] = "Slot 9"
		})
		b := collectOnce(t, f.Target("srv"))
		checkObject(t, b, wantObj{ref: "ctl:Slot 9", typ: "module", id: map[string]string{"serial": "SYNTH-OEM-0001"}, attrs: map[string]any{"model": "OEM controller"}})
	})
}

func refs(b *sdk.Batch, typ string) []string {
	var out []string
	for _, o := range b.Objects {
		if o.Type == typ {
			out = append(out, o.Ref)
		}
	}
	return out
}

func TestLogOptions(t *testing.T) {
	cases := []struct {
		name     string
		profile  Profile
		options  string
		critical int
		warning  int
		window   int
		noLogs   bool
	}{
		{"default window", ILO4, "", 2, 2, 30, false},
		{"explicit default", ILO4, "log_days: 30", 2, 2, 30, false},
		{"wider window reaches the oldest critical", ILO4, "log_days: 60", 3, 2, 60, false},
		{"narrow window", ILO4, "log_days: 5", 1, 1, 5, false},
		{"empty window still counts zero", ILO4, "log_days: 2", 1, 0, 2, false},
		{"skip logs", ILO4, "skip_logs: true", 0, 0, 0, true},
		{"dell window with a zone offset", IDRAC8, "log_days: 1", 1, 0, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFakeRedfish(t, tc.profile)
			tg := f.Target("srv")
			if tc.options != "" {
				tg = withOptions(t, tg, tc.options)
			}
			b := collectOnce(t, tg)
			if tc.noLogs {
				checkAbsent(t, b, "dev", "log_window_days", "log_critical_count", "log_warning_count", "log_last_critical", "log_last_critical_at")
				if n := f.Count("/redfish/v1/Systems/1/LogServices"); n != 0 {
					t.Errorf("%d requests to the log services with skip_logs", n)
				}
				return
			}
			checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{
				"log_window_days": tc.window, "log_critical_count": tc.critical, "log_warning_count": tc.warning,
			}})
		})
	}
}

func TestLastCriticalIsBoundedAndTrimmed(t *testing.T) {
	f := NewFakeRedfish(t, ILO5)
	f.Set("/redfish/v1/Systems/1/LogServices/IML/Entries", map[string]any{"Members": []any{
		map[string]any{"Severity": "Critical", "Created": "2026-10-13T10:00:00Z", "Message": strings.Repeat("x", 800)},
		map[string]any{"Severity": "CRITICAL", "Created": "2026-10-12T10:00:00Z", "Message": "older"},
	}})
	b := collectOnce(t, f.Target("srv"))
	got, _ := object(t, b, "dev").Attributes["log_last_critical"].(string)
	if n := len([]rune(got)); n != 500 || !strings.HasSuffix(got, "…") {
		t.Errorf("last critical: %d runes, want 500 ending with an ellipsis", n)
	}
	checkObject(t, b, wantObj{ref: "dev", typ: "device", attrs: map[string]any{"log_critical_count": 2, "log_last_critical_at": "2026-10-13T10:00:00Z"}})
}

func TestNew(t *testing.T) {
	good := collect.Target{Kind: Kind, Name: "srv", Address: "192.0.2.1", Username: "u", Password: "env:X"}
	cases := []struct {
		name    string
		mutate  func(tg *collect.Target)
		wantErr string
	}{
		{"no address", func(tg *collect.Target) { tg.Address = "" }, "no address"},
		{"not a url", func(tg *collect.Target) { tg.Address = "http://" }, "not a host or URL"},
		{"bad scheme", func(tg *collect.Target) { tg.Address = "ftp://192.0.2.1" }, "scheme"},
		{"bad tls version", func(tg *collect.Target) { tg.TLS.MinVersion = "0.9" }, "min_version"},
		{"unknown option", func(tg *collect.Target) {
			if err := yaml.Unmarshal([]byte("log_dayz: 3"), &tg.Options); err != nil {
				t.Fatal(err)
			}
		}, "log_dayz"},
		{"negative window", func(tg *collect.Target) {
			if err := yaml.Unmarshal([]byte("log_days: -3"), &tg.Options); err != nil {
				t.Fatal(err)
			}
		}, "log_days"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg := good
			tc.mutate(&tg)
			_, err := New(tg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
	for _, addr := range []string{"192.0.2.1", "192.0.2.1:8443", "https://bmc.example.invalid", "http://192.0.2.1:8000/"} {
		g := good
		g.Address = addr
		if _, err := New(g); err != nil {
			t.Errorf("address %q: %v", addr, err)
		}
	}
}

func TestMemoryTechnology(t *testing.T) {
	for in, want := range map[string]string{
		"DDR4": "DDR4", "DDR4_SDRAM": "DDR4", "DDR4E_SDRAM": "DDR4", "ddr5": "DDR5", "DDR3_SDRAM\t": "DDR3",
		"LPDDR5_SDRAM": "LPDDR5", "HBM2": "HBM2", "DRAM": "", "Unknown": "", "": "", "Other": "",
	} {
		if got := memoryTechnology(in); got != want {
			t.Errorf("memoryTechnology(%q) = %q, want %q", in, got, want)
		}
	}
}
