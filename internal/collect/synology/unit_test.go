package synology

import "testing"

func TestUptime(t *testing.T) {
	for in, want := range map[string]int64{
		"2:51:33": 10293, "0:00:05": 5, "30:05:09": 108309, "100:00:00": 360000, " " + "1:00:00 ": 3600,
	} {
		if got, ok := uptime(in); !ok || got != want {
			t.Errorf("uptime(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "5", "1:61:00", "1:00", "a:b:c", "-1:00:00", "1:2:3:4"} {
		if _, ok := uptime(in); ok {
			t.Errorf("uptime(%q) accepted", in)
		}
	}
}

func TestSplitImage(t *testing.T) {
	for in, want := range map[string][2]string{
		"netbirdio/netbird:0.78.1":                {"netbirdio/netbird", "0.78.1"},
		"nginx":                                   {"nginx", "latest"},
		"registry.example.test:5000/team/app":     {"registry.example.test:5000/team/app", "latest"},
		"registry.example.test:5000/team/app:2.0": {"registry.example.test:5000/team/app", "2.0"},
		"app:1.0@sha256:0000":                     {"app", "1.0"},
		"app@sha256:0000":                         {"app", "latest"},
		"":                                        {"", ""},
	} {
		if n, v := splitImage(in); n != want[0] || v != want[1] {
			t.Errorf("splitImage(%q) = %q, %q; want %q, %q", in, n, v, want[0], want[1])
		}
	}
}

func TestOSVersion(t *testing.T) {
	for in, want := range map[string]string{
		"DSM 7.4.1-90080 Update 1": "7.4.1-90080 Update 1", "dsm 7.2": "7.2", "7.2.2-72806": "7.2.2-72806", "": "",
	} {
		if got := osVersion(in); got != want {
			t.Errorf("osVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormMAC(t *testing.T) {
	for in, want := range map[string]string{
		"02-11-32-00-00-01": "02:11:32:00:00:01", "02:11:32:00:00:0A": "02:11:32:00:00:0a", "021132000001": "02:11:32:00:00:01",
		"": "", "02-11-32": "", "zz-11-32-00-00-01": "",
	} {
		if got := normMAC(in); got != want {
			t.Errorf("normMAC(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsableIP(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.57": "192.0.2.57", " " + "192.0.2.57 ": "192.0.2.57", "2001:db8::1": "2001:db8::1",
		"169.254.1.2": "", "fe80::1": "", "127.0.0.1": "", "0.0.0.0": "", "224.0.0.1": "", "": "", "nas.example.test": "",
	} {
		if got := usableIP(in); got != want {
			t.Errorf("usableIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrefixLength(t *testing.T) {
	for in, want := range map[string]int{"255.255.255.0": 24, "255.255.255.128": 25, "255.255.0.0": 16, "64": 64, "0": 0} {
		if got, ok := prefixLength(in); !ok || got != want {
			t.Errorf("prefixLength(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "255.0.255.0", "129", "-1", "fe80::", "abc"} {
		if _, ok := prefixLength(in); ok {
			t.Errorf("prefixLength(%q) accepted", in)
		}
	}
}

func TestStatusLevelAndHealth(t *testing.T) {
	for in, want := range map[string]string{
		"normal": "ok", "": "ok", " " + "Normal ": "ok", "attention": "warning", "degraded": "warning", "warning": "warning",
		"crashed": "critical", "Failed": "critical", "broken": "critical",
	} {
		if got := statusLevel(in); got != want {
			t.Errorf("statusLevel(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"normal": "ok", "warning": "warning", "attention": "warning", "crashed": "critical", "failed": "critical", "": "unknown", "initialized": "unknown"} {
		if got := diskHealth(in); got != want {
			t.Errorf("diskHealth(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUPSHealth(t *testing.T) {
	for in, want := range map[string]string{
		"usb_ups_status_online": "ok", "usb_ups_status_onbatt": "warning", "usb_ups_status_low_battery": "critical",
		"usb_ups_status_replace_battery": "warning", "": "unknown", "usb_ups_status_unknown": "unknown",
	} {
		if got := upsHealth(in); got != want {
			t.Errorf("upsHealth(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBaseMAC(t *testing.T) {
	nics := func(ids ...string) []nic {
		var out []nic
		for i, id := range ids {
			out = append(out, nic{ID: id, MAC: "02-11-32-00-00-0" + string(rune('9'-i))})
		}
		return out
	}
	if got := baseMAC(nics("eth1", "eth0", "eth2")); got != "02:11:32:00:00:08" {
		t.Errorf("eth0's MAC expected, got %q", got)
	}
	if got := baseMAC(nics("eth1", "eth2", "pppoe")); got != "02:11:32:00:00:08" {
		t.Errorf("the lowest MAC expected, got %q", got)
	}
	if got := baseMAC(nics("pppoe", "lo")); got != "" {
		t.Errorf("no physical port: %q", got)
	}
}

func TestTolerantDecoding(t *testing.T) {
	// DSM writes numbers as numbers or as strings, booleans as true or "yes".
	var d disk
	if err := unmarshalJSON(`{"size_total":"4000787030016","temp":33,"isSsd":"true","remain_life":{"trustable":"yes","value":"85"}}`, &d); err != nil {
		t.Fatal(err)
	}
	if v, ok := d.SizeTotal.Int(); !ok || v != 4000787030016 {
		t.Errorf("size %d %v", v, ok)
	}
	if !bool(d.IsSSD) || !bool(d.RemainLife.Trustable) {
		t.Errorf("flags %v %v", d.IsSSD, d.RemainLife.Trustable)
	}
	if v, ok := d.RemainLife.Value.Int(); !ok || v != 85 {
		t.Errorf("remain_life %d %v", v, ok)
	}
}
