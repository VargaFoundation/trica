// Package api is the REST API of trica: JSON over net/http, under /api/v1.
//
// Reads go through the store and internal/search; every write is an sdk.Batch
// submitted to the reconciliation engine, which alone decides what changes
// (ADR 0005). The OpenAPI document served at /api/v1/openapi.json is generated
// at each request from the route table below and the schema registry, so a
// custom type appears in it as soon as the registry has it (ADR 0003).
//
// Authentication, RBAC and audit are interfaces (Authorizer, Auditor): the
// server refuses to be built without them.
package api

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/live"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
)

// Prefix is where the API is mounted.
const Prefix = "/api/v1"

// maxBody bounds a request body; a batch of a few thousand objects fits.
const maxBody = 8 << 20

// Config builds a Server.
type Config struct {
	// Engine is the reconciliation engine; it carries the store, the schema
	// registry (Engine.Schema, read at each request) and the policy.
	Engine *reconcile.Engine
	// Authorizer authenticates callers and answers rights questions. Required.
	Authorizer Authorizer
	// Auditor records mutating calls. Required.
	Auditor Auditor
	// Logger receives access and error logs (JSON in production). Default slog.Default().
	Logger *slog.Logger
	// Version is shown in the OpenAPI document.
	Version string
	// Now is the clock for default time ranges; tests replace it.
	Now func() time.Time
	// Images serves the face images of the device-type library. Nil: no
	// model has an image.
	Images DeviceTypeImages
	// Library answers which models a device may be (its chassis variants,
	// ADR 0048). Nil: GET /objects/{id}/device-type-candidates lists none.
	Library DeviceTypeLibrary
	// Live serves the live topology overlay (ADR 0041); nil when the server
	// has no live block, and GET /topology/live answers enabled: false.
	Live LiveSource
	// Collectors is the collector catalogue and the targets this server runs
	// (ADR 0055). Nil: the registry alone, with no target.
	Collectors CollectorSource
	// Graph is the shared inventory graph the topology map reads (ADR 0051),
	// kept current by the reconciliation engine's commits. Nil: the map loads
	// a snapshot on each request.
	Graph *graph.Cache
	// MapMaxStale is how long after a commit the map may still be served from
	// the previous graph (default 10 s): bursts of commits cost one reload.
	MapMaxStale time.Duration
	// Themes administers the console's theme (ADR 0054). Nil: the endpoints
	// answer 404.
	Themes ThemeAdmin
}

// LiveSource is the live overlay: a shared, cached measurement set.
type LiveSource interface {
	Snapshot(ctx context.Context) *live.Snapshot
}

// Server is the REST API.
type Server struct {
	eng        *reconcile.Engine
	st         *store.Store
	searcher   *search.Searcher
	auth       Authorizer
	audit      Auditor
	log        *slog.Logger
	version    string
	now        func() time.Time
	images     DeviceTypeImages
	lib        DeviceTypeLibrary
	routes     []route
	live       LiveSource
	collectors CollectorSource
	graph      *graph.Cache
	mapStale   time.Duration
	themes     ThemeAdmin
}

// New builds a Server. It fails without an engine, an Authorizer and an
// Auditor: there is no anonymous, unaudited mode.
func New(cfg Config) (*Server, error) {
	switch {
	case cfg.Engine == nil || cfg.Engine.Store == nil || cfg.Engine.Schema == nil:
		return nil, errors.New("api: an engine with a store and a schema registry is required")
	case cfg.Authorizer == nil:
		return nil, errors.New("api: an Authorizer is required (use StaticAuthorizer for tests only)")
	case cfg.Auditor == nil:
		return nil, errors.New("api: an Auditor is required")
	}
	s := &Server{
		eng: cfg.Engine, st: cfg.Engine.Store, searcher: &search.Searcher{Store: cfg.Engine.Store},
		auth: cfg.Authorizer, audit: cfg.Auditor, log: cfg.Logger, version: cfg.Version, now: cfg.Now,
		images: cfg.Images, lib: cfg.Library,
		live: cfg.Live, collectors: cfg.Collectors, graph: cfg.Graph, mapStale: cmp.Or(cfg.MapMaxStale, mapMaxStale), themes: cfg.Themes,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.version == "" {
		s.version = "dev"
	}
	s.routes = s.routeTable()
	return s, nil
}

func (s *Server) registry() *schema.Registry { return s.eng.Schema() }

// Handler returns the HTTP handler to mount (it serves paths under Prefix).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes {
		mux.Handle(rt.Method+" "+Prefix+rt.Path, s.wrap(rt))
	}
	// The mux answers a wrong method with plain text, and a method-less
	// fallback per path would collide with a wildcard sibling (a literal
	// /themes/active beside /themes/{sha256}); so the catch-all matches the
	// route templates itself and answers in problem+json.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var methods []string
		for _, rt := range s.routes {
			if pathMatches(Prefix+rt.Path, r.URL.Path) {
				methods = append(methods, rt.Method)
			}
		}
		if len(methods) == 0 {
			writeProblem(w, r, notFound("no such endpoint: %s", r.URL.Path))
			return
		}
		allow := strings.Join(methods, ", ")
		w.Header().Set("Allow", allow)
		writeProblem(w, r, newErr(http.StatusMethodNotAllowed, "method-not-allowed", "%s is not allowed here; allowed: %s", r.Method, allow))
	})
	return s.middleware(mux)
}

// statusWriter records the status for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		sw.Header().Set("Cache-Control", "no-store")
		sw.Header().Set("X-Content-Type-Options", "nosniff")
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("api: panic", slog.String("path", r.URL.Path), slog.Any("panic", rec))
				if sw.status == http.StatusOK {
					writeProblem(sw, r, newErr(http.StatusInternalServerError, "internal", "an internal error occurred"))
				}
			}
			// The query string is not logged: it can hold search text.
			s.log.Info("api: request", slog.String("method", r.Method), slog.String("path", r.URL.Path),
				slog.Int("status", sw.status), slog.Int64("duration_ms", time.Since(start).Milliseconds()))
		}()
		next.ServeHTTP(sw, r)
	})
}

// call is one authenticated request.
type call struct {
	s *Server
	w http.ResponseWriter
	r *http.Request
	p Principal

	// Set by mutating handlers for the audit record.
	auditAction  string
	auditTarget  string
	auditDetails map[string]any
}

func (c *call) ctx() context.Context { return c.r.Context() }

// audited names the target and details of a mutating call.
func (c *call) audited(action, target string, details map[string]any) {
	c.auditAction, c.auditTarget, c.auditDetails = action, target, details
}

func (c *call) json(status int, v any) error { return writeJSON(c.w, status, v) }

type handler func(c *call) error

// wrap authenticates, gates on the route's action, runs the handler, answers
// errors as problems and audits mutating calls: in one place, so that a
// handler cannot forget to.
func (s *Server) wrap(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &call{s: s, w: w, r: r, auditAction: rt.Audit}
		if rt.Action != "" {
			p, err := s.auth.Principal(r)
			if errors.Is(err, ErrAuthUnavailable) {
				s.log.Error("api: authentication unavailable", slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Any("error", err))
				w.Header().Set("Retry-After", AuthRetryAfter)
				writeProblem(w, r, newErr(http.StatusServiceUnavailable, "auth-unavailable", "the credential could not be checked; retry later"))
				return
			}
			if err != nil {
				writeProblem(w, r, newErr(http.StatusUnauthorized, "unauthenticated", "authentication is required"))
				return
			}
			c.p = p
			if !s.auth.Allow(p, rt.Action, "", "") {
				if rt.Mutating {
					s.recordAudit(c, rt, OutcomeDenied, http.StatusForbidden)
				}
				writeProblem(w, r, forbidden("the %q action is not granted", rt.Action))
				return
			}
		}
		if rt.Body != nil || len(rt.BodyBinary) > 0 {
			limit := int64(maxBody)
			if rt.MaxBody > 0 {
				limit = rt.MaxBody
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		err := rt.H(c)
		if err == nil {
			if rt.Mutating {
				s.recordAudit(c, rt, OutcomeOK, 0)
			}
			return
		}
		he := s.fail(w, r, err)
		if rt.Mutating {
			outcome := OutcomeError
			if he.status == http.StatusForbidden {
				outcome = OutcomeDenied
			}
			s.recordAudit(c, rt, outcome, he.status)
		}
	})
}

func (s *Server) recordAudit(c *call, rt route, outcome string, status int) {
	e := AuditEntry{At: s.now().UTC(), Actor: c.p.Subject, Kind: c.p.Kind, Action: c.auditAction, Target: c.auditTarget,
		Outcome: outcome, Details: map[string]any{"method": rt.Method, "path": c.r.URL.Path}}
	for k, v := range c.auditDetails {
		e.Details[k] = v
	}
	if status != 0 {
		e.Details["status"] = status
	}
	if e.Action == "" {
		e.Action = rt.ID
	}
	if e.Target == "" {
		e.Target = c.r.URL.Path
	}
	if err := s.audit.Record(c.r.Context(), e); err != nil {
		s.log.Error("api: audit record failed", slog.String("action", e.Action), slog.String("actor", e.Actor), slog.Any("error", err))
	}
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

func parseUUID(s, what string) (string, error) {
	if !uuidRe.MatchString(s) {
		return "", badRequest("%s must be a UUID", what)
	}
	return strings.ToLower(s), nil
}

func parseTime(v, what string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, badRequest("%s must be an RFC 3339 time (2026-03-03T10:00:00Z) or a date (2026-03-03)", what)
}

// optTime parses an optional time query parameter.
func optTime(c *call, name string) (*time.Time, error) {
	v := c.r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	t, err := parseTime(v, name)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// pathMatches reports whether a request path fits a route template, where a
// {name} segment matches any one non-empty segment.
func pathMatches(tmpl, path string) bool {
	ts, ps := strings.Split(tmpl, "/"), strings.Split(path, "/")
	if len(ts) != len(ps) {
		return false
	}
	for i, t := range ts {
		if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
			if ps[i] == "" {
				return false
			}
			continue
		}
		if t != ps[i] {
			return false
		}
	}
	return true
}
