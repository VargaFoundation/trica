# ADR-0014: One server process assembling components, migrations on start under a lock

- **Status**: accepted (2026-10)
- **Concerns**: `internal/server`, `cmd/trica`, `charts/trica`, `deploy/compose`

## Context

The API, the MCP server, the transport endpoint, the cabling deducer and the absence sweeper
all need the same store, engine and schema, and must start and stop together. Installation
must take under 30 minutes (lot 1 acceptance), which rules out a separate migration step that
operators forget. Several replicas may start at the same time.

## Decision

- `trica server` is one process. `internal/server` owns the lifecycle: configuration, store,
  migrations, engine, schema overlays, the HTTP listener, `/healthz`, `/readyz`, `/metrics`
  and the absence sweep. Everything else is a `Component` with an optional `Mount` (routes on
  the shared mux) and an optional `Run` (a background loop). A failing `Run` stops the server:
  a half-running inventory that silently stopped deducing cables is worse than a restart.
- Migrations run on start by default (`migrate_on_start`), under a PostgreSQL advisory lock:
  concurrent replicas take turns. `trica migrate up|down|status` exists for operators who
  prefer a separate job; `down` rolls back one migration, never all of them.
- The database URL is a secret: it comes from `TRICA_DATABASE_URL` or a file
  (`database.url_file`, a mounted Secret), never from the configuration file, and is never
  logged. The chart mounts it as a file and refuses to render without it.
- `/healthz` is liveness and does not touch the database; `/readyz` pings it. A database
  outage takes the pod out of the Service without restarting it.

## Consequences

- New components are listed by `internal/wiring` (`Components()`, called from
  `cmd/trica/components.go`, [ADR 0025](0025-server-wiring.md)); they do not open their own store.
- The image is a static binary on distroless, non-root, read-only root filesystem.
- The sweep and the migrations are safe with several replicas; components with their own
  singleton loops (the deducer) must take their own advisory lock or run in one replica.
