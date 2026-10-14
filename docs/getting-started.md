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

## Backup and restore

`trica backup` and `trica restore` take and load a consistent archive of the database. The
procedure for Compose is in [`deploy/compose/README.md`](../deploy/compose/README.md); on Kubernetes
the reference deployment relies on the PostgreSQL operator's WAL archiving, and the chart has an
optional CronJob for standalone installs ([chart README](../charts/trica/README.md)).

## What you get today

The server, its health and metrics endpoints, the migrations and the absence sweep. The REST
API, the MCP server, the collectors and the UI are mounted as they land; see
[`plan/STATUS.md`](plan/STATUS.md).
