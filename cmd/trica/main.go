// Command trica is the operator's command line. Subcommands:
//
//	trica export   write the inventory as JSON or YAML
//	trica import   submit a JSON or YAML file to the reconciliation engine
//
// Logic lives in internal packages; this file only wires flags.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/inventoryio"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const usage = `usage: trica <command> [flags]

commands:
  export   write the inventory (objects, attributes, relations) as JSON or YAML
  import   submit a JSON or YAML file to the reconciliation engine

The database is given by --database-url or TRICA_DATABASE_URL.
`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "trica:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("no command")
	}
	switch args[0] {
	case "export":
		return runExport(ctx, args[1:], stdout, stderr, getenv)
	case "import":
		return runImport(ctx, args[1:], stdin, stdout, stderr, getenv)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	}
	fmt.Fprint(stderr, usage)
	return fmt.Errorf("unknown command %q", args[0])
}

func openStore(ctx context.Context, url string, getenv func(string) string) (*store.Store, error) {
	if url == "" {
		url = getenv("TRICA_DATABASE_URL")
	}
	if url == "" {
		return nil, errors.New("no database: pass --database-url or set TRICA_DATABASE_URL")
	}
	return store.Open(ctx, url)
}

// formatOf picks the format from the flag, else the file extension, else json.
func formatOf(flagValue, path string) string {
	if flagValue != "" {
		return flagValue
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return "yaml"
	}
	return "json"
}

func runExport(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbURL := fs.String("database-url", "", "PostgreSQL URL (default $TRICA_DATABASE_URL)")
	format := fs.String("format", "", "json or yaml (default: from the -o extension, else json)")
	out := fs.String("o", "", "output file (default stdout)")
	at := fs.String("at", "", "export the state at this date (RFC 3339 or YYYY-MM-DD)")
	history := fs.Bool("history", false, "include every past value and closed relation")
	noProv := fs.Bool("no-provenance", false, "leave out the source and confidence of each value")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opt := inventoryio.ExportOptions{History: *history, NoProvenance: *noProv}
	if *at != "" {
		t, err := parseTime(*at)
		if err != nil {
			return err
		}
		opt.At = &t
	}
	s, err := openStore(ctx, *dbURL, getenv)
	if err != nil {
		return err
	}
	defer s.Close()
	doc, err := inventoryio.Export(ctx, s, schema.Builtin(), opt)
	if err != nil {
		return err
	}
	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if err := doc.Encode(w, formatOf(*format, *out)); err != nil {
		return err
	}
	if doc.Omitted != nil && doc.Omitted.SensitiveAttributes > 0 {
		fmt.Fprintf(stderr, "trica: %d sensitive value(s) not exported\n", doc.Omitted.SensitiveAttributes)
	}
	return nil
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("--at %q: use RFC 3339 or YYYY-MM-DD", s)
}

func runImport(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbURL := fs.String("database-url", "", "PostgreSQL URL (default $TRICA_DATABASE_URL)")
	format := fs.String("format", "", "json or yaml (default: from the file extension, else json)")
	name := fs.String("name", "", "import name: the source becomes import:<name> (default: the file name)")
	scope := fs.String("scope", "partial", "partial adds and refreshes; full also withdraws what a previous import of this name claimed and this one omits")
	dry := fs.Bool("dry-run", false, "show what would change, write nothing")
	author := fs.String("author", os.Getenv("USER"), "author recorded in the changeset")
	reason := fs.String("reason", "", "reason recorded in the changeset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("import takes exactly one file (or - for stdin)")
	}
	path := fs.Arg(0)
	r := stdin
	if path != "-" {
		f, err := os.Open(filepath.Clean(path))

		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	doc, err := inventoryio.Decode(r, formatOf(*format, path))
	if err != nil {
		return err
	}
	n := *name
	if n == "" {
		if path == "-" {
			return errors.New("reading stdin: pass --name")
		}
		n = strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if *scope != string(sdk.ScopePartial) && *scope != string(sdk.ScopeFull) {
		return fmt.Errorf("--scope %q: use partial or full", *scope)
	}
	s, err := openStore(ctx, *dbURL, getenv)
	if err != nil {
		return err
	}
	defer s.Close()
	reg := schema.Builtin()
	eng := &reconcile.Engine{Store: s, Schema: func() *schema.Registry { return reg }}
	res, err := inventoryio.Import(ctx, eng, doc, inventoryio.ImportOptions{
		Name: n, Scope: sdk.Scope(*scope), Author: *author, Reason: *reason, DryRun: *dry,
	})
	if err != nil {
		return err
	}
	verb := "imported"
	if *dry {
		verb = "dry run"
	}
	fmt.Fprintf(stdout, "%s as import:%s: %d created, %d values changed, %d relations opened, %d closed\n",
		verb, n, res.Created, res.ValuesChanged, res.RelationsOpened, res.RelationsClosed)
	for _, w := range res.Warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	return nil
}
