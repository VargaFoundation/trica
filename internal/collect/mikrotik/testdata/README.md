# Fixtures of the mikrotik collector

**These fixtures are synthesised. They were not recorded from a real device.**
No MikroTik equipment was available when the collector was written. Every row
was written by hand (with a throw-away generator that is not committed) from
the MikroTik documentation and from what is publicly known about the shape of
the API replies. They prove that the collector is consistent with the model
below, not that it works on real RouterOS. Treat the first run on a real device
as the real test (see "To verify on a real device").

The reply of each `.../print` command is a JSON object: command path to a list of
rows, each row a map of attribute name to string value, exactly what the API
puts in the `!re` sentences (the API only carries strings). `fake.go` serves
them over a real RouterOS-API socket.

## What each file models

| File | Device | Software | Modelled on |
|---|---|---|---|
| `crs354-ros7.8.json` | CRS354-48G-4S+2Q+ (48 x 1G, 4 x SFP+, 2 x QSFP+), a bridge with VLAN filtering, a bond, a management VLAN interface | RouterOS 7.8 (7.x family: per-sensor `/system/health`, `discovered-by` on neighbours) | MikroTik manual pages for the API, `/interface/bridge`, `/interface/bridge/vlan`, `/interface/bridge/host`, `/ip/neighbor`, `/system/health`, `/system/routerboard`, `/system/package` |
| `crs326-ros6.49.json` | CRS326-24G-2S+ (24 x 1G, 2 x SFP+), a bridge, a bond, a management VLAN interface | RouterOS 6.49.10 (long-term 6.x: flat `/system/health` row, voltage in dV, no `discovered-by`) | The same pages for 6.x, and the 6.x to 7.x differences as documented and discussed on the MikroTik forum |

All other versions are untested. The collector reads attributes by name and
ignores the ones it does not know, so minor releases are expected to work, but
that is an expectation, not a measurement.

## Anonymisation

- Serial numbers are invented (`FAKE000001`, `FAKE000002`).
- MAC addresses are locally administered (`02:xx:...`), so they cannot belong to
  a real vendor block.
- IP addresses are in the documentation ranges `192.0.2.0/24`, `198.51.100.0/24`
  and `203.0.113.0/24`.
- Names are generic (`sw-a1`, `core-1.example.net`). No password, key, SNMP community
  or user list is in any fixture, and the collector never asks for one.

## To verify on a real device

Each point is a guess the fixtures encode. A real device that disagrees is a
bug in the fixture, and probably in the mapper.

1. **Health sensor names** on CRS3xx (`temperature`, `board-temperature1`,
   `cpu-temperature`, `fan1-speed`, `psu1-state`, `fan1-state`, `power-consumption`,
   `voltage`) and the `psuN-state` / `fanN-state` values (`ok` is assumed).
2. **6.x flat health**: voltage in decivolts (`240` = 24.0 V; the mapper divides
   the voltage by 10 on the flat form only). The 6.49 fixture has no `current`
   or `power-consumption`; whether a real CRS3xx on 6.x reports them, and in
   which unit, is unknown.
3. **7.x health rows**: one row per sensor with `name`, `value`, `type` (`C`, `V`,
   `RPM`, `W`...).
4. **`discovered-by`** on `/ip/neighbor` exists from 7.7 on. The 6.49 fixture does
   not have it and the mapper then leaves the `protocol` of the relation unset.
5. **`interface-name`** for LLDP neighbours, and the format of the `interface`
   list of a neighbour (comma-separated, local interface first, then the
   bridge it is seen through).
6. **`mac-address` of a neighbour** is the MAC of the packet source, not an LLDP
   chassis ID. The collector therefore never sets a `base_mac` on the remote device.
7. **Booleans**: `true`/`false` in the API replies (the old manual says `yes`/`no`;
   both are accepted).
8. **Speed format** of `/interface/ethernet/print`: the `speed` attribute is
   assumed to read `1Gbps`, `10Gbps` or `100Mbps`, and `full-duplex` to be a boolean.
9. **`vid` in `/interface/bridge/host/print`**: assumed present on 7.x and absent
   (then `0`) on bridges without VLAN filtering.
10. **Interface-list names** in `tagged` / `untagged` of `/interface/bridge/vlan`:
    the fixtures contain only interface names; an interface-list name there is
    not resolved.
11. **`actual-mtu`**: absent on 6.x, falling back to `mtu`.
12. **`!fatal`**: the API manual documents a bare word (`!fatal` followed by
    a free-text word). That is what the fake sends; confirm that a real device
    does the same.
13. **Ordering** of rows, and `dynamic`/`disabled`/`invalid` flags on rows such as
    bridge ports and packages.
