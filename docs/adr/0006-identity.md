# ADR-0006: Identity, strong first, weak scoped by parent

- **Status**: accepted (2026-10)
- **Concerns**: `internal/reconcile`, every collector

## Context

Equipment gets renamed, readdressed, its NIC replaced, its disk moved. Two sources describe the
same server with different identifiers. Merging wrongly is worse than not merging.

## Decision

- Match on strong identifiers first (serial, MAC, UUID). A strong match holds even if another
  strong identifier changed (a replaced NIC keeps the server).
- Then on weak identifiers (name, IP), scoped by the parent when the type says so (an interface
  named `Ethernet1` is only unique within its device). A weak candidate is rejected if one of
  its strong identifiers contradicts the incoming object.
- `id` designates an existing object by its trica id; the UI and API use it.
- A parent change is a move, recorded in the history.

## Consequences

- A conflicting serial number never merges two objects; it creates a second one, and the
  duplicate name is visible.
- An address or a prefix is only unique within a routing domain: [ADR 0036](0036-identity-scope.md)
  scopes their identity by an optional `domain`, the global one when absent.
