package synology

import (
	"encoding/json"
	"io/fs"
	"net/netip"
	"regexp"
	"strings"
	"testing"
)

var (
	macDash  = regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?:[-:][0-9a-f]{2}){5}\b`)
	macPlain = regexp.MustCompile(`(?i)^[0-9a-f]{12}$`)
	ipv4     = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	serialOK = regexp.MustCompile(`^(0000ABCDE\d{4}|FAKEHDD\d{4}|FAKENVME\d{5})$`)
	fakeMAC  = regexp.MustCompile(`(?i)^02[-:]11[-:]32[-:]00[-:]00[-:][0-9a-f]{2}$`)
)

// The fixtures were recorded on a real NAS: every value that identifies it is
// replaced, and this keeps it so. A new recording that forgets a MAC, a
// serial number, a private address or a credential fails here, before it is
// committed.
func TestFixturesAreAnonymised(t *testing.T) {
	files := 0
	err := fs.WalkDir(fixtures, "testdata", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		files++
		b, err := fs.ReadFile(fixtures, path)
		if err != nil {
			return err
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		for _, p := range anonymisationProblems(doc, "") {
			t.Errorf("%s: %s", path, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 18 {
		t.Fatalf("only %d fixture files scanned: the scan is broken", files)
	}
}

func TestAnonymisationScanCatchesLeaks(t *testing.T) {
	for name, doc := range map[string]string{
		"real MAC":          `{"mac":"00-11-32-12-34-56"}`,
		"colon MAC":         `{"mac":"00:11:32:12:34:56"}`,
		"another fake MAC":  `{"mac":"02-11-33-00-00-01"}`,
		"plain MAC":         `{"id":"ac8ba9123456"}`,
		"plain digit MAC":   `{"mac":"001132123456"}`,
		"MAC in a sentence": `{"name":"eth 00-11-32-12-34-56 lan"}`,
		"real serial":       `{"serial":"2150ABC123456"}`,
		"disk serial":       `{"ui_serial":"WD-WX12A3456789"}`,
		"machine serial":    `{"machineSerial":"2150ABC123456"}`,
		"private IPv4":      `{"ip":"10.1.2.3"}`,
		"private 192.168":   `{"ip":"192.168.1.20"}`,
		"private 172":       `{"ip":"172.16.4.4"}`,
		"public IPv4":       `{"ip":"8.8.8.8"}`,
		"IPv4 in a note":    `{"note":"nas at 10.0.0.9"}`,
		"public IPv6":       `{"ip":"2a01:e0a::1"}`,
		"password":          `{"password":"x"}`,
		"passwd":            `{"passwd":"x"}`,
		"session id":        `{"sid":"abcdef"}`,
		"token":             `{"synotoken":"abcdef"}`,
		"snmp community":    `{"snmp_community":"public"}`,
		"e-mail":            `{"mail":"someone@corp.example"}`,
	} {
		var v any
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			t.Fatal(err)
		}
		if len(anonymisationProblems(v, "")) == 0 {
			t.Errorf("%s: not reported", name)
		}
	}
	for name, doc := range map[string]string{
		"fake MAC":      `{"mac":"02-11-32-00-00-0a","x":"02:11:32:00:00:0A"}`,
		"documentation": `{"a":"192.0.2.1","b":"198.51.100.2","c":"203.0.113.10","d":"0.0.0.0"}`,
		"link-local":    `{"a":"169.254.14.47","b":"fe80::1"}`,
		"netmasks":      `{"mask":"255.255.255.0","m2":"255.255.0.0","m3":"255.255.255.128"}`,
		"versions":      `{"version":"7.4.1.2","firmware_ver":"DSM 7.4.1-90080","dsm_version":"1.2.3.4"}`,
		"a size":        `{"size_total":"908576759808"}`,
		"fake serials":  `{"serial":"0000ABCDE0001","ui_serial":"FAKEHDD0001","machineSerial":"FAKENVME00001"}`,
		"empty secret":  `{"snmp_community":"","passwd":""}`,
	} {
		var v any
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			t.Fatal(err)
		}
		if p := anonymisationProblems(v, ""); len(p) != 0 {
			t.Errorf("%s: reported %v", name, p)
		}
	}
}

func anonymisationProblems(v any, key string) []string {
	var out []string
	lk := strings.ToLower(key)
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			out = append(out, anonymisationProblems(x, k)...)
		}
	case []any:
		for _, x := range v {
			out = append(out, anonymisationProblems(x, key)...)
		}
	case string:
		for _, secret := range []string{"passw", "passwd", "secret", "community", "token", "sid"} {
			if v != "" && (lk == secret || (secret != "sid" && strings.Contains(lk, secret))) {
				out = append(out, key+" holds a credential")
				break
			}
		}
		if strings.Contains(lk, "serial") && v != "" && !serialOK.MatchString(v) {
			out = append(out, "serial "+v+" is not one of the fake forms")
		}
		for _, m := range macDash.FindAllString(v, -1) {
			if !fakeMAC.MatchString(m) {
				out = append(out, "MAC "+m+" is not 02-11-32-00-00-xx")
			}
		}
		// A 12-digit number is a size; a 12-character hex string, or any value
		// in a field called mac, is an address.
		if macPlain.MatchString(v) && (strings.Contains(lk, "mac") || strings.ContainsAny(strings.ToLower(v), "abcdef")) {
			out = append(out, "MAC-like value "+v)
		}
		// Versions look like addresses ("7.4.1.2"): they are not read as such.
		if !strings.Contains(lk, "ver") {
			for _, a := range ipv4.FindAllString(v, -1) {
				if !allowedIPv4(a) {
					out = append(out, "address "+a+" is outside the documentation and link-local ranges")
				}
			}
		}
		if addr, err := netip.ParseAddr(v); err == nil && addr.Is6() && !addr.Is4In6() {
			if !addr.IsLinkLocalUnicast() && !netip.MustParsePrefix("2001:db8::/32").Contains(addr) {
				out = append(out, "address "+v+" is not link-local or documentation")
			}
		}
		if strings.Contains(v, "@") {
			out = append(out, "e-mail "+v)
		}
	}
	return out
}

func allowedIPv4(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return true // not an address ("999.1.1.1" in a version)
	}
	for _, p := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "0.0.0.0/32", "169.254.0.0/16"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return true
		}
	}
	return isNetmask(a)
}

// isNetmask reports whether a is a contiguous IPv4 netmask (255.255.255.0).
func isNetmask(a netip.Addr) bool {
	if !a.Is4() {
		return false
	}
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	inv := ^v
	return v != 0 && inv&(inv+1) == 0
}
