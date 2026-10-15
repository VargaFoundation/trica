# ADR-0013: OIDC and scoped tokens for authentication, RBAC with deny by default, a hash-chained audit log

- **Status**: accepted (2026-10)
- **Concerns**: `internal/auth`, `internal/audit`, the future REST API and MCP server

## Context

Release 1 needs people (the UI, `curl`) and programs (MCP clients, scripts, CI) to call the
API, with three requirements from the specification: single sign-on through the organisation's
identity provider, tokens that can be scoped down and revoked, and a tamper-evident record of
who changed what (ADR 0009: an MCP write is a proposal, so the audit log must say who made it
and who approved it). The API and MCP server are separate stories; this one delivers the
libraries they mount as `net/http` middleware.

## Decision

### OIDC: our own verifier on `go-jose/v4`, not `go-oidc`

- Bearer ID tokens and access tokens are JWTs verified against the issuer's JWKS, discovered at
  `<issuer>/.well-known/openid-configuration`. Checked: algorithm (allow-list, RS256 and ES256
  only), signature, `iss` (exact), `aud` (any of the configured audiences), `exp` (required),
  `nbf`, `iat`, `sub` (required). The configured groups claim is mapped to roles.
- `coreos/go-oidc` would also do this, but: it re-fetches the JWKS on every unknown `kid` with no
  cool-down (a stream of forged `kid`s becomes a stream of requests to the identity provider),
  it does not require `exp`, it checks no `nbf`, and its audience model is a single client id.
  The code that closes those gaps is about as long as the verifier itself. The verifier is ~250
  lines on `go-jose/v4`, which `go-oidc` depends on anyway, so no heavier a dependency tree.
- JWKS: cached in memory; an unknown `kid` triggers one refresh, at most one per
  `MinRefreshInterval` (30 s by default), concurrent callers wait for the refresh in flight.
  A refresh replaces the set, so a key the issuer withdrew stops working. A token without a
  `kid` is refused. Issuer and `jwks_uri` must be https.
- The algorithm allow-list is applied while parsing, before any key is selected: `none` and
  the HMAC family never reach a signature check, and configuration cannot enable them.
- The user principal is `user:<sub>`; roles come from `group_roles` (group to roles) of the
  policy file. A user with no mapped group has no role and therefore no access.

### API tokens: `trica_<id>_<secret>`, SHA-256 of 256 random bits

- The secret is 32 bytes from `crypto/rand`, base64url. Only `sha256(secret)` is stored. A slow
  hash (argon2id) defends low-entropy secrets against offline guessing; a 256-bit random secret
  has nothing to guess, and a slow hash would be paid on every request. The `trica_` prefix
  makes a leaked token recognisable by secret scanners and lets the middleware route a bearer
  value without parsing it. The id in the token is the row id: lookup is one primary-key read,
  then a constant-time comparison.
- A token has scopes (`read`, `write`, `admin`), optional restrictions to object types and
  sites, an expiry, a revocation time and `last_used` (refreshed at most once a minute).
  Revocation and expiry are checked on every request, so they take effect immediately.
- Only a user can create a token (a token minting tokens would outlive its own revocation).
  The token stores a snapshot of its owner's roles at creation.

### RBAC: deny by default, tokens only narrow

- `Allow(principal, action, objectType, site)`, actions `read`, `propose`, `approve`, `admin`;
  roles `viewer` (read), `operator` (read, propose), `admin` (all). `write` never means a direct
  write: it means `propose` (ADR 0005, 0009). Approving proposals is admin-only by default;
  the YAML policy can give it to another role.
- A policy file is YAML, layered on the defaults (a role of the same name replaces, a new name
  adds); unknown fields, actions and group-to-unknown-role mappings are errors.
- A token passes twice: its scopes and restrictions must allow the request, **and** the roles
  snapshot must grant it. The scope is a ceiling that can never exceed the owner's role,
  both at creation (refused with `ErrScopeExceedsRole`) and at use (so a row edited by hand,
  or an owner whose role is lower than the scope, is clamped).
- A restricted role or token asked about "no type" or "no site" is refused: a handler must
  name what it touches.

### Audit: append-only, hash-chained, serialised

- Entry: time, actor (`user:<sub>`, `token:<id>`, `anonymous`), action, target, outcome
  (`success`, `denied`, `failure`), request id, details JSON. `hash = sha256(prev_hash ||
  canonical(entry))`, the canonical form being a fixed-field-order JSON object; the first
  entry chains from 32 zero bytes. Details are decoded and re-encoded before hashing so the
  hash is the same after PostgreSQL's jsonb has reordered keys.
- Appends take a transaction-scoped advisory lock (`pg_advisory_xact_lock`, a key distinct
  from the reconciliation engine's), read the head, then insert; concurrent appenders queue
  and the chain cannot fork. `Verify` walks the chain in one snapshot and reports the first
  broken link: an entry whose content does not match its hash, or whose previous hash is not
  its predecessor's hash (removed, inserted or reordered entry).
- The table is append-only at the database level: triggers refuse UPDATE, DELETE and TRUNCATE.
  The chain is what detects a role that disables them.
- Secrets are redacted before storage: the value of any key whose normalised name contains
  `password`, `secret`, `token`, `authorization`, `apikey`, `privatekey`, `credential`,
  `cookie`, `bearer`, `session`, and any string value that looks like a trica token, a JWT or
  a bearer header. The audit middleware records the path only, never the query string or any
  header.

### Middleware

`audit.Middleware` (outermost) → `auth.Middleware` → `RateLimiter.Middleware` →
`auth.Require(action)` → handler. The audit middleware records every mutating request and every
denied one (401, 403, 429) after the handler has answered; the inner middleware tell it who the
caller was, and why they were refused, through a slot in the context. The rate limiter is a
token bucket per principal (per peer address before authentication), answering 429 with
`Retry-After`. A credential that cannot be checked because the identity provider or database is
down gets a 503, never a pass.

## Consequences

- One new direct dependency, `github.com/go-jose/go-jose/v4`.
- Migration `00004_auth.sql` adds `audit_log.outcome` and `request_id`, the append-only
  triggers, and `api_tokens.types`, `sites`, `owner_roles` plus a CHECK on scope values.
- The hash chain is not keyed: someone who can write the table **and** reimplement the hash can
  rewrite the tail consistently. Detecting that, or the truncation of the last entries, needs
  the head recorded somewhere the table's writers cannot reach (`Result.Head`): the server logs
  it and publishes it as a metric ([ADR 0034](0034-refusals-audit-and-head-anchor.md)); a
  signed head, and rotating or sealing the chain, are later stories.
- The audit entry of a request is written after the response is decided, so a crash between
  the two loses it, and an audit write failure is logged, not surfaced to the client.
- Token roles are a snapshot: demoting a user does not demote tokens they already created
  until those are revoked.
