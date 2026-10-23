package api

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
)

// CollectorSource is the collector catalogue and the targets this server runs
// (ADR 0055). The view the API builds is read-only and computed at each
// request from this, the health the engine records and nothing else: no table
// holds a second copy of the targets file.
type CollectorSource interface {
	// Modules describes every collector module linked into the binary.
	Modules() []collect.Descriptor
	// Targets lists the targets this server runs itself (its targets file).
	Targets() []collect.Target
}

// RegistryCollectors is the CollectorSource of a server: the modules linked
// into the binary (internal/collect.Modules) and the targets it was
// configured with.
type RegistryCollectors struct{ InServer []collect.Target }

// Modules implements CollectorSource.
func (r RegistryCollectors) Modules() []collect.Descriptor { return collect.Modules() }

// Targets implements CollectorSource.
func (r RegistryCollectors) Targets() []collect.Target { return r.InServer }

// CollectorCatalogue is every collector module and the sources no module
// claims.
type CollectorCatalogue struct {
	Modules []CollectorModule `json:"modules"`
	// Unregistered are the health rows whose source prefix no module of this
	// binary registers: a collector built outside the repository through
	// pkg/sdk, or one of a newer version.
	Unregistered []SourceHealth `json:"unregistered"`
}

// CollectorModule is what a collector module says about itself, with the
// count of its instances by status.
type CollectorModule struct {
	Kind        string                `json:"kind"`
	Title       string                `json:"title"`
	Vendors     []string              `json:"vendors"`
	Protocol    string                `json:"protocol"`
	Summary     schema.Label          `json:"summary"`
	Deployment  string                `json:"deployment"`
	Collects    []string              `json:"collects"`
	ReadOnly    string                `json:"read_only"`
	Address     string                `json:"address,omitempty"`
	Credentials *CollectorCredentials `json:"credentials,omitempty"`
	Options     []CollectorOption     `json:"options"`
	Tested      []string              `json:"tested"`
	Evidence    string                `json:"evidence"`
	// Docs is the repository-relative path of the README; DocsURL is where to
	// read it on GitHub (the tag of this build, or main).
	Docs    string          `json:"docs"`
	DocsURL string          `json:"docs_url"`
	Counts  CollectorCounts `json:"counts"`
}

// CollectorCredentials says how a target of the module uses its two
// credential keys.
type CollectorCredentials struct {
	Username CollectorCredential `json:"username"`
	Secret   CollectorCredential `json:"secret"`
}

// CollectorCredential is one credential key: required, optional or unused,
// and what to put there.
type CollectorCredential struct {
	Use     string `json:"use"`
	Meaning string `json:"meaning,omitempty"`
}

// CollectorOption documents one key of a target's options block.
type CollectorOption struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Default string   `json:"default,omitempty"`
	Values  []string `json:"values,omitempty"`
	// ValueKind is the kind of the values of a "map" option (string or integer).
	ValueKind   string `json:"value_kind,omitempty"`
	Description string `json:"description"`
}

// CollectorCounts counts the instances of a module by status.
type CollectorCounts struct {
	Instances int `json:"instances"`
	OK        int `json:"ok"`
	Failing   int `json:"failing"`
	Pending   int `json:"pending"`
	Stale     int `json:"stale"`
}

// Instance statuses, in the order a list shows them.
const (
	instFailing = "failing"
	instStale   = "stale"
	instPending = "pending"
	instOK      = "ok"
)

var instanceStatuses = []string{instOK, instFailing, instPending, instStale}

// Instance origins: configured in this server's targets file, or known only
// through its health (a remote collector, the host agent).
const (
	originServer   = "server"
	originReported = "reported"
)

// CollectorInstance is one source of a module: a target of the server's
// targets file, or a source seen only through its health.
type CollectorInstance struct {
	// Source is "<kind>:<name>", the key of the health board.
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	// Origin is "server" (in this server's targets file) or "reported".
	Origin string `json:"origin"`
	// Status is ok, failing, pending (configured, no run yet) or stale
	// (configured, last attempt older than three intervals and a timeout).
	Status string        `json:"status"`
	Config *TargetConfig `json:"config,omitempty"`
	Health *SourceHealth `json:"health,omitempty"`
}

// TargetTLS is the TLS part of a target.
type TargetTLS struct {
	CAFile     string `json:"ca_file"`
	Insecure   bool   `json:"insecure"`
	MinVersion string `json:"min_version"`
}

// TargetConfig is a target of the targets file as the API shows it: values
// that can carry a secret are references or absent (ADR 0055).
type TargetConfig struct {
	// Address has the credentials of a URL cleaned out.
	Address         string    `json:"address"`
	IntervalSeconds int64     `json:"interval_seconds"`
	TimeoutSeconds  int64     `json:"timeout_seconds"`
	Domain          string    `json:"domain,omitempty"`
	TLS             TargetTLS `json:"tls"`
	// Options are the decoded options; a secret reference shows as its
	// reference and a sensitive-named key never shows a value.
	Options map[string]any `json:"options"`
	// Credentials is set only for a caller holding the admin action.
	Credentials       *TargetCredentials `json:"credentials,omitempty"`
	CredentialsHidden bool               `json:"credentials_hidden,omitempty"`
}

// TargetCredentials are the references of a target, never a value.
type TargetCredentials struct {
	Username string `json:"username,omitempty"`
	// SecretRef is a "file:" or "env:" reference, or "[redacted]" for a value
	// written without a prefix.
	SecretRef string `json:"secret_ref,omitempty"`
}

// collectorSource is the source the server was configured with, or the
// registry alone.
func (s *Server) collectorSource() CollectorSource {
	if s.collectors == nil {
		return RegistryCollectors{}
	}
	return s.collectors
}

// moduleOf is the kind of the module a source belongs to: its prefix, when a
// module registers it. The prefix comes from the credential the source was
// enrolled with, which is why instances are keyed by it and not by the
// collector name or the target a collector declares for itself.
func moduleOf(source string, mods map[string]collect.Descriptor) string {
	kind, _, found := strings.Cut(source, ":")
	if !found {
		return ""
	}
	if _, ok := mods[kind]; ok {
		return kind
	}
	return ""
}

func moduleIndex(src CollectorSource) map[string]collect.Descriptor {
	out := map[string]collect.Descriptor{}
	for _, d := range src.Modules() {
		out[d.Kind] = d
	}
	return out
}

// docsURL is where the README of a module can be read: its tag for a release
// build, main otherwise.
func (s *Server) docsURL(docs string) string {
	ref := "main"
	if semverRe.MatchString(s.version) && s.version != "0.0.0" {
		ref = "v" + strings.TrimPrefix(s.version, "v")
	}
	return "https://github.com/VargaFoundation/trica/blob/" + ref + "/" + docs
}

var semverRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// instances joins the targets of the server with the health rows, for every
// module. Instances of a module are sorted failing, stale, pending, ok, then
// by name.
func (s *Server) instances(ctx context.Context) (map[string][]CollectorInstance, []SourceHealth, error) {
	src := s.collectorSource()
	mods := moduleIndex(src)
	rows, err := s.sourceHealth(ctx)
	if err != nil {
		return nil, nil, err
	}
	health := map[string]*SourceHealth{}
	var unregistered []SourceHealth
	for i := range rows {
		if rows[i].Module == "" {
			unregistered = append(unregistered, rows[i])
			continue
		}
		health[rows[i].Source] = &rows[i]
	}
	out := map[string][]CollectorInstance{}
	configured := map[string]bool{}
	for _, t := range src.Targets() {
		if _, ok := mods[t.Kind]; !ok {
			continue
		}
		configured[t.Source()] = true
		in := CollectorInstance{Source: t.Source(), Kind: t.Kind, Name: t.Name, Origin: originServer, Config: targetConfig(t, mods[t.Kind]), Health: health[t.Source()]}
		in.Status = s.instanceStatus(t, in.Health)
		out[t.Kind] = append(out[t.Kind], in)
	}
	for source, h := range health {
		if configured[source] {
			continue
		}
		kind := h.Module
		in := CollectorInstance{Source: source, Kind: kind, Name: strings.TrimPrefix(source, kind+":"), Origin: originReported, Health: h, Status: instOK}
		if !h.OK {
			in.Status = instFailing
		}
		out[kind] = append(out[kind], in)
	}
	rank := map[string]int{instFailing: 0, instStale: 1, instPending: 2, instOK: 3}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool {
			a, b := out[k][i], out[k][j]
			if rank[a.Status] != rank[b.Status] {
				return rank[a.Status] < rank[b.Status]
			}
			return a.Name < b.Name
		})
	}
	if unregistered == nil {
		unregistered = []SourceHealth{}
	}
	return out, unregistered, nil
}

// instanceStatus is the status of a configured target: pending until it has
// run once; stale when its last attempt is older than three intervals and a
// timeout, which says the collector is not running at all; otherwise what its
// last run says.
func (s *Server) instanceStatus(t collect.Target, h *SourceHealth) string {
	switch {
	case h == nil:
		return instPending
	case s.now().Sub(h.LastAttempt) > 3*t.EffectiveInterval()+t.EffectiveTimeout():
		return instStale
	case h.OK:
		return instOK
	}
	return instFailing
}

// targetConfig is the view of a target. It never copies a value that can be a
// secret: the address is cleaned of URL credentials, a secret-kind or
// sensitive-named option shows as a reference, and the credentials are added
// by the handler for an administrator only.
func targetConfig(t collect.Target, d collect.Descriptor) *TargetConfig {
	return &TargetConfig{
		Address:         scrubError(t.Address),
		IntervalSeconds: int64(t.EffectiveInterval() / time.Second),
		TimeoutSeconds:  int64(t.EffectiveTimeout() / time.Second),
		Domain:          t.Domain,
		TLS:             TargetTLS{CAFile: t.TLS.CAFile, Insecure: t.TLS.Insecure, MinVersion: t.TLS.MinVersion},
		Options:         optionsView(t, d),
	}
}

func optionsView(t collect.Target, d collect.Descriptor) map[string]any {
	out := map[string]any{}
	if t.Options.Kind == 0 {
		return out
	}
	var raw map[string]any
	if err := t.Options.Decode(&raw); err != nil {
		return out
	}
	refs := map[string]bool{}
	fields, _ := d.OptionFields()
	for _, f := range fields {
		if f.Kind == collect.OptionSecretReference {
			refs[f.Name] = true
		}
	}
	for k, v := range raw {
		if shown, ok := optionValue(k, v, refs[k]); ok {
			out[k] = shown
		}
	}
	return out
}

// optionValue shows an option. A reference-kind or sensitive-named key shows
// as collect.Secret does (the reference, or "[redacted]"), or not at all when
// it is not a string.
func optionValue(key string, v any, isRef bool) (any, bool) {
	if isRef || Sensitive(key) {
		if str, ok := v.(string); ok {
			return collect.Secret(str).String(), true
		}
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		sub := make(map[string]any, len(m))
		for k, x := range m {
			if shown, ok := optionValue(k, x, false); ok {
				sub[k] = shown
			}
		}
		return sub, true
	}
	return v, true
}

// moduleView is the API view of a descriptor.
func (s *Server) moduleView(d collect.Descriptor, insts []CollectorInstance) CollectorModule {
	m := CollectorModule{
		Kind: d.Kind, Title: d.Title, Vendors: orEmpty(d.Vendors), Protocol: d.Protocol, Summary: d.Summary,
		Deployment: string(d.Deployment), Collects: orEmpty(d.Collects), ReadOnly: d.ReadOnly, Address: d.Address,
		Options: []CollectorOption{}, Tested: orEmpty(d.Tested), Evidence: string(d.Evidence),
		Docs: d.Docs, DocsURL: s.docsURL(d.Docs),
	}
	if d.Deployment == collect.DeploymentTarget {
		m.Credentials = &CollectorCredentials{
			Username: CollectorCredential{Use: string(d.Username.Use), Meaning: d.Username.Meaning},
			Secret:   CollectorCredential{Use: string(d.Secret.Use), Meaning: d.Secret.Meaning},
		}
	}
	fields, _ := d.OptionFields() // validated at registration
	for _, f := range fields {
		m.Options = append(m.Options, CollectorOption{Name: f.Name, Type: string(f.Kind), Default: f.Default, Values: f.Values, ValueKind: string(f.ValueKind), Description: f.Description})
	}
	m.Counts.Instances = len(insts)
	for _, in := range insts {
		switch in.Status {
		case instOK:
			m.Counts.OK++
		case instFailing:
			m.Counts.Failing++
		case instPending:
			m.Counts.Pending++
		case instStale:
			m.Counts.Stale++
		}
	}
	return m
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// instanceView adds the credential references to a copy of an instance for a
// caller who holds admin; any other caller is told they are hidden.
func (c *call) instanceView(in CollectorInstance, targets []collect.Target) CollectorInstance {
	if in.Config == nil {
		return in
	}
	cfg := *in.Config
	if c.s.auth.Allow(c.p, ActionAdmin, "", "") {
		for _, t := range targets {
			if t.Source() == in.Source {
				cfg.Credentials = &TargetCredentials{Username: t.Username, SecretRef: t.Password.String()}
			}
		}
	} else {
		cfg.CredentialsHidden = true
	}
	in.Config = &cfg
	return in
}

func (s *Server) hListCollectors(c *call) error {
	src := s.collectorSource()
	insts, unregistered, err := s.instances(c.ctx())
	if err != nil {
		return err
	}
	out := CollectorCatalogue{Modules: []CollectorModule{}, Unregistered: unregistered}
	for _, d := range src.Modules() {
		out.Modules = append(out.Modules, s.moduleView(d, insts[d.Kind]))
	}
	return c.json(http.StatusOK, out)
}

// module answers the descriptor of the {kind} path parameter, or a 404.
func (c *call) module() (collect.Descriptor, error) {
	kind := c.r.PathValue("kind")
	for _, d := range c.s.collectorSource().Modules() {
		if d.Kind == kind {
			return d, nil
		}
	}
	return collect.Descriptor{}, notFound("no collector module %q", kind)
}

func (s *Server) hGetCollector(c *call) error {
	d, err := c.module()
	if err != nil {
		return err
	}
	insts, _, err := s.instances(c.ctx())
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, s.moduleView(d, insts[d.Kind]))
}

func (s *Server) hListCollectorInstances(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	status := c.r.URL.Query().Get("status")
	if status != "" && !contains(instanceStatuses, status) {
		return badRequest("status must be one of %s", strings.Join(instanceStatuses, ", "))
	}
	d, err := c.module()
	if err != nil {
		return err
	}
	insts, _, err := s.instances(c.ctx())
	if err != nil {
		return err
	}
	src := s.collectorSource()
	out := []CollectorInstance{}
	for _, in := range insts[d.Kind] {
		if status == "" || in.Status == status {
			out = append(out, c.instanceView(in, src.Targets()))
		}
	}
	return c.json(http.StatusOK, pageOf(out, pp, false))
}

func (s *Server) hGetCollectorInstance(c *call) error {
	d, err := c.module()
	if err != nil {
		return err
	}
	insts, _, err := s.instances(c.ctx())
	if err != nil {
		return err
	}
	name := c.r.PathValue("name")
	for _, in := range insts[d.Kind] {
		if in.Name == name {
			return c.json(http.StatusOK, c.instanceView(in, s.collectorSource().Targets()))
		}
	}
	return notFound("no instance %q of the %s module", name, d.Kind)
}
