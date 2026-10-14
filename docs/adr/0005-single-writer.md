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
