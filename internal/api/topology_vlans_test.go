package api_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/internal/collect/mikrotik"
	"github.com/VargaFoundation/trica/internal/collect/pfsense"
	"github.com/VargaFoundation/trica/internal/reconcile"
)

// vlanFirewall runs the pfSense collector against its fake, as target
// "pfsense-1", with the given options ("" for none), and applies the batch.
func (e *env) vlanFirewall(options string) {
	e.t.Helper()
	f, err := pfsense.NewFake(pfsense.Version272)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(f.Close)
	tg := f.Target("pfsense-1")
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

// vlanSwitch runs the MikroTik collector against its fake, as target "sw-1",
// with default options, and applies the batch.
func (e *env) vlanSwitch() {
	e.t.Helper()
	f, err := mikrotik.NewFake(mikrotik.FakeCRS354v78)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = f.Close() })
	secret := filepath.Join(e.t.TempDir(), "password")
	if err := os.WriteFile(secret, []byte(mikrotik.FakePassword), 0o600); err != nil {
		e.t.Fatal(err)
	}
	c, err := collect.New(collect.Target{
		Kind: mikrotik.Kind, Name: "sw-1", Address: f.Addr(),
		Username: mikrotik.FakeUser, Password: collect.Secret("file:" + secret), Timeout: 10 * time.Second,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range collecttest.Run(e.t, c) {
		e.apply(b)
	}
}

// vlanObjects returns the VLAN objects of the API with the given VID, by id.
func (e *env) vlanObjects(vid int) map[string]map[string]any {
	e.t.Helper()
	out := map[string]map[string]any{}
	for _, raw := range e.page("/api/v1/objects?type=vlan&limit=1000", tokReader).Items {
		var o map[string]any
		if err := json.Unmarshal(raw, &o); err != nil {
			e.t.Fatal(err)
		}
		attrs, _ := o["attributes"].(map[string]any)
		if v, _ := attrs["vid"].(float64); int(v) == vid {
			out[o["id"].(string)] = o
		}
	}
	return out
}

// memberOf returns, by interface name, the ids of the VLANs the interface is
// a member of.
func (e *env) memberOf() map[string][]string {
	e.t.Helper()
	out := map[string][]string{}
	for _, l := range links(e.t, e.page("/api/v1/relations?type=member_of&limit=1000", tokReader)) {
		out[l.From.Display] = append(out[l.From.Display], l.ToID)
	}
	return out
}

// The firewall and the switch both carry VLAN 20, without any option: they
// report one VLAN object, keyed by the shared default layer-2 domain, not one
// per source (ADR 0050).
func TestOneVLANAcrossFirewallAndSwitch(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.vlanFirewall("")
	e.vlanSwitch()

	got := e.vlanObjects(20)
	if len(got) != 1 {
		t.Fatalf("%d VLAN objects with VID 20, want 1: %v", len(got), got)
	}
	var id string
	for k, o := range got {
		id = k
		if o["status"] != "active" || o["display"] != "20" {
			t.Errorf("VLAN 20 = %v", o)
		}
		if a := o["attributes"].(map[string]any); a["domain"] != "default" {
			t.Errorf("VLAN 20 attributes %v", a)
		}
	}
	member := e.memberOf()
	if !slices.Equal(member["igb1.20"], []string{id}) {
		t.Errorf("the firewall's igb1.20 is a member of %v, want the shared VLAN %q", member["igb1.20"], id)
	}
	// The switch's VLAN 20 ports (ether2 to ether5, bond0, sfp-sfpplus3).
	if !slices.Contains(member["ether2"], id) || !slices.Contains(member["bond0"], id) {
		t.Errorf("the switch's ports are not members of the shared VLAN %q: %v", id, member)
	}
}

// Moving a firewall from its old target-named domain to the default one leaves
// the old VLAN object to the sweep: absent, with its history, never deleted;
// the membership moves to the new object.
func TestVLANDomainChangeLeavesTheOldVLANAbsent(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.vlanFirewall("{vlan_domain: pfsense-1}")
	old := e.vlanObjects(20)
	if len(old) != 1 {
		t.Fatalf("%d VLAN 20 before the change, want 1", len(old))
	}
	var oldID string
	for k := range old {
		oldID = k
	}
	if m := e.memberOf()["igb1.20"]; !slices.Equal(m, []string{oldID}) {
		t.Fatalf("igb1.20 is a member of %v, want %q", m, oldID)
	}

	e.clk.advance(time.Hour)
	e.vlanFirewall("")
	m := e.memberOf()["igb1.20"]
	if len(m) != 1 || m[0] == oldID {
		t.Fatalf("igb1.20 is a member of %v, want the new VLAN only (old %q)", m, oldID)
	}
	newID := m[0]

	// Past the default absence delay for the old object, not for the new one.
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
	// Nothing was deleted: the old object still answers, with its history.
	if p := e.page("/api/v1/objects/"+oldID+"/history", tokReader); p.Total == 0 {
		t.Error("the old VLAN has lost its history")
	}
}
