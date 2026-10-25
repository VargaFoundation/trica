# ADR-0043: Chassis variants by part number, and device faces drawn by the project

- **Status**: accepted (2026-10); superseded in part by [ADR 0063](0063-the-device-type-library-is-tricas-data.md) (§4, the image origin: a drawn image is `drawn`, every embedded image has the origin `trica`)
- **Concerns**: `internal/devicetypes` (`part_numbers`, `Find`, image origin, `drawings/`),
  `scripts/devicetype-faces`, `cmd/trica-e2e-server` (rack r21). Builds on ADR 0020 (library,
  matching) and ADR 0042 (images).

## Context

ADR 0042 shows a model's picture on the rack. Two gaps remained:

1. **A model name does not say which chassis.** HPE servers report the same model for every
   drive cage of a generation: a DL380 Gen9 says `ProLiant DL380 Gen9` whether it is the 8SFF
   or the 24SFF chassis, a DL180 Gen9 the same for 8SFF, 8LFF and 12LFF. The front differs,
   and so does what an operator looks for on it. The part number does say it (Redfish `SKU`,
   MAAS `system_sku`: `767032-B21` is the 24SFF configure-to-order chassis), but the upstream
   format has one `part_number` per model, and the match tried the name first. The upstream
   DL360 Gen9 file even names the 8SFF chassis (`755258-B21`) while its picture is the 10SFF
   build.
2. **Few pictures exist that may be shipped.** Upstream has none for the DL380 Gen9, DL180 Gen9,
   R330, CRS354, 7280SE-64, nor rears of the 7050SX-64; VisioCafe's licence forbids web use and
   modification; vendor photographs are not ours to redistribute.

## Decision

1. **`part_numbers`**, a list in a model file (a trica extension; upstream ignores unknown
   keys, so the file stays upstream-compatible), names the SKUs of that exact chassis. `Find`
   tries it **first**: a device whose part number is listed by exactly one model of its vendor
   matches it (`how: sku`), before the name, the upstream `part_number` and the airflow family.
   The list is indexed apart from names and `part_number`, so it never makes them ambiguous; a
   SKU listed by two models matches neither (and a test keeps the embedded library free of
   that). Without a listed SKU nothing changes.
2. **Variants are separate models** written by the project (`library/trica/`), named after the
   chassis (`ProLiant DL380 Gen9 24SFF`, slug `hpe-proliant-dl380-gen9-24sff`). The upstream
   model keeps the plain name and stays what a device of unknown chassis matches; when the
   variants share no generic model upstream (DL180 Gen9), an unknown SKU matches nothing rather
   than a guessed cage.
3. **The project draws faces itself**: original, schematic, vendor-neutral vector drawings
   (no logo, no trademark, no traced picture), built from the counts, grouping and order the
   vendors publish as text (QuickSpecs, owner's manuals, data sheets), by a deterministic
   generator, `scripts/devicetype-faces/` (Node, no dependency; PNG through the e2e suite's
   pinned Playwright Chromium). The SVG sources live in `library/drawings/<manufacturer>/<slug>.<face>.svg`,
   the PNG (1000 px wide, 19-inch proportions) in `elevation-images/` as any image. The
   drawings are released under **CC0-1.0**, like the upstream library they complement, so they
   can be contributed there.
4. **An image with a drawing is the project's**: its origin is `trica` even when its model is
   an upstream file (the CRS354, the 7050SX-64 rears). SVG stays refused as a served image.
5. **What the text does not say is drawn schematically and said so** in the generator and the
   model file (Dell R330 front positions, DL180 Gen9 LFF bay positions, CRS354 port grouping and
   rear). Dell reports no chassis part number (its Redfish `SKU` is the service tag), so an R330
   variant is reached by name or by the operator's directory, not by SKU.

## Consequences

- 11 trica models have drawn faces (9 server chassis variants and the 7280SE-64-F/-R), and three
  upstream models gain the faces upstream lacks (CRS354 front and rear, 7050SX-64-F/-R rears);
  the rack r21 of the e2e server shows them.
- A wrong `part_numbers` entry would picture the wrong cage: entries come from the vendor's
  configure-to-order and pre-configured tables only, cited in each file.
- Rejected: choosing the variant from the number of drives a collector sees (a half-populated
  24SFF looks like an 8SFF), and a single model with a list of pictures (weight, depth and ports
  differ by chassis too).
- Not decided here: picking a model by hand in the UI (still ADR 0042's open point), which is
  what a Dell R330 variant needs today.

## Addendum (2026-10-19): the catalogue

The library grows into a catalogue of the most common refurbished and data-centre models
(docs/reference/devicetypes.md, "Catalogue"): 190 models, 159 with faces. Three choices follow
this ADR rather than reopen it:

- **Families.** Every variant names its generic model with `variant_of` (ADR 0048). A family so
  curated is the whole family: the chooser no longer offers a model whose name merely extends
  the generic one's (`ProLiant DL380 Gen10 Plus` under `ProLiant DL380 Gen10`), which the
  catalogue's wider vocabulary would otherwise bring in.
- **Parametric drawings without layouts.** Catalogue faces are generated by builders from counts
  (bays per chassis, ports per group, supplies, fans, slots), not drawn one by one; they carry no
  layout, since their numbering is not in the vendors' text. The hand-drawn faces keep theirs.
- **Embedded, under a budget.** The whole library stays in the binary while it weighs under
  15 MiB (10.6 MB with the catalogue, the drawn PNG reduced to a palette); past that, the
  catalogue moves to a pack read as `devicetypes.extra_dir`, the models of the project's own
  racks staying embedded.
