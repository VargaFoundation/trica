package pfsense

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseUptime(t *testing.T) {
	for in, want := range map[string]int{
		`"3 Days 04 Hours 12 Minutes 40 Seconds"`: 3*86400 + 4*3600 + 12*60 + 40,
		`"1 Day 00 Hours 00 Minutes 01 Second"`:   86401,
		`"45 Minutes 10 Seconds"`:                 2710,
		`12345`:                                   12345,
		`"12345"`:                                 12345,
		`12345.7`:                                 12345,
	} {
		if got, ok := parseUptime([]byte(in)); !ok || got != want {
			t.Errorf("%s: got %d, %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{``, `null`, `""`, `"unknown"`, `-5`} {
		if got, ok := parseUptime([]byte(in)); ok {
			t.Errorf("%q: got %d, want no uptime", in, got)
		}
	}
}

func TestParseMedia(t *testing.T) {
	for in, want := range map[string]struct {
		speed  int
		duplex string
	}{
		"1000baseT <full-duplex>":                      {1000, "full"},
		"autoselect (100baseTX <half-duplex>)":         {100, "half"},
		"10Gbase-Twinax <full-duplex,rxpause,txpause>": {10000, "full"},
		"2500Base-T":          {2500, ""},
		"":                    {0, ""},
		"Ethernet autoselect": {0, ""},
	} {
		if s, d := parseMedia(in); s != want.speed || d != want.duplex {
			t.Errorf("%q: got %d %q, want %d %q", in, s, d, want.speed, want.duplex)
		}
	}
}

func TestStatusMappings(t *testing.T) {
	for in, want := range map[string]string{
		"none": "online", "online": "online", "UP": "online", "down": "offline", "force_down": "offline",
		"delay": "degraded", "loss": "degraded", "delay+loss": "degraded", "pending": "unknown", "": "",
	} {
		if got := gatewayStatusName(in); got != want {
			t.Errorf("gateway %q: got %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"up": "up", "server_user_listening": "up", "server_tls_listening": "up", "connected": "up",
		"down": "down", "reconnecting": "down", "failed": "down", "waiting": "unknown", "": "unknown",
	} {
		if got := openvpnState(in); got != want {
			t.Errorf("openvpn %q: got %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"up": "up", "no carrier": "down", "down": "down", "": "", "weird": "unknown"} {
		if got := operStatus(in); got != want {
			t.Errorf("link %q: got %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"igb0": "physical", "igb0.20": "physical", "lo0": "loopback", "lagg0": "lag", "ovpns1": "tunnel", "tun_wg0": "tunnel",
		"bridge0": "virtual", "enc0": "virtual", "vtnet0": "physical",
	} {
		if got := ifaceKind(in, false); got != want {
			t.Errorf("kind %q: got %q, want %q", in, got, want)
		}
	}
	if ifaceKind("igb0.20", true) != "vlan" {
		t.Error("vlan interface not recognised")
	}
}

func TestTunnelStatus(t *testing.T) {
	for _, tc := range []struct {
		disabled, read bool
		state          string
		found          bool
		want           string
	}{
		{true, true, "up", true, "down"},
		{false, false, "", false, ""},
		{false, true, "", false, "down"},
		{false, true, "up", true, "up"},
	} {
		if got := tunnelStatus(tc.disabled, tc.read, tc.state, tc.found); got != tc.want {
			t.Errorf("%+v: got %q", tc, got)
		}
	}
}

func TestVersionAndSerial(t *testing.T) {
	for _, tc := range []struct {
		v    versionInfo
		want string
	}{
		{versionInfo{"2.7.2-RELEASE", "0"}, "2.7.2"},
		{versionInfo{"2.8.1-RELEASE", "1"}, "2.8.1-p1"},
		{versionInfo{"24.11-RELEASE", ""}, "24.11"},
		{versionInfo{"", "1"}, ""},
	} {
		if got := versionString(tc.v); got != tc.want {
			t.Errorf("%+v: got %q", tc.v, got)
		}
	}
	for in, want := range map[string]string{"": "", "none": "", "None": "", "To Be Filled By O.E.M.": "", " SN123 ": "SN123"} {
		if got := usableSerial(in); got != want {
			t.Errorf("serial %q: got %q", in, got)
		}
	}
}

func TestParseMeasure(t *testing.T) {
	if v, ok := parseMeasure("12.5ms", "ms"); !ok || v != 12.5 {
		t.Errorf("got %v %v", v, ok)
	}
	if v, ok := parseMeasure(" 0.0% ", "%"); !ok || v != 0 {
		t.Errorf("got %v %v", v, ok)
	}
	for _, in := range []string{"", "~", "ms"} {
		if _, ok := parseMeasure(in, "ms"); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestFlexibleTypes(t *testing.T) {
	var v struct {
		S  str   `json:"s"`
		N  str   `json:"n"`
		B  str   `json:"b"`
		Z  str   `json:"z"`
		O  str   `json:"o"`
		L  strs  `json:"l"`
		L2 strs  `json:"l2"`
		L3 strs  `json:"l3"`
		F1 flag  `json:"f1"`
		F2 flag  `json:"f2"`
		F3 flag  `json:"f3"`
		F4 flag  `json:"f4"`
		P  *flag `json:"p"`
	}
	doc := `{"s":" x ","n":24,"b":true,"z":null,"o":{"a":1},"l":["a","","b"],"l2":"solo","l3":null,
	         "f1":true,"f2":"yes","f3":1,"f4":"","p":false}`
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	if v.S != "x" || v.N != "24" || v.B != "true" || v.Z != "" || v.O != "" {
		t.Errorf("str: %+v", v)
	}
	if !slices.Equal(v.L, strs{"a", "b"}) || !slices.Equal(v.L2, strs{"solo"}) || v.L3 != nil {
		t.Errorf("strs: %+v %+v %+v", v.L, v.L2, v.L3)
	}
	if !v.F1 || !v.F2 || !v.F3 || v.F4 || v.P == nil || *v.P {
		t.Errorf("flag: %+v", v)
	}
	if v.S.String() != "x" {
		t.Error("String")
	}
	// A list of objects is not a list of texts only when it is not a list.
	if err := json.Unmarshal([]byte(`{"l":[1,2]}`), &v); err != nil || !slices.Equal(v.L, strs{"a", "b", "1", "2"}) {
		t.Errorf("numbers in a list: %v %v", v.L, err)
	}
	if err := json.Unmarshal([]byte(`{"s":"unterminated`), &v); err == nil {
		t.Error("invalid JSON accepted")
	}
	var s str
	if err := s.UnmarshalJSON([]byte(`"\x"`)); err == nil {
		t.Error("invalid string accepted")
	}
	if err := s.UnmarshalJSON(nil); err != nil {
		t.Error(err)
	}
	var l strs
	if err := l.UnmarshalJSON([]byte(`["a",`)); err == nil {
		t.Error("truncated list accepted")
	}
}

func TestRequestErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		err  *requestError
		want string
	}{
		{&requestError{Kind: errAuth, Path: "p", Msg: "Authentication failed"}, "authentication refused"},
		{&requestError{Kind: errForbidden, Path: "p"}, "read privilege"},
		{&requestError{Kind: errNotFound, Path: "p"}, "404"},
		{&requestError{Kind: errServer, Path: "p", Status: 502}, "HTTP 502"},
		{&requestError{Kind: errMalformed, Path: "p", Err: errors.New("bad json")}, "REST API package"},
		{&requestError{Kind: errTransport, Path: "p", Err: errors.New("refused")}, "refused"},
	} {
		if got := tc.err.Error(); !strings.Contains(got, tc.want) || !strings.HasPrefix(got, "GET p: ") {
			t.Errorf("%q lacks %q", got, tc.want)
		}
	}
	if kindOf(errors.New("plain")) != errTransport {
		t.Error("foreign error not treated as transport")
	}
	if got := clip(strings.Repeat("x", 300)); len(got) != 203 {
		t.Errorf("clip: %d", len(got))
	}
	if !errors.Is(&requestError{Err: errors.ErrUnsupported}, errors.ErrUnsupported) {
		t.Error("Unwrap")
	}
}
