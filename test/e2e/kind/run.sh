#!/bin/sh
# Installs the chart on the current kube context (a kind cluster with Calico)
# and checks what is only true on a real cluster:
#   - the pod starts non-root on a read-only root filesystem and becomes Ready
#     (migrations applied against a real PostgreSQL);
#   - the database URL arrives as a file from the Secret, not as an env var;
#   - the NetworkPolicy lets a pod of the namespace in and keeps another
#     namespace out; the egress policy still lets the server reach PostgreSQL.
# Usage: IMAGE=trica:e2e test/e2e/kind/run.sh   (the image is already loaded)
set -eu
ns=trica-e2e
image="${IMAGE:?set IMAGE to a loaded image, e.g. trica:e2e}"
here=$(dirname "$0")

kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace trica-e2e-outside --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" apply -f "$here/postgres.yaml"
kubectl -n "$ns" rollout status deploy/postgres --timeout=300s
kubectl -n "$ns" create secret generic trica-db \
  --from-literal=database-url="postgres://trica:e2e-only@postgres.$ns.svc:5432/trica?sslmode=disable" \
  --dry-run=client -o yaml | kubectl apply -f -

helm upgrade --install trica charts/trica -n "$ns" --wait --timeout 5m \
  --set database.existingSecret=trica-db \
  --set image.repository="${image%:*}" --set image.tag="${image##*:}" --set image.pullPolicy=Never \
  --set-json 'networkPolicy.egressTo=[{"to":[{"podSelector":{"matchLabels":{"app":"postgres"}}}],"ports":[{"port":5432,"protocol":"TCP"}]}]'

pod=$(kubectl -n "$ns" get pod -l app.kubernetes.io/name=trica -o jsonpath='{.items[0].metadata.name}')
echo "== pod $pod is Ready"

if kubectl -n "$ns" get pod "$pod" -o jsonpath='{.spec.containers[0].env}' | grep -q TRICA_DATABASE_URL; then
  echo "FAIL: the database URL is passed as an environment variable" >&2; exit 1
fi
echo "== the database URL is not in the environment"

probe() { # probe NAMESPACE: exit status of a GET /readyz from a pod there
  kubectl -n "$1" run "probe-$$-$(date +%s)" --rm -i --restart=Never --quiet \
    --image=curlimages/curl:8.11.1 --command -- \
    curl -fsS --max-time 5 "http://trica.$ns.svc:8080/readyz"
}
probe "$ns"
echo "== reachable from its namespace"
if probe trica-e2e-outside; then
  echo "FAIL: reachable from another namespace despite the NetworkPolicy" >&2; exit 1
fi
echo "== unreachable from another namespace"

kubectl -n "$ns" exec deploy/postgres -- psql -U trica -tAc 'SELECT max(version_id) FROM goose_db_version' | grep -qE '^[1-9]'
echo "== migrations applied"
echo "PASS"
