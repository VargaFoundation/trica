package kubernetes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// writeKubeconfig writes a kubeconfig with two contexts and returns its path.
func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()
	cfg := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: alpha
  cluster: {server: %s}
- name: beta
  cluster: {server: https://198.51.100.9:6443}
users:
- name: alpha-user
  user: {token: alpha-token}
- name: beta-user
  user: {token: beta-token}
contexts:
- name: alpha
  context: {cluster: alpha, user: alpha-user}
- name: beta
  context: {cluster: beta, user: beta-user}
current-context: alpha
`, server)
	p := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func realTarget(t *testing.T, address string, opts map[string]any) collect.Target {
	t.Helper()
	tg := collect.Target{Kind: Kind, Name: "real", Address: address, Timeout: 5 * time.Second}
	if err := tg.Options.Encode(opts); err != nil {
		t.Fatal(err)
	}
	return tg
}

func TestRestConfig(t *testing.T) {
	kc := writeKubeconfig(t, "https://192.0.2.10:6443")
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("not read by this test"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*collect.Target)
		opts   map[string]any
		check  func(*testing.T, *rest.Config)
	}{
		{name: "current context", opts: map[string]any{"cluster": "c", "kubeconfig": kc}, check: func(t *testing.T, c *rest.Config) {
			if c.Host != "https://192.0.2.10:6443" || c.BearerToken != "alpha-token" {
				t.Errorf("host %q token %q", c.Host, c.BearerToken)
			}
		}},
		{name: "named context", opts: map[string]any{"cluster": "c", "kubeconfig": kc, "context": "beta"}, check: func(t *testing.T, c *rest.Config) {
			if c.Host != "https://198.51.100.9:6443" || c.BearerToken != "beta-token" {
				t.Errorf("host %q token %q", c.Host, c.BearerToken)
			}
		}},
		{name: "address overrides the server", opts: map[string]any{"cluster": "c", "kubeconfig": kc}, mutate: func(tg *collect.Target) {
			tg.Address = "https://203.0.113.5:6443"
		}, check: func(t *testing.T, c *rest.Config) {
			if c.Host != "https://203.0.113.5:6443" {
				t.Errorf("host %q", c.Host)
			}
		}},
		{name: "address that is not a URL keeps the server", opts: map[string]any{"cluster": "c", "kubeconfig": kc}, mutate: func(tg *collect.Target) {
			tg.Address = "lab"
		}, check: func(t *testing.T, c *rest.Config) {
			if c.Host != "https://192.0.2.10:6443" {
				t.Errorf("host %q", c.Host)
			}
		}},
		{name: "password is a bearer token", opts: map[string]any{"cluster": "c", "kubeconfig": kc}, mutate: func(tg *collect.Target) {
			tg.Password = collect.Secret("file:" + tokenFile)
		}, check: func(t *testing.T, c *rest.Config) {
			if c.BearerToken != "file-token" {
				t.Errorf("token %q", c.BearerToken)
			}
		}},
		{name: "ca file", opts: map[string]any{"cluster": "c", "kubeconfig": kc}, mutate: func(tg *collect.Target) {
			tg.TLS.CAFile = ca
		}, check: func(t *testing.T, c *rest.Config) {
			if c.CAFile != ca || c.Insecure {
				t.Errorf("ca %q insecure %v", c.CAFile, c.Insecure)
			}
		}},
		{name: "insecure drops the CA", opts: map[string]any{"cluster": "c", "kubeconfig": kc}, mutate: func(tg *collect.Target) {
			tg.TLS.CAFile, tg.TLS.Insecure = ca, true
		}, check: func(t *testing.T, c *rest.Config) {
			if !c.Insecure || c.CAFile != "" || len(c.CAData) != 0 {
				t.Errorf("insecure %v ca %q", c.Insecure, c.CAFile)
			}
		}},
		{name: "client settings", opts: map[string]any{"cluster": "c", "kubeconfig": kc}, mutate: func(tg *collect.Target) {
			tg.Timeout = 7 * time.Second
		}, check: func(t *testing.T, c *rest.Config) {
			if c.Timeout != 7*time.Second || c.UserAgent != userAgent || c.QPS != clientQPS || c.Burst != clientBurst {
				t.Errorf("timeout %v agent %q qps %v burst %v", c.Timeout, c.UserAgent, c.QPS, c.Burst)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := realTarget(t, "kubeconfig", tc.opts)
			if tc.mutate != nil {
				tc.mutate(&tg)
			}
			c, err := newCollector(tg, nil)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := c.restConfig()
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, cfg)
		})
	}

	for _, tc := range []struct {
		name    string
		address string
		mutate  func(*collect.Target)
		opts    map[string]any
		wantErr string
	}{
		{name: "missing kubeconfig", address: "https://192.0.2.10:6443", opts: map[string]any{"cluster": "c", "kubeconfig": "/nonexistent/kubeconfig"}, wantErr: "kubeconfig /nonexistent/kubeconfig"},
		{name: "unknown context", address: "https://192.0.2.10:6443", opts: map[string]any{"cluster": "c", "kubeconfig": kc, "context": "gamma"}, wantErr: "gamma"},
		{name: "token that cannot be read", address: "https://192.0.2.10:6443", opts: map[string]any{"cluster": "c", "kubeconfig": kc},
			mutate: func(tg *collect.Target) { tg.Password = "file:/nonexistent/token" }, wantErr: "/nonexistent/token"},
		{name: "in-cluster outside a cluster", address: "", opts: map[string]any{"cluster": "c"}, wantErr: "in-cluster configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", "")
			t.Setenv("KUBERNETES_SERVICE_PORT", "")
			tg := realTarget(t, tc.address, tc.opts)
			if tc.mutate != nil {
				tc.mutate(&tg)
			}
			c, err := newCollector(tg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.connectReal(); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %v, want one containing %q", err, tc.wantErr)
			}
			// The failure is the run's health, not a crash.
			b := collecttest.Run(t, c)[0]
			if b.Health == nil || b.Health.OK || !strings.HasPrefix(b.Health.Error, "connect: ") || b.Scope != sdk.ScopePartial {
				t.Errorf("scope %s health %+v", b.Scope, b.Health)
			}
		})
	}

	t.Run("in-cluster", func(t *testing.T) {
		if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			t.Skip("running inside a cluster: the ServiceAccount token exists")
		}
		t.Setenv("KUBERNETES_SERVICE_HOST", "192.0.2.1")
		t.Setenv("KUBERNETES_SERVICE_PORT", "443")
		c, err := newCollector(realTarget(t, inClusterAddress, map[string]any{"cluster": "c"}), nil)
		if err != nil {
			t.Fatal(err)
		}
		// The ServiceAccount token file is not there: that is the error, and
		// it proves the in-cluster path was taken.
		if _, err := c.restConfig(); err == nil || !strings.Contains(err.Error(), "in-cluster configuration") {
			t.Errorf("error %v", err)
		}
	})
}

// apiServer is a minimal HTTP API server: it serves the version, empty lists
// and two pages of nodes, and records every request.
type apiServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
	// override answers a path with a status and body instead of the default.
	override map[string]func(http.ResponseWriter, *http.Request)
}

var listKinds = map[string]struct{ gv, kind string }{
	"/api/v1/nodes":                                {"v1", "NodeList"},
	"/api/v1/namespaces":                           {"v1", "NamespaceList"},
	"/api/v1/pods":                                 {"v1", "PodList"},
	"/api/v1/services":                             {"v1", "ServiceList"},
	"/apis/apps/v1/deployments":                    {"apps/v1", "DeploymentList"},
	"/apis/apps/v1/statefulsets":                   {"apps/v1", "StatefulSetList"},
	"/apis/apps/v1/daemonsets":                     {"apps/v1", "DaemonSetList"},
	"/apis/apps/v1/replicasets":                    {"apps/v1", "ReplicaSetList"},
	"/apis/batch/v1/jobs":                          {"batch/v1", "JobList"},
	"/apis/batch/v1/cronjobs":                      {"batch/v1", "CronJobList"},
	"/apis/networking.k8s.io/v1/ingresses":         {"networking.k8s.io/v1", "IngressList"},
	"/apis/kubevirt.io/v1/virtualmachineinstances": {"kubevirt.io/v1", "VirtualMachineInstanceList"},
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()
	s := &apiServer{override: map[string]func(http.ResponseWriter, *http.Request){}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if h, ok := s.override[r.URL.Path]; ok {
			h(w, r)
			return
		}
		switch k, listed := listKinds[r.URL.Path]; {
		case r.URL.Path == "/version":
			fmt.Fprint(w, `{"major":"1","minor":"31","gitVersion":"v1.31.4+k3s1"}`)
		case r.URL.Path == "/api/v1/nodes":
			// Two pages: the collector must follow the continue token.
			if r.URL.Query().Get("continue") == "" {
				fmt.Fprint(w, `{"apiVersion":"v1","kind":"NodeList","metadata":{"continue":"page-2"},"items":[{"metadata":{"name":"node-a","uid":"uid-a"}}]}`)
			} else {
				fmt.Fprint(w, `{"apiVersion":"v1","kind":"NodeList","metadata":{},"items":[{"metadata":{"name":"node-b","uid":"uid-b"}}]}`)
			}
		case r.URL.Path == "/apis/kubevirt.io/v1/virtualmachineinstances":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","code":404}`)
		case listed:
			fmt.Fprintf(w, `{"apiVersion":%q,"kind":%q,"metadata":{},"items":[]}`, k.gv, k.kind)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","code":404}`)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *apiServer) requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.reqs...)
}

// TestRealClients runs the collector, built the way production builds it,
// against an HTTP server: this is where the client-go wiring is proved (token,
// TLS, paging, the dynamic client), which the fake clients cannot do.
func TestRealClients(t *testing.T) {
	srv := newAPIServer(t)
	kc := writeKubeconfig(t, srv.URL)
	tg := realTarget(t, "kubeconfig", map[string]any{"cluster": "real-cluster", "kubeconfig": kc})
	tg.TLS.Insecure = true // the test server's certificate is self-signed

	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	b := collecttest.Run(t, c)[0]

	if b.Scope != sdk.ScopeFull || !b.Health.OK || len(b.Health.Warnings) != 0 {
		t.Fatalf("scope %s health %+v", b.Scope, b.Health)
	}
	wantAttrs(t, object(t, b, "cluster"), map[string]any{"version": "v1.31.4+k3s1", "distribution": "k3s", "api_endpoint": srv.URL})
	if n := count(b, "k8s_node"); n != 2 {
		t.Errorf("%d nodes, want 2: the second page was not read", n)
	}

	seen := map[string]int{}
	for _, r := range srv.requests() {
		if r.Method != http.MethodGet {
			t.Errorf("%s %s: a collector only reads", r.Method, r.URL.Path)
		}
		if strings.Contains(r.URL.Path, "secrets") {
			t.Errorf("Secrets requested: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer alpha-token" {
			t.Errorf("%s: authorization %q", r.URL.Path, got)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, userAgent) {
			t.Errorf("%s: user agent %q", r.URL.Path, ua)
		}
		if r.URL.Path != "/version" && r.URL.Query().Get("limit") != "500" {
			t.Errorf("%s: limit %q, want 500", r.URL.Path, r.URL.Query().Get("limit"))
		}
		seen[r.URL.Path]++
	}
	if seen["/api/v1/nodes"] != 2 || seen["/version"] != 1 || seen["/apis/kubevirt.io/v1/virtualmachineinstances"] != 1 {
		t.Errorf("requests %v", seen)
	}
}

func TestRealClientsFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override map[string]func(http.ResponseWriter, *http.Request)
		scope    sdk.Scope
		errHas   string
		objects  bool
	}{
		{
			name: "token refused",
			override: map[string]func(http.ResponseWriter, *http.Request){"/version": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Unauthorized","code":401}`)
			}},
			scope: sdk.ScopePartial, errHas: "provide credentials",
		},
		{
			name: "version is not JSON",
			override: map[string]func(http.ResponseWriter, *http.Request){"/version": func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<html>502 Bad Gateway</html>`)
			}},
			scope: sdk.ScopePartial, errHas: "server version",
		},
		{
			name: "one list is not JSON",
			override: map[string]func(http.ResponseWriter, *http.Request){"/api/v1/services": func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `{"items": [`)
			}},
			scope: sdk.ScopePartial, errHas: "services:", objects: true,
		},
		{
			name: "one list is forbidden",
			override: map[string]func(http.ResponseWriter, *http.Request){"/api/v1/pods": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","message":"pods is forbidden","code":403}`)
			}},
			scope: sdk.ScopePartial, errHas: "pods is forbidden", objects: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAPIServer(t)
			srv.override = tc.override
			tg := realTarget(t, "kubeconfig", map[string]any{"cluster": "real-cluster", "kubeconfig": writeKubeconfig(t, srv.URL)})
			tg.TLS.Insecure = true
			c, err := collect.New(tg)
			if err != nil {
				t.Fatal(err)
			}
			b := collecttest.Run(t, c)[0]
			if b.Scope != tc.scope || b.Health.OK || !strings.Contains(b.Health.Error, tc.errHas) {
				t.Errorf("scope %s health %+v, want a failure containing %q", b.Scope, b.Health, tc.errHas)
			}
			if tc.objects != (len(b.Objects) > 0) {
				t.Errorf("%d objects, kept = %v", len(b.Objects), tc.objects)
			}
		})
	}
}

func TestRealClientsUnreachable(t *testing.T) {
	// Nothing listens on port 1: the run ends as a failed health, quickly.
	tg := realTarget(t, "https://127.0.0.1:1", map[string]any{"cluster": "c", "kubeconfig": writeKubeconfig(t, "https://127.0.0.1:1")})
	tg.Timeout = 3 * time.Second
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	b := collecttest.Run(t, c)[0]
	if b.Health.OK || b.Scope != sdk.ScopePartial || !strings.Contains(b.Health.Error, "server version") || len(b.Objects) != 0 {
		t.Errorf("scope %s health %+v", b.Scope, b.Health)
	}
}

// TestNewWithRealClients keeps the production wiring and the
// injected wiring on the same types.
func TestNewWithRealClients(t *testing.T) {
	cfg := &rest.Config{Host: "https://192.0.2.10:6443"}
	kube, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithClients(realTarget(t, "", map[string]any{"cluster": "c"}), kube, dyn); err != nil {
		t.Fatal(err)
	}
}
