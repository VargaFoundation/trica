# synology collector

Reads a **Synology NAS** (DiskStation, DSM 7) through the **DSM Web API**: the device, its network
interfaces and addresses, every drive, the installed packages, the running container images and the
USB UPS it watches. One target is one NAS. Read-only by construction: the client sends form `POST`s to
`/webapi/entry.cgi` and only for the calls of an allow-list, so it cannot reboot, shut down or
configure the NAS ([ADR 0064](../../../docs/adr/0064-synology-dsm-collector.md)).

> Status: **recorded** on 2026-10-26 from one DS1821+ running DSM 7.4.1-90080 Update 1, anonymised
> (`testdata/recorded-2026-10/`), plus a **synthesised** set for what that NAS could not show (a
> crashed pool and drive, a wear reading, no UPS, no Container Manager, two addresses). Never run
> against DSM 6 or against another model. `testdata/README.md` says what was recorded, what was
> anonymised, and which recorded files the collector does not read.

## Target

```yaml
- kind: synology
  name: nas-a1                      # source "synology:nas-a1"
  address: https://192.0.2.57:5001  # the DSM HTTPS port, no path; https only
  username: trica                   # a DSM account of the administrators group, see below
  password: file:/var/run/secrets/collect/nas-a1/password
  tls: {ca_file: /etc/trica/nas-a1.crt}   # DSM's certificate is usually self-signed: ca_file, or explicit insecure: true
  domain: office-a                  # LAN addresses repeat across sites (ADR 0036)
  interval: 5m
  options:
    role: nas                       # optional, a value of the schema's device.role; default nas
    skip: [containers]              # optional: storage, network, packages, ups, containers
```

`address` is required and must be `https`: the password travels in a request body. Any other option
is an error. A run that sets `skip` is **partial**: what the skipped part would have reported is not
withdrawn.

### The account to create

DSM answers these APIs (system information, storage, packages, network) only to an account of the
**administrators** group; there is no read-only role for them. Create a dedicated administrator,
**without 2-step verification**, used only by trica, with a long random password kept in a
Kubernetes Secret or a file (a reference, never in the file of targets). Restrict it in DSM
(Control Panel, Security: allow only trica's address, and the `Login Portal` / firewall rules you
already use). A login that fails because the account requires 2-step verification (DSM codes 403
and 404) says so in the run's error. This is a known limit of DSM, not a choice of trica.

## What is read

| Call (all `POST /webapi/entry.cgi`) | Used for | If it fails |
|---|---|---|
| `SYNO.API.Auth` v6 `login` (`session=trica`, `format=sid`), `logout` | one session per run, always closed | login: the run fails; logout: a warning |
| `SYNO.Core.System` v1 `info` | model, serial, firmware, RAM, CPU cores, uptime, temperature warning | the run fails |
| `SYNO.DSM.Info` v2 `getinfo` | the same, as a fallback; DSM version | the run fails |
| `SYNO.Core.System` v1 `info` `type=network` | NICs: name, MAC, MTU, speed, status, address, mask; hostname | optional |
| `SYNO.Core.Network.Interface` v1 `list` | NICs, only when the call above lists none (no MAC) | optional |
| `SYNO.Core.Network` v1 `get` | server name, gateway address | optional |
| `SYNO.Storage.CGI.Storage` v1 `load_info` | drives, storage pools, volumes, SSD caches | optional |
| `SYNO.Core.Package` v2 `list` | installed packages | optional |
| `SYNO.Core.ExternalDevice.UPS` v1 `get` | the USB UPS | optional |
| `SYNO.Docker.Container` v1 `list` | running containers | optional |

An optional call that fails with DSM error 102 or 103 (no such API: a NAS with no Container Manager,
an older DSM) is a **warning** and the run stays complete. With 104 or 105 (version, permission) it is
a warning and the run is partial. Any other failure of an optional call makes the run partial and
unhealthy, with the call named in the error. Everything else the answers carry (NTP and DNS servers,
share names, the UPS's SNMP settings, container environments) is not declared in the decode
structs, so it is never read.

Error codes are named in the errors: 100 unknown, 101 no parameter, 102 no such API, 103 no such
method, 104 version not supported, 105 and 402 permission denied, 106 session timeout, 107 duplicate
login, 119 session id not found, 400 wrong account or password, 401 account disabled, 403 and 404
2-step verification, 407 address blocked.

## Mapping

| DSM | trica |
|---|---|
| the NAS | `device`, identity `serial` + `name`; `vendor` Synology, `model`, `os_name` DSM, `os_version` (`DSM ` prefix dropped), `role` (option), `memory_bytes` (`ram_size` MiB), `logical_cpus`, `uptime_s` (`up_time` is `H:MM:SS`, H may exceed 24), `base_mac` (eth0's, else the lowest), `primary_ip` (the gateway interface's address), `mgmt_ip` (the target address when it is an IP literal), `health` |
| a NIC `eth*` | `interface` (child of the device): `kind` physical, `mac`, `mtu`, `speed_mbps` (unset when DSM says -1 or 0), `oper_status` (`connected` is up). `pppoe`, `lo`, bonds and bridges are not ports |
| a NIC address that is not link-local | `ip_address` (`prefix_length` from the mask, `role` primary when it is the gateway interface's), `assigned_to` the interface; the runner stamps the target's `domain` |
| a drive | `disk` (child of the device), identity `serial` + `name` (`sata1`, `nvme0n1`): `slot` (`Drive 2`, `M.2 Drive 1`), `model`, `firmware_version`, `capacity_bytes`, `media` (`nvme` when the type says NVMe, `ssd` when `isSsd`, else `hdd`), `protocol` (`SATA`, `NVMe`, `SAS`), `temperature_c`, `health`, `wear_pct` (100 minus `remain_life`, only when DSM says it is trustable and not negative) |
| DSM, a package, a running container image | `software`, key `os/DSM/<version>`, `package/<name>/<version>` (`vendor` is the package's maintainer, else Synology), `container_image/<repository>/<tag>` (no tag is `latest`, a digest is dropped); each with `runs` from the NAS. Stopped containers are not reported |
| the USB UPS (`enable` and `usb_ups_connect`) | `device`, role `ups`, identity `name` = `<NAS name>/ups` (DSM gives no serial), `vendor`, `model` (trimmed), `health` (`online` ok, on battery or replace warning, low battery critical, else unknown); `powered_by` from the NAS |

Not mapped, because the schema has no type for them and none was added: **storage pools, volumes and
SSD caches**. They only feed the NAS's health.

### The `health` of the NAS

`critical` if any drive, pool, volume or SSD cache is `crashed`, `failed` or `broken`; otherwise
`warning` if any of them is in any other state than `normal` (`attention`, `degraded`, ...) or DSM raises
its temperature warning; otherwise `ok`. When the storage could not be read there is no way to say
`ok`: the value is `unknown` unless the temperature warning says `warning`. The enum carries no
reason; the drive, pool and volume states are in the DSM console. A drive's own `health` maps
`normal` to ok, `warning` and `attention` to warning, `crashed`, `failed` and `broken` to critical,
anything else to unknown.

## What the collector will not do

- It never writes: see the allow-list in `client.go` (`allowed`, `readVerbs`) and `readonly_test.go`,
  which proves that the fake received only allow-listed calls, with nothing in a URL, that the guard
  refuses `SYNO.Core.System` `reboot` before sending it, and that the source builds no request that is
  not a `POST` to `/webapi/entry.cgi`.
- It never puts the password or the session id in a URL, a log line, an error or a batch
  (`privacy_test.go`), and never follows a redirect.
- It always logs out, also after a failed or timed-out run, with its own 10 s timeout.

## Tests and what they do not prove

`make ci` runs them all against `FakeDSM` (TLS, embedded fixtures); `reconcile_test.go` sends the batches
through the real engine on PostgreSQL. Not proven: a run against a live NAS from CI; DSM 6; any model
but the DS1821+; an account with 2-step verification (only its error text is tested); a NAS with more
than the 8 drives and 2 pools recorded.
