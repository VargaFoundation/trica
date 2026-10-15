# ADR-0027: The agent sends its batches through the transport client

- **Status**: accepted (2026-10)
- **Concerns**: `cmd/trica-agent`, `packaging/`, `deploy/kubernetes/`, `docs/reference/agent.md`.
  Builds on ADR 0008 (transport), 0015 (agent inventory), 0022 (packaging and update).

## Context

The transport client (`internal/transport.Client`, now `internal/transport/uplink.Client`: enrolment, mutual TLS, disk spool) existed
and no binary used it: `trica-agent` wrote to stdout or a file, so a deployed agent inventoried
nothing (issue #59).

## Decision

- `--server`, `--ca-file` and `--enrol-token-file` (and `TRICA_AGENT_SERVER`,
  `TRICA_AGENT_CA_FILE`, `TRICA_AGENT_ENROL_TOKEN_FILE`) make the agent emit through
  `uplink.Client`. The token is only ever a file reference. `--server` needs `--ca-file` and
  `--state-dir`, and `https://`: a client that trusted what a server presented on first contact
  would make enrolment pointless, and an identity with nowhere to live would enrol at every start.
- The identity (`identity.pem`) and the spool live in `--state-dir`, next to the staged versions.
  No second flag: one directory is the one the unit and the manifest already make writable.
- The source stays `agent:<hostname>`, the binding the token grants. A host whose name differs
  from the token's binding enrols and is then refused (403) on every batch, not spooled.
- `--output` stays for offline use. With `--server`, stdout is not written; an explicit `--output`
  is a local copy written in addition.
- A background goroutine (`Client.Run`, every minute) flushes the spool and renews the
  certificate between two collections of a long-running agent.
- **Probation.** A collection counts as healthy when `Emit` returned nil: the batch was accepted
  or spooled. A server outage must not roll back a good version. A refused batch (4xx) is not
  healthy. Accepted cost: a version whose transport only spools is confirmed; the spool size is
  the signal to watch.
- **DaemonSet.** The identity must outlive the pod, so the state directory is a `hostPath`
  (`/var/lib/trica-agent`, the one writable host path; `packaging_test.go` pins it). Enrolment
  tokens are one per node, as a file on the node, placed at provisioning: a token grants one
  binding and `pki.ValidBinding` refuses a wildcard, so no shared Secret can work. `pki` is not
  weakened.

## Consequences

- Enrolling a fleet by hand is slow, and no command mints a token yet (STATUS row 35). A
  token-less or class-based enrolment (Kubernetes TokenReview, a prefix class token) needs a
  server change in `internal/pki` and `internal/transport`; it is a finding, not part of this
  decision.
- The agent binary first linked `internal/transport` (and with it pgx, through the registry in
  the same package): measured at 15.6 MB peak resident and 0.10 % of a core with the transport
  on, the same as without. Since #64 the client lives in `internal/transport/uplink` and the
  wire contract in `internal/transport/wire`; the agent links neither the server nor pgx
  (`TestAgentDoesNotLinkTheServer`), and its stripped binary went from 7.72 MB to 7.40 MB.
