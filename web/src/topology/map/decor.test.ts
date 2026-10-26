import { describe, expect, it } from "vitest";
import { doorGlass, faceOf, facePoly, facePt, floorMatrix, ledDots, ledStates, MAX_LEDS, portStrip, ventLines, wallBoxes, windows } from "./decor";
import { isoBox, project, projectBox } from "./iso";

const R = { x: 2, y: 3, w: 4, d: 2 };

describe("faces", () => {
  it("a whole face is the face isoBox draws", () => {
    const box = projectBox({ ...R, z: 0.5, h: 1.5 });
    expect(facePoly(R, "south", 0, 1, 0.5, 2)).toBe(box.south);
    expect(facePoly(R, "east", 0, 1, 0.5, 2)).toBe(box.east);
  });
  it("a point of a face lies on it", () => {
    expect(facePt(R, "south", 0.5, 1)).toEqual(project(4, 5, 1));
    expect(facePt(R, "east", 0.25, 0)).toEqual(project(6, 3.5, 0));
  });
  it("turned footprints match isoBox once turned", () => {
    expect(projectBox({ x: 0, y: 0, w: 1, d: 1, z: 0, h: 1 }).south).toBe(isoBox({ x: 0, y: 0, w: 1, d: 1, z: 0, h: 1 }, { w: 1, d: 1 }, 0).south);
  });
  it("the hull of a box has six corners", () => {
    expect(projectBox({ ...R, z: 0, h: 1 }).hull.split(" ")).toHaveLength(6);
  });
});

describe("windows", () => {
  it("fill both faces in rows and columns that follow the building", () => {
    const small = windows({ x: 0, y: 0, w: 1.2, d: 1.2 }, 0, 0.8);
    const big = windows({ x: 0, y: 0, w: 3.6, d: 3.6 }, 0, 2);
    expect(small.length).toBeGreaterThan(0);
    expect(big.length).toBeGreaterThan(small.length);
    // 3.6 / 0.6 = 6 columns on each of the two faces, (2 - .25) / .5 = 3 rows.
    expect(big).toHaveLength(6 * 3 * 2);
  });
  it("draws quadrilaterals", () => {
    for (const w of windows({ x: 0, y: 0, w: 2, d: 2 }, 0, 1.5)) expect(w.split(" ")).toHaveLength(4);
  });
});

describe("cabinet details", () => {
  it("the door glass is the face less a frame", () => {
    const door = doorGlass(R, "south", 0, 2.4);
    expect(door).not.toBe(facePoly(R, "south", 0, 1, 0, 2.4));
    expect(door.split(" ")).toHaveLength(4);
  });
  it("vent slits are n segments of one path", () => {
    expect(ventLines(R, "south", 0.2, 1, 4).match(/M/g)).toHaveLength(4);
    expect(ventLines(R, "east", 0.2, 1, 0)).toBe("");
  });
  it("LEDs are laid in rows and never more than the cap", () => {
    expect(ledDots(R, "south", 2, 3)).toHaveLength(3);
    const many = ledDots(R, "south", 2, 99);
    expect(many).toHaveLength(MAX_LEDS);
    // The second row sits lower than the first.
    expect((ledDots(R, "south", 2, 8)[6] as { y: number }).y).toBeGreaterThan((ledDots(R, "south", 2, 8)[0] as { y: number }).y);
    expect(ledDots(R, "south", 2, 0)).toEqual([]);
  });
  it("LED states put the worst first and keep every problem when the door is full", () => {
    expect(ledStates({ ok: 2, warning: 1, critical: 1, unknown: 1 })).toEqual(["critical", "warning", "unknown", "ok", "ok"]);
    const full = ledStates({ ok: 40, warning: 2, critical: 1, unknown: 0 });
    expect(full).toHaveLength(MAX_LEDS);
    expect(full.slice(0, 3)).toEqual(["critical", "warning", "warning"]);
    expect(ledStates({ ok: 0, warning: 0, critical: 0, unknown: 0 })).toEqual([]);
  });
  it("the face a facing vector shows", () => {
    expect(faceOf(undefined)).toBe("south");
    expect(faceOf({ x: 0, y: 1 })).toBe("south");
    expect(faceOf({ x: 1, y: 0 })).toBe("east");
    expect(faceOf({ x: 0, y: -1 })).toBeUndefined();
    expect(faceOf({ x: -1, y: 0 })).toBeUndefined();
  });
});

describe("walls", () => {
  const { back, front } = wallBoxes({ x: 0, y: 0, w: 6, d: 4 }, 0.5, 0.9, 0.3);
  it("have two far walls taller than the two near ones", () => {
    expect(back).toHaveLength(2);
    expect(front).toHaveLength(2);
    expect(Math.min(...back.map((b) => b.h))).toBeGreaterThan(Math.max(...front.map((b) => b.h)));
  });
  it("stand on the slab and stay inside its footprint", () => {
    for (const b of [...back, ...front]) {
      expect(b.z).toBe(0.5);
      expect(b.x).toBeGreaterThanOrEqual(0);
      expect(b.y).toBeGreaterThanOrEqual(0);
      expect(b.x + b.w).toBeLessThanOrEqual(6 + 1e-9);
      expect(b.y + b.d).toBeLessThanOrEqual(4 + 1e-9);
    }
  });
});

describe("text on the floor", () => {
  it("lies along the x axis of the floor, centred on the footprint", () => {
    const c = project(4, 4, 0.5);
    expect(floorMatrix({ x: 2, y: 2, w: 4, d: 4 }, 0.5, 1)).toBe(`matrix(1 0.5 -1 0.5 ${c.x} ${c.y})`);
  });
});

describe("the ports of a device's front", () => {
  it("are one closed square each, in one path", () => {
    expect(portStrip(R, 0, 0.3, 5).match(/M/g)).toHaveLength(5);
    expect(portStrip(R, 0, 0.3, 5).match(/Z/g)).toHaveLength(5);
    expect(portStrip(R, 0, 0.3, 0)).toBe("");
  });
});
