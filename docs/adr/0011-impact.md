# ADR-0011: Impact analysis on an in-memory graph, with relation impact modes

- **Status**: accepted (2026-10); the graph cache has a second reader, the topology map, since ADR 0051 (`Cache.Recent`)
- **Concerns**: `internal/graph`, MCP `impact_analysis`

## Context

"What breaks if this switch, this PDU or this host goes down" must answer in under 2 seconds,
and redundancy must count: a server with two power supplies on two feeds survives one.

## Decision

- The current graph is held in memory and refreshed from the commit stream of the engine.
  Past states are read from SQL.
- Containment (parent) and `hosts` propagate failure down.
- Each relation type has an impact mode in the schema: `supply` fails the dependent only when
  all its suppliers fail; `link` makes reachability matter (an object cut from every anchor is
  down); `degrade` marks the dependent degraded, not down.

## Consequences

- Impact is a pure function of a graph snapshot and a set of failed objects, and is tested as
  such.
