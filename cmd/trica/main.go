// Command trica is the trica server and its administration commands.
//
//	trica server  [--config FILE]   serve the inventory
//	trica migrate [--config FILE] up|down|status   (down rolls back one step)
//	trica collector --config FILE   run collectors remotely, sending over mTLS
//	trica export  write the inventory as JSON or YAML
//	trica import  submit a JSON or YAML file to the reconciliation engine
//	trica backup  [--config FILE] (--file F | --dir D)
//	trica restore [--config FILE] --file F [--replace]
//	trica token   [--config FILE] create|list|revoke   (API tokens)
//	trica enrol   [--config FILE] create   (agent and collector enrolment tokens)
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
	"path/filepath"
	"syscall"
	"time"

	"github.com/VargaFoundation/trica/internal/backup"
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
  collector run collectors at a site the server cannot reach (--config FILE)
  export   write the inventory (objects, attributes, relations) as JSON or YAML
  import   submit a JSON or YAML file to the reconciliation engine
  backup   write a consistent archive of the database (--file F, or --dir D
           for a timestamped name); needs pg_dump 17
  restore  load an archive into an empty database (--file F), or over an
           existing one (--replace, server stopped); runs migrations up
  token    create (prints the token once on stdout), list or revoke API tokens
  enrol    create an enrolment token for an agent or collector (prints it once
           on stdout)
  version  print the version

server, migrate, backup, restore, token and enrol read the database URL from TRICA_DATABASE_URL or the file
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
	case "token":
		return runToken(ctx, args, stdout, stderr, getenv)
	case "enrol":
		return runEnrol(ctx, args, stdout, stderr, getenv)
	case "collector":
		return runCollector(ctx, args, stderr, getenv)
	case "server", "migrate", "backup", "restore":
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
	file := fs.String("file", "", "backup: archive to write; restore: archive to read")
	dir := fs.String("dir", "", "backup: directory for a timestamped archive name")
	replace := fs.Bool("replace", false, "restore: drop the target database's tables first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := server.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	log := newLogger(stderr, cfg.LogLevel)

	switch cmd {
	case "migrate":
		return migrate(ctx, cfg, fs.Args(), stdout)
	case "backup":
		return runBackup(ctx, cfg, log, *file, *dir, stdout)
	case "restore":
		return runRestore(ctx, cfg, log, *file, *replace, stdout)
	}
	return server.New(cfg, log, version, components(cfg)...).Run(ctx)
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

func runBackup(ctx context.Context, cfg *server.Config, log *slog.Logger, file, dir string, stdout io.Writer) error {
	if (file == "") == (dir == "") {
		return errors.New("backup: give exactly one of --file and --dir")
	}
	if file == "" {
		file = filepath.Join(dir, "trica-"+time.Now().UTC().Format("20060102T150405Z")+".dump")
	}
	url, err := cfg.DatabaseURL()
	if err != nil {
		return err
	}
	info, err := backup.Backup(ctx, url, file, backup.Tools{}, log)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "backup %s schema version %d, %d bytes, %.1fs\n", info.File, info.Version, info.Bytes, info.Duration.Seconds())
	return nil
}

func runRestore(ctx context.Context, cfg *server.Config, log *slog.Logger, file string, replace bool, stdout io.Writer) error {
	if file == "" {
		return errors.New("restore: --file is required")
	}
	url, err := cfg.DatabaseURL()
	if err != nil {
		return err
	}
	info, err := backup.Restore(ctx, url, file, backup.RestoreOptions{Replace: replace}, log)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "restored %s: archive schema version %d, now %d, %.1fs\n", info.File, info.Version, info.Migrated, info.Duration.Seconds())
	return nil
}
