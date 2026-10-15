# ADR-0032: Commits are announced on a PostgreSQL channel

- **Status**: accepted (2026-10)
- **Concerns**: `internal/reconcile` (`ChangesChannel`), `internal/store` (`Store.Listen`),
  `internal/wiring` (`runGraphRefresh`). Amends ADR 0025 (a replica's graph was up to
  `mcp.graph_refresh` behind a commit made by another one).

## Context

Every replica keeps the dependency graph in memory (ADR 0004). `Engine.OnCommit` marks it stale,
but only in the process that committed: with two replicas behind one Service, an MCP client
asking `impact_analysis` right after a batch lands on the other replica half the time and gets
the graph as it was up to a minute earlier (#57). Shortening `mcp.graph_refresh` trades staleness
for a reload of the whole graph on every graph call.

## Decision

- Every commit of the engine announces itself with `pg_notify('trica_changes', <changeset id>)`
  **inside its transaction**: a batch, an approved proposal, the absence sweep, the deducer's
  batch. PostgreSQL delivers the notification at commit and drops it on rollback, so a preview
  never announces anything and a commit is never announced before it is visible.
- Each server holds one connection **outside the pool** with `LISTEN trica_changes`, and marks
  its graph stale on every notification. Outside the pool, because a LISTEN belongs to the
  session and a pooled connection would carry it to its next borrower.
- When that connection is lost, the listener reconnects (1 s, doubling up to 30 s) and marks the
  graph stale as soon as it listens again: notifications sent in between are lost, and a stale
  mark costs one lazy reload.
- The `mcp.graph_refresh` timer stays, as a safety net, with the same one-minute default.

## Consequences

- A commit made by any replica reaches the graph of every other one as soon as PostgreSQL
  delivers the notification, instead of up to a minute later.
- One more database connection per replica.
- A connection pooler in transaction mode (PgBouncer) does not carry LISTEN: behind one, the
  listener sees nothing and the timer alone bounds the delay, as before this decision. Point
  `database.url` at PostgreSQL directly, or at a pooler in session mode, to get the faster path.
- The channel carries no data a reader could not already query: a changeset id.
