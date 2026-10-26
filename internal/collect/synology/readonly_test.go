package synology

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The collector must never write to the NAS. DSM's Web API sends every call,
// reads and writes alike, as a POST of an api/method pair, so "POST only" does
// not make it read-only: the allow-list of pairs does. Three layers are
// checked: the wire (the fake records everything it receives), the guard in
// the client, and the source.

func TestWireIsAllowListedPostsOnly(t *testing.T) {
	for _, set := range []string{SetRecorded, SetSynthesised} {
		f := newFake(t, set)
		runOne(t, f) // runTarget calls OnlyAllowed
		reqs := f.Requests()
		if len(reqs) < 8 {
			t.Fatalf("%s: only %d requests: the check would be vacuous", set, len(reqs))
		}
		for _, r := range reqs {
			if r.Query != "" {
				t.Errorf("%s: %s %s has a query string", set, r.API, r.Call)
			}
			if r.Call != "login" && r.Call != "logout" && !readVerbs[r.Call] {
				t.Errorf("%s: %s %s is not a read verb", set, r.API, r.Call)
			}
		}
	}
}

// The fake's own check catches what it claims to catch.
func TestOnlyAllowedCatchesWrites(t *testing.T) {
	f := newFake(t, SetRecorded)
	hc := f.server.Client()
	post := func(path, form string) {
		t.Helper()
		resp, err := hc.Post(f.URL()+path, "application/x-www-form-urlencoded", strings.NewReader(form))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if err := f.OnlyAllowed(); err != nil {
		t.Fatalf("empty: %v", err)
	}
	for name, do := range map[string]func(){
		"a reboot":     func() { post(webAPIPath, "api=SYNO.Core.System&version=1&method=reboot") },
		"another path": func() { post("/webapi/auth.cgi", "api=SYNO.API.Auth&version=6&method=login") },
		"a GET": func() {
			resp, err := hc.Get(f.URL() + webAPIPath)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		},
		"a query":         func() { post(webAPIPath+"?_sid=x", "api=SYNO.Core.System&version=1&method=info") },
		"a wrong version": func() { post(webAPIPath, "api=SYNO.Core.System&version=9&method=info") },
	} {
		g := newFake(t, SetRecorded)
		hc = g.server.Client()
		f = g
		do()
		if f.OnlyAllowed() == nil {
			t.Errorf("%s: not reported", name)
		}
	}
}

// The guard refuses every call that is not on the allow-list, before it is
// sent.
func TestClientRefusesWrites(t *testing.T) {
	f := newFake(t, SetRecorded)
	hc, err := f.Target(nas).HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(f.URL())
	cl := newClient(hc, base, Account, Password)
	if err := cl.login(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.logout(context.Background()) }()
	before := len(f.Requests())
	for _, e := range []endpoint{
		{"SYNO.Core.System", 1, "reboot"},
		{"SYNO.Core.System", 1, "shutdown"},
		{"SYNO.Core.User", 1, "create"},
		{"SYNO.Core.Package", 2, "uninstall"},
		{"SYNO.Docker.Container", 1, "delete"},
		{"SYNO.Core.System", 1, "set"},
		{"SYNO.Core.System", 2, "info"}, // a version nobody allowed
		{"SYNO.Core.User", 1, "list"},   // a read of something not listed
		{"SYNO.API.Auth", 6, "set"},
		{"", 0, ""},
	} {
		_, err := cl.call(context.Background(), e, nil)
		if !errors.Is(err, errRefused) {
			t.Errorf("%v: err %v, want errRefused", e, err)
		}
	}
	if got := len(f.Requests()); got != before {
		t.Errorf("%d requests sent by refused calls", got-before)
	}
	// An allowed one still goes.
	if _, err := cl.call(context.Background(), epSystem, nil); err != nil {
		t.Errorf("allowed call: %v", err)
	}
}

func TestAllowListShape(t *testing.T) {
	seen := map[endpoint]bool{}
	for _, e := range allowed {
		if seen[e] {
			t.Errorf("%v listed twice", e)
		}
		seen[e] = true
		if e.API == "" || e.Version <= 0 {
			t.Errorf("%v is incomplete", e)
		}
		switch e {
		case epLogin, epLogout:
			if e.API != "SYNO.API.Auth" {
				t.Errorf("%v: login and logout are SYNO.API.Auth", e)
			}
		default:
			if !readVerbs[e.Method] {
				t.Errorf("%v: method %q is not a read verb", e, e.Method)
			}
		}
	}
	if len(allowed) != 10 {
		t.Errorf("%d endpoints: a new one needs a mapping, a fixture and an ADR note", len(allowed))
	}
	for verb := range readVerbs {
		for _, bad := range []string{"set", "create", "delete", "reboot", "start", "stop", "install", "write", "update", "reset"} {
			if verb == bad {
				t.Errorf("%q is a write verb", verb)
			}
		}
	}
	if isAllowed(endpoint{"SYNO.Core.System", 1, "reboot"}) {
		t.Error("reboot is allowed")
	}
}

// Source scan: nothing builds a request that is not a POST, and the only path
// named is /webapi/entry.cgi.
func TestOnlyPOSTToEntryCGIIsBuilt(t *testing.T) {
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

// readOnlyProblems lists what in f could send a request that is not a POST,
// or name a path other than the Web API's entry point; paths false skips the
// path check (the fake names the paths it serves).
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
				if len(n.Args) <= i || !isHTTP(n.Args[i], "MethodPost") {
					out = append(out, problem{n.Pos(), "a request whose method is not http.MethodPost"})
				}
			case "Get", "Head", "Put", "Patch", "Delete", "Post", "PostForm", "Method":
				// Only the client's own do() builds requests; a package-level
				// http.Get and friends would bypass the allow-list.
				if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "http" || id.Name == "hc" || id.Name == "c.hc") {
					out = append(out, problem{n.Pos(), "call to ." + sel.Sel.Name + ": requests are built by client.call only"})
				}
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && id.Name == "http" && strings.HasPrefix(n.Sel.Name, "Method") &&
				n.Sel.Name != "MethodPost" && n.Sel.Name != "MethodGet" && n.Sel.Name != "MethodNotAllowed" {
				out = append(out, problem{n.Pos(), "http." + n.Sel.Name + " is not the Web API's method"})
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING && paths {
				s, _ := strconv.Unquote(n.Value)
				if strings.HasPrefix(s, "/") && len(s) > 1 && s[1] >= 'A' && (s[1] <= 'Z' || (s[1] >= 'a' && s[1] <= 'z')) &&
					s != "/webapi/entry.cgi" && !strings.Contains(s, " ") && !strings.HasPrefix(s, "/if:") && !strings.HasPrefix(s, "/disk:") && s != "/ups" {
					out = append(out, problem{n.Pos(), "string " + n.Value + " is not the Web API's entry point"})
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
		"GET request":      `package p; import "net/http"; func f() { _, _ = http.NewRequest("GET", "u", nil) }`,
		"method variable":  `package p; import "net/http"; func f(m string) { _, _ = http.NewRequestWithContext(nil, m, "u", nil) }`,
		"http.Get":         `package p; import "net/http"; func f() { _, _ = http.Get("u") }`,
		"http.Post":        `package p; import "net/http"; func f() { _, _ = http.Post("u", "t", nil) }`,
		"MethodPut":        `package p; import "net/http"; var m = http.MethodPut`,
		"MethodDelete":     `package p; import "net/http"; var m = http.MethodDelete`,
		"query endpoint":   `package p; var u = "/webapi/query.cgi"`,
		"auth endpoint":    `package p; var u = "/webapi/auth.cgi"`,
		"another endpoint": `package p; var u = "/webman/login.cgi"`,
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(readOnlyProblems(f, true)) == 0 {
			t.Errorf("%s: not reported", name)
		}
	}
	good, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; import ("context"; "net/http"); func f(ctx context.Context) { _, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/webapi/entry.cgi", nil) }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(good, true); len(p) != 0 {
		t.Errorf("a POST to the entry point is reported: %v", p)
	}
	fake, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; import "net/http"; var u = "/anything"; var m = http.MethodPut`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(fake, false); len(p) != 1 {
		t.Errorf("a fake: %v, want only the write reported", p)
	}
}
