# ADR-0028: An API token may be bound to a source

- **Status**: accepted (2026-10)
- **Concerns**: `internal/auth`, `internal/wiring`, `cmd/trica` (`token create`), migration
  `00005_token_source.sql`. Amends ADR 0013 (tokens) and builds on ADR 0004 (source priorities).

## Context

Every batch is attributed to one source, and source priorities (ADR 0004) rank claims by
source name. A user speaks as `user:<sub>`; a transport client as its certificate binding. An
API token had no source of its own: the server spoke for it as `api:token-<id>` (#53). A
collector pushing through the REST API with a token could therefore not be ranked as what it is
(`arista:sw-a1`), and its claims changed name if the token was rotated.

## Decision

- A token carries an optional `source`, set at creation (`trica token create --source`) and
  never changed afterwards. Its batches are attributed to that source; without one, the derived
  `api:token-<id>` is kept, so existing tokens and their claims are untouched.
- The format is that of a transport binding (`internal/pki.ValidBinding`): `kind:name`, two
  segments at least, so a token cannot speak for a whole kind. The database checks it too.
- The kinds `user` and `agent` are refused. `user:` is how people's own batches are attributed;
  `agent:` sources are bound by a certificate on the transport (ADR 0027). A token speaking as
  either would pass for a person or a host.
- A token speaks as exactly its source, not as sub-sources below it (unlike a transport binding,
  `pki.Allows`): a batch body's own `source` is still ignored by the API.

## Consequences

- Two tokens may be bound to the same source (rotation without a gap). Their claims then merge,
  as they would for one collector restarted with a new token: that is the point.
- A token bound to the source of a running collector can overwrite that collector's claims. Only
  an `admin` creates tokens, and the binding is in the audit log entry of every submission.
- An unbound token is an API caller for the engine: the `api` kind, like `user` and `token`, is
  strict on schema errors and limited to 15 minutes of `observed_at` age (ADR 0005, #75). The
  derived name is not renamed, so existing claims keep their source.
- A token bound to a source is classified by that source's kind: bound to `arista:sw-a1`, it
  gets a collector's leniency (a warning, 7 days of age), because it stands for that collector.
