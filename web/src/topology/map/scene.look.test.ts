import { describe, expect, it } from "vitest";
import type { MapEdge, TopologyMap } from "../../api/types";
import { BAND_WIDTH } from "./speed";
import { buildScene } from "./scene";
import { MAPS } from "./testdata/maps";

// What the scene draws per level (ADR 0066). Each case fails when the matching drawing code is removed.
const withEdges = (m: TopologyMap, f: (e: MapEdge) => Partial<MapEdge>): TopologyMap => ({ ...m, edges: m.edges.map((e) => ({ ...e, ...f(e) }) as MapEdge) });

describe("the look of the scene", () => {
  it("lays a ground plane with a grid under every level", () => {
    for (const lv of ["global", "site", "room", "rack"] as const) {
      const g = buildScene(MAPS[lv], 0).ground;
      expect(g.poly.split(" ").length).toBeGreaterThanOrEqual(4);
      expect(g.grid.length).toBeGreaterThan(0);
      expect(g.r).toBeGreaterThan(0);
    }
  });

  it("draws a site as a building with windows and a roof", () => {
    const sites = buildScene(MAPS.global, 0).items.filter((i) => i.node.kind === "site");
    expect(sites.length).toBeGreaterThan(0);
    for (const s of sites) {
      expect(s.windows.length).toBeGreaterThan(0);
      expect(s.roof).toBeTruthy();
    }
  });

  it("draws a room with cut-away walls, its name on the floor and its racks as cabinets", () => {
    const rooms = buildScene(MAPS.site, 0).items.filter((i) => i.node.kind === "room");
    expect(rooms.length).toBeGreaterThan(0);
    for (const r of rooms) {
      expect(r.walls?.back.length).toBeGreaterThan(0);
      expect(r.floorText?.text).toBe(r.label);
      expect(r.cabinets.length).toBe(r.node.counts.racks);
    }
  });

  it("draws a rack of a room as a cabinet with a glass door, vents and one LED per device", () => {
    const racks = buildScene(MAPS.room, 0).items.filter((i) => i.node.kind === "rack");
    expect(racks.length).toBeGreaterThan(0);
    for (const r of racks) {
      expect(r.door?.glass).toBeTruthy();
      expect(r.door?.vents).toBeTruthy();
      expect(r.door?.leds.length).toBeGreaterThan(0);
    }
  });

  it("routes the cables of a room along trays, on the floor, and keeps arcs above it", () => {
    const room = buildScene(MAPS.room, 0);
    expect(room.trays.length).toBeGreaterThan(0);
    const cable = room.edges.find((e) => e.edge.medium === "physical");
    expect(cable?.floor).toBe(true);
    expect(cable?.d).toMatch(/L/); // orthogonal segments with rounded corners, not one arc
    expect(buildScene(MAPS.global, 0).edges.every((e) => !e.floor)).toBe(true);
  });

  it("takes a line's colour class and width from its capacity", () => {
    const e = buildScene(MAPS.room, 0).edges.find((x) => x.edge.medium === "physical");
    expect(e?.band).toBe("10g");
    expect(e?.width).toBe(BAND_WIDTH["10g"]);
    const unknown = buildScene(MAPS.room, 0).edges.find((x) => x.edge.capacity_mbps === undefined);
    expect(unknown?.band).toBe("unknown");
    expect(unknown?.width).toBeLessThan(e?.width ?? 0);
  });

  it("draws a bundle as one line with its count", () => {
    const e = buildScene(MAPS.room, 0).edges.find((x) => x.edge.medium === "physical" && x.edge.links === 2);
    expect(e?.bundle?.text).toBe("×2");
  });

  it("draws a down line as down and an unreported one as unknown only when something is reported", () => {
    const down = buildScene(withEdges(MAPS.global, (e) => (e.medium === "uplink" ? { oper: "down" } : {})), 0);
    expect(down.edges.find((e) => e.edge.medium === "uplink")?.state).toBe("down");
    const unknown = buildScene(MAPS.global, 0).edges.find((e) => e.edge.oper === "unknown");
    expect(unknown?.state).toBe("unknown");
    const noLive = buildScene(withEdges(MAPS.global, () => ({ oper: "unknown" })), 0);
    expect(noLive.edges.every((e) => e.state === "up")).toBe(true);
  });

  it("gives a tunnel a travelling pulse and a measured line its two lanes", () => {
    expect(buildScene(MAPS.global, 0).edges.find((e) => e.edge.medium === "tunnel")?.pulse).toBe(true);
    expect(buildScene(MAPS.room, 0).edges.find((e) => e.edge.measured)?.lanes).toHaveLength(2);
  });
});
