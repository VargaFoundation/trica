// Command trica is the trica server and its administration commands.
//
//	trica server  [--config FILE]   serve the inventory
//	trica migrate [--config FILE] up|down|status   (down rolls back one step)
//	trica export  write the inventory as JSON or YAML
//	trica import  submit a JSON or YAML file to the reconciliation engine
//	trica version
//
// Logic lives in internal packages; this package only wires flags.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/store"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(mainCode())
}

func mainCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "trica:", err)
		}
		return 1
	}
	return 0
}

const usage = `usage: trica <command> [flags]

commands:
  server   serve the inventory (HTTP, background loops)
  migrate  apply pending migrations (up), roll the latest one back (down)
           or print the schema version (status)
  export   write the inventory (objects, attributes, relations) as JSON or YAML
  import   submit a JSON or YAML file to the reconciliation engine
  version  print the version

server and migrate read the database URL from TRICA_DATABASE_URL or the file
named by database.url_file in the configuration; export and import from
--database-url or TRICA_DATABASE_URL.`

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return errors.New("no command")
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "export":
		return runExport(ctx, args, stdout, stderr, getenv)
	case "import":
		return runImport(ctx, args, stdin, stdout, stderr, getenv)
	case "server", "migrate":
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usage)
		return nil
	default:
		fmt.Fprintln(stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", getenv("TRICA_CONFIG"), "configuration file (YAML)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := server.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	log := newLogger(stderr, cfg.LogLevel)

	if cmd == "migrate" {
		return migrate(ctx, cfg, fs.Args(), stdout)
	}
	return server.New(cfg, log, version, components()...).Run(ctx)
}

func newLogger(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l}))
}

func migrate(ctx context.Context, cfg *server.Config, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("migrate: want up, down or status")
	}
	url, err := cfg.DatabaseURL()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "up":
		if err := st.Migrate(ctx); err != nil {
			return err
		}
	case "down":
		if err := st.MigrateDownOne(ctx); err != nil {
			return err
		}
	case "status":
	default:
		return fmt.Errorf("migrate: unknown action %q", args[0])
	}
	v, err := st.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "schema version %d\n", v)
	return nil
}
