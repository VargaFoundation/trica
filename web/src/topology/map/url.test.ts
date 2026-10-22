import { describe, expect, it } from "vitest";
import { mapSearch, parseAddress } from "./url";

const parse = (q: string) => parseAddress(new URLSearchParams(q));

describe("the address of the topology page", () => {
  it("opens the map on the world by default", () => {
    expect(parse("")).toEqual({ view: "map", level: "global", id: undefined, sel: undefined, rot: 0, invalid: false });
  });

  it.each([["site=paris"], ["rack=r12"], ["vlan=10"], ["classes=cable"]])("keeps opening the network view for the old address ?%s", (q) => {
    expect(parse(q).view).toBe("network");
  });

  it("lets an explicit view win over the old parameters", () => {
    expect(parse("view=map&site=paris").view).toBe("map");
    expect(parse("view=network").view).toBe("network");
    expect(parse("view=nonsense").view).toBe("map");
  });

  it("reads the level, the place, the selection and the turn", () => {
    expect(parse("level=rack&id=r1&sel=d1&rot=3")).toMatchObject({ level: "rack", id: "r1", sel: "d1", rot: 3, invalid: false });
    expect(parse("level=unplaced")).toMatchObject({ level: "unplaced", id: undefined, invalid: false });
  });

  it("falls back to the world, and says so, when the level or the place is unusable", () => {
    expect(parse("level=moon")).toMatchObject({ level: "global", invalid: true });
    expect(parse("level=site")).toMatchObject({ level: "global", invalid: true });
    expect(parse("level=room&id=a%20b")).toMatchObject({ level: "global", invalid: true });
  });

  it("ignores a turn that is not a quarter turn", () => {
    expect(parse("rot=7").rot).toBe(0);
    expect(parse("rot=x").rot).toBe(0);
    expect(parse("rot=-1").rot).toBe(0);
  });

  it("writes the shortest address that says the same", () => {
    expect(mapSearch({ level: "global", rot: 0 })).toBe("");
    expect(mapSearch({ level: "site", id: "s1", rot: 0 })).toBe("level=site&id=s1");
    expect(mapSearch({ level: "rack", id: "r1", sel: "d1", rot: 2 })).toBe("level=rack&id=r1&sel=d1&rot=2");
    // A place means nothing to the levels that have none.
    expect(mapSearch({ level: "unplaced", id: "x", rot: 0 })).toBe("level=unplaced");
  });

  it("reads back what it wrote", () => {
    const a = { level: "room" as const, id: "<room>", sel: "<r1>", rot: 1 as const };
    expect(parse(mapSearch(a))).toMatchObject(a);
  });
});
