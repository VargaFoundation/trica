# ADR-0010: Cabling is deduced from LLDP and MAC tables, with a confidence

- **Status**: accepted (2026-10)
- **Concerns**: `internal/deduce`, network collectors

## Context

Cables are rarely documented and drift from the documentation. The specification asks for
cabling deduced from LLDP and MAC tables with a confidence level, created automatically or
proposed depending on the policy, and for a moved cable to show within 15 minutes.

## Decision

- Collectors report what they see: `neighbor_of` from LLDP, MAC address tables.
- A deducer turns a pair of consistent LLDP views into a `cabled_to` relation between the two
  interfaces, from source `deduce:lldp` with confidence `deduced`. A single-sided view or a MAC
  table entry on an access port gives a lower confidence.
- A documented cable is a `cable` object with two `connects` relations. When documentation and
  deduction disagree, the deducer opens `cable_not_observed`, `cable_not_declared` or
  `cable_conflict` discrepancies, and resolves the ones that no longer hold at the end of a pass.
- Whether deduced cables are applied or proposed is the policy's `Propose` list (`deduce:*`).

## Consequences

- The cable map is only as complete as LLDP coverage; hosts without lldpd fall back to MAC
  tables and lower confidence.
