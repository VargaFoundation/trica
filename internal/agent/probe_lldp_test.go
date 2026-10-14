package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProbeLLDPFixture(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "debian12"), newFakeRunner("debian12"), nil, "lldp")
	if err != nil {
		t.Fatal(err)
	}
	want := []Neighbor{
		{Local: "eno1", Protocol: "lldp", ChassisIDType: "mac", ChassisID: "02:00:00:00:aa:01", SysName: "sw-01", PortIDType: "ifname", PortID: "Ethernet12", PortDescr: "uplink host-01 eno1"},
		{Local: "eno2", Protocol: "lldp", ChassisIDType: "mac", ChassisID: "02:00:00:00:aa:02", SysName: "", PortIDType: "local", PortID: "Eth1/13", PortDescr: "Eth1/13"},
	}
	if !reflect.DeepEqual(inv.Neighbors, want) {
		t.Fatalf("neighbours\n got %+v\nwant %+v", inv.Neighbors, want)
	}
}

func TestParseLLDPArrayLayout(t *testing.T) {
	// Older lldpd: arrays of single-key objects, and the chassis as an array-free object too.
	const doc = `{"lldp":{"interface":[
	  {"eth0":{"via":"CDPv2","chassis":{"sw-09":{"id":{"type":"mac","value":"02:00:00:00:AA:09"}}},"port":{"id":{"type":"ifname","value":"Gi1/0/1"},"descr":"GigabitEthernet1/0/1"}}},
	  {"eth1":{"via":"LLDP","chassis":{"sw-10":{"id":{"type":"mac","value":"02:00:00:00:aa:0a"}}},"port":{"id":{"type":"ifalias","value":"Port-7"},"descr":"d"}}}
	]}}`
	ns, notes, err := parseLLDP([]byte(doc))
	if err != nil || len(notes) != 0 || len(ns) != 2 {
		t.Fatalf("%+v %v %v", ns, notes, err)
	}
	if ns[0].Local != "eth0" || ns[0].Protocol != "cdp" || ns[0].ChassisID != "02:00:00:00:aa:09" || ns[0].SysName != "sw-09" || ns[0].peerPort() != "Gi1/0/1" {
		t.Errorf("%+v", ns[0])
	}
	if ns[1].Protocol != "lldp" || ns[1].peerPort() != "Port-7" {
		t.Errorf("%+v", ns[1])
	}
}

func TestParseLLDPNoNeighbours(t *testing.T) {
	for _, doc := range []string{`{"lldp":{}}`, `{"lldp":{"interface":null}}`, `{"lldp":{"interface":[]}}`, `{"lldp":{"interface":{}}}`, `{}`} {
		ns, notes, err := parseLLDP([]byte(doc))
		if err != nil || len(ns) != 0 || len(notes) != 0 {
			t.Errorf("%s: %+v %v %v", doc, ns, notes, err)
		}
	}
}

func TestParseLLDPUnusableNeighboursAreNoted(t *testing.T) {
	const doc = `{"lldp":{"interface":{
	  "a":{"via":"LLDP","chassis":{"id":{"type":"local","value":"x"}},"port":{"id":{"type":"ifname","value":"p"},"descr":""}},
	  "b":{"via":"LLDP","chassis":{"id":{"type":"mac","value":"02:00:00:00:aa:0b"}},"port":{"id":{"type":"mac","value":"02:00:00:00:aa:0b"},"descr":""}},
	  "c":{"via":"LLDP","chassis":{"id":{"type":"mac","value":"02:00:00:00:aa:0c"}},"port":{"id":{"type":"mac","value":"02:00:00:00:aa:0c"},"descr":"Ethernet3"}}
	}}}`
	ns, notes, err := parseLLDP([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	// a: no MAC chassis id and no system name; b: only a MAC port id and no description; c: MAC port id, the description names the port.
	if len(ns) != 1 || ns[0].Local != "c" || ns[0].peerPort() != "Ethernet3" {
		t.Fatalf("neighbours %+v", ns)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "neighbour on a") || !strings.Contains(notes[1], "neighbour on b") {
		t.Fatalf("notes %v", notes)
	}
}

func TestParseLLDPErrors(t *testing.T) {
	for _, doc := range []string{`not json`, `{"lldp":{"interface":"x"}}`, `{"lldp":{"interface":{"eth0":{"via":"LLDP"}}}}`} {
		if _, _, err := parseLLDP([]byte(doc)); err == nil {
			t.Errorf("%s was accepted", doc)
		}
	}
}

func TestProbeLLDPAbsentIsQuiet(t *testing.T) {
	inv := observe(t.Context(), openRoot(t, "rhel9"), newFakeRunner("rhel9"), fsAddrs{path: "/addrs.txt"})
	if len(inv.Neighbors) != 0 || containsSub(inv.Skipped, "lldp") || containsPrefix(inv.Failed, "lldp") {
		t.Fatalf("a host without lldpd is normal: %+v %v %v", inv.Neighbors, inv.Skipped, inv.Failed)
	}
}

func TestProbeLLDPFailures(t *testing.T) {
	boom := runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("lldpctl: unable to connect to the lldpd socket")
	})
	if _, err := probeAfter(t, openRoot(t, "debian12"), boom, nil, "lldp"); err == nil {
		t.Fatal("a failing lldpctl was ignored")
	}
	junk := runnerFunc(func(context.Context, string, ...string) ([]byte, error) { return []byte("<xml/>"), nil })
	if _, err := probeAfter(t, openRoot(t, "debian12"), junk, nil, "lldp"); err == nil {
		t.Fatal("garbage output was accepted")
	}
}
