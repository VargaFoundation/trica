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

## Addendum (2026-10): the DL380 Gen10, one part under two names, why a part is not placed

Seen on a real DL380 Gen10 8SFF (CTO 868703-B21) used as a storage node: five NVMe drives iLO 5
reports as `3:1` to `3:5` (and each again as its own storage controller module, `DA000000`…), two
SATA system disks on the embedded controller that only the operating system reports (`sda`,
`sdb`), two power supplies `HpeServerPowerSupply 0` and `1`. The library matched the 8SFF chassis
by its part number, but its face was a catalogue drawing without a layout, its drives drawn in the
wrong box: nothing was placed, and the upstream photograph of the generic model shows one supply.

1. **The DL380 Gen10 faces are drawn with their layouts** (`faces.mjs`), from QuickSpecs
   a00008180enw: Box 1 (the only one that takes the Universal Media Bay), Box 2, Box 3 from left
   to right, the 8SFF CTO's "Primary 8SFF cage in Box 3"; bays named `<box>:<bay>`, so iLO's `3:N`
   (or `1I:3:N`, `Box=3:Bay=N`) lands in Box 3, bay N. The rear (shared by the five chassis):
   slots 1-8 of the three risers, power supply bay 1 on the far right and bay 2 beside it (the rear
   legend's order, and the CC0 picture's), the iLO port, the embedded and FlexibleLOM ports. The
   six fans are inside: no fan spot, so the fans are not listed under the drawing either. The LFF
   fronts keep their catalogue drawing without a layout (their bay numbering is not in the text).
2. **A cage option is a chassis variant, chosen by a person.** `hpe-proliant-dl380-gen10-8sff-nvme`
   (the 8 NVMe Express Bay kit 826689-B21 in Box 3) has no part number of its own and is a
   `variant_of` the 8SFF: the device's part number keeps matching the 8SFF, whose bays 3:1-3:8 hold
   NVMe drives just as well, and the "Device model" card offers the NVMe cage. A declared model
   that is a `variant_of` the model the part number names refines it: no `sku_conflict` (ADR 0048,
   decision 5). **Rejected:** inferring the cages from the parts (a drive in `3:x`, a firmware entry
   "8SFF NVMe Backplane" at `Box=3`) to swap the drawing in the faces endpoint: the rack, the
   "Device model" card and the faces would then show different models for one device, and a drive
   seen in a box says the box has a cage, not which one. A drive in a box the drawing shows blank
   is not moved elsewhere: it is unplaced as `no_spot`, and the page points at the chassis choice.
3. **One physical part is counted once.** A disk with the serial number, or a port with the MAC
   address, of a placed part shares its spot (`3:1` and `nvme0n1`; iLO's `2` and `eno1`); unplaced
   ones are one line, the others in `also`. The real iLO 5 numbered its embedded ports `2` to `5`
   and the FlexibleLOM's second port `1`: matching by MAC address places them where an order would
   have misplaced them. Modules are never parts of a face, so the NVMe drives' controller modules
   are not counted. A part without a state (the OS's view of a drive) does not grey out the spot
   the BMC's view colours.
4. **The answer says why a part is not placed** (`unplaced`: `os`, `no_spot`, `unread`) and the
   page groups the list by it, each line with a disk's model and capacity: the two system disks
   appear apart from the data disks, as "known only to the operating system".
5. **The drawings read as hardware at the size the page draws them**: a near-black drive cage,
   dark carriers with a status ring, a label window, a vent, a lever and a coloured latch
   (`driveCage`, `hotplugCarrier` in `draw.mjs`, used by the HPE faces of `faces.mjs`); a spot of a
   healthy part is a thin frame of its state's colour instead of a tinted box, a part needing
   attention keeps its tint, the spot under the pointer or the focus stands out.

Where the two SATA system disks of such a server physically sit (a Box 1 or 2 cage on the
embedded controller, the Universal Media Bay's 2 SFF, a rear cage) the inventory does not say: no
collector reports it, and they stay unplaced until one does or a person records it.
