# Transport reference

Packages `internal/transport` (server, client, spool, registry) and `internal/pki` (CA, binding).
Design: [ADR 0008](../adr/0008-transport.md). Status of what is and is not proven:
[STATUS](../plan/STATUS.md), row 15.

`trica server` serves it when `transport.addr` is set, on a listener of its own, apart from the
HTTP port ([configuration](configuration.md), [ADR 0025](../adr/0025-server-wiring.md)). The CA
is loaded from or created in `transport.ca_dir` (a persistent volume), the server certificate is
issued for `transport.hosts` at start and renewed by reopening the listener after two thirds of
`transport.server_cert_ttl`, and batches go to `reconcile.Engine.Submit` as source-bound by the
certificate. The agent binary does not use the client yet (see [STATUS](../plan/STATUS.md)).

## Trust model

- The server runs an internal CA (ECDSA P-256). It is created or loaded with
  `pki.LoadOrCreateCA(dir, ...)` (files `ca.pem` 0644 and `ca-key.pem` 0600 in a 0700
  directory) or loaded by reference with `pki.LoadCA(certPath, keyPath)`. The key is never
  logged and never leaves the server.
- TLS 1.3 only, HTTP/2 (`h2`) negotiated by ALPN. One clock (the registry's, injectable) drives
  both the handshake and the per-request validity check.
- A client trusts the server through a CA certificate file handed over out of band
  (`ClientConfig.CAFile`, required). The CA in the enrolment response is informative and is
  never trusted: there is no trust on first use.
- Client certificates are short-lived (default 7 days). Their identity is a URI SAN
  `urn:trica:source:<binding>`; the subject and any identity requested in the CSR are ignored.

## Binding rule

A binding is the source prefix a certificate may report as: at least two segments separated by
`:`, each `[A-Za-z0-9._-]+`, at most 128 bytes (`agent:host-01`, `collector:site-a`).

A batch is accepted when `batch.Source` equals the binding, or starts with `binding + ":"`.
`collector:site-a` may therefore report `collector:site-a` and `collector:site-a:switches`, never
`collector:site-ab` nor `agent:host-01`. Anything else is a 403, before the batch is validated.

## Endpoints

| Endpoint | Auth | Request | Success | Errors |
|---|---|---|---|---|
| `POST /v1/enrol` | server-side TLS only (no client certificate) | JSON `{"token": "...", "csr": "<PEM>"}` | 200 `{"certificate": "<PEM>", "ca": "<PEM>"}` | 400 malformed body, missing token or bad CSR (the token is not consumed); 401 unknown, used or expired token (not distinguished); 500 |
| `POST /v1/renew` | valid client certificate | JSON `{"csr": "<PEM>"}` | 200, same shape; same binding as the presenting certificate | 400 bad CSR; 401 no or revoked or expired certificate; 503 revocation list unreadable |
| `POST /v1/batches` | valid client certificate | gzip JSON `sdk.Batch`, header `Content-Encoding: gzip` | 204 | 400 invalid gzip, JSON or batch; 401; 403 source outside the binding; 413 too large; 415 not gzip; 422 refused by the sink; 500; 503 |

Limits (`ServerConfig`): `MaxBodyBytes` 8 MiB as received, `MaxBatchBytes` 64 MiB once
decompressed (a gzip bomb gets a 413). The CSR must be signed (proof of possession) and use
ECDSA P-256.

Every request with a certificate is checked against `revoked_certs` in PostgreSQL, one query per
request. If the query fails the request is refused with a 503 (fail closed).

The sink is an injected `transport.Sink` (`Ingest(ctx, *sdk.Batch, Identity) error`). Production
wiring (`internal/wiring/transport.go`): a sink that calls `reconcile.Engine.Submit` with the
binding as the submitter, mapping `reconcile.ErrRejected` and `ErrInvalid` to `ErrRejected`. A sink
returning `transport.ErrRejected` yields a 422; any other error a 500 whose detail is not sent to
the client. The transport package never writes inventory tables.

## Operator: create an enrolment token

There is no CLI yet. From Go, with a `Registry` on the server's pool:

```go
reg := transport.NewRegistry(pool)
token, err := reg.NewEnrolmentToken(ctx, "agent:host-01", time.Hour) // ttl 0 = transport.DefaultTokenTTL
```

The token (256 random bits) is returned once and only its SHA-256 is stored (`enrol_tokens`).
It works once (`used_at`), until `expires_at`, and only for its binding. Give the machine the
token and the CA certificate file (`ca.pem`, public).

Revocation: `reg.Revoke(ctx, serial, reason)`, or `reg.RevokeBinding(ctx, binding, reason)` for
every certificate issued to a binding (`reg.IssuedTo` lists them). A revoked certificate is
refused at the next request, including on renew; the binding can be enrolled again with a new
token.

Tables (migration `00003_transport.sql`): `enrol_tokens`, `issued_certs`, `revoked_certs`.

## Client

```go
c, err := transport.NewClient(transport.ClientConfig{
    ServerURL: "https://trica.example.test:8443", CAFile: "/etc/trica/ca.pem",
    Dir: "/var/lib/trica-agent", EnrolToken: token, // or EnrolTokenFile
})
go c.Run(ctx, 30*time.Second) // background flush of the spool
err = c.Emit(ctx, batch)      // transport.Client implements sdk.Emitter
```

- Identity: key and certificate in one file `Dir/identity.pem` (0600), written atomically.
  Without one, the client enrols with the token on first use (no network in `NewClient`).
- Renewal: when less than a third of the validity remains, with a new key each time. A failed
  renewal is tolerated while the current certificate is valid, and is an error once it expired.
- Emit order: invalid batch (checked locally) returns an error, nothing sent or spooled.
  Otherwise the spool is flushed first (order preserved), then the batch is posted.

## Spool

| Outcome of a send | Behaviour |
|---|---|
| network or TLS failure, 5xx, 408, 429 | batch written to the spool, `Emit` returns nil |
| other 4xx | not spooled, `Emit` returns a `*StatusError` |

408 and 429 are treated as transient on purpose: retrying is what they ask for. This is the one
deviation from "4xx is never spooled".

- One file per batch (`<20 digit sequence>.batch`), temp file, fsync, rename, directory sync.
  The sequence is monotonic across restarts and clock changes. Leftover temp files are removed
  when the spool is opened.
- Total size bounded (`MaxSpoolBytes`, 64 MiB by default). When full the oldest batches are
  removed first and counted in `Stats().Dropped`. A single batch larger than the bound is dropped
  and `Emit` returns `ErrSpoolFull`. The counter lives in memory: it is not persisted.
- `Flush(ctx)` (called by `Run`) resends oldest first and stops at the first transient failure.
  A 401 stops the flush and keeps the spool (the identity may be fixed by an operator). Any other
  4xx discards that batch and counts it in `Stats().Rejected`; so does an unreadable file.

## Not done

Per-source rate limiting (only body size limits), CLI, binaries, CA rotation, remote update
and configuration long-poll (ADR 0008, later stories).
