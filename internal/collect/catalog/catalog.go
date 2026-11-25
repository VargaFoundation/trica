// Package catalog reads the application catalog: the applications people
// declare (owner, criticality, lifecycle, dependencies, SaaS vendors) and the
// instances no collector derives (an application on a VM or a server), from a
// YAML file, a directory of them (a mounted ConfigMap) or an https URL (a raw
// file of a Git repository). ADR 0071.
//
// It speaks as a declaration: every value it gives is declared, ranks below
// what a collector observes, and opens a discrepancy where both disagree. The
// batch is full-scope: an application taken out of the catalog loses what the
// catalog said of it, and becomes absent unless a collector still sees it.
// A catalog that does not parse or validate is not applied at all: the run
// fails and nothing is withdrawn.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector family and the Target.Kind value that selects it.
const Kind = "catalog"

// MaxBytes bounds what one run reads, over all files: a catalog is text
// written by people, not a dump.
const MaxBytes = 4 << 20

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:     Kind,
	Title:    "Application catalog",
	Vendors:  []string{"trica"},
	Protocol: "YAML files read from disk (a file or a directory, such as a mounted ConfigMap), or one file fetched with GET over HTTPS",
	Summary: schema.Label{
		EN: "The applications you declare, kept next to your code: owner (team or person), criticality, lifecycle, business domain, repository, dependencies between applications, SaaS vendors, components, and the instances no collector discovers (an application on a VM or a server, by the name the agent or another collector gives the host). Applications discovered in Kubernetes and Argo CD are the same objects, completed with what is declared here.",
		FR: "Les applications que vous déclarez, à côté de votre code : propriétaire (équipe ou personne), criticité, cycle de vie, domaine métier, dépôt, dépendances entre applications, éditeurs SaaS, composants, et les instances qu'aucun collecteur ne découvre (une application sur une VM ou un serveur, par le nom que l'agent ou un autre collecteur donne à l'hôte). Les applications découvertes dans Kubernetes et Argo CD sont les mêmes objets, complétés par ce qui est déclaré ici.",
	},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"application", "component", "component_instance", "endpoint", "environment", "person", "team", "vendor"},
	ReadOnly:   "It writes nothing anywhere. A file address is only opened for reading; an https address is fetched with one GET per run, redirects not followed, the optional token sent in an Authorization header and never in a URL, a log or an error.",
	Address:    "Required: a file or a directory of *.yaml files (/etc/trica/collect/configmaps/catalog), or the https URL of one file (https://git.example.org/org/catalog/raw/main/applications.yaml).",
	Username:   collect.Credential{Use: collect.UseUnused},
	Secret:     collect.Credential{Use: collect.UseOptional, Meaning: "for an https address: a read-only token sent as a bearer token (a Git host's personal or deploy token restricted to the catalog repository)"},
	Options:    Options{},
	Tested:     []string{"catalog format trica-catalog version 1, from a file, a directory laid out as a mounted ConfigMap, and an https server (synthesised)"},
	Evidence:   collect.EvidenceSynthesised,
	Docs:       "internal/collect/catalog/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Options are the collector-specific settings (target.options).
type Options struct {
	AllowEmpty bool `yaml:"allow_empty" default:"false" doc:"Accept a catalog with no application. By default an empty catalog fails the run: applied, it would withdraw every application the catalog declared before (an emptied ConfigMap, a wrong path)."`
}

type collector struct {
	t    collect.Target
	opts Options
	// path is set for a file or directory address, url for an https one.
	path string
	url  *url.URL
}

// New builds a collector for t and checks what can be checked without
// reading: the address form, the options, the TLS settings.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	c := &collector{t: t, opts: opts}
	addr := strings.TrimSpace(t.Address)
	switch {
	case addr == "":
		return nil, fmt.Errorf("target %s: address is required (a file, a directory or an https URL)", t.Source())
	case strings.Contains(addr, "://"):
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("target %s: invalid address %q", t.Source(), addr)
		}
		if u.Scheme != "https" {
			return nil, fmt.Errorf("target %s: address must be https or a path, got %q", t.Source(), u.Scheme)
		}
		if u.User != nil || u.Fragment != "" || secretInQuery(u) {
			return nil, fmt.Errorf("target %s: address must hold no credentials or fragment: use password for a token", t.Source())
		}
		if _, err := t.HTTPClient(); err != nil {
			return nil, err
		}
		c.url = u
	default:
		c.path = strings.TrimPrefix(addr, "file:")
		if !filepath.IsAbs(c.path) {
			return nil, fmt.Errorf("target %s: address %q must be an absolute path", t.Source(), addr)
		}
		if t.Password != "" {
			return nil, fmt.Errorf("target %s: password is only for an https address", t.Source())
		}
	}
	return c, nil
}

// secretInQuery reports a query parameter whose name says it carries a
// credential (GitLab's private_token, an access_token...): it would be
// written in the targets file, the logs and the Collectors page.
func secretInQuery(u *url.URL) bool {
	for k := range u.Query() {
		k = strings.ToLower(k)
		for _, w := range []string{"token", "secret", "password", "key", "sig", "auth"} {
			if strings.Contains(k, w) {
				return true
			}
		}
	}
	return false
}

// Kind implements sdk.Collector.
func (c *collector) Kind() string { return Kind }

// Collect implements sdk.Collector: one run, one batch.
func (c *collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	rctx, cancel := collect.Context(ctx, c.t)
	defer cancel()

	doc, files, err := c.read(rctx)
	if err == nil {
		err = doc.Validate()
	}
	if err == nil && len(doc.Applications) == 0 && !c.opts.AllowEmpty {
		err = errors.New("the catalog declares no application; set options.allow_empty to accept that (it withdraws every application declared before)")
	}
	if err != nil {
		return emit.Emit(ctx, collect.Failed(c.t, Kind, "", started, err))
	}

	b := sdk.NewBatch(c.t.Source(), sdk.ScopeFull)
	b.Confidence = sdk.Declared
	b.WithdrawOmitted = true
	Build(b, doc)
	var warnings []string
	if len(files) > 1 {
		warnings = append(warnings, fmt.Sprintf("catalog merged from %d files: %s", len(files), strings.Join(files, ", ")))
	}
	return emit.Emit(ctx, collect.Succeeded(b, c.t, Kind, "", started, warnings))
}

// read returns the merged catalog and the names of the files it came from.
func (c *collector) read(ctx context.Context) (*Document, []string, error) {
	if c.url != nil {
		raw, err := c.fetch(ctx)
		if err != nil {
			return nil, nil, err
		}
		d, err := ParseBytes(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", c.url.Path, err)
		}
		return d, []string{c.url.Path}, nil
	}
	return ReadPath(c.path)
}

// ReadPath reads a file, or every *.yaml and *.yml file directly in a
// directory, in name order. Names starting with a dot are skipped: a mounted
// ConfigMap keeps its real files under ..data and links each key at the top.
// It does not validate: `trica catalog check` and the collector call
// Validate on the merged result.
func ReadPath(path string) (*Document, []string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	var files []string
	if st.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, ".") || (!strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml")) {
				continue
			}
			full := filepath.Join(path, n)
			if fi, err := os.Stat(full); err != nil || fi.IsDir() {
				continue
			}
			files = append(files, full)
		}
		sort.Strings(files)
		if len(files) == 0 {
			return nil, nil, fmt.Errorf("%s: no *.yaml or *.yml file", path)
		}
	} else {
		files = []string{path}
	}
	var docs []*Document
	total := 0
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // the operator names the catalog in the targets file
		if err != nil {
			return nil, nil, err
		}
		if total += len(raw); total > MaxBytes {
			return nil, nil, fmt.Errorf("%s: the catalog is larger than %d bytes", path, MaxBytes)
		}
		d, err := ParseBytes(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		docs = append(docs, d)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = filepath.Base(f)
	}
	return Merge(docs...), names, nil
}

// errRedirect stops the client at a redirect: the token must not follow the
// request to another host, and a catalog that moved is a configuration to fix.
var errRedirect = errors.New("redirect not followed")

// fetch GETs the catalog. The token, when there is one, goes in the
// Authorization header only; an error never quotes the response body or the
// header, which might echo it.
func (c *collector) fetch(ctx context.Context) ([]byte, error) {
	client, err := c.t.HTTPClient()
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errRedirect }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trica-collector")
	req.Header.Set("Accept", "application/yaml, text/plain, */*")
	if c.t.Password != "" {
		token, err := c.t.Password.Resolve()
		if err != nil {
			return nil, fmt.Errorf("password: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			// url.Error quotes the URL, which holds no credential (New refuses one).
			return nil, fmt.Errorf("GET %s: %w", c.url.Redacted(), ue.Err)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", c.url.Redacted(), resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", c.url.Redacted(), err)
	}
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("GET %s: the catalog is larger than %d bytes", c.url.Redacted(), MaxBytes)
	}
	return raw, nil
}
