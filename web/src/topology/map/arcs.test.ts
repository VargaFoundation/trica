import { describe, expect, it } from "vitest";
import type { MapEdge, MapNode, TopologyMap } from "../../api/types";
import { insidePolygon, pointsOf } from "./iso";
import { buildScene, type Scene } from "./scene";
import { MAPS } from "./testdata/maps";

// Lines between places pass above the walls of the rooms in their way (ADR 0066, amendment "visual
// language"): a room's lines start from the top of a mast above its walls, and an arc is raised until
// it clears every wall.
const health = { worst: "ok", ok: 1, warning: 0, critical: 0, unknown: 0, critical_events: 0 };
const counts = { rooms: 0, racks: 2, devices: 4, internal_links: 0 };
const room = (id: string): MapNode => ({ id, kind: "room", display: id, counts, health, drill: { level: "room", id } }) as MapNode;
const cable = (a: string, b: string): MapEdge =>
  ({ id: `${a}~${b}~physical`, a, b, medium: "physical", kinds: { cable: 1, cabled_to: 0, neighbor_of: 0, tunnel: 0, uplink: 0, uplinked_to: 0 }, links: 1, confidence: "observed", capacity_mbps: 10000, capacity_partial: false, oper: "up", measured: true, a_to_b_bps: 1e8, b_to_a_bps: 1e8, members: [], members_truncated: false }) as MapEdge;
/** Four rooms in a row, with cables from the first to the others: they pass over the rooms between. */
const row: TopologyMap = { ...MAPS.site, nodes: ["r1", "r2", "r3", "r4"].map(room), edges: [cable("r1", "r2"), cable("r1", "r3"), cable("r1", "r4"), cable("r2", "r4")] };

/** The points of the level's arcs, but near their ends, that lie inside the walls of a room they pass over (not one of their ends). */
function overWalls(s: Scene): string[] {
  const walls = s.items.flatMap((i) => (i.walls ? [...i.walls.back, ...i.walls.front].map((w) => ({ id: i.id, poly: pointsOf(w.hull) })) : []));
  const out: string[] = [];
  for (const e of s.edges) {
    for (let k = 1; k < 10; k++) {
      const p = e.at((e.len * k) / 10);
      for (const w of walls) if (w.id !== e.edge.a && w.id !== e.edge.b && insidePolygon(p, w.poly)) out.push(`${e.edge.id} over the walls of ${w.id}`);
    }
  }
  return [...new Set(out)];
}

describe("lines between places", () => {
  it("know whether a point is inside a polygon", () => {
    const sq = pointsOf("0,0 10,0 10,10 0,10");
    expect(insidePolygon({ x: 5, y: 5 }, sq)).toBe(true);
    expect(insidePolygon({ x: 15, y: 5 }, sq)).toBe(false);
  });

  for (const rot of [0, 1, 2, 3] as const) {
    it(`pass above every wall of the rooms they cross (rotation ${rot})`, () => {
      const s = buildScene(row, rot);
      expect(s.edges).toHaveLength(4);
      expect(overWalls(s)).toEqual([]);
    });
  }

  it("pass above the walls of the recorded site too", () => {
    for (const rot of [0, 1, 2, 3] as const) expect(overWalls(buildScene(MAPS.site, rot))).toEqual([]);
  });

  it("start from the top of a room's mast, above its near walls", () => {
    const s = buildScene(row, 0);
    for (const r of s.items.filter((i) => i.role === "room")) {
      expect(r.mast, r.id).toBeDefined();
      const top = Math.min(...(r.walls?.front ?? []).map((w) => w.bounds.y0));
      expect(r.port.y, r.id).toBeLessThan(top);
      expect(r.mast!.y2).toBe(Math.round(r.port.y * 10) / 10);
    }
  });
});
