# ADR-0008: Agent and collector transport, HTTP/2 over mutual TLS with a disk spool

- **Status**: accepted (2026-10); supersedes the gRPC and bbolt option of the initial plan
- **Concerns**: `internal/transport` (server), `internal/transport/uplink` (client), `internal/transport/wire`, `cmd/trica-agent`, remote collectors

## Context

Agents and remote collectors push batches to the server, outbound only, must survive server
outages, and must be updatable remotely in waves with rollback.

## Decision

- HTTP/2 over mutual TLS. Batches are gzip-compressed JSON, the same `sdk.Batch` encoding the
  API accepts; no code generation, one encoding to debug.
- The server runs an internal CA. An agent enrols with a one-time token and receives a
  short-lived client certificate it renews itself; its source name is bound to the certificate,
  so an agent cannot report as another.
- While the server is unreachable, batches are written to a spool directory as one file each,
  bounded in size, oldest dropped first with a counter.
- Configuration and updates are fetched by long-poll; an update is a signed binary, applied by
  wave, with automatic rollback when the new version does not check in.

## Consequences

- No bbolt dependency and no gRPC toolchain; the agent stays small.
- Streaming is not needed: a batch is a complete report.

## Details settled in the implementation

Reference: [transport](../reference/transport.md).

- The binding is a source prefix of at least two segments (`agent:host-01`); a batch source
  equals it or starts with `binding:`. It travels in a URI SAN, `urn:trica:source:<binding>`.
- 408 and 429 are retried through the spool like a 5xx; every other 4xx is final and not spooled.
- Revocation is a PostgreSQL lookup on every request, failing closed.
- Per-source rate limiting is not implemented yet; only body size limits are.
