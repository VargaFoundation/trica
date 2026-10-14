package pfsense

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// The fixtures are SYNTHESISED from the pfrest v2 documentation, not recorded
// on a firewall: see testdata/README.md.
//
//go:embed testdata
var fixtures embed.FS

// Device versions the fixtures model.
const (
	Version272 = "2.7.2" // pfSense CE 2.7.2: LAN, WAN on DHCP, a VLAN, OpenVPN, IPsec, rules and NAT
	Version281 = "2.8.1" // pfSense CE 2.8.1 patch 1: virtual machine, static WAN, IPv6, 1:1 NAT
)

// APIKey is the key a FakePfSense accepts unless Key is changed.
const APIKey = "fake-api-key-0000" //nolint:gosec // G101: the fixed key of an in-process fake, not a credential

// FakeRequest is one request received by the fake. The API key is not kept.
type FakeRequest struct {
	Method string
	Path   string // path below /api/v2, e.g. "status/system"
	Query  string
}

// FakePfSense is an in-process pfSense REST API v2 serving the fixtures of one
// device version over TLS (a self-signed certificate, as on a real firewall:
// targets use tls.insecure). It checks the API key, answers GET only and
// records every request, so tests can assert that a collector never writes.
//
// The failure knobs may be called at any time, from any goroutine.
type FakePfSense struct {
	server  *httptest.Server
	secret  string // path of the file holding APIKey, for Target
	version string

	mu        sync.Mutex
	key       string
	requests  []FakeRequest
	status    map[string]int           // endpoint -> forced HTTP status
	malformed map[string]bool          // endpoint -> body is not JSON
	body      map[string]string        // endpoint -> replacement body
	delay     map[string]time.Duration // endpoint -> response delay ("" = all)
	missing   map[string]bool          // endpoint -> 404, as on an older API
}

// NewFake starts a fake firewall of the given version (Version272, Version281).
// The caller must Close it.
func NewFake(version string) (*FakePfSense, error) {
	if _, err := fs.Stat(fixtures, "testdata/"+version); err != nil {
		return nil, fmt.Errorf("pfsense fake: no fixtures for version %q", version)
	}
	dir, err := os.MkdirTemp("", "pfsense-fake-")
	if err != nil {
		return nil, err
	}
	secret := filepath.Join(dir, "api-key")
	if err := os.WriteFile(secret, []byte(APIKey+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	f := &FakePfSense{
		version:   version,
		secret:    secret,
		key:       APIKey,
		status:    map[string]int{},
		malformed: map[string]bool{},
		body:      map[string]string{},
		delay:     map[string]time.Duration{},
		missing:   map[string]bool{},
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	return f, nil
}

// Close stops the server and removes the key file.
func (f *FakePfSense) Close() {
	f.server.Close()
	_ = os.RemoveAll(filepath.Dir(f.secret))
}

// Target returns a collect.Target aimed at the fake: its address, the key in a
// file, TLS verification off (self-signed certificate).
func (f *FakePfSense) Target(name string) collect.Target {
	return collect.Target{
		Kind:     Kind,
		Name:     name,
		Address:  strings.TrimPrefix(f.server.URL, "https://"),
		Password: collect.Secret("file:" + f.secret),
		TLS:      collect.TLS{Insecure: true},
		Timeout:  10 * time.Second,
	}
}

// URL is the base URL of the fake, https://127.0.0.1:port.
func (f *FakePfSense) URL() string { return f.server.URL }

// SetKey changes the key the fake accepts: the collector's key then fails.
func (f *FakePfSense) SetKey(key string) { f.lock(func() { f.key = key }) }

// Fail makes endpoint answer with an HTTP status (500, 403...) and a pfrest
// error envelope.
func (f *FakePfSense) Fail(endpoint string, status int) {
	f.lock(func() { f.status[endpoint] = status })
}

// Malformed makes endpoint answer 200 with a body that is not JSON; endpoint
// "" does it for every endpoint, as a web server that is not the REST API.
func (f *FakePfSense) Malformed(endpoint string) { f.lock(func() { f.malformed[endpoint] = true }) }

// Missing makes endpoint answer 404, as an install without that endpoint;
// endpoint "" does it for every endpoint.
func (f *FakePfSense) Missing(endpoint string) { f.lock(func() { f.missing[endpoint] = true }) }

// Slow delays the answer of endpoint by d; endpoint "" delays every answer.
func (f *FakePfSense) Slow(endpoint string, d time.Duration) {
	f.lock(func() { f.delay[endpoint] = d })
}

// SetBody replaces the answer of endpoint with body, a raw JSON document.
func (f *FakePfSense) SetBody(endpoint, body string) { f.lock(func() { f.body[endpoint] = body }) }

// Requests returns a copy of every request received so far.
func (f *FakePfSense) Requests() []FakeRequest {
	var out []FakeRequest
	f.lock(func() { out = append(out, f.requests...) })
	return out
}

// OnlyReads returns an error naming the first request that was not a GET.
func (f *FakePfSense) OnlyReads() error {
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet {
			return fmt.Errorf("non-read request: %s %s", r.Method, r.Path)
		}
	}
	return nil
}

func (f *FakePfSense) lock(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

const apiPrefix = "/api/v2/"

func (f *FakePfSense) handle(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, apiPrefix)
	var (
		key       string
		status    int
		malformed bool
		missing   bool
		body      string
		delay     time.Duration
	)
	f.lock(func() {
		f.requests = append(f.requests, FakeRequest{Method: r.Method, Path: endpoint, Query: r.URL.RawQuery})
		key, status = f.key, f.status[endpoint]
		malformed, missing = f.malformed[endpoint] || f.malformed[""], f.missing[endpoint] || f.missing[""]
		body = f.body[endpoint]
		delay = f.delay[""]
		if d, ok := f.delay[endpoint]; ok {
			delay = d
		}
	})
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case !strings.HasPrefix(r.URL.Path, apiPrefix):
		http.NotFound(w, r)
	case r.Header.Get("X-API-Key") != key:
		fakeError(w, http.StatusUnauthorized, "unauthorized", "AUTHENTICATION_FAILED", "Authentication failed")
	case r.Method != http.MethodGet:
		// The collector must never get here; the record above proves it did.
		fakeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED", "the fake serves GET only")
	case status != 0:
		fakeError(w, status, "error", "FORCED_FAILURE", "forced failure")
	case malformed:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>not json"))
	case missing:
		fakeError(w, http.StatusNotFound, "not found", "NOT_FOUND", "no such endpoint")
	case body != "":
		_, _ = w.Write([]byte(body))
	default:
		b, err := fixtures.ReadFile(path.Join("testdata", f.version, endpoint+".json"))
		if err != nil {
			fakeError(w, http.StatusNotFound, "not found", "NOT_FOUND", "no such endpoint")
			return
		}
		_, _ = w.Write(b) //nolint:gosec // G705: JSON response of a test fake, Content-Type application/json
	}
}

func fakeError(w http.ResponseWriter, code int, status, id, msg string) {
	w.WriteHeader(code)
	b, _ := json.Marshal(envelope{Code: code, Status: status, ResponseID: id, Message: msg, Data: json.RawMessage("[]")})
	_, _ = w.Write(b)
}
