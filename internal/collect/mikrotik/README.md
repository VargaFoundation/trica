# mikrotik collector

Reads a MikroTik switch or router running RouterOS through the **RouterOS API**
(the binary word protocol: TCP 8728, or TCP 8729 with TLS, "api-ssl"). The
collector kind is `mikrotik`; one target is one device, the batch source is
`mikrotik:<name>`.

**Read-only.** The collector sends `/login` and 15 `print` commands, nothing
else. Every command goes through an allowlist (`readCommands` in `client.go`)
that refuses anything else before it reaches the wire, and a test asserts that
the router only ever receives `/login` and `.../print` without arguments.

**Status: not validated on a real device.** The code and its fixtures were
written from the MikroTik documentation (see `testdata/README.md`). Versions
modelled: RouterOS 7.8 (CRS354) and 6.49.10 (CRS326). Everything else is untested.

## What is collected

| RouterOS command | Equipment data | trica type.attribute |
|---|---|---|
| `/system/identity/print` | Device name | `device.name` (identifier) |
| `/system/routerboard/print` | Serial number | `device.serial` (identifier) |
| | Model (`CRS354-48G-4S+2Q+`) | `device.model` |
| | RouterBOOT firmware (`current-firmware`) | `device.bios_version` |
| `/system/resource/print` | Version (`7.8 (stable)`: first word) | `device.os_version`; `device.os_name` is `RouterOS` |
| | Uptime (`1w2d3h4m5s`) | `device.uptime_s` |
| | Total memory | `device.memory_bytes` |
| | CPU count | `device.cpu_count` |
| (constants and options) | Vendor, role | `device.vendor` = `MikroTik`; `device.role` = `switch` for CRS/CSS models, `router` otherwise, or the `role` option |
| `/interface/ethernet/print` | Base MAC (`orig-mac-address` of the first port; then the port MAC, the first `ether` in `/interface`, last the first bridge MAC) | `device.base_mac` (identifier) |
| (the address of the target) | Management address, only when it is an IP literal | `device.mgmt_ip` (identifier) |
| `/interface/print` | Interfaces (name, comment, MAC, MTU, state) | `interface.name`, `description`, `mac`, `mtu` (`actual-mtu`, else `mtu`), `admin_status` (`disabled`), `oper_status` (`running`), `kind` |
| `/interface/ethernet/print` | Negotiated speed and duplex (only while the link is running) | `interface.speed_mbps`, `interface.duplex` |
| `/interface/bonding/print` | Bond members | relation `member_of` (port to `lag` interface) |
| `/interface/bridge/port/print` | Bridge ports (disabled rows skipped), PVID, frame types | relation `member_of` (port to bridge), `interface.mode`, `interface.native_vlan` |
| `/interface/bridge/print` | Whether a bridge filters VLANs | decides whether the VLAN data of that bridge is used |
| `/interface/bridge/vlan/print` | VLANs and their tagged / untagged ports (configured and current sets united) | `vlan.key` (`<domain>:<vid>`), `vlan.vid`, `vlan.domain`, `vlan.name` (row comment, single-VLAN rows); relation `member_of` (interface to VLAN) with `tagged` |
| `/interface/vlan/print` | VLAN interfaces | relation `member_of` (VLAN interface to VLAN, tagged) |
| `/ip/address/print` | IPv4 addresses (disabled rows skipped) | `ip_address.address`, `prefix_length`; relation `assigned_to` (interface, else the device); `interface.mode` = `routed` for a physical or LAG port that holds an address and has no bridge mode |
| `/ip/neighbor/print` | Discovered neighbours (LLDP, CDP, MNDP) | relation `neighbor_of` (local physical or LAG port to remote `interface` named `interface-name` under the remote `device` named by its short identity), attribute `protocol` (`lldp` preferred over `cdp` over `mndp`, from `discovered-by`, 7.7+) |
| `/interface/bridge/host/print` | MAC address table, on edge ports only | `interface.learned_macs` (`<vid>/<mac>`, lowercase) |
| `/system/package/print` | Installed packages (disabled skipped) | `software` (`kind` = `package`, key `package/<name>/<version>`), relation `runs` |
| `/system/health/print` | Temperatures, voltages, currents | `sensor` objects (`kind`, `placement`, `unit`, `reading`) under the device or the PSU |
| | Fan speed and state | `fan.speed_rpm`, `fan.health` |
| | PSU state, voltage, current | `psu.health` and `sensor` under the `psu` |
| | Power consumption | `device.power_w` |

Notes on the mapping:

- **Identity.** `device` is identified by serial, base MAC, name and (when the
  address is an IP literal) management IP. The RouterBOARD serial is stable;
  the bridge MAC is not (RouterOS derives it from the ports), which is why it is
  used for `base_mac` only as a last resort.
- **Remote devices of neighbour links** are identified by name only
  (`identity`, domain stripped). RouterOS does not expose the LLDP chassis ID:
  the `mac-address` of a neighbour row is the source address of the frame, not
  a chassis MAC, so no `base_mac` is set on the remote side (a wrong strong
  identifier would merge or split devices). MNDP rows have no remote port and
  produce no link.
- **Bridge VLAN data** is used only for bridges that do not say
  `vlan-filtering=false`. Without filtering the VLAN table has no effect, so no
  `mode`, `native_vlan` or VLAN membership is reported from it. A port is a
  `trunk` when it carries a tagged VLAN, `access` otherwise; `native_vlan` is
  the PVID unless the port is `admit-only-vlan-tagged`.
- **Safety cap.** A single `bridge/vlan` row is expanded to at most 512 VLAN
  ids; a row such as `vlan-ids=1-4094` is skipped with a warning.
- **`learned_macs`** is reported on physical and LAG ports that have at most
  `max_edge_macs` addresses (default 4): a trunk learns thousands of addresses
  that say nothing about what is cabled there. The collector lowercases the
  addresses itself (the schema does not normalise list elements). The VLAN id
  is `0` when the table has none.
- **Health shapes.** RouterOS 7.x answers `/system/health/print` with one row per
  sensor (`name`, `value`, `type`); 6.x with one row whose properties are the
  sensors, voltage in decivolts. The shape is detected from the rows, and the
  voltage is divided by 10 on the flat shape only. Names that are not recognised
  are reported in `Health.Warnings`, not guessed (at most 20 warnings are kept).
- **Firmware.** The schema has no firmware version on a device; the RouterBOOT
  version goes to `device.bios_version`.

## Compatibility

| RouterOS | Hardware | Status |
|---|---|---|
| 7.8 (7.x family) | CRS354-48G-4S+2Q+ | **Modelled** from the documentation (synthesised fixture `crs354-ros7.8.json`). Not run on a device. |
| 6.49.10 long-term (6.x family) | CRS326-24G-2S+ | **Modelled** from the documentation (synthesised fixture `crs326-ros6.49.json`). Not run on a device. |
| Other 7.x, other 6.x (6.43 or later for the login), other hardware (CCR, RB, hEX, CSS, CHR) | | **Untested.** Expected to work because attributes are read by name and unknown ones are ignored. |
| Before 6.43 | | Untested. The login is the one of the client library, which handles the older challenge login too; the fake only implements the post-6.43 login. |

Version differences handled: flat vs per-sensor health, `discovered-by`
(7.7 and later) and `actual-mtu` (absent on 6.x) may be missing.

## The account to create on the device

Create a group that can only read, and a user in it. On the device:

```
/user group add name=trica-read policy=read,api
/user add name=trica group=trica-read password=<long random password> address=<collector address>/32
```

Check what the group really has:

```
/user group print where name=trica-read
```

The expected policy list is exactly `read,api`: `read` allows the `print`
commands, `api` allows the API service, and nothing else (no `write`, `policy`,
`sensitive`, `test`, `ftp`, `reboot`, `password`, `sniff`, `romon`, `local`,
`telnet`, `ssh`, `winbox`, `web`, `rest-api`). In RouterOS a group created with
`/user group add` has only the policies that are listed, so the others are
off; the output of the command above is the proof, not this page. If your
version shows more, remove them with `/user group set trica-read policy=read,api`.
A stricter spelling that names the denials, if you prefer explicit negations:

```
/user group add name=trica-read policy=read,api,!write,!policy,!sensitive,!test,!ftp,!reboot,!password,!sniff,!romon,!local,!telnet,!ssh,!winbox,!web,!rest-api
```

The policy names above come from the MikroTik manual (`/user group`, 7.x
pages); they were **not checked on a device**. In particular `rest-api` does not
exist in 6.x and was not confirmed in 7.8: drop it from the explicit list when
the router refuses it.

The `address=` restriction makes the password useless from anywhere else. The
collector reads no secret, and the account has no policy to read one.

For the TLS service (`tls: true`), the router needs a certificate on the
service, otherwise the handshake fails (Go does not offer the anonymous
Diffie-Hellman suites that a certificate-less api-ssl falls back to):

```
/ip service set api-ssl certificate=<certificate name> disabled=no
```

and the collector must trust it: set the target's `tls.ca_file`, or
`tls.insecure: true` for a lab. Plain `api` (8728) sends the password in clear text on the wire.
Prefer api-ssl, or keep the API on a management network.

## Target configuration

```yaml
targets:
  - name: sw-a1
    kind: mikrotik
    address: 192.0.2.10          # port 8728, or 8729 when options.tls is true
    username: trica
    password: env:MIKROTIK_PASSWORD   # or file:/run/secrets/mikrotik-password
    timeout: 30s
    tls:                              # how the certificate is verified (with options.tls: true)
      ca_file: /etc/trica/mikrotik-ca.pem
    options:
      tls: true
      max_edge_macs: 4
      vlan_domain: default
      role: switch
      skip: [health]
```

`address` may carry a port (`192.0.2.10:18728`). With `options.tls: true`,
certificate verification follows the target's `tls:` block (CA file, insecure,
minimum version), like every other collector.

| Option | Default | Meaning |
|---|---|---|
| `tls` | `false` | Use api-ssl (8729 unless the address has a port). |
| `max_edge_macs` | `4` | Report `learned_macs` only on ports with at most this many addresses. `0` reports none. |
| `vlan_domain` | `default` | The layer-2 domain VLAN keys are built in: `<domain>:<vid>`. Use one value per set of devices that share VLAN numbering. |
| `role` | by model | `switch` or `router`. By default CRS and CSS models are switches, everything else a router. |
| `skip` | none | Sections not to collect: `health`, `package`, `neighbor`, `host`. For a device that lacks the command (a CHR has no sensors) so that its permanent failure does not keep the target unhealthy. |

## Failure behaviour

| Situation | Result |
|---|---|
| Secret unreadable, cannot connect, login refused | `collect.Failed` batch (health data), `Collect` returns nil. |
| Every command ok | `ScopeFull`, run succeeded. |
| A command answers `!trap` (package missing, permission), others fine | The rest is kept, `ScopePartial`, health not OK, the error is named in the health; nothing is withdrawn. |
| The connection drops or times out | Remaining commands are not sent; partial, as above. |
| No identity at all (no name, serial or MAC) | `Failed`. |

All time bounds are the target's timeout, put on the connection itself (the
library ignores the context while waiting for a reply).

## What is NOT collected

| Not collected | Why |
|---|---|
| IPv6 addresses (`/ipv6/address`) | Out of this first version. |
| SFP/QSFP transceiver data (DOM: rx/tx power, temperature, part numbers) | `/interface/ethernet/monitor` is an action, not a `print`; it is not in the read allowlist. |
| Switch-chip and hardware-offload tables (`/interface/ethernet/switch/...`) | Differ per chip generation; not modelled without a device. |
| Whether LLDP/MNDP discovery is enabled, interface lists | Not used by any trica attribute. |
| Remote chassis ID / remote `base_mac` of neighbours | RouterOS does not expose it (see above). |
| Wireless, firewall, NAT, routing protocols (BGP, OSPF), DHCP, queues, users | Not part of the inventory model of this collector. |
| Configuration backup, licence key, secrets | Never read; the account cannot. |

## Known limits of the client library

The collector uses the Go RouterOS API client library (module imported as `ros`
in `client.go`, MIT). Four of its behaviours are handled here; one is not:

- It ignores the context in synchronous mode: handled with a deadline on the
  connection and by closing it when the context ends.
- It prints sentences, passwords included, at debug level: a handler that is
  never enabled is installed.
- The documented bare `!fatal` word makes it answer "invalid RouterOS sentence
  word": treated as a connection error.
- **Not handled:** it trusts the length prefix of a word and allocates it before
  reading. A hostile or broken peer on the management network can make the
  collector allocate a lot of memory. The risk is bounded by the fact that the
  address comes from the operator's own configuration; it is a reason to prefer
  api-ssl with a verified certificate.

## Tests and fakes

`FakeMikroTik` (`fake.go`) is a real RouterOS-API server on a local socket
(plain or TLS) serving the fixtures in `testdata/`. It has its own codec
written independently of the library's, so the library is tested against it,
not against its twin. It can refuse a login, hang, drop the connection, answer
`!trap` or `!fatal` for a command, send a malformed reply or omit a command.
The password is masked in the requests it records.
