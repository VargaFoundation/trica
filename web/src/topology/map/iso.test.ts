import { describe, expect, it } from "vitest";
import { CELL, isoBox, project, turnedWorld, turnPoint, turnRect, union, ZUNIT, type Rot } from "./iso";

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
