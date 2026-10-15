# ADR-0029: Probation requires a delivery when the server was reachable

- **Status**: accepted (2026-10)
- **Concerns**: `cmd/trica-agent` (`probation`), `docs/reference/agent.md`.
  Amends the probation rule of ADR 0027; builds on ADR 0022 (packaging and update).

## Context

ADR 0027 counted a collection as healthy when `Emit` returned nil, that is when the batch was
accepted **or spooled**, so that a server outage would not roll back a good version. The cost
was named there: a new version whose transport only spools (a broken certificate, a changed
protocol) passes probation and is kept, while its batches pile up (issue #65).

## Decision

A version on probation is confirmed by an emitted batch **and**, when the server was reachable
before the update, by an accepted delivery within `--health-timeout`.

- **"Reachable before" is an empty spool at start.** The previous version delivered all it
  collected, so it reached the server. The agent records the number of accepted batches at
  start; the version is healthy once that number has grown. A delivery from the spool counts:
  the background flush can deliver after the emit, and the decision is taken again at the
  health timeout.
- **A spool already waiting means the server was down before the update.** The previous
  version could not reach it either, so the new one cannot be blamed: an emitted batch is
  enough, as in ADR 0027. Without `--server`, nothing changes: an emitted batch is enough.
- A refused batch (4xx) is still not healthy.

## Consequences

- A version that cannot reach a server the previous version reached is stopped at the health
  timeout and rolled back on the next start, then marked bad on this host.
- **Accepted cost.** A server outage that starts during the probation window and lasts past
  `--health-timeout` (2 minutes by default) rolls back a good version and marks it bad on this
  host; the server will not offer it there again. The window is short, and an update wave is
  rolled out while the server is up, since the server is what offers it.
- The default health timeout is longer than the flush interval (one minute), so one flush
  happens inside the window.
- ~~Not done: spool size and age as metrics (#65 proposes them).~~ Done by ADR 0033: the
  agent writes them to a file for the node exporter (`--metrics-file`).
