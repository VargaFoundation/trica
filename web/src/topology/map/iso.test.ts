import { describe, expect, it } from "vitest";
import { CELL, columnShifts, isoBox, project, shiftIso, shiftPoints, turnedWorld, turnPoint, turnRect, union, ZUNIT, type Rot } from "./iso";

const world = { w: 10, d: 6 };
const ROTS: Rot[] = [0, 1, 2, 3];

describe("isometric geometry", () => {
  it("projects the origin to the origin and rises with z", () => {
    expect(project(0, 0, 0)).toEqual({ x: 0, y: 0 });
    expect(project(0, 0, 2)).toEqual({ x: 0, y: -2 * ZUNIT });
  });

  it("sends east down-right and south down-left, two to one", () => {
    expect(project(1, 0, 0)).toEqual({ x: CELL, y: CELL / 2 });
    expect(project(0, 1, 0)).toEqual({ x: -CELL, y: CELL / 2 });
  });

  it("turns a point back to itself after four quarter turns", () => {
    let p = { x: 3, y: 2 };
    let w = world;
    for (let i = 0; i < 4; i++) {
      p = turnPoint(p, w, 1);
      w = turnedWorld(w, 1);
    }
    expect(p).toEqual({ x: 3, y: 2 });
    expect(w).toEqual(world);
  });

  it("keeps a turned point inside the turned world", () => {
    for (const r of ROTS) {
      const tw = turnedWorld(world, r);
      for (const p of [{ x: 0, y: 0 }, { x: 10, y: 6 }, { x: 4, y: 5 }]) {
        const q = turnPoint(p, world, r);
        expect(q.x).toBeGreaterThanOrEqual(0);
        expect(q.y).toBeGreaterThanOrEqual(0);
        expect(q.x).toBeLessThanOrEqual(tw.w);
        expect(q.y).toBeLessThanOrEqual(tw.d);
      }
    }
  });

  it("turns a footprint into a footprint of the same area, with width and depth swapped on odd turns", () => {
    const b = { x: 2, y: 1, w: 3, d: 2 };
    for (const r of ROTS) {
      const t = turnRect(b, world, r);
      expect(t.w * t.d).toBe(6);
      expect(r % 2 === 0 ? [t.w, t.d] : [t.d, t.w]).toEqual([3, 2]);
    }
  });

  it("draws three faces and paints what is nearer later", () => {
    const far = isoBox({ x: 0, y: 0, w: 1, d: 1, z: 0, h: 1 }, world, 0);
    const near = isoBox({ x: 8, y: 4, w: 1, d: 1, z: 0, h: 1 }, world, 0);
    expect(far.top.split(" ")).toHaveLength(4);
    expect(far.south.split(" ")).toHaveLength(4);
    expect(far.east.split(" ")).toHaveLength(4);
    expect(near.depth).toBeGreaterThan(far.depth);
  });

  it("orders by depth differently once the scene is turned", () => {
    const a = { x: 0, y: 0, w: 1, d: 1, z: 0, h: 1 };
    const b = { x: 8, y: 4, w: 1, d: 1, z: 0, h: 1 };
    expect(isoBox(b, world, 0).depth).toBeGreaterThan(isoBox(a, world, 0).depth);
    expect(isoBox(a, world, 2).depth).toBeGreaterThan(isoBox(b, world, 2).depth);
  });

  it("frames a set of bounds with a margin, and nothing as nothing", () => {
    expect(union([{ x0: 0, y0: 0, x1: 10, y1: 5 }, { x0: -4, y0: 2, x1: 3, y1: 9 }], 1)).toEqual({ x0: -5, y0: -1, x1: 11, y1: 10 });
    expect(union([])).toEqual({ x0: 0, y0: 0, x1: 0, y1: 0 });
  });
});

describe("moving a projected box on the screen", () => {
  it.each([
    ["1,2 3.5,4", 10, -1, "11,1 13.5,3"],
    ["0,0", 0, 0, "0,0"],
    ["", 5, 5, ""],
  ])("shifts the points %j by (%d, %d)", (points, dx, dy, want) => expect(shiftPoints(points, dx, dy)).toBe(want));

  it("moves every polygon, anchor and bound by the same amount and keeps the painter's order", () => {
    const b = isoBox({ x: 1, y: 2, w: 1, d: 1, z: 0, h: 1 }, world, 0);
    const m = shiftIso(b, 100, -40);
    expect(m.bounds).toEqual({ x0: b.bounds.x0 + 100, y0: b.bounds.y0 - 40, x1: b.bounds.x1 + 100, y1: b.bounds.y1 - 40 });
    expect(m.anchor).toEqual({ x: b.anchor.x + 100, y: b.anchor.y - 40 });
    expect(m.base).toEqual({ x: b.base.x + 100, y: b.base.y - 40 });
    expect(m.depth).toBe(b.depth);
    // Every polygon still sits inside the moved bounds, and the opposite shift gives the polygons back.
    expect(shiftIso(m, -100, 40).hull).toBe(b.hull);
    for (const poly of [m.top, m.south, m.east, m.foot, m.hull]) {
      for (const pt of poly.split(" ")) {
        const [x, y] = pt.split(",").map(Number) as [number, number];
        expect(x).toBeGreaterThanOrEqual(m.bounds.x0 - 0.1);
        expect(x).toBeLessThanOrEqual(m.bounds.x1 + 0.1);
        expect(y).toBeGreaterThanOrEqual(m.bounds.y0 - 0.1);
        expect(y).toBeLessThanOrEqual(m.bounds.y1 + 0.1);
      }
    }
  });
});

describe("stacking boxes in a column", () => {
  it("puts every left edge on x and each box under the previous one, a gap apart", () => {
    const boxes = [
      { x0: 10, y0: 5, x1: 50, y1: 45 },
      { x0: -20, y0: 100, x1: 0, y1: 120 },
      { x0: 0, y0: 0, x1: 30, y1: 10 },
    ];
    const moves = columnShifts(boxes, 200, 0, 30);
    const moved = boxes.map((b, i) => ({ x0: b.x0 + (moves[i]?.x ?? 0), y0: b.y0 + (moves[i]?.y ?? 0), x1: b.x1 + (moves[i]?.x ?? 0), y1: b.y1 + (moves[i]?.y ?? 0) }));
    expect(moved.map((b) => b.x0)).toEqual([200, 200, 200]);
    expect(moved.map((b) => b.y0)).toEqual([0, 70, 120]);
    expect(moved[1]?.y1).toBe(90);
  });
  it("gives nothing for nothing", () => expect(columnShifts([], 0, 0, 10)).toEqual([]));
});
