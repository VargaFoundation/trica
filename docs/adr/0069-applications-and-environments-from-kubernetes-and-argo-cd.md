# ADR-0069: Applications and environments, discovered from Kubernetes and Argo CD

- **Status**: accepted (2026-10). Amends [ADR 0011](0011-impact.md) only by test (no
  rule changes) and extends the Kubernetes collector.
- **Concerns**: `internal/schema/builtin.yaml`, `internal/collect/kubernetes`, `charts/trica`
  (`collect.kubernetes.rbac`), `web/src/pages/AppInstances.tsx`, `docs/reference/`. The schema
  change is additive.

## Context

The application layer (`application`, `component`, `component_instance`, `environment`,
`endpoint`) existed in the schema but nothing filled it. The production estate is three k3s
clusters (build, dev, prod) with one Argo CD on build managing all three: 203 Applications on
2026-10-26, named `<tenant>-<component>-<env>`, in projects `tenant-<x>` or the platform's.
Only the build cluster runs a trica collector; the dev and prod workloads are not read, but
Argo CD describes them.

## Decision

1. **One environment per cluster.** The Kubernetes collector takes `environment` (default: the
   cluster name) and `environment_kind` (default guessed from the name). It emits the
   `environment`, `k8s_cluster deployed_in environment`, and `namespace deployed_in <x>` for a
   namespace labelled `trica.io/environment=<x>` (the label overrides the cluster's).
2. **Applications from labels, no new API call.** A workload with `app.kubernetes.io/part-of`
   gives the application; its component is the `component` label, else the `name` label, else
   the workload name; the workload gives a `component_instance` that `runs_on` it. Version: the
   `version` label, else the first image tag. Workloads with no `part-of` and no Argo CD owner
   produce nothing. Jobs are never instances.
3. **Argo CD** (`argocd: true` on the collector of the cluster that runs it): a read-only list of
   `applications.argoproj.io` in `argocd_namespace`. Each Application is a `component_instance`
   in the environment of its destination: this cluster for `in-cluster` or the API server's
   own address, else the environment named by `argocd_clusters` (server or name to environment),
   else the server's host with a warning. The application is, in order: `trica.io/application`
   (annotation or label), the platform projects (`argocd_platform_projects`, all under
   `platform`), `part-of`, the project `tenant-<x>`, any other named project but `default` (a project groups
   what one team deploys: its Applications are components of one application), the Application's
   name without its environment suffix. Health and sync status are attributes; the version is
   `targetRevision@<short sha>` unless a label or image gave one. The repository address is
   kept without credentials, query or fragment.
4. **One key, so one object.** An instance is identified by
   `application/component/environment/namespace` (each slugged; an empty namespace is `-`). The
   label view and the Argo CD view, even from two collectors, give the same key and the engine
   merges them (ADR 0006). With Argo CD off, a workload that carries only an Argo CD label
   gets an application only if it also has a component label (Application named
   `<application>-<component>-<env>`); otherwise nothing is guessed.
5. **Endpoints.** An ingress host that serves a service selecting the pods of an instance's
   workload is an `endpoint` (`https://<host>`), `exposed_by` the instance.
6. **Schema.** `environment` gains `kind` and `cluster`; `component_instance` gains `health` and
   `sync_status` and is a child of `component`; `application` gains `hosting` (`self`, `saas`)
   and `vendor` for applications declared by hand; relation `deployed_in` from
   `component_instance`, `k8s_cluster` and `namespace` to `environment`.
7. **Never read the Argo CD cluster Secrets.** They hold bearer tokens. The cluster to
   environment mapping is an option, not read from the cluster.
8. **Chart.** `collect.kubernetes.rbac.argocd: true` adds a Role and RoleBinding in
   `collect.kubernetes.rbac.argocdNamespace` (default `argocd`) with `get`, `list`, `watch` on
   `applications.argoproj.io`: a namespaced grant, not a ClusterRole rule.
9. **UI.** An application's Overview starts with "Instances by environment": its instances
   (under its components), grouped by the environment they are deployed in, with health, version
   and sync status.

## Consequences

- Impact (ADR 0011) needs no new rule: a cluster or node down takes its pods down, the workload
  when every pod is gone, then the instance (`runs_on`). A failure never climbs from an instance
  to its application. `TestImpactReachesApplicationInstances` guards it.
- dev and prod are known only through Argo CD (instance, health, sync, version) until a
  collector runs there; their workloads, pods and nodes are not in the inventory.
- `platform-apps` (project `default`) becomes an application of its own unless `default` is listed in
  `argocd_platform_projects` (done at Diametral).
- The `<application>-<component>-<env>` naming is a convention: an estate that does not follow
  it sets `trica.io/application` and `trica.io/component` on the Application.
- A scp-style repository address (`git@host:org/repo`) is kept as `host:org/repo`.

## Alternatives rejected

- Reading the Argo CD cluster Secrets for the names: secrets of that kind carry tokens.
- A ClusterRole rule for `applications`: it would grant every namespace for what lives in one.
- Guessing an application from an Argo CD label alone: wrong guesses are noise in a CMDB.

## Amendment (2026-10-27): the platform's tools installed outside Argo CD

Measured in production after the first run: Argo CD itself, Cilium and Tekton are installed
outside Argo CD and carry `app.kubernetes.io/part-of`, so each became an application of its own
(`argocd`, `cilium`, five `tekton-*`). The option `platform_namespaces` lists the namespaces of
such tools: a labelled workload there is the component `<part-of>` of the application
`platform`. Elsewhere `part-of` keeps naming an application.

