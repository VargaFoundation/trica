import { describe, expect, it } from "vitest";
import type { MapEdge, MapNode, TopologyMap } from "../../api/types";
import { fitFrame, FIT_MAX } from "./IsoScene";
import { buildScene, LABEL_MIN_PX, TEXT_STEPS, textScale } from "./scene";
import { MAPS } from "./testdata/maps";

// Text keeps one size on screen, a lone thing is not blown up, and a crowd keeps its names to itself.
const node = (id: string, extra: Partial<MapNode> = {}): MapNode => ({
  id,
  kind: "device",
  display: id,
  role: "server",
  counts: { rooms: 0, racks: 0, devices: 0, internal_links: 0 },
  health: { worst: "ok", ok: 1, warning: 0, critical: 0, unknown: 0, critical_events: 0 },
  ...extra,
});
const unplaced = (nodes: MapNode[], edges: MapEdge[] = []): TopologyMap => ({ ...MAPS.unplaced, nodes, edges });
/** Devices loose in a room: small units on a strip. */
const loose = (nodes: MapNode[]): TopologyMap => ({ ...MAPS.room, nodes, edges: [] });

describe("the text scale of a view", () => {
  it.each([
    [1, 1],
    [0.5, 2.1],
    [0.4, 2.5],
    [0.1, 2.5],
    [1.6, 0.6],
    [1.1, 0.9],
  ])("a view at %f screen px per scene px draws text %f times its design size", (screen, want) => expect(textScale(screen)).toBe(want));

  it("only ever gives one of its steps", () => {
    for (let s = 0.05; s < 4; s *= 1.07) expect(TEXT_STEPS).toContain(textScale(s));
  });

  it("sizes and spaces the pills with the text", () => {
    const at1 = buildScene(MAPS.global, 0).items.find((i) => i.labelShown && i.node.kind === "site");
    const at2 = buildScene(MAPS.global, 0, { text: 2 }).items.find((i) => i.id === at1?.id);
    expect(at1 && at2).toBeTruthy();
    const h = (i: NonNullable<typeof at1>) => (i.pill.y1 - i.pill.y0) / (i.subShown ? 34 : 21);
    expect(h(at1!)).toBeCloseTo(1, 5);
    expect(h(at2!)).toBeCloseTo(2, 5);
    expect(buildScene(MAPS.global, 0, { text: 2 }).text).toBe(2);
  });
});

describe("the frame of a level", () => {
  const stage = { w: 1000, h: 500 };
  it("does not draw a lone device bigger than FIT_MAX", () => {
    const f = fitFrame({ x0: 0, y0: 0, x1: 40, y1: 30 }, stage);
    expect(Math.min(stage.w / (f.x1 - f.x0), stage.h / (f.y1 - f.y0))).toBeCloseTo(FIT_MAX, 5);
  });
  it("frames a big level on its blocks, with room for names and the controls", () => {
    const f = fitFrame({ x0: 0, y0: 0, x1: 3000, y1: 1200 }, stage);
    expect(f.x0).toBeLessThan(0);
    expect(f.x1).toBeGreaterThan(3000);
    expect(f.y0).toBeLessThan(-48);
    expect(f.x1 - f.x0).toBeLessThan(3200);
  });
  it("frames the blocks, not the floor they stand on", () => {
    const s = buildScene(MAPS.room, 0);
    const floor = s.plates.find((p) => p.id === "floor");
    expect(floor).toBeDefined();
    expect(s.core.x1 - s.core.x0).toBeLessThan(floor!.iso.bounds.x1 - floor!.iso.bounds.x0);
  });
});

describe("names in a crowd", () => {
  const many = Array.from({ length: 60 }, (_, i) => node(`srv-${String(i).padStart(2, "0")}`));
  const sick = node("srv-sick", { health: { worst: "critical", ok: 0, warning: 0, critical: 1, unknown: 0, critical_events: 1 } });

  it("zoomed out, a small device's name waits until it is pointed at, but one in trouble keeps it", () => {
    const s = buildScene(loose([...many, sick]), 0, { text: 2.5 });
    const devs = s.items.filter((i) => i.id !== "srv-sick");
    expect((devs[0]!.iso.bounds.x1 - devs[0]!.iso.bounds.x0) / 2.5).toBeLessThan(LABEL_MIN_PX);
    expect(devs.every((i) => !i.labelShown)).toBe(true);
    expect(s.items.find((i) => i.id === "srv-sick")?.labelShown).toBe(true);
    expect(buildScene(loose([...many, sick]), 0).items.some((i) => i.id !== "srv-sick" && i.labelShown)).toBe(true);
  });

  it("never sends a device's name sideways or far below it", () => {
    const s = buildScene(unplaced([...many, sick]), 0);
    for (const i of s.items.filter((x) => x.labelShown)) {
      const mid = (i.pill.x0 + i.pill.x1) / 2;
      expect(Math.abs(mid - i.iso.base.x), i.id).toBeLessThan(1);
      expect(i.pill.y0 - i.iso.bounds.y1, i.id).toBeLessThan(40);
    }
  });

  it("keeps the port names of a level of many lines until a line is pointed at", () => {
    const ports = (a: string, b: string) => [{ a_device: { id: a, type: "device", display: a }, a_port: { id: `${a}/p`, type: "port", display: "Et1" }, b_device: { id: b, type: "device", display: b }, b_port: { id: `${b}/p`, type: "port", display: "Et2" }, kind: "cable", confidence: "observed" }];
    const edge = (a: string, b: string): MapEdge =>
      ({ id: `${a}~${b}~physical`, a, b, medium: "physical", kinds: { cable: 1, cabled_to: 0, neighbor_of: 0, tunnel: 0, uplink: 0, uplinked_to: 0 }, links: 1, confidence: "observed", capacity_partial: false, oper: "up", measured: false, members: ports(a, b), members_truncated: false }) as MapEdge;
    const few = buildScene(unplaced(many, [edge("srv-00", "srv-59")]), 0).edges;
    expect(few.some((e) => e.portsShown)).toBe(true);
    const lots = buildScene(unplaced(many, many.slice(0, 13).map((n, i) => edge(n.id, many[59 - i]!.id))), 0).edges;
    expect(lots).toHaveLength(13);
    expect(lots.every((e) => !e.portsShown)).toBe(true);
  });
});
