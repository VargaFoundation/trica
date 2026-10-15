# ADR-0033: The agent writes its metrics to a file, it opens no port

- **Status**: accepted (2026-10)
- **Concerns**: `cmd/trica-agent` (`--metrics-file`), `internal/transport/uplink` (`Stats`),
  `docs/reference/agent.md`. Closes the "not done" of ADR 0029; builds on ADR 0027.

## Context

An agent whose server is out of reach spools its batches (ADR 0027) and still exits 0: the
spool is the only trace, and nothing told an operator it was growing until it was full and
batches were lost (issue #65). The remote collector exposes its spool on its `/metrics`
endpoint; the agent has none. It runs on every host, as root in the DaemonSet, and talks
only outbound (ADR 0027): a listener on every host is a new attack surface, a port to
allocate on `hostNetwork` pods, and a firewall rule per host.

## Decision

The agent writes its transport metrics to a file, in the Prometheus text format, for the
node exporter's textfile collector, which runs on these hosts anyway.

- `--metrics-file` (or `TRICA_AGENT_METRICS_FILE`); it needs `--server`. Written every flush
  interval (one minute) and at exit by a running agent, after the collection with `--once`;
  replaced atomically (temporary file in the same directory, rename), mode 0644, no secret.
- The number to alert on is the **age of the oldest spooled batch** (the batch file names
  are their emission time), not the count: a count says nothing about how long the server
  has been gone. The file also carries the size, sent, dropped and rejected batches, the
  last accepted delivery, its own write time (a stale file is a stopped agent) and the
  version.
- The format is written by hand: the agent does not link the Prometheus client library for
  nine lines of text. The tests parse the file with the Prometheus text parser.

## Consequences

- No port, no new dependency in the agent binary.
- The operator wires the file to a node exporter: a systemd drop-in giving the unit the
  textfile directory, or, in the DaemonSet, a directory under the one writable host path.
  Neither is done by the packages or the example manifest.
- Counters restart with the process, as an endpoint's would; with `--once`, they count one
  run.
- Not done: the same age gauge on the remote collector (`trica_collector_spooled_batches`
  is still a count only).
