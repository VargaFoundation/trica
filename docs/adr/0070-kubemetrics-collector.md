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
2. **`GET /api/v1/query` only, six metrics.** Read-only is an allow-list of metric names plus the
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
