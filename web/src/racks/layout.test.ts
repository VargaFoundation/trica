import { DEFAULT_TILE_MM, layoutFloor, tileOf } from "./floor";
import { formatRange, freeUnits, intervalsOverlap, layoutRack, type Mounted } from "./layout";

const dev = (id: string, position: number | undefined, heightU: number | undefined, extra: Partial<Mounted> = {}): Mounted => ({
  id,
  name: id,
  position,
  heightU,
  face: "front",
  ...extra,
});

describe("layoutRack", () => {
  it("places a device from its lowest U and its height", () => {
    const l = layoutRack(42, [dev("sw-a1", 10, 2)]);
    expect(l.front).toHaveLength(1);
    expect(l.front[0]).toMatchObject({ bottom: 10, height: 2, top: 11, shadow: false, issues: [], overlaps: [] });
    expect(l.rear).toHaveLength(0);
  });

  it("keeps the two faces apart: devices on opposite faces at the same U do not overlap", () => {
    const l = layoutRack(42, [dev("a", 5, 1, { face: "front" }), dev("b", 5, 1, { face: "rear" })]);
    expect(l.front[0]?.issues).toEqual([]);
    expect(l.rear[0]?.issues).toEqual([]);
  });

  it("a full-depth device occupies both faces, and the other face is a shadow", () => {
    const l = layoutRack(42, [dev("srv", 3, 2, { fullDepth: true, face: "rear" })]);
    expect(l.front[0]).toMatchObject({ shadow: true, bottom: 3, height: 2 });
    expect(l.rear[0]).toMatchObject({ shadow: false });
  });

  it("a half-depth device facing a full-depth one at the same U overlaps it", () => {
    const l = layoutRack(42, [dev("srv", 3, 2, { fullDepth: true, face: "front" }), dev("pdu", 4, 1, { face: "rear" })]);
    expect(l.rear.find((s) => s.item.id === "pdu")?.issues).toContain("overlap");
    expect(l.rear.find((s) => s.item.id === "srv")?.overlaps).toEqual(["pdu"]);
  });

  it("flags overlapping devices on both sides, naming the other", () => {
    const l = layoutRack(42, [dev("a", 10, 2), dev("b", 11, 1)]);
    const a = l.front.find((s) => s.item.id === "a");
    const b = l.front.find((s) => s.item.id === "b");
    expect(a?.issues).toContain("overlap");
    expect(b?.issues).toContain("overlap");
    expect(a?.overlaps).toEqual(["b"]);
    expect(b?.overlaps).toEqual(["a"]);
  });

  it("devices that touch do not overlap", () => {
    const l = layoutRack(42, [dev("a", 10, 2), dev("b", 12, 1), dev("c", 9, 1)]);
    for (const s of l.front) expect(s.issues, s.item.id).toEqual([]);
  });

  it("an overlap hidden behind a tall device is still found", () => {
    const l = layoutRack(42, [dev("tall", 1, 10), dev("x", 3, 1), dev("y", 8, 1)]);
    expect(l.front.find((s) => s.item.id === "x")?.overlaps).toEqual(["tall"]);
    expect(l.front.find((s) => s.item.id === "y")?.overlaps).toEqual(["tall"]);
    expect(l.front.find((s) => s.item.id === "tall")?.overlaps.sort()).toEqual(["x", "y"]);
  });

  it("flags a device that sticks out of the top or starts below U1", () => {
    const l = layoutRack(12, [dev("top", 12, 2), dev("fits", 11, 2), dev("below", 0, 1)]);
    expect(l.front.find((s) => s.item.id === "top")?.issues).toContain("outOfRange");
    expect(l.front.find((s) => s.item.id === "fits")?.issues).not.toContain("outOfRange");
    expect(l.front.find((s) => s.item.id === "below")?.issues).toContain("outOfRange");
  });

  it("lists devices without a position apart and draws nothing for them", () => {
    const l = layoutRack(42, [dev("nowhere", undefined, 1), dev("here", 1, 1)]);
    expect(l.unplaced.map((m) => m.id)).toEqual(["nowhere"]);
    expect(l.front.map((s) => s.item.id)).toEqual(["here"]);
  });

  it("draws an unknown height as 1 U and says so; an unknown face goes to the front and says so", () => {
    const l = layoutRack(42, [dev("mystery", 7, undefined, { face: undefined })]);
    expect(l.front[0]).toMatchObject({ height: 1, top: 7 });
    expect(l.front[0]?.issues).toEqual(expect.arrayContaining(["assumedHeight", "assumedFace"]));
  });

  it("a zero or negative height is not a height", () => {
    expect(layoutRack(42, [dev("z", 1, 0)]).front[0]?.issues).toContain("assumedHeight");
    expect(layoutRack(42, [dev("n", 1, -2)]).front[0]?.issues).toContain("assumedHeight");
  });

  it("half units: 0.5 U devices side by side do not overlap", () => {
    const l = layoutRack(42, [dev("a", 4, 0.5), dev("b", 4.5, 0.5)]);
    for (const s of l.front) expect(s.issues, s.item.id).toEqual([]);
    expect(intervalsOverlap(4, 0.5, 4.5, 0.5)).toBe(false);
    expect(intervalsOverlap(4, 1, 4.5, 0.5)).toBe(true);
  });
});

describe("helpers", () => {
  it("formatRange", () => {
    expect(formatRange(10, 1)).toBe("U10");
    expect(formatRange(10, 2)).toBe("U10-U11");
    expect(formatRange(1, 4)).toBe("U1-U4");
  });

  it("freeUnits counts units no device touches, once", () => {
    const l = layoutRack(10, [dev("a", 1, 2), dev("b", 2, 2), dev("c", 9, 2)]);
    // a: U1-2, b: U2-3 (overlap), c: U9-10 (sticks out nowhere: 10 is the top)
    expect(freeUnits(10, l.front)).toBe(10 - 5);
    expect(freeUnits(10, [])).toBe(10);
  });
});

describe("layoutFloor", () => {
  it("sizes the grid from the room in 600 mm tiles, rounding up", () => {
    expect(DEFAULT_TILE_MM).toBe(600);
    const p = layoutFloor(6000, 3100, []);
    expect(p).toMatchObject({ cols: 10, rows: 6, sizeFromRacks: false });
  });

  it("sizes the grid in the room's own tile, and places racks in it", () => {
    const p = layoutFloor(6000, 3100, [{ id: "a", name: "a", x: 15, y: 10 }], 300);
    expect(p).toMatchObject({ cols: 20, rows: 11, outside: [] });
    // The same rack is outside a room counted in 600 mm tiles.
    expect(layoutFloor(6000, 3100, [{ id: "a", name: "a", x: 15, y: 10 }]).outside).toEqual(["a"]);
  });

  it("falls back to the default tile when tile_mm is missing or unusable", () => {
    expect(tileOf(300)).toBe(300);
    for (const v of [undefined, null, 0, -600, 300.5, "300"]) expect(tileOf(v)).toBe(DEFAULT_TILE_MM);
  });

  it("fits the grid to the racks when the room has no size", () => {
    const p = layoutFloor(undefined, undefined, [{ id: "a", name: "a", x: 3, y: 1 }]);
    expect(p).toMatchObject({ cols: 4, rows: 2, sizeFromRacks: true });
  });

  it("separates racks without coordinates, and flags two racks on the same tile", () => {
    const p = layoutFloor(3000, 3000, [
      { id: "a", name: "a", x: 1, y: 1 },
      { id: "b", name: "b", x: 1, y: 1 },
      { id: "c", name: "c", x: 2, y: 0 },
      { id: "d", name: "d" },
      { id: "e", name: "e", x: 2 },
    ]);
    expect(p.unplaced.map((r) => r.id)).toEqual(["d", "e"]);
    expect(p.placed.find((r) => r.id === "a")?.clash).toBe(true);
    expect(p.placed.find((r) => r.id === "b")?.clash).toBe(true);
    expect(p.placed.find((r) => r.id === "c")?.clash).toBe(false);
  });

  it("lists racks outside the declared size", () => {
    const p = layoutFloor(1200, 1200, [
      { id: "in", name: "in", x: 1, y: 1 },
      { id: "right", name: "right", x: 2, y: 0 },
      { id: "below", name: "below", x: 0, y: 2 },
    ]);
    expect(p.outside.sort()).toEqual(["below", "right"]);
  });

  it("orders placed racks row by row", () => {
    const p = layoutFloor(3000, 3000, [
      { id: "b", name: "b", x: 0, y: 1 },
      { id: "a", name: "a", x: 2, y: 0 },
      { id: "c", name: "c", x: 0, y: 0 },
    ]);
    expect(p.placed.map((r) => r.id)).toEqual(["c", "a", "b"]);
  });
});
