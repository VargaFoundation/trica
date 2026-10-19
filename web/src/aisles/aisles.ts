// Hot and cold aisles (ADR 0049), kept free of React and of the DOM so that it
// is tested on its own. A rack says which aisle its front faces
// (rack.front_aisle, cold or hot); the rear faces the other one. The server
// checks every mounted device against it (the rack contents' airflow, GET
// /objects/{id}/airflow); the page only says it in words and colours, and
// draws the aisles of a room from its racks.
import type { AirflowCheck } from "../api/types";
import type { Facing, PlacedRack } from "../racks/floor";
import type { Face } from "../racks/layout";

export type Aisle = "cold" | "hot";
export type AirflowStatus = "ok" | "mismatch" | "unknown";

export const AISLES: readonly Aisle[] = ["cold", "hot"];

/** A rack's front_aisle when it is one of the two known values. */
export function aisleOf(v: unknown): Aisle | undefined {
  return v === "cold" || v === "hot" ? v : undefined;
}

/** The aisle a face of the rack is on: the front's, or the other one for the rear. */
export function faceAisle(frontAisle: Aisle | undefined, face: Face): Aisle | undefined {
  if (!frontAisle) return undefined;
  if (face === "front") return frontAisle;
  return frontAisle === "cold" ? "hot" : "cold";
}

/** The status of a check, unknown when the server says something this page does not know. */
export function statusOf(c: AirflowCheck | undefined): AirflowStatus {
  return c?.status === "ok" || c?.status === "mismatch" ? c.status : "unknown";
}

export interface AirflowSummary {
  ok: number;
  mismatch: number;
  unknown: number;
  /** Devices that cannot be checked because the rack's aisle is not said. */
  noAisle: number;
}

/** The counts of a rack's devices by check status. */
export function summarize(checks: readonly (AirflowCheck | undefined)[]): AirflowSummary {
  const s: AirflowSummary = { ok: 0, mismatch: 0, unknown: 0, noAisle: 0 };
  for (const c of checks) {
    s[statusOf(c)]++;
    if (c?.reason === "aisle_not_set") s.noAisle++;
  }
  return s;
}

// ---------------------------------------------------------------- floor plan

/** The tile next to (x, y) in a direction of the plan (north is up, y grows southwards). */
export function neighbour(x: number, y: number, dir: Facing): { x: number; y: number } {
  switch (dir) {
    case "north":
      return { x, y: y - 1 };
    case "south":
      return { x, y: y + 1 };
    case "east":
      return { x: x + 1, y };
    case "west":
      return { x: x - 1, y };
  }
}

const OPPOSITE: Record<Facing, Facing> = { north: "south", south: "north", east: "west", west: "east" };

export interface AisleTile {
  x: number;
  y: number;
  /** The aisle the racks around say the tile is, or "conflict" when they disagree. */
  kind: Aisle | "conflict";
  /** The racks whose front or rear opens on the tile. */
  racks: string[];
}

/**
 * The aisles of a room, drawn from its racks: the tile in front of a rack that
 * faces a direction is its front aisle, the tile behind it the other aisle.
 * Two racks that open on the same tile and disagree make it a conflict: one
 * of them is turned the wrong way, or one of the declarations is wrong. Tiles
 * a rack stands on, and racks without a facing or an aisle, give nothing.
 */
export function aisleTiles(racks: readonly (PlacedRack & { frontAisle?: Aisle })[]): AisleTile[] {
  const taken = new Set(racks.map((r) => `${r.x},${r.y}`));
  const tiles = new Map<string, AisleTile>();
  const mark = (x: number, y: number, kind: Aisle, rack: string) => {
    if (x < 0 || y < 0) return;
    const key = `${x},${y}`;
    if (taken.has(key)) return;
    const t = tiles.get(key);
    if (!t) tiles.set(key, { x, y, kind, racks: [rack] });
    else {
      if (t.kind !== kind) t.kind = "conflict";
      if (!t.racks.includes(rack)) t.racks.push(rack);
    }
  };
  for (const r of racks) {
    if (!r.facing || !r.frontAisle) continue;
    const front = neighbour(r.x, r.y, r.facing);
    const back = neighbour(r.x, r.y, OPPOSITE[r.facing]);
    mark(front.x, front.y, r.frontAisle, r.id);
    mark(back.x, back.y, r.frontAisle === "cold" ? "hot" : "cold", r.id);
  }
  return [...tiles.values()].sort((a, b) => a.y - b.y || a.x - b.x);
}

/** The racks that open on a conflicting aisle tile, by id. */
export function conflictingRacks(tiles: readonly AisleTile[]): Set<string> {
  return new Set(tiles.filter((t) => t.kind === "conflict").flatMap((t) => t.racks));
}
