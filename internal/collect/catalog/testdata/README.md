# Test data of the catalog collector

Every file here is **synthesised**: an application catalog is written by people, there is no
device or service to record it from. No real organisation, person, host or address appears in it:
names are invented, e-mail addresses and hosts are under `example.org` (RFC 2606), the serial
number is made up.

| File | What it stands for |
|---|---|
| `applications.yaml` | A catalog of format `trica-catalog` version 1: two teams, one person (her e-mail written in mixed case, to prove it is lowered), two environments, and three applications. `orion` is self-hosted and is the application the kubernetes collector derives from `internal/collect/kubernetes/testdata/build-1.31` (Argo CD project `tenant-orion`): the reconcile test proves both describe one object. `entra-id` is a SaaS with a vendor. `legacy-erp` runs outside Kubernetes: one instance on a VM designated by name, one on a server designated by serial number, with a URL and a host:port endpoint, and a dependency (`payroll`) on an application nobody declares or discovers. |
| `teams.yaml` | A second file of the same catalog, read together with the first from a directory laid out as a mounted ConfigMap: it declares the team `lyra-team` and the application `lyra` (Argo CD project `tenant-lyra` in the same kubernetes fixture). |

The fake (`fake.go`) serves these files over TLS under `/raw/<name>`, the way a Git host serves a
raw file, and `/moved/<name>` as a redirect the collector must not follow.
