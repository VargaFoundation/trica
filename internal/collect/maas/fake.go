package maas

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The fixtures are SYNTHESISED from the MAAS API documentation, not recorded
// from a real region controller: see testdata/README.md.
//
//go:embed testdata/v3.3/*.json testdata/v3.5/*.json
var fixtures embed.FS

// Version selects the MAAS release a FakeMAAS imitates.
type Version string

// The releases the fixtures model.
const (
	V33 Version = "3.3"
	V35 Version = "3.5"
)

// Endpoint names, for FailEndpoint and Malform.
const (
	EndpointVersion  = "version"
	EndpointWhoami   = "whoami"
	EndpointMachines = "machines"
	EndpointSubnets  = "subnets"
	EndpointFabrics  = "fabrics"
)

// The credentials the fake accepts. They are obviously fake.
const (
	fakeConsumerKey = "fakeconsumer"
	fakeTokenKey    = "faketoken"
	fakeTokenSecret = "faketokensecret"
	fakeUser        = "alice" // the non-administrator of AsNonAdmin
)

// Request is one request the fake received.
type Request struct {
	Method string
	Path   string
	Query  string
}

// FakeMAAS is an in-process MAAS region controller API. It checks the OAuth
// 1.0a PLAINTEXT header like MAAS does (including nonce replay), serves
// fixtures, records every request so that tests can assert a collector only
// reads, and can simulate the failures a collector must survive.
type FakeMAAS struct {
	srv *httptest.Server
	dir string

	mu        sync.Mutex
	reqs      []Request
	nonces    map[string]bool
	failAuth  bool
	failures  map[string]int
	malformed map[string]bool
	delay     time.Duration
	nonAdmin  bool
}

// NewFake starts a fake for the given release. Close it when done.
func NewFake(v Version) *FakeMAAS {
	if v != V33 && v != V35 {
		panic(fmt.Sprintf("maas: no fixtures for version %q", v))
	}
	f := &FakeMAAS{
		dir:       "testdata/v" + string(v),
		nonces:    map[string]bool{},
		failures:  map[string]int{},
		malformed: map[string]bool{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// URL is the address to give the collector (no trailing path).
func (f *FakeMAAS) URL() string { return f.srv.URL }

// APIKey is the key the collector must present: "consumer:token:secret".
func (f *FakeMAAS) APIKey() string {
	return fakeConsumerKey + ":" + fakeTokenKey + ":" + fakeTokenSecret
}

// Close stops the server.
func (f *FakeMAAS) Close() { f.srv.Close() }

// Requests returns a copy of what the fake received, in order.
func (f *FakeMAAS) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.reqs...)
}

// FailAuth makes the fake refuse every request with HTTP 401.
func (f *FakeMAAS) FailAuth() { f.set(func() { f.failAuth = true }) }

// FailEndpoint makes one endpoint answer with the given HTTP status.
func (f *FakeMAAS) FailEndpoint(name string, status int) {
	f.set(func() { f.failures[name] = status })
}

// Malform makes one endpoint answer 200 with a truncated JSON document.
func (f *FakeMAAS) Malform(name string) { f.set(func() { f.malformed[name] = true }) }

// SetDelay makes every answer wait d before it is sent (timeout tests).
func (f *FakeMAAS) SetDelay(d time.Duration) { f.set(func() { f.delay = d }) }

// AsNonAdmin makes the API key belong to a plain user: whoami says so and the
// machine list only holds what MAAS shows such a user, the unallocated
// machines and the ones the user owns.
func (f *FakeMAAS) AsNonAdmin() { f.set(func() { f.nonAdmin = true }) }

func (f *FakeMAAS) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *FakeMAAS) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery})
	delay, failAuth, nonAdmin := f.delay, f.failAuth, f.nonAdmin
	f.mu.Unlock()

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if failAuth || !f.authorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	name, file := f.route(r)
	if name == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	f.mu.Lock()
	status, bad := f.failures[name], f.malformed[name]
	f.mu.Unlock()
	if status != 0 {
		http.Error(w, "simulated failure", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if bad {
		_, _ = w.Write([]byte(`[{"system_id": "fk0a01", "hostname": `))
		return
	}
	if name == EndpointWhoami && nonAdmin {
		file = "whoami-user.json"
	}
	body, err := fixtures.ReadFile(f.dir + "/" + file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if name == EndpointMachines && nonAdmin {
		body = visibleTo(body, fakeUser)
	}
	_, _ = w.Write(body)
}

// route maps a request to an endpoint name and its fixture file.
func (f *FakeMAAS) route(r *http.Request) (name, file string) {
	rest, ok := strings.CutPrefix(r.URL.Path, apiPath)
	if !ok {
		return "", ""
	}
	op := r.URL.Query().Get("op")
	switch {
	case rest == "version/" && op == "":
		return EndpointVersion, "version.json"
	case rest == "users/" && op == "whoami":
		return EndpointWhoami, "whoami-admin.json"
	case rest == "machines/" && op == "":
		return EndpointMachines, "machines.json"
	case rest == "subnets/" && op == "":
		return EndpointSubnets, "subnets.json"
	case rest == "fabrics/" && op == "":
		return EndpointFabrics, "fabrics.json"
	}
	return "", ""
}

// authorized checks the OAuth 1.0a PLAINTEXT header like MAAS (django-piston)
// does: the signature is "&<token secret>", and a nonce is accepted once.
func (f *FakeMAAS) authorized(r *http.Request) bool {
	h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "OAuth ")
	if !ok {
		return false
	}
	p := map[string]string{}
	for _, part := range strings.Split(h, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return false
		}
		val, err := url.PathUnescape(strings.Trim(v, `"`))
		if err != nil {
			return false
		}
		p[k] = val
	}
	if p["oauth_version"] != "1.0" || p["oauth_signature_method"] != "PLAINTEXT" ||
		p["oauth_consumer_key"] != fakeConsumerKey || p["oauth_token"] != fakeTokenKey ||
		p["oauth_signature"] != "&"+fakeTokenSecret {
		return false
	}
	if _, err := strconv.ParseInt(p["oauth_timestamp"], 10, 64); err != nil {
		return false
	}
	nonce := p["oauth_nonce"]
	if nonce == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nonces[nonce] {
		return false // replay
	}
	f.nonces[nonce] = true
	return true
}

// visibleTo keeps the machines a non-administrator user sees in MAAS: the ones
// nobody owns and the ones the user owns.
func visibleTo(machines []byte, user string) []byte {
	var all []map[string]any
	if err := json.Unmarshal(machines, &all); err != nil {
		return machines
	}
	kept := all[:0]
	for _, m := range all {
		if owner, _ := m["owner"].(string); owner == "" || owner == user {
			kept = append(kept, m)
		}
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return machines
	}
	return out
}
