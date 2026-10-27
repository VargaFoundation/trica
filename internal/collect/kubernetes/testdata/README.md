# Fixtures of the kubernetes collector

**Recorded (2026-10-26): `build-1.31/` and `dev-1.31/`**, from a k3s v1.31.1
cluster pair and the Argo CD v3.3.18 that manages them (203 Applications; 19
kept in `build-1.31/applications.yaml`: tenant applications in dev and prod, platform
applications, an in-cluster one, a multi-source one). Read with `kubectl get` on
`build` and `dev` (get only) and cut down to the fields the collector reads
(nodes, namespaces, workloads, ReplicaSets, pods, services, ingresses,
Applications). Anonymised: tenant names (`orion`, `lyra`, `vega`, `altair`, `hiring`),
host names (`example.test`), the repository organisation (`example-org`), the
project (`infra`), node names (`kube-build-01`, `kube-dev-01`...), every IP
(documentation ranges; the dev API server is `203.0.113.1`, prod
`198.51.100.1`), UIDs, UUIDs and commit SHAs (remapped). Dropped: environment
variables, container arguments, annotations other than `trica.io/`, Application
history, resources and operation state, Secrets and ConfigMaps (never read; the
Argo CD cluster Secrets, which hold tokens, were not read at all).
`build-1.31/applications-synthesised.yaml` is **synthesised**: a multi-source
Application with a credentialed repository address (a fake `EXAMPLE-NOT-A-SECRET`),
one annotated `trica.io/application`, one with an unmapped destination.
`k3s-1.31/` and `kubeadm-1.29/` below are still synthesised.

**These fixtures are synthesised from the Kubernetes and KubeVirt API
documentation and from the shape of objects the author knows. None of them was
recorded from a real cluster.** They are written to be valid for the API types
(the typed ones are decoded strictly by `fake.go`: an unknown field fails the
load), but a real cluster can differ in ways that cannot be guessed here.

Everything is anonymised by construction: addresses are in the documentation
ranges (192.0.2.0/24 for nodes and the load balancer, 198.51.100.0/24 for the
service network, 203.0.113.0/24 for the pod network, 198.51.100.0/24 again for
the kubeadm nodes), names are generic, UIDs and system UUIDs are
`00000000-0000-4000-...` patterns, MAC addresses are locally administered
(`02:00:...`), registries are `registry.example.test`. The ranges are reused
across roles because the documentation ranges are the only ones allowed;
real clusters use private ranges that overlap between clusters, which matters
for `ip_address` (see the package README).

## What each fixture models

| Directory | Models | Contains |
|---|---|---|
| `k3s-1.31/` | K3s v1.31.4+k3s1 with Cilium 1.16 and KubeVirt 1.4 | 3 nodes (1 control plane with etcd, 2 workers, one of them without role label), 8 namespaces, CoreDNS, local-path-provisioner, metrics-server, Traefik, Cilium (operator + DaemonSet), node-exporter, an application (Deployment of 2 replicas with a sidecar, StatefulSet, CronJob and its finished Job), 6 services (ClusterIP, headless, LoadBalancer, no selector), 1 Ingress with two hosts, 2 KubeVirt VirtualMachineInstances with their virt-launcher pods, and a Secret (see below) |
| `build-1.31/` | Recorded: the build cluster (Argo CD and Harbor namespaces) | See above; `applications.yaml` is read through the dynamic fake. |
| `dev-1.31/` | Recorded: a development cluster | 3 nodes, namespaces `orion` and `lyra`, no Argo CD. |
| `kubeadm-1.29/` | kubeadm v1.29.9 | 2 nodes (one `NotReady`, the control-plane label has an empty value as kubeadm sets it), CoreDNS (2 replicas, 1 ready), kube-proxy, one application, a NodePort service; no Ingress, no KubeVirt |

`version.json` is the body of `GET /version`; the other files are lists of
objects as `kubectl get -o yaml` would print them (one document per object).
KubeVirt objects (`kubevirt.io`) are served by the dynamic fake and are not
validated against a schema, since the fake has none: what the collector reads
of them is the subject of `units_test.go`.

## The Secret

`k3s-1.31/secrets.yaml` holds a Secret whose content is a marker string. It is
a canary: the collector must never read Secrets, the tests prove that no
request touches them, and that the marker never appears in a batch. It is not a
credential.

## What to verify against a real cluster

Nothing here replaces a run against a real cluster. In particular:

- **Node `systemUUID`**: the kubelet reports the SMBIOS UUID of the machine.
  On virtual machines cloned from a template it can be duplicated; the
  `hosts` link from a device (matched on `hw_uuid`) is then wrong or
  ambiguous. Compare with what the BMC or hypervisor collector reports.
- **Role labels**: K3s sets `node-role.kubernetes.io/control-plane`, `etcd` and
  `master` to `"true"`, kubeadm sets `control-plane` to an empty value, RKE2
  and managed services use other labels. Only the first is modelled.
- **Owner references**: Deployment -> ReplicaSet -> Pod and CronJob -> Job ->
  Pod are modelled as the controllers set them (`controller: true`); operators
  and custom controllers add other owners that are ignored.
- **KubeVirt**: field names (`status.interfaces`, `status.guestOSInfo`,
  `status.conditions`, `spec.domain.firmware.uuid`, ...) follow the KubeVirt v1
  API; `guestOSInfo` and the guest-reported addresses only exist when the guest
  agent runs. Check a VMI with a hot-plugged interface, a `masquerade` and a
  `bridge` binding, and a paused one.
- **Quantities**: allocatable memory is read from `status.allocatable`, which
  is what the scheduler can use, not the machine's capacity.
- **Paging**: the fake ignores `limit`; real paging is exercised against an
  HTTP server in `real_test.go`, not against an etcd-backed API server.
- **Versions**: only `/version` differs between the two fixtures; behaviour
  that changes between Kubernetes releases (for example `Endpoints`
  deprecation in favour of EndpointSlices) is not modelled because the collector
  does not read those resources yet.
