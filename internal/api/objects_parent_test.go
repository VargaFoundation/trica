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
