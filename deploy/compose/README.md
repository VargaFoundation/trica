# trica with Docker Compose

Install: see [getting started](../../docs/getting-started.md). This page is the operator's
backup and restore procedure ([ADR 0021](../../docs/adr/0021-backup-restore.md)).

PostgreSQL holds all of trica's state; a backup is one consistent `pg_dump` archive, taken while
the server runs. The `ops` service (profile `ops`, never started by `up`) runs the same `trica`
binary with the PostgreSQL 17 client tools. Run everything from `deploy/compose`, with `.env`
in place.

## Back up

```bash
docker compose --profile ops run --rm ops backup --dir /backups
# backup /backups/trica-20261014T021700Z.dump schema version 1, 417855 bytes, 0.7s
```

Archives land in the `backups` volume, or in the host directory named by `TRICA_BACKUP_DIR` in
`.env` (it must be writable by uid 65532). Nothing is ever deleted or overwritten: prune old
archives yourself, and copy them off the machine, a backup on the same disk is not a backup.
Schedule it with cron on the host:

```cron
17 2 * * *  cd /opt/trica/deploy/compose && docker compose --profile ops run -T --rm ops backup --dir /backups
```

List what you have: `docker compose --profile ops run --rm --entrypoint ls ops -l /backups`.

## Restore

The archive records the schema version it was taken at. A restore refuses an archive from a newer
release than the image you run, loads an older one then migrates it up, and refuses a database
that already holds tables unless you say `--replace`. It loads in one transaction: a failed
restore changes nothing.

Into an empty database (disaster recovery, a new machine):

```bash
docker compose stop trica
docker compose up -d postgres
docker compose exec postgres psql -U trica -d postgres -c 'DROP DATABASE IF EXISTS trica WITH (FORCE)' -c 'CREATE DATABASE trica'
docker compose --profile ops run --rm ops restore --file /backups/trica-20261014T021700Z.dump
docker compose up -d trica
curl -fsS http://127.0.0.1:8080/readyz
```

Over the existing database (roll back to an archive; the server must be stopped, the restore
refuses while it is connected):

```bash
docker compose stop trica
docker compose --profile ops run --rm ops restore --file /backups/trica-20261014T021700Z.dump --replace
docker compose up -d trica
```

Restoring is a matter of seconds to minutes: CI restores a 2,000-device inventory and fails if
a restore takes over 15 minutes. Run a restore on a copy now and then; an archive never restored
is a hope.

## Kubernetes

A PostgreSQL operator with WAL archiving takes physical backups with point-in-time recovery: use
those, and leave the chart's CronJob off. `backup.enabled` is for standalone
installs on a PostgreSQL that has nothing of its own; see the chart README.
