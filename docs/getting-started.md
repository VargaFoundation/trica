# Getting started

Two ways to run trica: Docker Compose on one machine, or the Helm chart on Kubernetes.

## Docker Compose

Needs Docker with the Compose plugin.

```bash
git clone https://github.com/VargaFoundation/trica && cd trica/deploy/compose
cp .env.example .env
$EDITOR .env                       # set TRICA_DB_PASSWORD
docker compose up -d --build       # builds the image from this checkout
curl -fsS http://127.0.0.1:8080/readyz && echo ready
```

The server applies the database migrations on its first start. Configuration is in
`deploy/compose/trica.yaml` ([reference](reference/configuration.md)); the database URL is
assembled from `.env` and never written to that file.

## Kubernetes

Needs a PostgreSQL 17 database and a Secret holding its URL:

```bash
kubectl create namespace trica
kubectl -n trica create secret generic trica-db \
  --from-literal=database-url='postgres://trica:PASSWORD@HOST:5432/trica?sslmode=require'
helm install trica ./charts/trica -n trica \
  --set database.existingSecret=trica-db \
  --set image.repository=REGISTRY/trica --set image.tag=TAG
kubectl -n trica rollout status deploy/trica
```

Releases publish the image to `ghcr.io/vargafoundation/trica` and the chart to
`oci://ghcr.io/vargafoundation/charts/trica` ([releasing](reference/releasing.md)). No
release has been tagged yet: until then, build the image (`docker build -t REGISTRY/trica:TAG .`)
and push it to a registry the cluster can pull from.

The chart's values are documented in [`charts/trica/README.md`](../charts/trica/README.md).

## First credentials

A fresh deployment has no credential. The `token` and `enrol` commands create them from next to
the server; whoever can run them has database access, which already is everything
([security](reference/security.md#minting-the-first-token)). The secret is the only thing on
stdout, shown once; metadata goes to stderr. The server must have started once (it migrates), or
run `trica migrate up`.

An API token, for the MCP client (Claude Code) or a script:

```bash
# Compose, from deploy/compose
TOKEN=$(docker compose exec -T trica trica token create --name claude-code --owner you --roles admin --scopes write)
# Kubernetes (the chart's pod reads its configuration from /etc/trica/trica.yaml)
TOKEN=$(kubectl -n trica exec deploy/trica -- trica token create --config /etc/trica/trica.yaml \
  --name claude-code --owner you --roles admin --scopes write)
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/v1/me
```

The roles must exist in the server's RBAC policy (`admin`, `operator`, `viewer`, or your
`auth.policy_file`), and a scope cannot exceed them: use `--roles operator --scopes write` for a
client that should only propose. `trica token list` shows tokens (never a secret),
`trica token revoke --id N` disables one at once.

An enrolment token, for each agent or collector (needs `transport.addr`):

```bash
docker compose exec -T trica trica enrol create --binding agent:host-01 --ttl 24h
kubectl -n trica exec deploy/trica -- trica enrol create --config /etc/trica/trica.yaml --binding agent:host-01 --ttl 24h
```

Hand the token and the CA certificate (`<transport.ca_dir>/ca.pem`, public; the command says where) to the
machine over a channel you trust. The token works once.

## Backup and restore

`trica backup` and `trica restore` take and load a consistent archive of the database. The
procedure for Compose is in [`deploy/compose/README.md`](../deploy/compose/README.md); on Kubernetes
the reference deployment relies on the PostgreSQL operator's WAL archiving, and the chart has an
optional CronJob for standalone installs ([chart README](../charts/trica/README.md)).

## What you get today

One server process with the migrations, the absence sweep, `/healthz`, `/readyz`, `/metrics`, and:

- the REST API at `/api/v1/` and the MCP server at `/mcp`, both behind a bearer credential (an
  API token, or an OIDC JWT when `auth.oidc` is set), rate-limited and audited;
- the web UI at `/` (a placeholder page in a build without the `webui` tag);
- the cabling deducer, which runs every five minutes, and the device type library, matched once
  at start;
- the mutual-TLS endpoint for agents, on its own port, when `transport.addr` is set.

There is no anonymous access: create the first API token with `trica token create`
([below](#first-credentials)), or sign in through OIDC ([configuration](reference/configuration.md)).
The collectors and the agent are not started by the server; see [`plan/STATUS.md`](plan/STATUS.md).
