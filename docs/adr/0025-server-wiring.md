# ADR-0025: The server mounts its components; the adapters live in `internal/wiring`

- **Status**: accepted (2026-10)
- **Concerns**: `internal/wiring` (new), `internal/server` (configuration), `internal/api`
  (one field), `cmd/trica`, `charts/trica`. Builds on ADR 0008, 0009, 0013, 0014, 0017, 0020.

## Context

ADR 0014 made `trica server` a lifecycle and a list of components, and said new components
register in `cmd/trica/components.go`. That list was empty: the server served `/healthz`,
`/readyz` and `/metrics`, and nothing else. The REST API, the MCP server, the web UI, the
transport, the deducer and the device type library all existed and were tested, each against a
fake of what it needs. The pieces that join them did not exist: `internal/auth` does not
implement `api.Authorizer`, `internal/audit` does not implement `api.Auditor`, and
`internal/graph` does not implement `mcp.GraphQuerier` (issue #29).

## Decision

**The adapters live in a new package, `internal/wiring`, which only `cmd/trica` imports.** The
dependency points one way: `internal/api` and `internal/mcp` define the interfaces they need,
`internal/auth`, `internal/audit` and `internal/graph` know nothing of them. Putting an adapter
in either side would make one of them import the other (`api` importing `auth`, or `auth`
importing `api`) and the fakes the API and MCP tests rely on would stop being the natural way to
test them. `cmd/trica` is too small a place: the assembly must be testable as a whole, and
`package main` cannot be imported by a test of another package. `wiring.New(cfg, version)
.Components()` returns the list `cmd/trica` hands to `server.New`.

**Authentication fails closed, with no anonymous mode.** `wiring.Authorizer` authenticates the
bearer credential (API token or OIDC JWT, `internal/auth.Authenticator`) and answers every
`Allow` from `auth.Policy`, which already refuses an empty type or site to a principal limited to
types or sites (a grant on `paris-1` does not match "unknown site"). To keep that logic in one
place, `api.Principal` carries an opaque `Credential` (`json:"-"`) holding the verified
`*auth.Principal`; a principal the adapter did not build has none and is allowed nothing. Any
authentication error is an `api.ErrUnauthenticated`: the interface has no way to say "backend
down", so a database failure answers 401, not 503 (known limit).

**One rate limiter for the API and the MCP server**, keyed by principal after authentication,
by peer address before. The middleware authenticates first and leaves the principal in the
context, so a request costs one credential check. Defaults 20 requests per second, burst 60
(`auth.rate_limit`).

**Tokens speak as `api:token-<id>`.** `internal/auth` tokens have no bound source, but the API
expects one for a token to submit a batch. The adapter derives it from the token id: it cannot
collide with a collector or an agent, and the engine's source policies can match `api:*`.
A real per-token source binding is left to a later change.

**Audit.** `wiring.Auditor` appends every `api.Auditor` record to the hash chain
(`internal/audit.Log`); the actor is `<kind>:<subject>` as the audit package defines it, the
outcome is mapped (`ok` to `success`, `error` to `failure`), the time is the log's.

**Graph.** One `graph.Cache` is shared. The engine's `OnCommit` marks it stale (a batch, an
approval, the sweep, the deducer's batch); a ticker (`mcp.graph_refresh`, one minute) also marks
it stale, because a commit made by another replica does not reach this one's `OnCommit`. The
reload is lazy, on the next graph tool call: an idle server does nothing.

**The transport has its own listener, `transport.addr`.** The HTTP listener usually sits behind a
TLS-terminating ingress, which would remove the client certificate the transport authenticates
by; the transport needs TLS terminated by the process. A second port also lets the network
policy, the Service and the firewall treat agents apart from browsers. It is off when
`transport.addr` is empty. The CA directory must be a persistent volume: a CA created on an
emptyDir un-enrols every agent at the next restart, so the chart requires a claim.
The server certificate is issued at start for `transport.hosts` and renewed after two thirds of
`transport.server_cert_ttl` by reopening the listener on the same address (agents spool and
retry, ADR 0008). A transport that cannot start stops the server, like any component (ADR 0014).

**The deducer runs on an interval of our own** (`deduce.interval`, five minutes), not
`Deducer.Loop`: the loop has no metrics, and the singleton rule of ADR 0014 asks for a lock, so
each pass takes a session-level PostgreSQL advisory lock and a replica that does not get it skips
the pass (`trica_deduce_skipped_total`). A failed pass is logged and counted and does not stop the
server: unlike a listener, a deducer that misses a pass leaves the inventory as it was.

**The device type library is non-fatal.** `devicetypes.Sync` runs once at start, after the
migrations, and its error is logged and swallowed: the library enriches the inventory, and a bad
overlay must not keep the API down. It matches the devices that exist at start; a device
discovered later is matched at the next restart (known limit, a periodic run is a later change).

**Configuration.** `internal/server.Config` gains `auth`, `mcp`, `transport`, `deduce` and `web`
sections. Unknown keys are still rejected. No secret is a configuration value: the database URL
is a file reference, the UI is a public client, and the OIDC verifier needs none. An OIDC issuer
that cannot be reached at start stops the server (no silent fallback to tokens only), as does a
group mapped to a role the policy does not define.

**The web UI is mounted at `/`** with the browser OIDC settings from `web.oidc` (the issuer
defaults to `auth.oidc.issuer`). A build without the `webui` tag serves the embedded placeholder
page and logs a warning at start.

## Consequences

- New components register in `internal/wiring` (`Components()`), not in `cmd/trica`; they still
  do not open their own store.
- A deployment had no way to mint the first API token or an enrolment token (`TokenStore` needs a
  user principal, `NewEnrolmentToken` is a Go call). `trica token` and `trica enrol` now do, run
  next to the server and against the policy `wiring.Policy` builds (STATUS row 36).
- Audit entries are written by the API and MCP handlers for mutating or denied calls; a 401 or a
  429 never reaches them, and the chain is still not anchored outside the database (ADR 0013).
- The graph of a replica is at most `mcp.graph_refresh` behind a commit made by another one.
- Renewing the transport certificate closes the listener for a moment; clients retry.
