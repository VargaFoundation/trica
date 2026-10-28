package kubemetrics

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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// The fixtures are recorded and anonymised; testdata/README.md says how.
//
//go:embed testdata
var fixtures embed.FS

// SetRecorded is the kube-state-metrics of a three-node k3s v1.31 cluster
// stored in VictoriaMetrics, recorded on 2026-10-28 and anonymised.
const SetRecorded = "recorded-2026-10"

// The account and password a FakeKubeMetrics accepts unless SetCredentials
// changes them, and the path prefix it serves the query API under.
const (
	Account  = "trica-fake"
	Password = "fake-metrics-password-0000" //nolint:gosec // G101: the fixed password of an in-process fake, not a credential
	Prefix   = "/inventory"
)

// FakeRequest is one request received by the fake. The password is not kept.
type FakeRequest struct {
	Method    string
	Path      string
	Params    map[string][]string // the URL's query parameters
	Metric    string              // the metric the PromQL names
	HasAuth   bool
	AuthValid bool
}

// FakeKubeMetrics is an in-process Prometheus-compatible query API serving the
// fixtures of one set over TLS (httptest's certificate: targets use
// tls.insecure). It answers GET <Prefix>/api/v1/query only, checks the account,
// applies the selector the way a server does (matchers on the series' labels)
// and records every request, so tests can assert what a collector sent.
//
// The failure knobs may be called at any time, from any goroutine.
type FakeKubeMetrics struct {
	server *httptest.Server
	secret string
	set    string

	mu       sync.Mutex
	account  string
	password string
	requests []FakeRequest
	status   map[string]int    // metric ("" = all) -> HTTP status
	body     map[string]string // metric -> raw answer
	partial  map[string]bool
	delay    map[string]time.Duration
}

// NewFake starts a fake serving a fixture set. The caller must Close it.
func NewFake(set string) (*FakeKubeMetrics, error) {
	if _, err := fs.Stat(fixtures, "testdata/"+set); err != nil {
		return nil, fmt.Errorf("kubemetrics fake: no fixtures for set %q", set)
	}
	dir, err := os.MkdirTemp("", "kubemetrics-fake-")
	if err != nil {
		return nil, err
	}
	secret := filepath.Join(dir, "password")
	if err := os.WriteFile(secret, []byte(Password+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	f := &FakeKubeMetrics{
		set: set, secret: secret, account: Account, password: Password,
		status: map[string]int{}, body: map[string]string{}, partial: map[string]bool{}, delay: map[string]time.Duration{},
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	return f, nil
}

// Close stops the server and removes the password file.
func (f *FakeKubeMetrics) Close() {
	f.server.Close()
	_ = os.RemoveAll(filepath.Dir(f.secret))
}

// Target returns a collect.Target aimed at the fake: its address with the
// path prefix, the account, the password in a file, TLS verification off. The
// cluster option is not set: the caller adds options.
func (f *FakeKubeMetrics) Target(name string) collect.Target {
	return collect.Target{
		Kind:     Kind,
		Name:     name,
		Address:  f.server.URL + Prefix,
		Username: Account,
		Password: collect.Secret("file:" + f.secret),
		TLS:      collect.TLS{Insecure: true},
		Timeout:  10 * time.Second,
	}
}

// URL is the base URL of the fake, https://127.0.0.1:port.
func (f *FakeKubeMetrics) URL() string { return f.server.URL }

// SetCredentials changes the account and password the fake accepts.
func (f *FakeKubeMetrics) SetCredentials(account, password string) {
	f.lock(func() { f.account, f.password = account, password })
}

// FailStatus makes the queries of a metric ("" for every metric) answer with
// an HTTP status; 0 restores them.
func (f *FakeKubeMetrics) FailStatus(metric string, status int) {
	f.lock(func() {
		if status == 0 {
			delete(f.status, metric)
			return
		}
		f.status[metric] = status
	})
}

// SetBody replaces the answer of a metric with body, a raw JSON document.
func (f *FakeKubeMetrics) SetBody(metric, body string) {
	f.lock(func() { f.body[metric] = body })
}

// Partial makes the answers of a metric carry "isPartial": true.
func (f *FakeKubeMetrics) Partial(metric string, on bool) {
	f.lock(func() { f.partial[metric] = on })
}

// Slow delays the answer of a metric by d.
func (f *FakeKubeMetrics) Slow(metric string, d time.Duration) {
	f.lock(func() { f.delay[metric] = d })
}

// Requests returns a copy of every request received so far.
func (f *FakeKubeMetrics) Requests() []FakeRequest {
	var out []FakeRequest
	f.lock(func() { out = append(out, f.requests...) })
	return out
}

// Metrics returns the metric of every request, in order.
func (f *FakeKubeMetrics) Metrics() []string {
	var out []string
	for _, r := range f.Requests() {
		out = append(out, r.Metric)
	}
	return out
}

// OnlyAllowed returns an error naming the first request that was not a GET of
// <Prefix>/api/v1/query with the one parameter "query", for an allowed metric.
func (f *FakeKubeMetrics) OnlyAllowed() error {
	for _, r := range f.Requests() {
		switch {
		case r.Method != http.MethodGet || r.Path != Prefix+"/"+queryPath:
			return fmt.Errorf("request %s %s: not a GET of %s/%s", r.Method, r.Path, Prefix, queryPath)
		case len(r.Params) != 1 || len(r.Params["query"]) != 1:
			return fmt.Errorf("request %s %s: parameters %v, want only query", r.Method, r.Path, r.Params)
		case !isAllowed(r.Metric):
			return fmt.Errorf("request for metric %q is not on the allow-list", r.Metric)
		}
	}
	return nil
}

func (f *FakeKubeMetrics) lock(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// metricOf returns the metric name a PromQL selector starts with.
func metricOf(q string) string {
	q = strings.TrimSpace(q)
	if i := strings.IndexAny(q, "{ ("); i >= 0 {
		return q[:i]
	}
	return q
}

// selectorOf returns the text between the braces of q, if any.
func selectorOf(q string) string {
	i := strings.Index(q, "{")
	j := strings.LastIndex(q, "}")
	if i < 0 || j < i {
		return ""
	}
	return q[i+1 : j]
}

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (f *FakeKubeMetrics) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")
	metric := metricOf(q)
	user, pass, hasAuth := r.BasicAuth()
	req := FakeRequest{Method: r.Method, Path: r.URL.Path, Params: r.URL.Query(), Metric: metric, HasAuth: hasAuth}
	var (
		status  int
		body    string
		partial bool
		delay   time.Duration
		ok      bool
	)
	f.lock(func() {
		req.AuthValid = hasAuth && user == f.account && pass == f.password
		ok = req.AuthValid
		f.requests = append(f.requests, req)
		status = f.status[metric]
		if status == 0 {
			status = f.status[""]
		}
		body, partial, delay = f.body[metric], f.partial[metric], f.delay[metric]
	})
	switch {
	case r.URL.Path != Prefix+"/"+queryPath:
		http.NotFound(w, r)
		return
	case r.Method != http.MethodGet:
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "the fake serves GET only", http.StatusMethodNotAllowed)
		return
	case !ok:
		w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	waitOrAbort(r, delay)
	if status != 0 {
		reply(w, status, `{"status":"error","errorType":"fake","error":"failure injected by the test"}`)
		return
	}
	if body != "" {
		reply(w, http.StatusOK, body)
		return
	}
	f.serve(w, q, metric, partial)
}

// waitOrAbort holds the answer for d; when the client gives up first it aborts
// the handler, so that the failure is a transport one, not an empty 200.
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

// serve answers from the fixture of the metric, keeping the series that match
// the query's selector.
func (f *FakeKubeMetrics) serve(w http.ResponseWriter, q, metric string, partial bool) {
	if !isAllowed(metric) {
		reply(w, http.StatusUnprocessableEntity, `{"status":"error","errorType":"bad_data","error":"the fake has no series for this query"}`)
		return
	}
	raw, err := fixtures.ReadFile(path.Join("testdata", f.set, metric+".json"))
	if err != nil {
		reply(w, http.StatusUnprocessableEntity, `{"status":"error","errorType":"bad_data","error":"no fixture"}`)
		return
	}
	_, ms, err := parseMatchers(selectorOf(q))
	if err != nil {
		reply(w, http.StatusUnprocessableEntity, `{"status":"error","errorType":"bad_data","error":"bad selector"}`)
		return
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		reply(w, http.StatusInternalServerError, `{"status":"error","errorType":"fake","error":"corrupt fixture"}`)
		return
	}
	var data struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(doc["data"], &data); err != nil {
		reply(w, http.StatusInternalServerError, `{"status":"error","errorType":"fake","error":"corrupt fixture"}`)
		return
	}
	kept := make([]json.RawMessage, 0, len(data.Result))
	for _, s := range data.Result {
		var one sample
		if err := json.Unmarshal(s, &one); err == nil && matches(ms, one.Metric) {
			kept = append(kept, s)
		}
	}
	data.Result = kept
	doc["data"], _ = json.Marshal(data)
	if partial {
		doc["isPartial"] = json.RawMessage("true")
	}
	out, _ := json.Marshal(doc)
	reply(w, http.StatusOK, string(out))
}

// Files lists the fixture files of a set, sorted.
func Files(set string) ([]string, error) {
	entries, err := fs.ReadDir(fixtures, "testdata/"+set)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
