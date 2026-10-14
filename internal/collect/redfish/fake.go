package redfish

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// The fixtures are synthesised, see testdata/README.md.
//
//go:embed testdata/*.json
var fixtures embed.FS

// Profile names one of the modelled BMCs.
type Profile string

// The device versions the fake can serve.
const (
	// ILO4 is an HPE ProLiant DL360 Gen9 with iLO 4 (firmware 2.7x): storage
	// under the SmartStorage extension, firmware inventory under the system.
	ILO4 Profile = "ilo4-dl360g9"
	// ILO5 is an HPE ProLiant DL380 Gen10 with iLO 5 (2.x): standard Storage
	// and Memory, firmware inventory under UpdateService.
	ILO5 Profile = "ilo5-dl380g10"
	// IDRAC8 is a Dell PowerEdge R330 with iDRAC 8 (2.8x).
	IDRAC8 Profile = "idrac8-r330"
)

// Credentials the fake accepts.
const (
	FakeUsername = "trica-ro"
	FakePassword = "synthetic-not-a-secret"
)

// FakeNow is the instant the fixtures' relative timestamps are expanded from.
// A collector under test must use the same clock (Collector.now).
var FakeNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// Request is one request the fake received.
type Request struct {
	Method   string
	Path     string
	RawQuery string
}

// String renders the request as "METHOD /path?query".
func (r Request) String() string {
	s := r.Method + " " + r.Path
	if r.RawQuery != "" {
		s += "?" + r.RawQuery
	}
	return s
}

// FakeRedfish is a Redfish service over TLS that serves a fixture bundle. It
// checks the Basic credentials on everything but the service root (as real
// BMCs do), refuses any method other than GET and records every request, so
// that a test can prove the collector only reads.
type FakeRedfish struct {
	t   testing.TB
	srv *httptest.Server

	mu        sync.Mutex
	docs      map[string][]byte
	reqs      []Request
	failures  map[string]int
	malformed map[string]bool
	slow      map[string]time.Duration
}

// NewFakeRedfish starts a fake serving the profile. It is closed when the test
// ends.
func NewFakeRedfish(t testing.TB, p Profile) *FakeRedfish {
	t.Helper()
	raw, err := fixtures.ReadFile("testdata/" + string(p) + ".json")
	if err != nil {
		t.Fatalf("fixture %s: %v", p, err)
	}
	var bundle map[string]json.RawMessage
	if err := json.Unmarshal([]byte(expandTimes(string(raw), FakeNow)), &bundle); err != nil {
		t.Fatalf("fixture %s: %v", p, err)
	}
	f := &FakeRedfish{
		t:         t,
		docs:      make(map[string][]byte, len(bundle)),
		failures:  map[string]int{},
		malformed: map[string]bool{},
		slow:      map[string]time.Duration{},
	}
	for path, doc := range bundle {
		f.docs[cleanKey(path)] = doc
	}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the base URL of the fake.
func (f *FakeRedfish) URL() string { return f.srv.URL }

// Target is a target pointing at the fake with the accepted credentials.
func (f *FakeRedfish) Target(name string) collect.Target {
	return f.TargetWithPassword(name, FakePassword)
}

// TargetWithPassword is Target with another password, to exercise a refusal.
func (f *FakeRedfish) TargetWithPassword(name, password string) collect.Target {
	f.t.Helper()
	file := filepath.Join(f.t.TempDir(), "password")
	if err := os.WriteFile(file, []byte(password+"\n"), 0o600); err != nil {
		f.t.Fatalf("password file: %v", err)
	}
	return collect.Target{
		Kind:     Kind,
		Name:     name,
		Address:  f.srv.URL,
		Username: FakeUsername,
		Password: collect.Secret("file:" + file),
		TLS:      collect.TLS{Insecure: true},
		Timeout:  5 * time.Second,
	}
}

// FailPath makes every request for path answer with the HTTP status.
func (f *FakeRedfish) FailPath(path string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[cleanPath(path)] = status
}

// MalformPath makes path answer 200 with a body that is not JSON.
func (f *FakeRedfish) MalformPath(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.malformed[cleanPath(path)] = true
}

// SlowPath makes path wait before answering (or until the client gives up).
func (f *FakeRedfish) SlowPath(path string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slow[cleanPath(path)] = d
}

// Set replaces (or adds) the document served at path. A paging link is keyed
// with its query ("/redfish/v1/Systems/1/Processors?page=2").
func (f *FakeRedfish) Set(path string, doc map[string]any) {
	f.t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		f.t.Fatalf("fake document %s: %v", path, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs[cleanKey(path)] = b
}

// Edit changes the document served at path.
func (f *FakeRedfish) Edit(path string, fn func(doc map[string]any)) {
	f.t.Helper()
	f.mu.Lock()
	raw, ok := f.docs[cleanKey(path)]
	f.mu.Unlock()
	if !ok {
		f.t.Fatalf("fake has no document %s", path)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		f.t.Fatalf("fake document %s: %v", path, err)
	}
	fn(doc)
	f.Set(path, doc)
}

// Delete removes the document served at path, which then answers 404.
func (f *FakeRedfish) Delete(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.docs, cleanKey(path))
}

// cleanKey normalises the path part of a document key.
func cleanKey(k string) string {
	path, query, hasQuery := strings.Cut(k, "?")
	if hasQuery {
		return cleanPath(path) + "?" + query
	}
	return cleanPath(path)
}

// Requests returns every request received so far, in order.
func (f *FakeRedfish) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.reqs...)
}

// NonReads returns the requests that were not a GET.
func (f *FakeRedfish) NonReads() []Request {
	var out []Request
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

// Count returns how many requests had a path starting with prefix.
func (f *FakeRedfish) Count(prefix string) int {
	n := 0
	for _, r := range f.Requests() {
		if strings.HasPrefix(r.Path, prefix) {
			n++
		}
	}
	return n
}

func cleanPath(p string) string {
	if p = strings.TrimRight(p, "/"); p == "" {
		return "/"
	}
	return p
}

func (f *FakeRedfish) serve(w http.ResponseWriter, r *http.Request) {
	path := cleanPath(r.URL.Path)
	f.mu.Lock()
	f.reqs = append(f.reqs, Request{Method: r.Method, Path: path, RawQuery: r.URL.RawQuery})
	status, failing := f.failures[path]
	bad := f.malformed[path]
	delay := f.slow[path]
	f.mu.Unlock()

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		redfishError(w, http.StatusMethodNotAllowed, "Base.1.0.GeneralError", "Method not allowed.")
		return
	}
	if path != "/redfish/v1" {
		if u, p, ok := r.BasicAuth(); !ok || u != FakeUsername || p != FakePassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="Redfish"`)
			redfishError(w, http.StatusUnauthorized, "Base.1.0.NoValidSession", "Login failed: the credentials were not accepted.")
			return
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if failing {
		redfishError(w, status, "Base.1.0.GeneralError", fmt.Sprintf("Injected failure %d.", status))
		return
	}
	if bad {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Members": [ {"@odata.id": `))
		return
	}
	f.mu.Lock()
	doc, ok := f.docs[path+"?"+r.URL.RawQuery]
	if !ok {
		doc, ok = f.docs[path]
	}
	f.mu.Unlock()
	if !ok {
		redfishError(w, http.StatusNotFound, "Base.1.0.ResourceMissingAtURI", "The resource was not found.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(doc)
}

func redfishError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code":                  code,
		"message":               "See @Message.ExtendedInfo for more information.",
		"@Message.ExtendedInfo": []map[string]string{{"Message": msg}},
	}})
	_, _ = w.Write(body)
}

// timeTemplate matches {{now}}, {{now-45d}}, {{now-12h}} and the same with a
// zone ({{now-2d@-05:00}}).
var timeTemplate = regexp.MustCompile(`\{\{now(?:([+-])(\d+)([hd]))?(?:@([+-])(\d{2}):(\d{2}))?\}\}`)

// expandTimes replaces the time templates of a fixture with RFC 3339
// timestamps relative to now, so the fixtures never go stale.
func expandTimes(s string, now time.Time) string {
	return timeTemplate.ReplaceAllStringFunc(s, func(m string) string {
		g := timeTemplate.FindStringSubmatch(m)
		t := now
		if g[1] != "" {
			n, _ := strconv.Atoi(g[2])
			d := time.Duration(n) * time.Hour
			if g[3] == "d" {
				d *= 24
			}
			if g[1] == "-" {
				d = -d
			}
			t = t.Add(d)
		}
		if g[4] != "" {
			h, _ := strconv.Atoi(g[5])
			min, _ := strconv.Atoi(g[6])
			off := h*3600 + min*60
			if g[4] == "-" {
				off = -off
			}
			t = t.In(time.FixedZone("", off))
		}
		return t.Format(time.RFC3339)
	})
}
