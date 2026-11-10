# kubemetrics: a cluster seen through kube-state-metrics

For clusters trica cannot query directly. A Kubernetes token there could read the environment
values of every pod (secret tier, refused), whereas the kube-state-metrics series already carry the
placement without any manifest (ADR 0070).

## Account and endpoint

- `address`: https base URL of a Prometheus-compatible query API, including the path prefix that
  serves it (`https://metric-select.internal.example.org/inventory`). The client appends
  `/api/v1/query`. No credential, query or fragment in the address.
- `username` and `password` (`file:` or `env:` reference): basic authentication, sent in a header.
- Only instant queries, `GET /api/v1/query`. No range, series or labels call; redirects are not
  followed. Read-only is an allow-list of nine metric names (`client.go`, proven by `readonly_test.go`).

## The nine metrics

`kube_node_info`, `kube_pod_info`, `kube_pod_owner`, `kube_replicaset_owner`, `kube_job_owner`,
`kube_pod_status_phase`, `kube_node_status_allocatable`, `kube_node_spec_unschedulable`,
`kube_pod_container_resource_requests`. The first two are core: an error or an empty answer fails
the run. The other seven are optional: a failure makes the run partial and unhealthy, the pods are
kept. The last three feed the capacity rule of impact analysis (ADR 0011 amendment of 2026-11-10;
ADR 0070 amendment): the two per-resource ones, allocatable and requests, are asked for `cpu` and
`memory` only (`resource=~"cpu|memory"` is ANDed to the query).

## Mapping

| Source | Object or relation |
|---|---|
| `cluster` option | `k8s_cluster` (name) `deployed_in` the environment |
| `kube_node_info` | `k8s_node` (name; system uuid, internal IP, kernel, kubelet, OS image) in the cluster; `hosts` from the device whose `hw_uuid` is the system uuid |
| `kube_pod_info` | `namespace` and `pod` (name, uid, pod IP, node name), hosted by its node |
| `kube_node_status_allocatable` | on the node: `cpu_capacity` (cores) and `memory_capacity_bytes`, each only when its series is present |
| `kube_node_spec_unschedulable` | on the node: `unschedulable` (cordoned) |
| `kube_pod_container_resource_requests` | on the pod: `cpu_request` (cores) and `memory_request_bytes`, the sum over its containers (a container reported twice counts once). A Running pod with no series asks for 0 when the family is usable; when the family is empty or could not be read, no attribute is set |
| `kube_pod_status_phase` | the pod's phase; finished pods (Succeeded, Failed) are left out unless `finished_pods` is true |
| `kube_pod_owner` and `kube_replicaset_owner` | pod to ReplicaSet to Deployment; StatefulSet and DaemonSet directly |
| `kube_pod_owner` and `kube_job_owner` | Job to CronJob, else the Job as a `job` workload; `served_by` workload to pod |

Workload, namespace and cluster identities are those of the Argo CD view of the kubernetes
collector, so that both views meet on one object each. The environment `kind` is not set here.

## Left out

Pod IPs as `ip_address` (they overlap between clusters), environment values, manifests, container
arguments (the series carry none), bare pods have no workload. Init-container requests
(`kube_pod_init_container_resource_requests`) are not read: the scheduler asks for the larger of
the sum of the containers and the largest init container, so a pod with a heavy init container
shows here a request below what the scheduler reserves for it (the kubernetes collector, which
sees the manifest, takes it into account). Limits, other resources (ephemeral storage, extended
resources), the `pods` allocatable count and pod overhead are not read either.

## Options

| Option | Meaning |
|---|---|
| `cluster` | Required. The `k8s_cluster` name; it must equal the environment name the Argo CD view uses. |
| `environment` | Environment name, default the cluster. |
| `selector` | Extra label matchers ANDed to every query, for example `job="monitoring-kube-state-metrics"`. Braces optional. |
| `finished_pods` | Keep Succeeded and Failed pods; default false. |

## Errors

401 bad credentials, 403 forbidden, 429 rate limited, other non-2xx, `status` not `success`, a
result that is not a vector: the run fails. `isPartial: true` is a warning with a partial scope.
Secrets never appear in an error or a log (`privacy_test.go`).
