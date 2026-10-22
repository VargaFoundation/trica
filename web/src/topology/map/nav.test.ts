import { describe, expect, it } from "vitest";
import { nextSpot, type Spot } from "./nav";

const spots: Spot[] = [
  { id: "c", at: { x: 0, y: 0 } },
  { id: "e", at: { x: 100, y: 10 } },
  { id: "far-e", at: { x: 300, y: 0 } },
  { id: "w", at: { x: -80, y: -20 } },
  { id: "n", at: { x: 5, y: -90 } },
  { id: "s", at: { x: -10, y: 70 } },
  { id: "diag", at: { x: 60, y: 90 } },
];

describe("arrow navigation", () => {
  it.each([
    ["ArrowRight", "e"],
    ["ArrowLeft", "w"],
    ["ArrowUp", "n"],
    ["ArrowDown", "s"],
  ] as const)("%s goes to the nearest place in its cone", (key, id) => {
    expect(nextSpot(spots, "c", key)).toBe(id);
  });

  it("does not jump to a place outside the 90-degree cone", () => {
    expect(nextSpot([{ id: "c", at: { x: 0, y: 0 } }, { id: "d", at: { x: 10, y: 100 } }], "c", "ArrowRight")).toBeUndefined();
  });

  it("stays where it is at the edge of the scene", () => {
    expect(nextSpot(spots, "far-e", "ArrowRight")).toBeUndefined();
  });

  it("starts from the first place when the current one is gone", () => {
    expect(nextSpot(spots, "gone", "ArrowRight")).toBe("c");
    expect(nextSpot([], "x", "ArrowRight")).toBeUndefined();
  });
});
