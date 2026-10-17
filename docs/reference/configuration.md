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
| `mcp.graph_refresh` | | `1m` | The dependency graph is rebuilt on the next graph tool call after a commit, made by this server or announced by another replica on the `trica_changes` channel (ADR 0032); and at most this late after a commit whose notification was lost (a listener reconnecting, a pooler in transaction mode). |
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
