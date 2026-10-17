import type { Mounted } from "../racks/layout";
import { physicalSchema, wholeUnitSchema } from "./fixtures";
import { editRules } from "./rules";
import { checkPlacement, checkTile, firstFit, parseNumber } from "./validate";

const m = (id: string, position: number, heightU: number, face: "front" | "rear", fullDepth = false): Mounted => ({ id, name: id, position, heightU, face, fullDepth });
const base = { rackU: 12, heightU: 1, face: "front" as const, fullDepth: false, halfUnits: true, others: [] as Mounted[] };

describe("checkPlacement", () => {
  it.each([
    ["no position", { position: undefined }, ["positionMissing"]],
    ["U 0", { position: 0 }, ["belowRack"]],
    ["top unit of a 12 U rack", { position: 12 }, []],
    ["a 2 U device at the top", { position: 12, heightU: 2 }, ["aboveRack"]],
    ["a 2 U device just fitting", { position: 11, heightU: 2 }, []],
    ["a half unit where halves are allowed", { position: 3.5 }, []],
    ["a half unit where they are not", { position: 3.5, halfUnits: false }, ["positionStep"]],
    ["a quarter unit", { position: 3.25 }, ["positionStep"]],
    ["a height of zero", { position: 3, heightU: 0 }, ["heightInvalid"]],
    ["a half-unit height on whole units", { position: 3, heightU: 1.5, halfUnits: false }, ["heightStep"]],
  ])("%s", (_, input, errors) => {
    expect(checkPlacement({ ...base, ...input }).errors).toEqual(errors);
  });

  it("warns about devices sharing rack space on the same face only", () => {
    const others = [m("a", 5, 2, "front"), m("b", 5, 1, "rear"), m("c", 8, 1, "front")];
    expect(checkPlacement({ ...base, position: 6, others }).overlaps.map((o) => o.id)).toEqual(["a"]);
    expect(checkPlacement({ ...base, position: 7, others }).overlaps).toEqual([]);
    expect(checkPlacement({ ...base, position: 5, face: "rear", others }).overlaps.map((o) => o.id)).toEqual(["b"]);
  });

  it("counts a full-depth device on both faces, whether it is the one placed or one already there", () => {
    const others = [m("deep", 3, 2, "rear", true)];
    expect(checkPlacement({ ...base, position: 4, others }).overlaps.map((o) => o.id)).toEqual(["deep"]);
    expect(checkPlacement({ ...base, position: 1, face: "front", fullDepth: true, others: [m("r", 1, 1, "rear")] }).overlaps.map((o) => o.id)).toEqual(["r"]);
  });

  it("does not count half a unit apart as an overlap", () => {
    expect(checkPlacement({ ...base, position: 5.5, heightU: 0.5, others: [m("a", 5, 0.5, "front")] }).overlaps).toEqual([]);
  });
});

describe("firstFit", () => {
  it("finds the lowest free position for the height, on the face", () => {
    const others = [m("a", 1, 2, "front"), m("b", 4, 1, "front")];
    expect(firstFit(12, 1, "front", false, others, false)).toBe(3);
    expect(firstFit(12, 2, "front", false, others, false)).toBe(5);
    expect(firstFit(12, 1, "rear", false, others, false)).toBe(1);
    expect(firstFit(2, 3, "front", false, [], false)).toBeUndefined();
  });
});

describe("checkTile", () => {
  const racks = [
    { id: "r1", name: "r12", x: 1, y: 1 },
    { id: "r2", name: "r13" },
  ];
  it("accepts no tile, or a whole tile; refuses half of one", () => {
    expect(checkTile(undefined, undefined, racks).errors).toEqual([]);
    expect(checkTile(0, 0, racks).errors).toEqual([]);
    expect(checkTile(1, undefined, racks).errors).toEqual(["tileMissing"]);
    expect(checkTile(1.5, 0, racks).errors).toEqual(["tileWhole"]);
    expect(checkTile(-1, 0, racks).errors).toEqual(["tileWhole"]);
  });
  it("says which rack stands there already, except the rack itself", () => {
    expect(checkTile(1, 1, racks).taken).toEqual(["r12"]);
    expect(checkTile(1, 1, racks, undefined, "r1").taken).toEqual([]);
  });
  it("says when the tile is outside the room", () => {
    expect(checkTile(5, 0, racks, { cols: 5, rows: 3 }).outside).toBe(true);
    expect(checkTile(4, 2, racks, { cols: 5, rows: 3 }).outside).toBe(false);
  });
});

describe("parseNumber", () => {
  it("reads a decimal comma too, and nothing from an empty or wrong field", () => {
    expect(parseNumber(" 12,5 ")).toBe(12.5);
    expect(parseNumber("")).toBeUndefined();
    expect(parseNumber("abc")).toBeUndefined();
  });
});

describe("editRules: read from the schema, not from the code", () => {
  it("takes faces, half units, roles and the device height from the registry", () => {
    const r = editRules(physicalSchema);
    expect(r.faces).toEqual(["front", "rear"]);
    expect(r.halfUnits).toBe(true);
    expect(r.canMount).toBe(true);
    expect(r.roles).toContain("patch_panel");
    expect(r.deviceHeight).toBe(true);
    expect(r.isIdentity("site", "code")).toBe(true);
    expect(r.isIdentity("rack", "u_height")).toBe(false);
  });
  it("follows an overlay: whole units when position is an int, nothing when the relation is missing", () => {
    expect(editRules(wholeUnitSchema).halfUnits).toBe(false);
    const none = editRules({ ...physicalSchema, relations: [] });
    expect(none.faces).toEqual([]);
    expect(none.canMount).toBe(false);
    expect(editRules(undefined).roles).toEqual([]);
  });
});
