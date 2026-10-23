package maas

import (
	"net/http"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// TestUntaggedVIDs: options.untagged_vids reports a fabric's untagged VLAN
// under the VID the switches use, as the switches' object (#148).
func TestUntaggedVIDs(t *testing.T) {
	f := newFake(t, V35)
	b := collectFrom(t, targetFor(t, f, "{vlan_domains: {fabric-0: default}, untagged_vids: {fabric-0: 203}}"))

	v := obj(t, b, "vlan/default:203")
	wantIdentity(t, v, map[string]string{"key": "default:203"})
	wantAttrs(t, v, map[string]any{"vid": int64(203), "domain": "default", "dhcp_on": true})
	wantNoAttrs(t, v, "name") // MAAS's default name must not compete with the switches'
	noObj(t, b, "vlan/default:0")
	noObj(t, b, "vlan/fabric-0:0")

	for _, ifc := range []string{"fk0a01/if/eno1", "fk0a01/if/eno2", "fk0a01/if/eno3", "fk0a01/if/bond0", "fk0a02/if/eno1", "fk0a03/if/eno1"} {
		r := rel(b, "member_of", "machine/"+ifc, "vlan/default:203")
		if r == nil || r.Attributes["tagged"] != false {
			t.Errorf("%s -> mapped untagged VLAN: %+v (membership must stay untagged)", ifc, r)
		}
	}
	if r := rel(b, "member_of", "machine/fk0a01/if/bond0.204", "vlan/default:204"); r == nil || r.Attributes["tagged"] != true {
		t.Errorf("bond0.204 -> VLAN 204: %+v", r)
	}
	wantAttrs(t, obj(t, b, "prefix/192.0.2.0/24"), map[string]any{"vlan": int64(203)})
	if rel(b, "assigned_to", "prefix/192.0.2.0/24", "vlan/default:203") == nil {
		t.Error("the prefix is not on the mapped VLAN")
	}
	wantAttrs(t, obj(t, b, "vlan/fabric-1:0"), map[string]any{"vid": int64(0), "name": "untagged"})
	if len(b.Health.Warnings) != 0 {
		t.Errorf("warnings: %v", b.Health.Warnings)
	}
}

// TestUntaggedVIDsOptionIsDescribed: the option shows in the descriptor as a
// map of integers (ADR 0055).
func TestUntaggedVIDsOptionIsDescribed(t *testing.T) {
	fields, err := Descriptor.OptionFields()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if f.Name == "untagged_vids" {
			if f.Kind != collect.OptionMap || f.ValueKind != collect.OptionInteger {
				t.Errorf("untagged_vids = %s of %s, want a map of integers", f.Kind, f.ValueKind)
			}
			return
		}
	}
	t.Error("untagged_vids is not in the descriptor")
}

// TestUntaggedVIDKeepsAnOperatorName: only MAAS's default name is dropped.
func TestUntaggedVIDKeepsAnOperatorName(t *testing.T) {
	bd := &builder{b: sdk.NewBatch("s", sdk.ScopeFull), untagged: map[string]int{"f": 10}}
	bd.vlan(&vlanInfo{Fabric: "f", Name: "PXE"})
	wantAttrs(t, obj(t, bd.b, "vlan/f:10"), map[string]any{"name": "PXE"})
	bd.vlan(&vlanInfo{Fabric: "g", Name: "untagged"})
	wantAttrs(t, obj(t, bd.b, "vlan/g:0"), map[string]any{"name": "untagged"})
}

// TestUntaggedVIDOfAnEmptyFabric: a fabric no one is on reports nothing, mapped or not.
func TestUntaggedVIDOfAnEmptyFabric(t *testing.T) {
	f := newFake(t, V35)
	b := collectFrom(t, targetFor(t, f, "untagged_vids: {fabric-2: 99}"))
	noObj(t, b, "vlan/fabric-2:99")
	noObj(t, b, "vlan/fabric-2:0")
}

// TestUntaggedVIDCollision: a fabric that already has the VID tagged is not remapped.
func TestUntaggedVIDCollision(t *testing.T) {
	f := newFake(t, V35)
	b := collectFrom(t, targetFor(t, f, "untagged_vids: {fabric-1: 30}"))
	for _, ifc := range []string{"fk0a01/if/eno4", "fk0a01/if/br0"} {
		if rel(b, "member_of", "machine/"+ifc, "vlan/fabric-1:0") == nil {
			t.Errorf("%s left the untagged VLAN of fabric-1", ifc)
		}
	}
	wantAttrs(t, obj(t, b, "vlan/fabric-1:30"), map[string]any{"name": "backup"})
	if w := b.Health.Warnings; len(w) != 1 || !strings.Contains(w[0], "fabric-1") || !strings.Contains(w[0], "30") {
		t.Errorf("warnings = %v, want one naming fabric-1 and 30", w)
	}
}

// TestUntaggedVIDUnknownFabric: a typo is reported, unless fabrics/ was not read.
func TestUntaggedVIDUnknownFabric(t *testing.T) {
	f := newFake(t, V35)
	b := collectFrom(t, targetFor(t, f, "untagged_vids: {fabric-9: 203}"))
	if w := b.Health.Warnings; len(w) != 1 || !strings.Contains(w[0], "fabric-9") {
		t.Errorf("warnings = %v, want one naming fabric-9", w)
	}

	f = newFake(t, V35)
	f.FailEndpoint(EndpointFabrics, http.StatusForbidden)
	b = collectFrom(t, targetFor(t, f, "{vlan_domains: {fabric-0: default}, untagged_vids: {fabric-9: 203, fabric-0: 203}}"))
	for _, w := range b.Health.Warnings {
		if strings.Contains(w, "untagged_vids") {
			t.Errorf("warning without fabrics/: %q", w)
		}
	}
	// The mapping still applies on the partial batch.
	obj(t, b, "vlan/default:203")
}

func TestUntaggedVIDsResolution(t *testing.T) {
	fabrics := []fabric{
		{Name: "a", VLANs: []vlanInfo{{VID: 0}, {VID: 10}}},
		{Name: "b", VLANs: []vlanInfo{{VID: 0}}},
	}
	tests := []struct {
		name   string
		opt    map[string]int
		known  bool
		want   map[string]int
		nwarns int
	}{
		{name: "none", known: true},
		{name: "free", opt: map[string]int{"b": 10}, known: true, want: map[string]int{"b": 10}},
		{name: "clash", opt: map[string]int{"a": 10}, known: true, want: map[string]int{}, nwarns: 1},
		{name: "unknown", opt: map[string]int{"z": 10}, known: true, want: map[string]int{}, nwarns: 1},
		{name: "fabrics unread", opt: map[string]int{"z": 10, "a": 10}, want: map[string]int{"z": 10, "a": 10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, w := untaggedVIDs(tc.opt, fabrics, tc.known)
			if len(w) != tc.nwarns || len(got) != len(tc.want) {
				t.Fatalf("got %v, %v; want %v, %d warnings", got, w, tc.want, tc.nwarns)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %d, want %d", k, got[k], v)
				}
			}
		})
	}
}
