# trica Helm chart

Deploys the trica server. PostgreSQL is external; the chart reads its URL from an existing
Secret and refuses to render without one.

```bash
helm install trica ./charts/trica -n trica --set database.existingSecret=trica-db
```

Nothing in the chart is specific to one site: no secret manager, policy engine, CNI or ingress
controller is assumed. `values.schema.json` rejects unknown and mistyped keys. Two example values
files are in [`examples/`](examples/): [`minimal.yaml`](examples/minimal.yaml) (the database Secret
and the image, nothing else) and [`production.yaml`](examples/production.yaml) (Ingress, OIDC,
restricted NetworkPolicy, collectors, transport). Where the image comes from is in
[getting started](../../docs/getting-started.md#the-image).

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
| `ingress.enabled` | `false` | An Ingress for the HTTP port (UI, API, MCP); needs `ingress.hosts`. With the default NetworkPolicy, add the ingress controller's namespace to `networkPolicy.ingressFrom`. |
| `ingress.className` / `annotations` / `hosts` / `tls` | `""` / `{}` / `[]` / `[]` | Controller-agnostic: the class (cluster default when empty), the annotations your controller needs, host names, standard Ingress TLS entries. |
| `imagePullSecrets` | `[]` | For a private registry. |
| `deviceTypes.existingClaim` / `subPath` | `""` / `""` | PersistentVolumeClaim holding the operator's device models and face images, mounted read-only at `/etc/trica/devicetypes` and configured as `devicetypes.extra_dir` ([layout](../../docs/reference/devicetypes.md#your-own-models-and-images)). Read at start. |
| `theme.builtin` | `""` | A theme shipped in the image: `default` (as before) or `flat`. Exclusive with `theme.existingConfigMap`. |
| `theme.url` / `sha256` | `""` / `""` | A theme archive (zip) downloaded once from an `https` URL, kept in PostgreSQL, and verified against `sha256` before anything reads it: a mismatch applies nothing. Both are required together; exclusive with `builtin` and `existingConfigMap`. |
| `theme.locked` | `false` | The theme above always wins: an administrator's choice in the console (stored in PostgreSQL, shared by every replica) is ignored and the API refuses to change it. Needs one of the sources above. |
| `theme.existingConfigMap` / `archiveKey` | `""` / `""` | A ConfigMap holding your theme, mounted read-only at `/etc/trica/theme` (a directory mount, so an edited ConfigMap is picked up within a minute, no restart). With `archiveKey`, the key that holds a zip archive (`kubectl create configmap trica-theme --from-file=theme.zip`); without it, the keys are the files of the theme (`--from-file=my-theme/`). A theme that fails validation is logged and counted, never fatal ([format](../../docs/reference/themes.md)). Set the theme here, not in `config.web.theme`. |
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
| `serviceAccount.create` / `name` / `annotations` | `true` / `""` / `{}` | The token is not mounted, except with `collect.kubernetes.rbac.enabled`. |
| `collect.enabled` | `false` | The server runs collectors on schedule ([reference](../../docs/reference/collectors.md)). Needs `collect.targets` or `collect.targetsConfigMap`. |
| `collect.targets` | `[]` | Targets, rendered into the chart ConfigMap as the targets file. Credentials are `file:` references, never values. |
| `collect.targetsConfigMap` / `targetsKey` | `""` / `targets.yaml` | An existing ConfigMap holding the targets file instead of `collect.targets`. |
| `collect.secrets` | `[]` | Existing Secrets to mount at `/var/run/secrets/collect/<name>/` (one file per key), for targets' `file:` references. |
| `collect.kubernetes.rbac.enabled` | `false` | ClusterRole and binding, read-only (`get`/`list`/`watch`, no Secrets, no ConfigMaps), for a `kubernetes` target with address `in-cluster`; mounts the service account token. |
| `live.enabled` | `false` | The live topology overlay ([reference](../../docs/reference/configuration.md#live-topology-overlay), ADR 0041): the server reads link throughput and state from a Prometheus-compatible endpoint. Needs `live.config.url` and `live.config.sources`. |
| `live.config` | `{}` | The server's `live` block, rendered as is and checked by the values schema. Credentials are `file:` references to `live.secrets`, never values. |
| `live.secrets` | `[]` | Existing Secrets to mount at `/var/run/secrets/live/<name>/` (one file per key), for `live.config`'s `file:` references. |
| `backup.enabled` | `false` | Renders the backup CronJob below. |
| `backup.schedule` | `17 2 * * *` | Cron schedule. |
| `backup.existingClaim` | `""` (required when enabled) | PersistentVolumeClaim receiving the archives. |
| `backup.subPath` | `""` | Subdirectory of the claim. |
| `backup.image.repository` / `tag` | `ghcr.io/vargafoundation/trica-ops` / appVersion | The `ops` image (`docker build --target ops .`): the server binary plus `pg_dump` 17. Build it with that command when you cannot pull it. |

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

## Collectors

With `collect.enabled`, the server collects from the listed targets (one replica at a time: the
others wait on an advisory lock). Devices are reached from the server pod: with
`networkPolicy.egressTo` set, list them there. Example, an Arista switch whose password lives in a
Secret you created (`kubectl create secret generic arista-credentials --from-file=password`):

```yaml
collect:
  enabled: true
  secrets: [{name: arista-credentials}]
  targets:
    - kind: arista
      name: sw-a1
      address: https://192.0.2.10
      username: monitor
      password: file:/var/run/secrets/collect/arista-credentials/password
```

For devices the cluster cannot reach, run `trica collector` next to them instead.

A `unifi` target reads the UniFi Site Manager cloud API, not a device: the server pod needs egress to
`api.ui.com:443` (an `ipBlock` of `0.0.0.0/0` on port 443 with `networkPolicy.egressTo` set, since a
NetworkPolicy cannot name a host, or an `HTTPS_PROXY`). Its API key is a file reference like any other,
for instance `collect.secrets: [{name: unifi-key}]` mounted at `/var/run/secrets/collect/unifi-key/key`.

A `unifi` target with `options.api: network` reads a console on the LAN, which the cluster usually cannot reach: run it with `trica collector` next to the console (ADR 0060).

## Live topology

With `live.enabled`, the topology can overlay link throughput and state read by the server from a
Prometheus-compatible endpoint; the browser never reaches it. With `networkPolicy.egressTo` set,
list the endpoint there. Example, with the password in a Secret you created
(`kubectl create secret generic metrics-credentials --from-file=password`):

```yaml
live:
  enabled: true
  secrets: [{name: metrics-credentials}]
  config:
    url: https://metrics.example.net/prometheus
    username: trica-reader
    password: file:/var/run/secrets/live/metrics-credentials/password
    sources:
      - kind: node
      - kind: arista
        matchers: {job: arista-exporter}
```

## Backup

A PostgreSQL operator with WAL archiving (CloudNativePG, Zalando, a managed service) takes its own
physical backups with point-in-time recovery: leave `backup.enabled` off there. The CronJob is for standalone
installs, where PostgreSQL has nothing of its own: it runs `trica backup --dir /backups` on the
schedule with the same database Secret as the server (a mounted file, no credential in values) and
writes `trica-<timestamp>.dump` to the claim. It never deletes: prune the volume yourself. For an
S3-compatible target use a claim backed by an S3 CSI driver, or copy the archives off with a job of
your own; the chart holds no object-storage credentials. Restore is attended, not scheduled:
see [`deploy/compose/README.md`](../../deploy/compose/README.md) for the procedure, which is the same
command with the same image (`kubectl run`, scale the server to 0 first) and
[ADR 0021](../../docs/adr/0021-backup-restore.md).

## Authentication (OIDC)

Everything under `config` is passed to the server as is ([configuration](../../docs/reference/configuration.md)).
Any OpenID Connect provider works; the UI is a public PKCE client, so no client secret exists:

```yaml
config:
  auth:
    oidc:
      issuer: https://sso.example.com/realms/example
      audience: [trica]
    group_roles:
      infra-admins: [admin]
  web:
    oidc:
      client_id: trica-ui
```

## Tests

```bash
helm lint charts/trica --set database.existingSecret=ci
helm unittest charts/trica
for f in charts/trica/examples/*.yaml; do helm template ci charts/trica -f "$f" | kubeconform -strict -summary; done
```
