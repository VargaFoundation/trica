package unifi

import (
	"encoding/json"
	"io/fs"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	macColon = regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?::[0-9a-f]{2}){5}\b`)
	macPlain = regexp.MustCompile(`(?i)^[0-9a-f]{12}$`)
	ipv4     = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
)

// The fixtures were recorded on a real account: every value that identifies
// it is replaced, and this keeps it so. A new recording that forgets a MAC, a
// public address or an e-mail fails here, before it is committed.
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
	if files < 8 {
		t.Fatalf("only %d fixture files scanned: the scan is broken", files)
	}
}

// The scan reports what it claims to.
func TestAnonymisationScanCatchesLeaks(t *testing.T) {
	for name, doc := range map[string]string{
		"real MAC":          `{"mac":"ac:8b:a9:12:34:56"}`,
		"plain MAC":         `{"id":"ac8ba9123456"}`,
		"universal MAC":     `{"mac":"00:11:22:33:44:55"}`,
		"public IPv4":       `{"ip":"8.8.8.8"}`,
		"private IPv4":      `{"ip":"10.1.2.3"}`,
		"public IPv6":       `{"ip":"2a01:e0a::1"}`,
		"e-mail":            `{"email":"someone@corp.example"}`,
		"coordinates":       `{"location":{"lat":48.85,"long":2.35,"radius":0}}`,
		"api key":           `{"apiKey":"abcdef"}`,
		"token":             `{"accessToken":"abcdef"}`,
		"password":          `{"password":"x"}`,
		"IPv4 in a note":    `{"note":"switch at 10.0.0.9"}`,
		"MAC in a sentence": `{"name":"AP 70:a7:41:00:00:01 lobby"}`,
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
		"fake MAC":      `{"mac":"02:00:00:00:00:0a","id":"02000000000A"}`,
		"documentation": `{"a":"192.0.2.1","b":"198.51.100.2","c":"203.0.113.10","d":"0.0.0.0"}`,
		"link-local":    `{"a":"fe80::0:ff:fe00:d"}`,
		"versions":      `{"version":"10.6.106.0","uiVersion":"1.2.3.4","updateAvailable":"4.13.9.0","firmwareVersion":"5.1.33"}`,
		"zero location": `{"location":{"lat":0,"long":0,"radius":0,"text":"Example City"}}`,
		"example mail":  `{"email":"operator@example.test"}`,
		"next token":    `{"nextToken":"page-2"}`,
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
	case float64:
		switch lk {
		case "lat", "long", "radius":
			if v != 0 {
				out = append(out, key+" is not 0: a real location")
			}
		}
	case string:
		for _, secret := range []string{"apikey", "password", "secret", "passwd"} {
			if strings.Contains(lk, secret) && v != "" {
				out = append(out, key+" holds a credential")
			}
		}
		if strings.Contains(lk, "token") && lk != "nexttoken" && v != "" {
			out = append(out, key+" holds a token")
		}
		for _, m := range macColon.FindAllString(v, -1) {
			if !localMAC(m) {
				out = append(out, "MAC "+m+" is not locally administered")
			}
		}
		if macPlain.MatchString(v) && !localMAC(v) {
			out = append(out, "MAC "+v+" is not locally administered")
		}
		// Versions look like addresses ("10.6.106.0"): they are not read as such.
		if !strings.Contains(lk, "version") && lk != "updateavailable" {
			for _, a := range ipv4.FindAllString(v, -1) {
				if !allowedIPv4(a) {
					out = append(out, "address "+a+" is outside the documentation ranges")
				}
			}
		}
		if addr, err := netip.ParseAddr(v); err == nil && addr.Is6() && !addr.Is4In6() {
			if !addr.IsLinkLocalUnicast() && !netip.MustParsePrefix("2001:db8::/32").Contains(addr) {
				out = append(out, "address "+v+" is not link-local or documentation")
			}
		}
		if strings.Contains(v, "@") && !strings.HasSuffix(v, "@example.test") {
			out = append(out, "e-mail "+v+" is outside example.test")
		}
	}
	return out
}

// localMAC reports whether a MAC is locally administered and unicast: the
// fake ones are 02:00:..., and no real vendor address looks like that.
func localMAC(s string) bool {
	h := strings.ReplaceAll(s, ":", "")
	if len(h) != 12 {
		return false
	}
	b, err := strconv.ParseUint(h[:2], 16, 8)
	return err == nil && b&0x02 != 0 && b&0x01 == 0
}

func allowedIPv4(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return true // not an address ("999.1.1.1" in a version)
	}
	for _, p := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "0.0.0.0/32", "127.0.0.0/8", "169.254.0.0/16"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return true
		}
	}
	return false
}
