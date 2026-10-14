package maas

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAuthHeaderFormat(t *testing.T) {
	c := credentials{consumerKey: "ck", tokenKey: "tk", tokenSecret: "ts/with space"}
	got := c.authHeader("n0nce", time.Unix(1700000000, 0))
	want := `OAuth oauth_version="1.0", oauth_signature_method="PLAINTEXT"` +
		`, oauth_consumer_key="ck", oauth_token="tk", oauth_signature="&ts%2Fwith%20space"` +
		`, oauth_nonce="n0nce", oauth_timestamp="1700000000"`
	if got != want {
		t.Errorf("header =\n%s\nwant\n%s", got, want)
	}
}

func TestPctEncode(t *testing.T) {
	tests := map[string]string{
		"AZaz09-._~": "AZaz09-._~",
		"a b":        "a%20b",
		"a&b=c":      "a%26b%3Dc",
		"é":          "%C3%A9",
		"":           "",
	}
	for in, want := range tests {
		if got := pctEncode(in); got != want {
			t.Errorf("pctEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseAPIKey(t *testing.T) {
	c, err := parseAPIKey(" a:b:c \n")
	if err != nil || c != (credentials{"a", "b", "c"}) {
		t.Errorf("got %+v, %v", c, err)
	}
	for _, bad := range []string{"", "a", "a:b", "a:b:c:d", "a::c", ":b:c", "a:b:"} {
		_, err := parseAPIKey(bad)
		if err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := parseAPIKey("zzsecretzz:onlytwo"); err == nil || strings.Contains(err.Error(), "zzsecretzz") {
		t.Errorf("the error echoes the key: %v", err)
	}
}

func TestNoncesDoNotRepeat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		n, err := newNonce()
		if err != nil {
			t.Fatal(err)
		}
		if len(n) != 32 || seen[n] {
			t.Fatalf("bad or repeated nonce %q", n)
		}
		seen[n] = true
	}
}

// The fake refuses a replayed nonce like MAAS does, so a client that reused one
// would fail the collector tests. This pins the fake's own behaviour.
func TestFakeRejectsReplayAndWrongMethods(t *testing.T) {
	f := newFake(t, V35)
	creds, _ := parseAPIKey(f.APIKey())
	do := func(method, header string) int {
		req, _ := http.NewRequest(method, f.URL()+apiPath+"version/", http.NoBody)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	h := creds.authHeader("same-nonce", time.Now())
	if s := do(http.MethodGet, h); s != http.StatusOK {
		t.Fatalf("first use: %d", s)
	}
	if s := do(http.MethodGet, h); s != http.StatusUnauthorized {
		t.Errorf("replayed nonce: %d, want 401", s)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if s := do(m, creds.authHeader("n-"+m, time.Now())); s != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, want 405", m, s)
		}
	}
	for name, header := range map[string]string{
		"no header":        "",
		"not OAuth":        "Bearer abc",
		"garbled":          "OAuth nonsense",
		"bad escape":       `OAuth oauth_nonce="%zz"`,
		"wrong signature":  strings.Replace(creds.authHeader("n-sig", time.Now()), "&faketokensecret", "&nope", 1),
		"wrong method":     strings.Replace(creds.authHeader("n-m", time.Now()), "PLAINTEXT", "HMAC-SHA1", 1),
		"bad timestamp":    strings.Replace(creds.authHeader("n-ts", time.Now()), "oauth_timestamp=\"", "oauth_timestamp=\"x", 1),
		"empty nonce":      creds.authHeader("", time.Now()),
		"wrong consumer":   strings.Replace(creds.authHeader("n-c", time.Now()), `"fakeconsumer"`, `"other"`, 1),
		"wrong token":      strings.Replace(creds.authHeader("n-t", time.Now()), `"faketoken"`, `"other"`, 1),
		"wrong oauth vers": strings.Replace(creds.authHeader("n-v", time.Now()), `"1.0"`, `"2.0"`, 1),
	} {
		if s := do(http.MethodGet, header); s != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, s)
		}
	}
	// Unknown endpoints and operations are 404, not silently served.
	for _, path := range []string{"nodes/", "machines/?op=details", "zones/"} {
		req, _ := http.NewRequest(http.MethodGet, f.URL()+apiPath+path, http.NoBody)
		req.Header.Set("Authorization", creds.authHeader("n-"+path, time.Now()))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, f.URL()+"/elsewhere", http.NoBody)
	req.Header.Set("Authorization", creds.authHeader("n-else", time.Now()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("outside the API root: %d, want 404", resp.StatusCode)
	}
}

func TestFakePanicsOnUnknownVersion(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewFake accepted a version without fixtures")
		}
	}()
	NewFake("9.9")
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	c := newClient(srv.URL, srv.Client(), credentials{"a", "b", "c"})
	var out any
	err := c.get(context.Background(), "machines/", nil, &out)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusFound {
		t.Errorf("err = %v, want the 302 reported", err)
	}
	if hits != 1 {
		t.Errorf("%d requests, a redirect must not be followed", hits)
	}
}

func TestClientQueryAndBadBodies(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`{"username": "x"}`))
	}))
	defer srv.Close()
	c := newClient(srv.URL+"/", srv.Client(), credentials{"a", "b", "c"})
	var w whoami
	if err := c.get(context.Background(), "users/", url.Values{"op": {"whoami"}}, &w); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "op=whoami" || w.Username != "x" {
		t.Errorf("query %q whoami %+v", gotQuery, w)
	}
	// A JSON document of the wrong shape is malformed too.
	var list []machine
	err := c.get(context.Background(), "users/", nil, &list)
	if err == nil || !strings.Contains(err.Error(), "malformed response") {
		t.Errorf("err = %v", err)
	}
	if fatal(err) {
		t.Error("a malformed answer must not stop the run")
	}
	// A cancelled context is a transport failure.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.get(ctx, "users/", nil, &w); !fatal(err) {
		t.Errorf("cancelled: %v", err)
	}
	// An address the HTTP stack cannot parse.
	bad := newClient("http://exa mple.invalid", srv.Client(), credentials{"a", "b", "c"})
	if err := bad.get(context.Background(), "x/", nil, &w); err == nil {
		t.Error("bad URL accepted")
	}
}

func TestNumUnmarshal(t *testing.T) {
	tests := []struct {
		in      string
		want    num
		wantErr bool
	}{
		{in: `42`, want: 42},
		{in: `"480103981056"`, want: 480103981056},
		{in: `480.9`, want: 480},
		{in: `null`, want: 0},
		{in: `""`, want: 0},
		{in: `"abc"`, wantErr: true},
	}
	for _, tc := range tests {
		var got num
		err := json.Unmarshal([]byte(tc.in), &got)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("%s: got %d, %v", tc.in, got, err)
		}
	}
	var v struct {
		N num `json:"n"`
	}
	if err := json.Unmarshal([]byte(`{"n": "x"}`), &v); err == nil {
		t.Error("a non-numeric string must be an error")
	}
}

func TestClean(t *testing.T) {
	for _, ph := range []string{
		"", "  ", "Unknown", "unknown", "Not Specified", "To be filled by O.E.M.", "0123456789",
		"Default string", "N/A", "None", "00000000", "0", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF",
		"00000000-0000-0000-0000-000000000000", "03000200-0400-0500-0006-000700080009",
	} {
		if got := clean(ph); got != "" {
			t.Errorf("clean(%q) = %q, want a placeholder", ph, got)
		}
	}
	for in, want := range map[string]string{
		" SN-FAKE-0001 ":                       "SN-FAKE-0001",
		"02a1b2c3-0000-4000-8000-000000000001": "02a1b2c3-0000-4000-8000-000000000001",
		"F00D":                                 "F00D",
		"Example Systems":                      "Example Systems",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiskMedia(t *testing.T) {
	tests := []struct {
		name      string
		tags      []string
		media, pr string
	}{
		{"sda", []string{"ssd", "sata"}, "ssd", "SATA"},
		{"sdb", []string{"rotary", "SAS"}, "hdd", "SAS"},
		{"sdc", []string{"hdd"}, "hdd", ""},
		{"nvme0n1", nil, "nvme", "NVMe"},
		{"x", []string{"nvme"}, "nvme", "NVMe"},
		{"vda", nil, "", ""},
	}
	for _, tc := range tests {
		m, p := diskMedia(tc.name, tc.tags)
		if m != tc.media || p != tc.pr {
			t.Errorf("diskMedia(%q, %v) = %q, %q; want %q, %q", tc.name, tc.tags, m, p, tc.media, tc.pr)
		}
	}
}

func TestVisibleToKeepsOwnAndUnallocated(t *testing.T) {
	in := `[{"system_id":"a","owner":""},{"system_id":"b","owner":"bob"},{"system_id":"c","owner":"alice"},{"system_id":"d","owner":null}]`
	var got []map[string]any
	if err := json.Unmarshal(visibleTo([]byte(in), "alice"), &got); err != nil {
		t.Fatal(err)
	}
	var ids []any
	for _, m := range got {
		ids = append(ids, m["system_id"])
	}
	if !reflect.DeepEqual(ids, []any{"a", "c", "d"}) {
		t.Errorf("visible = %v", ids)
	}
	if out := visibleTo([]byte("not json"), "alice"); string(out) != "not json" {
		t.Errorf("garbage altered: %q", out)
	}
}
