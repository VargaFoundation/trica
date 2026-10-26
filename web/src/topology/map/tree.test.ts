import { describe, expect, it } from "vitest";
import { layoutCards, linkedIds, rankTree, roleRank, type TreeEdge, type TreeNode } from "./tree";

const dev = (id: string, role: string | undefined, u: number, name = id): TreeNode => ({ id, kind: "device", role, u, name });
const edge = (a: string, b: string, extra: Partial<TreeEdge> = {}): TreeEdge => ({ a, b, medium: "physical", ...extra });

describe("rank by role", () => {
  it.each([
    [{ kind: "internet" }, 0],
    [{ kind: "portal" }, 0],
    [{ kind: "device", role: "firewall" }, 1],
    [{ kind: "device", role: "router" }, 1],
    [{ kind: "device", role: "switch" }, 2],
    [{ kind: "device", role: "server" }, 3],
    [{ kind: "device" }, 3],
  ])("%j is rank %d", (n, r) => expect(roleRank(n)).toBe(r));
});

describe("the tree of a rack", () => {
  const nodes = [dev("srv", "server", 10), dev("sw2", "switch", 30), dev("fw", "firewall", 40), dev("sw1", "switch", 35), dev("lone", "server", 5), dev("srv2", "server", 12)];
  const edges = [edge("fw", "sw1"), edge("fw", "sw2"), edge("sw1", "srv"), edge("sw2", "srv2")];

  it("puts the gateway on top, then the switches, then the rest, highest unit first", () => {
    expect(rankTree(nodes, edges)).toEqual([["fw"], ["sw1", "sw2"], ["srv2", "srv"]]);
  });
  it("leaves out a device nothing is linked to", () => {
    expect(rankTree(nodes, edges).flat()).not.toContain("lone");
    expect(linkedIds(nodes, edges).has("lone")).toBe(false);
  });
  it("is empty without a link, so that the rack keeps its order by unit", () => {
    expect(rankTree(nodes, [])).toEqual([]);
  });
  it("ignores an edge whose end is not in the rack", () => {
    expect(rankTree(nodes, [edge("fw", "elsewhere")])).toEqual([]);
  });
  it("puts a gate or the Internet above everything", () => {
    const net: TreeNode = { id: "internet", kind: "internet", name: "Internet" };
    expect(rankTree([...nodes, net], [...edges, edge("fw", "internet")])[0]).toEqual(["internet"]);
  });
  it("puts a switch under the one it is uplinked through", () => {
    const two = [dev("core", "switch", 30), dev("edge", "switch", 20)];
    // toward "a": the first end is the one the other reaches the network through.
    expect(rankTree(two, [edge("core", "edge", { medium: "uplinked_to", toward: "a" })])).toEqual([["core"], ["edge"]]);
    expect(rankTree(two, [edge("core", "edge", { medium: "uplinked_to", toward: "b" })])).toEqual([["edge"], ["core"]]);
  });
  it("follows a chain of uplinks and survives a loop", () => {
    const sws = [dev("a", "switch", 3), dev("b", "switch", 2), dev("c", "switch", 1)];
    const chain = [edge("a", "b", { medium: "uplinked_to", toward: "a" }), edge("b", "c", { medium: "uplinked_to", toward: "a" })];
    expect(rankTree(sws, chain)).toEqual([["a"], ["b"], ["c"]]);
    const loop = [...chain, edge("c", "a", { medium: "uplinked_to", toward: "a" })];
    expect(rankTree(sws, loop).flat().sort()).toEqual(["a", "b", "c"]);
  });
  it("orders a layer by name when the units are equal", () => {
    const two = [dev("z", "server", 4, "zeta"), dev("y", "server", 4, "alpha"), dev("sw", "switch", 9)];
    expect(rankTree(two, [edge("sw", "z"), edge("sw", "y")])[1]).toEqual(["y", "z"]);
  });
});

describe("card layout", () => {
  const size = { w: 100, h: 40, gapX: 10, gapY: 30, perRow: 3 };
  it("stacks the layers and centres each on the widest", () => {
    const { slots, w, h } = layoutCards([["fw"], ["a", "b", "c"], ["s1", "s2"]], size);
    expect(w).toBe(320);
    expect(h).toBe(40 * 3 + 30 * 2);
    expect(slots.get("fw")).toEqual({ x: 110, y: 0, layer: 0, row: 0 });
    expect(slots.get("a")).toEqual({ x: 0, y: 70, layer: 1, row: 0 });
    expect(slots.get("c")).toEqual({ x: 220, y: 70, layer: 1, row: 0 });
    expect(slots.get("s1")).toEqual({ x: 55, y: 140, layer: 2, row: 0 });
  });
  it("wraps a long layer onto a further row", () => {
    const { slots, h } = layoutCards([["a", "b", "c", "d"]], size);
    expect(slots.get("d")).toEqual({ x: 110, y: 55, layer: 0, row: 1 });
    expect(h).toBe(40 + 15 + 40);
  });
  it("is empty for no layer", () => {
    expect(layoutCards([], size)).toEqual({ slots: new Map(), w: 0, h: 0 });
  });
});
