# ADR-0009: MCP is read-only by default; writes are proposals

- **Status**: accepted (2026-10)
- **Concerns**: `internal/mcp`

## Context

The specification: read by default; writing only through dedicated tools that create a change
proposal. An LLM client may follow injected instructions.

## Decision

- Every MCP read tool answers in one call where possible, paginated, with a counted summary
  first.
- Write tools build an `sdk.Batch` from source `mcp:<client>` and submit it. The default policy
  lists `mcp:*` under `Propose`, so the batch becomes a proposal with its preview; a human
  approves it in the UI.
- The MCP server authenticates with scoped tokens; a token without the write scope does not
  see the write tools at all.

## Consequences

- An injected instruction can at most create a proposal, which is visible and reviewable.
