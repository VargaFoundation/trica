package arista

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNum(t *testing.T) {
	tests := []struct {
		in   string
		want num
	}{
		{`12`, num{12, true}},
		{`-2.5`, num{-2.5, true}},
		{`"12.5"`, num{12.5, true}},
		{`" 7 "`, num{7, true}},
		{`"N/A"`, num{}},
		{`"-inf"`, num{}},
		{`"nan"`, num{}},
		{`null`, num{}},
		{`""`, num{}},
		{`true`, num{}},
		{`{}`, num{}},
	}
	for _, tc := range tests {
		var got num
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Errorf("%s: %v (a number that cannot be read is absent, not an error)", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestSlots(t *testing.T) {
	var asObject, asArray slots[invItem]
	if err := json.Unmarshal([]byte(`{"1":{"serialNum":"A"},"3":{"serialNum":"C"}}`), &asObject); err != nil {
		t.Fatal(err)
	}
	if len(asObject) != 2 || asObject["3"].SerialNum != "C" {
		t.Errorf("object form: %+v", asObject)
	}
	if err := json.Unmarshal([]byte(`[{"serialNum":"A"},{"serialNum":"B"}]`), &asArray); err != nil {
		t.Fatal(err)
	}
	if len(asArray) != 2 || asArray["1"].SerialNum != "A" || asArray["2"].SerialNum != "B" {
		t.Errorf("array form (numbered from 1): %+v", asArray)
	}
	for _, bad := range []string{`"x"`, `[1]`, `{"1":3}`} {
		var s slots[invItem]
		if err := json.Unmarshal([]byte(bad), &s); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

func TestCanonicalInterface(t *testing.T) {
	tests := map[string]string{
		"Et1": "Ethernet1", "Et49/1": "Ethernet49/1", "Ma1": "Management1", "Po10": "Port-Channel10",
		"Vl100": "Vlan100", "Lo0": "Loopback0", "Ethernet1": "Ethernet1",
		"Et": "Et", "Etx": "Etx", "Cpu": "Cpu", "Vxlan1": "Vxlan1",
	}
	for in, want := range tests {
		if got := canonicalInterface(in); got != want {
			t.Errorf("canonicalInterface(%q) = %q, want %q", in, got, want)
		}
	}
	if got := canonicalInterface("\tEthernet2 "); got != "Ethernet2" {
		t.Errorf("surrounding whitespace is kept: %q", got)
	}
}

func TestClassifiers(t *testing.T) {
	kinds := map[string]string{
		"Ethernet1": "physical", "Management1": "physical", "Port-Channel2": "lag", "Vlan5": "vlan",
		"Loopback1": "loopback", "Vxlan1": "virtual", "Tunnel0": "virtual", "Cpu": "other",
	}
	for in, want := range kinds {
		if got := interfaceKind(in); got != want {
			t.Errorf("interfaceKind(%q) = %q, want %q", in, got, want)
		}
	}
	hw := map[string]string{"ethernet": "physical", "Ethernet": "physical", "portChannel": "lag", "vlan": "vlan", "loopback": "loopback", "vxlan": "other", "": "other"}
	for in, want := range hw {
		if got := kindFromHardware(in); got != want {
			t.Errorf("kindFromHardware(%q) = %q, want %q", in, got, want)
		}
	}
	admin := map[string]string{"": "", "disabled": "down", "adminDown": "down", "connected": "up", "errdisabled": "up", "notconnect": "up"}
	for in, want := range admin {
		if got := adminStatus(in); got != want {
			t.Errorf("adminStatus(%q) = %q, want %q", in, got, want)
		}
	}
	oper := map[string]string{"": "", "up": "up", "notPresent": "not_present", "down": "down", "lowerLayerDown": "down"}
	for in, want := range oper {
		if got := operStatus(in); got != want {
			t.Errorf("operStatus(%q) = %q, want %q", in, got, want)
		}
	}
	link := map[string]string{"": "", "connected": "up", "up": "up", "notconnect": "down", "errdisabled": "down"}
	for in, want := range link {
		if got := operFromLink(in); got != want {
			t.Errorf("operFromLink(%q) = %q, want %q", in, got, want)
		}
	}
	duplex := map[string]string{"": "", "duplexFull": "full", "full": "full", "duplexHalf": "half", "half": "half", "duplexUnknown": "unknown"}
	for in, want := range duplex {
		if got := duplexOf(in); got != want {
			t.Errorf("duplexOf(%q) = %q, want %q", in, got, want)
		}
	}
	media := map[string]string{"10GBASE-SR": "10GBASE-SR", " 1000BASE-T ": "1000BASE-T", "Not Present": "", "N/A": "", "": ""}
	for in, want := range media {
		if got := mediaOf(in); got != want {
			t.Errorf("mediaOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddressesOf(t *testing.T) {
	object := `{"primaryIp":{"address":"192.0.2.1","maskLen":24},"secondaryIps":{"b":{"address":"192.0.2.3","maskLen":24},"a":{"address":"192.0.2.2","maskLen":24}},"virtualIp":{"address":"0.0.0.0","maskLen":0}}`
	want := []ipInfo{
		{addr: "192.0.2.1", role: "primary", maskLen: 24},
		{addr: "192.0.2.2", role: "secondary", maskLen: 24},
		{addr: "192.0.2.3", role: "secondary", maskLen: 24},
	}
	tests := []struct {
		name string
		in   string
		want []ipInfo
	}{
		{"object (show ip interface)", object, want},
		{"array (show interfaces)", "[" + object + "]", want},
		{"empty array", `[]`, nil},
		{"no address", `{"primaryIp":{"address":"0.0.0.0","maskLen":0}}`, nil},
		{"CIDR in address", `{"primaryIp":{"address":"198.51.100.9/24","maskLen":24}}`, []ipInfo{{addr: "198.51.100.9", role: "primary", maskLen: 24}}},
		{"junk", `"x"`, nil},
		{"junk address", `{"primaryIp":{"address":"not-an-ip"}}`, nil},
		{"null", `null`, nil},
	}
	for _, tc := range tests {
		if got := addressesOf(json.RawMessage(tc.in)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestIdentifiers(t *testing.T) {
	macs := map[string]string{
		"02aa.0000.0101": "02:aa:00:00:01:01", "02:AA:00:00:01:01": "02:aa:00:00:01:01", "02-aa-00-00-01-01": "02:aa:00:00:01:01",
		"": "", "00:00:00:00:00:00": "", "0000.0000.0000": "", "zz": "",
	}
	for in, want := range macs {
		got, ok := validMAC(in)
		if got != want || ok != (want != "") {
			t.Errorf("validMAC(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	ips := map[string]string{"192.0.2.1": "192.0.2.1", "2001:db8::1": "2001:db8::1", "": "", "host": ""}
	for in, want := range ips {
		got, ok := validIP(in)
		if got != want || ok != (want != "") {
			t.Errorf("validIP(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if got, ok := validIP(" 192.0.2.1/24\t"); !ok || got != "192.0.2.1" {
		t.Errorf("validIP with a prefix and whitespace = %q, %v", got, ok)
	}
	names := map[string]string{"sw-a1.example.net": "sw-a1", "sw-a1.": "sw-a1", "sw-a1": "sw-a1", "": "", "192.0.2.5": "192.0.2.5", ".hidden": ".hidden"}
	for in, want := range names {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
	hosts := map[string]string{
		"192.0.2.11": "192.0.2.11", "http://192.0.2.11:80": "192.0.2.11", "https://sw-a1": "", "[2001:db8::1]:443": "2001:db8::1", "http://[::1": "",
	}
	for in, want := range hosts {
		if got := hostIP(in); got != want {
			t.Errorf("hostIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlacement(t *testing.T) {
	tests := map[string]string{
		"Switch chip inlet sensor": "inlet", "Air intake": "inlet", "Switch chip exhaust sensor": "outlet", "Outlet": "outlet",
		"Cpu temp sensor": "cpu", "Power supply 1 hotspot sensor": "psu", "Board sensor": "system", "System": "system",
		"Front-panel temp sensor": "other", "": "other",
	}
	for in, want := range tests {
		if got := placement(in); got != want {
			t.Errorf("placement(%q) = %q, want %q", in, got, want)
		}
	}
}
