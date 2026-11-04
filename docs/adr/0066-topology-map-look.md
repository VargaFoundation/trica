# ADR-0066: The topology map looks like UniFi's: a rich isometric scene and real links

- **Status**: accepted (2026-10).
- **Concerns**: `web/src/topology/map` (`scene.ts`, `IsoScene.tsx`, new pure modules `route`, `speed`,
  `port`, `tree`, `decor`, `labels`), `web/src/styles` (tokens, `app.css`), `internal/topomap`
  (`model` on a node), `internal/theme`, `docs/reference/ui.md`, `docs/reference/themes.md`.
  Amends ADR 0051 (drawing), 0062 (uplink style kept) and 0054 (new tokens).

## Context

Production feedback: the 3D view is "too simplistic" and the links of a rack are "very basic, not what
UniFi does". Boxes were flat, links were arcs from centre to centre, and confidence was drawn as a dash
style that hid the one thing a network engineer reads first: speed and state.

## Decision

1. **The data contract is unchanged**; `GET /api/v1/topology/map` gains one additive field, `model` on a
   node (a device's model, for the rack card). The aggregation of ADR 0051 is untouched.
2. **Scene per level.** Ground plane with a masked grid; every block extruded with three faces, a contact
   shadow and a rim. Global: sites as buildings (roof, windows, plate with code and device count), the
   Internet as a cloud, tunnels and uplinks as raised arcs. Site: rooms as slabs with cut-away walls
   (back taller than front) and their name on the floor, an area on its own slab, racks as small
   cabinets. Room: cabinets at `floor_x/floor_y` with a glass door, vents and LEDs, cable trays. Rack: the
   tall rack with its scale, slabs at their unit, a shelf for loose devices, and cards for linked devices.
3. **Links.** Room and rack lines are orthogonal, rounded, leave each device from its side, are fanned
   out when parallel and bundled with a `xN` badge for a LAG. Between rooms and above, they stay raised
   arcs (an orthogonal route over buildings reads worse). Non-rack endpoints of a room (portals, the
   shelf) also fall back to arcs.
4. **Encoding.** Colour and width = speed class (7 tokens `--trica-iso-speed-*`); halo = utilisation
   (existing util tokens); down = red dashed with a cross; unknown = grey dotted, **only when some line
   of the map has live state** (otherwise every line is drawn as running, since "unknown" everywhere
   carries no information); `uplinked_to` and tunnel keep their medium dash, which wins over a state
   dash; tunnel = double line plus a pulse. **Confidence is no longer a dash style**: it moves to the
   panel and the table.
5. **Traffic** is SMIL `animateMotion` dots, per direction, not CSS dashes: density and speed from the
   rate class. No rate, no particles. Motion is off under reduced motion, with the switch, or when hidden.
6. **Rack tree.** Linked devices are ordered gateway or router, then switches, then the rest, using the
   role and the `toward` of `uplinked_to`; unlinked devices keep their unit. The pure ordering is in `tree.ts`.
7. **Tokens.** 16 new tokens (ground, grid, rim, glass, vent, window, roof, tray, particle and seven speed
   classes) in `tokens.css`, both built-in themes, `contrast.json` and `docs/reference/themes.md`.
8. **Pure geometry** (routing, bundle fan-out, port shortening, speed class, label collision, rack tree,
   decor) is in modules of its own with table-driven tests.

## Amendment (2026-10-27): nothing written over anything

The first version left pills over racks at room level and put the Internet cloud and the far ends of
tunnels on the rack frame at rack level. Now:

- **Blocks are obstacles.** Label placement (`labels.ts`, `pillSlots`, `placePill`, `leaderBetween`) tries spots nearest to the default
  first (below, lower, sideways, then above), each with its caption and then without, and takes the
  first that touches neither a placed label nor any block's screen box (building, room, rack, device,
  gate). A pill moved away from its block keeps a thin leader line to it; a device pill that must stay
  at its unit loses its caption, then is hidden until pointed at.
- **External ends stand outside.** At rack level the Internet and the other places are drawn to the
  right of the device-card column, outside the rack's footprint, and their links are routed around the
  cards (`gateRoute` in `route.ts`).
- **Port badges** of one device side are spread so that they do not overlap (`badgeDistances`).
- **Proof.** `e2e/topology-labels.spec.ts` reads the boxes the browser draws at every level of the e2e
  inventory, in both themes, and fails if two labels intersect or a label covers a block.

## Amendment (2026-10-28): a rack's tree routes its lines round the cards

Production feedback on a busy rack (three switches, seven servers cabled to each, a bastion, two
gates): "c'est le bordel". Lines between the cards ran straight over the cards in their way, all the
links of a layer shared one 44 px channel, every line carried two port names, and a device cabled to
nothing but a gate had no card, so its link was an arc across the whole tree. Now (`tree.ts`,
`planTree`, `orderLayers`; `scene.ts`, `placeCards`, `cardEdges`):

- **Order.** Within a layer, a card moves to the mean position of its neighbours above, then below
  (one barycentre sweep each way), so that a tree's links cross less.
- **Rows.** A layer longer than six cards wraps onto rows of near equal length.
- **Channels and corridors.** A line between two rows runs along the channel between them, on a lane
  of its own; past rows in its way it runs down a corridor free of cards (between two of them, or
  beside the row), on a track of its own. A channel grows with its lanes, the gaps between cards with
  their tracks; a line that rises into a card moves aside when it would lie on one that drops out of
  the card above. Two cards side by side are joined straight across, two further apart over the top
  of their row. No line of the tree runs over a card or lies on another.
- **Gates.** A device linked to nothing but a gate (a bastion to the Internet) gets a card. A link to
  a gate leaves the card's east side when no card is right of it, otherwise its top, runs along the
  channel above its row to a lane of its own right of the tree, and down that lane to the gate.
- **Leaders.** A slab meets its card through a gutter left of the tree, then the card's west side,
  or, past a card on its left, along the top of its row.
- **Port names.** Past eight lines, a tree's port names wait until a line or a card is pointed at.
- **Proof.** `tree.test.ts` plans a three-switch, seven-server tree and fails if a line enters a card
  or lies on another; `rack-tree.test.ts` builds the scene of `testdata/busyRack.ts` (synthesised on
  the shape of the production rack) at all four rotations and fails if a sampled point of any line is
  inside a card, if a link is drawn as an arc, if the port names show on the dense tree, or if a
  device linked only to a gate has no card.

## Amendment (2026-10-29): visual language

A critical pass over every level in both themes found the scene geometrically sound but not a product:
- labels grew and shrank with the content (a lone device's name 40 px tall, a crowded floor's 6 px);
- the camera framed the floor, not what stands on it;
- every block was the same navy slab;
- health painted whole blocks yellow or pink;
- too many hues competed, and the lighting was flat;
- traffic dots read as beads, the gates were bare panels, the rack was a wireframe;
- the canvas was a 490 px strip above a legend of three paragraphs.

Now:

- **Text at one size on screen.** `buildScene(map, rot, { text })` sizes and places pills and badges at
  a text scale. `IsoScene` picks it from the screen scale (`textScale`, in `TEXT_STEPS`) and rebuilds
  the scene when a zoom crosses a step.
- **Framing.** The view frames the level's blocks (`Scene.core`), not the floor, with room for names
  and the floating controls. It never draws a level bigger than `FIT_MAX` (1.6) screen px per scene px.
- **Crowds.**
  - A device's pill only stands under its block, or one step lower (`near` in `labels.ts`).
  - Zoomed out, a healthy device under `LABEL_MIN_PX` on screen keeps its name until pointed at.
  - Past 12 lines at a level, port names wait until a line is pointed at.
- **Materials.**
  - The `iso-*` body tokens become a neutral product body: graphite in dark, off-white in light.
  - Speed lines form one cool family.
  - Faces get gradients (side faces darker towards the ground, a sheen on tops) and softer edges.
  - The contrast pairs of `contrast.json` are unchanged and still pass.
- **Health as a product shows it.**
  - A status LED on the front of every block. A rack keeps its door LEDs; a gate has none.
  - A soft pulsing glow on the ground under a critical one.
  - No colour across a block: a test reads `app.css` and fails if a health class fills or strokes a face.
- **Recognisable things.**
  - A device's front by role (`deviceFront` in `decor.ts`): ports, a screen, drive bays, outlets.
  - The rack is a cabinet: far panels, and blanking plates over its empty units.
  - The Internet and other places are round badges (a cloud, a site), drawn over the ends of their lines.
  - The slab-to-card leaders of the previous amendment are dropped: a card and its slab are one item, and pointing at either lights both.
- **Motion.**
  - Traffic is a soft streak running along each lane (`sheen` in `flow.ts`, an animated dash offset), at the old particles' rate and speed.
  - Going down a level flies in from where the place was on screen; going up backs out. The origin is set through the CSSOM, since the CSP forbids style attributes.
  - Pointing at a thing dims what it is not linked to.
  - Nothing moves under reduced motion.
- **Page.** The canvas takes the page's height, the controls and the hint float on it, and the legend is behind a disclosure.
- **Proof.**
  - Unit tests: `scale.test.ts`, `look.health.test.ts`, `objects.test.tsx`, and the sheen case of `flow.test.ts`.
  - E2E tests: `e2e/topology-scale.spec.ts` (a name keeps its size on screen across zooms; a lone device takes under a quarter of the stage), and in `e2e/topology-map.spec.ts` the flight between levels and the absence of dots.
  - The e2e tests fail on the previous build, and the unit tests fail without the code they cover.

Follow-ups in the same story:

- **Cards at one size.** The tree of a rack is laid out at the text scale, but only when the scale is under
  1. Zoomed in, a card keeps its design size on screen. Zoomed out, it shrinks with the scene, or a tree
  wider than the stage could never fit. The view frames the scene at the fitted text scale.
- **Arcs above walls.**
  - A room's or an area's lines start from the top of a mast above its walls.
  - An arc is raised, 16 px at a time and at most 240, until no point of it (but near its ends) lies inside the walls of a room it passes over.
  - `arcs.test.ts` checks this on four rooms in a row and on the recorded site, at every rotation.
- **Trunks.**
  - Three links or more from one card down to the rows below run as a trunk: one drop, one lane under the card, one track per corridor, one lane above each row served, then a drop into each card.
  - Each link stays its own line. Only links of one trunk lie on one another.
  - On three switches over seven servers, the channel under the switches holds three lanes instead of 21.

## Amendment (2026-10-30): the rack as it stands

The switch trunks of the previous follow-up still crossed one another in a meshed rack. This cannot be
helped in a drawing of nodes and links: three switches each cabled to three servers or more form
K3,3, which is not planar, so some lines cross whatever the order.

The tree of cards is therefore replaced by the rack as it stands (`elevation.ts`, `planElevation`):

- **Rows.** The linked devices, one row each beside the rack, highest unit first: unit, role icon,
  name, model and health. Unlinked devices stay in the drawn rack only.
- **Risers.**
  - Each link is owned by one end. A gate owns its links; otherwise it is the end with a hub's role
    (switch, patch panel, router, firewall), then the end with more links, then the higher one.
  - Each owner has a riser, a column beside the rows, from its own row to the farthest row it serves.
    The Internet and the other places have theirs right of the others, under their badge and name.
- **Dots.** A link is a dot on its owner's riser, at the other end's row.
  - Its colour is its speed. A red dot with a cross is a link that is down, and `xN` beside it is a bundle.
  - It is what one points at, which shows the ports and the traffic, and what one clicks, which opens the link.
- **Nothing crosses.**
  - Only owners have a stub, from their row to their riser.
  - A stub runs across a riser nearer the rows only if that riser spans the stub's row without a dot
    there. (A dot there is a junction: the device is cabled to that riser.)
  - The risers are ordered by a topological sort of these constraints, so a switch stack has its
    lowest switch nearest.
  - A cycle of hubs that each span the other's row is broken at its cheapest place and drawn as a hop.
- **Proof.**
  - `elevation.test.ts` checks the order, one dot per link on its owner's riser, the gates rightmost,
    zero crossings on three switches over seven servers, and exactly one counted hop for a cycle.
  - `rack-elevation.test.ts` checks the busy rack's scene at every rotation: no line across another,
    no line over a row, rows in unit order, the gates on top of their own risers.
  - `e2e/topology-look.spec.ts` checks r12's rows in unit order, the dots, and that pointing at a dot
    shows its ports and clicking it opens the link.

This supersedes decision 6 ("rack tree") and the tree parts of the amendments of 2026-10-28 and
2026-10-29: `tree.ts` and its tests are removed.

## Not drawn

No pixel test (the proof is the tests on the scene model plus screenshots reviewed by a person); no
route that avoids other lines crossing (but at the rack level, where nothing crosses); no per-port drawing on a rack front; no export of the map.

## Consequences

Room cables are no longer arcs, so tests of lanes and tunnel shifts moved to global-level edges
(`tunnel.test.tsx`, `layout.test.ts`). The e2e check of moving traffic counts `animateMotion` elements
instead of CSS animations. A 96-device rack stays under one second (devices have a flat shadow, no blur).
