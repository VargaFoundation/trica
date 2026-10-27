import { describe, expect, it } from "vitest";
import { badgeDistances, firstFree, hitsAny, leaderBetween, merge, overlaps, placePill, pillSlots, type PillSpec } from "./labels";

const R = (x0: number, y0: number, x1: number, y1: number) => ({ x0, y0, x1, y1 });

describe("label collisions", () => {
  it.each([
    [R(0, 0, 10, 10), R(20, 0, 30, 10), false],
    [R(0, 0, 10, 10), R(5, 5, 15, 15), true],
    [R(0, 0, 10, 10), R(11, 0, 20, 10), true], // closer than the 2 px padding
    [R(0, 0, 10, 10), R(12, 0, 20, 10), false],
    [R(0, 0, 10, 10), R(0, 30, 10, 40), false],
  ])("%j and %j overlap: %s", (a, b, hit) => expect(overlaps(a, b)).toBe(hit));

  it("takes the first candidate that is clear of everything taken", () => {
    const taken = [R(0, 0, 100, 20)];
    expect(firstFree(taken, [R(10, 5, 60, 25), R(10, 38, 60, 58), R(10, 76, 60, 96)])).toBe(1);
  });
  it("takes the first one when nothing is taken, and none when every one collides", () => {
    expect(firstFree([], [R(0, 0, 5, 5)])).toBe(0);
    expect(firstFree([R(0, 0, 100, 100)], [R(10, 10, 20, 20), R(30, 30, 40, 40)])).toBe(-1);
  });
});

const spec = (over: Partial<PillSpec> = {}): PillSpec => ({
  at: { x: 100, y: 100 },
  start: false,
  top: 40,
  size: (sub) => ({ w: 60, h: sub ? 30 : 20 }),
  hasSub: true,
  beside: false,
  ...over,
});

describe("obstacle-aware pills", () => {
  it("hitsAny and merge", () => {
    expect(hitsAny(R(0, 0, 10, 10), [R(50, 50, 60, 60), R(5, 5, 8, 8)])).toBe(true);
    expect(hitsAny(R(0, 0, 10, 10), [])).toBe(false);
    expect(merge(R(0, 0, 10, 10), R(5, -5, 20, 8))).toEqual(R(0, -5, 20, 10));
  });

  it("offers the default spot first, with its caption, then without", () => {
    const [a, b] = pillSlots(spec());
    expect([a!.dx, a!.dy, a!.withSub, a!.rect.x0, a!.rect.y0]).toEqual([0, 0, true, 70, 100]);
    expect([b!.dx, b!.dy, b!.withSub]).toEqual([0, 0, false]);
  });

  it("a pill that stays beside its block has no other spot than its own", () => {
    expect(new Set(pillSlots(spec({ beside: true })).map((s) => `${s.dx},${s.dy}`))).toEqual(new Set(["0,0"]));
  });

  it("a pill written from its start grows rightwards", () => {
    expect(pillSlots(spec({ start: true }))[0]!.rect.x0).toBe(100);
  });

  it.each([
    ["nothing in the way: the default", [], [], 0, 0],
    ["a block under the default: one step lower", [], [R(60, 90, 140, 130)], 0, 24],
    ["a pill taken there too: not the default", [R(60, 90, 140, 400)], [], 1, 1],
  ])("%s", (_n, taken, obstacles, dx, dy) => {
    const s = placePill(spec(), taken, obstacles)!;
    expect(s).toBeDefined();
    expect(hitsAny(s.rect, [...taken, ...obstacles], 3)).toBe(false);
    if (dy === 0 && dx === 0) expect([s.dx, s.dy]).toEqual([0, 0]);
    else expect(s.dx !== 0 || s.dy !== 0 || s.above).toBe(true);
  });

  it("goes above the block when nothing below is free, and gives up when nothing is", () => {
    const below = R(-1000, 95, 1000, 400);
    const s = placePill(spec(), [], [below])!;
    expect(s.above).toBe(true);
    expect(s.rect.y1).toBeLessThanOrEqual(40);
    expect(placePill(spec(), [], [R(-1000, -1000, 1000, 1000)])).toBeUndefined();
  });

  it("the leader leaves a point of the block for the nearest edge of the pill", () => {
    expect(leaderBetween({ x: 50, y: 0 }, R(40, 20, 100, 40))).toEqual({ x1: 50, y1: 0, x2: 50, y2: 20 });
    expect(leaderBetween({ x: 0, y: 100 }, R(40, 20, 100, 40))).toEqual({ x1: 0, y1: 100, x2: 50, y2: 40 });
    expect(leaderBetween({ x: 0, y: 30 }, R(40, 20, 100, 40))).toEqual({ x1: 0, y1: 30, x2: 40, y2: 30 });
  });

  it.each([
    [22, 70, [22, 44, 66]],
    [22, 10, [22]],
    [10, 40, [10, 32]],
  ])("badge distances from %d up to %d", (first, limit, want) => expect(badgeDistances(first, limit)).toEqual(want));
});
