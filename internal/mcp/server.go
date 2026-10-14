// Package mcp is the Model Context Protocol server of trica (ADR 0009, 0017).
//
// It exposes the inventory to AI assistants as tools: read tools answer in one
// call, paginated, with a counted summary first; the only write tool,
// propose_change, queues a proposal that a human approves. Nothing here writes
// the inventory: a proposal goes through the reconciliation engine, which alone
// decides (ADR 0005), and no tool approves, deletes or applies.
//
// Access control is the REST API's, not a second one: every call is
// authenticated and authorized per object type and site through the same
// api.Authorizer, the same sensitive-attribute rules (api.Guard) and the same
// audit trail (api.Auditor). Two transports: streamable HTTP (Handler) and
// stdio (ServeStdio, with a fixed local principal).
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
)

// Config builds a Server.
type Config struct {
	// Engine is the reconciliation engine: reads go through its store and
	// schema registry, proposals through its Propose. Required.
	Engine *reconcile.Engine
	// Authorizer authenticates callers and answers rights questions. Required.
	Authorizer api.Authorizer
	// Auditor records every tool call. Required.
	Auditor api.Auditor
	// Graph answers impact and path questions. Optional: without it the
	// impact_analysis and network_path tools are not offered.
	Graph GraphQuerier
	// Logger receives errors (JSON in production). Default slog.Default().
	Logger *slog.Logger
	// Version is reported to clients.
	Version string
	// Now is the clock for relative dates ("-24h") and audit records.
	Now func() time.Time
	// DisableLocalhostProtection turns off the SDK's refusal of requests whose
	// Host header is not a loopback name when the server listens on a loopback
	// address (DNS rebinding protection). Set it when a reverse proxy on the
	// same host forwards requests under their public name.
	DisableLocalhostProtection bool
}

// Server is the MCP server.
type Server struct {
	cfg      Config
	api      *api.Server
	eng      *reconcile.Engine
	st       *store.Store
	searcher *search.Searcher
	auth     api.Authorizer
	audit    api.Auditor
	graph    GraphQuerier
	log      *slog.Logger
	now      func() time.Time

	// How forecast_threshold reads its population: a page at a time, up to a bound.
	scanPage, scanMax int

	// The two servers the HTTP handler picks from: with and without the write
	// tool. A caller whose credential does not hold "propose" is served the
	// second one and never sees propose_change (ADR 0009).
	readOnly, readWrite *gosdk.Server
}

// New builds a Server. Like the REST API it refuses to be built without an
// Authorizer and an Auditor: there is no anonymous, unaudited mode.
func New(cfg Config) (*Server, error) {
	switch {
	case cfg.Engine == nil || cfg.Engine.Store == nil || cfg.Engine.Schema == nil:
		return nil, errors.New("mcp: an engine with a store and a schema registry is required")
	case cfg.Authorizer == nil:
		return nil, errors.New("mcp: an Authorizer is required")
	case cfg.Auditor == nil:
		return nil, errors.New("mcp: an Auditor is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	a, err := api.New(api.Config{Engine: cfg.Engine, Authorizer: cfg.Authorizer, Auditor: cfg.Auditor, Logger: cfg.Logger, Now: cfg.Now})
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, api: a, eng: cfg.Engine, st: cfg.Engine.Store,
		searcher: &search.Searcher{Store: cfg.Engine.Store, Now: cfg.Now},
		auth:     cfg.Authorizer, audit: cfg.Auditor, graph: cfg.Graph, log: cfg.Logger, now: cfg.Now,
		scanPage: api.MaxLimit, scanMax: maxForecastObjects,
	}
	s.readOnly = s.build(httpPrincipal, false)
	s.readWrite = s.build(httpPrincipal, true)
	return s, nil
}

// resolver tells who is calling a tool: from the HTTP request's verified
// credential, or a fixed principal on stdio.
type resolver func(req *gosdk.CallToolRequest) (api.Principal, error)

// ForPrincipal returns an MCP server bound to one principal, for transports
// that have no credential of their own (stdio, in-memory tests). The tools it
// offers and what they may do are still decided by the Authorizer for that
// principal, on every call.
func (s *Server) ForPrincipal(p api.Principal) *gosdk.Server {
	fixed := func(*gosdk.CallToolRequest) (api.Principal, error) { return p, nil }
	return s.build(fixed, s.auth.Allow(p, api.ActionPropose, "", ""))
}

// ServeStdio serves one client over stdin/stdout as principal p until the
// client disconnects or ctx ends. Logs must not go to stdout.
func (s *Server) ServeStdio(ctx context.Context, p api.Principal) error {
	return s.ForPrincipal(p).Run(ctx, &gosdk.StdioTransport{})
}

const instructions = `trica is an infrastructure inventory: a historised graph from sites to applications, where every value carries its source, confidence and history.
Start with search (free text or TQL: type:device vendor:arista os_version<4.20 sort:-power_w limit:10), describe_schema to learn the types and attributes, then object_summary on one object.
Every list answer starts with a summary (total, by_type, truncated) and is paginated: use offset and next_offset to read on.
Objects can be named by id or by name/address ("sw-a1", "192.0.2.13", "sw-a1/Ethernet2" for a port); an ambiguous name is refused with the candidates.
Reading is the default. The only way to write is propose_change, which queues a proposal for a human to approve: nothing is applied, deleted or approved through MCP.`

// build assembles an MCP server with the tools a caller may be offered.
func (s *Server) build(resolve resolver, write bool) *gosdk.Server {
	srv := gosdk.NewServer(&gosdk.Implementation{Name: "trica", Title: "trica inventory", Version: s.cfg.Version},
		&gosdk.ServerOptions{Instructions: instructions, Logger: s.log})
	add(s, srv, resolve, "search", descSearch, api.ActionRead, s.toolSearch)
	add(s, srv, resolve, "list_objects", descList, api.ActionRead, s.toolList)
	add(s, srv, resolve, "filter_objects", descFilter, api.ActionRead, s.toolFilter)
	add(s, srv, resolve, "rank_filter", descRank, api.ActionRead, s.toolRank)
	add(s, srv, resolve, "forecast_threshold", descForecast, api.ActionRead, s.toolForecast)
	add(s, srv, resolve, "version_compliance", descCompliance, api.ActionRead, s.toolCompliance)
	add(s, srv, resolve, "get_object", descGet, api.ActionRead, s.toolGet)
	add(s, srv, resolve, "object_summary", descSummary, api.ActionRead, s.toolSummary)
	add(s, srv, resolve, "object_history", descHistory, api.ActionRead, s.toolHistory)
	add(s, srv, resolve, "changes_since", descChanges, api.ActionRead, s.toolChanges)
	add(s, srv, resolve, "describe_schema", descSchema, api.ActionRead, s.toolSchema)
	if s.graph != nil {
		add(s, srv, resolve, "impact_analysis", descImpact, api.ActionRead, s.toolImpact)
		add(s, srv, resolve, "network_path", descPath, api.ActionRead, s.toolPath)
	}
	if write {
		add(s, srv, resolve, "propose_change", descPropose, api.ActionPropose, s.toolPropose)
	}
	return srv
}

// tc is one tool call: who, with which rights, and what to audit.
type tc struct {
	s   *Server
	ctx context.Context
	g   *api.Guard
	p   api.Principal

	// Set by tools for the audit record.
	target  string
	details map[string]any
}

// add registers a tool. Every tool goes through dispatch, in one place, so
// that no tool can forget to authenticate, authorize or audit.
func add[In any](s *Server, srv *gosdk.Server, resolve resolver, name, desc, action string, h func(*tc, In) (any, error)) {
	t := &gosdk.Tool{Name: name, Description: desc, Annotations: &gosdk.ToolAnnotations{
		ReadOnlyHint: action == api.ActionRead, DestructiveHint: ptr(false), IdempotentHint: action == api.ActionRead, OpenWorldHint: ptr(false),
	}}
	gosdk.AddTool(srv, t, func(ctx context.Context, req *gosdk.CallToolRequest, in In) (*gosdk.CallToolResult, any, error) {
		out, err := s.dispatch(ctx, req, resolve, name, action, in, func(t *tc) (any, error) { return h(t, in) })
		return nil, out, err
	})
}

func ptr[T any](v T) *T { return &v }

// dispatch authenticates the call, checks the tool's action, runs it, turns
// errors into tool errors that are safe to show, and audits the outcome.
func (s *Server) dispatch(ctx context.Context, req *gosdk.CallToolRequest, resolve resolver, name, action string, args any, h func(*tc) (any, error)) (out any, err error) {
	p, perr := resolve(req)
	t := &tc{s: s, ctx: ctx, p: p, details: map[string]any{"tool": name}}
	if perr != nil {
		t.record(name, api.OutcomeDenied, "unauthenticated")
		return nil, errors.New("unauthenticated: authentication is required")
	}
	t.g = s.api.Guard(ctx, p)
	t.details["args"] = summarizeArgs(name, args)
	defer func() {
		if rec := recover(); rec != nil {
			s.log.Error("mcp: panic in tool", slog.String("tool", name), slog.Any("panic", rec), slog.String("stack", string(debug.Stack())))
			t.record(name, api.OutcomeError, "panic")
			out, err = nil, errors.New("internal: an internal error occurred")
		}
	}()
	if !t.g.Allow(action, "", "") {
		t.record(name, api.OutcomeDenied, "the "+action+" action is not granted")
		return nil, fmt.Errorf("forbidden: the %q action is not granted", action)
	}
	out, err = h(t)
	if err != nil {
		status, slug, detail, internal := api.ErrorInfo(err)
		if internal {
			s.log.Error("mcp: internal error", slog.String("tool", name), slog.String("actor", p.Subject), slog.Any("error", err))
		}
		outcome := api.OutcomeError
		if status == http.StatusForbidden {
			outcome = api.OutcomeDenied
		}
		t.record(name, outcome, slug)
		return nil, fmt.Errorf("%s: %s", slug, detail)
	}
	t.record(name, api.OutcomeOK, "")
	return out, nil
}

// record writes the audit entry of the call. A failure to record is logged;
// it does not undo what the call did (a proposal, once queued, stays).
func (t *tc) record(name, outcome, reason string) {
	if reason != "" {
		t.details["reason"] = reason
	}
	e := api.AuditEntry{At: t.s.now().UTC(), Actor: t.p.Subject, Kind: t.p.Kind, Action: "mcp.tool." + name,
		Target: t.target, Outcome: outcome, Details: t.details}
	if err := t.s.audit.Record(t.ctx, e); err != nil {
		t.s.log.Error("mcp: audit record failed", slog.String("tool", name), slog.String("actor", e.Actor), slog.Any("error", err))
	}
}
