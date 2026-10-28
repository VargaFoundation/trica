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

## Amendment (2026-10-28): degraded states, rescheduling, applications

Measured in production with the placement of the pods (ADR 0070): one server of three down
listed 17 application instances as down, while Kubernetes starts their pods again on the other
nodes within minutes. The rules become:

- **Partial supply degrades.** A `supply` relation still takes the dependent down when every
  supplier of that type is down; while only some are, the dependent is **degraded** (its
  redundancy is gone): a workload that lost one replica of two, a prefix with one route left.
- **Rescheduling.** A workload whose pods are all down is **degraded** (rule `rescheduled`)
  while a node of its cluster is up, and down when none is (checked again once the propagation
  is still, so the order in which nodes fall does not matter).
- **Degraded travels along `to_from`.** What runs on or depends on a degraded object is degraded
  (`runs_on`, `depends_on`); a `calls` still only degrades its caller, and containment, `hosts`
  and supply relations do not carry a degraded state.
- **Applications take the state of their instances** (rule `instances`): down when all their
  component instances are down, degraded when one is down or degraded. An application with a
  healthy instance in another environment is degraded, not down. Its components are not
  evaluated (an application down takes them down by containment, as before).

The result keeps its shape (`state`, `rule`, `cause`, counts per type); `degraded` counts grow.

