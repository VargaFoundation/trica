# ADR-0068: Desktop devices on a rack shelf, and Synology's names on a face

- **Status**: accepted (2026-10). Amends [ADR 0047](0047-parts-placed-on-device-faces.md) and
  [ADR 0056](0056-growing-the-device-type-library.md).
- **Concerns**: `internal/devicetypes` (`height_mm`), `internal/api` (`RackItem.desktop`, placement
  rules), `scripts/devicetype-faces` (`desktop.mjs`), `web/src/racks` and `web/src/export`,
  `docs/reference/`. The API change is additive.

## Context

The production NAS (a Synology DS1821+, eight drives, not rackable) is declared on a shelf of a
16 U rack. Its model has `u_height: 0` ([ADR 0056](0056-growing-the-device-type-library.md): the
model occupies no rack unit), so the rack elevation drew the device's own `u_height` (4, the
declaration's guess) as a blank slab with no face, and its drives and ports on the object page were
not on any drawing: the model had no faces, and the collector's names (`sata1`, slot `Drive 2`,
`eth0`) are not what the placement rules read ([ADR 0064](0064-synology-dsm-collector.md)).

## Decision

1. **A model of 0 U that is mounted stands on a shelf.** The rack item says `desktop: true`. Its
   height in U is, in this order: the device's own `u_height`; else `ceil(height_mm / 44.45)`
   from the model's optional `height_mm` (the chassis height, 166 mm for the DS1821+, so 4 U);
   else the elevation's assumed 1 U, flagged as before. A model of 1 U or more is unchanged.
2. **A desktop device's face keeps its proportions.** The elevation (screen and export sheet) draws
   its front picture "contain"-fitted and centred inside the shelf instead of stretching it to the
   278 px slot. A rack unit's face is still stretched: its picture is a 19-inch panel by construction.
3. **A desktop model is drawn at its own size**: viewBox, layout and PNG have the chassis'
   proportions (343 x 166 mm), not 482.6 mm wide. The test that a drawing is a 19-inch
   panel of `u_height` U (`TestDrawingsMatchTheirImagesAndModels`) applies to racked models only; a
   0 U model with `height_mm` must have a drawing of that height.
4. **Names the placement rules read, added generically** (not DS1821+ special cases): a disk whose
   slot is `Drive <n>` is in bay `n` (DSM's long name for a tray; the DSM id does not say it, and
   empty trays leave holes); an interface `eth<n>` may be the port `LAN <n+1>` (DSM numbers the ports
   of the box then those of a PCIe card from `eth0`). A Linux host's `eth0` against a face without
   `LAN 1` is still an `os` name; `M.2 Drive <n>` and `nvme<k>n<m>` stay unplaced as `os` (the M.2
   slots are on the underside and no face has them).

## Consequences

- The production declaration no longer needs `u_height` on the NAS: its model gives 4 U. A declared
  value still wins.
- Rejected: stretching the face over the shelf (the drives look squashed and the picture lies about
  the proportions); drawing a desktop device at its true width on the rack (a 343 mm chassis on a
  450 mm shelf is a decision about the shelf, which the inventory does not know); a per-vendor
  mapping table (the two rules above are the whole need).
- Not proven: the drawing was not compared with a photograph, and the dimensions come from the
  author's memory of the specification sheet, which was not fetched. The ports of a PCIe card rely
  on DSM numbering them `eth4` and `eth5`: seen on one recorded DS1821+ with a 10 GbE card, not on
  another model of NAS.
