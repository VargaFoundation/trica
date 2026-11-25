package catalog

import (
	"embed"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	"github.com/VargaFoundation/trica/internal/collect"
)

// testdata holds the synthesised catalogs the fake serves and the tests read
// (testdata/README.md says what each file is).
//
//go:embed testdata
var testdata embed.FS

// Fixture returns a file of testdata.
func Fixture(name string) []byte {
	b, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// FakeRequest is what the fake saw of one request.
type FakeRequest struct {
	Method        string
	Path          string
	RawQuery      string
	Authorization string
}

// FakeCatalogServer is a Git host's raw file endpoint over TLS: it serves the
// files of testdata under /raw/<name>, answers 401 without the expected token
// when one is set, and records every request. /moved/<name> redirects to
// /raw/<name>, which the collector must not follow.
type FakeCatalogServer struct {
	*httptest.Server
	// Token, when set, is the bearer token the server expects.
	Token string

	mu       sync.Mutex
	requests []FakeRequest
}

// NewFakeCatalogServer starts the fake. Close it when done.
func NewFakeCatalogServer() *FakeCatalogServer {
	f := &FakeCatalogServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/raw/{name}", f.serve)
	mux.HandleFunc("/moved/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		http.Redirect(w, r, "/raw/"+r.PathValue("name"), http.StatusFound) //nolint:gosec // a test fake: a same-host path the collector must not follow
	})
	f.Server = httptest.NewTLSServer(mux)
	return f
}

func (f *FakeCatalogServer) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, FakeRequest{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Authorization: r.Header.Get("Authorization")})
}

func (f *FakeCatalogServer) serve(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	if r.Method != http.MethodGet {
		http.Error(w, "read-only", http.StatusMethodNotAllowed)
		return
	}
	if f.Token != "" && r.Header.Get("Authorization") != "Bearer "+f.Token {
		// A server that echoes what it was sent: the collector must not repeat it.
		http.Error(w, "bad credentials: "+r.Header.Get("Authorization"), http.StatusUnauthorized)
		return
	}
	b, err := testdata.ReadFile("testdata/" + r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b) //nolint:gosec // a test fake serving its own embedded fixtures as text/plain
}

// Requests returns what the fake saw, in order.
func (f *FakeCatalogServer) Requests() []FakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeRequest(nil), f.requests...)
}

// Target is a catalog target reading path of the fake (/raw/<name>), trusting
// its certificate through a CA file written under dir. password is a
// reference, or empty.
func (f *FakeCatalogServer) Target(dir, name, path string, password collect.Secret) (collect.Target, error) {
	ca := filepath.Join(dir, "fake-ca.pem")
	if err := os.WriteFile(ca, certPEM(f.Certificate().Raw), 0o600); err != nil {
		return collect.Target{}, err
	}
	return collect.Target{Kind: Kind, Name: name, Address: f.URL + path, Password: password, TLS: collect.TLS{CAFile: ca}}, nil
}

func certPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
