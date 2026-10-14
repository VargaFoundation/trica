# ADR-0010: Cabling is deduced from LLDP and MAC tables, with a confidence

- **Status**: accepted (2026-10)
- **Concerns**: `internal/deduce`, network collectors

## Context

Cables are rarely documented and drift from the documentation. The specification asks for
cabling deduced from LLDP and MAC tables with a confidence level, created automatically or
proposed depending on the policy, and for a moved cable to show within 15 minutes.

## Decision

- Collectors report what they see: `neighbor_of` from LLDP, MAC address tables.
- A deducer (`internal/deduce`) recomputes the whole cable map from the current inventory on
  every pass and submits it as one full-scope batch from source `deduce:cabling` with confidence
  `deduced`. A cable that loses its evidence is withdrawn, which closes its relation: a moved
  cable is one relation closed and one opened, both in the history.
- Each `cabled_to` relation carries its `evidence` and `level`:
  - `lldp` / `high`: the two ports see each other, and neither sees anyone else;
  - `lldp_one_sided` / `medium`: one port sees the other, and neither end has any other claim;
  - `mac_table` / `low`: an edge port with no LLDP has learned the addresses of exactly one
    physical port of another device (not a VM), and that port is learned on no other edge port
    and has no LLDP cable.
- Everything else is left undecided and logged with its reason: a port with several LLDP
  neighbours (a hub, a stale entry), addresses learned on an aggregate, a bond (one address on
  several physical ports), several hosts behind one edge port. Guessing would hide the cases an
  operator most needs to see.
- A documented cable is a `cable` object with two `connects` relations. When documentation and
  deduction disagree, the deducer opens `cable_not_observed`, `cable_not_declared` or
  `cable_conflict` discrepancies, and resolves the ones that no longer hold at the end of a pass.
  `cable_not_observed` is only raised when one end is on a device that reports LLDP or a
  forwarding table, and `cable_not_declared` only when one end is on a device that has documented
  cables: without these limits a site with no documentation, or a device with no discovery,
  would turn every cable into a discrepancy.
- Whether deduced cables are applied or proposed is the policy's `Propose` list (`deduce:*`).

## Consequences

- The cable map is only as complete as LLDP coverage; hosts without lldpd fall back to MAC
  tables and lower confidence.
- A forwarding table only places a host on the switch port that faces it; a host behind an
  unmanaged switch is not cabled at all rather than cabled to the wrong port.
- With `deduce:*` in `Propose`, every pass re-proposes the full map; identical content reuses
  the pending proposal instead of queueing a new one.
