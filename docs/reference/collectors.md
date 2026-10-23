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
| `kind` | The collector module: `arista`, `kubernetes`, `maas`, `mikrotik`, `pfsense`, `redfish` or `unifi` today. `GET /api/v1/collectors` (or the Collectors page) is the authoritative list of what this build knows. |
| `name` | Identifies the target. The batch source is `<kind>:<name>`, which is also the key of the health board and of the logs. |
| `address` | Where to connect; the form depends on the collector (examples below). |
| `username` | Account name, for the collectors that use one. |
| `password` | **A reference, never a value**: `file:/path` (a mounted Secret; a trailing newline is trimmed) or `env:NAME`. |
| `tls` | `ca_file`, `insecure` (explicit, per target), `min_version`. |
| `timeout` | Bound of one run, `30s` by default. |
| `interval` | Time between runs, `5m` by default. |
| `domain` | Routing domain (VRF, site, network) the addresses and prefixes the target reports are unique in, stamped on all of them: the same private address under two domains is two objects ([ADR 0036](../adr/0036-identity-scope.md)). Empty (default): the global domain. Letters, digits, `.`, `_`, `:`, `-`, at most 64; anything else stops the start. Set it before the first run: changing it creates new objects. |
| `options` | Collector-specific settings; unknown keys are an error. |
| `options.vlan_domain` | Arista, MikroTik, pfSense: the layer-2 domain the target's VLANs are identified in (`<domain>:<vid>`), `default` when unset. Targets on the same network use the same domain, so a VLAN seen by a switch and by the firewall is one object ([ADR 0050](../adr/0050-vlan-layer2-domain.md)). MAAS has `options.vlan_domains`, a map from fabric name to domain (a fabric not listed is its own domain), and `options.untagged_vids`, a map from fabric name to the VID the switches use for the fabric's untagged VLAN, which MAAS reports as VID 0. |

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
      vlan_domain: default     # the same as the switches': one VLAN object, not two

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

  - kind: unifi                # UniFi Site Manager (cloud); password is the API key, no address
    name: office
    password: env:UNIFI_API_KEY          # or file:/var/run/secrets/collect/unifi-key/key
    domain: office-a                     # LAN addresses repeat across sites (ADR 0036)
    interval: 5m
    options:
      hosts: [office-a]                  # optional: consoles to read; default every console of the key
      roles: ["UAH-DOOR=access_control"] # optional: shortname or model = device role
      isp_metrics: true                  # optional, default true: latency, loss and uptime of the active Internet access

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

`unifi` is the first collector that reaches an Internet service rather than a device on your
network: it reads `https://api.ui.com` (the UniFi Site Manager API) with an API key that covers
**every console of the UniFi account** that created it (restrict a target with `options.hosts`,
and prefer a key from a dedicated account with the least role that sees the consoles). The server
needs egress to `api.ui.com:443`; `HTTPS_PROXY` is honoured. A NetworkPolicy cannot name a DNS
name: with `networkPolicy.egressTo` set, allow port 443 to the Internet (`ipBlock: 0.0.0.0/0`) or
send the traffic through a proxy you allow. The answers carry the account's e-mail, name and
permissions and the console's location; none of it is decoded, stored or logged
([ADR 0057](../adr/0057-unifi-site-manager.md), [the module's README](../../internal/collect/unifi/README.md)).
Each run also asks for the last 30 minutes of the site's ISP metrics (`GET /v1/isp-metrics/5m`) and
keeps the newest period's latency, maximum latency, packet loss and uptime on the Internet access of
the WAN in use, as curves; `isp_metrics: false` turns that call off, and a failing call is a warning,
never a failed run ([ADR 0059](../adr/0059-unifi-isp-metrics.md)).

The Site Manager API says nothing about where a device stands: people place the access points and
readers in a site, building or room (`located_in`, declared from the object page or a declaration
file), which gives them a site; a collector's own `located_in` is dropped with a warning
([ADR 0058](../adr/0058-placing-devices-outside-racks.md)).

With `options.api: network` a `unifi` target reads one console's own Network Integration API
instead (`address` is the console, `https://192.0.2.1`; the key is created on the console), from the
LAN, so it runs with `trica collector` next to the console, with the same `domain` as the cloud
target: one device per MAC, the ports of each device with their PoE (`poe_status`, `poe_standard`),
and `uplinked_to` from each device to the device it reaches the network through. The API names no
uplink port, so no cable is deduced (the topology page draws the uplink as a device-level line,
[ADR 0062](../adr/0062-topology-uplinks.md)); clients are never requested
([ADR 0060](../adr/0060-unifi-network-integration-api.md)). Synthesised from the specification,
never run against a console.

```yaml
  - kind: unifi
    name: office-lan
    address: https://192.0.2.1
    password: file:/etc/trica/unifi-console-key
    domain: office-a
    options:
      api: network
      sites: [default]        # optional
```

Every run of a target ends in one health record on the board (`GET /api/v1/health/collection`):
the collector's own, or, when it returned an error, panicked or reported none, one made by the
runner (`collect.Failed` / `collect.Succeeded`). A failing target never stops the others, a run
that is still going is not started a second time (it is skipped and counted), and the first run
of each target waits a random fraction of its interval (10 % at most) so that a restart does not
hit every device at once.

A target removed from the file stays on the Collectors page as "reported" for as long as its row
exists on the health board: nothing is deleted (a source nobody runs any more is only silent).

## Collector modules

Every collector is a module that describes itself ([ADR 0055](../adr/0055-collector-modules-describe-themselves.md)):
its package registers a `collect.Descriptor` together with its factory, `collect.Register(Descriptor,
New)`, and a descriptor that is incomplete stops the start. The API (`GET /api/v1/collectors`, see
[the API](api.md)) and the **Collectors** pages of the web UI ([UI](ui.md)) are built from the
descriptors, the targets file and the health board, so a module appears there with no change to
either. Nothing is stored: the view is computed at each request.

| Field | What it says |
|---|---|
| `Kind` | The source prefix (`arista`), `^[a-z][a-z0-9-]*$`; the `kind` of a target. |
| `Title`, `Vendors`, `Protocol` | What it is: "Arista EOS", "Arista Networks", "eAPI: JSON-RPC 2.0 over HTTP(S)". |
| `Summary` | One sentence, in English and French. |
| `Deployment` | `target` (a line of the targets file, run by the server or by `trica collector`) or `agent` (installed on the host it inventories: no target and no factory; the host agent is the module `agent`). |
| `Collects` | The object types it emits. Every fake run is checked against it (`collecttest.Run`). |
| `ReadOnly` | How read-only is enforced. |
| `Address`, `Username`, `Secret` | What a target's `address` takes; whether `username` and `password` are `required`, `optional` or `unused`, and what to put there. `password` is always a reference. |
| `Options` | The struct `DecodeOptions` decodes into. The option table is generated from its `yaml`, `doc`, `default` and `enum` tags, so it cannot drift from what the collector accepts. |
| `Tested`, `Evidence` | The equipment versions the fakes stand for, and whether they are `synthesised`, `recorded` or `field`, as the package's `testdata/README.md` says. This is the compatibility matrix of the roadmap. |
| `Docs` | The module's README, linked on GitHub (the tag of the build, else `main`). |

What the pages show of a target: its address (URL credentials cleaned), interval, timeout, routing
domain, TLS settings and options; a secret-kind option or one named like a secret shows as its
reference, never as a value. The username and the secret reference of a target are shown to an
administrator only; a plain password written in the file (which the runner refuses) shows as
`[redacted]`. A remote `trica collector` keeps its targets on its own host: the server sees only
its health, so its instances show as "reported", with no configuration.

An instance is `ok` or `failing` after its last run, `pending` before its first, and `stale` when
its last attempt is older than three intervals plus a timeout (the collector is not running at all).

### Adding a module

1. Create `internal/collect/<kind>` with a `var Descriptor = collect.Descriptor{...}` and
   `func init() { collect.Register(Descriptor, New) }`. Give every field of the `Options` struct
   a `yaml` tag and a `doc` tag (and `default` or `enum` where they apply).
2. Write its README (the descriptor's `Docs` must exist) and a `Fake*` with its `testdata/`
   directory and `testdata/README.md` (recorded or synthesised, from which version, what was
   anonymised); set `Evidence` accordingly.
3. Run the fake through `collecttest.Run`: it fails for a type missing from `Collects`.
4. Add one import line to `internal/collect/all/all.go`. `TestEveryModulePackageIsImported` fails
   when it is missing, and `TestEveryModuleIsDescribed` checks the descriptor, the README and the
   declared types.

Nothing else changes: the endpoints and the pages list the new module, its instances and their
health.

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
