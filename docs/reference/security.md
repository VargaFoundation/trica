# Security reference: authentication, authorisation, audit

Decisions and their reasons: [ADR 0013](../adr/0013-authentication-and-audit.md). Code:
`internal/auth`, `internal/audit`, assembled by `internal/wiring` (ADR 0025, ADR 0034).

## Mounting order

The server mounts, on `/api/v1/` and on `/mcp`:

```go
audit.NewMiddleware(auditLog, audit.Options{RefusalsOnly: true})( // outermost: 401, 403, 429
    limit(limiter, authorizer,                                      // authenticates, then 429 per principal
        handler))                                                   // 401/403 itself; records its own calls
```

The API and the MCP server authenticate, authorise and record their calls themselves, through
`api.Authorizer` and `api.Auditor`, with the domain action (`proposal.approve`,
`mcp.tool.search`). The middleware records what they cannot see: the refusals answered in
front of them, and a 401 or a read's 403 they do not record. A request whose handler recorded
an entry gets none from the middleware.

A server built from the libraries alone mounts them this way:

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

**The site of an object** (what a grant limited to some sites is matched against) is decided in one
place, `internal/place`: the site of its parent; for a device, the site of the rack it is mounted
in; for a device in no rack, the site of the site, building or room a person placed it in
(`located_in`, [ADR 0058](../adr/0058-placing-devices-outside-racks.md)). A rack wins over a place.
An object with no site, or whose places lie in two different sites, has none, and a site-limited
grant does not match it. The engine surfaces the disagreement as a `location_conflict` discrepancy ([ADR 0061](../adr/0061-location-conflict-discrepancy.md)); a site-limited reader does not see a `sites` or `racks` one, as the device has no site for them.

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
| source | optional: the source its batches are attributed to (`kind:name`, not `user:` or `agent:`); default `api:token-<id>` ([ADR 0028](../adr/0028-token-bound-source.md)) |

A scope is a ceiling, never an extension. A token is allowed an action only if its scopes allow
it **and** its owner roles grant it. Creating a token whose scope needs an action the creator's
roles cannot perform is refused. Only a user, not a token, can create a token.

#### Minting the first token

A fresh deployment has no credential, and nobody to sign in through OIDC yet: the first token
comes from the command line, run next to the server (`kubectl exec`, `docker compose exec`, or a
Job; see [getting started](../getting-started.md#first-credentials)).

```bash
trica token create --name claude-code --owner alice --roles admin --scopes write \
  [--types device,rack] [--sites paris-1] [--expires 720h]   # the token alone on stdout
trica token list                                            # no secret, no hash
trica token revoke --id 7                                   # marks revoked_at, keeps the row
```

- **Who may run it.** Whoever can run `trica` against the database. The command does not
  authenticate anyone: it reads the database URL (`TRICA_DATABASE_URL` or `database.url_file`),
  and whoever holds that URL, or can `kubectl exec` into the pod, can already read and change
  every table, tokens included. Protect that access (cluster RBAC, the Secret holding the URL) as
  you would the database itself. The audit log does not record these commands; the token row
  records its owner and creation time.
- **The owner is a name you give.** `--owner` is a subject (stored as `user:<owner>`, the same
  form an OIDC user has), `--roles` its roles. The roles must exist in the policy the server
  loads (`auth.policy_file` and `auth.group_roles`, the same code path): an unknown role is
  refused. The ceiling above applies: `--scopes admin` with roles that cannot `approve` and
  `admin` is refused, nothing is stored.
- **The secret.** Printed once, alone, on stdout (metadata goes to stderr), so
  `TOKEN=$(trica token create ...)` works. It is never logged and never in an error. Only its
  SHA-256 is stored: a lost token cannot be recovered, revoke it and create another.

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
`MinRefreshInterval`; a refresh replaces the cached set. Discovery runs at start; if the issuer
does not answer, it is retried when a JWT arrives, at most once per `MinRefreshInterval`, and the
JWT gets a 503 until it succeeds ([ADR 0031](../adr/0031-oidc-issuer-down-at-start.md)).

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

## Themes

A theme is configuration, not user content, but it is validated as if it were not: archive and
file limits counted on bytes read, path rules, no symlink, content checked by type (magic bytes,
an SVG sanitiser, a CSS scanner that refuses `@import`, remote `url()` and escapes), and the
contrast of its palette. `/theme/...` is public (the sign-in page is themed) and serves only files
a validated theme references, under a sandboxing policy; the page's Content-Security-Policy is
unchanged. A theme that fails never stops the server. See [themes](themes.md), ADR 0054.

Choosing and uploading a theme (`/api/v1/ui/themes`) needs the `admin` action and is audited. An
upload is a raw zip bounded at 2 MiB by its own limit, validated before it is stored; a remote
theme (`web.theme.url`) is `https` only, pinned by a mandatory SHA-256 checked before parsing,
fetched once with a timeout and a size limit, and never followed to a non-`https` address.
`web.theme.locked` keeps the console's page from overriding the deployment's choice.

## Audit log

Every mutating request (not GET, HEAD, OPTIONS) and every denied request (401, 403, 429) is
recorded once the response is decided; with `RefusalsOnly` (what the server mounts), the
denied ones only, the handlers recording their calls. A request whose handler appended an
entry with the request's context gets no second one. A 429 is recorded once per caller and
`audit.rate_limited_every` (one minute); the next entry of that caller counts the others in
`details.suppressed`, and a count no entry will carry is logged when the caller is forgotten.

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

The server does it every `audit.anchor_interval` (five minutes, every replica): it logs
`audit chain head` with `head_id` and `head_hash`, and publishes `trica_audit_head_id`. An
entry anchored at the previous read that is gone or has another hash (entries appended after a
truncation do not hide it) is logged as an error and counted in
`trica_audit_head_regressions_total`. Alert on that counter, or on the
gauge decreasing; keep the logs where the database's administrators cannot edit them. The
anchor does not protect against whoever controls the server's process
([ADR 0034](../adr/0034-refusals-audit-and-head-anchor.md)).

The database also refuses UPDATE, DELETE and TRUNCATE on `audit_log`.
