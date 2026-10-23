# unifi collector

Reads the **UniFi consoles of an account and the devices they manage** through the
**UniFi Site Manager API v1** (`https://api.ui.com`, <https://developer.ui.com/site-manager-api/>).
It is a cloud collector: it sees what Ubiquiti's cloud knows of a console, not the console's local
configuration. Read-only: the client has one method, `GET`, on four endpoints, with the API key in
the `X-API-KEY` header and nowhere else. Redirects are never followed, so the key cannot leave the
configured address ([ADR 0057](../../../docs/adr/0057-unifi-site-manager.md), ISP metrics:
[ADR 0059](../../../docs/adr/0059-unifi-isp-metrics.md)).

> A second mode, `options.api: network`, reads one console's own **Network Integration API** from the
> LAN: see [Network Integration API](#network-integration-api-optionsapi-network) below.

> Status: **recorded** on 2026-10-23 from one UniFi Dream Machine Pro Max console with ten adopted
> devices, anonymised (`testdata/recorded-2026-10/`), plus a **synthesised** set for what that
> account could not show (pagination, several consoles, a disconnected console, a camera, an update
> available). See `testdata/README.md` for what was recorded, what was anonymised and the list of
> things still to check against another account.

## Target

```yaml
- kind: unifi
  name: office                     # source "unifi:office"
  password: env:UNIFI_API_KEY      # the Site Manager API key; or file:/var/run/secrets/collect/unifi-key/key
  domain: office-a                 # LAN addresses repeat across sites: ADR 0036
  interval: 5m
  options:
    hosts: [office-a]              # optional: host name, console name or host id; default every console of the key
    roles: ["UAH-DOOR=access_control"]  # optional: shortname or model = device role
    isp_metrics: true              # optional, default true: latency, loss and uptime of the active Internet access
```

`address` is empty for `https://api.ui.com`; it may be another `https` base URL (a proxy in front of
the API). `username` is unused. `hosts`, `roles` and `isp_metrics` are optional; any other option is an error.
`roles` entries are `<shortname or model>=<role>`, where the role is a value of the schema's
`device.role` (an unknown value stops the start); the key ignores case, spaces and dashes
(`UA Hub Door`, `ua-hub-door` and `UAHDOOR` are the same).

### The key to create

1. Sign in to <https://unifi.ui.com>, open **API**, **Create API key**. Give it a name that says what
   it is for (`trica`).
2. Use an account with the **least role that sees the consoles**, a dedicated account if you can. A
   Site Manager key reads **every console of the account that created it**: restrict a target with
   `options.hosts`.
3. Put the key where `password` points (a Kubernetes Secret mounted as a file, or an environment
   variable) and nowhere else. The key is shown once; rotate it by creating a new one and revoking
   the old.

### Network

The server (or `trica collector`) needs egress to `api.ui.com:443`. `HTTPS_PROXY` and `NO_PROXY` are
honoured. A Kubernetes NetworkPolicy cannot name a DNS name: with `networkPolicy.egressTo` set, allow
port 443 to `0.0.0.0/0`, or go through a proxy you allow.

## What is collected

Requests of one run: `GET /v1/hosts` (the probe: a refused key or an unreachable API ends the run
there), `GET /v1/sites`, `GET /v1/devices` (with `hostIds[]` when `options.hosts` is set) and, unless `isp_metrics` is
`false` or `/v1/sites` failed, `GET /v1/isp-metrics/5m?beginTimestamp=<now-30m>&endTimestamp=<now>`
(one bounded call, never `/ea/`, never a `duration`). Lists are followed by `nextToken`, at most
50 pages.

| UniFi | trica |
|---|---|
| each device of `/v1/devices` (the console is the same device as its `/v1/hosts` entry, merged by MAC) | `device`, identity `base_mac` (strong) and `name` (weak). `vendor` `Ubiquiti`, `model` the display model (`U7 Pro`), `part_number` the shortname (`U7PRO`), `role`, `os_version` and a `firmware` record (`name`, `version`, and `update_available` when the cloud offers one), `health`, `uptime_s`, `mgmt_ip`, `description` (the note) |
| the console's `hardware.serialno` | `serial` (strong; UniFi's equals the MAC), `os_name` `UniFi OS`. When `/v1/devices` did not answer, the console is still emitted from its host entry, with no model or version, so those never flip between the two sources |
| `status` | `health`: `online` ok; `offline`, `disconnected` critical; `updating`, `adopting`, `pending`, `provisioning`, `gettingReady`, `restarting`, `connectionInterrupted`, `isolated` warning; anything else unknown |
| role | In order: `options.roles`; a console with a gateway, or a `UDM*`, `UCG*`, `UXG*`, `UDR*`, `USG*` model: `firewall`; `USP PDU*`, `USP Strip`, `USP Plug`: `pdu`; `UNVR*`: `nas`; product line `access`: `access_control`; `protect`: `camera`; `U6*`, `U7*`, `UAP*`, `AC *`, `nanoHD`, `Flex*`, `UAL*`: `access_point`; `USW*`, `US <n>*`, shortname `USPM*`, `USL*`: `switch`; otherwise `other` and a warning naming the model, to be fixed with `options.roles` |
| WAN ports of the console | `interface` in the console: `name` (`eth9`), `description` (`WAN`, `WAN2`), `kind` physical, `mac`, `admin_status` (enabled), `oper_status` (plugged), `speed_mbps` (from `speedType`: `10G SFP+` is 10000) |
| device addresses | `ip_address` `assigned_to` the device (`role` primary). The WAN side address of the console is assigned to its WAN port instead; other private addresses the console reports (LAN) to the console; link-local addresses are left out. The runner stamps the target's `domain` on all of them |
| each enabled WAN and the site statistics | `internet_access` named `<target>/<console hostname>/<WAN>`: `provider` (the ISP's name), `status` (port up: online) and `public_ips` (the address the cloud sees). The console `routes_via` it. The public address is an attribute and not an `ip_address` object, because the runner would stamp it with the office's domain. Not emitted when `/v1/sites` failed |
| ISP metrics of a site (`/v1/isp-metrics/5m`) | the **newest period** of the last 30 minutes, set as `latency_ms` (`avgLatency`), `latency_max_ms` (`maxLatency`), `loss_pct` (`packetLoss`) and `uptime_pct` (`uptime`) on the `internet_access` of the site's **primary WAN**: the one that is up with the lowest `failoverPriority` in `/v1/sites`. Not attributed, with a warning, when no WAN is up, when several are up outside `FAILOVER_ONLY` load balancing, when the period's `ispName` is not the active WAN's provider, or when the site is unknown. A value that is null or not a number is left out; one that is negative or above 100 (percentages) is left out with a warning. Throughput, downtime and the AS number are not read ([ADR 0059](../../../docs/adr/0059-unifi-isp-metrics.md)) |
| installed applications of the console | `software` `application/UniFi Network/10.6.106` (and Protect, Access...), the console `runs` it |
| a managed device | relation `managed_by` to its console (new relation, no impact: a device keeps forwarding when its controller is down) |

### Schema additions

Device roles `access_point`, `access_control` and `camera` (without them nine of the ten devices of
an office are `other`), the relation `managed_by` (device to device), and the series attributes
`latency_ms`, `latency_max_ms`, `loss_pct` and `uptime_pct` of `internet_access` (floats, `series`,
drawn as Trends by the object page: ADR 0045).

### How the metrics become a curve

A value is stored when the engine applies it, at its own clock, so a 5-minute period cannot be stored
at its own time. Each run sets the newest period as the current value and the attribute history does
the rest: two runs that see the same period write nothing, a newer period with another value writes
one row. The curve is therefore as fine as the run interval (5 minutes by default), lags by up to one
period, and has a gap when no period is available (the series is withdrawn by the full batch). Periods
between two runs are not backfilled.

### Device types

`vendor`, `model` and `part_number` are what the device-type library matches on
(`internal/devicetypes`): vendor `Ubiquiti`, the UniFi display model (`U7 Pro`, `USW Pro Max 24 PoE`)
and the shortname (`U7PRO`, `USPM24P`, `UAH-DOOR`) as the part number. The contract is that the
library resolves the five recorded pairs; the test that holds it, `TestUniFiRecordedPairsResolve`,
lives with the library's Ubiquiti models (the device-type story), not here, so that this collector
does not depend on it.

### Run outcomes

| Situation | Batch |
|---|---|
| All the calls answer, every console connected | scope `full`, health OK |
| The ISP metrics call fails (400, 403, 429, 5xx, malformed, timeout) | scope and health as if it had not been made, a warning `ISP metrics: ...` (with the `Retry-After`); no series for this run, so the curve has a gap. Never `partial`, so absence detection keeps working |
| A connected console has no period in the last 30 minutes | a warning, no series for it this run |
| The metrics cannot be attributed to one WAN (none up, load balancing, another provider) | a warning, no series for it this run |
| `options.isp_metrics: false` | no metrics call, no series, no warning |
| The key is refused (401), the API is unreachable, `/v1/hosts` fails or is malformed, the secret cannot be read, `options.hosts` matches no console | `Failed`: at most one request, no objects, nothing withdrawn |
| `/v1/sites` fails (403, 429, 5xx, malformed, timeout) | devices and the console kept, no Internet access; scope `partial`, health not OK |
| `/v1/devices` fails | consoles from their host entries; scope `partial`, health not OK |
| A console is disconnected from the cloud | its devices are emitted with no `health` or `uptime_s`; scope `partial`, health OK with a warning |
| A host is blocked, has no reported state or no MAC | skipped, with a warning |
| A host that is not a console (a self-hosted Network Server) | its devices are collected without `managed_by`, with a warning |
| Pagination loops, or runs past 50 pages | that call fails, as above |

## Personal data

The answers carry the account behind the key (e-mail, full name, role, permissions, ids) and the
console's location (coordinates, address text), plus the time zone, direct-connect domain and other
identifiers. None of it is decoded: the structs of `types.go` are an allow-list that does not
declare those fields (`TestDecodeStructsHaveNoPersonalFields`), and a recorded run is checked for
every sentinel of the fixtures (`TestPersonalDataNeverEmitted`). Nothing is logged by the collector;
the runner logs `source`, `collector` and `target` only. The API key never reaches a log, a health
record, a warning or a batch (`TestKeyNeverLeaks`; a server that echoes it in an error message has
it removed from the message).

## Version compatibility

Site Manager API v1. Recorded: one UDM Pro Max (UniFi OS 5.1.x; Network 10.6.106, Protect 7.3.70,
Access 4.3.22) with six U7 Pro, a USW Pro Max 24 PoE, a UA Hub Door and a UA G2 Pro; its ISP metrics
(5m, 1h) were recorded the same day, with a throughput that is 0 throughout. Everything else
is untested: Protect cameras (product line and model strings), update states, offline devices,
several consoles and pages are synthesised from the public documentation.

## Not collected, and why

- **Ports, uplinks, LLDP, VLANs, clients**: the cloud API does not give them, and the console proxy
  (`/v1/connector/...`) needs the account owner's key, also forwards writes, and would send the key to
  the console's own API. Ports and uplinks come from the console's own API (`options.api: network`,
  below); LLDP, VLANs and clients are not collected.
- **ISP throughput** (`download_kbps`, `upload_kbps`): 0 in every recorded period and of unverified
  meaning; trica does not keep throughput (ADR 0041, 0045) (#166).
- **The `1h` metrics, `/ea/isp-metrics` and the metrics' `POST .../query`**: the scan refuses them.
- **Backfill of the periods between two runs**: the engine stores a value at its own clock (ADR 0059).
- **Personal data and location**: see above.
- **Sites**: a UniFi site is a namespace inside a console, not a place. No `site` object is made and
  the UniFi site name is not stored; devices are not placed in a trica site (people declare places,
  and a device outside a rack cannot be placed in one: see the issue linked from ADR 0057).
- **Rate limits**: a run makes four calls (more with pages) every interval; a 429 ends that call and
  shows on the health board with its `Retry-After`. Keep the interval at 5 minutes or more.

## Network Integration API (`options.api: network`)

Reads **one console's own API** (`https://<console>/proxy/network/integration/v1`, UniFi Network
10.6.106 schema) from the LAN, with a key created on the console, for what the cloud does not give:
the ports of each device with their PoE, and the device each one is uplinked to
([ADR 0060](../../../docs/adr/0060-unifi-network-integration-api.md)). **Synthesised from the official
OpenAPI document, never run against a real console**; the 13 points to verify are in
`testdata/README.md`.

```yaml
- kind: unifi
  name: office-lan
  address: https://192.0.2.1          # required, https, no path
  password: file:/etc/trica/unifi-console-key
  domain: office-a                    # the SAME domain as the cloud target: addresses merge
  tls: {ca_file: /etc/trica/console-ca.pem}   # or insecure: true
  options:
    api: network
    sites: [default]                  # optional: name or internal reference
```

Run it with `trica collector` on the office LAN. `options.hosts` and `options.isp_metrics` are refused
with `api: network`, `options.sites` with the Site Manager.

Requests of one run: `GET /v1/info` (probe), `/v1/sites`, `/v1/sites/{id}/devices` per site (paged,
`limit=200`) and `/v1/sites/{id}/devices/{id}` per device. Nothing else, and never the clients.

| Console API | trica |
|---|---|
| each device | the same `device` as the cloud's, merged by `base_mac`. This source sets `name`, `vendor`, `part_number` (the API's `model`), `role` (options.roles; features gateway, accessPoint, switching; the cloud's model rules; an `UA` prefix that is not `UAP`/`UAL` is `access_control`; else unset and a warning), `os_version`, `health`, `mgmt_ip` (not on a gateway: the cloud reports its WAN side) and the address. It never sets `model`, the firmware record, `uptime_s`, `serial` or `managed_by`, so the two sources never make a value flip |
| each port | `interface` `Port <idx>` in the device: `oper_status`, `speed_mbps` (up only), `max_speed_mbps`, `connector` (`RJ45`, `SFP`, `SFP+`, `SFP28`, `QSFP28`), `poe_status` (`delivering`, `idle`, `limited`, `disabled`, `unknown`; none on a port without PoE) and `poe_standard` |
| a device's `uplink.deviceId` | relation `uplinked_to` device to device (impact to_from), across sites; an unknown id is a warning, a self-uplink ignored |

**No cable is made.** The API names the parent device of an uplink, not a port on either end, and
trica does not guess a port pairing (ADR 0010): there is no `neighbor_of` and `internal/deduce` finds
nothing to deduce. Port-level adjacency is a follow-up.

Runs: `/v1/info` or `/v1/sites` failing, a refused key, or `options.sites` matching nothing fails the
run (nothing withdrawn); a site's device list or a device's detail failing, or an id that is not a
UUID (never put in a path), makes the batch partial; a 401 or 429 stops the remaining calls. The
console's WAN ports appear twice (cloud `eth8`/`eth9`, console `Port 9`/`Port 10`): the API gives no
port role to match them. Radios, LAGs, networks, statistics and the clients (personal data) are not
read.
