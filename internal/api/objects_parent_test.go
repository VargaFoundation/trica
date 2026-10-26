package api_test

import (
	"encoding/json"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

type listedParent struct {
	Display string         `json:"display"`
	Parent  *api.ObjectRef `json:"parent"`
	Device  *api.ObjectRef `json:"device"`
}

func (e *env) listed(path, token string) map[string]listedParent {
	e.t.Helper()
	out := map[string]listedParent{}
	for _, raw := range e.page(path, token).Items {
		var it listedParent
		if err := json.Unmarshal(raw, &it); err != nil {
			e.t.Fatal(err)
		}
		out[it.Display] = it
	}
	return out
}

// A list item says what it is part of: a component names its server (display
// and type), an object with no parent names nothing, and a parent the caller
// may not read is left out rather than named.
func TestObjectsListNamesTheParent(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := sdk.NewBatch("agent:srv-01", sdk.ScopeFull)
	b.Object("srv", "device").Identify("serial", "SRV-01-SN").Identify("name", "srv-01").Set("role", "server")
	b.Object("cpu", "cpu").Identify("name", "Proc 1").In("srv").Set("model", "Intel(R) Xeon(R) CPU E5-2695 v4 @ 2.10GHz")
	e.apply(b)

	items := e.listed("/api/v1/objects?type=cpu", tokReader)
	cpu, ok := items["Proc 1 · Intel Xeon E5-2695 v4 @ 2.10GHz"]
	if !ok {
		t.Fatalf("no cpu with its readable name: %v", items)
	}
	if cpu.Parent == nil || cpu.Parent.Display != "srv-01" || cpu.Parent.Type != "device" || cpu.Parent.ID != e.ids["srv"] {
		t.Fatalf("parent of the cpu: %+v", cpu.Parent)
	}
	if dev := e.listed("/api/v1/objects?type=device", tokReader)["srv-01"]; dev.Parent != nil {
		t.Fatalf("a device without parent names one: %+v", dev.Parent)
	}
	// A caller who reads cpus but not devices is not told the name of the server.
	e.auth.Tokens["t-cpu-only"] = api.Grant{Principal: api.Principal{Subject: "kim", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Types: []string{"cpu"}}
	for _, it := range e.listed("/api/v1/objects?type=cpu", "t-cpu-only") {
		if it.Parent != nil {
			t.Fatalf("the parent of a type the caller cannot read is named: %+v", it.Parent)
		}
	}
}

// A drive in a controller names the controller as its parent and the server
// above it as its device; a component whose parent is the server names no
// device; a caller who cannot read devices is not told the server.
func TestObjectsListNamesTheDeviceAboveTheParent(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	b := sdk.NewBatch("agent:srv-02", sdk.ScopeFull)
	b.Object("srv", "device").Identify("serial", "SRV-02-SN").Identify("name", "srv-02").Set("role", "server")
	b.Object("ctl", "module").Identify("name", "Slot 0").In("srv").Set("model", "Smart Array P440ar Controller").Set("kind", "storage_controller")
	b.Object("dsk", "disk").Identify("name", "1I:0:1").In("ctl").Set("media", "ssd").Set("capacity_bytes", 500107837440).Set("model", "CT500MX500SSD1")
	b.Object("psu", "psu").Identify("name", "PS 1").In("srv")
	e.apply(b)

	disks := e.listed("/api/v1/objects?type=disk", tokReader)
	d, ok := disks["1I:0:1 · SSD 500 GB CT500MX500SSD1"]
	if !ok {
		t.Fatalf("no drive with its readable name: %v", disks)
	}
	if d.Parent == nil || d.Parent.ID != e.ids["ctl"] || d.Parent.Display != "Slot 0 · Smart Array P440ar Controller" {
		t.Fatalf("parent of the drive: %+v", d.Parent)
	}
	if d.Device == nil || d.Device.ID != e.ids["srv"] || d.Device.Display != "srv-02" || d.Device.Type != "device" {
		t.Fatalf("device of the drive: %+v", d.Device)
	}
	for _, it := range e.listed("/api/v1/objects?type=psu", tokReader) {
		if it.Device != nil {
			t.Fatalf("a component whose parent is the server names a device: %+v", it.Device)
		}
	}
	e.auth.Tokens["t-no-device"] = api.Grant{Principal: api.Principal{Subject: "kim", Kind: api.KindUser}, Actions: []string{api.ActionRead}, Types: []string{"disk", "module"}}
	for _, it := range e.listed("/api/v1/objects?type=disk", "t-no-device") {
		if it.Device != nil {
			t.Fatalf("the device of a type the caller cannot read is named: %+v", it.Device)
		}
		if it.Parent == nil {
			t.Fatal("the readable controller is not named")
		}
	}
}
