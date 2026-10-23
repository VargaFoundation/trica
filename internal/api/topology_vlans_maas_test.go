package api_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/internal/collect/maas"
	"github.com/VargaFoundation/trica/internal/reconcile"
)

// vlanMAAS runs the MAAS collector against its fake, as target "maas-1", with
// the given options ("" for none), and applies the batch.
func (e *env) vlanMAAS(options string) {
	e.t.Helper()
	f := maas.NewFake(maas.V35)
	e.t.Cleanup(f.Close)
	secret := filepath.Join(e.t.TempDir(), "apikey")
	if err := os.WriteFile(secret, []byte(f.APIKey()), 0o600); err != nil {
		e.t.Fatal(err)
	}
	tg := collect.Target{
		Kind: maas.Kind, Name: "maas-1", Address: f.URL(),
		Password: collect.Secret("file:" + secret), Timeout: 10 * time.Second,
	}
	if options != "" {
		var w struct {
			Options yaml.Node `yaml:"options"`
		}
		if err := yaml.Unmarshal([]byte("options: "+options), &w); err != nil {
			e.t.Fatal(err)
		}
		tg.Options = w.Options
	}
	c, err := collect.New(tg)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range collecttest.Run(e.t, c) {
		e.apply(b)
	}
}

// MAAS's untagged VLAN, mapped to the VID the switches use, is the switches'
// VLAN object: one object, with the MAAS interfaces and the switch ports as
// members (#148, ADR 0050).
func TestMAASUntaggedVLANIsTheSwitchVLAN(t *testing.T) {
	const opts = "{vlan_domains: {fabric-0: default}, untagged_vids: {fabric-0: 20}}"

	e := newEnv(t, reconcile.Policy{})
	e.vlanSwitch()
	e.vlanMAAS(opts)

	got := e.vlanObjects(20)
	if len(got) != 1 {
		t.Fatalf("%d VLAN objects with VID 20, want 1: %v", len(got), got)
	}
	var id string
	for k, o := range got {
		id = k
		if o["status"] != "active" {
			t.Errorf("VLAN 20 = %v", o)
		}
		if a := o["attributes"].(map[string]any); a["domain"] != "default" {
			t.Errorf("VLAN 20 attributes %v", a)
		}
	}
	member := e.memberOf()
	if !slices.Contains(member["eno1"], id) {
		t.Errorf("MAAS's eno1 is a member of %v, want the shared VLAN %q", member["eno1"], id)
	}
	if !slices.Contains(member["ether2"], id) {
		t.Errorf("the switch's ether2 is a member of %v, want the shared VLAN %q", member["ether2"], id)
	}
	if got := e.vlanObjects(0); len(got) != 0 {
		for _, o := range got {
			if a := o["attributes"].(map[string]any); a["domain"] == "default" {
				t.Errorf("a VID 0 VLAN is left in the default domain: %v", o)
			}
		}
	}
}

// Without the option MAAS's untagged VLAN is a second object, beside the
// switch's (the behaviour #148 reported).
func TestMAASUntaggedVLANWithoutTheOption(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.vlanSwitch()
	e.vlanMAAS("{vlan_domains: {fabric-0: default}}")
	if got := e.vlanObjects(20); len(got) != 1 {
		t.Fatalf("%d VLAN 20, want the switch's alone", len(got))
	}
	if got := e.vlanObjects(0); len(got) == 0 {
		t.Fatal("without untagged_vids MAAS's untagged VLAN (VID 0) should be reported")
	}
}

// Turning the option on leaves the old <fabric>:0 object to the sweep: absent,
// with its history, never deleted; the new one is active.
func TestMAASUntaggedMappingLeavesTheOldVLANAbsent(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.vlanMAAS("{vlan_domains: {fabric-0: default}}")
	var oldID string
	for id, o := range e.vlanObjects(0) {
		if o["attributes"].(map[string]any)["domain"] == "default" {
			oldID = id
		}
	}
	if oldID == "" {
		t.Fatal("no default:0 VLAN before the mapping")
	}

	e.clk.advance(time.Hour)
	e.vlanMAAS("{vlan_domains: {fabric-0: default}, untagged_vids: {fabric-0: 203}}")
	var newID string
	for id := range e.vlanObjects(203) {
		newID = id
	}
	if newID == "" || newID == oldID {
		t.Fatalf("new VLAN %q, old %q", newID, oldID)
	}

	e.clk.advance(reconcile.DefaultAbsentAfter - 5*time.Minute)
	if _, err := e.eng.SweepAbsent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.get("/api/v1/objects/"+oldID, tokReader).obj(t)["status"]; got != "absent" {
		t.Errorf("old VLAN status %v, want absent", got)
	}
	if got := e.get("/api/v1/objects/"+newID, tokReader).obj(t)["status"]; got != "active" {
		t.Errorf("new VLAN status %v, want active", got)
	}
	if p := e.page("/api/v1/objects/"+oldID+"/history", tokReader); p.Total == 0 {
		t.Error("the old VLAN has lost its history")
	}
}
