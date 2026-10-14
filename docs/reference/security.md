# Security reference: authentication, authorisation, audit

Decisions and their reasons: [ADR 0013](../adr/0013-authentication-and-audit.md). Code:
`internal/auth`, `internal/audit`. Status: these are libraries; no server mounts them yet.

## Mounting order

```go
chain := audit.Middleware(auditLog, logger)(      // outermost: sees every refusal
    auth.Middleware(authenticator)(               // 401 without a valid bearer
        limiter.Middleware(                       // 429 per principal
            mux)))                                // routes wrapped in auth.Require(action)
```

`auth.Require(auth.ActionPropose, scopeFn)` guards a route; `scopeFn` returns the object type
and site the request touches. Without one, a request with no type and no site is refused to any
type- or site-restricted principal. A handler that only knows the object after loading it calls
`auth.Authorize(ctx, action, type, site)`. `audit.Annotate(ctx, key, value)` adds a field to the
entry recorded for the request.

## Credentials

A request carries `Authorization: Bearer <credential>`. The query string is never read.

| Credential | Recognised by | Principal |
|---|---|---|
| API token | starts with `trica_` | `token:<id>` |
| OIDC JWT (ID or access token) | anything else | `user:<sub>` |

A missing, malformed, expired, revoked or unverifiable credential gets `401` with
`WWW-Authenticate: Bearer`; the reason goes to the audit entry, not to the client. If the
identity provider or the database cannot be reached, the answer is `503`, never a pass.

### API tokens

Format: `trica_<id>_<secret>`. `<id>` is the row id, `<secret>` is 256 random bits in unpadded
base64url (43 characters). The server stores only `sha256(secret)` and shows the full value
once, at creation. Treat it like a password: the `trica_` prefix exists so secret scanners can
find a leaked one.

| Property | Meaning |
|---|---|
| scopes | `read` allows `read`; `write` allows `read` and `propose`; `admin` allows all four actions |
| types, sites | optional: the token can only touch these object types and sites; empty = no restriction |
| expiry | optional; the token is refused from that instant |
| revocation | takes effect on the next request |
| `last_used` | refreshed at most once a minute |
| owner roles | the creator's roles when the token was created |

A scope is a ceiling, never an extension. A token is allowed an action only if its scopes allow
it **and** its owner roles grant it. Creating a token whose scope needs an action the creator's
roles cannot perform is refused. Only a user, not a token, can create a token.

### OIDC

| Setting | Meaning |
|---|---|
| `Issuer` | exact `iss`, an https URL; discovery at `<issuer>/.well-known/openid-configuration` |
| `Audience` | accepted `aud` values (at least one) |
| `Algorithms` | subset of `RS256`, `ES256` (both by default); nothing else can be enabled |
| `GroupsClaim` | claim holding the groups, string or array; `groups` by default |
| `GroupRoles` | group to roles, from the policy file's `group_roles` |
| `Leeway` | clock skew tolerated on `exp`, `nbf`, `iat`; 30 s |
| `MinRefreshInterval` | minimum time between JWKS refreshes caused by unknown `kid`s; 30 s |

Required in the token: `kid` header, `sub`, `exp`; `iss` equal to the issuer; `aud` containing
one accepted value. `nbf` and `iat` are checked when present. The JWKS is fetched on first use
and when a token carries an unknown `kid` (key rotation), at most once per
`MinRefreshInterval`; a refresh replaces the cached set.

## Roles and actions

| Action | Meaning |
|---|---|
| `read` | query the inventory, history, discrepancies |
| `propose` | submit a batch; the reconciliation policy applies it or queues it as a proposal |
| `approve` | approve or reject a pending proposal |
| `admin` | administer tokens, schema overlay, policy |

| Default role | Actions |
|---|---|
| `viewer` | `read` |
| `operator` | `read`, `propose` |
| `admin` | `read`, `propose`, `approve`, `admin` |

No role, an unknown role, an unknown action or a nil policy means **deny**.

### Policy file

```yaml
roles:                       # added to, or replacing, the defaults
  site-operator:
    actions: [read, propose]
    types: [device, interface]   # optional restriction
    sites: [paris-1]             # optional restriction
group_roles:                 # OIDC group -> roles
  trica-admins: [admin]
  paris-ops: [site-operator]
```

Unknown fields, unknown actions, a role without actions and a group mapped to an undefined role
are load errors.

## Rate limiting

A token bucket per principal (per peer address for an unauthenticated request): `burst`
requests at once, refilled at `rate` per second. An empty bucket gets `429` with `Retry-After`
in whole seconds. `X-Forwarded-For` is not trusted.

## Audit log

Every mutating request (not GET, HEAD, OPTIONS) and every denied request (401, 403, 429) is
recorded once the response is decided.

| Field | Content |
|---|---|
| `at` | time, UTC, microseconds |
| `actor` | `user:<sub>`, `token:<id>`, or `anonymous` |
| `action` | `http.<METHOD>` |
| `target` | request path (no query string) |
| `outcome` | `success`, `denied` (401/403/429), `failure` (other 4xx/5xx) |
| `request_id` | `X-Request-Id` if it matches `[A-Za-z0-9._-]{1,64}`, else generated; echoed in the response |
| `details` | `method`, `status`, `remote`, `reason` (for refusals), handler annotations |

No secret is stored: the value of any details key containing `password`, `secret`, `token`,
`authorization`, `apikey`, `privatekey`, `credential`, `cookie`, `bearer` or `session` is
replaced by `[REDACTED]`, as is any string value that looks like a trica token, a JWT or a
bearer header. No header is recorded.

### Verification

`hash = sha256(prev_hash || canonical(at, actor, action, target, outcome, request_id, details))`;
the first entry chains from 32 zero bytes. `audit.Log.Verify(ctx)` reads the chain in one
snapshot and returns the number of entries checked, the first broken link (entry id and
reason) if any, and the head (id and hash):

- *content does not match its hash*: the entry was modified;
- *previous hash does not match the preceding entry*: an entry was removed, inserted or
  reordered before this one. A modified entry whose hash was recomputed is reported at the
  entry after it.

Record the head `Verify` returns somewhere the database's writers cannot reach, and compare it
on the next run: that is the only way to notice that the last entries were truncated, or that
the whole tail was rewritten consistently.

The database also refuses UPDATE, DELETE and TRUNCATE on `audit_log`.
