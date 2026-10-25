# ADR-0063: The device-type library is trica's data

- **Status**: accepted (2026-10). Supersedes in part ADR 0020 (§1, "dropped in unchanged and
  contributable", and §2), ADR 0043 (§4, the image origin) and ADR 0056 (§1, "the files stay
  unchanged", §3, §5, and the mechanism of the #173 and #177 amendments).
- **Concerns**: `internal/devicetypes` (`Load`, `Image`, the origins), `internal/devicetypes/library/`
  (the 97 Ubiquiti model files, `README.md`, the deleted `upstream.tsv` and `overlays/`),
  `scripts/devicetype-library/` (deleted), `scripts/devicetype-faces`, `internal/docscheck`,
  `NOTICE`, `docs/reference/devicetypes.md`, `devicetypes-catalogue.md` and `ui.md`.
  No schema change, no migration, no API change.

## Context

The owner does not want trica to depend on NetBox. The library never did, in code or at run time:
nothing reads, fetches or follows another repository when trica runs. But the library still carried
five ties to the repository its first files came from:

1. a manifest, `library/upstream.tsv`, and a fetch tool, `scripts/devicetype-library/`, that copied
   files "unchanged" and proved it with a test (`TestUpstreamFilesAreUnchanged`);
2. an overlay mechanism (`library/overlays/`, `applyOverlays`, `OverlayDir`) whose only reason was
   that the copied files could not be edited;
3. a model origin, `netbox-devicetype-library`, and an image origin of the same name;
4. a catalogue that labelled a picture `NetBox`, and comments, scripts and documents that named
   the repository, or "upstream", in more than a hundred places;
5. a format described as that of another project, so that "an upstream file can be dropped in and
   ours contributed back".

Reading the code showed that the origin value reaches nothing persisted or served: `Sync` declares
vendor, model, part number, height, depth, airflow, weight, PSU count, the ports and the image
paths, and not the origin; `Model` and `Image` are never sent out as JSON. The only origin anyone
can see is a layout's (`layout_origin`), and it is already `trica` or `operator`.

The 333 embedded images are of two kinds that the catalogue, `TestDrawingsMatchTheirImagesAndModels`
and the layouts rely on: 220 PNG drawn by the project (they have an SVG source under `drawings/`)
and 113 pictures of units. That distinction has to stay, under a neutral name.

## Decision

1. **The library is project data, edited in place.** No manifest, no fetch tool, nothing follows an
   outside repository. A change to a model is a change to its file.
2. **The format is kept, as trica's own.** It is documented in `docs/reference/devicetypes.md` as
   trica's model-file format, with no promise of compatibility with any other tool. A format is not
   a dependency.
3. **Origins become `trica` and `operator`, plus `Image.Drawn`.** `OriginUpstream` is deleted and
   `OriginLocal` is renamed `OriginTrica`; every embedded model, image and layout has it. A new
   field `Image.Drawn` (`json:"drawn,omitempty"`) is true when the embedded image has its SVG source
   under `drawings/`, false for a picture of a unit and for every operator image. The catalogue
   labels a face `picture`, `drawn` or `—`. Amends ADR 0042 and ADR 0043 §4.
4. **The overlays are folded into the files.** The 94 Ubiquiti models that had shortnames gain
   `part_numbers`, and the 40 that were renamed to `Port N` are renamed in their file, the former
   name kept as `label` (it is the evidence of the rename). The #177 rule now applies to the files
   themselves, and `TestUniFiPortTemplatesAreNamedAsTheConsoleNamesThem` checks it on the loaded
   models: 40 renamed, 46 blocked, a blocked model has no partly renamed interface. The loaded
   models are identical before and after (the dump compared in the pull request), so the first
   `Sync` after the upgrade changes nothing.
5. **Directories stay as they are.** `<Vendor>/` holds model files and `trica/` the models written
   from vendor documents, whose faces are drawn. `Model.Path` is never declared or served and images
   are found by vendor and slug, so moving files would be churn.
6. **The ADR 0056 §4 selection rules become rules for writing any model**: valid for `Parse`, at
   least one port, not a child device (a blade, a controller), a slug no model uses, found by its
   own name.
7. **Guards.** `TestNetBoxIsNamedOnlyInTheRecord` (`internal/docscheck`) fails when "netbox" appears
   anywhere in the repository but in the decision records that tell the history (ADRs 0020, 0042,
   0049, 0056 and this one) and `docs/plan/STATUS.md`. `TestEmbeddedLibraryLoadsAndIsValid` fails
   when a model has another origin than `trica`; `TestEveryPartNumberFindsItsModel` fails when a
   UniFi shortname is lost or becomes ambiguous.

**The only provenance line in the repository:** 451 model files and 113 face pictures were
imported between 2026-10-14 and 2026-10-23 from netbox-community/devicetype-library (CC0-1.0, which
attaches no condition); since this ADR they are maintained here. `NOTICE` no longer has a
third-party section for them: CC0 asks for no attribution.

## Rejected

- **Keeping the overlays.** They protected files that are now ours; a second place to edit a model
  has no remaining purpose.
- **Merging `trica/` into the vendor directories.** Churn without gain.
- **Keeping an "imported" origin.** It would keep the tie alive, and nothing at run time reads it.

## Consequences

- The first `Sync` after the upgrade writes no change: the models are identical, and the origin was
  never declared.
- In an operator's `extra_dir`, a directory named `overlays/` is no longer skipped (it was never
  documented).
- The part numbers that two models share (HPE `P19766-B21` and others), which the library had to
  leave as they were, can now be fixed in the files: a `finding` issue carries that work, out of
  scope here.
- A model's file can be edited to add or correct anything, with its source cited in the file.

## Not proven

- That the imported pictures are free of vendor trademarks (unchanged from ADR 0042).
- That the `idx` of the UniFi Integration API is the number printed on the faceplate (#177,
  unchanged).
