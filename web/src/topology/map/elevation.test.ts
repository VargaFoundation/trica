import { describe, expect, it } from "vitest";
import { crossings, planElevation, type ElevDevice, type ElevLink, type ElevationPlan } from "./elevation";

// The rack as it stands: rows in the order of the units, a riser per hub, a dot per link, nothing crossing.
const S = { rowW: 200, rowH: 30, rowGap: 4, firstRiser: 24, riserGap: 22, head: 40 };
const dev = (id: string, u: number, role = "server", uHeight = 1): ElevDevice => ({ id, u, uHeight, name: id, role });
const link = (a: string, b: string): ElevLink => ({ key: `${a}~${b}`, a, b });

const servers = ["s1", "s2", "s3", "s4", "s5", "s6", "s7"];
const devices = [dev("mgmt", 42, "switch"), dev("leaf1", 41, "switch"), dev("leaf2", 40, "switch"), dev("fw", 38, "firewall"), ...servers.map((s, i) => dev(s, 30 - i * 2, "server", 2)), dev("lone", 5)];
const links = [
  link("leaf1", "leaf2"),
  link("mgmt", "leaf1"),
  link("mgmt", "leaf2"),
  ...servers.flatMap((s) => [link(s, "mgmt"), link(s, "leaf1"), link(s, "leaf2")]),
  link("fw", "internet"),
  link("s7", "far"),
];
const plan = planElevation(devices, ["internet", "far"], links, S);

/** Segments of every line, and of the stubs; whether two cross away from their ends. */
type Seg = { a: { x: number; y: number }; b: { x: number; y: number }; of: string };
function segs(p: ElevationPlan): Seg[] {
  const out: Seg[] = [];
  for (const [k, pts] of p.routes) for (let i = 1; i < pts.length; i++) out.push({ a: pts[i - 1]!, b: pts[i]!, of: k });
  return out;
}
const cross = (s: Seg, t: Seg) => {
  const h = s.a.y === s.b.y ? s : t.a.y === t.b.y ? t : undefined;
  const v = s.a.x === s.b.x ? s : t.a.x === t.b.x ? t : undefined;
  if (!h || !v || h === v) return false;
  const x = v.a.x;
  const y = h.a.y;
  return x > Math.min(h.a.x, h.b.x) + 0.5 && x < Math.max(h.a.x, h.b.x) - 0.5 && y > Math.min(v.a.y, v.b.y) + 0.5 && y < Math.max(v.a.y, v.b.y) - 0.5;
};
/** The points where lines of different risers cross, but where a dot sits (a junction: the device is cabled there). */
function realCrossings(p: ElevationPlan): string[] {
  const riserOf = (k: string) => p.dots.get(k)!.riser;
  const dotAt = new Set([...p.dots.values()].map((d) => `${d.x}|${d.y}`));
  const all = segs(p);
  const out: string[] = [];
  all.forEach((s, i) => {
    for (const t of all.slice(i + 1)) {
      if (riserOf(s.of) === riserOf(t.of) || !cross(s, t)) continue;
      const v = s.a.x === s.b.x ? s : t;
      const h = v === s ? t : s;
      if (!dotAt.has(`${v.a.x}|${h.a.y}`)) out.push(`${v.a.x},${h.a.y}`);
    }
  });
  return [...new Set(out)];
}

describe("the rack as it stands", () => {
  it("draws the linked devices one row each, the highest unit first, and leaves out the others", () => {
    const order = [...plan.rows.entries()].sort((p, q) => p[1].index - q[1].index).map(([id]) => id);
    expect(order).toEqual(["mgmt", "leaf1", "leaf2", "fw", ...servers]);
    expect(plan.rows.has("lone")).toBe(false);
  });

  it("draws every link once, as a dot on its owner's riser at the other end's row", () => {
    expect(plan.dots.size).toBe(links.length);
    const at = (k: string) => plan.dots.get(k)!;
    const riserX = (id: string) => plan.risers.find((r) => r.id === id)!.x;
    const rowY = (id: string) => plan.rows.get(id)!.y + S.rowH / 2;
    for (const s of servers) for (const sw of ["mgmt", "leaf1", "leaf2"]) expect(at(`${s}~${sw}`)).toEqual({ x: riserX(sw), y: rowY(s), riser: sw });
    expect(at("fw~internet").riser).toBe("internet");
    expect(at("fw~internet").y).toBe(rowY("fw"));
    for (const [k, pts] of plan.routes) {
      const d = at(k);
      const ends = [pts[0]!, pts[pts.length - 1]!];
      expect(ends.some((e) => e.x === d.x && e.y === d.y), k).toBe(true);
    }
  });

  it("puts the gates' risers right of every device's", () => {
    const xs = plan.risers.map((r) => ({ gate: r.gate, x: r.x }));
    const lastDevice = Math.max(...xs.filter((r) => !r.gate).map((r) => r.x));
    for (const g of xs.filter((r) => r.gate)) expect(g.x).toBeGreaterThan(lastDevice);
  });

  it("orders a switch stack's risers lowest switch nearest, so that nothing crosses", () => {
    const order = plan.risers.filter((r) => !r.gate).map((r) => r.id);
    expect(order).toEqual(["leaf2", "leaf1", "mgmt"]);
    expect(crossings(plan)).toBe(0);
    expect(realCrossings(plan)).toEqual([]);
  });

  it("counts and places a bridge where two hubs each span the other's row (a cycle)", () => {
    // h1 at the top serves the bottom; h2 below it serves the very top; each spans the other's row.
    const d = [dev("top", 44), dev("h1", 42, "switch"), dev("h2", 40, "switch"), dev("mid", 30), dev("bottom", 10)];
    const l = [link("h1", "bottom"), link("h1", "mid"), link("h2", "top"), link("h2", "mid")];
    const p = planElevation(d, [], l, S);
    expect(crossings(p)).toBe(1);
    expect(realCrossings(p)).toHaveLength(1);
    expect(p.bridges).toHaveLength(1);
  });

  it("is empty with nothing linked", () => {
    const p = planElevation([dev("a", 1)], [], [], S);
    expect(p.rows.size).toBe(0);
    expect(p.risers).toEqual([]);
    expect(crossings(p)).toBe(0);
  });
});
