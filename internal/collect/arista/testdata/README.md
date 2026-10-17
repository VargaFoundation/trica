# Arista fixtures: synthesised values, measured availability

**The values in these files were written by hand from Arista's published
documentation and from the author's knowledge of the eAPI JSON. None of them
was recorded from a real switch.** They show what the collector does with the
documented shapes; they do not prove every value reads right on a real EOS
device (see "What must be checked on a real switch").

**Which commands have JSON output, and the shape of the replacements, were
measured on real switches (one EOS 4.14.2F, one EOS 4.20.7M) on 2026-10-17**,
key names and value types only; no value was copied. See "Measured on real
switches" below.

Everything in them is invented: serial numbers start with `FAKE`, MAC addresses
are locally administered (`02:xx`), IP addresses are in the documentation ranges
(192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24), and host names are generic
(`sw-a1`, `srv-01`).

## Layout

One directory per modelled EOS version, one file per command. The file name is
the command with spaces and dashes turned into underscores
(`show lldp local-info` is `show_lldp_local_info.json`). `fake.go` serves them.

| Directory | Models | Documentation it follows |
|---|---|---|
| `eos-4.14.2F/` | `sw-a1`, DCS-7050SX-64, EOS 4.14.2F (2014 train), MLAG active with `sw-a2` | Arista EOS 4.14 command reference and eAPI "JSON output" examples |
| `eos-4.20.7M/` | `sw-b1`, DCS-7280SE-64, EOS 4.20.7M (2018 train), MLAG disabled | Arista EOS 4.20 command reference and eAPI examples |

`sw-b1` is wired to `sw-a1` (Ethernet49 to Ethernet51) and `sw-a1`'s LLDP table
sees it back, so the two fixtures describe one coherent little network.

## Measured on real switches (2026-10-17)

A command that exists but has no JSON model on a release is answered with
eAPI error **1003**, `CLI command N of M '<cmd>' failed: unconverted command`
(text format works). The collector treats it as "not available on this EOS
release": a warning, not a failure. `fake.go` plays it from its `unconverted`
list, so those commands have no fixture.

| Command | 4.14.2F | 4.20.7M | Collector's replacement |
|---|---|---|---|
| `show uptime` | no JSON | no JSON | `show version`: `uptime` (4.20), else now minus `bootupTimestamp` (float epoch seconds, both releases) |
| `show lldp neighbors detail` | no JSON | JSON | `show lldp neighbors`: `lldpNeighbors` is a **list** of `{port, neighborDevice, neighborPort, ttl}`; asked only when the detail is unavailable |
| `show lldp local-info` | no JSON | JSON | none needed: `show hostname` names the switch |
| `show port-channel` | no JSON | JSON | `show interfaces`: each Port-Channel record has `memberInterfaces`, an object keyed by member name |
| `show lldp neighbors` | JSON | JSON | — |

On the 4.14 switch, 10 of 13 brief LLDP entries gave a MAC address as
`neighborPort` (Linux hosts): the brief table has no port description to
name those ports by, so the collector leaves them out and says how many.

`eos-4.20.7M/show_uptime.json` is kept only for `FakeArista.ConvertAll`, which
plays a release that converts every command.

## Differences between the two versions that the fixtures exercise

| Subject | 4.14.2F fixture | 4.20.7M fixture |
|---|---|---|
| LLDP neighbours | no detail (no JSON): brief table, names only | detail: `interfaceId` (quoted) and `interfaceId_v2` (clean) |
| LLDP chassis id | not in the brief table | MAC chassis ids only |
| MAC address format | dotted (`02aa.0000.0101`), `entryType: dynamic` / `static` | colon (`02:aa:00:00:03:01`), `learnedDynamicMac` / `configuredStaticMac` |
| Transceiver DOM keys | short names (`Et1`) | long names (`Ethernet1`), plus `vendorName` |
| DOM without optics | `"N/A"` | `"-inf"` |
| Fan trays | one speed per tray, no `fans` list | `fans` list per tray, each with its own `name` |
| Temperature | chassis sensors and per-PSU sensors | adds `cardSlots` sensors and sensors named inlet / exhaust |
| Power | `outputPower` only | `outputPower` and `inputPower`; PSU 2 in `powerLoss` |
| MLAG | `state: active`, domain, peer address | `state: disabled`, empty strings |
| Trunk ports in `show interfaces status` | no `vlanId` (native VLAN unknown) | `vlanId` is the native VLAN |
| `interfaceAddress` | array in `show interfaces`, object in `show ip interface` (both versions) | same |

## What must be checked on a real switch

Each line is a place where the fixture is a guess, or where the collector makes
an assumption a real device may contradict. Record one real response per
version and diff it against these files before trusting the collector.

1. ✅ *4.14: the detail has no JSON at all (measured); the brief table is used.*
   **LLDP port id** (4.20 detail): whether `interfaceId` is wrapped in literal
   quotes, and from which release `interfaceId_v2` exists. The collector prefers
   `interfaceId_v2` and strips quotes, so it tolerates both, but the names the
   neighbour's port ends up with must be compared with the neighbour's own view.
2. **Interface names in `show interfaces transceiver`**: `Et1` or `Ethernet1`,
   per release. The collector expands `Et`, `Ma`, `Po`, `Vl`, `Lo`.
3. **MAC address-table format**: dotted or colon, and the exact `entryType`
   spellings. The collector keeps an entry only when its type is empty or
   contains `dynamic`; if a release spells learned entries differently
   (`learned`, `Dynamic`...), `learned_macs` will silently be empty.
4. **`interfaceAddress`**: object or array depending on the command and the
   release. Both are read; `show ip interface` is the authority for addresses.
5. **Fan trays**: whether `show environment cooling` lists the fans of a tray
   (`fans`) on the target model, or only a tray-level `speed`.
6. **Inventory slot shapes**: `xcvrSlots`, `powerSupplySlots`, `fanTraySlots`
   as objects keyed by slot (documented) or arrays (tolerated); the shape of an
   empty transceiver slot (empty strings here; `"Not Present"` in some releases).
7. ✅ *4.14: no JSON (measured); membership read from `memberInterfaces`.*
   **`show port-channel`** (4.20): the JSON shape (`portChannels` / `activePorts` /
   `inactivePorts`) and whether the MLAG peer's ports (`Peer...`) appear in it
   (the collector skips them).
8. **`show mlag` when disabled**: the fields present; the collector reports
   `mlag_state` only and drops empty domain / peer.
9. **Native VLAN**: a trunk with no reported native VLAN is assumed to be VLAN
   1. A switch with a non-default native VLAN configured but not reported by
   its release would show that VLAN as tagged.
10. **Sensor placement**: only an explicit inlet / intake / outlet / exhaust /
    cpu / power supply / board / system in the sensor description is mapped. A
    "front-panel" or "rear-panel" sensor is `other` rather than guessed.
11. **Speed on LAGs**: `speed_mbps` of a Port-Channel is its operational
    bandwidth as reported; check it against the sum of the active members.
12. **Memory**: `memTotal` is read as kibibytes, as documented; check on a real
    device that `memory_bytes` matches the platform's RAM.
13. **PSU and fan states**: the collector maps `ok` to ok, `powerLoss` and
    `failed` to critical, `notInserted` and `absent` to absent, and anything
    else to unknown. The exact state strings per model are not known here.
