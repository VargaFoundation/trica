# Server configuration

`trica server --config FILE` reads a YAML file; unknown keys are rejected. Environment
variables override it. `TRICA_CONFIG` names the file when `--config` is not given.

| Key | Environment | Default | Meaning |
|---|---|---|---|
| `database.url_file` | `TRICA_DATABASE_URL` (the URL itself, takes precedence) | none, required | File holding the PostgreSQL URL, typically a mounted Secret. The URL is never logged. |
| `http.addr` | `TRICA_HTTP_ADDR` | `:8080` | Listen address. |
| `http.shutdown_timeout` | | `20s` | Grace period for in-flight requests on SIGTERM. |
| `log_level` | `TRICA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Logs are JSON on stderr. |
| `migrate_on_start` | | `true` | Apply pending migrations before serving, under an advisory lock (ADR 0014). |
| `sweep_interval` | | `1m` | How often unseen objects are marked absent. |
| `schema_overlays` | | `[]` | YAML files with custom types, attributes and relations merged over the built-in schema (ADR 0003). A broken overlay stops the start. |
| `policy.priorities` | | `[]` | Per-attribute source priorities (ADR 0004). |
| `policy.propose` | | `[]` | Source globs whose batches wait for approval, e.g. `mcp:*`, `deduce:*` (ADR 0009). |
| `policy.absent_after` | | `30m` | How long an observed object may go unseen before it is marked absent (ADR 0007). |

### Authentication and limits

There is no anonymous mode: the API and the MCP server refuse a request without a valid
bearer credential (an API token `trica_<id>_<secret>`, or an OIDC JWT when `auth.oidc` is set).
Secrets are never keys of this file: the database URL is a file reference, and the OIDC client
secret does not exist (the UI is a public PKCE client).

| Key | Environment | Default | Meaning |
|---|---|---|---|
| `auth.oidc.issuer` | `TRICA_OIDC_ISSUER` | none (OIDC off) | Issuer of the identity provider; `https` required, discovery runs at start; an issuer that does not answer is retried on use (JWTs get a 503 meanwhile, API tokens work), a discovery document that contradicts the configuration stops the start ([ADR 0031](../adr/0031-oidc-issuer-down-at-start.md)). |
| `auth.oidc.audience` | | none, required with `auth.oidc` | Accepted audiences. |
| `auth.oidc.groups_claim` | | `groups` | Claim listing the user's groups. |
| `auth.policy_file` | | built-in `viewer` / `operator` / `admin` | RBAC policy file (roles and group mappings, unknown keys rejected). |
| `auth.group_roles` | | `{}` | Group to roles, merged over the policy. A role that does not exist stops the start. |
| `auth.rate_limit.rate` / `burst` | | `20` / `60` | Requests per second and burst, per principal (per peer address before authentication), shared by the API and the MCP server; over it, a 429. |
| `audit.rate_limited_every` | | `1m` | A 429 is recorded in the audit log once per caller and period; the next entry counts the others in `details.suppressed` ([ADR 0034](../adr/0034-refusals-audit-and-head-anchor.md)). |
| `audit.anchor_interval` | | `5m` | How often each replica logs the head of the audit chain (`audit chain head`, `head_id`, `head_hash`) and publishes `trica_audit_head_id`. An entry anchored at the previous read that is gone or changed is logged as an error and counted in `trica_audit_head_regressions_total`. |

### Components

| Key | Environment | Default | Meaning |
|---|---|---|---|
| `mcp.disabled` | | `false` | Leave `/mcp` unmounted. |
| `mcp.graph_refresh` | | `1m` | The dependency graph, read by the MCP graph tools and by the topology map (`GET /topology/map`, also with `mcp.disabled`), is rebuilt on the next read after a commit, made by this server or announced by another replica on the `trica_changes` channel (ADR 0032); and at most this late after a commit whose notification was lost (a listener reconnecting, a pooler in transaction mode). |
| `mcp.disable_localhost_protection` | | `false` | Accept loopback `Host` headers behind a same-host proxy. |
| `deduce.disabled` | | `false` | Stop the cabling deducer. |
| `deduce.interval` | | `5m` | Time between deduction passes; one replica deduces at a time. Also the time between device type library passes. |
| `web.disabled` | | `false` | Leave `/` unmounted. |
| `web.oidc.client_id` | | none | Public client of the UI; with `issuer` (defaults to `auth.oidc.issuer`), `scopes` and `bearer` (`access_token` or `id_token`), enables "Sign in with SSO" in the browser. |
| `collect.targets_file` | | none (no collector runs) | Targets the server collects from, on schedule, one replica at a time ([collectors](collectors.md), ADR 0026). A file that does not parse stops the start. |
| `devicetypes.extra_dir` | `TRICA_DEVICETYPES_EXTRA_DIR` | none (embedded library only) | Directory of your own device models and face images, laid out like the library (`<Vendor>/*.yaml`, `elevation-images/<manufacturer>/<slug>.<front\|rear>.<png\|jpg\|jpeg\|webp>`), read at start over the embedded library: a model with an embedded slug replaces it, an image replaces the embedded one ([device types](devicetypes.md#your-own-models-and-images), ADR 0042). Not a directory: the start is refused. An invalid file inside: the library is not loaded, the server runs. Chart: `deviceTypes.existingClaim`. |
| `transport.addr` | `TRICA_TRANSPORT_ADDR` | none (transport off) | Listen address of the mutual-TLS endpoint, a port apart from `http.addr` (ADR 0025). |
| `transport.ca_dir` | | none, required with `transport.addr` | Directory holding `ca.pem` and `ca-key.pem`, created on first start. Mount a persistent volume: losing the CA un-enrols every agent. |
| `transport.hosts` | | none, required with `transport.addr` | Names and IPs of the server certificate: what agents dial. |
| `transport.cert_ttl` | | `168h` | Validity of agent certificates. |
| `transport.server_cert_ttl` | | `2160h` | Validity of the server certificate; a new one is presented from the next handshake on after two thirds of it, without closing the listener or the open connections. A failed renewal is logged and retried within a minute. |

The device type library (ADR 0020) is matched against the devices at start, after the
migrations, then every `deduce.interval` (also when the deducer is disabled), so a device
discovered later gets its device type without a restart; a failed pass is logged, retried at the
next tick, and does not stop the server.

### Live topology overlay

Optional, and off unless the file has a `live` block. With it, the server reads link throughput
and state from a Prometheus-compatible query endpoint (Prometheus, VictoriaMetrics, Thanos,
Mimir: anything serving `/api/v1/query`) and serves them, joined to the inventory's interfaces, at
`GET /api/v1/topology/live` ([API](api.md), [ADR 0041](../adr/0041-live-topology-overlay.md)).
Without the block, that endpoint answers `enabled: false` and the UI draws the inventory only.
The browser never contacts the endpoint; the queries are built by the server from the names
below and nothing else, and no measurement is written to the inventory.

| Key | Default | Meaning |
|---|---|---|
| `live.url` | none, required | Base URL of the Prometheus HTTP API, `http` or `https`; the server queries `<url>/api/v1/query`. A path prefix is kept (`https://metrics.example.net/select/0/prometheus` for a VictoriaMetrics cluster, a reverse-proxy prefix). No credentials, query or fragment in it. |
| `live.username` / `live.password` | none (no authentication) | HTTP basic authentication. The password is a reference, `file:/path` or `env:NAME`, read at each query; a plain value stops the start. |
| `live.bearer_token` | none | A reference to a bearer token, for an endpoint behind a token-checking proxy. Not combined with basic authentication. |
| `live.refresh` | `30s` | How long one measurement set is served to every caller before the endpoint is queried again (at least `5s`). A failing endpoint is also retried at most once per period. |
| `live.timeout` | `5s` | Bound of one query. |
| `live.rate_window` | `2m` | Range of `rate()` over the counters, whole seconds, at least `30s`; it should cover at least two scrape intervals. |
| `live.tls.ca_file` / `insecure` / `min_version` | system roots, verified, TLS 1.2 | Verification of the endpoint, as for a collector target. |
| `live.sources` | none, at least one | The exporters to read. Order matters: when two sources measure one interface, the first one wins. |
| `live.sources[].kind` | none, required | `arista`, `mikrotik`, `pfsense`, `node` (defaults below) or `generic` (no defaults: metrics and labels required). |
| `live.sources[].name` | the kind | Name shown in the answer and in the metrics; unique; letters, digits, `_`, `-`. |
| `live.sources[].metrics.rx` / `tx` | the kind's | Counters of received and sent bytes (or bits, see `unit`). The server asks `rate(<metric>{<matchers>}[<rate_window>])`. `-` disables one. |
| `live.sources[].metrics.oper` | the kind's | Gauge, `1` when the link is up, `0` when it is down. The server asks `<metric>{<matchers>}`. `-` disables it. |
| `live.sources[].labels.device` | the kind's | Labels naming the device, tried in order. A value is matched against the device's `mgmt_ip`, `bmc_ip`, `primary_ip` and `name`: an address with or without a port (`192.0.2.11:9100`, `[2001:db8::1]:9100`, a URL), or a name, case-insensitive, fully qualified or not (`SRV-01.example.net` meets `srv-01`). A key two devices share is ignored. |
| `live.sources[].labels.interface` | the kind's | Label naming the interface; compared case-insensitively, with the EOS abbreviations expanded (`Et1` meets `Ethernet1`, `Po10` meets `Port-Channel10`). |
| `live.sources[].matchers` | none | Label equalities added to every query of the source (`{job: arista-exporter}`); values are quoted by the server. |
| `live.sources[].exclude_interfaces` | the kind's | Regular expression (Go syntax); series whose interface matches it are ignored. `-` excludes nothing. |
| `live.sources[].unit` | `bytes` | `bytes` (multiplied by 8) or `bits`. |

The defaults of each kind are the exporters' own names; they assume nothing about how the metrics
are scraped, relabelled or stored:

| Kind | rx / tx | oper | Device labels | Interface label | Excluded |
|---|---|---|---|---|---|
| `arista` | `arista_port_inOctets` / `arista_port_outOctets` | `arista_l2_up` | `device`, `instance` | `exported_device` | |
| `mikrotik` (mktxp) | `mktxp_interface_rx_byte_total` / `mktxp_interface_tx_byte_total` | `mktxp_interface_running` | `routerboard_address`, `routerboard_name` | `name` | |
| `pfsense` | `pfsense_interface_in_bytes` / `pfsense_interface_out_bytes` | `pfsense_interface_up` | `host`, `instance` | `hwif` | |
| `node` (node-exporter) | `node_network_receive_bytes_total` / `node_network_transmit_bytes_total` | `node_network_up` | `node`, `instance` | `device` | `^(lo\|veth.*\|lxc.*\|cilium.*)$` |

An example with placeholder values (none of them is a default):

```yaml
live:
  url: https://metrics.example.net/prometheus
  username: trica-reader
  password: file:/run/secrets/live/password
  refresh: 30s
  timeout: 5s
  rate_window: 2m
  tls: {ca_file: /etc/trica/metrics-ca.pem}
  sources:
    - kind: arista
      matchers: {job: arista-exporter}
    - kind: mikrotik
    - kind: pfsense
      labels: {device: [instance]}
    - kind: node
      exclude_interfaces: '^(lo|veth.*|docker.*|br-.*)$'
    # Any other exporter, here SNMP interface counters, by explicit names.
    - kind: generic
      name: snmp
      metrics: {rx: ifHCInOctets, tx: ifHCOutOctets, oper: "-"}
      labels: {device: [instance], interface: ifName}
      matchers: {job: snmp}
```

The server publishes `trica_live_upstream_queries_total{source,outcome}`,
`trica_live_upstream_query_duration_seconds`, `trica_live_refreshes_total{outcome}` and
`trica_live_series{source,state="matched|unmatched"}` on `/metrics`: a source whose series are all
`unmatched` names devices or ports the inventory does not know under those labels. The answer's
`sources` gives the same counts to a caller who reads every interface.

With the Helm chart, the block is `live.config` (checked by the chart's values schema), enabled by
`live.enabled`, its credentials mounted from the Secrets listed in `live.secrets` at
`/var/run/secrets/live/<secret>/<key>` ([chart README](../../charts/trica/README.md#live-topology)).

## Endpoints

| Path | Purpose |
|---|---|
| `GET /healthz` | Liveness: the process serves HTTP. Does not touch the database. |
| `GET /readyz` | Readiness: started and the database answers within 2 s. |
| `GET /metrics` | Prometheus metrics: `trica_build_info`, `trica_absence_sweeps_total`, `trica_absence_sweep_errors_total`, `trica_objects_marked_absent_total`, `trica_deduce_runs_total`, `trica_deduce_errors_total`, `trica_deduce_skipped_total`, `trica_deduce_cables`, `trica_deduce_findings`, `trica_deduce_last_success_timestamp_seconds`, `trica_deduce_duration_seconds`, `trica_audit_head_id`, `trica_audit_head_regressions_total`, `trica_audit_anchor_errors_total`, `trica_audit_anchor_last_success_timestamp_seconds`, Go and process collectors. |
| `/api/v1/` | The REST API ([reference](api.md)). Bearer token or OIDC JWT. |
| `/mcp` | The MCP server, streamable HTTP ([reference](mcp.md)). Bearer token or OIDC JWT. |
| `/` | The web UI, served from the binary when built with the `webui` tag; otherwise a placeholder page says so. `/ui-config.json` tells the browser how to sign in. |
| `transport.addr` | Separate listener, mutual TLS: `POST /v1/enrol`, `/v1/renew`, `/v1/batches` ([reference](transport.md)). |

## Commands

| Command | Effect |
|---|---|
| `trica server` | Serve. Stops gracefully on SIGTERM or SIGINT; stops with an error if a component fails. |
| `trica migrate up` | Apply pending migrations. |
| `trica migrate down` | Roll back the latest migration only. |
| `trica migrate status` | Print the schema version. |
| `trica token create --name N --owner SUBJECT --roles R1,R2 --scopes read\|write\|admin [--types …] [--sites …] [--source kind:name] [--expires 720h]` | Create an API token; the token is the only thing on stdout, shown once, the metadata goes to stderr. Roles must exist in the configured policy ([security](security.md#minting-the-first-token)). |
| `trica token list` | List the tokens (id, name, owner, roles, scopes, restrictions, dates). Never a secret or a hash; revoked tokens stay listed. |
| `trica token revoke --id N` | Revoke a token at once; the row is kept. |
| `trica enrol create --binding KIND:NAME \| --prefix KIND [--ttl 24h]` | Create a one-time enrolment token, or with `--prefix` a class token that enrols each name under the prefix once (ADR 0035), for an agent or collector ([transport](transport.md)); prints it once on stdout, and its id and where the CA certificate is on stderr. |
| `trica enrol revoke --id HEX` | End an enrolment token now, by the id printed at its creation. Certificates already issued stay valid. |
| `trica version` | Print the version. |

`token` and `enrol` take `--config` (or `TRICA_CONFIG`) and read the database URL like `migrate`
(`TRICA_DATABASE_URL`, else `database.url_file`). The schema must be migrated: start the server
once, or run `trica migrate up`.
