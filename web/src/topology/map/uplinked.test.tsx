import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { expectNoA11yViolations } from "../../test/axe";
import { READER } from "../../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call, type Opts } from "../../test/support";
import type { MapEdge } from "../../api/types";
import { arc, arrowHead, buildScene, UPLINKED_DASH, UPLINKED_SHIFT } from "./scene";
import { MAPS, withLive } from "./testdata/maps";

vi.setConfig({ testTimeout: 20_000 });

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, now: Date.parse("2026-03-01T08:00:05Z") });
});
afterEach(() => {
  vi.useRealTimers();
});

const uplinked = (edges: MapEdge[]) => edges.find((e) => e.medium === "uplinked_to") as MapEdge;

function serve() {
  mockApi({
    ...defaultRoutes(READER),
    "GET /topology/map": (c: Call) => withLive(MAPS[(c.query.get("level") ?? "global") as keyof typeof MAPS], true),
  });
}
const scene = () => document.querySelector<SVGSVGElement>(".iso-svg");
const ready = () => waitFor(() => expect(scene()?.getAttribute("data-state")).toBe("drawn"), { timeout: 8000 });
const edgeEl = () => document.querySelector('.iso-edge[data-medium="uplinked_to"]');
const parse = (pts: string) => pts.split(" ").map((p) => p.split(",").map(Number) as [number, number]);

describe("the uplinked_to edge in the scene", () => {
  it("has one arrowhead at the end its uplink devices are on, none on the other media", () => {
    const sc = buildScene(MAPS.global, 0);
    const up = sc.edges.find((e) => e.edge.medium === "uplinked_to");
    expect(up).toBeDefined();
    expect(up?.edge.toward).toBe("b");
    expect(up?.arrows).toHaveLength(1);
    const a = sc.items.find((i) => i.id === up?.edge.a);
    const b = sc.items.find((i) => i.id === up?.edge.b);
    if (!a || !b || !up) throw new Error("ends missing");
    const shifted = arc(a.port, b.port, UPLINKED_SHIFT);
    const tip = parse(up.arrows[0] as string)[0] as [number, number];
    // The tip is the b end of the shifted line, not the a end.
    expect(tip).toEqual([Math.round(shifted.ends[1].x * 10) / 10, Math.round(shifted.ends[1].y * 10) / 10]);
    expect(up.d).toBe(shifted.d);
    expect(up.d).not.toBe(arc(a.port, b.port).d);
    for (const e of sc.edges) if (e.edge.medium !== "uplinked_to") expect(e.arrows).toEqual([]);
    expect(sc.edges.map((e) => e.edge.medium)).toEqual(expect.arrayContaining(["physical", "tunnel", "uplink"].filter((m) => MAPS.global.edges.some((e) => e.medium === m))));
  });

  it("has an arrowhead at each end when the uplinks go both ways, and at the a end for toward a", () => {
    const base = uplinked(MAPS.global.edges);
    const both = { ...MAPS.global, edges: [{ ...base, toward: "both" as const }] };
    expect(buildScene(both, 0).edges[0]?.arrows).toHaveLength(2);
    const toA = { ...MAPS.global, edges: [{ ...base, toward: "a" as const }] };
    const s = buildScene(toA, 0);
    expect(s.edges[0]?.arrows).toHaveLength(1);
    const a = s.items.find((i) => i.id === base.a);
    const b = s.items.find((i) => i.id === base.b);
    if (!a || !b) throw new Error("ends missing");
    const e = arc(a.port, b.port, UPLINKED_SHIFT).ends[0];
    expect(parse(s.edges[0]?.arrows[0] as string)[0]).toEqual([Math.round(e.x * 10) / 10, Math.round(e.y * 10) / 10]);
  });

  it("points the arrowhead along the tangent, the tip first", () => {
    const [tip, l, r] = parse(arrowHead({ x: 100, y: 0 }, { x: 0, y: 0 }, 10));
    expect(tip).toEqual([100, 0]);
    expect((l as number[])[0]).toBeLessThan(100);
    expect((r as number[])[0]).toBeLessThan(100);
    expect((l as number[])[1]).toBeCloseTo(-((r as number[])[1] as number), 5);
  });
});

describe("the uplinked_to edge on the page", () => {
  it.each<[string, Opts, string, string]>([
    ["en", {}, "Uplink (port unknown)", "nothing to measure"],
    ["fr", { locale: "fr" }, "Remontée (port inconnu)", "rien à mesurer"],
  ])("is drawn, named in the legend and the table, and explained in the panel (%s)", async (_l, opts, label, nothing) => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    serve();
    renderApp({ at: "/topology", ...opts });
    await ready();
    const el = edgeEl();
    expect(el).not.toBeNull();
    expect(el?.querySelector(".iso-edge-base")).toHaveAttribute("stroke-dasharray", UPLINKED_DASH);
    expect(el?.querySelectorAll(".iso-edge-arrow")).toHaveLength(1);
    expect(el?.querySelector(".iso-flow")).toBeNull();
    const legend = document.querySelector(".iso-legend") as HTMLElement;
    expect(legend.querySelector(".legend-line-uplink")).not.toBeNull();
    expect(legend.querySelector(".legend-arrow-uplink")).not.toBeNull();
    expect(legend.textContent).toContain(label.startsWith("Rem") ? "Remontée :" : "Uplink: a controller");

    const table = await screen.findByRole("table");
    const row = table.querySelector(`tr[data-id="${uplinked(MAPS.global.edges).id}"]`) as HTMLElement;
    expect(within(row).getByText(label)).toBeInTheDocument();
    await user.click(within(row).getByRole("button"));
    await waitFor(() => expect(document.querySelector(".iso-panel")).not.toBeNull());
    const panel = document.querySelector(".iso-panel") as HTMLElement;
    expect(panel.textContent).toContain(nothing);
    expect(panel.textContent).toContain(label.startsWith("Rem") ? "remontée (port inconnu)" : "uplink (port unknown)");
    expect(within(panel).getAllByRole("link").length).toBeGreaterThanOrEqual(2);
    await expectNoA11yViolations();
  });

  it("has no legend entry where no uplink is drawn", async () => {
    serve();
    renderApp({ at: "/topology?level=rack&id=%3Cr1%3E" });
    await ready();
    expect(edgeEl()).toBeNull();
    const legend = document.querySelector(".iso-legend") as HTMLElement;
    expect(legend.querySelector(".legend-line-uplink")).toBeNull();
  });
});
