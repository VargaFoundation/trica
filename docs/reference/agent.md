# Linux agent: host inventory

`trica-agent` observes the Linux host it runs on and reports it as one
full-scope `sdk.Batch` per run, from the source `agent:<hostname>`. Code:
`internal/agent` (probes, mapping) and `cmd/trica-agent` (the binary).

**Not in this release**: the mTLS transport and the disk spool (ADR-0008), deb/rpm
packages, a DaemonSet, remote update and rollback. Today batches go to
`--output` only (stdout or a file).

```
trica-agent [--root /] [--interval 5m] [--once] [--output -]
```

| Flag | Meaning |
|---|---|
| `--root` | Directory the probes read, default `/`. Any other root is a tree to describe (a test fixture, a mounted image): the agent then runs **no command** and asks **no netlink**, since both would describe the machine it runs on instead of the tree. |
| `--interval` | Time between runs, default 5 m, minimum 30 s (`--once` ignores it). |
| `--once` | Run once and exit. |
| `--output` | `-` (default): one JSON batch per line on stdout. A path: the latest batch, rewritten atomically (temporary file next to it, mode 0600, rename). |

Logs are JSON (`log/slog`) on stderr, with the keys `source` and `collector`.
Exit codes: 0 ok, 1 failure (bad root, output not writable, no host identity),
2 bad invocation.

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

What is measured in the tests (`TestFootprint`, `BenchmarkCollect`, over a fake
root with 2000 packages): heap in use after GC, allocation per run, and the mean
duration of a collection against the 30 s minimum interval. The figures and
thresholds are in `internal/agent/footprint_test.go`.

What is **not** measured here: the real resident set size and CPU of the binary
on a real host. Fake roots do not have a real `/proc` with thousands of
processes and sockets, and the Go runtime's own overhead is not in a heap
figure. That measurement is done on a host in the release pipeline, later.

## Running it

```
# one run, to stdout
sudo trica-agent --once | jq .

# describe a mounted image without running anything of this machine
trica-agent --once --root /mnt/image
```

Run as root for full coverage; without it, DMI serial and UUID, other users'
process names and SMART are skipped and said so in the batch's `Health.Warnings`.
