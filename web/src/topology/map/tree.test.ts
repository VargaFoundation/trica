import { describe, expect, it } from "vitest";
import { linkedIds, orderLayers, planTree, rankTree, roleRank, type TreeEdge, type TreeNode } from "./tree";

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

type Pt = { x: number; y: number };
const SIZE = { w: 100, h: 40, gapX: 30, gapY: 30, perRow: 3, track: 8 };
const box = (c: { x: number; y: number }) => ({ x0: c.x, y0: c.y, x1: c.x + SIZE.w, y1: c.y + SIZE.h });
/** Whether an axis-aligned segment enters the inside of a box (running along its edge does not). */
const enters = (a: Pt, b: Pt, r: { x0: number; y0: number; x1: number; y1: number }) =>
  Math.max(a.x, b.x) > r.x0 + 0.5 && Math.min(a.x, b.x) < r.x1 - 0.5 && Math.max(a.y, b.y) > r.y0 + 0.5 && Math.min(a.y, b.y) < r.y1 - 0.5;
const segments = (pts: Pt[]) => pts.slice(1).map((b, i) => [pts[i] as Pt, b] as const);
/** Two segments of different lines that lie on one another over more than a point. */
const overlap = ([a, b]: readonly [Pt, Pt], [c, d]: readonly [Pt, Pt]) => {
  if (a.x === b.x && c.x === d.x && Math.abs(a.x - c.x) < 1) return Math.min(Math.max(a.y, b.y), Math.max(c.y, d.y)) - Math.max(Math.min(a.y, b.y), Math.min(c.y, d.y)) > 1;
  if (a.y === b.y && c.y === d.y && Math.abs(a.y - c.y) < 1) return Math.min(Math.max(a.x, b.x), Math.max(c.x, d.x)) - Math.max(Math.min(a.x, b.x), Math.min(c.x, d.x)) > 1;
  return false;
};

describe("the order of a layer", () => {
  it("moves a node under the mean of its neighbours above", () => {
    // s1 hangs from c, s3 from a: they swap so that their links do not cross.
    expect(orderLayers([["a", "b", "c"], ["s1", "s2", "s3"]], [edge("c", "s1"), edge("b", "s2"), edge("a", "s3")])).toEqual([["a", "b", "c"], ["s3", "s2", "s1"]]);
  });
  it("keeps the order of nodes it has nothing to go on for", () => {
    expect(orderLayers([["a", "b"], ["x", "y"]], [])).toEqual([["a", "b"], ["x", "y"]]);
  });
});

describe("the plan of a tree", () => {
  // Three switches over seven servers, each server cabled to all three: the shape that tangled a real rack.
  const servers = ["s1", "s2", "s3", "s4", "s5", "s6", "s7"];
  const layers = [["fw"], ["m", "l1", "l2"], servers];
  const links = [
    { key: "fw-m", a: "fw", b: "m" },
    { key: "m-l1", a: "m", b: "l1" },
    { key: "m-l2", a: "m", b: "l2" },
    { key: "l1-l2", a: "l1", b: "l2" },
    { key: "fw-s7", a: "fw", b: "s7" },
    ...servers.flatMap((sv) => ["m", "l1", "l2"].map((sw) => ({ key: `${sv}-${sw}`, a: sv, b: sw }))),
  ];
  const gates = [
    { key: "fw-net", card: "fw", gate: 0 },
    { key: "s5-far", card: "s5", gate: 1 },
  ];
  const plan = planTree(layers, links, gates, SIZE);

  it("places every card, the layers top first, a long layer on rows of near equal length", () => {
    expect(plan.cards.size).toBe(11);
    const rowOf = (id: string) => plan.cards.get(id)?.row;
    expect(rowOf("fw")).toBe(0);
    expect(rowOf("m")).toBe(1);
    expect(servers.map(rowOf)).toEqual([2, 2, 2, 3, 3, 4, 4]);
    expect(plan.cards.get("fw")?.y).toBeLessThan(plan.cards.get("m")?.y ?? 0);
  });

  it("routes every link orthogonally from one card to the other", () => {
    expect(plan.routes.size).toBe(links.length);
    for (const l of links) {
      const pts = plan.routes.get(l.key) as Pt[];
      for (const [a, b] of segments(pts)) expect(a.x === b.x || a.y === b.y, `${l.key} is orthogonal`).toBe(true);
      const [ra, rb] = [box(plan.cards.get(l.a) as Pt), box(plan.cards.get(l.b) as Pt)];
      const onEdge = (p: Pt, r: typeof ra) => (p.x >= r.x0 - 0.01 && p.x <= r.x1 + 0.01 && (Math.abs(p.y - r.y0) < 0.01 || Math.abs(p.y - r.y1) < 0.01)) || (p.y >= r.y0 && p.y <= r.y1 && (Math.abs(p.x - r.x0) < 0.01 || Math.abs(p.x - r.x1) < 0.01));
      expect(onEdge(pts[0] as Pt, ra), `${l.key} leaves its first card`).toBe(true);
      expect(onEdge(pts[pts.length - 1] as Pt, rb), `${l.key} ends on its second card`).toBe(true);
    }
  });

  it("never runs a line over a card, even past the rows in its way", () => {
    const boxes = [...plan.cards.values()].map(box);
    const all = [...plan.routes.values(), ...[...plan.exits.values()].map((x) => x.pts)];
    for (const pts of all) for (const [a, b] of segments(pts)) for (const r of boxes) expect(enters(a, b, r)).toBe(false);
  });

  it("never lays two lines on one another", () => {
    const all = [...plan.routes.entries(), ...[...plan.exits.entries()].map(([k, x]) => [k, x.pts] as const)];
    const hits: string[] = [];
    all.forEach(([ka, pa], i) => {
      for (const [kb, pb] of all.slice(i + 1)) for (const sa of segments(pa)) for (const sb of segments(pb)) if (overlap(sa, sb)) hits.push(`${ka} / ${kb}`);
    });
    expect(hits).toEqual([]);
  });

  it("widens a channel to hold its lines, one lane each", () => {
    const thin = planTree([["a"], ["b"]], [{ key: "ab", a: "a", b: "b" }], [], SIZE);
    const gap = (p: typeof plan, up: string, low: string) => (p.cards.get(low)?.y ?? 0) - ((p.cards.get(up)?.y ?? 0) + SIZE.h);
    expect(gap(thin, "a", "b")).toBe(SIZE.gapY);
    expect(gap(plan, "m", "s1")).toBeGreaterThan(SIZE.gapY);
  });

  it("takes the gate links out to lanes of their own, right of every card", () => {
    const right = Math.max(...[...plan.cards.values()].map((c) => c.x + SIZE.w));
    const lanes = [...plan.exits.values()].map((x) => x.laneX);
    expect(lanes).toHaveLength(2);
    expect(new Set(lanes).size).toBe(2);
    for (const x of lanes) expect(x).toBeGreaterThan(right);
    expect(plan.laneRight).toBe(Math.max(...lanes));
    for (const x of plan.exits.values()) expect((x.pts[x.pts.length - 1] as Pt).x).toBe(x.laneX);
  });

  it("is empty for no layer", () => {
    const p = planTree([], [], [], SIZE);
    expect(p.cards.size).toBe(0);
    expect(p.routes.size).toBe(0);
    expect([p.w, p.h, p.laneRight]).toEqual([0, 0, 0]);
  });
});
