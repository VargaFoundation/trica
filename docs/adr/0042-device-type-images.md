# ADR-0042: Device-type face images, the operator's directory, and how the page shows them

- **Status**: accepted (2026-10)
- **Concerns**: `internal/devicetypes` (images, `Open`, `devicetypes.extra_dir`), `internal/api`
  (`GET /device-types/{slug}/{face}`, `GET /racks/{id}/contents`), `internal/store`
  (`DescendantValues`), `internal/wiring`, `web/src/racks`, `web/src/export/rackCanvas.ts`,
  `cmd/trica-e2e-server`, the chart (`deviceTypes.existingClaim`). Builds on ADR 0020 (library),
  ADR 0019 (racks), ADR 0024 (exports, "change no policy"), ADR 0040 (console look: tokens, `HealthPill`).

## Context

The rack elevation drew every device as the same blue box. Operators recognise a rack by its
faces; a picture of each model, and a hint of what a device is when there is no picture, make
the view usable at a glance. ADR 0020 left images out. Three constraints shape the answer:

1. **Licences.** The usual source of vendor drawings, VisioCafe, forbids redistribution and use
   on any web service. The netbox-community/devicetype-library `elevation-images/` are CC0-1.0,
   like its model files, and cover part of what we meet.
2. **Any organisation deploys trica.** Nothing in the binary may be specific to one site, and an
   operator must be able to add photographs of their own equipment without rebuilding.
3. **The page's policy.** The API takes a bearer token, which an `<image href>` cannot send; the
   CSP allows images from `'self'` only, so a `blob:` or `data:` URL of fetched bytes is refused
   (ADR 0024 already relies on this). Inline styles are forbidden.

## Decision

1. **Embedded images follow the upstream convention**:
   `library/elevation-images/<manufacturer>/<slug>.<face>.<ext>`, png, jpg, jpeg or webp, at most
   2 MiB, content checked against the extension; SVG refused (same-origin script). The upstream
   `front_image` / `rear_image` booleans are validated but not trusted: the file decides. Vendored
   files are scaled to 1000 px and recompressed (CC0 allows it), listed with their origin in
   `library/README.md` and `NOTICE`. An embedded image that matches no model fails the load.
2. **The operator's directory** (`devicetypes.extra_dir`, `TRICA_DEVICETYPES_EXTRA_DIR`, chart
   `deviceTypes.existingClaim`) has the library's layout and is read once at start over it: a
   model with an embedded slug replaces it (keeping that slug's embedded images), an image
   replaces the embedded one. An unused file is a logged warning; an invalid one leaves the
   server without the library, never down (as ADR 0020's sync failures); a path that is not a
   directory refuses the start. Hidden entries are skipped (Kubernetes volumes).
3. **Served by the API**, under the existing authentication: `GET /api/v1/device-types/{slug}/{face}`,
   read right, `Cache-Control: private, max-age=3600`, strong ETag and 304,
   `Content-Security-Policy: default-src 'none'; sandbox`. Images are library content, not
   inventory data, so no per-object check; the existence of an image says nothing about the
   inventory. The library is loaded once per process and shared by the API and the sync.
4. **The rack contents say what to draw**: `front_image` / `rear_image` paths (only those that
   exist), `model_name`, `role`, `health`, and `parts_health`, the worst ok / warning / critical
   among the device's parts **the caller may read** (one recursive query, three levels; absent
   parts and unknown values do not count). The `device_type` object also carries the library path
   in `front_image` / `rear_image` (declared, like the rest of the library's values).
   *Amended (#134, STATUS row 115): the image is the device's, not the rack face's: its front on the
   face it is mounted from, its rear on the other face of a full-depth device, none behind a shallow
   one (NetBox's convention, ADR 0049; `slotImage`, #146).*
5. **The page fetches the bytes with the token, decodes them with `createImageBitmap` and paints
   them on a canvas inside the SVG** (`foreignObject`). No URL is ever made of the bytes, so the
   CSP is unchanged. Only paths matching `/api/v1/device-types/<slug>/<face>` are ever fetched
   with the token. The export sheet draws the same bitmaps. Rejected: widening `img-src` to
   `blob:` (a policy change for a picture), and serving images without authentication (an
   operator's photographs are theirs to protect).
6. **Without an image, a generic face per role**, as pure geometry (`racks/faces.ts`) drawn both
   in SVG with theme classes and on the export canvas with the fixed palette. **Health** is a
   strip on the left edge plus an outline for warning and critical, in the hues of the
   `--trica-*` health tones of ADR 0040, always repeated in words (tooltip, accessible name with
   the shared `healthState.*` words, and the table's `HealthPill`).

## Consequences

- Six embedded images cover the HPE DL360 Gen9 and DL380 Gen10 (both faces) and the Arista
  7050SX-64-F/-R (front). Models upstream has no image of (DL380 Gen9, CRS354-48G-4S+2Q+RM) and
  models we wrote (DCS-7280SE-64-F/-R, the generic pfSense appliance) are drawn generically until
  an operator adds a photograph.
- A canvas inside `foreignObject` is supported by the three engines the e2e suite runs on; a
  browser without `createImageBitmap` falls back to the generic face.
- Images are read at start: a change in the operator's directory needs a restart.
- Not decided here: drawing port templates on the faces, module bays, picking a model by hand.
