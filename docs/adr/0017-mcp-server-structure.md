# ADR-0017: MCP server structure

- **Status**: accepted (2026-10)
- **Concerns**: `internal/mcp`, `internal/api`

## Context

ADR 0009 says MCP is read-only by default and that writes are proposals. It does not say how
the server is built: how a call is authenticated, how it shares rules with the REST API, how it
reaches the graph (a separate PR) and how it is kept from writing. These choices are structural
because every later tool follows them.

## Decision

- **Library, two transports.** `mcp.New(Config)` returns a `*Server`; `Handler()` serves
  streamable HTTP under `/mcp`, `ServeStdio` serves one fixed local principal. It is not
  mounted in a binary yet, like `internal/api`.
- **Stateless HTTP.** Every request is authenticated afresh by the REST API's `Authorizer` (a
  bearer credential) and served by a server built for that principal's rights. No session holds
  rights between calls, so a revoked credential stops working at once and nothing leaks
  between callers. Two servers exist, read-only and read-write, chosen by `Allow(propose)`: a
  credential without the right does not see the write tool at all (ADR 0009), and the right is
  checked again at the call.
- **One set of rules: `api.Guard`.** The REST handlers and the MCP tools both go through a facade
  in `internal/api` (type and site authorization, secret redaction and refusal, pagination
  bounds, relation and discrepancy visibility, batch checks). The REST code was refactored onto
  it with its tests unchanged; the MCP tools cannot drift from it. Errors are the same slugs,
  rendered as `<slug>: <detail>` in the tool result so that the assistant can correct itself.
  An object that exists but is not readable is `forbidden`, not `not-found`, as in REST.
- **Every call is audited** (`mcp.tool.<name>`), refused and failed ones too. Arguments are
  kept, except those of `propose_change` (counts and ids only: the content is in the proposal).
  A panic in a tool becomes an error result, not a dead server.
- **The graph is an interface.** `internal/graph` is another PR. The server depends on
  `GraphQuerier{Impact, Path}` and on result types that mirror those of the graph field for
  field. `*graph.Cache` cannot satisfy it directly (`Get` returns a concrete snapshot), so the
  wiring PR writes a short adapter (JSON round trip; sketch in `docs/reference/mcp.md`). Tools
  are tested with a fake; the graph tools are offered only when a graph is configured. They fail
  closed: an impact or a path through an object the caller cannot read is refused, never shown
  in part.
- **`propose_change` calls `Engine.Propose`, not `Submit`.** ADR 0009 words it as "the policy
  turns the batch into a proposal". That is true with the default policy and would stop being
  true with a policy that forgot `mcp:*`, which would let a model write. The tool therefore
  never asks the policy: it can only queue. The default policy still lists `mcp:*` so that the
  audit trail and the engine agree. This is a stricter reading of 0009, not a change to it.
- **The caller does not choose provenance.** The source is `mcp:<name>` (a token bound to an
  `mcp:` source keeps it; any other principal speaks as `mcp:<subject>`), the scope is always
  `partial` (a proposal withdraws nothing by omission), the confidence `declared`, the
  observation time now.
- **The propose input has its own types.** The JSON schema generator of the SDK rejects cyclic
  types and `sdk.Endpoint` is one (a parent endpoint). The tool declares flat input types and
  converts them to an `sdk.Batch`. Relation ends are checked too: the batch check covers the
  objects of the batch, a relation also touches the objects at its ends, so their types must be
  proposable. (The REST batch check does not look at them: filed as a finding.)
- **Versions are compared in Go**, number by number with zero padding (`4.20` equals `4.20.0`),
  so that a compliance answer does not depend on the textual order the database would give. TQL
  version comparison in `internal/search` is unchanged and can differ on such edge cases.
- **Forecasts are one generic tool, not a disk tool.** `forecast_threshold` takes any numeric
  attribute, a threshold, a direction (`rising` for "used" values such as `disk.wear_pct`,
  `falling` for "left" values such as `disk.spare_pct`) and a horizon, over the same selection and
  the same `Guard` as `filter_objects`. It fits a least-squares line through the attribute's
  `attr_history` (current value included) and reports who crosses in time. Choices that shape its
  answers: every object scanned is counted in exactly one class (will cross, beyond horizon, no
  trend, already crossed, insufficient history), so nothing is dropped silently; `already_crossed`
  is decided from the current value before any history is read; fewer than 2 points, or points
  under a day apart, are `insufficient_history` rather than a guess; a crossing the line puts in
  the past while the value has not reached the threshold is reported as now. `attr_history`
  records changes, so an unchanged value is one point: the tool says so instead of inventing a
  series. The scan has its own bound (5,000 objects per call, paged) and says `scan_truncated`.
  The model is linear on purpose: a richer one is a later decision, not a hidden default. The
  specification places SSD wear in lot 2; the tool answers the question early, on stored history.
- **A selector that matches nothing is said.** The `version_policy` selector is a search query
  (`role:switch`); a mistyped or non-TQL one governs no object and would read as "everything is
  compliant". The summary carries a warning in that case.
- **Site-limited callers read by id.** A grant limited to some sites cannot list or search (the
  counts and the order of a page would reveal other sites); it reads the objects it can see when
  it designates them by id. The same rule as the REST API.

## Consequences

- A new tool is a handler on `tc` that goes through `Guard`; the dispatcher gives it the action
  gate, the audit record and the error rendering. A tool that bypasses `Guard` is a review
  finding, and the authorization tests list the tools.
- The write surface of the whole MCP server is one tool, one engine call, and its test checks
  the inventory is unchanged.
- The graph adapter is untested until the graph PR merges; the contract tests use a canned
  graph.
- Resources, prompts and a rate limit are not part of this decision and are not built.
