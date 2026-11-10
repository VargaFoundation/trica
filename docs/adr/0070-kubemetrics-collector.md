# ADR-0070: A kubemetrics collector over kube-state-metrics series

- **Status**: accepted (2026-10).
- **Concerns**: `internal/collect/kubemetrics` (new), `internal/collect/all`, `internal/wiring`
  (the kinds test), `web/src/collectors/modular.test.ts`, `docs/reference/collectors.md`,
  `docs/plan/STATUS.md`. No schema change, no migration, no API change.

## Context

ADR 0069 and its amendments give dev and prod through Argo CD: a `k8s_cluster`, its namespaces,
its workloads and the instance that `runs_on` them. There are no pods and no nodes, so trica cannot
answer "server X fails: which services are hit" there. A Kubernetes token would answer, but it can
read every pod's environment values, which is the secret tier trica refuses.

The cluster's kube-state-metrics series, stored in VictoriaMetrics, carry the placement (which pod,
on which node, owned by what) and carry no manifest, environment or argument.

## Decision

1. **A collector `kubemetrics`, one target per cluster**, reading a Prometheus-compatible query API
   (https, basic authentication, `file:` password). One database holds one cluster; there is no
   `cluster` label.
2. **`GET /api/v1/query` only, six metrics** (nine since the amendment of 2026-11-10 below). Read-only is an allow-list of metric names plus the
   one path and verb: `kube_node_info`, `kube_pod_info`, `kube_pod_owner`, `kube_replicaset_owner`,
   `kube_job_owner`, `kube_pod_status_phase`. Redirects are not followed; the account travels in a
   header.
3. **Same identities as the Argo CD view.** The cluster, namespaces and workloads use the identities
   the kubernetes collector uses, so both views reconcile into one object each (proven on PostgreSQL
   with the impact of the servers). The node finds its device by `hw_uuid`, as in the kubernetes
   collector.
4. **A Job's pods attach to its CronJob**, as one `cronjob` workload, when `kube_job_owner` names
   it; otherwise the Job is a `job` workload. This differs from the kubernetes collector (job
   workloads and a cronjob `served_by` job): the series give no reason to keep one object per run.
5. **The environment `kind` is not set** here: the Argo CD view owns it.
6. **Options**: `cluster` (required), `environment` (default the cluster), `selector` (extra matchers
   to bridle a shared database), `finished_pods` (default false: Succeeded and Failed pods are noise).
7. **Pod IPs are not emitted as `ip_address`** (overlap between clusters). Core metrics
   (`kube_node_info`, `kube_pod_info`) fail the run when they fail or are empty; the others make it
   partial and unhealthy; `isPartial` from the database is a warning.

## Consequences

- The impact of a server reaches the workload, the instance and, through them, what depends on them;
  failing one of two servers leaves the instance up.
- Nothing is known of replica counts, probes or images: not in these series.
- Dev needs its own vmauth user and target before it is read.

## Amendment (2026-11-10): three more families, for the capacity rule

Impact analysis now asks whether the pods of a lost node would find room on the nodes left (ADR
0011 amendment of the same date). That needs what a node can hold and what a pod asks for, which
kube-state-metrics carries.

- **Three families join the allow-list**, nine in all: `kube_node_status_allocatable`,
  `kube_node_spec_unschedulable`, `kube_pod_container_resource_requests`. The path, the verb and
  the rule that any other metric is refused before it is sent are unchanged. All three are
  optional: a failure makes the run partial and unhealthy, as for the other optional families.
- **Only `cpu` and `memory`.** The two per-resource families are queried with
  `resource=~"cpu|memory"` ANDed after the user's selector, so the other resources (ephemeral
  storage, devices, hugepages) are never fetched.
- **Attributes**: `cpu_capacity` (cores) and `memory_capacity_bytes` on the node, from allocatable
  (what the scheduler uses, not capacity); `unschedulable` on the node; `cpu_request` (cores) and
  `memory_request_bytes` on the pod, summed over its containers (a series reported twice by two
  kube-state-metrics replicas counts once). A Running pod with no request series asks for 0 when
  the family is usable; when the family is empty or unreadable no attribute is set, so that a
  server without the family is not read as "every pod asks for nothing".
- **Init-container requests are not read** (`kube_pod_init_container_resource_requests` is not in
  the allow-list). The scheduler reserves the larger of the sum of the containers and the largest
  init container; here a pod with a heavy init container is under-counted. The kubernetes
  collector, which reads the manifest, applies the exact rule. Pod overhead is not added either.
- **Consequence**: `TestImpactOfTheServers` still gives degraded for both servers down with the
  recorded numbers (a survivor has 66 to 70 cores free against 0.2 core asked), and goes down
  only when the pods ask for more than a node has.
