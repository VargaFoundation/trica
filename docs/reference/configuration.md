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

## Endpoints

| Path | Purpose |
|---|---|
| `GET /healthz` | Liveness: the process serves HTTP. Does not touch the database. |
| `GET /readyz` | Readiness: started and the database answers within 2 s. |
| `GET /metrics` | Prometheus metrics: `trica_build_info`, `trica_absence_sweeps_total`, `trica_absence_sweep_errors_total`, `trica_objects_marked_absent_total`, Go and process collectors. |

## Commands

| Command | Effect |
|---|---|
| `trica server` | Serve. Stops gracefully on SIGTERM or SIGINT; stops with an error if a component fails. |
| `trica migrate up` | Apply pending migrations. |
| `trica migrate down` | Roll back the latest migration only. |
| `trica migrate status` | Print the schema version. |
| `trica version` | Print the version. |
