package redfish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestUniqueNames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
		ids   []string
		want  []string
	}{
		{"distinct names are kept", []string{"a", "b"}, []string{"0", "1"}, []string{"a", "b"}},
		{"duplicates take their member id", []string{"PSU", "PSU"}, []string{"0", "1"}, []string{"PSU 0", "PSU 1"}},
		{"duplicates without an id take their position", []string{"PSU", "PSU"}, []string{"", ""}, []string{"PSU 1", "PSU 2"}},
		{"empty names take their position", []string{"", "x"}, []string{"", ""}, []string{"item-1", "x"}},
		{"same id twice still ends distinct", []string{"PSU", "PSU"}, []string{"0", "0"}, []string{"PSU 0", "PSU 0 #2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := uniqueNames(tc.names, tc.ids); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMapping(t *testing.T) {
	for _, tc := range [][2]string{{"", ""}, {"OK", "ok"}, {" Warning ", "warning"}, {"Critical", "critical"}, {"Weird", "unknown"}} {
		if got := healthOf(tc[0]); got != tc[1] {
			t.Errorf("healthOf(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
	for in, want := range map[string]string{"": "", "Up": "up", "LinkUp": "up", "LinkDown": "down", "Down": "down", "NoLink": "down", "Odd": "unknown"} {
		if got := linkState(in); got != want {
			t.Errorf("linkState(%q) = %q, want %q", in, got, want)
		}
	}
	for _, tc := range []struct{ media, proto, want string }{
		{"SSD", "SATA", "ssd"}, {"HDD", "SAS", "hdd"}, {"SSD", "NVMe", "nvme"}, {"", "nvme", "nvme"}, {"", "", "unknown"}, {"Tape", "", "unknown"},
	} {
		if got := mediaOf(tc.media, tc.proto); got != tc.want {
			t.Errorf("mediaOf(%q, %q) = %q, want %q", tc.media, tc.proto, got, tc.want)
		}
	}
	if got := partHealth(status{State: "Absent", Health: "OK"}); got != "absent" {
		t.Errorf("absent slot: %q", got)
	}
	if got := partHealth(status{Health: "Warning"}); got != "warning" {
		t.Errorf("warning: %q", got)
	}
	if got := (status{Health: "OK", HealthRollup: "Critical"}).rollup(); got != "Critical" {
		t.Errorf("rollup prefers the rollup: %q", got)
	}
	if got := (status{Health: "OK"}).rollup(); got != "OK" {
		t.Errorf("rollup falls back to the health: %q", got)
	}
}

func TestAddresses(t *testing.T) {
	for _, tc := range [][2]string{{"192.0.2.1", "192.0.2.1"}, {" 198.51.100.7 ", "198.51.100.7"}} {
		if got, ok := usableIPv4(tc[0]); !ok || got != tc[1] {
			t.Errorf("usableIPv4(%q) = %q, %v", tc[0], got, ok)
		}
	}
	for _, in := range []string{"", "0.0.0.0", "not-an-ip", "2001:db8::1"} {
		if got, ok := usableIPv4(in); ok {
			t.Errorf("usableIPv4(%q) = %q, want none", in, got)
		}
	}
	for in, want := range map[string]int{"255.255.255.0": 24, "255.255.255.128": 25, "255.255.0.0": 16, "0.0.0.0": 0} {
		if got, ok := prefixLength(in); !ok || got != want {
			t.Errorf("prefixLength(%q) = %d, %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "junk", "255.0.255.0"} {
		if got, ok := prefixLength(in); ok {
			t.Errorf("prefixLength(%q) = %d, want none", in, got)
		}
	}
}

func TestSensorPlacement(t *testing.T) {
	for _, tc := range []struct {
		context, name, want string
	}{
		{"Intake", "x", "inlet"},
		{"Exhaust", "x", "outlet"},
		{"CPU", "x", "cpu"},
		{"PowerSupply", "x", "psu"},
		{"Memory", "x", "memory"},
		{"MemoryDevice", "x", "memory"},
		{"Room", "x", "ambient"},
		{"SystemBoard", "x", "system"},
		{"", "System Board Inlet Temp", "inlet"},
		{"", "System Board Exhaust Temp", "outlet"},
		{"", "CPU1 Temp", "cpu"},
		{"", "04-P1 DIMM 1-6", "memory"},
		{"", "Memory Zone", "memory"},
		{"", "09-P/S 2", "psu"},
		{"", "Power Supply 1", "psu"},
		{"", "PSU1", "psu"},
		{"SystemBoard", "Chipset", "system"},
		{"", "Mystery", "other"},
		{"Chassis", "Mystery", "other"},
	} {
		if got := sensorPlacement(temperature{PhysicalContext: tc.context, Name: tc.name}); got != tc.want {
			t.Errorf("sensorPlacement(%q, %q) = %q, want %q", tc.context, tc.name, got, tc.want)
		}
	}
}

func TestDecoding(t *testing.T) {
	t.Run("firmware version, string or object", func(t *testing.T) {
		var v struct{ A, B, C, D, E fwVersion }
		in := `{"A": " 6.06 ", "B": {"Current": {"VersionString": " 2.10 "}}, "C": null, "D": {}, "E": 5}`
		err := json.Unmarshal([]byte(in), &v)
		if err == nil {
			t.Fatal("a number is neither a string nor an object, it must be reported")
		}
		in = `{"A": " 6.06 ", "B": {"Current": {"VersionString": " 2.10 "}}, "C": null, "D": {}}`
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatal(err)
		}
		if v.A != "6.06" || v.B != "2.10" || v.C != "" || v.D != "" {
			t.Errorf("%+v", v)
		}
	})
	t.Run("loose string ignores objects", func(t *testing.T) {
		var v struct{ A, B, C looseString }
		if err := json.Unmarshal([]byte(`{"A": " Slot 0 ", "B": {"Info": "x"}, "C": null}`), &v); err != nil {
			t.Fatal(err)
		}
		if v.A != "Slot 0" || v.B != "" || v.C != "" {
			t.Errorf("%+v", v)
		}
	})
	t.Run("drive location", func(t *testing.T) {
		for in, want := range map[string]string{
			`{"Location": " 1I:1:1 "}`:                                           "1I:1:1",
			`{"Location": [{"Info": "Bay 3", "InfoFormat": "Bay"}]}`:             "Bay 3",
			`{"Location": []}`:                                                   "",
			`{"Location": []}, "x"`:                                              "",
			`{"PhysicalLocation": {"PartLocation": {"ServiceLabel": "Slot 7"}}}`: "Slot 7",
			`{"Location": 12, "PhysicalLocation": {"PartLocation": {"ServiceLabel": "Slot 8"}}}`: "Slot 8",
			`{}`: "",
		} {
			var d drive
			if err := json.Unmarshal([]byte(in), &d); err != nil {
				if strings.HasSuffix(in, `"x"`) { // not valid JSON on purpose, skipped
					continue
				}
				t.Fatalf("%s: %v", in, err)
			}
			if got := d.location(); got != want {
				t.Errorf("location of %s = %q, want %q", in, got, want)
			}
		}
	})
	t.Run("wear", func(t *testing.T) {
		for in, want := range map[string]string{
			`{"PredictedMediaLifeLeftPercent": 80}`:                                         "20",
			`{"SSDEnduranceUtilizationPercentage": 3}`:                                      "3",
			`{"Oem": {"Hpe": {"SSDEnduranceUtilizationPercentage": 4}}}`:                    "4",
			`{"Oem": {"Hp": {"SSDEnduranceUtilizationPercentage": 5}}}`:                     "5",
			`{"PredictedMediaLifeLeftPercent": 90, "SSDEnduranceUtilizationPercentage": 3}`: "10",
			`{"Oem": {}}`: "",
		} {
			var d drive
			if err := json.Unmarshal([]byte(in), &d); err != nil {
				t.Fatal(err)
			}
			got := ""
			if w := d.wearPct(); w != nil {
				got = strings.TrimSuffix(strings.TrimSuffix(jsonNumber(*w), ".0"), ".00")
			}
			if got != want {
				t.Errorf("wear of %s = %q, want %q", in, got, want)
			}
		}
	})
	t.Run("vendor extension, either spelling", func(t *testing.T) {
		var hp, hpe, none system
		_ = json.Unmarshal([]byte(`{"Oem": {"Hp": {"Bios": {"Current": {"VersionString": "old"}}}}}`), &hp)
		_ = json.Unmarshal([]byte(`{"Oem": {"Hpe": {"Bios": {"Current": {"VersionString": "new"}}}}}`), &hpe)
		if hp.hpe().Bios.Current.VersionString != "old" || hpe.hpe().Bios.Current.VersionString != "new" || none.hpe().Bios.Current.VersionString != "" {
			t.Error("the OEM extension is read under Hp or Hpe")
		}
	})
	t.Run("collection paths skip empty links", func(t *testing.T) {
		var c collection
		_ = json.Unmarshal([]byte(`{"Members": [{"@odata.id": " /a "}, {}, {"@odata.id": "/b"}]}`), &c)
		if got := c.paths(); !reflect.DeepEqual(got, []string{"/a", "/b"}) {
			t.Errorf("%q", got)
		}
		var l *link
		if l.path() != "" {
			t.Error("a nil link has no path")
		}
	})
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func TestHelpers(t *testing.T) {
	if got := first(" ", "", " x ", "y"); got != "x" {
		t.Errorf("first: %q", got)
	}
	if first() != "" {
		t.Error("first of nothing")
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Errorf("truncate: %q", got)
	}
	if got := truncate("abc", 4); got != "abc" {
		t.Errorf("truncate short: %q", got)
	}
	if r := round(nil); r != nil {
		t.Error("round of nil")
	}
	f := 799.6
	if r := round(&f); r == nil || *r != 800 {
		t.Errorf("round: %v", r)
	}
	n, z := int64(2), int64(0)
	if v := mib(&n); v == nil || *v != 2<<20 {
		t.Errorf("mib: %v", v)
	}
	if mib(&z) != nil || mib(nil) != nil {
		t.Error("mib of zero or nothing")
	}
	if first64(nil, &n) != &n || first64(nil) != nil {
		t.Error("first64")
	}
	if !zero(&z) || zero(&n) || zero(nil) {
		t.Error("zero")
	}
}

func TestClientHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1":               "https://192.0.2.1",
		"192.0.2.1:8443":          "https://192.0.2.1:8443",
		"https://bmc.invalid/x/y": "https://bmc.invalid",
		"http://192.0.2.1:8000":   "http://192.0.2.1:8000",
	} {
		got, err := baseURL(in)
		if err != nil || got != want {
			t.Errorf("baseURL(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if got, err := baseURL(" https://bmc.invalid "); err != nil || got != "https://bmc.invalid" {
		t.Errorf("a padded address: %q, %v", got, err)
	}
	for _, in := range []string{"", "  ", "https://", "ftp://192.0.2.1", "ht tp://x"} {
		if got, err := baseURL(in); err == nil {
			t.Errorf("baseURL(%q) = %q, want an error", in, got)
		}
	}
	for in, want := range map[string]string{
		"/redfish/v1/Systems/1/":             "/redfish/v1/Systems/1/",
		"redfish/v1/Systems/1":               "/redfish/v1/Systems/1",
		"https://192.0.2.1/redfish/v1/A?b=1": "/redfish/v1/A?b=1",
		"/redfish/v1/Log?$skip=2":            "/redfish/v1/Log?$skip=2",
	} {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
	for _, tc := range []struct{ body, want string }{
		{`{"error": {"code": "x", "message": "See extended info", "@Message.ExtendedInfo": [{"Message": "Login failed."}]}}`, "Login failed."},
		{`{"error": {"message": "Plain message"}}`, "Plain message"},
		{`<html>not json</html>`, ""},
		{``, ""},
		{`{"error": {"message": "` + strings.Repeat("m", 300) + `"}}`, strings.Repeat("m", 199) + "…"},
	} {
		if got := errorMessage([]byte(tc.body)); got != tc.want {
			t.Errorf("errorMessage(%.40q) = %q, want %q", tc.body, got, tc.want)
		}
	}
	e := &httpError{Path: "/p", Status: 401}
	if !e.Auth() || e.Error() != "GET /p: HTTP 401" {
		t.Errorf("%v", e)
	}
	e = &httpError{Path: "/p", Status: 500, Message: "boom"}
	if e.Auth() || e.Error() != "GET /p: HTTP 500: boom" {
		t.Errorf("%v", e)
	}
	if (&httpError{Status: 403}).Auth() != true {
		t.Error("403 is a privilege refusal")
	}
}

// TestClientOnlyReads proves, on the wire, that the client sends GET with
// Basic credentials on every request and offers no way to send anything else.
func TestClientOnlyReads(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" auth="+u+":"+p+" "+r.Header.Get("Accept"))
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()
	c := &client{base: srv.URL, http: srv.Client(), user: "u", pass: "p"}
	var out struct{ OK bool }
	if err := c.get(context.Background(), "/a?b=1", &out); err != nil || !out.OK {
		t.Fatalf("%v %+v", err, out)
	}
	want := []string{"GET /a?b=1 auth=u:p application/json"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("requests %q, want %q", seen, want)
	}

	// A refused context and a closed server are errors, not panics.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.get(ctx, "/a", &out); err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if err := c.get(context.Background(), "/bad path\x7f", &out); err == nil {
		t.Error("an invalid request URL must be an error")
	}
}

func TestReleaseDate(t *testing.T) {
	for _, tc := range []struct{ reported, version, want string }{
		{"2021-03-05T00:00:00Z", "2.10.5", "2021-03-05"},
		{"2021-03-05", "2.10.5", "2021-03-05"},
		// The reported date wins over the one in the version.
		{"2022-01-02T10:00:00+01:00", "P89 v2.60 (05/21/2018)", "2022-01-02"},
		// Dell's placeholder and nonsense fall back to the version.
		{"00:00:00Z", "2.83.83.83", ""},
		{"1970-01-01T00:00:00Z", "P89 v2.60 (05/21/2018)", "2018-05-21"},
		{"", "U30 v2.72 (07/09/2021)", "2021-07-09"},
		{"", "2.78 Dec 08 2020", "2020-12-08"},
		{"", "2.72 Aug 4 2022", "2022-08-04"},
		// Not a date: month 13, a version that only looks dotted.
		{"", "P89 v2.60 (13/21/2018)", ""},
		{"", "25.5.5.0005", ""},
		{"", "", ""},
	} {
		if got := releaseDate(tc.reported, tc.version); got != tc.want {
			t.Errorf("releaseDate(%q, %q) = %q, want %q", tc.reported, tc.version, got, tc.want)
		}
	}
}

func TestFirmwareShapes(t *testing.T) {
	for raw, want := range map[string]string{`" Slot 0 "`: "Slot 0", `{"Info": "Slot 0"}`: "", `[]`: "", ``: ""} {
		if got := fwLocation(json.RawMessage(raw)); got != want {
			t.Errorf("fwLocation(%s) = %q, want %q", raw, got, want)
		}
	}
	for name, want := range map[string]bool{"Backup System ROM": true, "Redundant System ROM": true, "System ROM": false, "iLO 5": false} {
		if standbyImage(name) != want {
			t.Errorf("standbyImage(%q) != %v", name, want)
		}
	}
}
