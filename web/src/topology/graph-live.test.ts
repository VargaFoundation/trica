import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEVICES, T, lagContext, lagLinks, sampleContext, sampleInput, sampleLinks } from "./fixtures";
import { applyLive, draw, figureOf, graphEnv, LABEL_ZOOM, nodeLabel, PAPER_PALETTE, stylesheetFor, toElements, type LiveStyle } from "./graph";
import { buildTopology, toGraph } from "./model";

const text = { deduced: "deduced", level: (l: string) => `deduced, ${l}`, lag: () => "Port-Channel11 (2×)", group: (d: string) => `MLAG ${d}` };
const devices = DEVICES.map((d) =>
  d.id === T.swA1 || d.id === T.swA2 ? { ...d, mlagDomain: "pair-1" } : d.id === T.srv01 ? { ...d, health: "critical" as const, critical: 3 } : d,
);
const graph = () => {
  const t = buildTopology(sampleInput({ devices, links: [...sampleLinks(), ...lagLinks()], context: [...sampleContext(), ...lagContext()] }));
  return toGraph(t, t.rows);
};

beforeEach(() => {
  graphEnv.headless = true;
  graphEnv.layout = "preset";
});
afterEach(() => {
  graphEnv.headless = false;
  graphEnv.layout = "elk";
});

const style = (u: Partial<LiveStyle> = {}): LiveStyle => ({ util: "normal", width: 4, label: "↑ 1 Gb/s", flowing: true, ...u });

describe("the live overlay on the drawing", () => {
  it("sets width, colour class and label on the lines it measures, and nothing on the others", () => {
    const g = graph();
    const d = draw(null, g, text, () => undefined);
    // Headless, the drawing uses the paper palette: crit is #ce2c31.
    const [first, second] = g.edges;
    applyLive(d.cy, new Map([[first!.id, style({ util: "crit", width: 6.5 })]]));
    const e = d.cy.getElementById(first!.id);
    expect(e.hasClass("live")).toBe(true);
    expect(e.hasClass("live-crit")).toBe(true);
    expect(e.data("lw")).toBe(6.5);
    expect(e.style("width")).toBe("6.5px");
    expect(e.style("line-color")).toBe("rgb(206,44,49)");
    expect(d.cy.getElementById(second!.id).hasClass("live")).toBe(false);
    d.destroy();
  });

  it("keeps the line style of the confidence under the live colour", () => {
    const g = graph();
    const d = draw(null, g, text, () => undefined);
    const deduced = g.edges.find((e) => e.klass === "deduced")!;
    applyLive(d.cy, new Map([[deduced.id, style({ util: "warn" })]]));
    expect(d.cy.getElementById(deduced.id).style("line-style")).toBe("dashed");
    d.destroy();
  });

  it("marks a line down with a bar at each end, not by its colour alone", () => {
    const g = graph();
    const d = draw(null, g, text, () => undefined);
    const e = g.edges[0]!;
    applyLive(d.cy, new Map([[e.id, style({ util: "down" })]]));
    expect(d.cy.getElementById(e.id).style("target-arrow-shape")).toBe("tee");
    d.destroy();
  });

  it("names an aggregate with its speed when measured, and gives its first name back when the overlay goes", () => {
    const g = graph();
    const d = draw(null, g, text, () => undefined);
    const lag = g.edges.find((e) => e.lag)!;
    const el = d.cy.getElementById(lag.id);
    expect(el.data("label")).toBe("Port-Channel11 (2×)");
    applyLive(d.cy, new Map([[lag.id, style({ title: "Port-Channel11 (2×10G)" })]]));
    expect(el.data("label")).toBe("Port-Channel11 (2×10G)");
    applyLive(d.cy, null);
    expect(el.data("label")).toBe("Port-Channel11 (2×)");
    expect(el.hasClass("live")).toBe(false);
    d.destroy();
  });

  it("shows the throughput under the pointer, and on every line past the zoom threshold", () => {
    const g = graph();
    const d = draw(null, g, text, () => undefined);
    applyLive(d.cy, new Map(g.edges.map((e) => [e.id, style()])));
    const e = d.cy.edges().first();
    expect(e.style("label")).not.toBe("↑ 1 Gb/s");
    e.emit("mouseover");
    expect(e.style("label")).toBe("↑ 1 Gb/s");
    e.emit("mouseout");
    expect(e.hasClass("show-live")).toBe(false);
    d.cy.zoom(LABEL_ZOOM + 0.1);
    expect(d.cy.edges(".show-live").length).toBe(d.cy.edges().length);
    d.destroy();
  });

  it("reports a click on a line, a double click (to unfold an aggregate) and a click on the background", () => {
    const onEdge = vi.fn();
    const onEdgeToggle = vi.fn();
    const onClear = vi.fn();
    const onOpen = vi.fn();
    const g = graph();
    const d = draw(null, g, text, onOpen, { onEdge, onEdgeToggle, onClear });
    const e = d.cy.edges().first();
    e.emit("tap");
    e.emit("dbltap");
    d.cy.emit("tap");
    d.cy.getElementById("mlag:pair-1").emit("tap");
    expect(onEdge).toHaveBeenCalledWith(e.id());
    expect(onEdgeToggle).toHaveBeenCalledWith(e.id());
    expect(onClear).toHaveBeenCalled();
    expect(onOpen).not.toHaveBeenCalled();
    d.select(e.id());
    expect(e.selected()).toBe(true);
    d.select(null);
    expect(e.selected()).toBe(false);
    d.destroy();
  });

  it("pulses the lines with traffic only when asked, and never under reduced motion", () => {
    vi.useFakeTimers();
    const g = graph();
    const d = draw(null, g, text, () => undefined);
    applyLive(d.cy, new Map([[g.edges[0]!.id, style({ flowing: true })]]));
    const e = d.cy.getElementById(g.edges[0]!.id);
    d.flow(true);
    vi.advanceTimersByTime(950);
    expect(e.hasClass("pulse")).toBe(true);
    d.flow(false);
    expect(e.hasClass("pulse")).toBe(false);
    const had = Object.getOwnPropertyDescriptor(window, "matchMedia");
    Object.defineProperty(window, "matchMedia", { configurable: true, value: (q: string) => ({ matches: q.includes("reduce") }) });
    d.flow(true);
    vi.advanceTimersByTime(2000);
    expect(e.hasClass("pulse")).toBe(false);
    if (had) Object.defineProperty(window, "matchMedia", had);
    else delete (window as { matchMedia?: unknown }).matchMedia;
    d.destroy();
    vi.useRealTimers();
  });
});

describe("devices on the canvas", () => {
  it("carry the icon of their role, their worst health and their critical events", () => {
    const els = toElements(graph(), text);
    const srv = els.find((e) => e.data.id === T.srv01)!;
    expect(srv.data).toMatchObject({ icon: "server", health: "critical", label: "srv-01  ⚠ 3", name: "srv-01" });
    expect(els.find((e) => e.data.id === T.swA1)?.data).toMatchObject({ icon: "switch", parent: "mlag:pair-1" });
    expect(els.find((e) => e.data.id === "mlag:pair-1")?.data).toMatchObject({ compound: true, label: "MLAG pair-1" });
    expect(nodeLabel({ name: "x" })).toBe("x");
  });

  it("take their icon from the application's files, the set of the theme, never a data: URL", () => {
    const sheet = stylesheetFor(PAPER_PALETTE, "dark") as { selector: string; style: Record<string, unknown> }[];
    const server = sheet.find((r) => r.selector === 'node[icon = "server"]');
    expect(server?.style["background-image"]).toBe("/topology-icons/dark/server.svg");
    expect(JSON.stringify(sheet)).not.toContain("data:");
    const critical = sheet.find((r) => r.selector === 'node[health = "critical"]');
    expect(critical?.style["border-color"]).toBe(PAPER_PALETTE.healthCrit);
  });

  it("leave the MLAG box and the event badge out of the vector export", () => {
    const d = draw(null, graph(), text, () => undefined);
    const fig = figureOf(d.cy);
    const texts = fig.shapes.filter((s) => s.kind === "text").map((s) => (s as { text: string }).text);
    expect(texts).toContain("srv-01");
    expect(texts.some((x) => x.includes("⚠") || x.startsWith("MLAG"))).toBe(false);
    d.destroy();
  });
});
