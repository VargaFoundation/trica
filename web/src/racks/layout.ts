import type { AirflowCheck } from "../api/types";
import type { Health as SharedHealth } from "../inventory/health";

// Rack elevation arithmetic, kept free of React and of the DOM so that it is
// tested on its own. Conventions come from the schema (internal/schema/
// builtin.yaml): U 1 is the bottom of the rack; a device is mounted with
// `mounted_in` (position = the lowest U it occupies, face = front | rear);
// its height is the u_height of its device type, and a full_depth device
// occupies both faces whichever one it is mounted from. Mounted from the rear
// means the device's front faces the rear of the rack (the usual rack-elevation convention, and
// the airflow check's, ADR 0049): a switch whose ports face the hot aisle is
// mounted from the rear, and the rear elevation shows its ports.

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
  /**
   * The model occupies no rack unit (a desktop NAS on a shelf, ADR 0068): heightU is the
   * shelf it stands on, and its face is drawn at its own proportions inside it.
   */
  desktop?: boolean;
  typeName?: string;
  /** The device's role (server, switch...): what a face without an image is drawn as. */
  role?: string;
  /** The device's own health. */
  health?: string;
  /** The worst health of its parts (power supplies, fans, disks) the caller may read. */
  partsHealth?: string;
  /** API paths of the model's face images, when the device-type library has them. */
  frontImage?: string;
  rearImage?: string;
  /** The library is not sure which chassis the device is and nobody chose: the object page offers the choice (ADR 0048). */
  variantUncertain?: boolean;
  /** The airflow check against the rack's aisles, as the server computed it (ADR 0049). */
  airflow?: AirflowCheck;
}

/** The health a rack face shows: the shared scale without "absent" (a mounted device is there). */
export type Health = Exclude<SharedHealth, "absent">;

const HEALTH_RANK: Record<string, number> = { ok: 1, warning: 2, critical: 3 };

/**
 * What a device's face says about its health: the worst of its own and its
 * parts'. "unknown" when neither says ok, warning or critical: a device nobody
 * reports on is not shown as healthy.
 */
export function overallHealth(m: Pick<Mounted, "health" | "partsHealth">): Health {
  let worst: Health = "unknown";
  for (const h of [m.health, m.partsHealth]) {
    if (h && (HEALTH_RANK[h] ?? 0) > (HEALTH_RANK[worst] ?? 0)) worst = h as Health;
  }
  return worst;
}

/**
 * The image a slot is drawn with, if the model has one: the device's front on
 * the face it is mounted from, its rear on the other face of a full-depth
 * device, none for a shallow device seen from behind (only its outline).
 */
export function slotImage(s: Pick<Slot, "item" | "shadow" | "behind">): string | undefined {
  if (s.behind) return undefined;
  return s.shadow ? s.item.rearImage : s.item.frontImage;
}

/**
 * Where an image of bw x bh is drawn inside a box of w x h so that it keeps its
 * proportions ("contain"): centred, as large as the box allows. A desktop device
 * is not stretched over the shelf it stands on (ADR 0068).
 */
export function containBox(w: number, h: number, bw: number, bh: number): { x: number; y: number; width: number; height: number } {
  if (!(bw > 0) || !(bh > 0)) return { x: 0, y: 0, width: w, height: h };
  const k = Math.min(w / bw, h / bh);
  const width = bw * k;
  const height = bh * k;
  return { x: (w - width) / 2, y: (h - height) / 2, width, height };
}

export type Issue = "overlap" | "outOfRange" | "assumedHeight" | "assumedFace";

export interface Slot {
  item: Mounted;
  face: Face;
  /** True on the face the device is not mounted from. */
  shadow: boolean;
  /**
   * A shallow (not full-depth) device seen from the face it is not mounted
   * from: drawn as an outline, so that the unit reads as used from behind; it
   * overlaps nothing and leaves the unit free on this face.
   */
  behind?: boolean;
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
    for (const face of ["front", "rear"] as const) {
      const shadow = face !== mountedFace;
      out[face].push({ item, face, shadow, behind: shadow && !item.fullDepth, bottom, height, top, issues: shadow && !item.fullDepth ? [] : [...issues], overlaps: [] });
    }
  }
  for (const face of ["front", "rear"] as const) {
    // Behind slots first, so that the devices of this face are drawn over them.
    out[face].sort((a, b) => Number(!!b.behind) - Number(!!a.behind) || a.bottom - b.bottom || a.item.name.localeCompare(b.item.name));
    const slots = out[face].filter((s) => !s.behind);
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
    if (!slots.some((s) => !s.behind && intervalsOverlap(u, 1, s.bottom, s.height))) free++;
  }
  return free;
}

/** The other face of a rack. */
export const otherFace = (f: Face): Face => (f === "front" ? "rear" : "front");
