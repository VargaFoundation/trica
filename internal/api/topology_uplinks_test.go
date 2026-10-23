package api_test

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// uplinksSnapshot is what the UI's topology model is tested against
// (web/src/topology/uplinks-api.test.ts): the answers of the real API over
// uplinks a collector wrote, not a hand-written mock.
const uplinksSnapshot = "../../web/src/topology/testdata/uplinks-api.json"

// observeUplinks adds, on top of the fixtures, what a controller reports
// without naming any port (ADR 0060), and one cable it does not know about:
//
//	ap-01 (Paris, no port)  uplinked_to  sw-a2   no cable between them
//	sw-a2                   uplinked_to  sw-a1   a cable joins them as well
//	srv-01 (Lyon)           uplinked_to  sw-a1   crosses sites
//	sw-a1 Ethernet1 <-> sw-a2 Ethernet1          cabled_to, observed
func (e *env) observeUplinks() {
	e.t.Helper()
	b := sdk.NewBatch("unifi:office-lan", sdk.ScopePartial)
	b.Confidence = sdk.Observed
	b.Object("ap01", "device").Identify("serial", "AP-01-SN").Identify("name", "ap-01").Set("role", "access_point")
	b.Object("sw2", "device").Identify("serial", "JPE-SW-A2").Identify("name", "sw-a2").Set("role", "switch")
	b.Object("a2e1", "interface").Identify("name", "Ethernet1").In("sw2")
	b.Relations = append(b.Relations,
		&sdk.Relation{Type: "mounted_in", From: sdk.Ref("ap01"), To: byID("rack", e.ids["rack-p"]), Attributes: map[string]any{}},
		&sdk.Relation{Type: "mounted_in", From: sdk.Ref("sw2"), To: byID("rack", e.ids["rack-p"]), Attributes: map[string]any{}},
	)
	e.apply(b)

	u := sdk.NewBatch("unifi:office-lan", sdk.ScopePartial)
	u.Confidence = sdk.Observed
	u.Relations = append(u.Relations,
		&sdk.Relation{Type: "uplinked_to", From: byID("device", e.ids["ap01"]), To: byID("device", e.ids["sw2"]), Attributes: map[string]any{}},
		&sdk.Relation{Type: "uplinked_to", From: byID("device", e.ids["sw2"]), To: byID("device", e.ids["sw"]), Attributes: map[string]any{}},
		&sdk.Relation{Type: "uplinked_to", From: byID("device", e.ids["srv"]), To: byID("device", e.ids["sw"]), Attributes: map[string]any{}},
	)
	e.apply(u)

	c := sdk.NewBatch("lldp:test", sdk.ScopePartial)
	c.Confidence = sdk.Observed
	c.Relations = append(c.Relations,
		&sdk.Relation{Type: "cabled_to", From: byID("interface", e.ids["e1"]), To: byID("interface", e.ids["a2e1"]), Attributes: map[string]any{}},
	)
	e.apply(c)
}

const topologyTypes = "cabled_to,connects,neighbor_of,uplinked_to"

func ofType(ls []link, typ string) []link {
	var out []link
	for _, l := range ls {
		if l.Type == typ {
			out = append(out, l)
		}
	}
	return out
}

// The topology page reads uplinks through the same bulk call as cables. This
// pins what it relies on: both ends are devices (never remapped to a parent),
// each says its site, the confidence and the source are the collector's, and
// the cable between sw-a1 and sw-a2 is returned next to the uplink between
// them (the page, not the API, draws one line). The UI side of the same
// answers is tested in web/src/topology/uplinks-api.test.ts, from the
// snapshot written here. These are contract tests over an endpoint that needed
// no change: they fail if the API stops answering this way.
func TestUplinksReachTheTopologyAsWritten(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.observeUplinks()

	linksPage := e.page("/api/v1/relations?type="+topologyTypes+"&limit=1000", tokReader)
	devicesPage := e.page("/api/v1/objects?type=device&limit=1000", tokReader)
	sitesPage := e.page("/api/v1/objects?type=site&limit=1000", tokReader)

	all := links(t, linksPage)
	ups := ofType(all, "uplinked_to")
	if len(ups) != 3 {
		t.Fatalf("uplinked_to read back: %d, want 3: %s", len(ups), linksPage.Items)
	}
	type ends struct{ from, to, site string }
	want := map[ends]bool{
		{"ap-01", "sw-a2", e.ids["paris"]}: true,
		{"sw-a2", "sw-a1", e.ids["paris"]}: true,
		{"srv-01", "sw-a1", e.ids["lyon"]}: true,
	}
	for _, l := range ups {
		if l.Confidence != "observed" || l.Source != "unifi:office-lan" {
			t.Errorf("uplink lost its provenance: %+v", l)
		}
		if l.From.Type != "device" || l.To.Type != "device" || l.From.ID != l.FromID || l.To.ID != l.ToID {
			t.Errorf("an uplink's ends are the devices themselves: %+v", l)
		}
		if l.From.ParentID != "" || l.To.ParentID != "" {
			t.Errorf("a device end has no parent: %+v", l)
		}
		k := ends{l.From.Display, l.To.Display, l.From.SiteID}
		if !want[k] || l.To.SiteID == "" {
			t.Errorf("uplink %s -> %s (site %q, to site %q) is not one of those written", l.From.Display, l.To.Display, l.From.SiteID, l.To.SiteID)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("uplinks not read back: %v", want)
	}
	// The API does not fold an uplink into the cable that covers it: both come back.
	cables := ofType(all, "cabled_to")
	if len(cables) != 1 || cables[0].From.ParentID != e.ids["sw"] || cables[0].To.ParentID != e.ids["sw2"] {
		t.Errorf("the cable between sw-a1 and sw-a2 is missing next to its uplink: %+v", cables)
	}

	snap := normalize(t, map[string]any{
		"links":   linksPage.Items,
		"devices": devicesPage.Items,
		"sites":   sitesPage.Items,
	})
	if os.Getenv("TRICA_UPDATE_TOPOLOGY") == "1" {
		if err := os.WriteFile(uplinksSnapshot, snap, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(uplinksSnapshot)
	if err != nil {
		t.Fatalf("%v: write it with TRICA_UPDATE_TOPOLOGY=1", err)
	}
	if !bytes.Equal(got, snap) {
		t.Fatalf("%s is stale: the API changed what the topology reads. Run TRICA_UPDATE_TOPOLOGY=1 go test ./internal/api -run UplinksReachTheTopology, then the web tests", uplinksSnapshot)
	}
}

// An uplink is a relation between two devices: a reader sees it when both
// devices are readable to them, like any link.
func TestUplinksFollowTheReadersRights(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.observeUplinks()
	path := "/api/v1/relations?type=" + topologyTypes + "&limit=1000"

	names := func(tok, typ string) []string {
		var out []string
		for _, l := range ofType(links(t, e.page(path, tok)), typ) {
			out = append(out, l.From.Display+">"+l.To.Display)
		}
		slices.Sort(out)
		return out
	}
	cases := []struct {
		name    string
		token   string
		uplinks []string
		cables  int
	}{
		{"an unrestricted reader sees all three", tokReader, []string{"ap-01>sw-a2", "srv-01>sw-a1", "sw-a2>sw-a1"}, 1},
		{"a Paris reader does not see the one that leaves for Lyon", tokSiteParis, []string{"ap-01>sw-a2", "sw-a2>sw-a1"}, 1},
		{"a devices-only reader sees every uplink but no cable between ports", tokDevices, []string{"ap-01>sw-a2", "srv-01>sw-a1", "sw-a2>sw-a1"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(tc.token, "uplinked_to"); !slices.Equal(got, tc.uplinks) {
				t.Errorf("uplinks = %v, want %v", got, tc.uplinks)
			}
			if got := len(names(tc.token, "cabled_to")); got != tc.cables {
				t.Errorf("cables = %d, want %d", got, tc.cables)
			}
		})
	}
}
