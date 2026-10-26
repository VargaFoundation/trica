import { describe, expect, it } from "vitest";
import { firstFree, overlaps } from "./labels";

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
