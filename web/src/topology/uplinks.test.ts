// An uplink (uplinked_to, ADR 0062) is a device-level line: a controller says
// which device another one reaches the network through, and names no port.
// These tests pin how it is read, when it is a line of its own and when it is
// the cable's, how it is drawn without relying on its colour, and that it is
// never measured.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { RelationLink } from "../api/types";
import { A1E1, A2E1, CABLE1, T, end, rel, sampleInput, sampleLinks } from "./fixtures";
import { AP01, DEVICES_WITH_AP, UPLINK, device, sampleUplinks } from "./fixtures-uplinks";
import { arrowHead, boxEdge, draw, edgeLabel, figureOf, graphEnv, PAPER_PALETTE, stylesheetFor, UPLINK_STYLE } from "./graph";
import { rowLive } from "./live";
import { ALL_CLASSES, buildTopology, filterRows, isUplink, LINK_TYPES, toGraph, type Graph } from "./model";
import type { Shape } from "../export/vectorPdf";

type Line = Extract<Shape, { kind: "line" }>;
type Box = Extract<Shape, { kind: "box" }>;
type Label = Extract<Shape, { kind: "text" }>;

/** Cytoscape reports a colour as rgb(r,g,b). */
const rgb = (hex: string) => `rgb(${[1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)).join(",")})`;
const text = { deduced: "deduced", uplink: "uplink (port unknown)", level: (l: string) => `deduced, ${l}` };

const inputOf = (links: RelationLink[], over: Parameters<typeof sampleInput>[0] = {}) =>
  sampleInput({ links, context: [], devices: DEVICES_WITH_AP, ...over });
const topology = () => buildTopology(sampleInput({ links: [...sampleLinks(), ...sampleUplinks()], devices: DEVICES_WITH_AP }));
const all = () => {
  const t = topology();
  return { t, g: toGraph(t, t.rows) };
};
const edgeOf = (g: Graph, from: string, to: string) => g.edges.find((e) => e.source === from && e.target === to);

beforeEach(() => {
  graphEnv.headless = true;
  graphEnv.layout = "preset";
});
afterEach(() => {
  graphEnv.headless = false;
  graphEnv.layout = "elk";
});

describe("reading uplinks", () => {
  it("asks for them in the same bulk call as the cables", () => {
    expect(LINK_TYPES).toContain("uplinked_to");
    // The server answers at most 8 types in one call (ADR 0024).
    expect(LINK_TYPES.length).toBeLessThanOrEqual(8);
  });

  it("gives a row per uplink, between the two devices themselves, with no port", () => {
    const rows = topology().rows.filter(isUplink);
    expect(rows).toHaveLength(3);
    const ap = rows.find((r) => r.a.deviceId === UPLINK.ap01)!;
    expect(ap).toMatchObject({ kind: "uplinked_to", klass: "observed", source: "unifi:office-lan" });
    expect(ap.a).toEqual({ id: UPLINK.ap01, name: "", deviceId: UPLINK.ap01 });
    expect(ap.b.deviceId).toBe(T.swA2);
    expect(ap.cable).toBeUndefined();
  });

  it("keeps a device end as the device, never as its parent", () => {
    const inChassis = end(T.swA2, "device", "sw-a2", T.swA1, T.paris);
    const t = buildTopology(inputOf([rel("uplinked_to", AP01, inChassis)]));
    const r = t.rows.find(isUplink)!;
    expect(r.b.deviceId).toBe(T.swA2);
  });

  it("refuses an uplink whose end is not a device and counts it", () => {
    const t = buildTopology(inputOf([rel("uplinked_to", AP01, end("p-if-x", "interface", "eth0", T.swA2, T.paris))]));
    expect(t.rows.filter(isUplink)).toHaveLength(0);
    expect(t.skipped.ends).toBe(1);
  });

  it("takes the site of a device from its own end", () => {
    const t = buildTopology(inputOf([rel("uplinked_to", AP01, device(T.swA2, "sw-a2", T.paris))], { devices: [] }));
    expect(t.nodes.get(UPLINK.ap01)?.siteId).toBe(T.paris);
  });
});

describe("one line between two devices", () => {
  it("draws an uplink no cable covers, towards the uplink device", () => {
    const { g } = all();
    expect(edgeOf(g, UPLINK.ap01, T.swA2)).toMatchObject({ uplink: true, klass: "observed", count: 1 });
    // The Lyon switch reaches sw-a1 through an uplink that crosses sites.
    expect(edgeOf(g, T.swL1, T.swA1)?.uplink).toBe(true);
    expect(g.edges.filter((e) => e.uplink)).toHaveLength(2);
  });

  it("attaches an uplink to the line that already joins the two devices, and counts it", () => {
    const { g } = all();
    const between = g.edges.filter((e) => [e.source, e.target].sort().join() === [T.swA1, T.swA2].sort().join());
    // No line of the uplink's own between sw-a1 and sw-a2: the cable's is the one.
    expect(between.length).toBeGreaterThan(0);
    expect(between.every((e) => !e.uplink)).toBe(true);
    const covering = between.flatMap((e) => e.uplinks ?? []);
    expect(covering).toHaveLength(1);
    expect(covering[0]).toMatchObject({ kind: "uplinked_to", a: { deviceId: T.swA2 }, b: { deviceId: T.swA1 } });
    expect(g.covered).toBe(1);
  });

  it("draws the uplink on its own when the cable covering it is filtered out", () => {
    const links = [
      rel("connects", CABLE1, A1E1, { confidence: "declared", attributes: { end: "a" } }),
      rel("connects", CABLE1, A2E1, { confidence: "declared", attributes: { end: "b" } }),
      sampleUplinks()[1]!,
    ];
    const t = buildTopology(inputOf(links));
    const shown = toGraph(t, filterRows(t, { classes: ALL_CLASSES }));
    expect(shown.edges.map((e) => !!e.uplink)).toEqual([false]);
    expect(shown.covered).toBe(1);
    // The reader hides declared links: the observed uplink is what is left to show for the pair.
    const hidden = toGraph(t, filterRows(t, { classes: new Set(["observed", "deduced"]) }));
    expect(edgeOf(hidden, T.swA2, T.swA1)?.uplink).toBe(true);
    expect(hidden.covered).toBe(0);
  });

  it("is covered by a neighbour link as well as by a cable", () => {
    const t = topology();
    const g = toGraph(
      t,
      t.rows.filter((r) => r.kind === "neighbor_of" || (isUplink(r) && r.b.deviceId === T.swA2) || (isUplink(r) && r.a.deviceId === T.swA2)),
    );
    expect(g.covered).toBe(1);
    expect(g.edges.filter((e) => e.uplink).map((e) => e.source)).toEqual([UPLINK.ap01]);
  });

  it("keeps two uplinks of opposite directions apart, and counts a loop", () => {
    const back = rel("uplinked_to", device(T.swA2, "sw-a2", T.paris), AP01);
    const self = rel("uplinked_to", AP01, AP01);
    const t = buildTopology(inputOf([sampleUplinks()[0]!, back, self]));
    const g = toGraph(t, t.rows);
    expect(g.edges.filter((e) => e.uplink)).toHaveLength(2);
    expect(g.loops).toBe(1);
  });
});

describe("filters", () => {
  it("drops uplinks under a VLAN filter: a VLAN is a property of ports", () => {
    const t = buildTopology(sampleInput({ links: [...sampleLinks(), ...sampleUplinks()], devices: DEVICES_WITH_AP }));
    const rows = filterRows(t, { vlan: T.vlan10, classes: ALL_CLASSES });
    expect(rows.some(isUplink)).toBe(false);
    expect(rows.length).toBeGreaterThan(0);
  });

  it("keeps an uplink that leaves a site when one end is in it, like any link", () => {
    const rows = filterRows(topology(), { site: T.lyon, classes: ALL_CLASSES }).filter(isUplink);
    expect(rows.map((r) => [r.a.deviceId, r.b.deviceId])).toEqual([[T.swL1, T.swA1]]);
  });

  it("follows the class boxes", () => {
    expect(filterRows(topology(), { classes: new Set(["declared"]) }).some(isUplink)).toBe(false);
    expect(filterRows(topology(), { classes: new Set(["observed"]) }).filter(isUplink)).toHaveLength(3);
  });
});

describe("how an uplink is drawn", () => {
  it("is labelled in words, before any level", () => {
    expect(edgeLabel({ klass: "observed", count: 1, uplink: true }, text)).toBe("uplink (port unknown)");
    expect(edgeLabel({ klass: "deduced", level: "low", count: 1, uplink: true }, text)).toBe("uplink (port unknown) deduced, low");
    expect(edgeLabel({ klass: "observed", count: 1 }, text)).toBe("");
  });

  it("is dash-dot with an arrowhead on the uplink device, and keeps its class colour", () => {
    const { g } = all();
    const d = draw(null, g, text, () => undefined);
    expect(d.cy.edges().filter((x) => x.data("uplink") === true).length).toBe(2);
    const ap = d.cy.getElementById(edgeOf(g, UPLINK.ap01, T.swA2)!.id);
    expect(ap.style("line-style")).toBe("dashed");
    expect(ap.style("target-arrow-shape")).toBe("triangle");
    expect(ap.style("source-arrow-shape")).toBe("none");
    expect(ap.style("line-dash-pattern")).toBe(UPLINK_STYLE.dash.join(" "));
    expect(ap.target().id()).toBe(T.swA2);
    expect(ap.source().id()).toBe(UPLINK.ap01);
    expect(ap.data("label")).toBe("uplink (port unknown)");
    // The colour is still the class's: told apart by the line, the arrow and the label as well.
    expect(ap.style("line-color")).toBe(rgb(PAPER_PALETTE.observed));
    expect(ap.style("target-arrow-color")).toBe(rgb(PAPER_PALETTE.observed));
    // A cable has no arrow.
    expect(d.cy.edges().filter((x) => !x.data("uplink")).first().style("target-arrow-shape")).toBe("none");
    d.destroy();
  });

  it("is set by the stylesheet after the class rules, so that nothing overrides it", () => {
    const sheet = stylesheetFor(PAPER_PALETTE) as { selector: string }[];
    const at = (s: string) => sheet.findIndex((r) => r.selector === s);
    expect(at("edge[?uplink]")).toBeGreaterThan(at('edge[klass = "deduced"]'));
    expect(at('edge[klass = "deduced"]')).toBeGreaterThan(-1);
  });
});

describe("in the PDF", () => {
  it("draws the dash-dot line, the label and an arrowhead ending on the uplink's box, after the boxes", () => {
    const { g } = all();
    const d = draw(null, g, text, () => undefined);
    const fig = figureOf(d.cy);
    const dashdot = fig.shapes.filter((s): s is Line => s.kind === "line" && s.stroke.dash?.join() === "10,3,2,3");
    expect(dashdot).toHaveLength(2);
    expect(dashdot.every((s) => s.stroke.width === UPLINK_STYLE.width)).toBe(true);
    const labels = fig.shapes.filter((s): s is Label => s.kind === "text");
    expect(labels.filter((s) => s.text === "uplink (port unknown)")).toHaveLength(2);
    // Two short strokes per uplink, all after the last box.
    const lastBox = fig.shapes.map((s) => s.kind).lastIndexOf("box");
    const heads = fig.shapes.slice(lastBox + 1).filter((s): s is Line => s.kind === "line");
    expect(heads).toHaveLength(4);
    // For ap-01 -> sw-a2, a head ends on the border of sw-a2's box (and none on ap-01's).
    const boxes = fig.shapes.filter((s): s is Box => s.kind === "box");
    const boxOf = (name: string) => {
      const l = labels.find((s) => s.text === name && !s.background)!;
      return boxes.find((b) => Math.abs(b.x + b.w / 2 - l.at.x) < 0.01 && Math.abs(b.y + b.h / 2 - l.at.y) < 0.01)!;
    };
    const onBorder = (b: Box, p: { x: number; y: number }) => {
      const near = (a: number, c: number) => Math.abs(a - c) < 0.01;
      const inX = p.x >= b.x - 0.01 && p.x <= b.x + b.w + 0.01;
      const inY = p.y >= b.y - 0.01 && p.y <= b.y + b.h + 0.01;
      return inX && inY && (near(p.x, b.x) || near(p.x, b.x + b.w) || near(p.y, b.y) || near(p.y, b.y + b.h));
    };
    expect(heads.filter((h) => onBorder(boxOf("sw-a2"), h.from)).length).toBeGreaterThanOrEqual(2);
    expect(heads.some((h) => onBorder(boxOf("ap-01"), h.from))).toBe(false);
    d.destroy();
  });

  it("gives a cable's line no arrowhead", () => {
    const t = buildTopology(sampleInput());
    const d = draw(null, toGraph(t, t.rows), text, () => undefined);
    const fig = figureOf(d.cy);
    const lastBox = fig.shapes.map((s) => s.kind).lastIndexOf("box");
    expect(fig.shapes.slice(lastBox + 1).filter((s) => s.kind === "line")).toHaveLength(0);
    d.destroy();
  });

  it("computes where a ray leaves a box and the two strokes of a head", () => {
    const box = { x: 100, y: 50, w: 40, h: 20 };
    expect(boxEdge(box, { x: 300, y: 50 })).toEqual({ x: 120, y: 50 });
    expect(boxEdge(box, { x: 100, y: -50 })).toEqual({ x: 100, y: 40 });
    expect(boxEdge(box, { x: 100, y: 50 })).toEqual({ x: 100, y: 50 });
    const [a, b] = arrowHead({ x: 120, y: 50 }, { x: 220, y: 50 }, { color: "#000000", width: 2, dash: [1, 1] }, 8) as Line[];
    expect(a!.from).toEqual({ x: 120, y: 50 });
    expect(a!.to.x).toBeGreaterThan(120);
    expect(a!.to.y).toBeLessThan(50);
    expect(b!.to.y).toBeGreaterThan(50);
    expect(a!.stroke.dash).toBeUndefined();
    expect(Math.hypot(a!.to.x - 120, a!.to.y - 50)).toBeCloseTo(8);
  });
});

describe("live", () => {
  it("never measures an uplink, whatever the live answer is keyed by", () => {
    const row = topology().rows.find((r) => isUplink(r) && r.a.deviceId === UPLINK.ap01)!;
    const measured = { rx_bps: 1, tx_bps: 1, oper: "up", source: "arista" };
    expect(rowLive(row, { [UPLINK.ap01]: measured, [T.swA2]: measured })).toBeUndefined();
  });
});
