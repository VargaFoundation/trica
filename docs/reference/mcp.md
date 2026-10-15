# MCP server

`internal/mcp` serves the inventory to AI assistants over the Model Context Protocol
(`github.com/modelcontextprotocol/go-sdk` v1.8.0). It is read-only by default; the only way to
write is `propose_change`, which queues a proposal that a person approves (ADR 0009). The
structural choices are in ADR 0017.

The `trica server` mounts it at `/mcp` (streamable HTTP), behind the same bearer authentication,
rate limit and audit log as the REST API, with the graph adapter below
(`internal/wiring`, [ADR 0025](../adr/0025-server-wiring.md)); `mcp.disabled` leaves it out
([configuration](configuration.md)). `mcp.New(Config)` returns a `*Server`, which is what an
embedding program uses.

```go
srv, err := mcp.New(mcp.Config{
    Engine:     eng,        // the reconciliation engine: reads its store, proposes through it
    Authorizer: authorizer, // the REST API's api.Authorizer
    Auditor:    auditor,    // the REST API's api.Auditor
    Graph:      graph,      // optional mcp.GraphQuerier (see below)
    Version:    "1.0.0",
})
mux.Handle(mcp.Prefix+"/", srv.Handler())      // streamable HTTP under /mcp
err = srv.ServeStdio(ctx, api.Principal{...})  // stdio, one fixed local principal
```

## Tools

Every list answer starts with a counted summary (`total`, `by_type`, `returned`, `truncated`,
`next_offset`) and then holds one page (`limit` 1 to 1000, default 50; `offset`). Objects are
designated by id, name, address, serial, or `device/port` (`sw-a1/Ethernet2`); a name that
matches several objects is refused with the candidates, never guessed. Attributes that look like
secrets (password, token, community, private key, pre-shared key...) are never returned and
cannot be queried, sorted on or compared.

| Tool | Action | Answers | One call for |
|---|---|---|---|
| `search` | read | Free text (name, address in any notation, MAC, serial) or TQL | "Find everything about 192.0.2.13" |
| `rank_filter` | read | Top-N objects by an attribute, with filters | "The 10 servers that consume the most" |
| `forecast_threshold` | read | Objects whose numeric attribute, extrapolated by a straight line through its history, reaches a threshold within a horizon | "SSDs that will reach 80 % wear within six months" |
| `version_compliance` | read | Versions against a target or a `version_policy`: counts first, then who is behind | "Switches behind the firmware reference" |
| `impact_analysis` | read | What fails when objects fail (needs a graph) | "If I cut sw-core-01, which applications go down?" |
| `network_path` | read | The chain of relations between two objects (needs a graph) | "How are the API and its database connected?" |
| `filter_objects` | read | Objects meeting attribute conditions (eq, ne, gt, ge, lt, le, contains, within, in, exists, missing) | "Reversed airflow" |
| `list_objects` | read | One type, stable order | "All racks" |
| `get_object` | read | One object now, or `at` a date | |
| `object_summary` | read | Attributes, provenance, relations, recent changes and open discrepancies in one answer | "Tell me about sw-a1" |
| `object_history` | read | Attribute values and relations over time, within a window | "What was plugged into this port last month?" |
| `changes_since` | read | Changes since a date, and the discrepancies (declared versus observed) opened in the period | "What changed since yesterday that was not planned?" |
| `describe_schema` | read | Types, attributes, relations; one type in full | |
| `propose_change` | propose | Queues a proposal, returns its id and a preview | "Prepare the documentation of the new server in rack 2, U10" |

Times are RFC 3339, a date (`2026-03-03`) or relative to now (`30m`, `24h`, `7d`, `2w`).

### Compliance

`version_compliance` compares dotted versions number by number (`4.20.7M` is 4.20.7; a missing
number counts as zero). With `target` the reference is strict unless `max_minor_lag` or
`max_patch_lag` tolerate a lag; with `policy` (the name of a `version_policy` object) the
reference, attribute, selector and tolerated lag come from the policy. The selector of a policy
is a search query (`role:switch`); one that matches nothing is flagged in `summary.warning`
instead of being read as "nothing is behind". Statuses: `compliant`, `within_tolerance`,
`behind` (furthest behind first), `unknown` (no version recorded).

### Forecast

`forecast_threshold` is generic: it takes the same selection as `filter_objects` (`type`, `query`,
`filters`, the same authorization and redaction) and a numeric `attribute`, a `threshold`, a
`direction` and a `horizon_days`. For each selected object it fits a least-squares line through
the values of the attribute in its history (the current value included) and returns the objects
whose line reaches the threshold within the horizon.

| Input | Meaning |
|---|---|
| `attribute`, `threshold` | Required. A sensitive attribute is refused. |
| `direction` | `rising` (default; "used" values such as `disk.wear_pct`) or `falling` ("left" values such as `disk.spare_pct`). |
| `horizon_days` | 1 to 3650, default 180. |
| `window_days` | Optional: only the values that began inside this window are fitted. |
| `type`, `query`, `filters`, `fields` | Selection and extra attributes to return with each object. |

Every object scanned lands in exactly one class, and the summary counts them all, so none is
dropped silently:

- `items` (`will_cross`): the line crosses within the horizon. Each carries `crosses_at`,
  `days_to_cross` (0 when the line is already past the threshold but the current value is not),
  `slope_per_day`, `points` and the `window` fitted. Soonest first.
- `already_crossed`: the current value is at or past the threshold. Checked first and needs no
  trend; it is listed (with its value), not forecast.
- `insufficient_history`: fewer than 2 points, or points spanning under one day. Listed with the
  reason.
- `no_trend`: the line is flat or moves away from the threshold; it never crosses (counted).
- `beyond_horizon`: it crosses, but later (counted).

The three lists share `limit` and `offset`; `truncated` and `next_offset` say when any has more.

What the figure is worth: it is a straight line, with no seasonality and no confidence interval,
and `attr_history` records **changes**, so a value that has not changed is one point, not a
series. A drive reporting 60 % for six months is `insufficient_history` ("one value since ...")
until its wear moves. The scan reads at most 5,000 objects per call; beyond that the summary says
`scan_truncated` and `matching` gives the real population: narrow the selection.

Wear in the built-in schema: `disk.wear_pct` (rising) and `disk.spare_pct` (falling), with
`disk.media` in `ssd`, `nvme`, `hdd`.

### Writing: `propose_change`

The input is the content of an `sdk.Batch` (objects, relations, unrelations) and a `reason`.
Source, scope and confidence are not the caller's to set: the batch is attributed to `mcp:<name>`
(the source a token is bound to when it is an `mcp:` one, else `mcp:` plus the subject), its
scope is `partial` (it withdraws nothing by omission) and its confidence `declared`. The answer is
`{status: "proposed", proposal_id, preview}`. Nothing is applied: a person approves or rejects the
proposal in trica. No tool approves, rejects, deletes or writes directly.

## Authentication, authorization, audit

- Streamable HTTP: `Authorization: Bearer <credential>` on every request, authenticated afresh
  by the `Authorizer` (the handler keeps no session, so a revoked credential stops working at once).
  No credential: 401. A credential that cannot be checked (the token store or the identity
  provider does not answer): 503 with `Retry-After`, so the client keeps it.
- Every tool call is authorized per object type and site, with the same `Authorizer` and the same
  rules as the REST API (`api.Guard`): what a caller may not read is neither listed nor counted
  nor named in a relation. A grant limited to some sites reads objects of those sites by id only:
  listing and searching need a type readable on every site.
- The write tool is offered only to a credential that holds `propose`, and the right is checked
  again when it is called.
- Every call is audited (`mcp.tool.<name>`, actor, outcome, target). The arguments are kept,
  except those of `propose_change`, of which only the counts are (the content is in the proposal).
  A refused or failed call is audited too.
- Errors reach the assistant as `<slug>: <detail>` (`invalid-request`, `not-found`, `forbidden`,
  `invalid-query`, `sensitive-attribute`...), so that it can correct itself. An internal error
  says only that it failed.

## Connect Claude Code

Remote, over streamable HTTP:

```sh
claude mcp add --transport http trica https://trica.example.org/mcp \
  --header "Authorization: Bearer $TRICA_TOKEN"
```

Local, over stdio (the process runs with the rights of the principal it is started with):

```sh
claude mcp add trica -- /path/to/your-embedding-binary
```

A `trica mcp` subcommand is not written yet (out of scope of this PR): embed
`Server.ServeStdio` in your own `main`.

## The dependency graph

`impact_analysis` and `network_path` need a graph. The server depends on the `mcp.GraphQuerier`
interface, not on `internal/graph`, so that it is tested with a fake and does not wait for the
graph. Without a `Graph` in the `Config`, the two tools are not offered. The server wires
`wiring.GraphAdapter` (`internal/wiring/graph.go`) over a `*graph.Cache` that is marked stale on
every commit of the engine (`Engine.OnCommit`: a batch, an approval, the absence sweep, the
deducer), on every notification on the PostgreSQL channel `trica_changes`, where every replica
announces its commits (ADR 0032), and every `mcp.graph_refresh` in case a notification was
missed; the reload itself happens on the next graph tool call. The adapter, in
short (the result types mirror those of `internal/graph` field for field, so a JSON round trip
converts them):

```go
type graphAdapter struct{ c *graph.Cache }

func (a graphAdapter) Impact(ctx context.Context, failed, anchors []string) (*mcp.ImpactResult, error) {
    snap, err := a.c.Get(ctx)
    if err != nil {
        return nil, err
    }
    res, err := snap.Impact(failed, graph.ImpactOptions{Anchors: anchors})
    return convert[mcp.ImpactResult](res, err)
}

func (a graphAdapter) Path(ctx context.Context, from, to string, rels []string) ([]mcp.Hop, error) {
    snap, err := a.c.Get(ctx)
    if err != nil {
        return nil, err
    }
    hops, err := snap.ShortestPath(from, to, rels)
    if hops == nil && err == nil {
        return nil, nil
    }
    out, err := convert[[]mcp.Hop](hops, err)
    if out == nil {
        return nil, err
    }
    return *out, err
}

// convert maps graph.ErrUnknownObject to mcp.ErrUnknownObject, and any other
// error through; v is converted by a JSON round trip.
func convert[T any](v any, err error) (*T, error) {
    if errors.Is(err, graph.ErrUnknownObject) {
        return nil, mcp.ErrUnknownObject
    }
    if err != nil {
        return nil, err
    }
    raw, _ := json.Marshal(v)
    var out T
    return &out, json.Unmarshal(raw, &out)
}
```

The graph tools fail closed: an impact or path that passes through an object the caller may not
read is refused, not shown in part.

## Not there yet

- MCP resources (model documentation, saved queries) and prompts (weekly health review,
  maintenance preparation, incident analysis) of the specification.
- A per-tool rate limit: the limit is per principal for the whole endpoint
  (`auth.rate_limit`), shared with the REST API.
- Alerts and metrics in `object_summary`, link load and flows, thermal and metrics/log
  correlation tools (later lots). End-of-life forecasts other than a threshold crossing are not
  there either.

## The twelve questions

`test/mcp/questions.yaml` holds the twelve questions of the specification, the tool that answers
each in one call, the seeded data and the expected answer; `go test ./test/mcp/` runs them
through an in-memory MCP session against PostgreSQL. 10 of 12 are answered in one call, which is
the specification's target for the first release. The two that are not (Q7 link load and flows,
Q11 metrics and log correlation) need data the inventory does not hold yet: the specification
puts them in lot 3 (`Priorisation`, lines 301-302), and the file says so. Q4 (SSD wear) is a lot 2
item in the specification (line 301), answered early because a generic trend tool covers it. Search latency on
2,000 devices: `go test -run '^$' -bench . ./test/bench/` (figures are measured, not asserted).
