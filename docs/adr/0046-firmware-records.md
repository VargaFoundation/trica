# ADR-0046: Firmware as version records, compared with the same model

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect/redfish` (the `device.firmware` attribute), `internal/schema`
  (`builtin.yaml`), `web/src/firmware`.

## Context

The Redfish collector kept a device's firmware as one list attribute, `firmware_inventory`, of
`Name=Version` strings. The object page showed it as a block of text. Operators asked three
questions it could not answer: when was this version released, is it too old, and does a newer
one exist.

Redfish carries more than the version for each `SoftwareInventory` member: `ReleaseDate` (iDRAC,
iLO 5), `SoftwareId`, `Updateable`, a location (`Oem.Hpe.DeviceContext` on iLO 5,
`Location` on iLO 4). HPE also writes the build date into the version itself
(`U30 v2.72 (07/09/2021)`, `2.78 Dec 08 2020`). Dell answers `00:00:00Z` as a `ReleaseDate` when
it has none.

## Decision

**The collector writes the firmware a second time, as a json list of records, and the page draws
any such list as a table compared with the other devices of the same model.**

- `device.firmware` is a list of `{name, version, released?, software_id?, location?,
  updateable?}`, sorted like `firmware_inventory`. `released` is `YYYY-MM-DD`, from `ReleaseDate`
  when it is a real date (before 1990 or the Dell placeholder is no date), else from a date in the
  version (`MM/DD/YYYY`, `Mon D YYYY`). Without either, it is left out: a date is never guessed.
- Backup and redundant images (`Backup …`, `Redundant …`) stay in `firmware_inventory` and are
  left out of the table: they are what the device would fall back to, not what it runs.
- `firmware_inventory` is kept unchanged: TQL searches it as strings, and existing saved queries
  keep working.
- The page finds version records by **the shape of the value** (a non-empty list of objects with a
  string `name` and `version`), not by the attribute's name, like the rest of the object page,
  which groups from the schema and never from a list of names. Another collector giving the same
  records gets the same table. Beside it, the generic cards leave out these attributes and the
  `name=version` lists.
- A component is **stale** past two years (730 days) from its release date: flagged in colour and
  in words.
- "Newer elsewhere" compares each component with the same component (by name) on the other devices
  of the same `model`, from the fleet query the inventory page already makes (1000 devices at
  most). Versions are compared token by token, numbers as numbers, build dates aside; two versions
  whose tokens disagree in kind or in letters (`P89` and `U30`, two ROM lines) are not comparable,
  and then the later release date decides, if both have one.

## Consequences

- No vendor catalogue and no CVE: "newer" means newer than what the fleet runs, not than what the
  vendor ships. Telling the latest release or the known vulnerabilities needs an outside source
  (the vendor's catalogue, NVD), with its own access, licence and cost: a separate decision.
- The firmware is stored twice per device. Both change together, at the rare firmware update, so
  the history grows by two rows per update, not per run.
- A version scheme the comparison does not understand gives no verdict rather than a wrong one;
  the table then says nothing in the fleet column for that component.
- The dates and locations come from what the BMC reports; the fixtures were extended from the
  vendors' documented schemas, not recorded (see the collector's `testdata/README.md`).
