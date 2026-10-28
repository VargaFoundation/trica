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
  followed. Read-only is an allow-list of six metric names (`client.go`, proven by `readonly_test.go`).

## The six metrics

`kube_node_info`, `kube_pod_info`, `kube_pod_owner`, `kube_replicaset_owner`, `kube_job_owner`,
`kube_pod_status_phase`. The first two are core: an error or an empty answer fails the run. The
other four are optional: a failure makes the run partial and unhealthy, the pods are kept.

## Mapping

| Source | Object or relation |
|---|---|
| `cluster` option | `k8s_cluster` (name) `deployed_in` the environment |
| `kube_node_info` | `k8s_node` (name; system uuid, internal IP, kernel, kubelet, OS image) in the cluster; `hosts` from the device whose `hw_uuid` is the system uuid |
| `kube_pod_info` | `namespace` and `pod` (name, uid, pod IP, node name), hosted by its node |
| `kube_pod_status_phase` | the pod's phase; finished pods (Succeeded, Failed) are left out unless `finished_pods` is true |
| `kube_pod_owner` and `kube_replicaset_owner` | pod to ReplicaSet to Deployment; StatefulSet and DaemonSet directly |
| `kube_pod_owner` and `kube_job_owner` | Job to CronJob, else the Job as a `job` workload; `served_by` workload to pod |

Workload, namespace and cluster identities are those of the Argo CD view of the kubernetes
collector, so that both views meet on one object each. The environment `kind` is not set here.

## Left out

Pod IPs as `ip_address` (they overlap between clusters), environment values, manifests, container
arguments (the series carry none), bare pods have no workload.

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
