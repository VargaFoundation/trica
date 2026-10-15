package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/VargaFoundation/trica/internal/auth"
	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/transport"
	"github.com/VargaFoundation/trica/internal/wiring"
)

// The administration commands mint the first credentials of a deployment:
// whoever can run them has database access, which already is everything
// (docs/reference/security.md). The secret of a created token or enrolment
// token goes to stdout and nowhere else: not to the log, not to an error.

const adminUsage = `usage:
  trica token create --name N --owner SUBJECT --roles R1,R2 --scopes read|write|admin
                     [--types T1,T2] [--sites S1,S2] [--expires 720h] [--config FILE]
  trica token list   [--config FILE]
  trica token revoke --id N [--config FILE]
  trica enrol create --binding KIND:NAME [--ttl 24h] [--config FILE]`

// tokenFlags holds the flags of "trica token"; the pointers of an action's
// flags are nil for the others.
type tokenFlags struct {
	name, owner, roles, scopes, types, sites *string
	source                                   *string
	expires                                  *time.Duration
	id                                       *int64
}

// runToken is "trica token create|list|revoke".
func runToken(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, adminUsage)
		return errors.New("token: want create, list or revoke")
	}
	action, args := args[0], args[1:]
	fs := flag.NewFlagSet("token "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", getenv("TRICA_CONFIG"), "configuration file (YAML)")
	var f tokenFlags
	switch action {
	case "create":
		f.name = fs.String("name", "", "token name (required)")
		f.owner = fs.String("owner", "", "subject of the user owning the token (required)")
		f.roles = fs.String("roles", "", "owner roles, comma separated, defined in the RBAC policy (required)")
		f.scopes = fs.String("scopes", "", "read, write or admin, comma separated (required)")
		f.types = fs.String("types", "", "restrict the token to these object types")
		f.sites = fs.String("sites", "", "restrict the token to these sites")
		f.source = fs.String("source", "", "source its batches are attributed to, e.g. arista:sw-a1 (default: api:token-<id>)")
		f.expires = fs.Duration("expires", 0, "validity, e.g. 720h (default: no expiry)")
	case "revoke":
		f.id = fs.Int64("id", 0, "id of the token to revoke (required)")
	case "list":
	default:
		fmt.Fprintln(stderr, adminUsage)
		return fmt.Errorf("token: unknown action %q", action)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("token %s: unexpected argument %q", action, fs.Arg(0))
	}
	// Validate before touching the database: a typo must not need a connection.
	switch action {
	case "create":
		if *f.expires < 0 {
			return errors.New("token create: --expires must be positive")
		}
		if *f.name == "" || *f.owner == "" || *f.roles == "" || *f.scopes == "" {
			return errors.New("token create: --name, --owner, --roles and --scopes are required")
		}
		if *f.source != "" {
			if err := auth.ValidTokenSource(*f.source); err != nil {
				return fmt.Errorf("token create: %w", err)
			}
		}
	case "revoke":
		if *f.id <= 0 {
			return errors.New("token revoke: --id is required")
		}
	}
	cfg, err := server.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	pol, err := wiring.Policy(cfg)
	if err != nil {
		return err
	}
	st, err := openConfigStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	ts := &auth.TokenStore{Store: st, Policy: pol}

	switch action {
	case "create":
		return tokenCreate(ctx, ts, pol, tokenArgs{
			name: *f.name, owner: *f.owner, roles: splitList(*f.roles), scopes: splitList(*f.scopes),
			types: splitList(*f.types), sites: splitList(*f.sites), source: *f.source, expires: *f.expires,
		}, stdout, stderr)
	case "revoke":
		// Revoke marks revoked_at; the row stays (it is the audit trail).
		if err := ts.Revoke(ctx, *f.id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("token revoke: no active token %d (unknown or already revoked)", *f.id)
			}
			return err
		}
		fmt.Fprintf(stderr, "token %d revoked\n", *f.id)
		return nil
	}
	return tokenList(ctx, ts, stdout)
}

type tokenArgs struct {
	name, owner, source         string
	roles, scopes, types, sites []string
	expires                     time.Duration
}

func tokenCreate(ctx context.Context, ts *auth.TokenStore, pol *auth.Policy, a tokenArgs, stdout, stderr io.Writer) error {
	known := pol.RoleNames()
	for _, r := range a.roles {
		if !slices.Contains(known, r) {
			return fmt.Errorf("token create: role %q is not in the RBAC policy (known: %s)", r, strings.Join(known, ", "))
		}
	}
	p := auth.CreateParams{Name: a.name, Scopes: a.scopes, Types: a.types, Sites: a.sites, Source: a.source}
	if a.expires > 0 {
		t := time.Now().UTC().Add(a.expires)
		p.ExpiresAt = &t
	}
	owner := &auth.Principal{Subject: "user:" + strings.TrimPrefix(a.owner, "user:"), Kind: auth.KindUser, Roles: a.roles}
	secret, tok, err := ts.Create(ctx, owner, p)
	if err != nil {
		return fmt.Errorf("token create: %w", err)
	}
	// Metadata on stderr, the secret alone on stdout: the output can be piped.
	fmt.Fprintf(stderr, "token %d %q created for %s: scopes %s, roles %s", tok.ID, tok.Name, tok.CreatedBy,
		strings.Join(tok.Scopes, ","), strings.Join(tok.Roles, ","))
	if tok.Source != "" {
		fmt.Fprintf(stderr, ", source %s", tok.Source)
	}
	if tok.ExpiresAt != nil {
		fmt.Fprintf(stderr, ", expires %s", tok.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Fprintln(stderr, "\nthe token is shown once, on stdout: store it now")
	fmt.Fprintln(stdout, secret)
	return nil
}

func tokenList(ctx context.Context, ts *auth.TokenStore, stdout io.Writer) error {
	toks, err := ts.List(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tOWNER\tROLES\tSCOPES\tTYPES\tSITES\tSOURCE\tCREATED\tEXPIRES\tLAST USED\tREVOKED")
	for _, t := range toks {
		src := t.Source
		if src == "" {
			src = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.CreatedBy,
			joinOrDash(t.Roles), joinOrDash(t.Scopes), joinOrDash(t.Types), joinOrDash(t.Sites), src,
			t.CreatedAt.Format(time.RFC3339), timeOrDash(t.ExpiresAt), timeOrDash(t.LastUsed), timeOrDash(t.RevokedAt))
	}
	return w.Flush()
}

// runEnrol is "trica enrol create".
func runEnrol(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprintln(stderr, adminUsage)
		return errors.New("enrol: want create")
	}
	fs := flag.NewFlagSet("enrol create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", getenv("TRICA_CONFIG"), "configuration file (YAML)")
	binding := fs.String("binding", "", "source the machine may report as, e.g. agent:host-01 (required)")
	ttl := fs.Duration("ttl", transport.DefaultTokenTTL, "validity of the token")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("enrol create: unexpected argument %q", fs.Arg(0))
	}
	if *binding == "" {
		return errors.New("enrol create: --binding is required")
	}
	if err := pki.ValidBinding(*binding); err != nil {
		return err
	}
	if *ttl <= 0 {
		return errors.New("enrol create: --ttl must be positive")
	}
	cfg, err := server.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	st, err := openConfigStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	token, err := transport.NewRegistry(st.Pool).NewEnrolmentToken(ctx, *binding, *ttl)
	if err != nil {
		return fmt.Errorf("enrol create: %w", err)
	}
	fmt.Fprintf(stderr, "enrolment token for %s, valid %s, usable once\n", *binding, *ttl)
	if t := cfg.Transport; t.Addr != "" {
		fmt.Fprintf(stderr, "the client dials %s (server certificate hosts: %s)\n", t.Addr, strings.Join(t.Hosts, ", "))
		fmt.Fprintf(stderr, "the client needs the CA certificate: %s on the server (public, hand it over with the token)\n",
			filepath.Join(t.CADir, pki.CACertFile))
	} else {
		fmt.Fprintln(stderr, "warning: transport.addr is not set in this configuration: no server accepts an enrolment yet")
	}
	fmt.Fprintln(stdout, token)
	return nil
}

func openConfigStore(ctx context.Context, cfg *server.Config) (*store.Store, error) {
	url, err := cfg.DatabaseURL()
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, url)
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func joinOrDash(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, ",")
}

func timeOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}
