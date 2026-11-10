# Fixtures of the kubemetrics collector

One set, `recorded-2026-10/`. Each file is the complete JSON body of one answer of the Prometheus
HTTP API (`GET /api/v1/query`, instant vector), served by `FakeKubeMetrics` under `/inventory`
after it filters the samples by the query's selector. The file is picked by the metric name.

| Set | Recorded or synthesised | Content |
|---|---|---|
| `recorded-2026-10/` | **Recorded** on 2026-10-28 from the kube-state-metrics series of a three-node k3s v1.31.1 cluster, stored in VictoriaMetrics behind vmauth, through `/inventory/api/v1/query`. The VictoriaMetrics version is not visible in the answers (they carry `isPartial: false` and `stats`). Then anonymised and trimmed (below). | 3 nodes, 71 pods (47 Running, 18 Succeeded, 6 Failed), 22 ReplicaSets, 19 Jobs. |

Error and partial answers are built by the tests (`SetBody`, `Partial`, `FailStatus`), not
stored here. One file is synthesised, `kube_pod_container_resource_requests.json` (below).

| File | Query | Read by the collector |
|---|---|---|
| `kube_node_info.json` | `kube_node_info` | yes (nodes, their servers) |
| `kube_pod_info.json` | `kube_pod_info` | yes (pods and the node of each) |
| `kube_pod_owner.json` | `kube_pod_owner` | yes (the controller of each pod) |
| `kube_replicaset_owner.json` | `kube_replicaset_owner` | yes (ReplicaSet to Deployment) |
| `kube_job_owner.json` | `kube_job_owner` | yes (Job to CronJob) |
| `kube_pod_status_phase.json` | `kube_pod_status_phase` | yes (current phase, value 1) |
| `kube_node_status_allocatable.json` | `kube_node_status_allocatable{resource=~"cpu\|memory"}` | yes (`cpu_capacity`, `memory_capacity_bytes`). **Recorded** the same day as the others, from the same cluster. |
| `kube_node_spec_unschedulable.json` | `kube_node_spec_unschedulable` | yes (`unschedulable`). **Recorded** (all three nodes schedulable, value 0). |
| `kube_pod_container_resource_requests.json` | `kube_pod_container_resource_requests{resource=~"cpu\|memory"}` | yes (`cpu_request`, `memory_request_bytes`). **Synthesised**, see below. |

### The capacity files

- `kube_node_status_allocatable.json` is recorded, then trimmed to the `cpu`, `memory` and `pods`
  resources (the recording also held devices, hugepages and ephemeral storage, which the collector
  never asks for) and anonymised as the others: nodes renamed `kube-prod-01` to `-03`, the
  `instance` label set to a documentation address, the kube-state-metrics pod name replaced, the
  timestamp reset, `stats.seriesFetched` reset. Values are as recorded: 72 cores, about 270 GB of
  memory per node, 200 pods.
- `kube_pod_container_resource_requests.json` is **synthesised**: the recording holds real
  workloads, under their real names, which the fixture renames; the series were rebuilt from the
  recorded ones rather than renamed one by one. The 60 series cover the 47 Running pods of the
  fixture:
  - 21 pods take the container names and the values of a recorded pod of the same namespace and
    the same name stem (same controller, so the same requests);
  - the `orion` workloads, which stand for several tenants, have one container named after the
    workload, with the median over the 245 recorded tenant pods (0.1 core and 256 MiB for an
    application or a worker, 0.255 core and 544 MiB for the database, 0.35 core and 4.1 GiB for
    the cache); the `lyra` virtual-machine pod takes the recorded `compute` container;
  - the 19 system pods for which no recorded series exists get no series: they count 0, which
    is also what the collector does for a Running pod without a series.
- Consequence: against nodes of 72 cores these requests are tiny, so this fixture alone never
  shows a cluster short of room. The tests that need a shortage build it (`capacity_test.go`,
  `TestImpactOfTheServers`, `internal/graph`).

## Anonymisation

- Nodes are `kube-prod-01` to `kube-prod-03`; node addresses are in 192.0.2.0/24
  (`192.0.2.11` to `.13`), pod addresses in 198.51.100.0/24; `provider_id` follows the nodes.
- `uid` values are sequential (`00000000-0000-4000-8000-000000000NNN`).
- `system_uuid` values are synthetic and keep the HPE structure (a fixed prefix, then the
  serial encoded in hexadecimal: `32353537-3835-5a43-4a30-30303031-5331` for serial `CZJ00001S1`,
  and `S2`, `S3` for the other two); none is a real serial.
- Tenant namespaces and pods are renamed `orion` and `lyra` (and their ReplicaSets, Jobs and
  CronJobs follow, for example `orion-boond-sync`).
- Kept as recorded: the `job="monitoring-kube-state-metrics"` and `tenant="platform"` labels,
  Kubernetes system namespaces, the kinds and shapes.
- Trimmed to 71 pods (and the ReplicaSets and Jobs they reference); `stats.seriesFetched` was
  reset. `anonymised_test.go` scans every file for banned tokens and non-documentation addresses.
