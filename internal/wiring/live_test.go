package wiring_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// liveSwitch is a switch whose mgmt_ip is the one the lab series carry, with
// a LAG of two ports. Everything is invented.
func liveSwitch() string {
	b := sdk.NewBatch("", sdk.ScopePartial)
	b.ObservedAt = time.Now().UTC()
	b.Object("sw", "device").Identify("name", "sw-a1").Set("role", "switch").Set("mgmt_ip", "192.0.2.11")
	b.Object("e1", "interface").Identify("name", "Ethernet1").In("sw").Set("speed_mbps", 10000)
	b.Object("e3", "interface").Identify("name", "Ethernet3").In("sw").Set("speed_mbps", 1000)
	b.Object("e4", "interface").Identify("name", "Ethernet4").In("sw").Set("speed_mbps", 1000)
	b.Object("po", "interface").Identify("name", "Port-Channel10").In("sw").Set("kind", "lag")
	b.Relate("member_of", "e3", "po")
	b.Relate("member_of", "e4", "po")
	raw, _ := json.Marshal(b)
	return string(raw)
}

type liveAnswer struct {
	Enabled        bool                 `json:"enabled"`
	Stale          bool                 `json:"stale"`
	GeneratedAt    *time.Time           `json:"generated_at"`
	RefreshSeconds int                  `json:"refresh_seconds"`
	Links          map[string]live.Link `json:"links"`
	Sources        []live.SourceStatus  `json:"sources"`
}

func TestServerServesTheLiveOverlay(t *testing.T) {
	fake, err := live.NewFakePrometheus("lab")
	if err != nil {
		t.Fatal(err)
	}
	fake.Username, fake.Password = "trica", "not-a-real-password"
	prom := httptest.NewServer(fake)
	defer prom.Close()
	pw := writeFile(t, "live-password", "not-a-real-password\n")
	r := newRig(t, fmt.Sprintf(`
live:
  url: %s
  username: trica
  password: file:%s
  refresh: 5s
  sources:
    - kind: arista
      matchers: {job: arista}
`, prom.URL, pw))

	if code, body := r.do("POST", "/api/v1/batches", r.token, liveSwitch()); code != http.StatusOK {
		t.Fatalf("submit: %d %s", code, body)
	}
	_, body := r.do("GET", "/api/v1/objects?type=interface&limit=100", r.readToken, "")
	var objs struct {
		Items []struct {
			ID         string         `json:"id"`
			Attributes map[string]any `json:"attributes"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &objs); err != nil {
		t.Fatal(err)
	}
	id := map[string]string{}
	for _, o := range objs.Items {
		id[o.Attributes["name"].(string)] = o.ID
	}

	code, body := r.do("GET", "/api/v1/topology/live", r.readToken, "")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var a liveAnswer
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatal(err)
	}
	if !a.Enabled || a.Stale || a.GeneratedAt == nil || a.RefreshSeconds != 5 {
		t.Fatalf("%s", body)
	}
	e1 := a.Links[id["Ethernet1"]]
	if e1.RX == nil || *e1.RX != 1e9 || e1.Oper != "up" || e1.Util == nil || *e1.Util != 0.1 {
		t.Fatalf("Ethernet1: %s", body)
	}
	if po := a.Links[id["Port-Channel10"]]; !po.Aggregated || po.Members != 2 {
		t.Fatalf("the LAG is the sum of its members: %s", body)
	}
	if len(a.Sources) != 1 || a.Sources[0].Name != "arista" {
		t.Fatalf("sources: %s", body)
	}
	// The endpoint is never named to the caller.
	if strings.Contains(body, prom.URL) || strings.Contains(body, "127.0.0.1") {
		t.Fatalf("the answer names the endpoint: %s", body)
	}
	res, err := http.Get(r.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	metrics, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(metrics), `trica_live_upstream_queries_total{outcome="ok",source="arista"}`) {
		t.Fatal("the upstream queries are counted on /metrics")
	}
}

func TestServerWithoutLiveBlockAnswersDisabled(t *testing.T) {
	r := newRig(t, "")
	code, body := r.do("GET", "/api/v1/topology/live", r.readToken, "")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var a liveAnswer
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatal(err)
	}
	if a.Enabled || a.Links == nil || len(a.Links) != 0 || a.GeneratedAt != nil {
		t.Fatalf("%s", body)
	}
	if code, _ := r.do("GET", "/api/v1/topology/live", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
}
