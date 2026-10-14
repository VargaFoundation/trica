package arista

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// The fake's responses are embedded; testdata/README.md says where they come
// from (synthesised from Arista's documentation, not recorded) and what must be
// checked on a real switch.
//
//go:embed testdata
var fixtures embed.FS

// Versions the fake can play. Each is a directory under testdata/ with one JSON
// file per command.
const (
	// EOS414 models a DCS-7050SX-64 on EOS 4.14.2F.
	EOS414 = "4.14.2F"
	// EOS420 models a DCS-7280SE-64 on EOS 4.20.7M.
	EOS420 = "4.20.7M"
)

// Credentials the fake accepts. They are fake, and meaningless anywhere else.
const (
	FakeUsername = "trica"
	FakePassword = "fake-password-not-a-secret"
	// PasswordEnv is the variable a test sets to FakePassword so that
	// FakeArista.Target can reference it ("env:" + PasswordEnv).
	PasswordEnv = "TRICA_FAKE_ARISTA_PASSWORD" //nolint:gosec // the name of a variable, not a credential
)

// Request is what the fake recorded of one HTTP request it received.
type Request struct {
	Method   string
	Path     string
	User     string
	AuthOK   bool
	Commands []string
	Format   string
	Version  int
}

// FakeArista is an eAPI endpoint serving the fixtures of one EOS version. It
// checks the credentials, records every request, and can be made to fail in
// the ways a real switch does: credentials refused, one command refused or
// answered with something unreadable, a malformed body, a slow answer.
//
// Like a switch holding a network-operator account, it refuses every command
// that is not a "show" command; the refusal is recorded, so a test can assert
// that the collector never even tried (NonReadOperations).
type FakeArista struct {
	version string
	srv     *httptest.Server
	tls     bool

	mu            sync.Mutex
	requests      []Request
	failing       map[string]bool
	malformedCmd  map[string]bool
	malformedBody bool
	shortResult   bool
	httpStatus    int
	rejectAuth    bool
	delay         time.Duration
}

// NewFakeArista starts a plain-HTTP fake playing version (EOS414 or EOS420).
// Old EOS releases cannot negotiate modern TLS, so the lab target is
// "http://...".
func NewFakeArista(version string) *FakeArista { return newFake(version, false) }

// NewFakeAristaTLS is NewFakeArista over HTTPS with a self-signed certificate.
func NewFakeAristaTLS(version string) *FakeArista { return newFake(version, true) }

func newFake(version string, withTLS bool) *FakeArista {
	f := &FakeArista{
		version:      version,
		tls:          withTLS,
		failing:      map[string]bool{},
		malformedCmd: map[string]bool{},
	}
	if withTLS {
		f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	} else {
		f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	}
	return f
}

// URL is the base URL of the fake ("http://127.0.0.1:port").
func (f *FakeArista) URL() string { return f.srv.URL }

// Close stops the fake.
func (f *FakeArista) Close() { f.srv.Close() }

// Target returns a target configuration pointing at the fake, with the fake
// credentials referenced through PasswordEnv.
func (f *FakeArista) Target(name string) collect.Target {
	t := collect.Target{
		Kind:     Kind,
		Name:     name,
		Address:  f.srv.URL,
		Username: FakeUsername,
		Password: collect.Secret("env:" + PasswordEnv),
		Timeout:  10 * time.Second,
	}
	t.TLS.Insecure = f.tls
	return t
}

// FailCommand makes eAPI refuse the given commands, as a switch does for a
// command its EOS does not know or its role does not allow.
func (f *FakeArista) FailCommand(cmds ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range cmds {
		f.failing[c] = true
	}
}

// MalformCommand makes the given commands answer with a result that is valid
// JSON but not the object the command returns.
func (f *FakeArista) MalformCommand(cmds ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range cmds {
		f.malformedCmd[c] = true
	}
}

// MalformBody makes every answer an unparsable body.
func (f *FakeArista) MalformBody() { f.set(func() { f.malformedBody = true }) }

// ShortResult makes every answer carry one result fewer than commands asked.
func (f *FakeArista) ShortResult() { f.set(func() { f.shortResult = true }) }

// HTTPStatus makes every answer an HTTP error of the given status.
func (f *FakeArista) HTTPStatus(code int) { f.set(func() { f.httpStatus = code }) }

// RejectCredentials makes the fake refuse every login, whatever it receives.
func (f *FakeArista) RejectCredentials() { f.set(func() { f.rejectAuth = true }) }

// Delay makes every answer wait d (or until the client gives up).
func (f *FakeArista) Delay(d time.Duration) { f.set(func() { f.delay = d }) }

func (f *FakeArista) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// Requests returns what the fake has recorded, oldest first.
func (f *FakeArista) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Commands returns every command received, in order, one entry per command
// (a batched request contributes several).
func (f *FakeArista) Commands() []string {
	var out []string
	for _, r := range f.Requests() {
		out = append(out, r.Commands...)
	}
	return out
}

// NonReadOperations lists everything the fake received that is not a read: a
// command that does not start with "show ", or a request other than
// "POST /command-api" (the only way eAPI takes a command, reads included). An
// empty result is the evidence that the collector only read.
func (f *FakeArista) NonReadOperations() []string {
	var out []string
	for _, r := range f.Requests() {
		if r.Method != http.MethodPost || r.Path != "/command-api" {
			out = append(out, r.Method+" "+r.Path)
		}
		for _, c := range r.Commands {
			if !isShow(c) {
				out = append(out, "command "+c)
			}
		}
	}
	return out
}

type fakeCall struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	ID      string `json:"id"`
	Params  struct {
		Version int      `json:"version"`
		Cmds    []string `json:"cmds"`
		Format  string   `json:"format"`
	} `json:"params"`
}

func (f *FakeArista) serve(w http.ResponseWriter, r *http.Request) {
	user, pass, hasAuth := r.BasicAuth()
	f.mu.Lock()
	authOK := hasAuth && !f.rejectAuth && user == FakeUsername && pass == FakePassword
	rec := Request{Method: r.Method, Path: r.URL.Path, User: user, AuthOK: authOK}
	failing, malformed := maps.Clone(f.failing), maps.Clone(f.malformedCmd)
	malformedBody, short, status, delay := f.malformedBody, f.shortResult, f.httpStatus, f.delay
	f.mu.Unlock()

	record := func() {
		f.mu.Lock()
		f.requests = append(f.requests, rec)
		f.mu.Unlock()
	}

	if r.Method != http.MethodPost || r.URL.Path != "/command-api" {
		record()
		http.NotFound(w, r)
		return
	}
	if !authOK {
		record()
		w.Header().Set("WWW-Authenticate", `Basic realm="COMMAND_API"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var call fakeCall
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&call); err != nil {
		record()
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	rec.Commands, rec.Format, rec.Version = call.Params.Cmds, call.Params.Format, call.Params.Version
	record()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		http.Error(w, "fake failure", status)
		return
	}
	if malformedBody {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": [`))
		return
	}

	results := make([]json.RawMessage, 0, len(call.Params.Cmds))
	for i, cmd := range call.Params.Cmds {
		var (
			res    json.RawMessage
			reason string
		)
		switch {
		case !isShow(cmd):
			reason = "Unauthorized to execute command"
		case failing[cmd]:
			reason = "Invalid input"
		case malformed[cmd]:
			res = json.RawMessage(`"not what this command returns"`)
		default:
			b, err := fixtures.ReadFile(fixturePath(f.version, cmd))
			if err != nil {
				reason = "Invalid input"
			} else {
				res = b
			}
		}
		if reason != "" {
			f.reply(w, call.ID, nil, map[string]any{
				"code":    1002,
				"message": fmt.Sprintf("CLI command %d of %d '%s' failed: %s", i+1, len(call.Params.Cmds), cmd, strings.ToLower(reason)),
				"data":    append(append([]any{}, rawAny(results)...), map[string]any{"errors": []string{reason + " (at token 1: '" + cmd + "')"}}),
			})
			return
		}
		results = append(results, res)
	}
	if short && len(results) > 0 {
		results = results[:len(results)-1]
	}
	f.reply(w, call.ID, results, nil)
}

func rawAny(in []json.RawMessage) []any {
	out := make([]any, len(in))
	for i, r := range in {
		out[i] = r
	}
	return out
}

func (f *FakeArista) reply(w http.ResponseWriter, id string, results []json.RawMessage, rpcErr map[string]any) {
	body := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		body["error"] = rpcErr
	} else {
		body["result"] = results
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// fixturePath names the fixture of a command: "show lldp local-info" is
// testdata/eos-4.14.2F/show_lldp_local_info.json.
func fixturePath(version, cmd string) string {
	name := strings.NewReplacer(" ", "_", "-", "_").Replace(cmd)
	return "testdata/eos-" + version + "/" + name + ".json"
}
