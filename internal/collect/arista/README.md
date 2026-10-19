# Arista collector (`arista`)

Reads an Arista switch through **eAPI** (JSON-RPC 2.0 over HTTP, `POST
/command-api`, method `runCmds`, HTTP Basic authentication) and emits one
batch per run. It sends **`show` commands only**; see "Read-only guarantee".

**Status: validated against synthesised fixtures only.** Nothing here was
recorded from a real switch; see `testdata/README.md` for what must be checked
on one.

## Target configuration

```yaml
targets:
  - kind: arista
    name: sw-a1
    address: http://192.0.2.11        # or a bare host / https://host[:port]
    username: trica
    password: file:/run/secrets/arista-password   # or env:ARISTA_PASSWORD
    timeout: 30s
    interval: 5m
    options:
      max_edge_macs: 4
      vlan_domain: default
```

| Field | Meaning |
|---|---|
| `address` | A bare host (`192.0.2.11`, `sw-a1:8443`) uses **https**. `http://host` is honoured for old EOS releases that cannot negotiate modern TLS; the Basic credentials then travel in clear text, so use it on a management network only. A path replaces `/command-api`. |
| `username`, `password` | The read-only account below. The password is a reference (`file:` or `env:`), resolved at each run and never logged. |
| `tls.insecure`, `tls.ca_file`, `tls.min_version` | The common TLS settings of `internal/collect`. Switches ship self-signed certificates: set `ca_file`, or `insecure: true` knowingly. |
| `timeout` | Bounds the whole run, all requests included. |
| `options.max_edge_macs` | `learned_macs` is reported only on ports with at most this many entries in the MAC table (default **4**); a trunk or an uplink learns the whole network and says nothing about what is plugged in. `0` turns `learned_macs` off. Negative is an error. |
| `options.vlan_domain` | The layer-2 domain the switch's VLANs belong to (default `default`). VLAN 100 of two switches in the same domain is one VLAN object (`key` = `<domain>:<vid>`); give each independent network its own domain. |

An unknown key under `options` is a configuration error, not ignored.

## Account to create on the switch

The collector needs no more than the `network-operator` role, which EOS limits
to `show` commands:

```
username trica privilege 1 role network-operator secret <password>
```

`management api http-commands` must be enabled (`no shutdown`, and `protocol
http` or `protocol https` to match `address`).

EOS enforces a role's command rules only when command authorization is on:

```
aaa authorization commands all default local
```

Without it the role is accepted but not applied, and what the account may run is
decided by its privilege level alone. Turn it on in a session that keeps an
administrator logged in, and check that the administrator accounts still have a
role that permits configuration (`network-admin`) before closing it. Then check
the collector's account: `show version` must answer, `configure` must be
refused. Nothing else on the switch needs to change. Do not give the account `network-admin`, and do not enable
`configure` access for it: the collector never needs it.

eAPI takes commands only by `POST`, reads included. That is protocol, not a
write: the commands in the request are what is read-only.

## Read-only guarantee

* The client refuses to send any command that does not start with `show `,
  before the request leaves the process (`client.run`); a test proves it.
* With the account above, the switch refuses everything but `show` as well.
* The fake (`FakeArista`) refuses and **records** a non-`show` command and any
  request other than `POST /command-api`; the tests assert that
  `NonReadOperations()` is empty after a run.

Commands sent (17), in three requests; see `Commands()`:

1. `show version`, `show hostname`, `show inventory`, `show uptime`
2. `show interfaces`, `show interfaces status`, `show ip interface`,
   `show vlan`, `show port-channel`, `show mlag`, `show lldp local-info`,
   `show lldp neighbors detail`, `show mac address-table`
3. `show environment temperature`, `show environment cooling`,
   `show environment power`, `show interfaces transceiver`

`show uptime` is not in the original brief; it is added to read the uptime
in seconds directly rather than deriving it from a boot time and the clock.

When eAPI refuses a request because of one command (unknown on this EOS train,
not allowed by the role), the group is replayed **one command per request**, so
the refused command costs only its own data.

## What is collected

| Equipment data | Source command | trica |
|---|---|---|
| Serial, system MAC, host name, management IP | `show version`, `show hostname`, `show interfaces`, `show lldp local-info` | `device` identity: `serial`, `base_mac` (system MAC), `name`, `mgmt_ip` |
| Model, vendor, EOS version, RAM | `show version` | `device.model`, `vendor`, `os_name` (`EOS`), `os_version`, `memory_bytes` |
| Role | fixed | `device.role` = `switch` |
| Uptime | `show uptime` | `device.uptime_s` |
| Overall health | `systemStatus` of the environment commands | `device.health` (worst of temperature and cooling) |
| Power drawn | `show environment power` | `device.power_w`: the PSUs' `inputPower` when every PSU that reports its output reports it too, `device.power_basis: input`; else the sum of their `outputPower`, `power_basis: output` (lower than the draw by the supplies' losses) |
| Inlet / outlet temperature | `show environment temperature` | `device.inlet_temp_c`, `outlet_temp_c` (only when a sensor says inlet / exhaust) |
| MLAG | `show mlag` | `device.mlag_state`, `mlag_domain`, `mlag_peer` (**new attributes**) |
| Physical, Management, Port-Channel, Vlan (SVI), Loopback interfaces | `show interfaces`, `show interfaces status` | `interface` (child of the device): `kind`, `mac`, `description`, `admin_status`, `oper_status`, `mtu`, `duplex`, `speed_mbps` (link up only), `mode`, `native_vlan` |
| MAC addresses seen on edge ports | `show mac address-table` | `interface.learned_macs` (`"<vlan>/<mac>"`, sorted, dynamic entries, at most `max_edge_macs` per port) |
| IP addresses | `show ip interface` (with `show interfaces`) | `ip_address` (`role` primary / secondary / vip, `prefix_length`) and `assigned_to` its interface |
| VLANs | `show vlan` | `vlan` (`key`, `vid`, `name`, `domain`) |
| VLAN membership | `show vlan`, `show interfaces status` | `member_of` interface to vlan, `tagged` (access: false; trunk: true unless it is the native VLAN, 1 when unreported); an SVI is a member of its VLAN |
| LAG membership | `show port-channel` | `member_of` member interface to Port-Channel (active and inactive ports; the MLAG peer's ports are skipped) |
| LLDP neighbours | `show lldp neighbors detail` | `neighbor_of` from the local interface to `interface{name}` under `device{base_mac, name}`, `protocol=lldp` |
| Power supplies | `show inventory`, `show environment power` | `psu` (child of the device): `serial`, `model`, `capacity_w`, `input_w`, `health`, `airflow` (from the part number: `-F` / `-RED` front to rear, `-R` / `-BLUE` rear to front; ADR 0049) |
| Fans | `show environment cooling`, `show inventory` | `fan` (child of the device, or of its PSU): `speed_pct`, `health`; a tray with no fan list is one fan; a tray's fans get `airflow` from the tray's part number (`FAN-7000-F` front to rear, `-R` rear to front). A PSU's own fan gets none: the PSU says it |
| Chassis airflow | `show environment cooling` (`airflowDirection`, on the releases that report it) | `device.airflow`: `frontToBackAirflow` is `front_to_rear`, `backToFrontAirflow` `rear_to_front` (front = the port side); another spelling is left out with a warning. The airflow check (ADR 0049) compares it with the model's |
| Temperature sensors | `show environment temperature` | `sensor` (child of the device or PSU): `placement`, `reading`, `unit`, `warning_threshold`, `critical_threshold`, `health` |
| Transceivers | `show inventory`, `show interfaces transceiver` | `transceiver` (child of its interface): `serial`, `vendor`, `model`, `media_type`, `temperature_c`, `voltage_v`, `tx_power_dbm`, `rx_power_dbm` |

The values are passed on as the switch reports them, trimmed; the schema
normalises MAC addresses and IP addresses. A reading that is `"N/A"` or `-inf`
(a copper cable has no optical power) is left out, never turned into zero.

### Outcomes of a run

| Situation | Batch |
|---|---|
| Everything read | `scope: full`, health OK |
| Some commands failed or unreadable | `scope: partial` (nothing is closed because of data that was not read), health **not OK**, the error names each failed command |
| Switch unreachable, credentials refused, `show version` unreadable, password not resolvable, timeout | no data, `scope: partial`, health not OK with the error |

## Version compatibility

| EOS | Hardware modelled | Status |
|---|---|---|
| 4.14.2F | DCS-7050SX-64 | Modelled on a synthesised fixture; **not tested on a real device** |
| 4.20.7M | DCS-7280SE-64 | Modelled on a synthesised fixture; **not tested on a real device** |
| Other 4.x | none | Untested. The decoders are tolerant (numbers as strings, maps as arrays, absent keys), but nothing proves a given release |

## What is not collected, and why

| Not collected | Why |
|---|---|
| Fan serial numbers and models | The schema has no field for them and eAPI does not give a fan serial. |
| Modular chassis (line cards, supervisors) | Only fixed switches are modelled; card sensors are kept under the device, prefixed `CardN`. |
| Airflow direction | No schema attribute; the 4.20 fixture shows the key but nothing maps it. |
| `lldp_enabled` per interface | Needs `show lldp` configuration output not requested here. |
| VRFs, routes, BGP, ARP | Outside the inventory scope of this story. |
| Per-lane DOM, transceiver bias current | No schema attribute. |
| PSU feed (A/B) | eAPI does not say which feed a PSU is plugged on. |
| MLAG port detail | Only the domain, the state and the peer address are kept. |
| Static and configured MAC entries | They say nothing about what is plugged in. |
| Front-panel / rear-panel sensor placement | Not an airflow statement; mapped to `other`. |

## Tests

`go test -race -cover ./internal/collect/arista/`. The tests run the collector
against `FakeArista` (`fake.go`), which serves `testdata/eos-<version>/` and
checks credentials, records every request, and can fail like a switch: refused
login, one refused command, an unreadable result, a malformed body, a slow
answer, an HTTP error, a self-signed TLS certificate. Every emitted batch is
validated against the built-in schema (`collecttest.Run`), which fails on an
attribute the schema would drop.
