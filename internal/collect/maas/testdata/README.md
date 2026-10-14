# MAAS fixtures

**These files are SYNTHESISED. They were NOT recorded from a real MAAS.** They
were written from the MAAS REST API 2.0 documentation and from memory of what
the API returns, then anonymised by construction. No real equipment was
contacted. Treat them as a statement of what this collector believes MAAS
sends, not as proof that MAAS sends it.

| Directory | Models | Basis |
|---|---|---|
| `v3.5/` | MAAS 3.5 (`version` 3.5.2) | API 2.0 documentation for 3.5. |
| `v3.3/` | MAAS 3.3 (`version` 3.3.4) | Same documentation plus **recollection** of what differs in 3.3. The differences are not checked against a 3.3 server. |

Files in each directory: `version.json` (`GET version/`), `whoami-admin.json` and
`whoami-user.json` (`GET users/?op=whoami` for a superuser and a plain user),
`machines.json`, `subnets.json`, `fabrics.json`.

Content: three machines in 3.5 (a bonded, bridged, VLAN-tagged server with real
identifiers; an unallocated server whose firmware reports only placeholders such
as `Unknown`, `Not Specified`, the AMI default UUID; an LXD virtual machine) and a
fourth, never-commissioned machine in 3.3 (no hardware info, no interfaces).
Differences modelled between 3.3 and 3.5: no `hardware_info.system_sku`, no
`firmware_version` on block devices, no vendor/product/firmware on interfaces, no
`disabled_boot_architectures` on subnets.

Anonymisation: MAC addresses are locally administered (`02:xx`), IP addresses
are only in `192.0.2.0/24`, `198.51.100.0/24` and `203.0.113.0/24` (RFC 5737),
names are generic (`srv-01`, `fabric-0`, user `alice`), serial numbers and
identifiers contain `FAKE` or start with `fk`, and there is no secret. The fake server
(`../fake.go`) accepts only the obviously fake key
`fakeconsumer:faketoken:faketokensecret`.

## What to verify on a real MAAS

1. Whether `version/` answers without authentication (the collector sends the
   credentials anyway) and its exact JSON shape (`version`, `subversion`).
2. That `users/?op=whoami` exists in your release with the `is_superuser` field,
   and that the `?op=...` query form (not `op-...`) is the one the API accepts.
3. Field names and types per release: numbers that arrive as strings, `memory`
   in MiB, block device `size` in bytes, `cpu_count` meaning logical CPUs.
4. The keys of `hardware_info` (`system_vendor`, `system_product`,
   `system_serial`, `system_sku`, `mainboard_firmware_version`) and what they
   hold on your hardware, VMs and machines that were never commissioned.
5. The tags MAAS sets on block devices (`ssd`, `rotary`, `sata`, `sas`, `nvme`)
   that drive `media` and `protocol`.
6. Interface types and the `parents` of bonds and bridges, `link_connected`,
   `link_speed`, `effective_mtu`, and the shape of `links`.
7. The 3.3 versus 3.5 differences listed above, and anything that changed in 3.4
   and 3.6.
8. What a non-admin key really gets from `machines/` (the fake keeps unowned
   machines and the user's own).
9. Large installations: response size and time of `machines/`, which embeds
   everything in one document.

When recorded responses become available, add them as new directories next to
these ones, anonymise them before committing, and extend the matrix in
`../README.md`.
