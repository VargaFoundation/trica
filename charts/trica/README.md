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
| `transport.enabled` | `false` | Mutual-TLS endpoint for agents, on its own port and Service port (ADR 0025). Needs `transport.hosts` and `transport.existingClaim`; the chart refuses to render without them. |
| `transport.port` / `hosts` | `8443` / `[]` | Listen port; names and IPs agents dial (the server certificate's). |
| `transport.existingClaim` / `subPath` | `""` / `""` | PersistentVolumeClaim holding the CA (`ca.pem`, `ca-key.pem`). Losing it un-enrols every agent; ReadWriteMany with several replicas. |
| `transport.ingressFrom` | `[]` | Peers allowed on the transport port; empty means any pod of the namespace. |
| `networkPolicy.enabled` | `true` | Ingress on the HTTP port, and on the transport port when enabled. |
| `networkPolicy.ingressFrom` | `[]` | Allowed peers; empty means any pod of the namespace. |
| `networkPolicy.egressTo` | `[]` | Egress rules besides DNS; empty leaves egress open. List the database when set. |
| `serviceMonitor.enabled` / `interval` / `labels` | `false` / `30s` / `{}` | Prometheus Operator scrape of `/metrics`. |
| `podDisruptionBudget.enabled` / `minAvailable` | `false` / `1` | |
| `resources` | 100m / 128Mi requested, 512Mi limit | |
| `podSecurityContext`, `securityContext` | non-root 65532, read-only root, no capabilities, RuntimeDefault seccomp | |
| `serviceAccount.create` / `name` / `annotations` | `true` / `""` / `{}` | The token is not mounted: the server does not call the Kubernetes API. |
| `backup.enabled` | `false` | Renders the backup CronJob below. |
| `backup.schedule` | `17 2 * * *` | Cron schedule. |
| `backup.existingClaim` | `""` (required when enabled) | PersistentVolumeClaim receiving the archives. |
| `backup.subPath` | `""` | Subdirectory of the claim. |
| `backup.image.repository` / `tag` | `ghcr.io/vargafoundation/trica-ops` / appVersion | The `ops` image (`docker build --target ops .`): the server binary plus `pg_dump` 17. Not published yet. |

## First token

A new release has no credential. Create one in the running pod (the image is distroless: call
`trica` directly, and pass the configuration the chart mounts). The token is the only thing on
stdout, shown once; metadata goes to stderr:

```bash
TOKEN=$(kubectl -n trica exec deploy/trica -- trica token create --config /etc/trica/trica.yaml \
  --name claude-code --owner you --roles admin --scopes write)
kubectl -n trica exec deploy/trica -- trica token list --config /etc/trica/trica.yaml
kubectl -n trica exec deploy/trica -- trica enrol create --config /etc/trica/trica.yaml \
  --binding agent:host-01 --ttl 24h        # needs transport.enabled; prints where ca.pem is
```

Whoever may `kubectl exec` into the pod already has the database URL, so limit that right with
cluster RBAC ([security](../../docs/reference/security.md#minting-the-first-token)). The server
must have started once (it migrates). `kubectl exec` without `-t` keeps stdout and stderr apart,
so `$(…)` captures the token only. No chart change is needed.

## Backup

The reference deployment (Zalando operator with WAL-G) takes its own physical backups with
point-in-time recovery: leave `backup.enabled` off there. The CronJob is for standalone
installs, where PostgreSQL has nothing of its own: it runs `trica backup --dir /backups` on the
schedule with the same database Secret as the server (a mounted file, no credential in values) and
writes `trica-<timestamp>.dump` to the claim. It never deletes: prune the volume yourself. For an
S3-compatible target use a claim backed by an S3 CSI driver, or copy the archives off with a job of
your own; the chart holds no object-storage credentials. Restore is attended, not scheduled:
see [`deploy/compose/README.md`](../../deploy/compose/README.md) for the procedure, which is the same
command with the same image (`kubectl run`, scale the server to 0 first) and
[ADR 0021](../../docs/adr/0021-backup-restore.md).

## Tests

```bash
helm unittest charts/trica
helm template ci charts/trica --set database.existingSecret=ci | kubeconform -strict -summary
```
