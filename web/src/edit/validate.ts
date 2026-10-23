// What the page checks before it asks the server: a position inside the rack,
// on the units the schema allows, and the devices already there. Errors stop
// the preview; warnings are said and the person decides (two devices on the
// same U may be the truth on the floor, and the drawing flags it afterwards).
import { intervalsOverlap, type Face, type Mounted } from "../racks/layout";

export type PlacementError = "positionMissing" | "positionStep" | "belowRack" | "aboveRack" | "heightInvalid" | "heightStep";

export interface PlacementInput {
  /** The rack's height in U. */
  rackU: number;
  position: number | undefined;
  /** Height of the device in U (its model's, its own, or 1 when nobody says). */
  heightU: number;
  face: Face;
  fullDepth: boolean;
  /** The schema's position kind allows half units (mounted_in.position is a float). */
  halfUnits: boolean;
  /** Devices in the rack now, the one being placed left out. */
  others: readonly Mounted[];
}

export interface PlacementCheck {
  errors: PlacementError[];
  /** Devices that would share rack space with this one on the same face. */
  overlaps: Mounted[];
}

const EPS = 1e-9;
const onStep = (v: number, step: number) => Math.abs(v / step - Math.round(v / step)) < EPS;

export function checkPlacement(p: PlacementInput): PlacementCheck {
  const errors: PlacementError[] = [];
  const step = p.halfUnits ? 0.5 : 1;
  const heightOk = Number.isFinite(p.heightU) && p.heightU > 0;
  if (!heightOk) errors.push("heightInvalid");
  else if (!onStep(p.heightU, step)) errors.push("heightStep");
  if (p.position === undefined || !Number.isFinite(p.position)) {
    errors.push("positionMissing");
    return { errors, overlaps: [] };
  }
  if (!onStep(p.position, step)) errors.push("positionStep");
  if (p.position < 1) errors.push("belowRack");
  // The top unit occupied is position + height - 1: a 2 U device at U 41 fits a 42 U rack.
  if (heightOk && p.position + p.heightU - 1 > p.rackU + EPS) errors.push("aboveRack");
  const overlaps: Mounted[] = [];
  if (heightOk) {
    for (const o of p.others) {
      if (o.position === undefined || !Number.isFinite(o.position)) continue;
      const oh = o.heightU !== undefined && o.heightU > 0 ? o.heightU : 1;
      const oFace: Face = o.face ?? "front";
      // A full-depth device blocks both faces, whichever one it hangs from.
      const sameFace = p.fullDepth || o.fullDepth === true || oFace === p.face;
      if (sameFace && intervalsOverlap(p.position, p.heightU, o.position, oh)) overlaps.push(o);
    }
  }
  return { errors, overlaps };
}

/** The lowest free position for a device of `heightU` on `face`, or undefined when it does not fit. */
export function firstFit(rackU: number, heightU: number, face: Face, fullDepth: boolean, others: readonly Mounted[], halfUnits: boolean): number | undefined {
  const step = halfUnits ? 0.5 : 1;
  for (let u = 1; u + heightU - 1 <= rackU + EPS; u += step) {
    if (checkPlacement({ rackU, position: u, heightU, face, fullDepth, halfUnits, others }).overlaps.length === 0) return u;
  }
  return undefined;
}

/**
 * The free position nearest to `from` for a device of `heightU` on `face`, or
 * undefined when it fits nowhere. A tie goes to the lower unit (cables and
 * weight sit low). `from` itself is a candidate: when it is free it is the answer.
 */
export function nearestFit(
  rackU: number,
  heightU: number,
  face: Face,
  fullDepth: boolean,
  others: readonly Mounted[],
  halfUnits: boolean,
  from: number,
): number | undefined {
  const step = halfUnits ? 0.5 : 1;
  let best: number | undefined;
  for (let u = 1; u + heightU - 1 <= rackU + EPS; u += step) {
    if (checkPlacement({ rackU, position: u, heightU, face, fullDepth, halfUnits, others }).overlaps.length > 0) continue;
    if (best === undefined || Math.abs(u - from) < Math.abs(best - from) - EPS) best = u;
  }
  return best;
}

export type TileError = "tileMissing" | "tileWhole";

export interface TileCheck {
  errors: TileError[];
  /** Racks already standing on that tile. */
  taken: string[];
  /** The tile is outside the room's declared size. */
  outside: boolean;
}

/** A rack's floor tile: whole tiles from the room's north-west corner (ADR 0030). */
export function checkTile(
  x: number | undefined,
  y: number | undefined,
  racks: readonly { id: string; name: string; x?: number; y?: number }[],
  size?: { cols: number; rows: number },
  self?: string,
): TileCheck {
  if (x === undefined && y === undefined) return { errors: [], taken: [], outside: false };
  if (x === undefined || y === undefined) return { errors: ["tileMissing"], taken: [], outside: false };
  if (!Number.isInteger(x) || !Number.isInteger(y) || x < 0 || y < 0) return { errors: ["tileWhole"], taken: [], outside: false };
  const taken = racks.filter((r) => r.id !== self && r.x === x && r.y === y).map((r) => r.name);
  const outside = size !== undefined && (x >= size.cols || y >= size.rows);
  return { errors: [], taken, outside };
}

/** A number typed in a field, or undefined when the field is empty or not a number. */
export function parseNumber(s: string): number | undefined {
  const t = s.trim().replace(",", ".");
  if (t === "") return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : undefined;
}
