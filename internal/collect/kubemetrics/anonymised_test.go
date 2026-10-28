package kubemetrics

import (
	"encoding/json"
	"io/fs"
	"net/netip"
	"regexp"
	"strings"
	"testing"
)

var (
	ipv4    = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	uuidRe  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	syntUID = regexp.MustCompile(`^00000000-0000-4000-8000-\d{12}$`)
	hpeUUID = regexp.MustCompile(`^32353537-3835-5a43-4a30-3030303[123]5[331]3[123]$`)
	secrets = []string{"passw", "secret", "token", "credential", "apikey", "api_key"}
	banned  = []string{"diametral", "moji", "recrut", "powerbi", "litellm", "ovh", "boondmanager"}
)

// The fixtures were recorded on a real cluster: every value that identifies it
// is replaced, and this keeps it so. A new recording that forgets a node name,
// a UID, a private address or a tenant name fails here, before it is
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
	if files != 6 {
		t.Fatalf("%d fixture files scanned, want 6: the scan is broken", files)
	}
}

func TestAnonymisationScanCatchesLeaks(t *testing.T) {
	for name, doc := range map[string]string{
		"private IPv4":    `{"pod_ip":"10.52.1.7"}`,
		"192.168":         `{"ip":"192.168.1.20"}`,
		"public IPv4":     `{"ip":"8.8.8.8"}`,
		"IP with a port":  `{"instance":"10.52.1.7:8080"}`,
		"real UID":        `{"uid":"6a3f9c1e-2b7d-4c58-9e10-0d4f7a8b1c22"}`,
		"real UUID":       `{"system_uuid":"32353537-3835-5a43-4a36-333333345335"}`,
		"company name":    `{"namespace":"diametral-prod"}`,
		"node name":       `{"node":"moji-fr1-cont-svr1"}`,
		"a password":      `{"password":"x"}`,
		"a token label":   `{"annotation_token":"x"}`,
		"a bearer secret": `{"secret_name":"x"}`,
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
		"documentation": `{"a":"192.0.2.1","b":"198.51.100.2:8080","c":"203.0.113.10","d":"198.51.100.0/26"}`,
		"synthetic UID": `{"uid":"00000000-0000-4000-8000-000000000042"}`,
		"synthetic HPE": `{"system_uuid":"32353537-3835-5a43-4a30-303030315331"}`,
		"versions":      `{"kubelet_version":"v1.31.1+k3s1","kernel_version":"6.8.0-60-generic"}`,
		"a timestamp":   `{"value":[1793200142,"1"]}`,
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

func documentation(a netip.Addr) bool {
	for _, p := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return true
		}
	}
	return false
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
		for _, s := range secrets {
			if v != "" && strings.Contains(lk, s) {
				out = append(out, key+" holds a credential")
			}
		}
		low := strings.ToLower(v)
		for _, b := range banned {
			if strings.Contains(low, b) {
				out = append(out, key+": "+v+" names the source cluster's operator or tenants")
			}
		}
		for _, m := range ipv4.FindAllString(v, -1) {
			if a, err := netip.ParseAddr(m); err != nil || (!documentation(a) && a != netip.MustParseAddr("0.0.0.0")) {
				out = append(out, key+": "+v+" holds an address outside the documentation ranges")
			}
		}
		for _, u := range uuidRe.FindAllString(v, -1) {
			if !syntUID.MatchString(u) && !hpeUUID.MatchString(u) {
				out = append(out, key+": "+u+" is a real UUID")
			}
		}
	}
	return out
}
