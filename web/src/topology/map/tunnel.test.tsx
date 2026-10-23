import { waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { expectNoA11yViolations } from "../../test/axe";
import { READER } from "../../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call, type Opts } from "../../test/support";
import type { MapEdge, TopologyMap } from "../../api/types";
import { arc, buildScene, LANE_GAP, MEDIUM_SHIFT, mediumShift, TUNNEL_DASH } from "./scene";
import { MAPS, withLive } from "./testdata/maps";

vi.setConfig({ testTimeout: 20_000 });

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, now: Date.parse("2026-03-01T08:00:05Z") });
});
afterEach(() => {
  vi.useRealTimers();
});

function serve(override?: TopologyMap) {
  mockApi({
    ...defaultRoutes(READER),
    "GET /topology/map": (c: Call) => withLive(override ?? MAPS[(c.query.get("level") ?? "global") as keyof typeof MAPS], true),
  });
}
const scene = () => document.querySelector<SVGSVGElement>(".iso-svg");
const ready = () => waitFor(() => expect(scene()?.getAttribute("data-state")).toBe("drawn"), { timeout: 8000 });
const as = (e: MapEdge, medium: string, extra: Partial<MapEdge> = {}): MapEdge => ({ ...e, ...extra, medium, id: `${e.a}~${e.b}~${medium}` }) as MapEdge;
const cable = () => MAPS.room.edges.find((e) => e.medium === "physical" && e.measured) as MapEdge;
const withEdges = (m: TopologyMap, edges: MapEdge[]): TopologyMap => ({ ...m, edges });
const edgeOf = (m: TopologyMap, rot: 0 | 1 | 2 | 3, medium: string) => buildScene(m, rot).edges.find((e) => e.edge.medium === medium);

describe("the lines of one pair in the scene", () => {
  it("keeps the cable, the tunnel and the device uplink of two places on three different lines", () => {
    const c = cable();
    const m = withEdges(MAPS.room, [c, as(c, "tunnel", { measured: false }), as(c, "uplinked_to", { measured: false, toward: "b" })]);
    for (const rot of [0, 1, 2, 3] as const) {
      const sc = buildScene(m, rot);
      expect(new Set(sc.edges.map((e) => e.d)).size).toBe(3);
      const a = sc.items.find((i) => i.id === c.a);
      const b = sc.items.find((i) => i.id === c.b);
      if (!a || !b) throw new Error("ends missing");
      expect(edgeOf(m, rot, "tunnel")?.d).toBe(arc(a.port, b.port, mediumShift("tunnel")).d);
      expect(edgeOf(m, rot, "physical")?.d).toBe(arc(a.port, b.port, 0).d);
    }
  });

  it("keeps a tunnel off the Internet access of the same place", () => {
    const up = MAPS.global.edges.find((e) => e.medium === "uplink") as MapEdge;
    const sc = buildScene(withEdges(MAPS.global, [up, as(up, "tunnel")]), 0);
    expect(sc.edges).toHaveLength(2);
    expect(sc.edges[0]?.d).not.toBe(sc.edges[1]?.d);
  });

  it("keeps every medium that can share a pair at least 8 px from the others", () => {
    const pairs: [string, string][] = [["physical", "tunnel"], ["physical", "uplinked_to"], ["tunnel", "uplinked_to"], ["uplink", "tunnel"]];
    for (const [x, y] of pairs) expect(Math.abs((MEDIUM_SHIFT[x] as number) - (MEDIUM_SHIFT[y] as number))).toBeGreaterThanOrEqual(8);
    expect(MEDIUM_SHIFT.uplinked_to).toBe(8);
    expect(MEDIUM_SHIFT.physical).toBe(0);
    expect(MEDIUM_SHIFT.uplink).toBe(0);
  });

  it("draws the lanes of a measured tunnel around its own line", () => {
    const c = cable();
    const sc = buildScene(withEdges(MAPS.room, [as(c, "tunnel", { measured: true })]), 0);
    const a = sc.items.find((i) => i.id === c.a);
    const b = sc.items.find((i) => i.id === c.b);
    if (!a || !b) throw new Error("ends missing");
    expect(sc.edges[0]?.lanes.map((l) => l.d)).toEqual([
      arc(a.port, b.port, mediumShift("tunnel") - LANE_GAP).d,
      arc(a.port, b.port, mediumShift("tunnel") + LANE_GAP, true).d,
    ]);
  });
});

describe("the tunnel on the page", () => {
  it.each<[string, Opts, string]>([
    ["en", {}, "Tunnel: a VPN"],
    ["fr", { locale: "fr" }, "Tunnel : un VPN"],
  ])("is drawn dash-dot and named in the legend (%s)", async (_l, opts, text) => {
    serve();
    renderApp({ at: "/topology", ...opts });
    await ready();
    expect(document.querySelector('.iso-edge[data-medium="tunnel"] .iso-edge-base')).toHaveAttribute("stroke-dasharray", TUNNEL_DASH);
    const legend = document.querySelector(".iso-legend") as HTMLElement;
    expect(legend.querySelector(".legend-line-tunnel")).toHaveAttribute("stroke-dasharray", TUNNEL_DASH);
    expect(legend.textContent).toContain(text);
    await expectNoA11yViolations();
  });

  it("has no legend entry where no tunnel is drawn", async () => {
    serve(withEdges(MAPS.global, MAPS.global.edges.filter((e) => e.medium !== "tunnel")));
    renderApp({ at: "/topology" });
    await ready();
    expect(document.querySelector('.iso-edge[data-medium="tunnel"]')).toBeNull();
    expect((document.querySelector(".iso-legend") as HTMLElement).querySelector(".legend-line-tunnel")).toBeNull();
  });
});
