# ADR-0005: The reconciliation engine is the only writer

- **Status**: accepted (2026-10)
- **Concerns**: every component that changes the inventory

## Context

Collectors, the agent, declaration files, the UI, imports and MCP tools all change the
inventory. If each wrote directly, identity, priority, history and proposal rules would be
re-implemented in each, and diverge.

## Decision

- Every change is an `sdk.Batch` submitted to `internal/reconcile`. A manual edit in the UI is a
  declared batch from source `user:<login>`; an MCP write is a batch from `mcp:<client>`; a
  declaration file is a batch from `declare:<file>`.
- The policy's `Propose` globs decide per source whether a batch is applied or queued as a
  proposal. A proposal stores the batch and a preview of its effect, computed by running it in
  a transaction that is rolled back; approving re-runs it against the current state.
- The engine serialises writes with a PostgreSQL advisory lock. Throughput is bounded by
  that; a site of a few thousand objects needs a few batches per second at most.
- A full-scope batch withdraws the claims and relation claims its source does not repeat. A
  batch whose health is not OK is forced to partial scope: a failed run withdraws nothing.

## Consequences

- One place to test the rules (`internal/reconcile/engine_test.go`).
- No package other than `internal/reconcile` and `internal/store` migrations writes to the
  inventory tables.

## Addendum: `observed_at` is bounded (2026-10)

A batch's `observed_at` becomes the first/last seen time of its claims, so an arbitrarily old
value lets a caller backdate what it says (absence detection, history order). The engine now:

- **Clamps** a future or zero `observed_at` to the engine clock (unchanged).
- **Rejects** a batch whose `observed_at` is older than a maximum age, with `ErrRejected`
  (the API answers 422, not 400: the batch is well formed, the engine refuses it). Nothing is
  applied.
- The maximum age is `Policy.MaxObservedAge`, **per source kind** (the part of the source before
  the first `:`; `*` is the fallback). Defaults: `user`, `token` and `api` 15 minutes (API callers have
  no spool, they observe "now"); every other kind 7 days, to cover a collector or agent replaying
  its disk spool after an outage (ADR 0008).
- The check runs when a batch is submitted (`Apply`, `Propose`), not when a stored proposal is
  approved: a proposal waiting days for a human is not stale for that reason.

## Addendum: schema violations are hard errors for API callers (2026-10)

A value the schema refuses (invalid enum, wrong kind) or a relation type it does not know used to
be dropped with a warning whatever the source, so an API client checking only the status code
believed its data was recorded. Now, by source kind:

- **`user`, `token` and `api`** (people and API clients; `api:token-<id>` is how a token that
  is not bound to a source speaks, ADR 0028): such a batch is **rejected** with
  `ErrRejected` (the API answers 422), nothing is applied, including for a proposal at proposal
  time. The caller can fix its input and resend.
- **Every other kind** (collectors, agent, `deduce`, `declare`, `mcp`): unchanged, a warning in
  `Result.Warnings`. A collector must not lose a whole inventory run on one odd value.

Not covered, still a warning for everyone: an unknown attribute (dropped, declare it as a custom
field first) and a relation between types the schema does not allow.
