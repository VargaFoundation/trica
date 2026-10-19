# ADR-0047: A device's parts placed on its faces

- **Status**: accepted (2026-10)
- **Concerns**: `internal/devicetypes` (`layouts/`, `Layout`, `Spot`), `scripts/devicetype-faces`,
  `internal/api` (`GET /objects/{id}/faces`, `placeParts`), `internal/store` (`Descendants`),
  `web/src/faces`, `cmd/trica-e2e-server` (srv-p1). Builds on ADR 0042 (images) and ADR 0043
  (faces drawn by the project).

## Context

The rack elevation paints each device's face, but the object page of a device says nothing of
where its parts are: a failed disk or power supply is a row in the Components tab, and finding it
on the chassis is the operator's job. The collectors already report the parts (Redfish disks,
power supplies, fans and ports; the agent's disks and interfaces; the switches' interfaces) with
names that, most of the time, say where they sit: `1I:1:3` on an iLO, `Disk.Bay.2` on an iDRAC,
`Ethernet12` on an Arista, `eno2` on a Linux host. Nothing says where those places are on a
picture.

## Decision

1. **A layout per face**, `layouts/<manufacturer>/<slug>.<face>.json` beside the image, gives the
   size of the image and its **spots**: kind (`bay`, `psu`, `fan`, `port`, `slot`), name, and a
   box in the image's units. The project's layouts are written by `scripts/devicetype-faces` from
   the same description as the drawing, so a drawing and its layout cannot drift. The file is
   checked strictly at load (known fields, positive size, 1 to 512 spots, each inside the image,
   no two of one kind with the same name); an embedded layout without its image, or an image
   nobody has, fails the load, as images do.
2. **A layout belongs to its image.** An operator image that replaces a face drops the embedded
   layout of that face (the spots of a drawing do not fit a photograph) unless the operator gives
   one; an operator model file that replaces an embedded one keeps its images and layouts. An
   unused operator layout is a warning, never fatal.
3. **Spot names are the device's own**: a bay number (`"3"`, or `"<box>:<bay>"` where the vendor
   numbers bays per box, HPE), the power supply, fan or slot number as printed, a switch port by
   the name its own system gives it, and on a server `bmc`, `nic<n>` (on-board ports) and
   `flexlom<n>`.
4. **The server places, the browser draws.** `GET /objects/{id}/faces` returns the faces of the
   device's model (image path, size, spots) and the device's parts of the kinds those faces have
   spots for, each with the spot it goes in, or none. The rules read the names the collectors
   write (listed in `internal/api/faces.go` and the [reference](../reference/devicetypes.md#where-the-parts-sit)).
   **A name the rules do not read leaves the part unplaced; nothing is guessed** (no "the third
   disk goes in the third bay"): the UI lists unplaced parts under the drawing and says why.
5. **Permissions per part.** The device needs the read right on its site; each part is listed
   only when the caller may read its type there; the model only when the caller may read it.
   Absent parts are left out. The parts are read below the device three levels deep, 2000 at most
   (`truncated` says so).
6. **The drawing is an SVG of spots over the image**, coloured by the worst health of their parts
   (else, for a port, whether its link is up), with the same tokens as the health pills and the
   state also in each spot's accessible name. The filled spots of a face are one tab stop; the
   arrow keys, Home and End move between them; hover or focus shows the part's details beside the
   drawing; a click opens the part. The image is fetched with the token and painted on a canvas,
   as on the rack (ADR 0042): no `blob:` or `data:` URL under the CSP.

## Consequences

- The project's drawings (ADR 0043) all have layouts. Upstream images (the DL380 Gen10, the
  7050SX-64 fronts) have none: the object page shows the picture without spots and says so.
- Known gaps, each leaving parts unplaced rather than misplaced: HPE Redfish NICs named `1`..`8`
  (not `eno<n>`), a FlexibleLOM port seen by Linux as `eno49`, power supplies read by
  `BayNumber` (ignored: the name is used), a Dell R330 that matches the upstream model (no
  variant, so no layout), and the DL180 Gen9 LFF fronts, whose bay positions are schematic and
  have no layout.
- Rejected: placing parts in the browser (the rules would be duplicated in TypeScript and the
  client would read every part to draw a few), storing a `spot` attribute on parts (a collector
  would have to know the library), and ordering parts into spots by count.
- The e2e server's srv-p1 (a DL360 Gen9 8SFF in no rack) shows every state; its placements are
  given, not computed: the rules are tested in `internal/api`.
