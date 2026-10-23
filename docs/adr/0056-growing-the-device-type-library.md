# ADR-0056: Growing the device-type library: 0 U models, a certainty rule by `variant_of`, shortnames in an overlay

- **Status**: accepted (2026-10)
- **Concerns**: `internal/devicetypes` (`Parse`, `Load`, `Index.Choices`, the new overlays),
  `internal/api` (rack contents), `scripts/devicetype-library/`, `docs/reference/devicetypes.md`
  and `devicetypes-catalogue.md`. Amends ADR 0020 (format, subset) and ADR 0048 (certainty). Builds
  on ADR 0042 (images), ADR 0043 (variants by part number).

## Context

The library held 199 models, 20 of them HPE, six Ubiquiti and four Synology, and the owner asked
for at least twice as many, with their own equipment in it: a Synology DS1821+ and DS3617xs, the
UniFi cloud gateways (UDM Pro Max, UCG...), U7 Pro access points, a USW Pro Max 24 PoE and UniFi
Access. Upstream (netbox-community/devicetype-library, CC0-1.0) has most of them, and three
rules of the loader and the matcher stood in the way:

- **Height 0.** Upstream writes `u_height: 0` for what occupies no rack unit: 120 of the 210
  Ubiquiti files (access points, desktop gateways), 31 of the 58 Synology files (DiskStations),
  the HPE MicroServers and Aruba access points. `Parse` required `u_height > 0`.
- **Certainty.** A name match was "not certain" (`base`) as soon as any model of the vendor had a
  longer name starting with the device's words. With the new files that would flag the user's six
  `U7 Pro` as "Variant not certain" (U7 Pro Max, Wall, XG, XGS, Outdoor), and the same for
  `UDM Pro`, `ThinkSystem SR630` and `DL325 Gen10`. It was already wrong for `ProLiant DL385
  Gen10`, uncertain only because `DL385 Gen10 Plus v2` exists.
- **Shortnames.** A UniFi controller reports a device by a shortname (`USPM24P`, `UDRULT`,
  `UDMPROSE`) and a display name that is not always upstream's (`Express 7`, `AC Pro`). Upstream
  files do not carry them.

## Decision

1. **`u_height: 0` is accepted and means "occupies no rack unit"** (desktop, wall, ceiling, a
   vertical PDU). This is upstream's meaning; upstream files stay unchanged. A missing, negative,
   NaN or over-100 value still fails the load. `Model.Racked()` says whether a model has a rack
   height. `Sync` declares the 0 as it is. Rack contents treat a model height of 0 or less as no
   height (`positive` in `internal/api`), so the device's own `u_height` applies; the web already
   draws an assumed 1 U for a mounted device with no height. Rejected: skipping the 0 U files
   (silent misses, against ADR 0020: a half-loaded library), editing the copies to say 1 U (false,
   and the copies would no longer be upstream's), and a new `rackable` key.
2. **When a name match is certain** (amends ADR 0048 §2). A name, mounting or part-number match is
   uncertain (`base`) only when another model's `variant_of` names the matched model. Models
   whose name merely extends the device's stay in the candidates, so "Change the model" still
   offers them, but they no longer make the match uncertain. Every family of the catalogue that
   has chassis variants is curated by `variant_of` (ADR 0043), so no existing behaviour changes
   but the false positives. The families without it keep their paths: the DL180 Gen9 has no
   generic model (`ambiguous`), the DCS-7280SE is an airflow family (`airflow`).
3. **Upstream files are recorded in a manifest and proved unchanged.**
   `internal/devicetypes/library/upstream.tsv` has one line per file of a vendor directory: its
   path, the upstream commit it was copied at and its git blob id. `scripts/devicetype-library/fetch.sh`
   copies the files listed in `selection.txt` (refusing a download whose blob is not the one the
   upstream tree lists at that commit, and never overwriting a file that differs), `adopt` records
   the files copied before the manifest existed by finding their blob among the three earlier
   pins, and `verify` re-checks. `TestUpstreamFilesAreUnchanged` recomputes every blob and requires
   the manifest and the vendor directories to match one to one. A change to a model goes in
   `library/trica/` or the overlay, never in a copy. Downloads are data: written to their target
   path, never executed.
4. **Selection rules** for an upstream file: valid for `Parse`; at least one port (the tests
   require it, a model with none cannot be matched usefully); not `subdevice_role: child` (blades,
   controllers, modules); no slug clash; found by its own model name (`TestEveryModelIsFoundByItsOwnName`).
   This copied 317 files at master `4a87296236ab01f624b42de578a5f7720d0255fe`, licence checked
   2026-10-23. Excluded on purpose: children and portless enclosures, Gen5 to Gen7 servers, airMAX,
   UISP and cameras. *Amended (#159): `norm` reads `+` as `plus`, and the four files whose name
   only differs by a `+` (DS218, DS218+, RS815, RS815+) are copied at the same commit (321 files
   at `4a87296`).*
5. **Shortnames live in an overlay, not in variant files.**
   `library/overlays/<Vendor>.yaml` (Apache-2.0, written here) adds `part_numbers` to upstream
   models by slug; `applyOverlays` runs at the end of `Load`. A device reporting the shortname then
   matches by SKU and is certain, and the upstream file stays byte-identical. Every mistake fails
   the load: an unknown key, an unknown slug, another manufacturer, a model that is not an
   upstream one (a project file lists its own `part_numbers`), an empty entry, a shortname listed
   twice (after normalisation) or already listed by another model. The shortnames of
   `overlays/Ubiquiti.yaml` come from Ubiquiti's public device fingerprint database
   (`https://static.ui.com/fingerprint/ui/public.json`, read 2026-10-23). Rejected: a variant model
   file per shortname (a second model with the same name makes the name ambiguous, so nothing
   matches, or a different name that is not what the device reports). Operator overlays in
   `devicetypes.extra_dir` are out of scope.
6. **Vendor aliases:** `Ubiquiti Inc.`, `Ubiquiti Networks`, `UBNT`, `UniFi` for Ubiquiti and
   `Synology Inc.` for Synology.
7. **Models written here** when upstream lacks them, each citing its source in the file: Synology
   DS3617xs (data sheet), UniFi Access Door Hub and G2 Reader Pro (Ubiquiti tech specs). They are
   `u_height: 0`; the DS3617xs power inlet and airflow are left out because the data sheet does not
   state them.
8. **Images.** Only the upstream faces of the rack-mounted UniFi gateways and the USW Pro Max 24
   PoE (ten files), processed like the catalogue's (at most 1000 px wide, Lanczos, 256-colour
   palette). Adding every upstream picture of the new models would break the 15 MiB budget and
   force the "catalogue pack" decision of the reference page. The list of the models is generated
   (`docs/reference/devicetypes-catalogue.md`, checked by `TestCatalogueListIsCurrent`) instead of
   written by hand.

## Consequences

- 519 models (from 199; 523 since #159): HPE 128, Synology 38 (42 since #159), Ubiquiti 101 among them.
- The Ubiquiti and Synology DiskStation models are 0 U: they are in the inventory with their
  ports and weight, but they have no place on a rack elevation (a mounted one draws as an assumed
  1 U, flagged).
- A UniFi collector must report the display name as `model` and the shortname as `part_number`.
  A name that matches neither is the case to add to the overlay.
- Part numbers that upstream gives to two models stop matching by part number alone (the names
  still match): HPE `P19766-B21` (DL360 Gen10 and Gen11), `P56707-B21`, `R4U28A`, Lenovo `7X0x`,
  `7Z7x`, `7D7x`. They are upstream's data errors; they are not fixed in the copies.
- Library drift after `4a87296` is not watched: a refresh is a run of `fetch.sh add` at a newer
  commit.
