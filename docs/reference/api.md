# REST API

`internal/api` serves the inventory over HTTP under `/api/v1`. The OpenAPI 3.1 document is **not
written by hand**: `GET /api/v1/openapi.json` generates it, at each request, from the route table
and the schema registry, so a custom type or attribute is in the document (and in `/schema`) as
soon as it is in the registry, with no restart. A test compares the document with the routes the
server actually serves, and validates real traffic (requests and responses) against the document.

`trica server` mounts it at `/api/v1/` (`internal/wiring`, [ADR 0025](../adr/0025-server-wiring.md)):
the `Authorizer` is `internal/auth` (API tokens and OIDC JWTs, RBAC policy), the `Auditor` is the
hash-chained audit log, and a rate limit applies. `api.New(Config)` returns a `*Server`;
`Handler()` serves paths under `api.Prefix`. A token speaks as the source it is bound to
(`trica token create --source arista:sw-a1`), or as `api:token-<id>` when it has none
([ADR 0028](../adr/0028-token-bound-source.md)). People and unbound tokens are strict callers: a
value the schema refuses rejects the whole batch with 422; a bound token is treated as the
collector it stands for, and gets a warning instead ([ADR 0005](../adr/0005-single-writer.md)).

## Endpoints

| Method and path | Action | What it does |
|---|---|---|
| `GET /healthz`, `GET /readyz` | none | Liveness; readiness (the database answers). No credential needed |
| `GET /openapi.json` | read | The generated OpenAPI document |
| `GET /me` | read | Who the caller is and which actions it holds globally (`actions`). The web UI uses it to decide which controls to offer; a grant limited to some types or sites can still be refused on one object, and every call is checked again |
| `GET /health/collection` | read | Last run of every source |
| `GET /schema`, `GET /schema/types/{type}` | read | Types, attributes, relation types (built-in and overlay) |
| `GET /objects` | read | List, with `q` (TQL), `type`, `sort`. Pages |
| `GET /relations` | read | The current relations of some types across the inventory, both ends named (`from`, `to`: id, type, display, parent, site). `type` is required: a comma list of at most 8 registered relation types. Listed only if the caller may read both ends. Pages, ordered by id; scans at most 5,000 candidates. Built for views that need the links in bulk (the topology page) |
| `GET /topology/live` | read | The live overlay of the topology (ADR 0041): `{enabled, generated_at, stale, refresh_seconds, links: {<interface id>: {rx_bps, tx_bps, oper, util, speed_mbps, source, aggregated, members}}, sources}`. Read by the server from the configured metrics endpoint ([configuration](configuration.md#live-topology-overlay)), cached for `refresh_seconds` and shared by every caller; `stale: true` when the last refresh failed, with the last good values (or none). `util` is `max(rx, tx)` over `speed_mbps`, absent when the speed is unknown. An aggregate (LAG) without series of its own is the sum of its members (`aggregated: true`, `members`): the members are the `member_of` relations from an interface to an interface that `GET /relations?type=member_of` lists, with both ends' `parent_id` (the device) to group them by. Only interfaces the caller may read are listed; `sources` only for a caller who reads interfaces everywhere. `enabled: false` and nothing else without a `live` block. Nothing is stored |
| `GET /objects/{id}` | read | One object; `at=<RFC 3339>` for the state at a date |
| `GET /objects/{id}/provenance` | read | Source, confidence and age of each attribute |
| `GET /objects/{id}/history` | read | Values over time; `key` restricts to one attribute |
| `GET /objects/{id}/series` | read | One measurement over time (ADR 0045): `key` (an attribute its type declares `series: true`, else 400 `not-a-series`), `from` and `to` (RFC 3339, the last 24 h by default, at most 366 days), `points` (buckets, 1 to 1000, 120 by default). Answers `{unit, from, to, step_seconds, points: [{t, avg, min, max}], truncated}`: `avg` is weighted by how long each value held in the bucket, a value still current holds until now, a bucket with no value is left out (a gap). `truncated: true` when the range held more than 50 000 readings: its end is missing |
| `GET /objects/{id}/faces` | read | A device drawn with its parts in place (ADR 0047): `{model, model_name, faces: [{face, image, width, height, layout_origin, spots: [{kind, name, x, y, w, h}]}], parts: [{part, kind, face, spot}], truncated}`. `faces` are the faces of the device's model that have an image (front first), `width`/`height` 0 and `spots` empty without a layout; `parts` are the device's parts (three levels down, 2000 at most, `truncated` beyond) the caller may read, not absent, of the kinds the faces have spots for, with the spot their name says or no `face`/`spot` when it says none ([rules](devicetypes.md#where-the-parts-sit)). Empty lists for an object without a model or a model without images |
| `GET /objects/{id}/device-type-candidates` | read | How sure the inventory is of a device's model, and the models it may be (ADR 0048): `{device, vendor, model, part_number, current: {model, slug, model_name, source}, library_match: {slug, model, how}, certain, reason, declared_by, sku_conflict, declarations: [{model, slug, source, last_seen}], candidates: [{slug, model, vendor, u_height, full_depth, airflow, front_image, rear_image, how, current, library_match}], can_choose}`. `reason` is what decided (`declared`, `sku`, `model`, `mounting`, `part_number`) or why it is not certain (`base`, `airflow`, `ambiguous`, `none`), see [devicetypes](devicetypes.md#how-sure-a-match-is-and-the-variants-a-person-can-choose). A person chooses by sending a partial batch that relates the device (by id) to the `device_type` (by `slug`) with `instance_of`: it wins over the library's match by name and survives later collections; a part number naming another chassis is `sku_conflict`, not applied. `can_choose`: the caller holds `propose` on devices in the device's site and on device models. 404 for an object that is not a device |
| `GET /objects/{id}/airflow` | read | The airflow check of a device (ADR 0049): `{device, rack, face, front_aisle, model, model_airflow, model_declared, device_airflow, parts: [{part, airflow}], check: {status, reason, airflow, airflow_from, intake, cold_face, warnings}}`. `status` is `ok`, `mismatch` (takes its air in from the hot aisle) or `unknown`; `reason` says why. 404 for an object that is not a device |
| `GET /objects/{id}/relations` | read | Relations now, or `at` a date |
| `GET /racks/{id}/contents` | read | The devices mounted in a rack, each with `position`, `face`, `model` (its `instance_of`), `u_height` and `full_depth` (from the model; when the model is unknown or has no height, `u_height` is the device's own, ADR 0039): what an elevation needs, in one call; also `model_name`, the device's `role` and `health`, `parts_health` (the worst `ok` / `warning` / `critical` among the device's parts the caller may read, absent parts left out, three levels deep) and `front_image` / `rear_image` (API paths of the model's face images, when the library has them). A device is listed only if the caller may read it, its model only if the caller may read the model. Ordered by position, unknown positions last, then by name. Pages. 404 for an object that is not a rack. `variant_uncertain` (with `variants`, their number) when the library is not sure which chassis the device is and nobody declared it; a declared model wins over the library's match (ADR 0048). Each item also carries `airflow`, the device's airflow check against the rack's `front_aisle` (same shape as the `check` of `GET /objects/{id}/airflow`, ADR 0049) |
| `GET /device-types/{slug}/{face}` | read | The image of one face (`front`, `rear`) of a library model, or of the operator's directory (`devicetypes.extra_dir`): `image/png`, `image/jpeg` or `image/webp`, `Cache-Control: private, max-age=3600`, strong `ETag`, `304` on `If-None-Match`, `Content-Security-Policy: default-src 'none'; sandbox`. Library content, not inventory: any reader gets it. 404 when the model has no image of that face, 400 for another face ([device types](devicetypes.md#face-images)) |
| `GET /objects/{id}/timeline` | read | Changes that touched the object (`from`, `to`) |
| `GET /search` | read | `q` free text or TQL: counts per type first, then a page |
| `GET /whohad` | read | `key`, `value`, optional `at`: which objects had it |
| `GET /changes` | read | Change feed between `from` and `to` (optional `object`) |
| `POST /batches` | propose | Submit an `sdk.Batch`; 200 when applied, 202 when queued as a proposal |
| `POST /batches/preview` | propose | What the batch would do, nothing written |
| `GET /proposals`, `GET /proposals/{id}` | read | Proposals; a pending one carries a preview against the current state |
| `POST /proposals/{id}/approve`, `.../reject` | approve | Decide a pending proposal (409 if already decided) |
| `GET /discrepancies` | read | Declared versus observed, filters `status`, `kind`, `object` |
| `POST /discrepancies/{id}/accept` | approve | Accept the difference, with a reason |
| `GET/POST /saved-queries`, `GET/DELETE /saved-queries/{name}` | read / propose | Saved queries; delete by the owner, or by an `admin` |

## Writes go through the engine

The API never writes the inventory itself: `POST /batches` hands an `sdk.Batch` to the
reconciliation engine, which decides to create, update, propose or flag.

- **The source comes from the credential, never from the body.** A user's batch is attributed to
  `user:<subject>` and is forced to `declared` confidence, whatever confidence the batch or its
  objects claim (a person declares; only collectors observe, and a person must not outrank a
  collector by saying so). A machine token's batch is attributed to the source the token is bound
  to. A token without a source gets 403. Whatever `source` the body claims is overwritten.
- Objects and relations are checked one by one against the caller's rights (type, and site for a
  grant limited to sites); one refused object refuses the whole batch (403), nothing is applied.
- Attributes with a sensitive name are refused at the door (400 `secret-attribute`).

## Authentication, authorization, audit

There is no anonymous mode: `api.New` returns an error without an `Authorizer` and an `Auditor`.

```go
type Authorizer interface {
    Principal(r *http.Request) (Principal, error)
    Allow(p Principal, action, objType, site string) bool
}
type Auditor interface {
    Record(ctx context.Context, e AuditEntry) error
}
```

- `Principal` fails with `ErrAuthUnavailable` (wrapped: the token store or the identity provider
  does not answer) → 503 `auth-unavailable` with `Retry-After`; any other error → 401 with
  `WWW-Authenticate: Bearer`.
- Every handler first asks `Allow(p, action, "", "")` (does the caller hold the action at all),
  then `Allow(p, action, type, site)` for each object it reads or writes. An empty `site` means
  unknown: a grant limited to sites must not match it. The site of an object is found through its
  parents, or for a device the rack it is mounted in; an object in two sites is unknown.
- A batch is checked on every object it touches: its objects, and the two ends of each relation
  and unrelation (parents included). An end designated by id counts with the type and site stored
  for that id, not the type the caller wrote. A denied end is a 403, like a denied object.
- Actions: `read`, `propose`, `approve`, `admin`.
- Every mutating call is recorded once with its outcome (`ok`, `denied`, `error`): batch
  submission, proposal approval and rejection, discrepancy acceptance, saved-query creation and
  deletion. A refusal at the first gate is recorded too. Reads and previews are not. A failing
  auditor is logged and does not undo a mutation that already committed.
- `StaticAuthorizer` and `MemoryAuditor` exist for tests and development only (plain tokens in
  memory, no expiry). Production uses `internal/auth` and `internal/audit` (later PRs).

## Errors

Every failure, including an unknown path and a wrong method, is an RFC 9457 problem
(`application/problem+json`) with `type` (`urn:trica:problem:<slug>`), `title`, `status`,
`detail` and `instance`.

| Status | Slug | When |
|---|---|---|
| 400 | `invalid-request`, `invalid-query`, `invalid-batch`, `secret-attribute` | Bad parameter, bad TQL, a batch the SDK or the engine refuses |
| 401 | `unauthenticated` | No valid credential |
| 403 | `forbidden`, `sensitive-attribute` | The action or the object is not allowed; a query on a sensitive attribute |
| 404 | `not-found` | Unknown object, proposal, saved query or path |
| 405 | `method-not-allowed` | With an `Allow` header |
| 409 | `conflict` | Proposal already decided; saved query exists |
| 413 | `too-large` | Body over 8 MiB |
| 422 | `rejected` | A well-formed batch the engine refuses: `observed_at` too old, or a value or relation type the schema refuses from a `user:` or `token:` source (ADR 0005); nothing applied |
| 500 | `internal` | The cause is logged, never returned |
| 503 | `auth-unavailable` | The credential could not be checked (token store or identity provider down); with `Retry-After` |

A panic in a handler is a 500 problem; the process keeps serving.

## Pagination

Every list answers `{"items": [...], "total": N, "limit": N, "offset": N}`. `limit` is 1 to 1000
(default 50), `offset` is 0 or more; anything else is a 400, never a silent clamp. `total` is the
size of the whole result for the caller, not of the page. Lists that are post-filtered by rights
(`/changes`, `/whohad`, `/relations`, history, relations, discrepancies) scan at most 5,000 candidates (200 for
`/whohad`) and say `truncated: true` when the cap was hit.

## What the API never returns

- No credential: an attribute whose name marks a secret (`password`, `passwd`, `passphrase`,
  `secret`, `token`, `credential`, `private_key`, `api_key`, `community`, `psk`, and the
  variants) is removed from objects, history, search results, discrepancies and proposals, and
  cannot be queried, sorted on or written. The rule is on the name, so it over-approximates on
  purpose; a test checks that no built-in attribute trips it.
- No id of an object the caller may not read, in the change feed: a relation change on a readable
  object stays in `/changes`, but its `other_id` is blank when the caller may not read the other
  end (by type or by site). The change is kept, only the id is redacted.
- A caller who cannot read all types does not learn object ids from a batch result: `objects` and
  `touched` are empty for them.
- Error answers do not carry driver or panic messages; the access log has no query string and
  no token; every answer has `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`.

## Limits of this version

- List endpoints (`/objects`, `/search`) are limited to types the caller may read in every
  site: a caller whose rights are limited to some sites reads by id (and gets history, relations,
  `/whohad`, `/changes`, discrepancies filtered per object), but cannot list.
- A free-text search could match a custom searchable attribute whose name is sensitive (the
  query is refused only when it names the attribute).
- The server rate-limits per principal (`auth.rate_limit`, a 429 over it) but has no CORS and no
  TLS (a reverse proxy or an ingress terminates it).
- A batch result may carry warnings with status 200: the engine downgrades a value that does not
  fit its kind, or an unknown relation type, to a warning instead of refusing the batch.
- Approval is not four-eyes: the submitter of a proposal may approve it if they hold `approve`.
