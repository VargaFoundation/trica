package synology

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// The fixtures are of two kinds, and testdata/README.md says which is which:
// a RECORDED set (one real NAS, anonymised) and a SYNTHESISED one.
//
//go:embed testdata
var fixtures embed.FS

// Fixture sets.
const (
	// SetRecorded is one DS1821+ (DSM 7.4.1-90080 Update 1) recorded on
	// 2026-10-26 and anonymised.
	SetRecorded = "recorded-2026-10"
	// SetSynthesised is a DS923+-like NAS with a crashed pool, a failed drive,
	// a SATA SSD with a wear reading, two addresses, no UPS and no Container
	// Manager (error 102).
	SetSynthesised = "synthesised"
)

// The account and password a FakeDSM accepts unless SetCredentials changes
// them.
const (
	Account  = "trica-fake"
	Password = "fake-dsm-password-0000" //nolint:gosec // G101: the fixed password of an in-process fake, not a credential
)

// FakeRequest is one request received by the fake. The password and the
// session id are not kept, only whether a session id came and was valid.
type FakeRequest struct {
	Method   string // the HTTP method
	Path     string
	Query    string // the URL's query: always empty from a correct client
	API      string
	Version  string
	Call     string // the DSM method
	Params   map[string]string
	HasSID   bool
	SIDValid bool
}

// FakeDSM is an in-process DSM serving the fixtures of one set over TLS
// (httptest's certificate: targets use tls.insecure). It answers POST
// /webapi/entry.cgi only, checks the account and the session id and records
// every request, so tests can assert what a collector sent and that it logged
// out.
//
// The failure knobs may be called at any time, from any goroutine.
type FakeDSM struct {
	server *httptest.Server
	secret string // path of the file holding the password, for Target
	set    string

	mu        sync.Mutex
	account   string
	password  string
	loginCode int
	requests  []FakeRequest
	sessions  map[string]bool
	issued    []string
	next      int
	fail      map[string]int
	body      map[string]string
	delay     map[string]time.Duration
}

// NewFake starts a fake DSM serving a fixture set (SetRecorded,
// SetSynthesised). The caller must Close it.
func NewFake(set string) (*FakeDSM, error) {
	if _, err := fs.Stat(fixtures, "testdata/"+set); err != nil {
		return nil, fmt.Errorf("synology fake: no fixtures for set %q", set)
	}
	dir, err := os.MkdirTemp("", "synology-fake-")
	if err != nil {
		return nil, err
	}
	secret := filepath.Join(dir, "password")
	if err := os.WriteFile(secret, []byte(Password+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	f := &FakeDSM{
		set:      set,
		secret:   secret,
		account:  Account,
		password: Password,
		sessions: map[string]bool{},
		fail:     map[string]int{},
		body:     map[string]string{},
		delay:    map[string]time.Duration{},
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	return f, nil
}

// Close stops the server and removes the password file.
func (f *FakeDSM) Close() {
	f.server.Close()
	_ = os.RemoveAll(filepath.Dir(f.secret))
}

// Target returns a collect.Target aimed at the fake: its address, the account,
// the password in a file, TLS verification off.
func (f *FakeDSM) Target(name string) collect.Target {
	return collect.Target{
		Kind:     Kind,
		Name:     name,
		Address:  f.server.URL,
		Username: Account,
		Password: collect.Secret("file:" + f.secret),
		TLS:      collect.TLS{Insecure: true},
		Timeout:  10 * time.Second,
	}
}

// URL is the base URL of the fake, https://127.0.0.1:port.
func (f *FakeDSM) URL() string { return f.server.URL }

// SetCredentials changes the account and password the fake accepts: the
// collector's then fail with error 400.
func (f *FakeDSM) SetCredentials(account, password string) {
	f.lock(func() { f.account, f.password = account, password })
}

// RejectLogin makes every login answer with a DSM error code (403 for 2-step
// verification, 407 for a blocked address, ...); 0 restores the login.
func (f *FakeDSM) RejectLogin(code int) { f.lock(func() { f.loginCode = code }) }

// Fail makes a call answer with a DSM error code; method "" does it for every
// method of the API. Code 0 restores the call.
func (f *FakeDSM) Fail(api, method string, code int) {
	f.lock(func() {
		if code == 0 {
			delete(f.fail, callKey(api, method))
			return
		}
		f.fail[callKey(api, method)] = code
	})
}

// SetBody replaces the answer of a call with body, a raw JSON document.
func (f *FakeDSM) SetBody(api, method, body string) {
	f.lock(func() { f.body[callKey(api, method)] = body })
}

// Slow delays the answer of a call by d.
func (f *FakeDSM) Slow(api, method string, d time.Duration) {
	f.lock(func() { f.delay[callKey(api, method)] = d })
}

// Requests returns a copy of every request received so far.
func (f *FakeDSM) Requests() []FakeRequest {
	var out []FakeRequest
	f.lock(func() { out = append(out, f.requests...) })
	return out
}

// OpenSessions is how many sessions are logged in and not logged out.
func (f *FakeDSM) OpenSessions() int {
	n := 0
	f.lock(func() {
		for _, open := range f.sessions {
			if open {
				n++
			}
		}
	})
	return n
}

// IssuedSIDs returns every session id the fake handed out, for tests that
// look for them in logs and errors.
func (f *FakeDSM) IssuedSIDs() []string {
	var out []string
	f.lock(func() { out = append(out, f.issued...) })
	return out
}

// Calls returns the "API method" of every request, in order.
func (f *FakeDSM) Calls() []string {
	var out []string
	for _, r := range f.Requests() {
		out = append(out, r.API+" "+r.Call)
	}
	return out
}

// OnlyAllowed returns an error naming the first request that was not a POST of
// an allowed call to /webapi/entry.cgi with an empty query.
func (f *FakeDSM) OnlyAllowed() error {
	for _, r := range f.Requests() {
		v, _ := strconv.Atoi(r.Version)
		switch {
		case r.Method != http.MethodPost || r.Path != webAPIPath:
			return fmt.Errorf("request %s %s: not a POST of %s", r.Method, r.Path, webAPIPath)
		case r.Query != "":
			return fmt.Errorf("request %s %s carries a query string: %q", r.Method, r.Path, r.Query)
		case !isAllowed(endpoint{API: r.API, Version: v, Method: r.Call}):
			return fmt.Errorf("request %s v%s %s is not on the allow-list", r.API, r.Version, r.Call)
		}
	}
	return nil
}

func callKey(api, method string) string { return api + "\x00" + method }

func (f *FakeDSM) lock(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// waitOrAbort holds the answer for d. When the request ends first (the client
// gave up), it aborts the handler: a handler that returns without writing
// would send an empty "200 OK", which is not the transport failure a slow call
// stands for.
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

// answer is a DSM envelope.
func answer(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func failure(w http.ResponseWriter, code int) {
	answer(w, `{"success":false,"error":{"code":`+strconv.Itoa(code)+`}}`)
}

func (f *FakeDSM) handle(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	form := r.PostForm
	req := FakeRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		API: form.Get("api"), Version: form.Get("version"), Call: form.Get("method"),
		Params: map[string]string{}, HasSID: form.Get("_sid") != "",
	}
	for k := range form {
		switch k {
		case "api", "version", "method", "passwd", "_sid", "account":
		default:
			req.Params[k] = form.Get(k)
		}
	}
	var (
		account, password string
		loginCode         int
		failCode          int
		failed            bool
		body              string
		delay             time.Duration
	)
	f.lock(func() {
		req.SIDValid = f.sessions[form.Get("_sid")]
		f.requests = append(f.requests, req)
		account, password, loginCode = f.account, f.password, f.loginCode
		if c, ok := f.fail[callKey(req.API, req.Call)]; ok {
			failCode, failed = c, true
		} else if c, ok := f.fail[callKey(req.API, "")]; ok {
			failCode, failed = c, true
		}
		body = f.body[callKey(req.API, req.Call)]
		delay = f.delay[callKey(req.API, req.Call)]
	})
	switch {
	case r.URL.Path != webAPIPath:
		http.NotFound(w, r)
		return
	case r.Method != http.MethodPost:
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "the fake serves POST only", http.StatusMethodNotAllowed)
		return
	}
	if req.API == "SYNO.API.Auth" {
		f.auth(w, form, req, account, password, loginCode)
		return
	}
	if !req.SIDValid {
		failure(w, 119)
		return
	}
	waitOrAbort(r, delay)
	switch {
	case failed:
		failure(w, failCode)
	case body != "":
		answer(w, body)
	default:
		f.serve(w, req)
	}
}

func (f *FakeDSM) auth(w http.ResponseWriter, form map[string][]string, req FakeRequest, account, password string, loginCode int) {
	get := func(k string) string {
		if v := form[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	switch req.Call {
	case "login":
		switch {
		case loginCode != 0:
			failure(w, loginCode)
		case get("account") != account || get("passwd") != password:
			failure(w, 400)
		default:
			var sid string
			f.lock(func() {
				f.next++
				sid = fmt.Sprintf("FAKESID%04d", f.next)
				f.sessions[sid] = true
				f.issued = append(f.issued, sid)
			})
			answer(w, `{"success":true,"data":{"sid":"`+sid+`"}}`)
		}
	case "logout":
		f.lock(func() { f.sessions[get("_sid")] = false })
		answer(w, `{"success":true}`)
	default:
		failure(w, 103)
	}
}

// fixtureFile names the fixture that answers a call.
func fixtureFile(api, call, typ string) string {
	switch api + " " + call {
	case "SYNO.Core.System info":
		switch typ {
		case "":
			return "system_info.json"
		case "network":
			return "system_network.json"
		case "storage":
			return "system_storage.json"
		}
	case "SYNO.DSM.Info getinfo":
		return "dsm_info.json"
	case "SYNO.Storage.CGI.Storage load_info":
		return "storage_load_info.json"
	case "SYNO.Core.Network.Interface list":
		return "net_iface.json"
	case "SYNO.Core.Network get":
		return "network.json"
	case "SYNO.Core.Package list":
		return "packages.json"
	case "SYNO.Core.ExternalDevice.UPS get":
		return "ups.json"
	case "SYNO.Docker.Container list":
		return "containers.json"
	}
	return ""
}

// serve answers from the fixtures; a call no fixture answers is error 102,
// "the API does not exist", as on a DSM without that package.
func (f *FakeDSM) serve(w http.ResponseWriter, req FakeRequest) {
	name := fixtureFile(req.API, req.Call, req.Params["type"])
	if name == "" {
		failure(w, 102)
		return
	}
	b, err := fixtures.ReadFile(path.Join("testdata", f.set, name))
	if err != nil {
		failure(w, 102)
		return
	}
	answer(w, string(b))
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
