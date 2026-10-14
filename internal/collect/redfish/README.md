# redfish collector

Reads the hardware inventory and health of a server from its BMC (HPE iLO,
Dell iDRAC) over the Redfish REST API. Kind: `redfish`. One target = one BMC =
one server.

The collector is read-only. It sends `GET` requests and nothing else, with
HTTP Basic credentials on every request. **It does not create a Redfish session**
(`POST /redfish/v1/SessionService/Sessions` is a write) and never calls an
action (`/Actions/...`). The test fake answers any other method with
`405 Method Not Allowed` and records it, and the tests fail if one was seen.

> **Test on ONE canary host first.** The fixtures behind the tests are
> synthesised from the Redfish schemas and the vendors' documentation, not
> captured from equipment (see `testdata/README.md`). Firmware in the field
> differs in ways no document predicts. Point a single target at one server
> you can afford to look at, run it once, compare what trica shows with the
> iLO/iDRAC web page, and only then add the fleet. A BMC is a small embedded
> server; do not point a hundred collectors at a rack before you know what one
> costs.

## Configuration

```yaml
targets:
  - kind: redfish
    name: srv-01-bmc                    # appears in the source: redfish:srv-01-bmc
    address: 192.0.2.11                 # host, host:port or https://host:port
    username: trica-ro
    password: file:/run/secrets/srv-01-bmc   # or env:NAME
    interval: 15m
    timeout: 60s                        # iLO 4 can be slow, see below
    tls:
      insecure: true                    # BMCs ship self-signed certificates
      # ca_file: /etc/trica/bmc-ca.pem  # preferred when you have one
      # min_version: "1.0"              # old iLO 4 firmware only offers TLS 1.0/1.1
    options:
      log_days: 30                      # look-back window of the event log counters
      skip_logs: false                  # true: do not read the event log at all
```

| Option | Default | Meaning |
|--------|---------|---------|
| `log_days` | 30 | How many days back the critical/warning counters look. Must be positive. |
| `skip_logs` | false | Do not read the IML/SEL (an old iLO 4 can take a long time to page through a full IML). |

Unknown option keys are rejected (a typo must not silently do nothing).

Cost: one run is **sequential** (never parallel) and takes about 30-40 GETs on
the modelled fixtures. On a real server it grows with the number of DIMMs, drives and
firmware components (each is its own GET) and with the event log: expect
60-150 requests. An interval of 15 minutes or more is a sensible start; the
inventory does not change faster than that. The whole run is bounded by the
target `timeout`; when it expires, what was read so far is kept and the run is
reported as partial.

## What is collected

| Equipment data | Redfish source | trica |
|----------------|----------------|-------|
| Serial number | `Systems/<id>`.SerialNumber | `device.serial` (identity, strong) |
| System UUID | `Systems/<id>`.UUID | `device.hw_uuid` (identity, strong) |
| Host name | `Systems/<id>`.HostName | `device.name` (identity, weak) |
| Role | fixed | `device.role` = `server` |
| Vendor, model | `Manufacturer`, `Model` | `device.vendor`, `device.model` |
| Part number | `SKU`, **HPE/HP only** (Dell's SKU is the service tag) | `device.part_number` |
| BIOS version | `BiosVersion`, else HPE OEM `Bios.Current.VersionString` | `device.bios_version` |
| Power state | `PowerState` | `device.power_state` |
| Health | `Status.HealthRollup` (else `Health`) | `device.health` |
| Location LED | `IndicatorLED` | `device.location_led` |
| Memory total | `MemorySummary.TotalSystemMemoryGiB`/`GB` (else sum of the DIMMs) | `device.memory_bytes` |
| CPU count | `ProcessorSummary.Count` (else number of processors) | `device.cpu_count` |
| Power draw | `Chassis/Power`.PowerControl[0].PowerConsumedWatts (else `PowerMetrics.AverageConsumedWatts`) | `device.power_w` |
| Inlet / exhaust temperature | `Chassis/Thermal`, by `PhysicalContext` then by sensor name | `device.inlet_temp_c`, `device.outlet_temp_c` |
| BMC firmware | `Managers/<id>`.FirmwareVersion | `device.bmc_version` |
| BMC address | first usable IPv4 of the manager's interfaces | `device.bmc_ip` and `device.mgmt_ip` (identity) |
| Firmware inventory | HPE `FirmwareInventory` / `UpdateService/FirmwareInventory` | `device.firmware_inventory` (list of `Name=Version`) |
| Event log counters | IML (iLO) and SEL (iDRAC) | `device.log_window_days`, `log_critical_count`, `log_warning_count`, `log_last_critical`, `log_last_critical_at` |
| Processors | `Systems/<id>/Processors` | `cpu` (model, cores, threads, health), in the device |
| Memory modules | `Systems/<id>/Memory` (HPE iLO 4: `Oem.Hp.Links.Memory`) | `memory` (name, serial, part number as `model`, `size_bytes`, `speed_mhz`, health); empty slots are skipped |
| Storage controllers | `Systems/<id>/Storage` (iLO 4: `Oem.Hp.Links.SmartStorage` ArrayControllers) | `module` of kind `storage_controller` (model, firmware, serial, health) |
| Disks | `Drives` (iLO 4: SmartStorage PhysicalDrives) | `disk` in its controller: serial and `wwn` (strong), model, media, protocol, capacity, firmware, slot, health, `wear_pct` |
| SSD wear | 100 - `PredictedMediaLifeLeftPercent`, else HPE `SSDEnduranceUtilizationPercentage` | `disk.wear_pct` |
| Power supplies | `Chassis/Power`.PowerSupplies | `psu` (model, serial, capacity, input watts, firmware, health) |
| Fans | `Chassis/Thermal`.Fans | `fan` (speed in rpm or percent depending on the unit the BMC reports, health); a missing fan is kept with health `absent` |
| Temperature sensors | `Chassis/Thermal`.Temperatures | `sensor` (placement, reading, thresholds, health) |
| Host network interfaces | `Systems/<id>/EthernetInterfaces` | `interface` kind `physical` (name, MAC, link status, speed, duplex) |
| BMC network interfaces | `Managers/<id>/EthernetInterfaces` | `interface` kind `bmc` named `bmc` (the one carrying the address; `bmc-<id>` for the others) |
| BMC addresses | same | `ip_address` with role `bmc`, prefix length, related to its interface by `assigned_to` |

Identity rules:

- Device: serial and UUID, both strong; the BMC address is also an identity key
  (`mgmt_ip`). A server whose BMC shows none of serial, UUID, host name or
  address is reported as a failed run rather than guessed.
- Parts (DIMM, disk, PSU, controller): serial when the BMC gives one (iLO 4
  does not give DIMM serials), always scoped by the device (or its controller)
  and named by slot. Values are trimmed, nothing else is normalised.
- Two parts with the same name (iLO names every supply `HpServerPowerSupply`)
  get their Redfish member id appended, so they cannot merge.

Event logs: no object is created per entry. For the entries newer than
`log_days`, the collector counts those with severity Critical and Warning and
keeps the message and date of the most recent Critical (truncated to 500
characters). Only the hardware logs are read: the **IML** on iLO and the
**SEL** on iDRAC. The iLO event log (IEL) and the iDRAC Lifecycle log describe
the controller's own activity (logins, configuration), are large, and are never
requested. If a log cannot be read to its end, its counters are dropped from
that run rather than reported low.

## Failure behaviour

| Situation | Result |
|-----------|--------|
| Credentials refused, BMC unreachable, TLS or timeout error on the service root, system missing or unreadable, nothing identifies the server | Failed run: partial scope (nothing is withdrawn), health `ok: false` with the error. No object. |
| One section fails (a sub-resource answers 5xx, malformed JSON, ...) | The rest is kept, scope is **partial**, health `ok: false` and names what failed. A log whose pages could not all be read reports no counters. |
| Something unusual but harmless (log entries without a readable date) | Run succeeds, health lists a warning. |

Passwords are never written to errors, logs or batches. The service root
(`/redfish/v1/`) is served without authentication by BMCs, so a wrong password
shows up as an HTTP 401 on the `Systems` collection: the error says
`HTTP 401` with the BMC's own message.

## Read-only account (what to create on the BMC)

The collector needs to read, never to change. Create a dedicated account for
it and give it the smallest privilege the BMC offers. trica never runs any of
the commands below: they are for the operator, with an administrator account
of their own, **once per BMC, by hand or with their own tooling**.

### HPE iLO 4 and iLO 5: Login privilege only

Privileges to leave **off**: Remote Console, Virtual Media, Virtual Power and
Reset, Configure iLO Settings, Administer User Accounts (and on iLO 5: Host
BIOS, Host NIC, Host Storage, Recovery Set). Only **Login** is on.

Using `hponcfg` on a host (or `ssh` to the iLO CLI with RIBCL), save as
`add-trica-ro.xml`, put the administrator's credentials in `LOGIN`, and run
`hponcfg -f add-trica-ro.xml`:

```xml
<RIBCL VERSION="2.0">
  <LOGIN USER_LOGIN="admin-account" PASSWORD="admin-password">
    <USER_INFO MODE="write">
      <ADD_USER USER_NAME="trica-ro" USER_LOGIN="trica-ro" PASSWORD="choose-a-long-random-password">
        <ADMIN_PRIV value="N"/>
        <REMOTE_CONS_PRIV value="N"/>
        <RESET_SERVER_PRIV value="N"/>
        <VIRTUAL_MEDIA_PRIV value="N"/>
        <CONFIG_ILO_PRIV value="N"/>
        <LOGIN_PRIV value="Y"/>
      </ADD_USER>
    </USER_INFO>
  </LOGIN>
</RIBCL>
```

On iLO 5, add `<HOST_BIOS_CONFIG_PRIV value="N"/>`,
`<HOST_NIC_CONFIG_PRIV value="N"/>`, `<HOST_STORAGE_CONFIG_PRIV value="N"/>` and
`<RECOVERY_SET_PRIV value="N"/>` inside `ADD_USER`.

Or with the iLO 5 Redfish API, as the administrator (this is **not** something
trica sends):

```http
POST /redfish/v1/AccountService/Accounts/
Content-Type: application/json

{
  "UserName": "trica-ro",
  "Password": "choose-a-long-random-password",
  "Oem": { "Hpe": {
    "LoginName": "trica-ro",
    "Privileges": {
      "LoginPriv": true,
      "RemoteConsolePriv": false,
      "UserConfigPriv": false,
      "VirtualMediaPriv": false,
      "VirtualPowerAndResetPriv": false,
      "iLOConfigPriv": false,
      "HostBIOSConfigPriv": false,
      "HostNICConfigPriv": false,
      "HostStorageConfigPriv": false,
      "SystemRecoveryConfigPriv": false
    }
  } }
}
```

(On iLO 4 the same body goes to `/rest/v1/AccountService/Accounts` with `Hp`
instead of `Hpe` and without the Host* and Recovery privileges.)

### Dell iDRAC 8: role "Read Only"

Privilege `0x1` (Login only) is the "Read Only" role. As an administrator,
with `racadm` (remote: `racadm -r <idrac> -u <admin> -p <password> ...`), on a
free user slot (3 here; check with `racadm get iDRAC.Users.3`):

```sh
racadm set iDRAC.Users.3.UserName trica-ro
racadm set iDRAC.Users.3.Password 'choose-a-long-random-password'
racadm set iDRAC.Users.3.Privilege 0x1
racadm set iDRAC.Users.3.IpmiLanPrivilege 15
racadm set iDRAC.Users.3.IpmiSerialPrivilege 15
racadm set iDRAC.Users.3.SolEnable Disabled
racadm set iDRAC.Users.3.Enable Enabled
racadm get iDRAC.Users.3        # check: Privilege=0x1
```

### What to verify on the canary

- The account can read every resource listed above, including the event log
  (HPE: IML needs no more than Login; confirm on your firmware).
- The account cannot do anything else: try a `PATCH` or an action with it and
  check that the BMC refuses.
- iLO accounts are often subject to a lock-out after failed logins: a wrong
  password in the target configuration locks the account for every client
  using it, including this collector.

## Compatibility

| Equipment | Firmware modelled | Status |
|-----------|-------------------|--------|
| HPE ProLiant DL360 Gen9, iLO 4 | 2.7x (Redfish 1.0, `Oem.Hp`, SmartStorage, FirmwareInventory under the system) | Fixture only, **untested on real equipment** |
| HPE ProLiant DL380 Gen10, iLO 5 | 2.x (`Oem.Hpe`, standard `Storage`/`Memory`, `UpdateService/FirmwareInventory`) | Fixture only, **untested on real equipment** |
| Dell PowerEdge R330, iDRAC 8 | 2.8x (standard `Storage`/`Drives`, `UpdateService/FirmwareInventory`) | Fixture only, **untested on real equipment** |
| iLO 6, iDRAC 9, other vendors (Supermicro, Lenovo XCC, Cisco CIMC, ...) | not modelled | Untested. The collector follows standard links and falls back to nothing vendor-specific, so a standard-compliant BMC probably yields a partial inventory, but nothing is promised. |

## Not collected

| What | Why |
|------|-----|
| Blades, multi-node chassis, more than one system per BMC | Only the first member of `Systems` and of `Managers` is read. A chassis manager is a different collector. |
| iLO event log (IEL), iDRAC Lifecycle log, individual log entries | Describe the controller, are large, and the inventory keeps state, not events (see above). |
| DIMM error counters, drive temperature and power-on hours, SMART data | Not in the first version; not needed by an inventory. |
| Storage volumes / logical drives, RAID levels | The schema has no volume type yet. |
| PCIe devices, GPUs, NVMe namespaces, USB | Not modelled yet. |
| Firmware per component as objects | Kept as one list attribute (`firmware_inventory`), not as objects. |
| IPv6 addresses, DNS/NTP settings, BMC users and certificates | Not inventory data; user and certificate listings need privileges beyond Login. |
| Sessions, power actions, anything with a side effect | The collector never writes. |
| `Oem` data other than iLO's SmartStorage, memory, firmware and SSD endurance | Vendor-specific, out of scope for the first version. |
