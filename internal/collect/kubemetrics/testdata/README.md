# Fixtures of the kubemetrics collector

One set, `recorded-2026-10/`. Each file is the complete JSON body of one answer of the Prometheus
HTTP API (`GET /api/v1/query`, instant vector), served by `FakeKubeMetrics` under `/inventory`
after it filters the samples by the query's selector. The file is picked by the metric name.

| Set | Recorded or synthesised | Content |
|---|---|---|
| `recorded-2026-10/` | **Recorded** on 2026-10-28 from the kube-state-metrics series of a three-node k3s v1.31.1 cluster, stored in VictoriaMetrics behind vmauth, through `/inventory/api/v1/query`. The VictoriaMetrics version is not visible in the answers (they carry `isPartial: false` and `stats`). Then anonymised and trimmed (below). | 3 nodes, 71 pods (47 Running, 18 Succeeded, 6 Failed), 22 ReplicaSets, 19 Jobs. |

Nothing is synthesised: error and partial answers are built by the tests (`SetBody`, `Partial`,
`FailStatus`), not stored here.

| File | Query | Read by the collector |
|---|---|---|
| `kube_node_info.json` | `kube_node_info` | yes (nodes, their servers) |
| `kube_pod_info.json` | `kube_pod_info` | yes (pods and the node of each) |
| `kube_pod_owner.json` | `kube_pod_owner` | yes (the controller of each pod) |
| `kube_replicaset_owner.json` | `kube_replicaset_owner` | yes (ReplicaSet to Deployment) |
| `kube_job_owner.json` | `kube_job_owner` | yes (Job to CronJob) |
| `kube_pod_status_phase.json` | `kube_pod_status_phase` | yes (current phase, value 1) |

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
