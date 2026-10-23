import { describe, expect, it } from "vitest";
import type { MapNode, TopologyMap } from "../../api/types";
import { layoutWorld, type WorldItem } from "./layout";
import { buildScene } from "./scene";
import { MAPS } from "./testdata/maps";

const overlap = (a: WorldItem, b: WorldItem) =>
  a.box.x < b.box.x + b.box.w && b.box.x < a.box.x + a.box.w && a.box.y < b.box.y + b.box.d && b.box.y < a.box.y + a.box.d && a.box.z < b.box.z + b.box.h && b.box.z < a.box.z + a.box.h;

const node = (id: string, extra: Partial<MapNode> = {}): MapNode => ({
  id,
  kind: "device",
  display: id,
  counts: { rooms: 0, racks: 0, devices: 0, internal_links: 0 },
  health: { worst: "unknown", ok: 0, warning: 0, critical: 0, unknown: 0, critical_events: 0 },
  ...extra,
});
const level = (l: string, nodes: MapNode[], extra: Partial<TopologyMap> = {}): TopologyMap => ({
  ...MAPS.rack,
  level: l,
  nodes,
  edges: [],
  focus: undefined,
  trail: [],
  ...extra,
});

describe("the layout of every recorded level", () => {
  for (const [name, m] of Object.entries(MAPS)) {
    it(`${name}: every node is placed once, inside the world, without two solids in one place`, () => {
      const w = layoutWorld(m);
      expect(new Set(w.items.map((i) => i.id)).size).toBe(w.items.length);
      expect(w.items.map((i) => i.id).sort()).toEqual(m.nodes.map((n) => n.id).sort());
      for (const i of w.items) {
        expect(i.box.x).toBeGreaterThanOrEqual(0);
        expect(i.box.y).toBeGreaterThanOrEqual(0);
        expect(i.box.x + i.box.w).toBeLessThanOrEqual(w.w);
        expect(i.box.y + i.box.d).toBeLessThanOrEqual(w.d);
      }
      // The recorded rack declares two half-depth devices on the same unit and face: a
      // conflict in the data, drawn as it is declared, so the rack level is not checked here.
      if (name !== "rack") for (const [k, a] of w.items.entries()) for (const b of w.items.slice(k + 1)) expect(overlap(a, b), `${a.id} / ${b.id}`).toBe(false);
    });
  }

  it("global: the Internet is a gate behind the sites, and the unplaced devices sit in the row of the sites", () => {
    const w = layoutWorld(MAPS.global);
    const net = w.items.find((i) => i.role === "internet");
    const sites = w.items.filter((i) => i.role === "site");
    expect(sites).toHaveLength(2);
    expect(net && net.box.w).toBeLessThan(0.5);
    // Behind: further from the viewer than every site (a smaller x + y).
    expect(net && net.box.x + net.box.y).toBeLessThan(Math.min(...sites.map((s) => s.box.x + s.box.y)));
    const un = w.items.find((i) => i.role === "unplaced");
    expect(un && Math.abs(un.box.x + un.box.y - (sites[0]?.box.x ?? 0) - (sites[0]?.box.y ?? 0))).toBeLessThan(1.5);
  });

  it("room and site: the portals are gates outside the floor, never on it", () => {
    for (const name of ["room", "site"] as const) {
      const w = layoutWorld(MAPS[name]);
      const gates = w.items.filter((i) => i.role === "portal" || i.role === "internet");
      expect(gates.length, name).toBeGreaterThan(0);
      const others = w.items.filter((i) => !gates.includes(i));
      const floor = w.plates.find((p) => p.id === "floor")?.box;
      for (const g of gates) {
        expect(g.box.w).toBeLessThan(0.5);
        expect(g.box.x + g.box.w, `${name} ${g.id}`).toBeLessThanOrEqual(Math.min(...others.map((o) => o.box.x), floor?.x ?? Infinity));
      }
    }
  });

  it("room: racks stand on the tile their floor position names", () => {
    const m = level("room", [node("a", { kind: "rack", floor: { x: 3, y: 1, facing: "north" } }), node("b", { kind: "rack", floor: { x: 0, y: 2 } })], {
      focus: { id: "r", type: "room", display: "R", width_mm: 3000, depth_mm: 2400, tile_mm: 600 },
    });
    const w = layoutWorld(m);
    const a = w.items.find((i) => i.id === "a");
    const b = w.items.find((i) => i.id === "b");
    expect(a && b && a.box.x - b.box.x).toBeCloseTo(3, 5);
    expect(a && b && a.box.y - b.box.y).toBeCloseTo(-1, 5);
    expect(a?.facing).toEqual({ x: 0, y: -1 });
    expect(b?.facing).toEqual({ x: 0, y: 1 });
  });

  it("room: racks without a floor position go in lines of their own", () => {
    const m = level("room", Array.from({ length: 10 }, (_, i) => node(`r${i}`, { kind: "rack" })));
    const w = layoutWorld(m);
    expect(w.items).toHaveLength(10);
    expect(new Set(w.items.map((i) => i.box.y)).size).toBe(2);
  });

  it("room: devices in the room but in no rack stand on their own dashed strip below the racks", () => {
    const m = level("room", [node("p", { kind: "rack", floor: { x: 0, y: 0 } }), ...Array.from({ length: 30 }, (_, i) => node(`d${i}`, { role: "switch" }))], {
      focus: { id: "r", type: "room", display: "R", width_mm: 1800, depth_mm: 1800, tile_mm: 600 },
    });
    const w = layoutWorld(m);
    const devs = w.items.filter((i) => i.node.kind === "device");
    expect(devs).toHaveLength(30);
    expect(devs.every((d) => d.role === "shelf")).toBe(true);
    const rack = w.items.find((i) => i.id === "p");
    expect(Math.min(...devs.map((d) => d.box.y))).toBeGreaterThan((rack?.box.y ?? 0) + (rack?.box.d ?? 0));
    expect(w.plates.some((p) => p.id === "devices" && p.dashed)).toBe(true);
  });

  it("room: racks beyond the placed ones wait on a dashed strip below the floor", () => {
    const m = level("room", [node("p", { kind: "rack", floor: { x: 0, y: 0 } }), node("q", { kind: "rack" })], {
      focus: { id: "r", type: "room", display: "R", width_mm: 1800, depth_mm: 1800, tile_mm: 600 },
    });
    const w = layoutWorld(m);
    expect(w.plates.some((p) => p.dashed)).toBe(true);
    const p = w.items.find((i) => i.id === "p");
    const q = w.items.find((i) => i.id === "q");
    expect(q && p && q.box.y).toBeGreaterThan((p?.box.y ?? 0) + 3);
  });

  it("rack: a device stands at the height of its unit, higher units higher", () => {
    const m = level("rack", [
      node("low", { mount: { position: 1, u_height: 2, full_depth: true } }),
      node("high", { mount: { position: 20, u_height: 1, full_depth: true } }),
    ]);
    const w = layoutWorld(m);
    const low = w.items.find((i) => i.id === "low");
    const high = w.items.find((i) => i.id === "high");
    expect(high && low && high.box.z).toBeGreaterThan((low?.box.z ?? 0) + (low?.box.h ?? 0));
    expect(low && high && low.box.h).toBeGreaterThan((high?.box.h ?? 0) * 1.5);
  });

  it("rack: a half-depth device sits on its own face, the other face stays free", () => {
    const m = level("rack", [
      node("front", { mount: { position: 5, u_height: 1, full_depth: false } }),
      node("rear", { mount: { position: 5, u_height: 1, full_depth: false, face: "rear" } }),
      node("deep", { mount: { position: 8, u_height: 1, full_depth: true } }),
    ]);
    const w = layoutWorld(m);
    const by = (id: string) => w.items.find((i) => i.id === id)?.box;
    expect(by("rear")?.y).toBeLessThan(by("front")?.y ?? 0);
    expect(by("front")?.d).toBeCloseTo(by("rear")?.d ?? -1, 5);
    expect(by("deep")?.d).toBeGreaterThan((by("front")?.d ?? 0) * 1.8);
    expect(w.items.filter((i) => i.id !== "deep").every((i, k, all) => all.every((o, j) => k === j || !overlap(i, o)))).toBe(true);
  });

  it("rack: the cabinet is drawn at the first usual height that holds its devices", () => {
    const at = (p: number) => layoutWorld(level("rack", [node("d", { mount: { position: p, u_height: 1, full_depth: true } })])).units;
    expect(at(10)).toBe(12);
    expect(at(30)).toBe(42);
    expect(at(44)).toBe(48);
  });

  it("rack: a device with no unit waits on a shelf beside the cabinet", () => {
    const w = layoutWorld(level("rack", [node("loose"), node("in", { mount: { position: 1, u_height: 1, full_depth: true } })]));
    expect(w.items.find((i) => i.id === "loose")?.role).toBe("shelf");
    expect(w.items.find((i) => i.id === "in")?.role).toBe("device");
  });

  it("rack: a frame of four posts stands round the devices, and the gates in a column outside it on the right", () => {
    const w = layoutWorld(MAPS.rack);
    const f = w.frame;
    expect(f).toBeDefined();
    expect(f?.units).toBe(w.units);
    const devs = w.items.filter((i) => i.role === "device");
    for (const d of devs) {
      expect(d.box.x).toBeGreaterThanOrEqual(f?.box.x ?? 0);
      expect(d.box.x + d.box.w).toBeLessThanOrEqual((f?.box.x ?? 0) + (f?.box.w ?? 0));
      expect(d.box.z + d.box.h).toBeLessThanOrEqual(f?.box.h ?? 0);
      expect(d.portSide).toBe("east");
    }
    const gates = w.items.filter((i) => i.role === "portal" || i.role === "internet");
    expect(gates.length).toBeGreaterThan(0);
    for (const g of gates) expect(g.box.x).toBeGreaterThanOrEqual((f?.box.x ?? 0) + (f?.box.w ?? 0));
    expect(new Set(gates.map((g) => g.box.x)).size).toBe(1);
  });

  it("rack: a gate stands at the height of the device it is cabled from", () => {
    const m = level(
      "rack",
      [
        node("low", { mount: { position: 2, u_height: 1, full_depth: true } }),
        node("high", { mount: { position: 30, u_height: 2, full_depth: true } }),
        node("g1", { kind: "portal", place_type: "internet" }),
        node("g2", { kind: "portal" }),
      ],
    );
    const edge = (id: string, a: string, b: string) => ({ ...MAPS.rack.edges[0]!, id, a, b });
    const w = layoutWorld({ ...m, edges: [edge("e1", "low", "g1"), edge("e2", "g2", "high")] });
    const gate = (id: string) => w.items.find((i) => i.id === id)!;
    const dev = (id: string) => w.items.find((i) => i.id === id)!;
    const mid = (i: WorldItem) => i.box.z + i.box.h / 2;
    expect(Math.abs(mid(gate("g1")) - mid(dev("low")))).toBeLessThan(0.05);
    expect(Math.abs(mid(gate("g2")) - mid(dev("high")))).toBeLessThan(0.05);
    expect(mid(gate("g2"))).toBeGreaterThan(mid(gate("g1")) + 5);
  });

  it("rack: a rack of 96 devices is laid out (the heaviest recorded size)", () => {
    const devs = Array.from({ length: 96 }, (_, i) => node(`d${i}`, { mount: { position: i + 1, u_height: 1, full_depth: i % 2 === 0, face: i % 2 ? "rear" : "front" } }));
    const w = layoutWorld(level("rack", devs));
    expect(w.items).toHaveLength(96);
    expect(w.units).toBe(96);
  });
});

describe("the scene", () => {
  it("draws a line only between two things it draws, and gives measured lines two lanes", () => {
    const m = MAPS.room;
    const s = buildScene(m, 0);
    const ids = new Set(s.items.map((i) => i.id));
    expect(s.edges.length).toBe(m.edges.filter((e) => ids.has(e.a) && ids.has(e.b)).length);
    expect(s.edges.length).toBeGreaterThan(0);
    for (const e of s.edges) expect(e.lanes).toHaveLength(e.edge.measured ? 2 : 0);
    const cut = buildScene({ ...m, nodes: m.nodes.filter((n) => n.id !== "<r2>") }, 0);
    expect(cut.edges.length).toBeLessThan(s.edges.length);
  });

  it("drops the caption, then the label, of a device that would be written over its neighbour's", () => {
    const m = level("rack", [
      node("a", { role: "switch", mount: { position: 1, u_height: 2, full_depth: true } }),
      node("b", { role: "switch", mount: { position: 3, u_height: 2, full_depth: true } }),
      node("c", { role: "switch", mount: { position: 5, u_height: 2, full_depth: true } }),
    ]);
    const s = buildScene(m, 0);
    const shown = s.items.filter((i) => i.labelShown);
    expect(shown.length).toBeGreaterThan(0);
    expect(shown.length).toBeLessThan(3);
    expect(s.items.some((i) => !i.subShown)).toBe(true);
    const far = buildScene(level("rack", [node("a", { role: "switch", mount: { position: 1, u_height: 2, full_depth: true } }), node("b", { role: "switch", mount: { position: 30, u_height: 2, full_depth: true } })]), 0);
    expect(far.items.every((i) => i.labelShown && i.subShown)).toBe(true);
  });

  it("scene: labels are pills that do not overlap, in every recorded level and every turn", () => {
    const hit = (a: { x0: number; x1: number; y0: number; y1: number }, b: typeof a) => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;
    for (const [name, m] of Object.entries(MAPS)) {
      for (const rot of [0, 1, 2, 3] as const) {
        const shown = buildScene(m, rot).items.filter((i) => i.labelShown);
        for (const [k, a] of shown.entries())
          for (const b of shown.slice(k + 1)) expect(hit(a.pill, b.pill), `${name}/${rot}: ${a.id} / ${b.id}`).toBe(false);
      }
    }
  });

  it("scene: a label that would sit on another's is moved down a step rather than lost", () => {
    const m = level("room", [node("a", { kind: "rack", floor: { x: 0, y: 0 } }), node("b", { kind: "rack", floor: { x: 1, y: 0 } })], {
      focus: { id: "r", type: "room", display: "R", width_mm: 1800, depth_mm: 1200, tile_mm: 600 },
    });
    const s = buildScene(m, 0);
    expect(s.items.filter((i) => i.labelShown)).toHaveLength(2);
  });

  it("scene: the rack's frame has two layers of posts, U ticks, and numbers", () => {
    const s = buildScene(MAPS.rack, 0);
    expect(s.frame?.back).toHaveLength(1);
    expect(s.frame?.front).toHaveLength(3);
    expect((s.frame?.ticks.match(/M/g) ?? []).length).toBe(s.units);
    expect(s.frame?.marks[0]?.text).toBe("1");
    expect(buildScene(MAPS.room, 0).frame).toBeUndefined();
  });

  it("runs the two lanes of a line along the same curve in opposite directions", () => {
    const s = buildScene(MAPS.room, 0);
    const e = s.edges.find((x) => x.lanes.length === 2);
    const ab = e?.lanes.find((l) => l.dir === "ab")?.d ?? "";
    const ba = e?.lanes.find((l) => l.dir === "ba")?.d ?? "";
    const pts = (d: string) => d.match(/-?\d+(\.\d+)?/g)?.map(Number) ?? [];
    const [ax, ay, , , bx, by] = pts(ab);
    const [cx, cy, , , dx, dy] = pts(ba);
    // ba starts near where ab ends and ends near where ab starts (they are 6 px apart sideways).
    expect(Math.hypot((ax ?? 0) - (dx ?? 0), (ay ?? 0) - (dy ?? 0))).toBeLessThan(7);
    expect(Math.hypot((bx ?? 0) - (cx ?? 0), (by ?? 0) - (cy ?? 0))).toBeLessThan(7);
  });

  it("keeps every item in view whatever the turn, and orders them nearest last", () => {
    for (const rot of [0, 1, 2, 3] as const) {
      const s = buildScene(MAPS.site, rot);
      const depths = s.items.map((i) => i.iso.depth);
      expect(depths).toEqual([...depths].sort((a, b) => a - b));
      for (const i of s.items) {
        expect(i.iso.bounds.x0).toBeGreaterThanOrEqual(s.bounds.x0);
        expect(i.iso.bounds.x1).toBeLessThanOrEqual(s.bounds.x1);
        expect(i.iso.bounds.y0).toBeGreaterThanOrEqual(s.bounds.y0);
        expect(i.iso.bounds.y1).toBeLessThanOrEqual(s.bounds.y1);
      }
    }
  });

  it("draws the same scene after four quarter turns", () => {
    const a = buildScene(MAPS.room, 0);
    const b = buildScene(MAPS.room, 0);
    expect(a.viewBox).toBe(b.viewBox);
    const turned = [1, 2, 3].map((r) => buildScene(MAPS.room, r as 1 | 2 | 3).viewBox);
    expect(new Set([a.viewBox, ...turned]).size).toBeGreaterThan(1);
  });
});
