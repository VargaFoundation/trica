package unifi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRoleOf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		d     device
		gw    bool
		over  map[string]string
		role  string
		known bool
	}{
		{"console with a gateway", device{Model: "UDM Pro Max", Shortname: "UDMPROMAX", IsConsole: true}, true, nil, "firewall", true},
		{"gateway by model", device{Model: "UXG-Lite", Shortname: "UXGLITE"}, false, nil, "firewall", true},
		{"cloud gateway", device{Model: "UCG Max", Shortname: "UCGMAX"}, false, nil, "firewall", true},
		{"console without a gateway", device{Model: "UniFi OS Server", Shortname: "UOSSERVER", IsConsole: true}, false, nil, "other", true},
		{"U7", device{Model: "U7 Pro", Shortname: "U7PRO", ProductLine: "network"}, false, nil, "access_point", true},
		{"U6", device{Model: "U6 Lite", Shortname: "U6LITE", ProductLine: "network"}, false, nil, "access_point", true},
		{"AC", device{Model: "UAP-AC-Pro", Shortname: "U7PG2", ProductLine: "network"}, false, nil, "access_point", true},
		{"nanoHD", device{Model: "nanoHD", Shortname: "U7NHD", ProductLine: "network"}, false, nil, "access_point", true},
		{"switch", device{Model: "USW Pro Max 24 PoE", Shortname: "USPM24P", ProductLine: "network"}, false, nil, "switch", true},
		{"old switch", device{Model: "US 8 60W", Shortname: "US8P60", ProductLine: "network"}, false, nil, "switch", true},
		{"lite switch by shortname", device{Model: "Lite 8 PoE", Shortname: "USL8LP", ProductLine: "network"}, false, nil, "switch", true},
		{"PDU, whose shortname looks like a switch's", device{Model: "USP PDU Pro", Shortname: "USPPDUP", ProductLine: "network"}, false, nil, "pdu", true},
		{"NVR", device{Model: "UNVR", Shortname: "UNVR", ProductLine: "protect"}, false, nil, "nas", true},
		{"door hub", device{Model: "UA Hub Door", Shortname: "UAH-DOOR", ProductLine: "access"}, false, nil, "access_control", true},
		{"reader", device{Model: "UA G2 Pro", Shortname: "UA-G2-PRO", ProductLine: "access"}, false, nil, "access_control", true},
		{"camera", device{Model: "UVC G5 Bullet", Shortname: "UVCG5BULLET", ProductLine: "protect"}, false, nil, "camera", true},
		{"unknown", device{Model: "XYZ-9000", Shortname: "XYZ9000", ProductLine: "network"}, false, nil, "other", false},
		{"override by shortname", device{Model: "UA Hub Door", Shortname: "UAH-DOOR", ProductLine: "access"}, false, map[string]string{"UAHDOOR": "sensor"}, "sensor", true},
		{"override by model", device{Model: "XYZ-9000", Shortname: "XYZ9000"}, false, map[string]string{"XYZ9000": "router"}, "router", true},
	} {
		if role, known := roleOf(tc.d, tc.gw, tc.over); role != tc.role || known != tc.known {
			t.Errorf("%s: got %s, %v; want %s, %v", tc.name, role, known, tc.role, tc.known)
		}
	}
}

func TestHealthOf(t *testing.T) {
	for in, want := range map[string]string{
		"online": "ok", "ONLINE": "ok", "offline": "critical", "disconnected": "critical",
		"updating": "warning", "adopting": "warning", "pending": "warning", "pendingAdoption": "warning", "PENDING_ADOPTION": "warning",
		"provisioning": "warning", "gettingReady": "warning", "restarting": "warning", "connectionInterrupted": "warning", "isolated": "warning", "DELETING": "warning", "U5G_INCORRECT_TOPOLOGY": "warning",
		"": "unknown", "weird": "unknown",
	} {
		if got := healthOf(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestSpeedMbps(t *testing.T) {
	for in, want := range map[string]int{
		"10G SFP+": 10000, "2.5GbE": 2500, "1GbE": 1000, "GbE": 1000, "gbe": 1000, "100MbE": 100, "100M": 100, "10GbE": 10000, "": 0, "SFP": 0, "auto": 0,
	} {
		if got := speedMbps(in); got != want {
			t.Errorf("%q: got %d, want %d", in, got, want)
		}
	}
}

func TestSoftwareName(t *testing.T) {
	for in, want := range map[string]string{"network": "UniFi Network", "PROTECT": "UniFi Protect", "access": "UniFi Access", "innerspace": "UniFi Innerspace", "": "UniFi"} {
		if got := softwareName(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func TestNormMAC(t *testing.T) {
	for in, want := range map[string]string{
		"020000000001": "02:00:00:00:00:01", "02:00:00:00:00:0A": "02:00:00:00:00:0a", "02-00-00-00-00-0b": "02:00:00:00:00:0b",
		"": "", "0200": "", "02:00:00:00:00:0g": "", "not a mac": "", "0200000000010": "",
	} {
		if got := normMAC(in); got != strings.ToLower(want) {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestUsableIP(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1": "192.0.2.1", "198.51.100.2": "198.51.100.2", "2001:db8::1": "2001:db8::1", "::ffff:192.0.2.5": "192.0.2.5",
		"fe80::1": "", "169.254.1.1": "", "127.0.0.1": "", "0.0.0.0": "", "": "", "dhcp": "", "224.0.0.1": "",
	} {
		if got := usableIP(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestTolerantTypes(t *testing.T) {
	var v struct {
		S str   `json:"s"`
		N str   `json:"n"`
		B str   `json:"b"`
		Z str   `json:"z"`
		O str   `json:"o"`
		L str   `json:"l"`
		F flag  `json:"f"`
		G flag  `json:"g"`
		H flag  `json:"h"`
		I flag  `json:"i"`
		P *flag `json:"p"`
	}
	doc := `{"s":" x ","n":24,"b":true,"z":null,"o":{"a":1},"l":[1],"f":true,"g":"yes","h":1,"i":"","p":false}`
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	if v.S != "x" || v.N != "24" || v.B != "true" || v.Z != "" || v.O != "" || v.L != "" {
		t.Errorf("str: %+v", v)
	}
	if !v.F || !v.G || !v.H || v.I || v.P == nil || *v.P {
		t.Errorf("flag: %+v", v)
	}
	if v.S.String() != "x" {
		t.Error("String")
	}
	var s str
	if err := s.UnmarshalJSON([]byte(`"\x"`)); err == nil {
		t.Error("invalid string accepted")
	}
	if err := s.UnmarshalJSON(nil); err != nil {
		t.Error(err)
	}
	var f flag
	if err := f.UnmarshalJSON([]byte(`"\x"`)); err == nil {
		t.Error("invalid flag accepted")
	}
	// A device with numbers where strings are expected still decodes.
	var d device
	if err := json.Unmarshal([]byte(`{"name":12,"version":7,"note":null,"isManaged":"true","ip":["a"]}`), &d); err != nil || d.Name != "12" || d.Version != "7" || !bool(d.IsManaged) || d.IP != "" {
		t.Errorf("device %+v, %v", d, err)
	}
}

func TestRequestErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		err  *requestError
		want string
	}{
		{&requestError{Kind: errAuth, Path: "/p", Msg: "unauthorized"}, "authentication refused"},
		{&requestError{Kind: errForbidden, Path: "/p"}, "may not read"},
		{&requestError{Kind: errNotFound, Path: "/p"}, "404"},
		{&requestError{Kind: errRateLimited, Path: "/p", Retry: "30"}, "retry after 30"},
		{&requestError{Kind: errRateLimited, Path: "/p"}, "429"},
		{&requestError{Kind: errServer, Path: "/p", Status: 502}, "HTTP 502"},
		{&requestError{Kind: errMalformed, Path: "/p", Err: errors.New("bad json")}, "Site Manager"},
		{&requestError{Kind: errTransport, Path: "/p", Err: errors.New("refused")}, "refused"},
	} {
		if got := tc.err.Error(); !strings.Contains(got, tc.want) || !strings.HasPrefix(got, "GET /p: ") {
			t.Errorf("%q lacks %q", got, tc.want)
		}
	}
	if kindOf(errors.New("plain")) != errTransport {
		t.Error("foreign error not treated as transport")
	}
	if !errors.Is(&requestError{Err: errors.ErrUnsupported}, errors.ErrUnsupported) {
		t.Error("Unwrap")
	}
}

func TestUpdateOf(t *testing.T) {
	for in, want := range map[string]string{"": "", "false": "", "FALSE": "", "0": "", "null": "", "7.5.20": "7.5.20", "true": "true"} {
		if got := updateOf(device{UpdateAvailable: str(in)}); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
