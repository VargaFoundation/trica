// Floor plan arithmetic: where racks stand in a room. The schema gives each
// rack floor_x / floor_y ("tiles from the room origin") and a facing, and the
// room width_mm / depth_mm; it does not say how big a tile is, so the UI uses
// the usual data-centre floor tile (see TILE_MM).

export type Facing = "north" | "east" | "south" | "west";
export const FACINGS: readonly Facing[] = ["north", "east", "south", "west"];

/** A 600 mm floor tile. Not in the schema: a finding is filed for it. */
export const TILE_MM = 600;

export interface FloorRack {
  id: string;
  name: string;
  x?: number;
  y?: number;
  facing?: Facing;
}

export interface PlacedRack extends FloorRack {
  x: number;
  y: number;
  /** Another rack stands on the same tile. */
  clash: boolean;
}

export interface FloorPlan {
  cols: number;
  rows: number;
  placed: PlacedRack[];
  unplaced: FloorRack[];
  /** The room has no width or depth: the grid is sized to fit the racks. */
  sizeFromRacks: boolean;
  /** Ids of racks standing outside the room's declared size. */
  outside: string[];
}

const whole = (n: unknown): n is number => typeof n === "number" && Number.isInteger(n) && n >= 0;

export function layoutFloor(widthMm: number | undefined, depthMm: number | undefined, racks: readonly FloorRack[]): FloorPlan {
  const placed: PlacedRack[] = [];
  const unplaced: FloorRack[] = [];
  for (const r of racks) {
    if (whole(r.x) && whole(r.y)) placed.push({ ...r, x: r.x, y: r.y, clash: false });
    else unplaced.push(r);
  }
  const seen = new Map<string, PlacedRack>();
  for (const r of placed) {
    const key = `${r.x},${r.y}`;
    const other = seen.get(key);
    if (other) {
      other.clash = true;
      r.clash = true;
    } else seen.set(key, r);
  }
  const known = typeof widthMm === "number" && widthMm > 0 && typeof depthMm === "number" && depthMm > 0;
  const cols = known ? Math.ceil(widthMm / TILE_MM) : Math.max(1, ...placed.map((r) => r.x + 1));
  const rows = known ? Math.ceil(depthMm / TILE_MM) : Math.max(1, ...placed.map((r) => r.y + 1));
  const outside = known ? placed.filter((r) => r.x >= cols || r.y >= rows).map((r) => r.id) : [];
  placed.sort((a, b) => a.y - b.y || a.x - b.x || a.name.localeCompare(b.name));
  return { cols, rows, placed, unplaced, sizeFromRacks: !known, outside };
}
