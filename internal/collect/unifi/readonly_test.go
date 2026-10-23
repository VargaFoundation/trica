package unifi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The collector must never write to the account. Behaviour is checked at the
// wire (the fake answers 405 to a non-GET and records it); this checks the
// source: nothing in the package can build a request that is not a GET, and
// no path outside the exact allow-list below can be named: not the cloud's
// console proxy (/v1/connector/..., which also forwards writes), not the
// early-access API (/ea/...), not a console's legacy API
// (/proxy/network/api/...), not the clients of a site.
func TestOnlyGETIsBuilt(t *testing.T) {
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
		// The fakes name the paths they serve; what they must not do is write.
		for _, problem := range readOnlyProblems(f, !strings.HasPrefix(name, "fake")) {
			t.Errorf("%s: %s", fset.Position(problem.pos), problem.msg)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d source files checked: the scan is broken", checked)
	}
}

// allowedPaths are the only strings starting with a slash and a letter that
// the package's sources (the fakes apart) may hold: the Site Manager
// endpoints (ADR 0057, 0059), and the Network Integration API prefix and its
// four endpoint shapes, whose %s are ids checked against uuidPattern
// (ADR 0060). Any other one, a clients endpoint or a legacy path, is a new
// request: it needs an ADR and an entry here.
var allowedPaths = map[string]bool{
	"/v1/hosts": true, "/v1/sites": true, "/v1/devices": true, "/v1/isp-metrics/5m": true,
	"/proxy/network/integration": true,
	"/v1/info":                   true, "/v1/sites/%s/devices": true, "/v1/sites/%s/devices/%s": true,
}

// refSuffixes are the object-reference fragments of the mappings (the ref of
// a port is its device's ref plus one): they are not paths.
var refSuffixes = map[string]bool{"/if:": true, "/wan:": true}

var pathLike = regexp.MustCompile(`^/[A-Za-z]`)

type problem struct {
	pos token.Pos
	msg string
}

// readOnlyProblems lists what in f could write or name a path that is not
// allowed; paths false skips the path check (the fakes).
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
				if len(n.Args) <= i || !isMethodGet(n.Args[i]) {
					out = append(out, problem{n.Pos(), "a request whose method is not http.MethodGet"})
				}
			case "Post", "PostForm", "Put", "Patch", "Delete", "Head", "Method":
				out = append(out, problem{n.Pos(), "call to ." + sel.Sel.Name + ": only GET may be sent"})
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && id.Name == "http" && strings.HasPrefix(n.Sel.Name, "Method") && n.Sel.Name != "MethodGet" && n.Sel.Name != "MethodNotAllowed" {
				out = append(out, problem{n.Pos(), "http." + n.Sel.Name + " is not a read"})
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				s, _ := strconv.Unquote(n.Value)
				if !paths || allowedPaths[s] || refSuffixes[s] {
					break
				}
				for _, bad := range []string{"/connector/", "/ea/", "/proxy/"} {
					if strings.Contains(s, bad) && !strings.Contains(s, " ") { // a sentence of documentation may name them
						out = append(out, problem{n.Pos(), "string " + n.Value + " reaches " + bad})
					}
				}
				// Exactly the allowed paths: not the 1h metrics, not a
				// query path, not the clients, not a prefix to append to.
				if pathLike.MatchString(s) {
					out = append(out, problem{n.Pos(), "string " + n.Value + " is not one of the allowed paths " + strings.Join(sortedPaths(), ", ")})
				}
			}
		}
		return true
	})
	return out
}

func sortedPaths() []string {
	var out []string
	for p := range allowedPaths {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func isMethodGet(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "http" && sel.Sel.Name == "MethodGet"
}

// The scan itself must catch what it claims to: a source that builds a POST,
// calls http.Post or names a connector path is reported.
func TestReadOnlyScanCatchesWrites(t *testing.T) {
	for name, src := range map[string]string{
		"POST request":          `package p; import "net/http"; func f() { _, _ = http.NewRequest("POST", "u", nil) }`,
		"method variable":       `package p; import "net/http"; func f(m string) { _, _ = http.NewRequestWithContext(nil, m, "u", nil) }`,
		"http.Post":             `package p; import "net/http"; func f() { _, _ = http.Post("u", "t", nil) }`,
		"client.Put":            `package p; func f(c interface{ Put() }) { c.Put() }`,
		"MethodPost":            `package p; import "net/http"; var m = http.MethodPost`,
		"connector":             `package p; var u = "/v1/connector/consoles/x/proxy"`,
		"early access":          `package p; var u = "/ea/isp-metrics"`,
		"metrics query":         `package p; var u = "/v1/isp-metrics/5m/query"`,
		"1h metrics":            `package p; var u = "/v1/isp-metrics/1h"`,
		"another path":          `package p; var u = "/v1/anything"`,
		"prefix to append to":   `package p; var u = "/v1/sites/"`,
		"clients":               `package p; var u = "/v1/sites/%s/clients"`,
		"site devices prefix":   `package p; func f(id string) string { return "/v1/sites/" + id }`,
		"device actions":        `package p; var u = "/v1/sites/%s/devices/%s/actions"`,
		"pending devices":       `package p; var u = "/v1/pending-devices"`,
		"legacy network API":    `package p; var u = "/proxy/network/api/s/default/stat/device"`,
		"cloud connector proxy": `package p; var u = "/v1/connector/consoles/x/proxy/network/integration"`,
		"integration only":      `package p; var u = "/proxy/network/integration/v1/sites"`,
		"bare integration":      `package p; var u = "/integration/v1/sites"`,
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(readOnlyProblems(f, true)) == 0 {
			t.Errorf("%s: not reported", name)
		}
	}
	good, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; import ("context"; "net/http"); func f(ctx context.Context) { _, _ = http.NewRequestWithContext(ctx, http.MethodGet, "u", nil) }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(good, true); len(p) != 0 {
		t.Errorf("a GET is reported: %v", p)
	}
	endpoints, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; var u = []string{"/v1/hosts", "/v1/sites", "/v1/devices", "/v1/isp-metrics/5m", "/proxy/network/integration", "/v1/info", "/v1/sites/%s/devices", "/v1/sites/%s/devices/%s", "/if:", "/wan:", "/"}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(endpoints, true); len(p) != 0 {
		t.Errorf("the collector's own endpoints are reported: %v", p)
	}
	// A fake may name any path, but never write.
	fake, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; import "net/http"; var u = "/v1/anything"; var m = http.MethodPost`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(fake, false); len(p) != 1 {
		t.Errorf("a fake: %v, want only the write reported", p)
	}
	if _, err := os.Stat("client.go"); err != nil {
		t.Fatal(err)
	}
}
