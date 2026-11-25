# ADR-0071: The application catalog, declared next to the code

- **Status**: accepted (2026-11)
- **Concerns**: `internal/collect/catalog` (new module `catalog`), `cmd/trica` (`catalog check`), `pkg/sdk` (`Batch.WithdrawOmitted`,
  additive), `internal/reconcile` (withdrawal of omitted objects), `internal/inventoryio` (import of
  scope full), `internal/api` (`POST /batches`), `internal/graph` (amends [ADR 0011](0011-impact.md)),
  `internal/schema/builtin.yaml` (additive), `charts/trica` (`collect.configMaps`).

## Context

ADR 0069 fills the application layer from Kubernetes labels and Argo CD. Reviewed against "every
asset of the estate is in trica", it leaves these gaps:

1. **Only what runs in Kubernetes exists.** An application on a VM or a server, a database
   installed by hand, a SaaS (the identity provider, the mail), are in no source. The agent sees
   packages, services and sockets on a host, but nothing says which application they serve.
2. **Nothing a person knows is recorded.** The schema has `owner`, `criticality`, `domain`,
   `hosting`, `vendor`, and the relations `owned_by`, `depends_on`, `calls`; no source fills them.
   Discovery cannot know who owns an application or what it is for; without that, a CMDB answers
   "what runs" but not "whose is it" or "what breaks for the business".
3. **Declaring by hand is impractical.** `trica import` writes a generic document (refs,
   identities) and needs a direct database URL, so it is run from a workstation, not reviewed in
   Git, not repeated. The declaration files ADR 0005 names were never built.
4. **A declared full-scope source never forgets an object.** The engine withdrew, on a full-scope
   batch, the omitted values of the objects the batch still named, never the claims on objects it
   no longer names: those wait for the absence sweep, which ignores declared objects. An
   application taken out of an import of scope full (and so of any catalog) kept its owner and
   criticality for ever, although [export-import](../reference/export-import.md) promised the
   withdrawal.
5. **Impact reads every environment alike.** An application is down when all its instances are
   (ADR 0011 amendment of 2026-10-28): with a dev instance alive, a production outage reads
   "degraded", and a dev outage degrades the application, which says nothing of its users.

## Decision

1. **A collector module `catalog`** reads a YAML catalog (`format: trica-catalog`, `version: 1`)
   from a file, every `*.yaml`/`*.yml` of a directory (a mounted ConfigMap, dot names skipped) or
   one https URL (a raw file of a Git repository). It declares teams, people, environments and
   their kind, applications (display name, description, domain, criticality, lifecycle, hosting,
   vendor, repository, owner), their dependencies (`depends_on`, `calls`), components, and the
   instances no collector derives, `runs_on` a `vm` or `device` designated by identity (name,
   serial, uuid), with endpoints. Running it as a target gives it the runner's interval, health
   board, Collectors page, and both modes (in the server, `trica collector`), with no new path
   into the engine.
2. **Declared, and the same objects as discovery.** The batch's confidence is `declared` (it ranks
   below what is observed, and a contradiction opens `value_mismatch`). Applications, components,
   environments and instances carry the identities of ADR 0069 (application slug, component name
   under it, instance key `application/component/environment/location`), so that a declared
   application and a discovered one are one object. An application name must be a slug: a
   declared `Billing` must never stand beside a discovered `billing`.
3. **Strict, all or nothing.** Unknown keys, values outside an enumeration, names that are not
   slugs, an owner not declared in the catalog, an instance of an undeclared component, a SaaS with
   instances, duplicate keys, an endpoint carrying credentials: all reported at once, and the run
   fails, which withdraws nothing (the engine's rule for failed runs). An empty catalog fails too
   unless `options.allow_empty`: an emptied ConfigMap must not withdraw every application. A
   dependency or a host outside the catalog is designated by identity: not found, it is an
   `unresolved_reference`, visible, not an error.
4. **The catalog is a document of record: `Batch.WithdrawOmitted`.** A new, additive field of the
   collector contract (`pkg/sdk`), valid only on a full-scope declared batch (`Validate` refuses it
   otherwise). With it, the engine withdraws the claims its source holds on the objects the batch
   no longer names, **except the identifying ones** (the object stays named once absent, and is the
   same object if declared again), and marks absent an object that no collector ever observed and
   on which nothing else is claimed. An object something observed keeps its status (the sweep
   decides); another source's declarations keep an object active. A declared object marked absent
   this way becomes active again when declared again. The catalog and `trica import --scope full`
   set it; the device-type library does not (it keeps the models no device uses any more), and
   collectors do not (their omissions wait for the sweep, values kept, ADR 0007).
5. **Not for a person's batch.** `POST /batches` refuses `withdraw_omitted` from a person (400):
   one call would retire everything the person declared elsewhere. A token bound to its own source
   may send it.
6. **The token never leaks.** For an https address the reference in `password` is sent as a bearer
   token in the `Authorization` header only; redirects are not followed (the header would travel);
   an address with credentials, a fragment or a query parameter named like a credential
   (`private_token`) is refused at start; errors quote neither the response body nor the header.
   Only `GET` is sent. At most 4 MiB per run.
7. **Schema, additive.** `application.lifecycle` (`planned`, `development`, `production`,
   `deprecated`, `retired`, searchable); `supplied_by` may start at an `application` (a SaaS and its
   vendor).
8. **`trica catalog check FILE|DIR`** runs the collector's parsing and validation offline (no
   database, no network) and prints every problem: the catalog is checked in the merge request
   that changes it, not by a failed run in production.
9. **Chart.** `collect.configMaps` mounts existing ConfigMaps as directories at
   `/etc/trica/collect/configmaps/<name>/`, read-only, without `subPath`, so that an edit reaches the
   pod and the next run without a restart.
10. **Impact follows production (amends ADR 0011).** An application's state is read from its
   instances deployed in an environment of kind `prod` when it has any (an environment absent does
   not count), else from all its instances as before: production all down is the application down
   even with a dev instance alive; a dev instance down leaves the application untouched (the
   instance itself shows it).

## Consequences

- One file, reviewed in Git, says who owns what. The searches `type:application -source:catalog:*`
  (discovered, not declared) and `type:application -rel.owned_by:*` (no owner) are the governance
  checks; a test proves both.
- Declared and observed values of one application sit side by side; where they differ (a
  repository URL), a `value_mismatch` is open, by design.
- A catalog run is cheap (a file read) and idempotent: a second run changes nothing.
- The catalog is per source: renaming the target starts a new source, whose first run does not
  withdraw what the old one declared.
- `trica import --scope full` now does what its page promised: an object left out of the file
  becomes absent when nothing else declares or observes it.

## What is left out (and why)

- **Host-level discovery of applications** (a systemd unit or a container on a VM tagged with its
  application by the agent): it needs a convention on the host and an agent change; the catalog
  covers these instances by declaration meanwhile.
- **Contracts and licences of SaaS** (`covered_by` from an application): the schema's `contract`
  exists, the catalog does not declare it yet.
- **Backstage `catalog-info.yaml`**: a reader for that format would let an estate that already
  keeps one reuse it; not needed by the estate this was written for.
- **Coverage discrepancies** (a self-hosted application declared with no instance observed): a
  search answers "discovered, not declared" and "no owner"; "declared, never seen" is not computed.
- **The naming conventions of ADR 0069** (`tenant-<x>` projects, the `-ci` suffix, `platform`) stay
  built in; an estate with others sets `trica.io/application` on its Argo CD Applications.
- **The version of a multi-container workload** is still its first image's tag, which may be a
  sidecar's.

## Alternatives rejected

- **A declaration endpoint or UI form only.** It leaves the knowledge in a database instead of
  next to the code, and gives no review and no history outside trica.
- **Withdrawing omitted objects for every full-scope batch.** It would close the values of a
  device a collector stops seeing (ADR 0007 keeps them on the absent object) and retire the
  device-type models nobody uses.
- **A field-level list of what to withdraw, kept by the collector.** A collector is stateless; the
  engine already knows what the source claimed.
