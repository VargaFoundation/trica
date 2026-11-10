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

## Amendment (2026-11-10): capacity-aware rescheduling

A node left is not enough: three nodes of three, two failed, and the pods of the two do not fit
on the third, yet the workloads were reported degraded. The rescheduling rule now asks whether
the pods would be placed.

- **Rule `capacity`.** A workload that the previous rule left degraded as `rescheduled` goes down
  with rule `capacity` when none of its down pods finds a place on the nodes left. A workload
  with at least one pod placed stays degraded (it has lost replicas, not its service). The
  application above follows through `instances`, as before.
- **Placement heuristic** (`internal/graph/capacity.go`). Per cluster: the survivors are the
  `k8s_node` objects of the cluster that are not absent, not down, not cordoned (`unschedulable`)
  and not `ready: false`. The free room of a survivor is `cpu_capacity` and `memory_capacity_bytes`
  (allocatable) minus the requests (`cpu_request`, `memory_request_bytes`) of the pods it hosts
  that are not down. The demand is the down pods of the workloads that are degraded as
  `rescheduled` or already lost to capacity, largest CPU request first (then memory, then id).
  Each pod goes on the node with most free CPU that has room for both its CPU and its memory
  (the scheduler spreads; ties go to the smaller id), and takes that room. Deterministic: the
  result does not depend on the order of objects or relations.
- **Unknown capacity keeps the old behaviour.** When a survivor reports no `cpu_capacity` or no
  `memory_capacity_bytes`, the rule does not apply and the workload stays degraded. A pod with no
  request attribute asks for 0. DaemonSet pods are not moved and take no room; a daemonset
  workload is left as it was.
- **Where the numbers come from**: the kubernetes collector (manifest) and the kubemetrics
  collector (kube-state-metrics, ADR 0070 amendment of the same date).

What it does **not** model, so a workload shown degraded here can still fail to start, and a
workload shown down could in rare cases fit:

- taints and tolerations, node selectors and affinity or anti-affinity of any kind;
- PodDisruptionBudgets and topology spread constraints;
- priorities and preemption (a pod that would evict another is not placed here);
- init-container requests when the numbers come from kube-state-metrics (the kubernetes
  collector applies the scheduler's rule: the larger of the sum of containers and the largest
  init container), and pod overhead (neither collector adds it);
- the `pods` allocatable count (a node full of pods by number);
- persistent volume zone or node binding;
- the time Kubernetes takes (image pulls, evictions, controller delay), and anything beyond CPU
  and memory (ephemeral storage, devices, hugepages);
- pods of a StatefulSet that need the same node, and pods already pending before the failure.
