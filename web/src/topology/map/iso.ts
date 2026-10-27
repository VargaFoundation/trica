// Isometric (2:1 dimetric) geometry of the map. The world is a flat plane of
// cells (x east, y south, z up); a quarter-turn rotates it around its centre.
// Everything here is pure arithmetic so that it is tested without a DOM.

/** Half the width of a cell's diamond, in px. */
export const CELL = 30;
/** Height of one z unit, in px. */
export const ZUNIT = 22;

export type Rot = 0 | 1 | 2 | 3;
export interface Pt {
  x: number;
  y: number;
}
export interface World {
  w: number;
  d: number;
}
/** A box standing on the floor: footprint (x, y, w, d) in cells, base z, height h. */
export interface Box {
  x: number;
  y: number;
  w: number;
  d: number;
  z: number;
  h: number;
}

/** The world's size once turned. */
export function turnedWorld(world: World, rot: Rot): World {
  return rot % 2 === 0 ? world : { w: world.d, d: world.w };
}

/** A point of the plane after `rot` quarter turns. */
export function turnPoint(p: Pt, world: World, rot: Rot): Pt {
  switch (rot) {
    case 1:
      return { x: world.d - p.y, y: p.x };
    case 2:
      return { x: world.w - p.x, y: world.d - p.y };
    case 3:
      return { x: p.y, y: world.w - p.x };
    default:
      return p;
  }
}

/** A footprint after `rot` quarter turns: still an axis-aligned rectangle. */
export function turnRect(b: Pick<Box, "x" | "y" | "w" | "d">, world: World, rot: Rot) {
  const a = turnPoint({ x: b.x, y: b.y }, world, rot);
  const c = turnPoint({ x: b.x + b.w, y: b.y + b.d }, world, rot);
  return { x: Math.min(a.x, c.x), y: Math.min(a.y, c.y), w: Math.abs(c.x - a.x), d: Math.abs(c.y - a.y) };
}

/** Screen position of a turned world point at height z. */
export function project(x: number, y: number, z: number): Pt {
  return { x: (x - y) * CELL, y: ((x + y) * CELL) / 2 - z * ZUNIT };
}

const pts = (...p: Pt[]) => p.map((q) => `${round(q.x)},${round(q.y)}`).join(" ");
const round = (n: number) => Math.round(n * 10) / 10;

export interface IsoBox {
  /** Polygon points of the three visible faces. */
  top: string;
  south: string;
  east: string;
  /** The footprint on the floor, for the shadow. */
  foot: string;
  /** Screen centre of the top face and of the base footprint's centre. */
  anchor: Pt;
  base: Pt;
  /** Painter's order key: larger is nearer the viewer. */
  depth: number;
  /** The silhouette of the three faces, for the outline of a hovered or selected block. */
  hull: string;
  /** Screen bounds of the drawn box. */
  bounds: { x0: number; y0: number; x1: number; y1: number };
}

/** The three visible faces of a box seen from the south-east, after `rot` quarter turns. */
export function isoBox(box: Box, world: World, rot: Rot): IsoBox {
  return projectBox({ ...box, ...turnRect(box, world, rot) });
}

/** The same for a box whose footprint is already in turned coordinates. */
export function projectBox(box: Box): IsoBox {
  const { x, y, w, d } = box;
  const z0 = box.z;
  const z1 = box.z + box.h;
  const top = [project(x, y, z1), project(x + w, y, z1), project(x + w, y + d, z1), project(x, y + d, z1)];
  const south = [project(x, y + d, z1), project(x + w, y + d, z1), project(x + w, y + d, z0), project(x, y + d, z0)];
  const east = [project(x + w, y, z1), project(x + w, y + d, z1), project(x + w, y + d, z0), project(x + w, y, z0)];
  const all = [...top, ...south, ...east];
  return {
    top: pts(...top),
    south: pts(...south),
    east: pts(...east),
    hull: pts(project(x, y, z1), project(x + w, y, z1), project(x + w, y, z0), project(x + w, y + d, z0), project(x, y + d, z0), project(x, y + d, z1)),
    foot: pts(project(x, y, z0), project(x + w, y, z0), project(x + w, y + d, z0), project(x, y + d, z0)),
    anchor: project(x + w / 2, y + d / 2, z1),
    base: project(x + w / 2, y + d / 2, z0),
    depth: x + w + (y + d) + z0 * 0.01,
    bounds: {
      x0: Math.min(...all.map((p) => p.x)),
      y0: Math.min(...all.map((p) => p.y)),
      x1: Math.max(...all.map((p) => p.x)),
      y1: Math.max(...all.map((p) => p.y)),
    },
  };
}

export interface Bounds {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

/** The union of bounds, grown by a margin; a 0-size box when there is nothing. */
export function union(list: readonly Bounds[], margin = 0): Bounds {
  if (list.length === 0) return { x0: 0, y0: 0, x1: 0, y1: 0 };
  return {
    x0: Math.min(...list.map((b) => b.x0)) - margin,
    y0: Math.min(...list.map((b) => b.y0)) - margin,
    x1: Math.max(...list.map((b) => b.x1)) + margin,
    y1: Math.max(...list.map((b) => b.y1)) + margin,
  };
}

/** The viewBox string that frames bounds. */
export function viewBoxOf(b: Bounds): string {
  return `${round(b.x0)} ${round(b.y0)} ${round(b.x1 - b.x0)} ${round(b.y1 - b.y0)}`;
}

/** Polygon points ("x,y x,y") moved by (dx, dy). */
export function shiftPoints(points: string, dx: number, dy: number): string {
  return points
    .split(" ")
    .filter((s) => s !== "")
    .map((s) => {
      const [x, y] = s.split(",").map(Number) as [number, number];
      return `${round(x + dx)},${round(y + dy)}`;
    })
    .join(" ");
}

/** A projected box moved on the screen by (dx, dy): every polygon, anchor and bound, not its painter's order. */
export function shiftIso(b: IsoBox, dx: number, dy: number): IsoBox {
  return {
    ...b,
    top: shiftPoints(b.top, dx, dy),
    south: shiftPoints(b.south, dx, dy),
    east: shiftPoints(b.east, dx, dy),
    foot: shiftPoints(b.foot, dx, dy),
    hull: shiftPoints(b.hull, dx, dy),
    anchor: { x: b.anchor.x + dx, y: b.anchor.y + dy },
    base: { x: b.base.x + dx, y: b.base.y + dy },
    bounds: { x0: b.bounds.x0 + dx, y0: b.bounds.y0 + dy, x1: b.bounds.x1 + dx, y1: b.bounds.y1 + dy },
  };
}

/** The moves that stack boxes in one column: every left edge at `x`, the first top at `y`, `gap` px between one and the next. */
export function columnShifts(boxes: readonly Bounds[], x: number, y: number, gap: number): Pt[] {
  let top = y;
  return boxes.map((b) => {
    const at = { x: x - b.x0, y: top - b.y0 };
    top += b.y1 - b.y0 + gap;
    return at;
  });
}
