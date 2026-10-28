package kubemetrics

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The wire: a whole run sends GETs of the query path, one parameter, and only
// the six metrics, in order.
func TestWireIsReadOnly(t *testing.T) {
	f := newFake(t)
	runOne(t, f, `selector: job="monitoring-kube-state-metrics"`)
	want := []string{metricNodeInfo, metricPodInfo, metricPodOwner, metricRSOwner, metricJobOwner, metricPodPhase}
	if got := f.Metrics(); !slices.Equal(got, want) {
		t.Errorf("metrics %v, want %v", got, want)
	}
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet || r.Path != "/inventory/api/v1/query" || !r.AuthValid {
			t.Errorf("request %+v", r)
		}
		// The selector is ANDed to the metric and is the whole of the PromQL.
		if q := r.Params["query"][0]; q != r.Metric+`{job="monitoring-kube-state-metrics"}` {
			t.Errorf("query %q", q)
		}
	}
	if err := f.OnlyAllowed(); err != nil {
		t.Error(err)
	}
}

// The guard: the client refuses, before sending, anything off the allow-list.
func TestClientRefusesOtherMetrics(t *testing.T) {
	f := newFake(t)
	tg := target(t, f, "")
	hc, err := tg.HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(f.URL() + Prefix)
	cl := newClient(hc, base, Account, Password, "")
	for _, m := range []string{
		"up", "kube_pod_container_info", "kube_pod_spec_volumes_persistentvolumeclaims_info", "kube_secret_info",
		"kube_configmap_info", "kube_pod_container_env", "container_cpu_usage_seconds_total",
		metricPodInfo + "[5m]", metricPodInfo + "{x=\"y\"}", "count(" + metricPodInfo + ")", "",
	} {
		if _, err := cl.query(context.Background(), m); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("metric %q: %v", m, err)
		}
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	for _, m := range allowedMetrics {
		if !strings.HasPrefix(m, "kube_") {
			t.Errorf("%q is not a kube-state-metrics family", m)
		}
	}
	// What kube-state-metrics holds that must never be read: environment
	// values, secrets, config maps, container arguments.
	for _, m := range allowedMetrics {
		for _, bad := range []string{"env", "secret", "configmap", "container", "label", "annotation"} {
			if strings.Contains(m, bad) {
				t.Errorf("allowed metric %q contains %q", m, bad)
			}
		}
	}
}

// The fake refuses what is not a GET of the query path.
func TestFakeServesGetOnly(t *testing.T) {
	f := newFake(t)
	tg := target(t, f, "")
	hc, _ := tg.HTTPClient()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, Prefix + "/api/v1/query"},
		{http.MethodGet, Prefix + "/api/v1/series"},
		{http.MethodGet, "/api/v1/query"},
	} {
		req, _ := http.NewRequest(tc.method, f.URL()+tc.path+"?query=kube_pod_info", nil)
		req.SetBasicAuth(Account, Password)
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s %s answered 200", tc.method, tc.path)
		}
	}
	if err := f.OnlyAllowed(); err == nil {
		t.Error("OnlyAllowed accepted a POST and a call to another path")
	}
}

// Source scan: nothing builds a request that is not a GET, and the only path
// named in the package is the query path.
func TestOnlyGETToTheQueryPathIsBuilt(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, p := range readOnlyProblems(f, !strings.HasPrefix(name, "fake")) {
			t.Errorf("%s: %s", fset.Position(p.pos), p.msg)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d source files checked: the scan is broken", checked)
	}
}

type problem struct {
	pos token.Pos
	msg string
}

// readOnlyProblems lists what in f could send a request that is not a GET, or
// name an API path other than the query path; paths false skips the path
// check (the fake names the paths it serves).
func readOnlyProblems(f *ast.File, paths bool) []problem {
	var out []problem
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "NewRequest", "NewRequestWithContext":
				i := 0
				if sel.Sel.Name == "NewRequestWithContext" {
					i = 1
				}
				if len(n.Args) <= i || !isHTTP(n.Args[i], "MethodGet") {
					out = append(out, problem{n.Pos(), "a request whose method is not http.MethodGet"})
				}
			case "Get", "Head", "Put", "Patch", "Delete", "Post", "PostForm", "Method":
				if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "http" || id.Name == "hc") {
					out = append(out, problem{n.Pos(), "call to ." + sel.Sel.Name + ": requests are built by client.query only"})
				}
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && id.Name == "http" && strings.HasPrefix(n.Sel.Name, "Method") &&
				n.Sel.Name != "MethodGet" && n.Sel.Name != "MethodNotAllowed" {
				out = append(out, problem{n.Pos(), "http." + n.Sel.Name + " is not the query API's method"})
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING && paths {
				s, _ := strconv.Unquote(n.Value)
				if strings.Contains(s, "api/") && !strings.Contains(s, " ") && s != "api/v1/query" {
					out = append(out, problem{n.Pos(), "string " + n.Value + " is not the query path"})
				}
			}
		}
		return true
	})
	return out
}

func isHTTP(e ast.Expr, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "http" && sel.Sel.Name == name
}

func TestReadOnlyScanCatchesWrites(t *testing.T) {
	for name, src := range map[string]string{
		"POST request":     `package p; import "net/http"; func f() { _, _ = http.NewRequest("POST", "u", nil) }`,
		"method variable":  `package p; import "net/http"; func f(m string) { _, _ = http.NewRequestWithContext(nil, m, "u", nil) }`,
		"http.Get":         `package p; import "net/http"; func f() { _, _ = http.Get("u") }`,
		"http.Post":        `package p; import "net/http"; func f() { _, _ = http.Post("u", "t", nil) }`,
		"MethodPost":       `package p; import "net/http"; var m = http.MethodPost`,
		"MethodDelete":     `package p; import "net/http"; var m = http.MethodDelete`,
		"series endpoint":  `package p; var u = "api/v1/series"`,
		"range endpoint":   `package p; var u = "api/v1/query_range"`,
		"admin endpoint":   `package p; var u = "api/v1/admin/tsdb/delete_series"`,
		"another endpoint": `package p; var u = "prefix/api/v1/labels"`,
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(readOnlyProblems(f, true)) == 0 {
			t.Errorf("%s: not reported", name)
		}
	}
	good, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; import ("context"; "net/http"); func f(ctx context.Context) { _, _ = http.NewRequestWithContext(ctx, http.MethodGet, "u", nil); _ = "api/v1/query" }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(good, true); len(p) != 0 {
		t.Errorf("a GET of the query path is reported: %v", p)
	}
	fake, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; import "net/http"; var u = "api/v1/anything"; var m = http.MethodPut`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(fake, false); len(p) != 1 {
		t.Errorf("a fake: %v, want only the write reported", p)
	}
}
