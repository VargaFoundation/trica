package unifi

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

// The fixtures are of two kinds, and testdata/README.md says which is which:
// a RECORDED set (one real console, anonymised) and a SYNTHESISED one.
//
//go:embed testdata
var fixtures embed.FS

// Fixture sets.
const (
	// SetRecorded is one UDM Pro Max console with ten adopted devices,
	// recorded on 2026-10-23 and anonymised.
	SetRecorded = "recorded-2026-10"
	// SetSynthesised has two pages of consoles and devices, a disconnected
	// console, a self-hosted server, a camera, an unknown model.
	SetSynthesised = "synthesised"
)

// APIKey is the key a FakeSiteManager accepts unless SetKey changes it.
const APIKey = "fake-unifi-key-0000" //nolint:gosec // G101: the fixed key of an in-process fake, not a credential

// FakeRequest is one request received by the fake. The API key is not kept,
// only whether the right one came in the X-API-KEY header.
type FakeRequest struct {
	Method string
	Path   string // e.g. "/v1/hosts"
	Query  string
	HasKey bool
}

// FakeSiteManager is an in-process Site Manager API serving the fixtures of
// one set over TLS (httptest's certificate: targets use tls.insecure). It
// checks the X-API-KEY header, answers GET only and records every request, so
// tests can assert that a collector never writes.
//
// The failure knobs may be called at any time, from any goroutine.
type FakeSiteManager struct {
	server *httptest.Server
	secret string // path of the file holding APIKey, for Target
	set    string

	mu        sync.Mutex
	key       string
	requests  []FakeRequest
	status    map[string]int
	malformed map[string]bool
	body      map[string]string
	delay     map[string]time.Duration
	redirect  map[string]string
	replayEnd time.Time // ReplayMetricsUntil
}

// NewFake starts a fake Site Manager serving a fixture set (SetRecorded,
// SetSynthesised). The caller must Close it.
func NewFake(set string) (*FakeSiteManager, error) {
	if _, err := fs.Stat(fixtures, "testdata/"+set); err != nil {
		return nil, fmt.Errorf("unifi fake: no fixtures for set %q", set)
	}
	dir, err := os.MkdirTemp("", "unifi-fake-")
	if err != nil {
		return nil, err
	}
	secret := filepath.Join(dir, "api-key")
	if err := os.WriteFile(secret, []byte(APIKey+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	f := &FakeSiteManager{
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
func (f *FakeSiteManager) Close() {
	f.server.Close()
	_ = os.RemoveAll(filepath.Dir(f.secret))
}

// Target returns a collect.Target aimed at the fake: its address, the key in a
// file, TLS verification off.
func (f *FakeSiteManager) Target(name string) collect.Target {
	return collect.Target{
		Kind:     Kind,
		Name:     name,
		Address:  f.server.URL,
		Password: collect.Secret("file:" + f.secret),
		TLS:      collect.TLS{Insecure: true},
		Timeout:  10 * time.Second,
	}
}

// URL is the base URL of the fake, https://127.0.0.1:port.
func (f *FakeSiteManager) URL() string { return f.server.URL }

// SetKey changes the key the fake accepts: the collector's key then fails.
func (f *FakeSiteManager) SetKey(key string) { f.lock(func() { f.key = key }) }

// Fail makes path answer with an HTTP status (500, 403, 429...) and the error
// envelope; 429 carries a Retry-After.
func (f *FakeSiteManager) Fail(path string, status int) { f.lock(func() { f.status[path] = status }) }

// Malformed makes path answer 200 with a body that is not JSON; path "" does
// it for every path, as a web server that is not the API.
func (f *FakeSiteManager) Malformed(path string) { f.lock(func() { f.malformed[path] = true }) }

// Slow delays the answer of path by d; path "" delays every answer.
func (f *FakeSiteManager) Slow(path string, d time.Duration) { f.lock(func() { f.delay[path] = d }) }

// SetBody replaces the answer of path with body, a raw JSON document.
func (f *FakeSiteManager) SetBody(path, body string) { f.lock(func() { f.body[path] = body }) }

// Redirect makes path answer 302 to location.
func (f *FakeSiteManager) Redirect(path, location string) {
	f.lock(func() { f.redirect[path] = location })
}

// ReplayMetricsUntil shifts the periods of the recorded ISP metrics so that the
// last one sits at end truncated to 5 minutes, for a test whose clock is far
// from the recording. The zero time restores the recorded times.
func (f *FakeSiteManager) ReplayMetricsUntil(end time.Time) {
	f.lock(func() { f.replayEnd = end })
}

// Requests returns a copy of every request received so far.
func (f *FakeSiteManager) Requests() []FakeRequest {
	var out []FakeRequest
	f.lock(func() { out = append(out, f.requests...) })
	return out
}

// OnlyReads returns an error naming the first request that was not a GET.
func (f *FakeSiteManager) OnlyReads() error {
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet {
			return fmt.Errorf("non-read request: %s %s", r.Method, r.Path)
		}
	}
	return nil
}

func (f *FakeSiteManager) lock(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// waitOrAbort holds the answer for d. When the request ends first (the client
// gave up, or the server closes), it aborts the handler instead of returning:
// a handler that returns without writing makes net/http send "200 OK" with an
// empty body, which a client still reading would take for a real, empty
// answer (a malformed one) instead of the transport failure a slow endpoint
// stands for. Panicking with http.ErrAbortHandler closes the connection
// without a response and without a log line.
func waitOrAbort(r *http.Request, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-r.Context().Done():
		panic(http.ErrAbortHandler)
	}
}

func (f *FakeSiteManager) handle(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	var (
		key       string
		status    int
		malformed bool
		body      string
		delay     time.Duration
		location  string
	)
	f.lock(func() {
		f.requests = append(f.requests, FakeRequest{Method: r.Method, Path: p, Query: r.URL.RawQuery, HasKey: r.Header.Get("X-API-KEY") == f.key})
		key, status = f.key, f.status[p]
		malformed = f.malformed[p] || f.malformed[""]
		body, location = f.body[p], f.redirect[p]
		delay = f.delay[""]
		if d, ok := f.delay[p]; ok {
			delay = d
		}
	})
	waitOrAbort(r, delay)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case location != "":
		http.Redirect(w, r, location, http.StatusFound)
	case r.Header.Get("X-API-KEY") != key:
		f.unauthorized(w)
	case r.Method != http.MethodGet:
		// The collector must never get here; the record above proves it did.
		fakeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "the fake serves GET only")
	case status != 0:
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "30")
		}
		fakeError(w, status, "forced_failure", "forced failure")
	case malformed:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>not json"))
	case body != "":
		_, _ = w.Write([]byte(body))
	default:
		f.serve(w, r)
	}
}

// serve answers from the fixtures: /v1/<x> is testdata/<set>/v1/<x>.json, the
// page asked with nextToken=T is <x>.T.json, and /v1/devices?hostIds[]=...
// keeps the groups of those hosts, as the real API does.
func (f *FakeSiteManager) serve(w http.ResponseWriter, r *http.Request) {
	if rest, _ := strings.CutPrefix(r.URL.Path, "/v1/"); strings.HasPrefix(rest, "isp-metrics/") {
		if typ := strings.TrimPrefix(rest, "isp-metrics/"); typ != "" && !strings.Contains(typ, "/") {
			f.serveMetrics(w, r, typ)
			return
		}
	}
	name, ok := strings.CutPrefix(r.URL.Path, "/v1/")
	if !ok || name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		fakeError(w, http.StatusNotFound, "not_found", "no such endpoint")
		return
	}
	if tok := r.URL.Query().Get("nextToken"); tok != "" {
		name += "." + tok
	}
	b, err := fixtures.ReadFile(path.Join("testdata", f.set, "v1", name+".json"))
	if err != nil {
		fakeError(w, http.StatusNotFound, "not_found", "no such endpoint")
		return
	}
	if ids := r.URL.Query()["hostIds[]"]; len(ids) > 0 && strings.HasPrefix(name, "devices") {
		b = filterHosts(b, ids)
	}
	_, _ = w.Write(b) //nolint:gosec // G705: JSON response of a test fake, Content-Type application/json
}

// serveMetrics answers /v1/isp-metrics/<type> from the recorded periods. Like
// the real API it refuses the 5m type with a duration other than 24h (the
// recorded error), and it keeps the periods with begin <= metricTime <= end,
// both bounds inclusive, when beginTimestamp and endTimestamp are given. With
// neither it serves every period (synthesised behaviour: the real API's
// default window was not recorded).
func (f *FakeSiteManager) serveMetrics(w http.ResponseWriter, r *http.Request, typ string) {
	q := r.URL.Query()
	if typ != "5m" && typ != "1h" {
		fakeError(w, http.StatusBadRequest, "parameter_invalid", "unsupported metric type")
		return
	}
	if d := q.Get("duration"); typ == "5m" && d != "" && d != "24h" {
		w.WriteHeader(http.StatusBadRequest)
		b, err := fixtures.ReadFile(path.Join("testdata", f.set, "errors", "400-isp-metrics-duration.json"))
		if err != nil {
			b, _ = json.Marshal(envelope{Code: "parameter_invalid", HTTPStatusCode: http.StatusBadRequest, Message: "5m metric only supports '24h' duration"})
		}
		_, _ = w.Write(b)
		return
	}
	var from, to time.Time
	for param, dst := range map[string]*time.Time{"beginTimestamp": &from, "endTimestamp": &to} {
		if v := q.Get(param); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				fakeError(w, http.StatusBadRequest, "parameter_invalid", param+" is not an RFC 3339 timestamp")
				return
			}
			*dst = t
		}
	}
	b, err := fixtures.ReadFile(path.Join("testdata", f.set, "v1", "isp-metrics", typ+".json"))
	if err != nil {
		fakeError(w, http.StatusNotFound, "not_found", "no such endpoint")
		return
	}
	var replay time.Time
	f.lock(func() { replay = f.replayEnd })
	_, _ = w.Write(filterPeriods(b, from, to, replay)) //nolint:gosec // G705: JSON response of a test fake, Content-Type application/json
}

// filterPeriods keeps the periods of each site in [from, to] (a zero bound is
// open). A non-zero replay first shifts every metricTime so that the last
// period of the document sits at replay truncated to 5 minutes.
func filterPeriods(doc []byte, from, to, replay time.Time) []byte {
	var env map[string]json.RawMessage
	if json.Unmarshal(doc, &env) != nil {
		return doc
	}
	var sites []map[string]json.RawMessage
	if json.Unmarshal(env["data"], &sites) != nil {
		return doc
	}
	periods := func(site map[string]json.RawMessage) (ps []map[string]json.RawMessage) {
		_ = json.Unmarshal(site["periods"], &ps)
		return ps
	}
	timeOf := func(p map[string]json.RawMessage) (t time.Time) {
		var s string
		if json.Unmarshal(p["metricTime"], &s) == nil {
			t, _ = time.Parse(time.RFC3339, s)
		}
		return t
	}
	var shift time.Duration
	if !replay.IsZero() {
		var last time.Time
		for _, site := range sites {
			for _, p := range periods(site) {
				if t := timeOf(p); t.After(last) {
					last = t
				}
			}
		}
		shift = replay.UTC().Truncate(5 * time.Minute).Sub(last)
	}
	for _, site := range sites {
		keep := []map[string]json.RawMessage{}
		for _, p := range periods(site) {
			t := timeOf(p).Add(shift)
			if (!from.IsZero() && t.Before(from)) || (!to.IsZero() && t.After(to)) {
				continue
			}
			if shift != 0 {
				p["metricTime"], _ = json.Marshal(t.UTC().Format(time.RFC3339))
			}
			keep = append(keep, p)
		}
		site["periods"], _ = json.Marshal(keep)
	}
	env["data"], _ = json.Marshal(sites)
	out, err := json.Marshal(env)
	if err != nil {
		return doc
	}
	return out
}

// filterHosts keeps the entries of data[] whose hostId is in ids.
func filterHosts(doc []byte, ids []string) []byte {
	var env map[string]json.RawMessage
	if json.Unmarshal(doc, &env) != nil {
		return doc
	}
	var groups []json.RawMessage
	if json.Unmarshal(env["data"], &groups) != nil {
		return doc
	}
	keep := []json.RawMessage{}
	for _, g := range groups {
		var h struct {
			HostID string `json:"hostId"`
		}
		if json.Unmarshal(g, &h) == nil {
			for _, id := range ids {
				if id == h.HostID {
					keep = append(keep, g)
					break
				}
			}
		}
	}
	env["data"], _ = json.Marshal(keep)
	out, err := json.Marshal(env)
	if err != nil {
		return doc
	}
	return out
}

// unauthorized answers with the recorded 401 body of the set when it has one.
func (f *FakeSiteManager) unauthorized(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized)
	if b, err := fixtures.ReadFile(path.Join("testdata", f.set, "errors", "401.json")); err == nil {
		_, _ = w.Write(b)
		return
	}
	b, _ := json.Marshal(envelope{Code: "unauthorized", HTTPStatusCode: http.StatusUnauthorized, Message: "unauthorized"})
	_, _ = w.Write(b)
}

func fakeError(w http.ResponseWriter, code int, id, msg string) {
	w.WriteHeader(code)
	b, _ := json.Marshal(envelope{Code: str(id), HTTPStatusCode: code, Message: str(msg)})
	_, _ = w.Write(b)
}
