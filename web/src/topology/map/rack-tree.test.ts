import { describe, expect, it } from "vitest";
import { buildScene, type Scene } from "./scene";
import { BUSY_RACK } from "./testdata/busyRack";
import { MAPS } from "./testdata/maps";

// The tree of cards of a rack's linked devices, on a busy rack (three switches, seven servers cabled to
// each, a bastion, a gate link from a server): the lines go round the cards, not over them.
const inside = (p: { x: number; y: number }, c: { x: number; y: number; w: number; h: number }, m = 1.5) => p.x > c.x + m && p.x < c.x + c.w - m && p.y > c.y + m && p.y < c.y + c.h - m;

/** The ids of the cards a line of the tree runs over, sampled every 2 px. */
function overCards(s: Scene): string[] {
  const cards = s.items.flatMap((i) => (i.card ? [{ id: i.id, c: i.card }] : []));
  const out: string[] = [];
  for (const e of s.edges) {
    for (let d = 0; d <= e.len; d += 2) {
      const p = e.at(d);
      for (const { id, c } of cards) if (inside(p, c)) out.push(`${e.edge.id} over ${id}`);
    }
  }
  return [...new Set(out)];
}

describe("the tree of a busy rack", () => {
  for (const rot of [0, 1, 2, 3] as const) {
    it(`never runs a line over a card (rotation ${rot})`, () => {
      const s = buildScene(BUSY_RACK, rot);
      expect(s.items.filter((i) => i.card)).toHaveLength(12);
      expect(overCards(s)).toEqual([]);
    });
  }

  it("draws every link of the tree as a routed line, none as an arc across it", () => {
    const s = buildScene(BUSY_RACK, 0);
    expect(s.edges).toHaveLength(BUSY_RACK.edges.length);
    for (const e of s.edges) expect(e.d, e.edge.id).not.toMatch(/Q[^L]*$/); // an arc is one quadratic curve; a route ends on a straight run
  });

  it("keeps the port names of a dense tree until a line or a card is pointed at, and shows them on a small one", () => {
    const busy = buildScene(BUSY_RACK, 0).edges.filter((e) => e.ports.a || e.ports.b);
    expect(busy.length).toBeGreaterThan(0);
    expect(busy.every((e) => !e.portsShown)).toBe(true);
    const small = buildScene(MAPS.rack, 0).edges.filter((e) => e.ports.a && e.len >= 150);
    expect(small.some((e) => e.portsShown)).toBe(true);
  });

  it("gives a card to a device cabled to nothing but a gate, and routes its link to the gate", () => {
    const s = buildScene(MAPS.rack, 0);
    for (const id of ["<fwa>", "<swa>"]) expect(s.items.find((i) => i.id === id)?.card, id).toBeDefined();
    const net = s.edges.find((e) => e.edge.id === "<fwa>~portal:internet~uplink");
    const gate = s.items.find((i) => i.id === "portal:internet");
    expect(net && gate).toBeTruthy();
    const end = net!.at(net!.len);
    expect(Math.abs(end.x - (gate!.iso.bounds.x0 - 3))).toBeLessThan(12);
  });
});
