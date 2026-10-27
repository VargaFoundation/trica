# Kubernetes collector

Kind: `kubernetes`. Source name: `kubernetes:<target name>`.

Reads one Kubernetes cluster through its API: nodes, namespaces, workloads,
pods, services, ingresses and, when KubeVirt is installed, its virtual machine
instances, and the applications and environments they run (ADR 0069). It is read-only: it only issues `list` calls (and one `GET
/version`), it never reads a Secret or a ConfigMap, and the ServiceAccount it
runs with has no verb but `get`, `list` and `watch`.

One run lists everything once (paged, 500 objects per call) and emits one batch
with full scope. Following the cluster in real time with informers (watch) is a
follow-up, see "What is not collected".

## Configuration

```yaml
targets:
  # Inside the cluster: the pod's ServiceAccount is the credential.
  - kind: kubernetes
    name: prod
    options:
      cluster: prod
      finished_pods: false # the default: pods that have ended are not collected

  # From outside: a kubeconfig.
  - kind: kubernetes
    name: lab
    address: https://192.0.2.10:6443      # optional, overrides the kubeconfig's server
    password: file:/run/secrets/lab-token # optional bearer token, overrides the kubeconfig's
    tls: {ca_file: /etc/trica/lab-ca.crt}
    options:
      cluster: lab
      kubeconfig: /etc/trica/lab.kubeconfig
      context: lab-readonly
```

| Where | Setting | Meaning |
|---|---|---|
| `address` | empty or `in-cluster` | In-cluster configuration (ServiceAccount token and the `KUBERNETES_SERVICE_*` variables). |
| `address` | anything else | Needs `options.kubeconfig`. A value with `://` overrides the server of the kubeconfig's context (a tunnel, a load balancer); any other value is just a label. |
| `options.cluster` | required | Name of the cluster: the identity of the `k8s_cluster` object and the scope of everything below it. Two clusters need two names, or their objects merge. |
| `options.kubeconfig` | path | Kubeconfig file. Required unless in-cluster. |
| `options.context` | name | Context of the kubeconfig; empty is its current context. |
| `options.finished_pods` | bool, default `false` | Also collects the pods that have ended (phase `Succeeded` or `Failed`). Off by default: on a cluster running CI or batch jobs they outnumber the live pods by far (on one cluster, 2 000 finished Tekton pods out of 2 363 made `pods -A` a 118 MB answer that outlasted the 30 s timeout), and they run nothing. |
| `password` | secret reference | Optional bearer token (`file:` or `env:`), overrides the kubeconfig's credentials. Never read from the cluster. |
| `tls.ca_file` | path | CA that signed the API server's certificate. |
| `tls.insecure` | bool | Skips verification (and ignores the CA). Explicit, per target. `tls.min_version` is not applied: client-go negotiates its own. |
| `options.environment` | name, default `options.cluster` | The environment this cluster is: its `environment` object, `k8s_cluster deployed_in` it. A namespace labelled `trica.io/environment=<x>` is `deployed_in` `<x>` instead, and its instances too. |
| `options.environment_kind` | `prod`, `staging`, `dev`, `test`, `build`, `other` | Kind of the environment; guessed from the name when empty (`prod`, `dev`, `staging`, `test`, `build` and their usual spellings). |
| `options.argocd` | bool, default `false` | Also lists `applications.argoproj.io` in `argocd_namespace` (read-only). Set it on the collector of the cluster that runs Argo CD. |
| `options.argocd_namespace` | name, default `argocd` | Where the Applications live. |
| `options.argocd_clusters` | map server or name to environment | The environment of an Application's destination: `{"https://203.0.113.1:6443": dev}`. In-cluster destinations are this cluster. An unmapped one is named after its host and reported as a warning. Never read from Argo CD's cluster Secrets (they hold tokens). |
| `options.argocd_platform_projects` | list of Argo CD projects | Applications of these projects are all components of the application `platform`. |
| `timeout` | duration | Bounds the whole run (default 30 s), and each request. |

Exec-based and auth-provider credentials of a kubeconfig work as client-go
supports them; prefer a static ServiceAccount token for a collector.

## What is collected

| Kubernetes | trica | Attributes and relations |
|---|---|---|
| server version (`/version`) | `k8s_cluster` | `name` (identity, from `options.cluster`), `version` (gitVersion), `distribution` (guessed from the version suffix: `+k3s`, `+rke2`, `+k0s`, `-eks-`, `-gke.`, otherwise `kubernetes`), `api_endpoint` |
| `Node` | `k8s_node` in the cluster | identity `name`, `uid`; `system_uuid`, `kubelet_version`, `os_image`, `kernel`, `roles` (from `node-role.kubernetes.io/*` labels), `ready` (Ready condition), `internal_ip`, `cpu_capacity` and `memory_capacity_bytes` (from `status.allocatable`). Relation `hosts` from the `device` whose `hw_uuid` equals `system_uuid` (found by the server; unresolved while no device has it). |
| `Namespace` | `namespace` in the cluster | identity `name`, `uid`; `labels` |
| `Deployment`, `StatefulSet`, `DaemonSet`, `CronJob`, `Job` | `workload` in its namespace | identity `name`, `uid`; `kind`, `replicas` and `ready_replicas` (not for cron jobs and jobs), `images` (pod template containers). A Job started by a CronJob is `served_by` it. |
| `ReplicaSet` | nothing | Only used to attach a pod to its Deployment. |
| `Pod` (live ones; all with `finished_pods`) | `pod` in its namespace | identity `name`, `uid`; `phase`, `pod_ip`, `node_name`, `restarts` (sum over containers), `images`. Relations: `hosts` node -> pod; `served_by` workload -> pod (through the ReplicaSet for a Deployment). Pods owned by a Node (static pods), by a KubeVirt instance (virt-launcher) or by nothing have no workload. |
| `Service` | `k8s_service` in its namespace | identity `name`, `uid`; `type`, `cluster_ip` (`None` for headless), `ports` (name, port, protocol, target port, node port), `selector`. Relations: `served_by` service -> pod (pods of the same namespace matching the selector, not finished); `exposed_by` workload -> service (workloads owning those pods). A service without selector has no relation. |
| `Ingress` | `ingress` in its namespace | identity `name`, `uid`; `hosts`; `rules` (one entry per host and path: host, path, path type, backend service, port). Relation `exposed_by` service -> ingress for each backend service. |
| KubeVirt `VirtualMachineInstance` | `vm` | identity `uuid` (firmware UUID, else the instance UID) and `name`; `namespace`, `vcpus` (cores x sockets x threads, when the instance declares a cpu block), `memory_bytes` (guest memory, else the pod's memory request or limit), `power_state` (Running -> running, Paused condition -> paused, Succeeded/Failed -> stopped, otherwise unknown), `os_name` (guest agent). Relation `hosts` node -> vm. |
| VMI `status.interfaces` | `interface` in the `vm` | `name`, `kind: virtual`, `mac`; each address (loopback and link-local are dropped) is an `ip_address` `assigned_to` the interface. |

| `app.kubernetes.io/part-of`, `name`, `component`, `version` of a workload | `application`, `component` (in it), `component_instance` (in the component) | Instance key `application/component/environment/namespace`; `runs_on` the workload; `deployed_in` the environment; `version` from the label, else the image tag. No label, no application. |
| Ingress host serving a service of an instance | `endpoint` | `url` `https://<host>`, `exposed_by` the instance. |
| Argo CD `Application` (with `argocd`) | `component_instance` | Same key rule, in the environment of the destination cluster; `health`, `sync_status`, `version` (`targetRevision@sha7`, only if no label gave one); `repository` of the application (credentials stripped). See below. |

Lists are sorted, so two runs on an unchanged cluster emit the same batch.

Pods that have ended are left out by the API server itself (field selector
`status.phase!=Succeeded,status.phase!=Failed`, applied before paging), and
again on the way back in case a server ignores the selector. A pod that ends
between two runs is not seen by the second one and becomes `absent`, like any
object a full-scope run no longer sees; its Job keeps its own object.

### Applications and Argo CD

Application name, in order: `trica.io/application` (annotation or label of the
Application); a platform project (`platform`); `app.kubernetes.io/part-of`; the
project `tenant-<x>` (`-ci` dropped); any other named project but `default`; the
Application's name without its environment suffix. Component: `trica.io/component`, else the name without the
application prefix and the suffix. The same instance seen by workload labels
(here or from another cluster's collector) and by Argo CD has the same key, so
the server keeps one object. A multi-source Application is read through the
source that has a path or a chart, with the matching synced revision. An
Application that does not parse is a problem (partial scope); a failed or
forbidden list of Applications is a problem too; a destination no option maps
is only a warning. Without `argocd`, a workload with only an Argo CD label gets
no application unless it also has a component label (Application named
`<application>-<component>-<env>`).

### Caveats

- **Pod and service addresses are not collected as `ip_address`**: pod and
  service networks overlap between clusters, and `ip_address` is identified
  by its address alone. Only VM addresses are, because they are the machine's
  own. Two clusters whose VMs share a private address will share the object.
- **The device-to-node link** (`hosts`) relies on the kubelet's `systemUUID`
  (the SMBIOS UUID). Virtual machines cloned from a template can share it. The
  link is only proposed if a device (for example from the BMC or hypervisor
  collectors) carries that `hw_uuid`.
- A pod scheduled on a node missing from the node list (removed while the run
  was reading) has no `hosts` relation: the relation needs both ends.
- `cpu_capacity` and `memory_capacity_bytes` are the node's *allocatable*
  resources (what the scheduler can hand out), not the hardware's.
- The `kubernetes.io/metadata.name` label and the others of a namespace are
  collected as is in `labels`; no other labels or annotations are.

## Errors

| Situation | Result |
|---|---|
| Cannot build the client (missing kubeconfig, unknown context, unreadable token, not in a cluster) | `Failed` batch, `connect: ...` in the health. |
| `/version` fails (unreachable, refused credentials, not an API server, timeout) | `Failed` batch, `server version: ...`. |
| Credentials refused (401) or timeout during the lists, or every list failing | `Failed` batch. |
| One list fails (RBAC forgot a resource: 403, a response that cannot be decoded) | Partial scope: what was read is kept, nothing else is withdrawn, health is not OK and names the failed lists. |
| KubeVirt API absent (404) | Not an error: the cluster has no KubeVirt. |
| Applications forbidden (403), not installed (404), or one malformed | Partial scope, named in the health. |
| An Argo CD destination no `argocd_clusters` entry maps | Warning, once per environment name; scope stays full. |
| One VirtualMachineInstance is malformed | Reported as a warning, that instance is skipped, partial scope. |

A `Failed` or partial batch never has full scope: an unread resource must not
make its objects absent (ADR 0005).

## Role to grant

The collector needs the verbs `get`, `list` and `watch` on the resources below
and nothing else. **It is deliberately not granted Secrets or ConfigMaps**:
nothing it maps lives there, and a collector that cannot read them cannot leak
them.

`endpoints`, `endpointslices` and `virtualmachines` are granted but not read
yet: they are reserved for the follow-up (service endpoints as seen by the
control plane, stopped virtual machines). Remove them if you prefer to grant
only what is read today.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: trica-collector
  namespace: trica
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: trica-collector
rules:
  - apiGroups: [""]
    resources: [nodes, namespaces, pods, services, endpoints]
    verbs: [get, list, watch]
  - apiGroups: [discovery.k8s.io]
    resources: [endpointslices]
    verbs: [get, list, watch]
  - apiGroups: [apps]
    resources: [deployments, statefulsets, daemonsets, replicasets]
    verbs: [get, list, watch]
  - apiGroups: [batch]
    resources: [jobs, cronjobs]
    verbs: [get, list, watch]
  - apiGroups: [networking.k8s.io]
    resources: [ingresses]
    verbs: [get, list, watch]
  - apiGroups: [kubevirt.io]
    resources: [virtualmachineinstances, virtualmachines]
    verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: trica-collector
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: trica-collector
subjects:
  - kind: ServiceAccount
    name: trica-collector
    namespace: trica
```

With `argocd`, add a namespaced grant in the Argo CD namespace (the chart does
it with `collect.kubernetes.rbac.argocd`); nothing else of Argo CD, and never
its Secrets:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: trica-collector-argocd, namespace: argocd}
rules:
  - apiGroups: [argoproj.io]
    resources: [applications]
    verbs: [get, list, watch]
```

`GET /version` needs no grant (the `system:public-info-viewer` role covers it).
For a collector running outside the cluster, create a token for the
ServiceAccount (`kubectl create token trica-collector -n trica --duration=...`,
or a long-lived token Secret created by the cluster administrator) and
reference it with `password: file:...`; the collector itself never reads that
Secret from the cluster.

## Versions

| Cluster | Status |
|---|---|
| K3s v1.31 with Cilium, KubeVirt 1.4 | Modelled on a synthesised fixture (`testdata/k3s-1.31`), **not tested on a real cluster**. |
| kubeadm v1.29 | Modelled on a synthesised fixture (`testdata/kubeadm-1.29`), **not tested on a real cluster**. |
| Other Kubernetes (RKE2, EKS, GKE, OpenShift, ...) | Untested. Only GA APIs are used (`v1`, `apps/v1`, `batch/v1`, `networking.k8s.io/v1`), which exist since Kubernetes 1.21 (`batch/v1` CronJob). |
| Build cluster with Argo CD v3.3 on k3s v1.31.1 | Recorded 2026-10-26 (`testdata/build-1.31`, `dev-1.31`), anonymised; read through the fake, **not run by trica against the live Argo CD**. |
| KubeVirt | `kubevirt.io/v1` VirtualMachineInstance, as served by KubeVirt 1.x. Untested on a real cluster. |

The client library is `k8s.io/client-go` v0.37, which targets Kubernetes 1.37.
Its supported skew with a server is +-1 minor version; against 1.31 or 1.29 it
is outside that guarantee. The collector only uses list calls on GA APIs, which
the API server keeps serving across versions, and the fixtures model those
servers, but this has not been run against a real 1.29 or 1.31 server.

## What is not collected

| Not collected | Why |
|---|---|
| Secrets, ConfigMaps | A collector that cannot read them cannot leak them; nothing mapped lives there. |
| TLS secrets of an Ingress (the name is not even read) | Same. |
| Init containers, ephemeral containers, container resources and limits | Out of the model so far. |
| Services: endpoints, EndpointSlices | Selector matching is used instead; reserved for the follow-up. |
| Ingress controller addresses (`status.loadBalancer`), IngressClass, Gateway API | Follow-up. |
| Stopped `VirtualMachine` resources | Only running instances are observed; reserved for the follow-up. |
| PersistentVolumes and claims, StorageClasses, CRDs other than KubeVirt | Out of the model so far. |
| Pod and service IPs as `ip_address` | The address alone identifies an `ip_address`, and pod networks overlap between clusters. |
| Pods that have ended, unless `finished_pods` | They run nothing, and on a CI cluster they are most of the pods. |
| Workloads of clusters with no collector (dev, prod) | Only their Argo CD Applications are seen, through the cluster that runs Argo CD. |
| Argo CD cluster Secrets, repository credentials, Application history and operation state | Tokens; or not inventory. |
| Events, metrics, logs | Not inventory. |
| Real-time updates | A run lists everything. Informers (watch) per resource, with a resync, would turn changes into batches within seconds instead of at the next run; the role already grants `watch`. |
