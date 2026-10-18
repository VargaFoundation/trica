package wiring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/audit"
	"github.com/VargaFoundation/trica/internal/auth"
	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/graph"
	"github.com/VargaFoundation/trica/internal/mcp"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/web"
)

// Wiring holds what the components of one server share: the authenticator and
// policy, the audit log, the rate limiter and the dependency graph. They are
// built from the store when the first component mounts (the store does not
// exist before the server starts), then shared.
type Wiring struct {
	cfg     *server.Config
	version string

	once    sync.Once
	core    *core
	coreErr error

	transportAddr atomic.Value // string, the bound address of the transport

	libOnce sync.Once
	lib     *devicetypes.Index // nil when the library did not load
}

// core is the shared state.
type core struct {
	authz   *Authorizer
	auditor *Auditor
	limiter *auth.RateLimiter
	cache   *graph.Cache
	// refusals records what the API and the MCP server cannot: the 401, 403
	// and 429 answered before or around their handlers (#56). It is shared,
	// so that one caller's 429s are sampled once across both.
	refusals func(http.Handler) http.Handler
}

// New returns the wiring for cfg.
func New(cfg *server.Config, version string) *Wiring {
	return &Wiring{cfg: cfg, version: version}
}

// TransportAddr is the address the transport listens on, once it does (empty
// when the transport is disabled or not started yet).
func (w *Wiring) TransportAddr() string {
	a, _ := w.transportAddr.Load().(string)
	return a
}

// Components lists what a server mounts besides health and metrics. Mount
// order matters only in that the catch-all UI is registered on "/", which the
// mux ranks below every more specific pattern.
func (w *Wiring) Components() []server.Component {
	cs := []server.Component{
		{Name: "api", Mount: w.mountAPI},
	}
	if !w.cfg.MCP.Disabled {
		cs = append(cs, server.Component{Name: "mcp", Mount: w.mountMCP, Run: w.runGraphRefresh})
	}
	if !w.cfg.Web.Disabled {
		cs = append(cs, server.Component{Name: "web", Mount: w.mountWeb})
	}
	if !w.cfg.Deduce.Disabled {
		cs = append(cs, server.Component{Name: "deduce", Run: w.runDeduce})
	}
	if w.cfg.Transport.Addr != "" {
		cs = append(cs, server.Component{Name: "transport", Run: w.runTransport})
	}
	if w.cfg.Collect.TargetsFile != "" {
		cs = append(cs, server.Component{Name: "collect", Run: w.runCollect})
	}
	cs = append(cs, server.Component{Name: "devicetypes", Run: w.runDeviceTypes})
	cs = append(cs, server.Component{Name: "audit-anchor", Run: w.runAuditAnchor})
	return cs
}

// shared builds the shared state once.
func (w *Wiring) shared(d server.Deps) (*core, error) {
	w.once.Do(func() { w.core, w.coreErr = w.build(d) })
	return w.core, w.coreErr
}

func (w *Wiring) build(d server.Deps) (*core, error) {
	pol, err := w.policy()
	if err != nil {
		return nil, err
	}
	a := &auth.Authenticator{
		Tokens: &auth.TokenStore{Store: d.Store, Policy: pol},
		Policy: pol,
		Logger: d.Log,
	}
	if o := w.cfg.Auth.OIDC; o != nil {
		a.OIDC, err = startOIDC(d.Log, auth.OIDCConfig{
			Issuer: o.Issuer, Audience: o.Audience, GroupsClaim: o.GroupsClaim, GroupRoles: pol.GroupRoles,
		})
		if err != nil {
			return nil, err
		}
	}
	rl := w.cfg.Auth.RateLimit
	al := &audit.Log{Store: d.Store}
	return &core{
		authz:   &Authorizer{Auth: a, Policy: pol},
		auditor: &Auditor{Log: al},
		limiter: auth.NewRateLimiter(rl.Rate, rl.Burst),
		cache:   graph.NewCache(d.Store, d.Schema),
		refusals: audit.NewMiddleware(al, audit.Options{
			RefusalsOnly: true, RateLimitedEvery: w.cfg.Audit.RateLimitedEvery, Logger: d.Log,
		}),
	}, nil
}

func (w *Wiring) policy() (*auth.Policy, error) { return Policy(w.cfg) }

// Policy is the RBAC policy a server started with cfg enforces: the default
// policy, or the configured file, with the configured group-to-role mappings
// added. A mapping to a role that does not exist is an error: a typo must not
// silently drop or widen access. The administration commands of cmd/trica call
// it too, so that a token is minted against the policy the server will apply.
func Policy(cfg *server.Config) (*auth.Policy, error) {
	pol := auth.DefaultPolicy()
	if f := cfg.Auth.PolicyFile; f != "" {
		fh, err := os.Open(f) //nolint:gosec // the operator names the policy file
		if err != nil {
			return nil, fmt.Errorf("auth.policy_file: %w", err)
		}
		defer fh.Close()
		if pol, err = auth.ParsePolicy(fh); err != nil {
			return nil, fmt.Errorf("auth.policy_file %s: %w", f, err)
		}
	}
	known := pol.RoleNames()
	if pol.GroupRoles == nil {
		pol.GroupRoles = map[string][]string{}
	}
	for group, roles := range cfg.Auth.GroupRoles {
		for _, r := range roles {
			if !slices.Contains(known, r) {
				return nil, fmt.Errorf("auth.group_roles: group %q maps to unknown role %q", group, r)
			}
			if !slices.Contains(pol.GroupRoles[group], r) {
				pol.GroupRoles[group] = append(pol.GroupRoles[group], r)
			}
		}
	}
	return pol, nil
}

func (w *Wiring) mountAPI(mux *http.ServeMux, d server.Deps) error {
	c, err := w.shared(d)
	if err != nil {
		return err
	}
	ls, err := w.liveSource(d)
	if err != nil {
		return err
	}
	srv, err := api.New(api.Config{Engine: d.Engine, Authorizer: c.authz, Auditor: c.auditor, Logger: d.Log, Version: w.version,
		Images: Images{Index: w.library(d)}, Live: ls})
	if err != nil {
		return err
	}
	mux.Handle(api.Prefix+"/", c.refusals(limit(c.limiter, c.authz, srv.Handler())))
	return nil
}

func (w *Wiring) mountMCP(mux *http.ServeMux, d server.Deps) error {
	c, err := w.shared(d)
	if err != nil {
		return err
	}
	// Every commit (a batch, an approved proposal, the absence sweep, the
	// deducer) marks the graph stale; the next graph tool call reloads it.
	prev := d.Engine.OnCommit
	d.Engine.OnCommit = func(r reconcile.Result) {
		if prev != nil {
			prev(r)
		}
		c.cache.Invalidate()
	}
	srv, err := mcp.New(mcp.Config{
		Engine: d.Engine, Authorizer: c.authz, Auditor: c.auditor, Graph: GraphAdapter{Cache: c.cache},
		Logger: d.Log, Version: w.version, DisableLocalhostProtection: w.cfg.MCP.DisableLocalhostProtection,
	})
	if err != nil {
		return err
	}
	h := c.refusals(limit(c.limiter, c.authz, srv.Handler()))
	mux.Handle(mcp.Prefix, h)
	mux.Handle(mcp.Prefix+"/", h)
	return nil
}

// runGraphRefresh marks the graph stale when another replica commits: a commit
// made elsewhere does not reach this one's OnCommit, but its notification on
// reconcile.ChangesChannel does. Every mcp.graph_refresh it also marks it
// stale, in case a notification was missed. The reload itself is lazy (next
// graph tool call), so an idle server pays nothing.
func (w *Wiring) runGraphRefresh(ctx context.Context, d server.Deps) error {
	c, err := w.shared(d)
	if err != nil {
		return err
	}
	listened := make(chan struct{})
	go func() {
		defer close(listened)
		_ = d.Store.Listen(ctx, reconcile.ChangesChannel, c.cache.Invalidate, d.Log)
	}()
	defer func() { <-listened }()
	t := time.NewTicker(w.cfg.MCP.GraphRefresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			c.cache.Invalidate()
		}
	}
}

func (w *Wiring) mountWeb(mux *http.ServeMux, d server.Deps) error {
	var cfg web.Config
	if o := w.cfg.Web.OIDC; o != nil {
		cfg.OIDC = &web.OIDC{Issuer: o.Issuer, ClientID: o.ClientID, Scopes: o.Scopes, Bearer: o.Bearer}
	}
	h, err := web.DefaultWithConfig(cfg)
	if err != nil {
		return err
	}
	if !web.Built {
		d.Log.Warn("the web UI is not embedded in this build (no webui tag): / serves a placeholder page")
	}
	mux.Handle("/", h)
	return nil
}

// logFailure logs an error unless it is a cancellation; it is used by loops that must not stop the server for a recoverable
// failure.
func logFailure(log *slog.Logger, what string, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error(what, "error", err.Error())
	}
}

// startOIDC builds the OIDC verifier and tries to discover its issuer. An issuer that does
// not answer does not stop the server (#57): API tokens keep working and a JWT is answered
// 503 until a later discovery succeeds. A discovery document that contradicts the
// configuration (another issuer, a jwks_uri over http) still stops it: no retry fixes that.
func startOIDC(log *slog.Logger, cfg auth.OIDCConfig) (*auth.Verifier, error) {
	v, err := auth.NewLazyVerifier(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := v.Discover(ctx); err != nil {
		if !errors.Is(err, auth.ErrIssuerUnreachable) {
			return nil, err
		}
		log.Warn("OIDC issuer unreachable at start: JWTs get a 503 until it is discovered, API tokens work",
			"issuer", cfg.Issuer, "error", err.Error())
	}
	return v, nil
}
