# ADR-0021: Logical backup and restore as `trica backup` / `trica restore`, versioned by the migration level

- **Status**: accepted (2026-10)
- **Concerns**: `internal/backup`, `cmd/trica`, `Dockerfile` (target `ops`), `deploy/compose`, `charts/trica`

## Context

Milestone 8 requires integrated backup and restore, with the restore tested and timed (target:
under 15 minutes). trica's state is PostgreSQL and nothing else: no files, no queue, no cache that
cannot be rebuilt. The inventory is append-only history (ADR 0002, 0007), so losing it loses
what no collector can rediscover: the past. The reference Kubernetes deployment (Zalando operator
with WAL-G) already has physical backups and point-in-time recovery; standalone installs (the
compose file, a plain PostgreSQL) have nothing.

## Decision

- **Backup is a logical dump**: `trica backup (--file F | --dir D)` runs `pg_dump --format=custom`
  (one snapshot, so consistent while the server runs and writes) into `F.partial`, reads the
  archive back, and only then renames it to `F`. `--dir` names it `trica-<UTC timestamp>.dump`.
  It never overwrites and never deletes: pruning old archives is the volume's or the operator's
  job, in keeping with "nothing is deleted".
- **The schema version lives in the archive**, not beside it: the dump contains the
  `goose_db_version` table, and `trica` reads the version from that (`pg_restore --data-only
  --table=goose_db_version`). A side file or a file name could drift from the content; this cannot.
- **Restore is guarded**. `trica restore --file F` first reads the archive's version:
  - newer than the migrations the binary embeds: refused before touching the target
    (`ErrNewerArchive`); the operator runs the release that wrote it;
  - older: loaded, then `migrate up` runs (the engine and the schema registry never see an
    old schema);
  - the target must be empty; a populated database is refused unless `--replace`, which uses
    `pg_restore --clean --if-exists`, and is itself refused while other connections are open on
    the database (stop the server first);
  - loading is `--single-transaction --exit-on-error`: a failed restore leaves the target as it was.
- **The PostgreSQL client tools are executed, not reimplemented**: `pg_dump` and `pg_restore` 17.
  The server image stays distroless, so a second Dockerfile target `ops` (`postgres:17-alpine`
  plus the same static binary, non-root, entrypoint `trica`) carries them. It is declared before
  the final stage so `docker build .` still yields the server image. The connection reaches the
  tools through `PG*` environment variables, never the command line.
- **Compose**: an `ops` service under the `ops` profile (never started by `up`), writing to a
  `backups` volume or `TRICA_BACKUP_DIR`. The procedure is documented in `deploy/compose/README.md`.
- **Chart**: an optional CronJob, `backup.enabled=false` by default, writing to an existing
  PersistentVolumeClaim (`backup.existingClaim`, required when enabled). Values hold schedule,
  claim and image only: the database URL is the same Secret reference as the server's, there is
  no credential in values. An S3-compatible target is a claim backed by an S3 CSI driver or a copy
  job of the operator's choosing, which keeps object-storage credentials out of this chart. The
  pod does not carry the server's selector labels, so the Service never routes to it.
  Restore is not a chart job: it is a deliberate, attended act, done with the same image and
  `kubectl run`/`exec`.

## Proof

`internal/backup` loads 50 racks and 2,000 devices through the engine, with a second observation
so history holds closed ranges, backs up, drops the database, restores into the empty one, and
compares a row count and an md5 of every table plus time-travel reads of a sample. It logs the
restore duration and fails over 15 minutes. CI runs it with real `pg_dump` 17, and runs the
packaged compose path (ops image, drop, restore) with the same budget.

## Consequences

- A logical dump is slower and heavier than WAL archiving, and its recovery point is the last
  archive, not the last transaction. For a standalone install that is the right trade; the
  reference deployment keeps WAL-G and does not enable the CronJob.
- `pg_dump` and the server must stay within one major version: the ops image pins 17, like the
  PostgreSQL the tests run on. A PostgreSQL upgrade moves the ops image with it.
- An archive restores under whichever role runs the restore (`--no-owner --no-privileges`);
  the role needs the right to `CREATE EXTENSION` for `pg_trgm` and `btree_gist`, as migrations do.
- The ops image is not published yet (no release workflow; PR for the release pipeline should
  add it next to the server image).
- Restoring an archive from an older release moves the schema forward only. There is no
  restore into an older binary: a rollback is a restore of an archive taken before the upgrade.
