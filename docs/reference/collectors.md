# Running collectors

Collectors (`internal/collect/<kind>`) read equipment and submit batches; they never write to a
device and never write the inventory (only `internal/reconcile` does, ADR 0005). This page is
about running them on a schedule: `internal/collect/runner`, which both modes below share. Design:
[ADR 0026](../adr/0026-collector-runner.md). What is and is not proven:
[STATUS](../plan/STATUS.md), row 37. Each collector's own mapping and settings are in its README
(`internal/collect/<kind>/README.md`).

## The targets file

One YAML file lists what to collect from. Unknown keys are an error, as are a missing `kind` or
`name`, two targets with the same `kind:name`, and a password that is not a reference: a typo or
a pasted secret stops the start instead of collecting nothing.

| Key | Meaning |
|---|---|
| `kind` | `arista`, `kubernetes`, `maas`, `mikrotik`, `pfsense` or `redfish`. |
| `name` | Identifies the target. The batch source is `<kind>:<name>`, which is also the key of the health board and of the logs. |
| `address` | Where to connect; the form depends on the collector (examples below). |
| `username` | Account name, for the collectors that use one. |
| `password` | **A reference, never a value**: `file:/path` (a mounted Secret; a trailing newline is trimmed) or `env:NAME`. |
| `tls` | `ca_file`, `insecure` (explicit, per target), `min_version`. |
| `timeout` | Bound of one run, `30s` by default. |
| `interval` | Time between runs, `5m` by default. |
| `domain` | Routing domain (VRF, site, network) the addresses and prefixes the target reports are unique in, stamped on all of them: the same private address under two domains is two objects ([ADR 0036](../adr/0036-identity-scope.md)). Empty (default): the global domain. Letters, digits, `.`, `_`, `:`, `-`, at most 64; anything else stops the start. Set it before the first run: changing it creates new objects. |
| `options` | Collector-specific settings; unknown keys are an error. |

One example per kind (documentation addresses, names and MACs; nothing here is a real device):

```yaml
targets:
  - kind: arista               # eAPI (EOS)
    name: sw-a1
    address: https://192.0.2.10
    username: monitor
    password: file:/var/run/secrets/collect/arista-credentials/password
    interval: 5m

  - kind: mikrotik             # RouterOS API
    name: sw-a2
    address: 192.0.2.11        # port 8728, or 8729 with options.tls
    username: monitor
    password: file:/var/run/secrets/collect/mikrotik-credentials/password
    options:
      tls: true
      role: switch
      skip: [health]

  - kind: pfsense              # pfSense REST API v2; password is the API key
    name: fw-a1
    address: 198.51.100.1:443
    password: file:/var/run/secrets/collect/pfsense-key/key
    tls: {ca_file: /etc/trica/lab-ca.crt}
    options:
      skip: [nat]

  - kind: redfish              # a server's BMC
    name: srv-01
    address: 198.51.100.21
    username: trica-ro
    password: env:SRV_01_BMC_PASSWORD
    options:
      log_days: 7

  - kind: maas                 # password is the API key; username is not used
    name: lab
    address: http://198.51.100.30:5240
    password: file:/var/run/secrets/collect/maas-key/key

  - kind: kubernetes           # inside the cluster: the pod's service account
    name: prod
    address: in-cluster
    options:
      cluster: prod
      finished_pods: false     # default: pods that have ended are not collected

  - kind: kubernetes           # from outside: a kubeconfig
    name: lab
    address: https://203.0.113.5:6443
    password: file:/var/run/secrets/collect/lab-token/token
    options:
      cluster: lab
      kubeconfig: /etc/trica/lab.kubeconfig
```

Every run of a target ends in one health record on the board (`GET /api/v1/health/collection`):
the collector's own, or, when it returned an error, panicked or reported none, one made by the
runner (`collect.Failed` / `collect.Succeeded`). A failing target never stops the others, a run
that is still going is not started a second time (it is skipped and counted), and the first run
of each target waits a random fraction of its interval (10 % at most) so that a restart does not
hit every device at once.

## Mode 1: in the server

```yaml
# trica.yaml
collect:
  targets_file: /etc/trica/collect/targets.yaml
```

The server loads the file at start (a file that does not parse stops the start) and runs the
targets as a component. Batches go to the reconcile engine in the same process, with the target's
source as author. Changes to the file need a restart.

With several replicas, **one collects at a time**: it holds a PostgreSQL advisory lock for as long
as it lives, the others retry every 15 seconds and take over when it stops or loses its database
connection. The devices must be reachable from the server's network; for those that are not, use
mode 2.

Chart: `collect.enabled`, `collect.targets` (or `collect.targetsConfigMap`), `collect.secrets`
(existing Secrets mounted under `/var/run/secrets/collect/<name>/`) and `collect.kubernetes.rbac`
(a read-only ClusterRole for an `in-cluster` kubernetes target: `get`, `list` and `watch` on what
the collector reads, no Secrets, no ConfigMaps; off by default). See
[`charts/trica/README.md`](../../charts/trica/README.md).

## Mode 2: a remote collector

`trica collector --config FILE` (or `TRICA_COLLECTOR_CONFIG`) runs the same targets next to the
equipment and sends the batches to the server over the mutual-TLS transport
([transport](transport.md), ADR 0008). The server needs `transport.addr`.

```yaml
# collector.yaml
server_url: https://trica.example.test:8443
server_name: trica.example.test       # optional, the name in the server certificate
ca_file: /etc/trica/ca.pem            # the server's ca.pem, handed over out of band
state_dir: /var/lib/trica-collector   # persistent
targets_file: /etc/trica/targets.yaml
flush_interval: 30s                   # how often spooled batches are resent
metrics_addr: ":9102"                 # optional
enrol_token_files:                    # only until a target has its certificate
  "arista:sw-a1": /run/secrets/enrol/arista-sw-a1
```

- **One identity per target.** A client certificate is bound to a source, and a collector's
  source is `<kind>:<name>`, so each target is enrolled on its own: its own one-time token
  (`enrol_token_files`, a file reference, never a value in the configuration), certificate and
  key in `state_dir/<kind>_<name>/` (key 0600), and its own spool there. The token is needed
  once; after the certificate exists the file can be removed. A target without a token and
  without a certificate delivers nothing.
- **No loss when the server is away.** A batch the server could not take is spooled on disk and
  resent every `flush_interval`; the spool is bounded and drops the oldest batch when full.
- Credentials of the equipment are file or environment references, exactly as in mode 1.
- Not proven: renewal of the client certificate over a long run (the transport tests cover it;
  the collector's use of it is not run for longer than the tests).

## Metrics

Mode 1 serves them on the server's `/metrics`, mode 2 on `metrics_addr`. Labels `collector` and
`target`.

| Metric | Meaning |
|---|---|
| `trica_collect_runs_total` | Runs finished, successful or not. |
| `trica_collect_failures_total` | Runs that failed. Alert on its rate. |
| `trica_collect_skipped_total` | Runs not started because the previous one was still going: the interval is shorter than a run. |
| `trica_collect_duration_seconds` | Duration of a run (histogram). |
| `trica_collect_last_success_timestamp_seconds` | When the target last succeeded. Alert on `time() - ...`. |
| `trica_collector_spooled_batches` | Mode 2, label `source`: batches waiting for the server. |
| `trica_collector_sent_batches_total` | Mode 2: batches the server accepted. |
| `trica_collector_dropped_batches_total` | Mode 2: batches lost to a full spool. |

Logs are JSON with the keys `source`, `collector` and `target`; they never contain a credential.
