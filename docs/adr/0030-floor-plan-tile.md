# ADR-0030: A room says the size of its floor tile; floor positions count from its north-west corner

- **Status**: accepted (2026-10)
- **Concerns**: `internal/schema/builtin.yaml` (`room`, `rack`), `web/src/racks`, `docs/reference/ui.md`.
  Builds on ADR 0003 (schema registry) and ADR 0018 (web UI).

## Context

A rack has `floor_x` and `floor_y`, counted in tiles, and a room has `width_mm` and `depth_mm`.
Nothing said how big a tile is, nor where the counting starts. The floor plan assumed a 600 mm
tile and a top-left origin in one UI constant (issue #43): a site with 300 mm or one-foot tiles
got a wrong plan, with racks flagged outside a room they stand in, and no sign in the data.

## Decision

- `room` gets `tile_mm` (int, mm): the side of one floor tile. When it is not set, 600 mm, the
  usual raised-floor tile. It is an ordinary attribute, with provenance and history like any
  other, so a declaration file or a correction can set it per room.
- `floor_x` counts tiles **east** and `floor_y` tiles **south** of the room's **north-west
  corner**; the floor plan draws north up. The descriptions in the schema say so, which is what
  the API, the MCP `schema` tool and the UI show.
- The UI reads `tile_mm` and falls back to 600 mm only when it is missing or not a positive
  integer; the caption of the plan says which tile it used.

## Consequences

- Adding an attribute to a built-in type is additive: existing objects simply have no value,
  and the default applies. An overlay that already defines `room.tile_mm` with another kind is
  refused at merge, as any kind change is (ADR 0003); with the same kind it merges.
- Rooms that are not rectangles, or tiles that are not square, are not described. Racks placed
  between tiles are not either: positions are whole tiles.
