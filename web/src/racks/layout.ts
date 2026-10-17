// Rack elevation arithmetic, kept free of React and of the DOM so that it is
// tested on its own. Conventions come from the schema (internal/schema/
// builtin.yaml): U 1 is the bottom of the rack; a device is mounted with
// `mounted_in` (position = the lowest U it occupies, face = front | rear);
// its height is the u_height of its device type, and a full_depth device
// occupies both faces whichever one it is mounted from.

export type Face = "front" | "rear";

/** What the page learned about one device mounted in the rack. */
export interface Mounted {
  id: string;
  name: string;
  /** Lowest U occupied (1 = bottom); absent when the relation has no position. */
  position?: number;
  /** Absent when the relation has no face: the device is drawn on the front and flagged. */
  face?: Face;
  /** Height in U; absent when the device type is unknown: 1 U is drawn and flagged. */
  heightU?: number;
  fullDepth?: boolean;
  typeName?: string;
}

export type Issue = "overlap" | "outOfRange" | "assumedHeight" | "assumedFace";

export interface Slot {
  item: Mounted;
  face: Face;
  /** True on the face the device is not mounted from (full-depth devices only). */
  shadow: boolean;
  bottom: number;
  height: number;
  /** Highest U occupied (inclusive). */
  top: number;
  issues: Issue[];
  /** Ids of the devices this one overlaps on this face. */
  overlaps: string[];
}

export interface RackLayout {
  uHeight: number;
  front: Slot[];
  rear: Slot[];
  /** Devices mounted in the rack without a position: listed, not drawn. */
  unplaced: Mounted[];
}

export const DEFAULT_HEIGHT_U = 1;

/** The height drawn for a rack whose u_height nobody recorded. */
export const DEFAULT_RACK_U = 42;

const finite = (n: number | undefined): n is number => typeof n === "number" && Number.isFinite(n);

/** Whether [a, a+ha) and [b, b+hb) share any rack space. A tiny tolerance keeps 0.5 U arithmetic exact. */
export function intervalsOverlap(a: number, ha: number, b: number, hb: number): boolean {
  const eps = 1e-9;
  return a < b + hb - eps && b < a + ha - eps;
}

export function layoutRack(uHeight: number, items: readonly Mounted[]): RackLayout {
  const out: RackLayout = { uHeight, front: [], rear: [], unplaced: [] };
  for (const item of items) {
    if (!finite(item.position)) {
      out.unplaced.push(item);
      continue;
    }
    const issues: Issue[] = [];
    const known = finite(item.heightU) && item.heightU > 0;
    const height = known ? (item.heightU as number) : DEFAULT_HEIGHT_U;
    if (!known) issues.push("assumedHeight");
    const mountedFace: Face = item.face ?? "front";
    if (!item.face) issues.push("assumedFace");
    const bottom = item.position;
    const top = bottom + height - 1;
    if (bottom < 1 || top > uHeight + 1e-9) issues.push("outOfRange");
    const faces: Face[] = item.fullDepth ? ["front", "rear"] : [mountedFace];
    for (const face of faces) {
      out[face].push({ item, face, shadow: face !== mountedFace, bottom, height, top, issues: [...issues], overlaps: [] });
    }
  }
  for (const face of ["front", "rear"] as const) {
    const slots = out[face].sort((a, b) => a.bottom - b.bottom || a.item.name.localeCompare(b.item.name));
    for (let i = 0; i < slots.length; i++) {
      const a = slots[i] as Slot;
      for (let j = i + 1; j < slots.length; j++) {
        const b = slots[j] as Slot;
        if (b.bottom >= a.bottom + a.height) break; // sorted by bottom: nothing further can touch a
        if (!intervalsOverlap(a.bottom, a.height, b.bottom, b.height)) continue;
        for (const [x, y] of [[a, b], [b, a]] as const) {
          x.overlaps.push(y.item.id);
          if (!x.issues.includes("overlap")) x.issues.push("overlap");
        }
      }
    }
  }
  return out;
}

/** "U12" or "U12-U13" (inclusive range), with halves kept when the height is fractional. */
export function formatRange(bottom: number, height: number): string {
  const n = (v: number) => String(Math.round(v * 100) / 100);
  const top = bottom + height - 1;
  return top > bottom ? `U${n(bottom)}-U${n(top)}` : `U${n(bottom)}`;
}

/** U units free on a face (a unit counts as free when no slot covers any part of it). Overlaps are not counted twice. */
export function freeUnits(uHeight: number, slots: readonly Slot[]): number {
  let free = 0;
  for (let u = 1; u <= uHeight; u++) {
    if (!slots.some((s) => intervalsOverlap(u, 1, s.bottom, s.height))) free++;
  }
  return free;
}
