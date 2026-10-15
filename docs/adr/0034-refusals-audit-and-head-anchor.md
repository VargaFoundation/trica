# ADR-0034: The server audits refusals in front of its handlers and anchors the chain's head

- **Status**: accepted (2026-10)
- **Concerns**: `internal/audit` (`NewMiddleware`, `Options`, `Log.Head`), `internal/wiring`
  (`limit`, `runAuditAnchor`), `internal/auth` (`WithPrincipal`, `RecordReason`),
  `internal/server` (`audit` section). Closes issue #56; completes ADR 0013 and ADR 0025.

## Context

ADR 0013 built an audit middleware that records every mutating and every denied request, and
said that truncating the end of the chain is only detectable against a head recorded outside
the table, "a later story". The server assembly (ADR 0025) did not mount that middleware: the
API and the MCP server record their own calls through `api.Auditor`, with the domain action
(`proposal.approve`, `mcp.tool.search`) the middleware cannot know. What they cannot see went
unrecorded: a 401 (no credential, a bad one), a 403 on a read, and every 429 of the rate
limiter in front of them. A credential-guessing run or a scraping client left no trace.

Mounting the middleware as it was would record each mutating call twice, once by the handler
and once by the middleware. And a refused request costs one chained write, serialised by an
advisory lock: a flood of 429s would become a flood of writes, the rate limiter's purpose
turned against the database.

## Decision

1. **The server mounts `audit.NewMiddleware` outermost on `/api/v1/` and `/mcp`, in
   `RefusalsOnly` mode**: it records 401, 403 and 429 only. The handlers keep recording their
   calls; the middleware adds the refusals around them.
2. **One request, one entry.** `Log.Append` called with a request's context tells the
   middleware watching it that the request has its entry; the middleware then records nothing.
   A mutating call refused with a 403 by the API is recorded once, by the API, with its domain
   action.
3. **A 429 is sampled**: one entry per caller (the principal, else the peer address) per
   `audit.rate_limited_every` (one minute); the next entry of that caller carries the number
   left out in `details.suppressed`. A caller whose period ends with a count no entry carried
   has it logged (`rate-limited requests not recorded in the audit log`). 401s are not sampled:
   they are bounded by the rate limiter, which counts unauthenticated requests per address.
4. **The refusal names who was refused and why.** The rate limiter's authentication
   (`wiring.limit`) gives the principal to the audit slot (`auth.WithPrincipal` does it for
   every caller) and records why a credential did not authenticate (`auth.RecordReason`). The
   reason stays in the audit log; the client sees a plain 401.
5. **The head of the chain is anchored outside the database.** Every `audit.anchor_interval`
   (five minutes), each replica reads the head (`Log.Head`), logs it
   (`audit chain head`, `head_id`, `head_hash`) and publishes `trica_audit_head_id`. The log
   pipeline and the metrics store keep both where the table's writers cannot reach. At each read
   the replica also checks that the entry it anchored the previous time is still there with
   the same hash (`Log.HashOf`): the head alone misses a truncation followed by new entries,
   since ids are never reused. An anchored entry gone or changed is logged as an error and
   counted in `trica_audit_head_regressions_total`.

## Consequences

- A truncation of anchored entries is visible on the replicas that were running (an error log
  line, a counter), and to any later reader who compares a logged `head_id` and `head_hash`
  with the table, or sees `trica_audit_head_id` drop. Entries appended and removed between
  two reads were never anchored: the window is `audit.anchor_interval`. A rewrite of the whole tail with a consistent chain is visible by comparing a logged
  `head_hash` with the entry of that id. An alert on the counter, or on the gauge decreasing,
  is the operator's to set: none ships with the chart.
- Nothing proves the anchor cannot be suppressed: whoever controls the server's process also
  controls what it logs. The anchor protects against a database writer, not against the server.
  A signed head, or a head pushed to an object store with retention lock, is not done.
- A restart forgets the previous head: the in-process comparison starts again; the metrics
  history does not.
- The transport (`internal/transport`) answers its own 401 and 403 to agents and collectors on
  another port: they are not in the audit log. Nor are requests to `/` (the UI, no
  authentication) and the health and metrics endpoints.
- A 403 on a read inside a handler (a site-scoped principal asking for another site's object)
  is recorded by the middleware with the HTTP status, not a domain action.
- Under a sustained flood from many addresses, the sampler holds one key per address for a
  period: memory grows with the number of distinct callers in one minute, then is swept.
