# MAAS collector (`kind: maas`)

Inventories the machines managed by a [MAAS](https://maas.io) region controller
(Canonical's bare-metal provisioning service) through its REST API 2.0. It is
**read-only**: the client has no code path that sends anything but `GET`, and the
tests assert that the fake server only ever saw `GET`s.

> Status: validated against **synthesised** fixtures only (see `testdata/README.md`).
> Nothing here was run against a real MAAS. Real-device validation is pending.

## Target

```yaml
- kind: maas
  name: maas-a
  address: http://maas.example.net:5240   # the API is <address>/MAAS/api/2.0/
  password: file:/run/secrets/maas-key    # API key "consumer_key:token_key:token_secret"
  tls:
    ca_file: /etc/ssl/maas-ca.pem         # when address is https
  timeout: 60s
  options:
    assume_full_visibility: false         # see "Read-only access" below
```

| Field | Meaning |
|---|---|
| `address` | `http(s)://host[:port]` of the region controller, without `/MAAS`. |
| `password` | Secret reference to the API key (`env:NAME` or `file:path`). The `username` field is not used. |
| `options.assume_full_visibility` | Declares that a non-administrator key sees every machine (default `false`). Unknown options are rejected. |

Authentication is OAuth 1.0a with the PLAINTEXT signature method, the only one
MAAS uses: the API key is `consumer_key:token_key:token_secret` (the consumer
secret is empty). The signing is implemented in this package (no dependency), a
fresh random nonce is sent with each request (MAAS refuses a replayed nonce) and
the `Authorization` header is never logged. Redirects are not followed.

## What is collected

A run issues five `GET` requests and emits one batch with source `maas:<name>`.

| Request | Purpose |
|---|---|
| `users/?op=whoami` | First authenticated request (credential probe); tells whether the key is a superuser. |
| `version/` | Version reported in the collector health. A failure here is only a warning. |
| `machines/` | Machines, with their interfaces, addresses and block devices embedded. |
| `subnets/` | Prefixes. |
| `fabrics/` | VLANs. |

| MAAS data | trica object / attribute | Note |
|---|---|---|
| `system_id` | `device.provisioning_id` (strong identity) | MAAS's own identifier. |
| `hardware_info.system_serial` | `device.serial` (strong identity) | Placeholders are dropped, see below. |
| `hardware_uuid` | `device.hw_uuid` (strong identity) | Placeholders dropped. |
| `hostname` | `device.name` (identity) | |
| `status_name` | `device.provisioning_status` | `New`, `Ready`, `Deployed`... |
| `hardware_info.system_vendor` / `system_product` / `system_sku` | `device.vendor` / `model` / `part_number` | `system_sku` appears in recent releases only. |
| `hardware_info.mainboard_firmware_version` | `device.bios_version` | |
| `osystem` / `distro_series` | `device.os_name` / `os_version` | `os_version` is the MAAS series (`jammy`), not a version number. |
| `cpu_count` | `device.logical_cpus` (**new**) | MAAS counts logical CPUs; `device.cpu_count` is sockets, so it is not used. |
| `memory` (MiB) | `device.memory_bytes` | |
| `power_state` | `device.power_state` | `on`/`off`; anything else is `unknown`. |
| `owner`, `zone.name`, `pool.name`, `hwe_kernel`, `power_type`, `tag_names` | `device.provisioning_owner` / `provisioning_zone` / `provisioning_pool` / `provisioning_kernel` / `provisioning_power_type` / `provisioning_tags` (**new**) | |
| boot interface address, else first `ip_addresses` | `device.primary_ip`; `ip_address.dns_name` = `fqdn` | |
| `interface_set[]` | `interface` (child of the device) | `kind`: physical, bond to `lag`, vlan, bridge to `virtual`, other. `mac`, `mtu` (effective MTU), `admin_status` (`enabled`); `oper_status` and `speed_mbps` on physical ports only. |
| bond / bridge `parents` | `member_of` interface to bond or bridge | A VLAN interface is not a member of its parent. |
| interface `vlan` | `member_of` interface to `vlan`, `tagged` = VID is not 0 | |
| `links[].ip_address` | `ip_address` (strong `address`, `prefix_length`) and `assigned_to` ip to interface | Addresses on no known interface hang on the device. |
| `blockdevice_set[]` (physical) | `disk` (child of the device) | Identity `name` + `serial`; `model`, `capacity_bytes`, `firmware_version`, `media`, `protocol`. RAID/LVM/bcache layers are skipped, and so are the units an HPE Smart Array presents (see below). |
| `subnets/` | `prefix` (strong `prefix`) | `description` from the subnet name/description, `gateway`, `vlan` (tag, if not 0), `assigned_to` prefix to VLAN. |
| `fabrics/` vlans | `vlan` (strong `key` = `<fabric>:<vid>`) | `vid`, `name`, `domain` = fabric name, `mtu` (**new**), `dhcp_on` (**new**). VID 0 (untagged) is kept: it is a real VLAN of the fabric. |

### Placeholder values

Firmware and MAAS report `Unknown`, `Not Specified`, `To be filled by O.E.M.`,
`0123456789`, the AMI default UUID `03000200-0400-0500-0006-000700080009`,
all-zero and all-`F` strings, and similar. These never become an identifier or an
attribute (two unrelated machines would merge on `Unknown`). A machine without a
usable serial and UUID is identified by `provisioning_id` alone, which MAAS
guarantees unique.

### Scope and failures

| Situation | Result |
|---|---|
| Everything answered, key is a superuser (or `assume_full_visibility`) | `ScopeFull`, health OK. |
| Key is not a superuser | `ScopePartial`, health OK with a warning: the batch cannot withdraw machines it was not allowed to see. |
| `machines/`, `subnets/`, `fabrics/` or `whoami` fails (HTTP error, malformed JSON) | What was gathered is kept, `ScopePartial`, health not OK with the error. |
| Authentication refused (401), connection refused, DNS, TLS, timeout | A single failed health report, no objects, `ScopePartial`; the run stops at the first request. |
| Missing or malformed API key, bad TLS settings | Same failed report; no request is sent. |
| `version/` fails | Warning only, no version in the health. |

## Version matrix

| MAAS | Status |
|---|---|
| 3.5 | Modelled on documentation, **synthesised fixtures** (`testdata/v3.5`), untested on a real server. |
| 3.3 | Modelled on documentation and recollection of release differences, **synthesised fixtures** (`testdata/v3.3`), untested on a real server. |
| 3.4, 3.6 and later | Not modelled, untested. The decoder reads only the fields listed above and ignores the rest, so it should tolerate additions. |
| 2.x | Not modelled, untested. |

## Read-only access

**The collector only issues `GET`s.** The question is what the MAAS account it
uses can do.

* MAAS 3.x community edition has **no read-only API role**: a MAAS user is
  either a regular user or an administrator (superuser). Confidence: high for
  3.5 and earlier; **lower for 3.6 and later**, where role/permission work may
  exist (Canonical RBAC, below). Check your release's documentation.
* A **regular (non-admin) user** can read subnets, fabrics, zones and pools, but
  `machines/` only returns the machines that are **unallocated or owned by that
  user**. Machines deployed or allocated by anybody else are **invisible**: the
  API does not say they exist.
* An **administrator** sees every machine, but its API key can write (allocate,
  deploy, release, delete...). The collector never does, but the key could.

The collector deals with the visibility gap instead of hiding it: it calls
`users/?op=whoami`, and when the key is not a superuser it downgrades the batch
to partial scope (with a warning), so that trica never reports the invisible
machines as gone. Set `options.assume_full_visibility: true` only if you know the
key sees everything (see the RBAC case below).

Choose, as the operator:

| Option | Machines collected | Risk |
|---|---|---|
| Dedicated **non-admin** user (least privilege) | Unallocated machines and the ones that user owns. Partial scope: never withdraws anything. | Incomplete inventory. |
| Dedicated **admin** user (complete inventory) | All. Full scope. | The key has write power on MAAS even though trica does not use it: protect it (secret file or environment, restricted permissions, rotate it). |
| Canonical RBAC (external Candid + RBAC service, enterprise) with a "view all" permission on every resource pool | All, with a non-admin key. Set `assume_full_visibility: true`. | Not verified by this project. |

Recommendation: for a complete inventory use a dedicated admin user and treat its
key as a privileged secret; if the MAAS owner will not accept that, use a
non-admin user and accept the partial view. Creating the user (commands to verify
on your release):

```sh
# administrator (complete view)
sudo maas createadmin --username trica --password '<password>' --email trica@example.net

# non-administrator (restricted view), through an authenticated CLI profile
maas $PROFILE users create username=trica password='<password>' email=trica@example.net is_superuser=0

# print the user's API key (consumer_key:token_key:token_secret), store it as a secret
sudo maas apikey --username trica
```

The key can also be created and revoked from the MAAS UI (user preferences, API
keys).

## What is not collected

| Item | Why |
|---|---|
| LLDP neighbours, MAC address tables | MAAS exposes LLDP only through `machines/{id}/?op=details`, as BSON-wrapped XML, one request per machine. Not modelled; no MAC table exists in MAAS. |
| `zones/`, `resourcepools/` as objects | Machines already embed the zone and pool names (collected as attributes); the schema has no zone or pool type to hold empty ones, and two more requests only add failure modes. |
| Events and audit log | One request per machine (N+1), needs view permission, no place in the schema. |
| `power_parameters` | Contain BMC credentials; readable by administrators only. Never requested. |
| Virtual block devices (RAID, LVM, bcache), partitions, filesystems, storage layout | Layers over the physical drives, which are what is inventoried. |
| A block device whose serial is a 16-digit locally assigned NAA name with HPE's OUI (`3001438…`) | A unit an HPE Smart Array presents (a logical drive, or a drive in HBA mode): the operating system reads the identifier the controller gave the unit, not the drive's serial, so nothing could match it to the drive the BMC reports and each drive was stored twice ([#128](https://github.com/VargaFoundation/trica/issues/128)). The drive is the Redfish collector's to report; a server behind such a controller that no Redfish target covers has no disks in the inventory. |
| MAAS machines that are virtual (LXD, virsh VMs) are not told apart from bare metal | They appear as `device` with `role: server`; `provisioning_power_type` (`lxd`, `virsh`) tells them apart. |
| DHCP ranges, DNS resources, static routes, spaces | No schema type; out of this collector's scope. |
| Mainboard and chassis serial numbers | Not used as identity: they are often placeholders and differ from the system serial. |
| Devices (`devices/`), controllers (`rackcontrollers/`, `regioncontrollers/`) | Not machines; not modelled yet. |
