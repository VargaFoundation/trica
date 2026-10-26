package synology

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// declaredJSONNames lists every JSON field name the package's decode structs
// (types.go) declare.
func declaredJSONNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "types.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		fld, ok := n.(*ast.Field)
		if !ok || fld.Tag == nil {
			return true
		}
		tag, _ := strconv.Unquote(fld.Tag.Value)
		if name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ","); name != "" {
			out = append(out, name)
		}
		return true
	})
	return out
}
