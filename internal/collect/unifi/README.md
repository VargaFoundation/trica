# unifi collector

Reads the **UniFi consoles of an account and the devices they manage** through the
**UniFi Site Manager API v1** (`https://api.ui.com`, <https://developer.ui.com/site-manager-api/>).
It is a cloud collector: it sees what Ubiquiti's cloud knows of a console, not the console's local
configuration. Read-only: the client has one method, `GET`, on three endpoints, with the API key in
the `X-API-KEY` header and nowhere else. Redirects are never followed, so the key cannot leave the
configured address ([ADR 0057](../../../docs/adr/0057-unifi-site-manager.md)).

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
```

`address` is empty for `https://api.ui.com`; it may be another `https` base URL (a proxy in front of
the API). `username` is unused. Both `hosts` and `roles` are optional; any other option is an error.
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
there), `GET /v1/sites`, `GET /v1/devices` (with `hostIds[]` when `options.hosts` is set). Lists are
followed by `nextToken`, at most 50 pages.

| UniFi | trica |
|---|---|
| each device of `/v1/devices` (the console is the same device as its `/v1/hosts` entry, merged by MAC) | `device`, identity `base_mac` (strong) and `name` (weak). `vendor` `Ubiquiti`, `model` the display model (`U7 Pro`), `part_number` the shortname (`U7PRO`), `role`, `os_version` and a `firmware` record (`name`, `version`, and `update_available` when the cloud offers one), `health`, `uptime_s`, `mgmt_ip`, `description` (the note) |
| the console's `hardware.serialno` | `serial` (strong; UniFi's equals the MAC), `os_name` `UniFi OS`. When `/v1/devices` did not answer, the console is still emitted from its host entry, with no model or version, so those never flip between the two sources |
| `status` | `health`: `online` ok; `offline`, `disconnected` critical; `updating`, `adopting`, `pending`, `provisioning`, `gettingReady`, `restarting`, `connectionInterrupted`, `isolated` warning; anything else unknown |
| role | In order: `options.roles`; a console with a gateway, or a `UDM*`, `UCG*`, `UXG*`, `UDR*`, `USG*` model: `firewall`; `USP PDU*`, `USP Strip`, `USP Plug`: `pdu`; `UNVR*`: `nas`; product line `access`: `access_control`; `protect`: `camera`; `U6*`, `U7*`, `UAP*`, `AC *`, `nanoHD`, `Flex*`, `UAL*`: `access_point`; `USW*`, `US <n>*`, shortname `USPM*`, `USL*`: `switch`; otherwise `other` and a warning naming the model, to be fixed with `options.roles` |
| WAN ports of the console | `interface` in the console: `name` (`eth9`), `description` (`WAN`, `WAN2`), `kind` physical, `mac`, `admin_status` (enabled), `oper_status` (plugged), `speed_mbps` (from `speedType`: `10G SFP+` is 10000) |
| device addresses | `ip_address` `assigned_to` the device (`role` primary). The WAN side address of the console is assigned to its WAN port instead; other private addresses the console reports (LAN) to the console; link-local addresses are left out. The runner stamps the target's `domain` on all of them |
| each enabled WAN and the site statistics | `internet_access` named `<target>/<console hostname>/<WAN>`: `provider` (the ISP's name), `status` (port up: online) and `public_ips` (the address the cloud sees). The console `routes_via` it. The public address is an attribute and not an `ip_address` object, because the runner would stamp it with the office's domain. Not emitted when `/v1/sites` failed |
| installed applications of the console | `software` `application/UniFi Network/10.6.106` (and Protect, Access...), the console `runs` it |
| a managed device | relation `managed_by` to its console (new relation, no impact: a device keeps forwarding when its controller is down) |

### Schema additions

Device roles `access_point`, `access_control` and `camera` (without them nine of the ten devices of
an office are `other`) and the relation `managed_by` (device to device).

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
| All three calls answer, every console connected | scope `full`, health OK |
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
Access 4.3.22) with six U7 Pro, a USW Pro Max 24 PoE, a UA Hub Door and a UA G2 Pro. Everything else
is untested: Protect cameras (product line and model strings), update states, offline devices,
several consoles and pages are synthesised from the public documentation.

## Not collected, and why

- **Ports, uplinks, LLDP, VLANs, clients**: the cloud API does not give them, and the console proxy
  (`/v1/connector/...`) needs the account owner's key and would send the key to the console's own
  API. The local **Network Integration API** of a console (`/proxy/network/integration/v1`, a key
  created on the console, reached from the LAN by `trica collector`) is the follow-up for ports and
  cabling.
- **ISP metrics** (`/ea/isp-metrics`): early access, large answers, per site and not per WAN; they
  need time series on `internet_access` (a schema change) and a bounded query.
- **Personal data and location**: see above.
- **Sites**: a UniFi site is a namespace inside a console, not a place. No `site` object is made and
  the UniFi site name is not stored; devices are not placed in a trica site (people declare places,
  and a device outside a rack cannot be placed in one: see the issue linked from ADR 0057).
- **Rate limits**: a run makes three calls (more with pages) every interval; a 429 ends that call and
  shows on the health board with its `Retry-After`. Keep the interval at 5 minutes or more.
