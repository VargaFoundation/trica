# ADR-0007: Absence, never deletion

- **Status**: accepted (2026-10)
- **Concerns**: `internal/reconcile`, `internal/store`

## Context

The specification: an object not observed for a configurable duration becomes absent, and is
never removed without a trace.

## Decision

- `SweepAbsent` marks `absent` every observed object whose last observation is older than the
  policy's `AbsentAfter` (30 minutes by default). Objects only ever declared never go absent.
- An absent object that is seen again becomes active again, with the same id and history.
- The inventory tables have no DELETE path; the only removal is a database restore.

## Consequences

- Inventories grow; the absent objects are filtered out of default views and searches, and kept
  for history.
