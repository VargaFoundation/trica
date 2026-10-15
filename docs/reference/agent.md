# Linux agent: host inventory

`trica-agent` observes the Linux host it runs on and reports it as one
full-scope `sdk.Batch` per run, from the source `agent:<hostname>`. Code:
`internal/agent` (probes, mapping) and `cmd/trica-agent` (the binary).

With `--server` the batches are sent to a trica server over mutual TLS, with an
on-disk spool while it is unreachable ([below](#sending-to-a-server),
[ADR-0027](../adr/0027-agent-sends-to-the-server.md)). Without it they go to
`--output` (stdout or a file), for offline use.

**Not in this release**: the network side of remote update (the `Source` that talks to
the server). Packaging, the DaemonSet example, the update machinery and the footprint
benchmark are described below (ADR-0022).

```
trica-agent [--root /] [--interval 5m] [--once] [--output -] [--state-dir DIR] [--health-timeout 2m]
            [--server https://host:port --ca-file ca.pem [--enrol-token-file FILE]]
```

| Flag | Meaning |
|---|---|
| `--root` | Directory the probes read, default `/`. Any other root is a tree to describe (a test fixture, a mounted image): the agent then runs **no command** and asks **no netlink**, since both would describe the machine it runs on instead of the tree. |
| `--interval` | Time between runs, default 5 m, minimum 30 s (`--once` ignores it). |
| `--once` | Run once and exit. |
| `--state-dir` | Directory of the agent's own state (`/var/lib/trica-agent` in the packages): staged versions and update state, and, with `--server`, the client certificate and the spool. Empty (default): the agent never runs anything but itself, and `--server` is refused. |
| `--server` | `https://host:port` of the server's transport listener (`transport.addr`). Batches go there through `uplink.Client`. `http://` is refused. Env `TRICA_AGENT_SERVER`. |
| `--ca-file` | PEM file of the CA certificate that signs the server's certificate, required with `--server`. The agent trusts nothing else: there is no trust on first use. Env `TRICA_AGENT_CA_FILE`. |
| `--enrol-token-file` | File holding the enrolment token: one-time for this host, or a class token for a fleet ([ADR 0035](../adr/0035-class-enrolment-tokens.md)). The agent asks for `agent:<hostname>`. Read only while the agent has no certificate. A path, never a value (no flag or variable takes the token itself). Env `TRICA_AGENT_ENROL_TOKEN_FILE`. |
| `--health-timeout` | How long a newly staged version has to emit one batch before it is rolled back, default 2 m. |
| `--output` | `-` (default): one JSON batch per line on stdout. A path: the latest batch, rewritten atomically (temporary file next to it, mode 0600, rename). With `--server`, nothing goes to stdout; a given `--output` is then a local copy written in addition to the send. |

Logs are JSON (`log/slog`) on stderr, with the keys `source` and `collector`.
Exit codes: 0 ok, 1 failure (bad root, output not writable, no host identity, the
server refused the batch, transport cannot start), 2 bad invocation (including
`--server` without `--ca-file` or `--state-dir`, or not `https://`).

## Sending to a server

```
# once, on the server side: a one-time token for this host (see getting-started, "First credentials")
#   trica enrol create --binding agent:<hostname> --ttl 24h
# on the host: the CA certificate and the token as files, then
trica-agent --server https://trica.example.com:8443 --ca-file /etc/trica-agent/ca.pem \
            --enrol-token-file /etc/trica-agent/enrol-token --state-dir /var/lib/trica-agent
```

- **Source and binding.** The batch source stays `agent:<hostname>`, the hostname the
  probe read. The token must be for exactly that binding (`agent:<hostname>`): the
  certificate carries it, and the server refuses (403) any batch whose source is not the
  binding or an extension of it (`pki.Allows`). A host whose name differs from the one the
  token was made for **enrols, then every batch is refused**: the agent logs the 403 and
  exits 1 (a 4xx is never spooled, it would fail again).
- **Enrolment.** At the first send there is no certificate: the agent generates an
  ECDSA P-256 key, sends a CSR with the token, and keeps key and certificate in
  `<state-dir>/identity.pem` (mode 0600, written atomically). The token is then used up
  server side. At the next start the agent finds the certificate and **never reads the
  token again** (the file may be deleted, or still be there and used up). The certificate
  is short-lived (7 days by default) and renewed by the agent itself, with a new key, when
  a third of its life is left.
- **Spool.** `<state-dir>/spool/` (directory 0700, one file per batch, 0600), bounded to
  64 MiB, oldest dropped first. When the server is down, a 5xx, 408 or 429, the batch is
  spooled and the run **succeeds** (exit 0). The next send, or the background flush of a
  long-running agent (every minute, a directory listing when the spool is empty), resends
  the spooled batches oldest first, before anything newer. This also covers the very first
  start: the batch is spooled, and enrolment happens when the server is back.
- **Secrets.** Neither the token nor the key is ever logged; tests assert it.
- **Read-only.** The agent writes only `--output` and its state directory.

### Troubleshooting

| What you see (stderr, JSON) | Cause | What to do |
|---|---|---|
| `server unreachable or failing, batch spooled` | network, TLS or 5xx failure | nothing: it is delivered when the server is back; if it stays, `trica_agent_spool_oldest_age_seconds` grows ([Metrics](#metrics)) |
| `read enrolment token file` | no certificate yet and the token file is missing or unreadable | create the file, readable by the agent's user |
| `server answered 401` at enrolment | token unknown, used or expired (the server does not say which) | ask for a new token |
| `server answered 403` on a batch | the token was for another binding than `agent:<hostname>` | revoke, then a token for the right binding; `hostname` is what the agent reads, not what DNS says |
| `x509: certificate signed by unknown authority` | `--ca-file` is not the CA of that server (it was recreated?) | copy the current `ca.pem` |
| `certificate expired and renewal failed` | the agent was off longer than the certificate lived | new token (the old binding can enrol again) |
| spool files that never leave | a 401 stops the flush and keeps the spool | the certificate was revoked: enrol again with a new token, delete `identity.pem` first |

### Metrics

The agent opens no port. With `--metrics-file` (or `TRICA_AGENT_METRICS_FILE`), it writes
the state of its transport in the Prometheus text format, for the node exporter's
[textfile collector](https://github.com/prometheus/node_exporter#textfile-collector)
(ADR 0033): every minute and at exit for a running agent, after the collection with
`--once`. The file is replaced atomically (a temporary file in the same directory, then a
rename, mode 0644; it holds no secret), so its directory must be writable by the agent, and
its name must end in `.prom`. It needs `--server`: there is nothing to report without one.

| Metric | Type | Meaning |
|---|---|---|
| `trica_agent_spooled_batches` | gauge | Batches waiting in the spool. |
| `trica_agent_spool_bytes` | gauge | Their size; the spool holds 64 MiB, oldest dropped first. |
| `trica_agent_spool_oldest_age_seconds` | gauge | Age of the oldest waiting batch: how long the server has been out of reach; 0 when the spool is empty. **The one to alert on.** |
| `trica_agent_last_delivery_timestamp_seconds` | gauge | When the server last accepted a batch from this process; 0 if never. |
| `trica_agent_sent_batches_total` | counter | Batches the server accepted. |
| `trica_agent_dropped_batches_total` | counter | Batches lost to a full spool. |
| `trica_agent_rejected_batches_total` | counter | Spooled batches the server refused on resend, discarded. |
| `trica_agent_metrics_timestamp_seconds` | gauge | When the file was written: a stale value means the agent stopped. |
| `trica_agent_build_info` | gauge | 1, label `version`. |

Counters start at zero with each process, as an endpoint's would: with `--once` (a timer
running the agent), they count one run, and `rate()` means nothing; the gauges do.

Alerts, as a starting point (PromQL; the node exporter adds the `instance` label):

```yaml
- alert: TricaAgentSpoolGrowing        # the server has been out of reach for an hour
  expr: trica_agent_spool_oldest_age_seconds > 3600
- alert: TricaAgentLosingBatches       # the spool is full: inventory is being lost
  expr: increase(trica_agent_dropped_batches_total[1h]) > 0
- alert: TricaAgentMetricsStale        # the agent stopped writing (stopped, or crash-looping)
  expr: time() - trica_agent_metrics_timestamp_seconds > 900
```

With the packages, the unit's file system is read-only outside `/var/lib/trica-agent`:
give it the textfile directory with a drop-in, and the directory to the agent's group.

```
# /etc/systemd/system/trica-agent.service.d/metrics.conf
[Service]
ReadWritePaths=/var/lib/node_exporter/textfile_collector
# /etc/trica-agent/agent.env
TRICA_AGENT_METRICS_FILE=/var/lib/node_exporter/textfile_collector/trica-agent.prom
```

In the example DaemonSet the only writable host path is `/var/lib/trica-agent`: write to
`/var/lib/trica-agent/metrics/trica-agent.prom` (the agent creates the directory, mode 0755)
and mount that directory as the node exporter's textfile directory. Not verified: neither layout has been run against a real
node exporter (the tests parse the file with the Prometheus text parser).

## Guarantees

- **Read-only.** The agent writes nothing but its own `--output`. Files are read
  through `os.Root`, which refuses to follow a symbolic link out of the root.
  Commands go through one `Runner`; the production `ExecRunner` accepts an exact
  allow-list of command lines and nothing else (below), looks the binary up in
  fixed system directories (not `PATH`), runs it with an empty environment, a
  20 s timeout and a 32 MiB output cap, and discards stderr.
- **No secret.** The agent never reads `/proc/<pid>/environ` or `cmdline`: a
  process is known by its `comm` (its executable name) and nothing else. Its own
  environment is not passed to commands and not reported. Tests assert both.
- **A probe that fails is not fatal.** Each probe runs on its own (a panic is
  recovered); the others still report. The failure goes to `Health.Error` and
  `Health.Warnings`, and `Health.OK` is **false**: the engine then treats the
  full-scope batch as partial and withdraws nothing, so one broken probe does not
  close the claims it would have renewed. A probe that could not look (a tool
  absent, a privilege missing) is not a failure: it is a line in
  `Health.Warnings` and `Health.OK` stays true.
- **No identity, no batch.** If no hostname, machine-id, serial or UUID can be
  read, nothing is emitted (the object could never be recognised again).

## What is collected, from where

| Data | Source | Needs root? | Without it |
|---|---|---|---|
| Hostname | `/proc/sys/kernel/hostname`, else `/etc/hostname` | no | |
| Machine id | `/etc/machine-id` | no | |
| Serial, hardware UUID, vendor, model, BIOS version | `/sys/class/dmi/id/*`, only the files that are world-readable | serial and UUID: yes | skipped quietly; placeholders (`Not Specified`, `To Be Filled By O.E.M.`, all-zero or all-`f` ids, ...) are never reported as identity |
| OS name, version | `/etc/os-release` | no | |
| Kernel | `/proc/sys/kernel/osrelease` | no | |
| CPU sockets, cores, threads, model | `/proc/cpuinfo` | no | |
| Memory | `/proc/meminfo` (`MemTotal`) | no | |
| Virtualisation | files only, no exec: container markers (`/run/systemd/container`, `/.dockerenv`, `/run/.containerenv`), DMI vendor strings, `/sys/hypervisor/type`, the `hypervisor` CPU flag | no | |
| Interfaces: kind, MAC, MTU, state, speed, duplex | `/sys/class/net/*` | no | |
| IP addresses | netlink `RTM_GETADDR` | no | on a non-`/` root: `/addrs.txt` of the tree |
| Primary IP | first IPv4 of the interface of the lowest-metric default route, `/proc/net/route` | no | |
| Listening sockets, outbound connections | `/proc/net/{tcp,tcp6,udp,udp6}`, process name from `/proc/<pid>/comm` through the socket inodes in `/proc/<pid>/fd` | to see other users' processes | sockets are still reported, without a process name; one note says so |
| Packages | `/var/lib/dpkg/status`; else `rpm -qa --qf ...` (only if `rpm` is installed) | no | no package database: one note, no software |
| Services | `systemctl list-units --type=service --output=json --no-pager`, plus the units enabled under `/etc/systemd/system/*.wants` | no | no systemd (`/run/systemd/system` absent): skipped; no `systemctl`: enabled units only, states unknown |
| Disks | `/sys/block` (size, model, serial, firmware, WWN, rotational) | no | |
| Disk health and wear | `smartctl --json -a /dev/<name>`, physical hosts only | yes (smartctl opens the device) | one note "not root" / "smartctl not available"; disks still listed from sysfs |
| LLDP neighbours | `lldpctl -f json` | usually | no `lldpctl`: nothing, quietly (most hosts have no lldpd) |

### Why netlink, from the standard library

Addresses need the interface each belongs to. `/proc/net/fib_trie` lists local
addresses but not the interface; `/proc/net/if_inet6` covers IPv6 only. Netlink
gives address, prefix, interface and flags in one answer. It is read with
`syscall.NetlinkRIB`, so the binary stays `CGO_ENABLED=0` with no new dependency.
Filters: loopback, link-local, multicast and unspecified addresses; IPv6
temporary, DAD-failed, deprecated and tentative addresses; addresses of
interfaces the agent does not report.

### Allow-listed commands

| Command line | Used for |
|---|---|
| `rpm -qa --qf '%{NAME}\t%|EPOCH?{%{EPOCH}:}|%{VERSION}-%{RELEASE}\t%{ARCH}\n'` | packages on RPM hosts |
| `systemctl list-units --type=service --output=json --no-pager` | service states |
| `lldpctl -f json` | LLDP neighbours |
| `smartctl --json -a /dev/<name>` (`name` matches `[a-z][a-z0-9]*`) | disk health and wear |

Anything else is refused with `ErrNotAllowed` before any process is started.

## What is deliberately not reported

- **Volatile values**: uptime, temperature, bytes written, packet counters. They
  change at every run and would write a history row each time. `power_on_hours`
  is rounded **down to a multiple of 24** for the same reason (one change a day
  instead of one an hour).
- **Container and virtual interfaces**: `lo`, `veth*`, `docker0`, `cali*`, `lxc*`,
  `cilium_*`, `flannel*`, `cni*`, `kube-*`, `virbr*`, `vnet*`, `tap*`, `br-<12 hex>`
  come and go with the workloads; their addresses are dropped too.
- **Ephemeral sockets**: only TCP `LISTEN` and unconnected UDP sockets below the
  ephemeral port range (`ip_local_port_range`, default 32768) are listeners.
  Connections are **outbound only** (the local port is not a listening port),
  without loopback, link-local, multicast and own-address peers, and folded to one
  per remote address (the smallest remote port). Caps: 5000 listeners, 1000
  connections; beyond them the probe says so.
- **VM disks and CPUs**: the schema's `disk` and `cpu` types have no `vm` parent.
  A virtual machine reports its vCPU count and memory on the `vm` itself, and
  one warning says its disks are not reported.
- **`hosts` relation**: the agent knows its own device or VM, not the hypervisor
  that runs it.

## Mapping to the schema

| Observation | Object | Identity | Notes |
|---|---|---|---|
| Host | `device` (bare metal, container) or `vm` (hypervisor found) | device: `serial`, `hw_uuid` (strong), `machine_id`, `name` (weak). vm: `uuid` (strong), `machine_id`, `name` (weak) | `machine_id` is weak: a cloned VM image shares it |
| CPU socket | `cpu` child of the device | `name` `cpu<N>` | devices only |
| Interface | `interface` child of the host | `name` | kind, MAC, MTU, status, speed, duplex |
| Address | `ip_address` | `address` | `assigned_to` the interface |
| OS | `software`, kind `os` | key `os/<name>/<version>` | `runs` from the host |
| Package | `software`, kind `package` | key `package/<name>/<version>` | `runs` from the host; a second architecture of the same version gets `:<arch>` appended |
| Service | `service` child of the host | `name` | `state`; `enabled` only when true (a unit can be enabled by a preset the agent does not read) |
| Listener | `listener` child of the host | key `<proto>/<addr>:<port>` | process name |
| Outbound connection | `talks_to` from the host to the remote `ip_address` | | `port`, `protocol`, `process` |
| Disk | `disk` child of the device | `name`, `serial`, `wwn` | media, capacity, firmware, health, `wear_pct`, `spare_pct`, `power_on_hours`, `reallocated_sectors`, `pending_sectors`, `media_errors` |
| LLDP neighbour | `neighbor_of` from the local interface to the peer's interface | peer interface `name` (port id, else description) under the peer device (`base_mac` of a MAC chassis id, `name` of the system name) | `protocol` (`lldp`, `cdp`, ...) |

**Software is keyed per version.** The schema's software key is
`kind/name/version` and global, so an upgraded package is a *new* software object
that the host runs: the engine opens a `runs` relation to it and closes the one to
the old version. That is the schema's model, not a loss: the history of what the
host ran is in the `runs` relation. Tested on PostgreSQL
(`TestUpgradedPackageIsANewSoftwareAndTheOldRunsIsClosed`).

### Schema additions

In `internal/schema/builtin.yaml`: `machine_id` on `device` and `vm` (string,
weak identity, searchable); `kernel` and `primary_ip` on `vm`, as already on
`device`. English and French labels and descriptions are there.

## Footprint

Target: under 50 MB resident and under 1 % of one core.

Two measurements, both in CI:

- `TestFootprint`, `BenchmarkCollect` (`internal/agent/footprint_test.go`, a fake
  root with 2000 packages): heap in use after GC, allocation per run, and the mean
  duration of a collection against the 30 s minimum interval. The cheap early
  warning.
- `TestRealFootprint` (`cmd/trica-agent/footprint_linux_test.go`, `make
  agent-footprint`): the **real static binary**, started as root on the real host
  at the shortest interval (30 s), measured from `/proc/<pid>` from start-up to the
  second batch, so over a full cycle: two collections and the idle time between
  them. It fails above 50 MB of **peak** resident set (`VmHWM`) or 1 % of one core
  (`utime + stime` over the wall-clock window). The CI job `agent` runs it on the
  GitHub runner. A development machine (non-root, shared) gave 9.7 MB and 0.10 %.

- `TestRealFootprintWithTransport` (same file): the same binary and window with the
  transport on (identity already in the state directory, mutual TLS and HTTP/2 to a
  local stand-in server, plus the one-minute flush goroutine). Same thresholds. A
  development machine gave 15.6 MB and 0.10 % (against 15.8 MB and 0.10 % without it).

What is not measured: a host with tens of thousands of processes or sockets (the
runner has a few hundred), enrolment and certificate renewal (a one-off key generation
and two requests), a spool being flushed (a few hundred KiB to a few MiB to send), and
a real network latency.

## Packaging

`make package-agent` builds the static binary (`CGO_ENABLED=0`, `-trimpath`) for
`amd64` and `arm64`, then a **deb and an rpm** each with nfpm (pinned version,
installed with `go install`, not a module dependency) into `dist/`. `VERSION` is
`0.0.0` locally; the release pipeline passes the tag. **The packages are not
signed here**: signing belongs to the release pipeline (PR #17).

The package ships `/usr/bin/trica-agent`, the unit
`/usr/lib/systemd/system/trica-agent.service` and a `sysusers.d` entry for the
`trica-agent` system user (nologin). The post-install script creates the user and
reloads systemd; it does **not** start the service, since the agent needs its
server and update key configured first. The server settings are in
`/etc/trica-agent/agent.env` (`TRICA_AGENT_SERVER`, `TRICA_AGENT_CA_FILE`,
`TRICA_AGENT_ENROL_TOKEN_FILE`, read by the unit's `EnvironmentFile=`; a conffile,
kept on upgrade, no secret in it). The CA and the token are files you place, readable by
the `trica-agent` user (`install -m 0640 -o root -g trica-agent`). Removal stops the service and deletes
nothing (the user and `/var/lib/trica-agent` stay).

The unit runs as `trica-agent`, not root, with `ProtectSystem=strict` (only
`/var/lib/trica-agent` is writable: identity, spool, staged versions), `NoNewPrivileges`, and the capability set
below, derived from what the probes read. Nothing else is granted, and
`packaging/packaging_test.go` fails if the set changes.

| Capability | Probe that needs it | Why |
|---|---|---|
| `CAP_DAC_READ_SEARCH` | host (DMI serial, UUID), disks (SMART) | `/sys/class/dmi/id/{product_serial,product_uuid,board_serial,chassis_serial}` are mode 0400 root; `smartctl` opens `/dev/sdX` (root:disk 0660) |
| `CAP_SYS_PTRACE` | sockets (process name of a listener or connection) | `readlink /proc/<pid>/fd/*` of another user's process is a ptrace-read access check, not a file permission |
| `CAP_SYS_RAWIO` | disks (SMART on SATA/SAS) | `smartctl` uses SG_IO pass-through |

Deliberately **not** granted:

- `CAP_SYS_ADMIN`: NVMe SMART needs admin-command pass-through. Without it NVMe
  wear is skipped. To have it, add a drop-in (`systemctl edit trica-agent`) adding
  `CAP_SYS_ADMIN` to both `CapabilityBoundingSet=` and `AmbientCapabilities=`: that
  is a very broad capability for a wear counter; decide knowingly.
- Access to the lldpd socket (`/run/lldpd.socket` is root:`_lldpd` 0660): add
  `SupplementaryGroups=_lldpd` in a drop-in on hosts that run lldpd. It is not in
  the unit because a group that does not exist stops the unit from starting.
- Netlink addresses and `systemctl list-units` need no capability.
  `PrivateDevices`, `PrivateUsers` and `ProtectProc` are left off because the
  probes read devices and other users' processes.

Not verified: the unit has been checked by `systemd-analyze verify` (no directive
error) and by the tests above, but **has not been started on a real host**, so a
sandbox directive that blocks a probe on some distribution would show only there.
`rpm -qa` on a Berkeley-DB rpmdb may try to write lock files, which
`ProtectSystem=strict` forbids; RHEL 9 and later use SQLite.

## Kubernetes

`deploy/kubernetes/trica-agent-daemonset.yaml` is an **example**: the image does not
exist yet and the manifest has not been applied to a cluster (`kubectl apply
--dry-run=client` only, and not even that for the transport settings). It sets `hostPID` (the node's process table names a
socket's owner) and `hostNetwork` (addresses, `/proc/net/*` and `/sys/class/net`
describe the namespace they are read from), because the probes need both; mounts
three host files read-only (`/etc/os-release`, `/etc/machine-id`, `/var/lib/dpkg`)
because the pod's own root is the image's; runs as uid 0 with **all capabilities
dropped except `DAC_READ_SEARCH` and `SYS_PTRACE`** (capabilities are only
effective for uid 0 in Kubernetes), a read-only root file system, no privilege
escalation and the runtime default seccomp profile. The image has no `rpm`,
`systemctl`, `smartctl` or `lldpctl`, so RPM packages, services, SMART and LLDP
are not inventoried in this mode. Updates are the DaemonSet's rolling update
(`maxUnavailable: 10 %`); `--state-dir` is there for the identity and the spool, and
nothing stages a binary in it. The namespace needs the `privileged`
Pod Security level. If the node's AppArmor profile denies reading
`/proc/<pid>/fd` of host processes, process names come back empty; relaxing the
profile is the cluster operator's call.

**Sending to the server from a DaemonSet.** The CA and the server URL are in the
`trica-agent` ConfigMap of the manifest (replace both). The identity and the spool live in
`/var/lib/trica-agent` on the node (`hostPath`, the one writable host path, checked by
`packaging_test.go`): without it every pod restart would need a new token.

The enrolment token is **one class token for the whole fleet** ([ADR 0035](../adr/0035-class-enrolment-tokens.md)),
in the Secret `trica-agent-enrol` (key `token`), mounted read-only at `/etc/trica-agent-enrol`
by an **optional** volume:

```bash
trica enrol create --prefix agent --ttl 24h > token   # the token id on stderr
kubectl -n trica-system create secret generic trica-agent-enrol --from-file=token=token && rm token
# once every node is enrolled:
trica enrol revoke --id <id> && kubectl -n trica-system delete secret trica-agent-enrol
```

Each agent asks for `agent:<its hostname>` (`EnrolRequest.Binding`, the source its batches
carry). The server mints it if it is one segment under the token's prefix and no unexpired,
unrevoked certificate holds it yet; otherwise 403 (outside the prefix) or 409 (already
enrolled). The token is not consumed, so every node enrols with it, each name once. A node
that has its certificate never reads the token again, so deleting the Secret after the
rollout stops nothing. A node whose state directory is lost (no `hostPath`, a reinstalled
node) gets 409 until its old certificate is revoked (`reg.RevokeBinding`) or expires.

Not verified: that a pod with `hostNetwork` reads the node's hostname in
`/proc/sys/kernel/hostname` on every runtime (the batch source depends on it).

## Known gaps of the transport

- **A leaked class token claims free names**: until it expires or is revoked, its holder can
  enrol any name under the prefix that no node holds yet, and the real node then gets 409.
  Mitigation: the TTL of a rollout, then `trica enrol revoke`. There is no command listing
  the live tokens; only their ids (printed at creation) revoke them (ADR 0035).
- The agent's `--output` with `--server` is written even when the batch could not be sent;
  a failing local copy fails the run.

## Remote update

Code: `internal/agent/update`. The agent updates itself in waves, rolls back when
the new version does not prove itself, and **never installs what it cannot
verify**. The network side is not here: the contract with the server is the
`Source` interface (`Desired`, `Open`), with `FakeSource` for tests: the transport
(this page, above) carries batches only, the update endpoints do not exist yet. `--state-dir` turns on the start-up side (running a
staged version, probation, rollback); the periodic check (`Updater.Run`) is **not
yet started by the binary**, since there is nothing to ask.

```
server: Desired(identity, current, os, arch) -> Offer{version, percent} | nothing
agent : in wave? newer? not known-bad? nothing on probation?
        SHA256SUMS + SHA256SUMS.sig  -> signature verifies with the pinned key?
        artifact (size-capped)       -> SHA-256 is the one SHA256SUMS lists?
        stage versions/<v>/trica-agent, then state.json {current, previous, pending}
restart : Boot() -> exec the staged binary (starts counted, deadline set)
health  : first batch emitted (and delivered, if the server was reachable before)
          -> Confirm(): probation over, previous version kept
failure : crash (3 starts) or no batch within --health-timeout -> Boot() restores
          the previous version and records the failed one as bad
```

- **Waves.** `InWave(identity, version, percent)`: SHA-256 of `version NUL
  identity`, first 8 bytes mod 10 000, below `percent x 100`. Stable for an agent
  and a release, monotonic (widening only adds agents), and reshuffled between
  releases. The server evaluates it per agent and only offers to agents in the
  wave; the agent evaluates it again, so a server that offers to everybody cannot
  push outside the wave.
- **Verification.** The pinned key (PEM, ECDSA P-256 as made by `cosign
  generate-key-pair`, or Ed25519) verifies the signature over `SHA256SUMS`: base64,
  ASN.1 ECDSA over the SHA-256 of the file, as `cosign sign-blob --key` writes it.
  The artifact is staged only if its SHA-256 is the listed one. Standard library
  only: ADR-0022 says why sigstore-go is not used. There is **no default key and no
  way to update without one**.
- **Not an update**: an offer not strictly newer than the running version (no
  downgrade by a replayed offer), a version already marked bad on this host,
  anything while another version is on probation, a version that is not
  `MAJOR.MINOR.PATCH`, a development build (`dev`).
- **Why a state directory and not `/usr/bin`.** The agent is unprivileged and
  `/usr/bin` is read-only to it. The packaged binary stays the launcher: at start it
  asks the state which version to run and `exec`s it (`TRICA_AGENT_BOOTED=1` stops
  a second decision). The state names **versions, never paths**: a tampered state
  file can only pick a directory the updater staged itself. The switch is two
  atomic renames (the binary into place, then `state.json`).
- **Health** is one batch emitted by the new version. If none within
  `--health-timeout`, the agent stops with status 1 and the service manager
  restarts it, which is when the rollback happens. Three starts without a healthy
  batch roll back too.

**Health with a server** (ADR 0027, amended by ADR 0029). A batch the server refuses (4xx),
or a failing `--output`, is never healthy. Otherwise it depends on the spool the new version
finds at start:

| Spool at start | Meaning | Healthy when |
|---|---|---|
| empty | the previous version delivered everything: the server was reachable | a batch is emitted **and** the server accepted at least one batch since the start, directly or from the spool flush |
| not empty | the server was already down before the update | a batch is emitted (accepted or spooled) |

The decision is taken at the first emitted batch if it is already healthy, and otherwise again
at `--health-timeout`, so that a flush of the spool in between counts. A version that spools
while the previous one delivered is stopped at the timeout and rolled back. The cost: a server
outage that starts during the probation window and outlasts `--health-timeout` rolls back a
good version and marks it bad on this host. A version that only spools shows in
`trica_agent_spool_oldest_age_seconds` ([Metrics](#metrics)).

Not verified: the staged binary has been executed in tests through a simulated
service manager, not through systemd; whether a real cosign produces a signature
this code accepts has not been tried (the tests sign the way cosign documents).

## Running it

```
# one run, to stdout
sudo trica-agent --once | jq .

# one run, sent to a server (first run enrols with the token file)
sudo trica-agent --once --server https://trica.example.com:8443 --ca-file ca.pem \
     --enrol-token-file token --state-dir /var/lib/trica-agent

# describe a mounted image without running anything of this machine
trica-agent --once --root /mnt/image
```

Run as root for full coverage; without it, DMI serial and UUID, other users'
process names and SMART are skipped and said so in the batch's `Health.Warnings`.
