package unifi

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// Fixture sets of the Network Integration API. Both are SYNTHESISED: nothing
// in them was observed on a console (testdata/README.md).
const (
	// SetNetwork is the office of SetRecorded as its console's own API would
	// describe it: the same ten devices, names, MACs and LAN addresses, with
	// their ports, PoE and uplinks.
	SetNetwork = "network-synthesised"
	// SetNetworkEdge has what the office does not: two sites and one that has
	// an id that is not a UUID, a gateway, port states and connectors the
	// office has not, a device whose detail answers 404, ids and MACs of
	// every awkward shape, uplinks to unknown devices and across sites.
	SetNetworkEdge = "network-edge"
)

// FakeNetworkRequest is one request received by FakeNetwork. The API key is not
// kept, only whether the right one came in the X-API-KEY header.
type FakeNetworkRequest struct {
	Method string
	// Path is relative to the Network Integration API prefix ("/v1/sites");
	// when the request did not start with the prefix it is the raw path and
	// Prefixed is false.
	Path     string
	Query    string
	HasKey   bool
	Prefixed bool
}

// FakeNetwork is an in-process Network Integration API of a console serving
// the fixtures of one set over TLS (httptest's certificate: targets use
// tls.insecure). It lives under /proxy/network/integration, checks the
// X-API-KEY header, answers GET only (405 otherwise) and records every request,
// so tests can assert that a collector never writes and never asks for what it
// should not. The lists are paged by offset and limit like the real API: the
// fixture holds the whole list and the fake cuts it.
//
// The knobs take paths relative to the prefix and may be called at any time,
// from any goroutine.
type FakeNetwork struct {
	server *httptest.Server
	secret string // path of the file holding APIKey, for Target
	set    string

	mu        sync.Mutex
	key       string
	maxLimit  int // 0: the API's own maximum, netPageSize
	requests  []FakeNetworkRequest
	status    map[string]int
	malformed map[string]bool
	body      map[string]string
	delay     map[string]time.Duration
	redirect  map[string]string
}

// NewFakeNetwork starts a fake Network Integration API serving a fixture set
// (SetNetwork, SetNetworkEdge). The caller must Close it.
func NewFakeNetwork(set string) (*FakeNetwork, error) {
	if _, err := fs.Stat(fixtures, "testdata/"+set+"/integration"); err != nil {
		return nil, fmt.Errorf("unifi fake: no Network Integration API fixtures for set %q", set)
	}
	dir, err := os.MkdirTemp("", "unifi-fake-net-")
	if err != nil {
		return nil, err
	}
	secret := filepath.Join(dir, "api-key")
	if err := os.WriteFile(secret, []byte(APIKey+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	f := &FakeNetwork{
		set:       set,
		secret:    secret,
		key:       APIKey,
		status:    map[string]int{},
		malformed: map[string]bool{},
		body:      map[string]string{},
		delay:     map[string]time.Duration{},
		redirect:  map[string]string{},
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	return f, nil
}

// Close stops the server and removes the key file.
func (f *FakeNetwork) Close() {
	f.server.Close()
	_ = os.RemoveAll(filepath.Dir(f.secret))
}

// Target returns a collect.Target aimed at the fake with options.api: network:
// the address of the console, the key in a file, TLS verification off.
func (f *FakeNetwork) Target(name string) collect.Target {
	return collect.Target{
		Kind:     Kind,
		Name:     name,
		Address:  f.server.URL,
		Password: collect.Secret("file:" + f.secret),
		TLS:      collect.TLS{Insecure: true},
		Timeout:  10 * time.Second,
	}
}

// URL is the address of the console, https://127.0.0.1:port.
func (f *FakeNetwork) URL() string { return f.server.URL }

// SetKey changes the key the fake accepts: the collector's key then fails.
func (f *FakeNetwork) SetKey(key string) { f.lock(func() { f.key = key }) }

// MaxLimit makes the fake page at most n items whatever limit is asked, as a
// server may; 0 restores the API's maximum of 200.
func (f *FakeNetwork) MaxLimit(n int) { f.lock(func() { f.maxLimit = n }) }

// Fail makes path answer with an HTTP status in the API's Error Message shape;
// 429 carries a Retry-After.
func (f *FakeNetwork) Fail(path string, status int) { f.lock(func() { f.status[path] = status }) }

// Malformed makes path answer 200 with a body that is not JSON; path "" does it
// for every path, as a web server that is not the API.
func (f *FakeNetwork) Malformed(path string) { f.lock(func() { f.malformed[path] = true }) }

// Slow delays the answer of path by d; path "" delays every answer.
func (f *FakeNetwork) Slow(path string, d time.Duration) { f.lock(func() { f.delay[path] = d }) }

// SetBody replaces the answer of path with body, a raw JSON document.
func (f *FakeNetwork) SetBody(path, body string) { f.lock(func() { f.body[path] = body }) }

// Redirect makes path answer 302 to location.
func (f *FakeNetwork) Redirect(path, location string) {
	f.lock(func() { f.redirect[path] = location })
}

// Requests returns a copy of every request received so far.
func (f *FakeNetwork) Requests() []FakeNetworkRequest {
	var out []FakeNetworkRequest
	f.lock(func() { out = append(out, f.requests...) })
	return out
}

// OnlyReads returns an error naming the first request that was not a GET.
func (f *FakeNetwork) OnlyReads() error {
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet {
			return fmt.Errorf("non-read request: %s %s", r.Method, r.Path)
		}
	}
	return nil
}

func (f *FakeNetwork) lock(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *FakeNetwork) handle(w http.ResponseWriter, r *http.Request) {
	rel, prefixed := strings.CutPrefix(r.URL.Path, networkPrefix)
	if !prefixed {
		rel = r.URL.Path
	}
	var (
		key       string
		maxLimit  int
		status    int
		malformed bool
		body      string
		delay     time.Duration
		location  string
	)
	f.lock(func() {
		f.requests = append(f.requests, FakeNetworkRequest{Method: r.Method, Path: rel, Query: r.URL.RawQuery, HasKey: r.Header.Get("X-API-KEY") == f.key, Prefixed: prefixed})
		key, maxLimit, status = f.key, f.maxLimit, f.status[rel]
		malformed = f.malformed[rel] || f.malformed[""]
		body, location = f.body[rel], f.redirect[rel]
		delay = f.delay[""]
		if d, ok := f.delay[rel]; ok {
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
	case location != "":
		http.Redirect(w, r, location, http.StatusFound)
	case !prefixed:
		f.netError(w, r, http.StatusNotFound, "NOT_FOUND", "api.not-found", "no such endpoint")
	case r.Header.Get("X-API-KEY") != key:
		f.unauthorized(w)
	case r.Method != http.MethodGet:
		// The collector must never get here; the record above proves it did.
		f.netError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "api.method-not-allowed", "the fake serves GET only")
	case status != 0:
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "30")
		}
		f.netError(w, r, status, http.StatusText(status), "api.forced-failure", "forced failure")
	case malformed:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>not json"))
	case body != "":
		_, _ = w.Write([]byte(body))
	default:
		f.serve(w, r, rel, maxLimit)
	}
}

// serve answers from the fixtures: /v1/<x> is testdata/<set>/integration/v1/<x>.json
// for a single document, and the two lists (/v1/sites and
// /v1/sites/<id>/devices) are cut by offset and limit.
func (f *FakeNetwork) serve(w http.ResponseWriter, r *http.Request, rel string, maxLimit int) {
	name, ok := strings.CutPrefix(rel, "/v1/")
	if !ok || name == "" || strings.Contains(name, "..") || strings.Contains(name, "//") || strings.ContainsAny(name, `\%`) {
		f.netError(w, r, http.StatusNotFound, "NOT_FOUND", "api.not-found", "no such endpoint")
		return
	}
	b, err := fixtures.ReadFile(path.Join("testdata", f.set, "integration", "v1", name+".json"))
	if err != nil {
		f.netError(w, r, http.StatusNotFound, "NOT_FOUND", "api.not-found", "no such endpoint")
		return
	}
	parts := strings.Split(name, "/")
	isList := name == "sites" || (len(parts) == 3 && parts[0] == "sites" && parts[2] == "devices")
	if !isList {
		_, _ = w.Write(b) //nolint:gosec // G705: JSON response of a test fake, Content-Type application/json
		return
	}
	offset, limit, bad := listParams(r, maxLimit)
	if bad != "" {
		f.netError(w, r, http.StatusBadRequest, "BAD_REQUEST", "api.request.argument-type-mismatch", bad)
		return
	}
	_, _ = w.Write(cutPage(b, offset, limit)) //nolint:gosec // G705: JSON response of a test fake, Content-Type application/json
}

// listParams reads offset (default 0) and limit (default 25, at most 200) as
// the specification gives them.
func listParams(r *http.Request, maxLimit int) (offset, limit int, bad string) {
	offset, limit = 0, 25
	q := r.URL.Query()
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, 0, "offset must be a non-negative integer"
		}
		offset = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > netPageSize {
			return 0, 0, "limit must be an integer between 0 and 200"
		}
		limit = n
	}
	if maxLimit > 0 && limit > maxLimit {
		limit = maxLimit
	}
	return offset, limit, ""
}

// cutPage returns the page [offset, offset+limit) of the fixture's list, with
// the five fields of a page recomputed.
func cutPage(doc []byte, offset, limit int) []byte {
	var env struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(doc, &env) != nil {
		return doc
	}
	total := len(env.Data)
	from := min(offset, total)
	to := min(from+limit, total)
	out, err := json.Marshal(map[string]any{
		"offset": offset, "limit": limit, "count": to - from, "totalCount": total, "data": env.Data[from:to],
	})
	if err != nil {
		return doc
	}
	return out
}

// unauthorized answers with the 401 body of the set (the example of the
// specification).
func (f *FakeNetwork) unauthorized(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized)
	if b, err := fixtures.ReadFile(path.Join("testdata", f.set, "errors", "401.json")); err == nil {
		_, _ = w.Write(b)
		return
	}
	_, _ = w.Write([]byte(`{"statusCode":401,"statusName":"UNAUTHORIZED","code":"api.authentication.missing-credentials","message":"Missing credentials"}`))
}

// netError answers in the API's Error Message shape.
func (f *FakeNetwork) netError(w http.ResponseWriter, r *http.Request, status int, name, code, msg string) {
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]any{
		"statusCode": status, "statusName": name, "code": code, "message": msg,
		"timestamp": "2026-10-23T09:00:00.000Z", "requestPath": strings.TrimPrefix(r.URL.Path, "/proxy/network"),
		"requestId": "00000000-0000-4000-8000-000000000000",
	})
	_, _ = w.Write(b)
}
