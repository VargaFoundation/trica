# ADR-0004: Every source keeps its own claim; a policy resolves the value

- **Status**: accepted (2026-10)
- **Concerns**: `internal/reconcile`

## Context

The same attribute is often reported by several sources: the OS version by the BMC and by the
agent, the owner by a human and by a declaration file. The specification asks for per-attribute
source priorities ("the version comes from the equipment, the owner comes from manual entry").

## Decision

- Claims are owned per source; two sources never overwrite each other.
- The resolved value is chosen by the policy: the first `PriorityRule` whose `type.attribute`
  glob matches orders the sources; ties and unlisted sources fall back to
  observed > deduced > declared, then the most recently confirmed claim, then the source name.
- When a declared value and an observed value disagree, the engine opens a `value_mismatch`
  discrepancy, whatever the winner. Discrepancies are first-class objects with a lifecycle
  (open, resolved, accepted); an accepted discrepancy is not reopened.

## Consequences

- Changing a priority rule changes the resolved values without touching the claims, and the
  history shows when.
