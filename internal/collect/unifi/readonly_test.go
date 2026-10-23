package unifi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The collector must never write to the account. Behaviour is checked at the
// wire (the fake answers 405 to a non-GET and records it); this checks the
// source: nothing in the package can build a request that is not a GET, or
// that reaches the console proxy (/v1/connector/..., which would carry the
// key to a console's own API) or the early-access API (/ea/...).
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
		for _, problem := range readOnlyProblems(f) {
			t.Errorf("%s: %s", fset.Position(problem.pos), problem.msg)
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

// readOnlyProblems lists what in f could write or leave the three endpoints.
func readOnlyProblems(f *ast.File) []problem {
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
				for _, bad := range []string{"/connector/", "/ea/", "/proxy/"} {
					if strings.Contains(s, bad) {
						out = append(out, problem{n.Pos(), "string " + n.Value + " reaches " + bad})
					}
				}
			}
		}
		return true
	})
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
		"POST request":    `package p; import "net/http"; func f() { _, _ = http.NewRequest("POST", "u", nil) }`,
		"method variable": `package p; import "net/http"; func f(m string) { _, _ = http.NewRequestWithContext(nil, m, "u", nil) }`,
		"http.Post":       `package p; import "net/http"; func f() { _, _ = http.Post("u", "t", nil) }`,
		"client.Put":      `package p; func f(c interface{ Put() }) { c.Put() }`,
		"MethodPost":      `package p; import "net/http"; var m = http.MethodPost`,
		"connector":       `package p; var u = "/v1/connector/consoles/x/proxy"`,
		"early access":    `package p; var u = "/ea/isp-metrics"`,
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(readOnlyProblems(f)) == 0 {
			t.Errorf("%s: not reported", name)
		}
	}
	good, err := parser.ParseFile(token.NewFileSet(), "x.go", `package p; import ("context"; "net/http"); func f(ctx context.Context) { _, _ = http.NewRequestWithContext(ctx, http.MethodGet, "u", nil) }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := readOnlyProblems(good); len(p) != 0 {
		t.Errorf("a GET is reported: %v", p)
	}
	if _, err := os.Stat("client.go"); err != nil {
		t.Fatal(err)
	}
}
