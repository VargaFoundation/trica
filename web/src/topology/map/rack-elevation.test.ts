import { describe, expect, it } from "vitest";
import { buildScene, type Scene } from "./scene";
import { BUSY_RACK } from "./testdata/busyRack";
import { MAPS } from "./testdata/maps";

// The rack as it stands (ADR 0066, amendment "the rack as it stands"): its linked devices one row each,
// highest unit first, a riser per hub and per gate, a dot per link, and no line across another.
type Pt = { x: number; y: number };
const segments = (pts: Pt[]) => pts.slice(1).map((b, i) => [pts[i] as Pt, b] as const);

/** Points where two lines of different risers cross, but where one of them has a dot (a junction). */
function crossings(s: Scene): string[] {
  const all = s.edges.filter((e) => e.dot).flatMap((e) => {
    const pts: Pt[] = [];
    for (let d = 0; d <= e.len + 0.01; d += 1) pts.push(e.at(Math.min(d, e.len)));
    return [{ e, pts: [pts[0] as Pt, ...pts.filter((p, i) => i > 0 && i < pts.length - 1 && (Math.abs(p.x - (pts[i - 1] as Pt).x) > 0.01) !== (Math.abs((pts[i + 1] as Pt).x - p.x) > 0.01)), pts[pts.length - 1] as Pt] }];
  });
  const dots = new Set(s.edges.flatMap((e) => (e.dot ? [`${Math.round(e.dot.x)}|${Math.round(e.dot.y)}`] : [])));
  const riserOf = (e: (typeof all)[number]["e"]) => Math.round((e.dot as Pt).x);
  const out = new Set<string>();
  all.forEach((a, i) => {
    for (const b of all.slice(i + 1)) {
      if (riserOf(a.e) === riserOf(b.e)) continue;
      for (const [p, q] of segments(a.pts))
        for (const [r, t] of segments(b.pts)) {
          const h = Math.abs(p.y - q.y) < 0.5 ? [p, q] : Math.abs(r.y - t.y) < 0.5 ? [r, t] : undefined;
          const v = Math.abs(p.x - q.x) < 0.5 ? [p, q] : Math.abs(r.x - t.x) < 0.5 ? [r, t] : undefined;
          if (!h || !v || h[0] === v[0]) continue;
          const x = v[0]!.x;
          const y = h[0]!.y;
          const inside = x > Math.min(h[0]!.x, h[1]!.x) + 1 && x < Math.max(h[0]!.x, h[1]!.x) - 1 && y > Math.min(v[0]!.y, v[1]!.y) + 1 && y < Math.max(v[0]!.y, v[1]!.y) - 1;
          if (inside && !dots.has(`${Math.round(x)}|${Math.round(y)}`)) out.add(`${Math.round(x)},${Math.round(y)}`);
        }
    }
  });
  return [...out];
}

describe("a busy rack as it stands", () => {
  for (const rot of [0, 1, 2, 3] as const) {
    it(`draws its links without any line across another (rotation ${rot})`, () => {
      const s = buildScene(BUSY_RACK, rot);
      expect(s.edges.filter((e) => e.dot)).toHaveLength(BUSY_RACK.edges.length);
      expect(s.bridges).toEqual([]);
      expect(crossings(s)).toEqual([]);
    });
  }

  it("draws the linked devices one row each, highest unit first", () => {
    const s = buildScene(BUSY_RACK, 0);
    const rows = s.items.filter((i) => i.card).sort((a, b) => a.card!.y - b.card!.y);
    const tops = rows.map((i) => (i.node.mount?.position ?? 0) + (i.node.mount?.u_height ?? 1) - 1);
    expect(tops).toEqual([...tops].sort((a, b) => b - a));
    expect(rows[0]?.card?.u).toBe("U42");
  });

  it("ends every link at its dot, and runs no line over a row", () => {
    const s = buildScene(BUSY_RACK, 0);
    const cards = s.items.flatMap((i) => (i.card ? [i.card] : []));
    for (const e of s.edges.filter((x) => x.dot)) {
      const ends = [e.at(0), e.at(e.len)];
      expect(ends.some((p) => Math.abs(p.x - e.dot!.x) < 0.2 && Math.abs(p.y - e.dot!.y) < 0.2), e.edge.id).toBe(true);
      for (let d = 0; d <= e.len; d += 2) {
        const p = e.at(d);
        for (const c of cards) expect(p.x > c.x + 1 && p.x < c.x + c.w - 1 && p.y > c.y + 1 && p.y < c.y + c.h - 1, e.edge.id).toBe(false);
      }
    }
  });

  it("puts the Internet and the other places at the top of their own risers, right of the switches'", () => {
    const s = buildScene(BUSY_RACK, 0);
    const gates = s.items.filter((i) => i.role === "internet" || i.role === "portal");
    expect(gates).toHaveLength(2);
    const switchDots = s.edges.filter((e) => e.dot && !gates.some((g) => g.id === e.edge.a || g.id === e.edge.b)).map((e) => e.dot!.x);
    for (const g of gates) {
      const own = s.edges.filter((e) => e.dot && (e.edge.a === g.id || e.edge.b === g.id));
      expect(own.length).toBeGreaterThan(0);
      for (const e of own) expect(e.dot!.x).toBeCloseTo(g.port.x, 1);
      expect(g.port.x).toBeGreaterThan(Math.max(...switchDots));
    }
  });

  it("keeps the recorded rack's links drawn and dotted too", () => {
    const s = buildScene(MAPS.rack, 0);
    const drawn = s.edges.filter((e) => e.dot);
    expect(drawn.length).toBeGreaterThan(0);
    expect(crossings(s)).toEqual([]);
  });
});
