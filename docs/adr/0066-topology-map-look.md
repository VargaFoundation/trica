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

## Not drawn

No pixel test (the proof is the tests on the scene model plus screenshots reviewed by a person); no
route that avoids other lines crossing; no per-port drawing on a rack front; no export of the map.

## Consequences

Room cables are no longer arcs, so tests of lanes and tunnel shifts moved to global-level edges
(`tunnel.test.tsx`, `layout.test.ts`). The e2e check of moving traffic counts `animateMotion` elements
instead of CSS animations. A 96-device rack stays under one second (devices have a flat shadow, no blur).
