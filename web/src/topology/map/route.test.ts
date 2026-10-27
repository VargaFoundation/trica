import { describe, expect, it } from "vitest";
import { assignLanes, fanOffsets, gateRoute, offsetPolyline, orthogonalRoute, pointAt, pointAtDistance, polylineLength, quadAt, quadLength, roundedPath, simplify, trayLaneY, trayRect, trayRoute } from "./route";

const P = (x: number, y: number) => ({ x, y });

describe("simplify", () => {
  it("drops repeated points and points on a straight run", () => {
    expect(simplify([P(0, 0), P(0, 0), P(5, 0), P(10, 0), P(10, 10)])).toEqual([P(0, 0), P(10, 0), P(10, 10)]);
  });
});

describe("rounded corners", () => {
  it("rounds each interior corner with a quadratic curve", () => {
    expect(roundedPath([P(0, 0), P(20, 0), P(20, 20)], 5)).toBe("M0,0L15,0Q20,0 20,5L20,20");
  });
  it("keeps a straight line straight", () => {
    expect(roundedPath([P(0, 0), P(30, 0)], 8)).toBe("M0,0L30,0");
    expect(roundedPath([], 8)).toBe("");
  });
  it("shrinks the radius where a segment is short", () => {
    // Segments of 4 px cannot take a radius of 10: it is halved to 2.
    expect(roundedPath([P(0, 0), P(4, 0), P(4, 4)], 10)).toBe("M0,0L2,0Q4,0 4,2L4,4");
  });
});

describe("length and position along a line", () => {
  const pts = [P(0, 0), P(10, 0), P(10, 10)];
  it("measures the polyline", () => expect(polylineLength(pts)).toBe(20));
  it.each([
    [0, P(0, 0)],
    [5, P(5, 0)],
    [10, P(10, 0)],
    [15, P(10, 5)],
    [99, P(10, 10)],
    [-3, P(0, 0)],
  ])("at %d px is %j", (s, p) => expect(pointAtDistance(pts, s)).toEqual(p));
  it("finds a fraction of the length", () => expect(pointAt(pts, 0.5)).toEqual(P(10, 0)));
});

describe("parallel offsets", () => {
  it("moves a segment sideways, to the right of its direction (y down)", () => {
    expect(offsetPolyline([P(0, 0), P(10, 0)], 3)).toEqual([P(0, 3), P(10, 3)]);
    expect(offsetPolyline([P(0, 0), P(0, 10)], 3)).toEqual([P(-3, 0), P(-3, 10)]);
  });
  it("keeps the corners mitred", () => {
    expect(offsetPolyline([P(0, 0), P(10, 0), P(10, 10)], 2)).toEqual([P(0, 2), P(8, 2), P(8, 10)]);
  });
  it("does nothing for a zero offset", () => {
    expect(offsetPolyline([P(0, 0), P(10, 0)], 0)).toEqual([P(0, 0), P(10, 0)]);
  });
});

describe("the fan of lines on one side", () => {
  it.each([
    [0, 4, []],
    [1, 4, [0]],
    [2, 4, [-2, 2]],
    [3, 4, [-4, 0, 4]],
    [4, 6, [-9, -3, 3, 9]],
  ])("%d lines, %d px apart: %j", (n, gap, offs) => expect(fanOffsets(n, gap)).toEqual(offs));
});

describe("orthogonal routes", () => {
  it("leaves the bottom of one card and enters the top of another through a channel", () => {
    expect(orthogonalRoute(P(0, 0), "s", P(100, 100), "n", { stub: 10, mid: 50 })).toEqual([P(0, 0), P(0, 50), P(100, 50), P(100, 100)]);
  });
  it("takes the middle of the gap when no channel is given", () => {
    const r = orthogonalRoute(P(0, 0), "s", P(60, 100), "n");
    expect(r.map((p) => p.y)).toContain(50);
  });
  it("goes from a right side to a left side through a vertical channel", () => {
    expect(orthogonalRoute(P(0, 0), "e", P(100, 40), "w", { stub: 10, mid: 50 })).toEqual([P(0, 0), P(50, 0), P(50, 40), P(100, 40)]);
  });
  it("loops under two cards side by side when both leave by the bottom", () => {
    const r = orthogonalRoute(P(0, 0), "s", P(100, 0), "s", { stub: 10 });
    expect(r[0]).toEqual(P(0, 0));
    expect(r.at(-1)).toEqual(P(100, 0));
    expect(Math.max(...r.map((p) => p.y))).toBe(20);
  });
  it("has only right angles", () => {
    for (const [as, bs] of [["s", "n"], ["e", "w"], ["s", "w"], ["e", "n"], ["s", "s"]] as const) {
      const r = orthogonalRoute(P(10, 10), as, P(90, 70), bs);
      for (let i = 1; i < r.length; i++) expect(r[i]?.x === r[i - 1]?.x || r[i]?.y === r[i - 1]?.y).toBe(true);
    }
  });
});

describe("cable tray", () => {
  it("runs up to the tray, along it and down to the other end", () => {
    expect(trayRoute(P(2, 5), P(8, 6), 1)).toEqual([P(2, 5), P(2, 1), P(8, 1), P(8, 6)]);
  });
  it("gives overlapping spans their own lanes and reuses a lane once it is free", () => {
    expect(assignLanes([{ from: 0, to: 4 }, { from: 2, to: 6 }, { from: 5, to: 8 }, { from: 9, to: 3 }])).toEqual([0, 1, 0, 2]);
    expect(assignLanes([])).toEqual([]);
  });
  it("puts lane k further from the racks, and sizes the tray to its lanes", () => {
    expect(trayLaneY(2, 0)).toBeCloseTo(1.55);
    expect(trayLaneY(2, 2)).toBeLessThan(trayLaneY(2, 1));
    const one = trayRect(0, 10, 2, 1);
    const three = trayRect(0, 10, 2, 3);
    expect(three.d).toBeGreaterThan(one.d);
    expect(one.x).toBeLessThan(0);
    // The lanes sit inside the tray.
    expect(trayLaneY(2, 0)).toBeLessThan(three.y + three.d);
    expect(trayLaneY(2, 2)).toBeGreaterThan(three.y);
  });
});

describe("quadratic curve", () => {
  it("is at its ends at 0 and 1 and at the middle of the polygon's weighted centre at 0.5", () => {
    expect(quadAt(P(0, 0), P(5, 10), P(10, 0), 0)).toEqual(P(0, 0));
    expect(quadAt(P(0, 0), P(5, 10), P(10, 0), 1)).toEqual(P(10, 0));
    expect(quadAt(P(0, 0), P(5, 10), P(10, 0), 0.5)).toEqual(P(5, 5));
  });
  it("is longer than its chord and shorter than its control polygon", () => {
    const l = quadLength(P(0, 0), P(5, 10), P(10, 0));
    expect(l).toBeGreaterThan(10);
    expect(l).toBeLessThan(2 * Math.hypot(5, 10));
  });
});

describe("the route from a card to a gate on its right", () => {
  const orthogonal = (pts: { x: number; y: number }[]) => pts.slice(1).every((p, i) => p.x === pts[i]?.x || p.y === pts[i]?.y);
  it.each([
    ["east side", "e" as const, P(100, 50), P(300, 120), { laneX: 250 }, [P(100, 50), P(250, 50), P(250, 120), P(300, 120)]],
    ["east side, gate at the same height", "e" as const, P(100, 50), P(300, 50), { laneX: 250 }, [P(100, 50), P(300, 50)]],
    ["top, into the channel above its row", "n" as const, P(100, 50), P(300, 20), { laneX: 250, chanY: 30 }, [P(100, 50), P(100, 30), P(250, 30), P(250, 20), P(300, 20)]],
    ["a lane left of the card is pushed past its stub", "e" as const, P(100, 50), P(300, 80), { laneX: 90 }, [P(100, 50), P(112, 50), P(112, 80), P(300, 80)]],
  ])("%s", (_, side, a, g, opts, want) => {
    const got = gateRoute(a, side, g, opts);
    expect(got).toEqual(want);
    expect(orthogonal(got)).toBe(true);
    expect(got[0]).toEqual(a);
    expect(got[got.length - 1]).toEqual(g);
  });
});
