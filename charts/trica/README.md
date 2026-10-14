# trica Helm chart

Deploys the trica server. PostgreSQL is external; the chart reads its URL from an existing
Secret and refuses to render without one.

```bash
helm install trica ./charts/trica -n trica --set database.existingSecret=trica-db
```

No chart or image is published yet; see [getting started](../../docs/getting-started.md).

## Values

| Key | Default | Meaning |
|---|---|---|
| `database.existingSecret` | `""` (required) | Secret holding the PostgreSQL URL. Mounted as a file, never as an environment variable. |
| `database.urlKey` | `database-url` | Key of that Secret. |
| `config` | see `values.yaml` | Server configuration ([reference](../../docs/reference/configuration.md)); `http.addr` and `database.url_file` are set by the chart. |
| `schemaOverlays` | `{}` | Custom schema files, one key per file, mounted under `/etc/trica/schema/`. |
| `image.repository` / `tag` / `digest` | `ghcr.io/vargafoundation/trica` / appVersion / `""` | A digest takes precedence over the tag. |
| `replicaCount` | `1` | Migrations and the sweep are safe with several replicas (ADR 0014). |
| `service.type` / `port` | `ClusterIP` / `8080` | |
| `networkPolicy.enabled` | `true` | Ingress on the HTTP port only. |
| `networkPolicy.ingressFrom` | `[]` | Allowed peers; empty means any pod of the namespace. |
| `networkPolicy.egressTo` | `[]` | Egress rules besides DNS; empty leaves egress open. List the database when set. |
| `serviceMonitor.enabled` / `interval` / `labels` | `false` / `30s` / `{}` | Prometheus Operator scrape of `/metrics`. |
| `podDisruptionBudget.enabled` / `minAvailable` | `false` / `1` | |
| `resources` | 100m / 128Mi requested, 512Mi limit | |
| `podSecurityContext`, `securityContext` | non-root 65532, read-only root, no capabilities, RuntimeDefault seccomp | |
| `serviceAccount.create` / `name` / `annotations` | `true` / `""` / `{}` | The token is not mounted: the server does not call the Kubernetes API. |

## Tests

```bash
helm unittest charts/trica
helm template ci charts/trica --set database.existingSecret=ci | kubeconform -strict -summary
```
