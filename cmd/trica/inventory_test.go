package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The two subcommands end to end on real databases: export a seeded one to a
// YAML file, import the file into an empty one.
func TestExportThenImportCLI(t *testing.T) {
	src, dst := testdb.New(t), testdb.New(t)
	reg := schema.Builtin()
	eng := &reconcile.Engine{Store: src, Schema: func() *schema.Registry { return reg }}
	b := sdk.NewBatch("user:vd", sdk.ScopePartial)
	b.Confidence = sdk.Declared
	b.Object("s", "site").Identify("code", "LAB-1").Set("name", "Lab")
	if _, err := eng.Apply(context.Background(), b, "", ""); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "inv.yaml")
	var out, errOut bytes.Buffer
	srcEnv := env(map[string]string{"TRICA_DATABASE_URL": src.Pool.Config().ConnString()})
	if err := run(context.Background(), []string{"export", "-o", file, "--history"}, nil, &out, &errOut, srcEnv); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), "LAB-1") {
		t.Fatalf("export file: %v %s", err, data)
	}
	dstEnv := env(map[string]string{"TRICA_DATABASE_URL": dst.Pool.Config().ConnString()})
	if err := run(context.Background(), []string{"import", "--dry-run", file}, nil, &out, &errOut, dstEnv); err != nil || !strings.Contains(out.String(), "dry run as import:inv: 1 created") {
		t.Fatalf("dry run: %v %q", err, out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"import", file}, nil, &out, &errOut, dstEnv); err != nil || !strings.Contains(out.String(), "imported as import:inv: 1 created") {
		t.Fatalf("import: %v %q", err, out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"export", "--at", "2999-01-01", "--no-provenance"}, nil, &out, &errOut, dstEnv); err != nil || !strings.Contains(out.String(), `"code": "LAB-1"`) {
		t.Fatalf("re-export: %v %q", err, out.String())
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, "no command"},
		{"unknown", []string{"frobnicate"}, "unknown command"},
		{"export without database", []string{"export"}, "no database"},
		{"export bad date", []string{"export", "--at", "yesterday"}, "--at"},
		{"import without file", []string{"import"}, "exactly one file"},
		{"import missing file", []string{"import", "--scope", "x", "/nonexistent.json"}, "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, env(nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestHelpAndHelpers(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help"}, nil, &out, &bytes.Buffer{}, env(nil)); err != nil || !strings.Contains(out.String(), "export") {
		t.Fatalf("help: %v %q", err, out.String())
	}
	if formatOf("", "x.yml") != "yaml" || formatOf("", "x.json") != "json" || formatOf("yaml", "x.json") != "yaml" || formatOf("", "") != "json" {
		t.Fatal("formatOf")
	}
	if _, err := parseTime("2026-03-01"); err != nil {
		t.Fatal(err)
	}
}

func TestImportRejectsBadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	if err := os.WriteFile(p, []byte(`{"format":"nope","version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"import", p}, nil, &bytes.Buffer{}, &bytes.Buffer{}, env(nil))
	if err == nil || !strings.Contains(err.Error(), "not a trica-inventory") {
		t.Fatalf("got %v", err)
	}
	err = run(context.Background(), []string{"import", "-"}, strings.NewReader(`{"format":"trica-inventory","version":1}`), &bytes.Buffer{}, &bytes.Buffer{}, env(nil))
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("got %v", err)
	}
}
