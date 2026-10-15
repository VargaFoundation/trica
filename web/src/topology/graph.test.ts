import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { T, sampleInput } from "./fixtures";
import {
  claimCytoscapeStyle,
  CYTOSCAPE_STYLE_ID,
  draw,
  edgeLabel,
  EDGE_STYLE,
  elkOptions,
  exportRatio,
  figureOf,
  graphEnv,
  MAX_PIXELS,
  MAX_SIDE,
  toElements,
} from "./graph";
import { buildTopology, toGraph, type Graph } from "./model";
import { textWidth, type Shape } from "../export/vectorPdf";

const text = { deduced: "deduced", level: (l: string) => `deduced, ${l}` };
const graph = () => {
  const t = buildTopology(sampleInput());
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

describe("labels", () => {
  it("says the level of a deduced line, in words, never only by colour", () => {
    expect(edgeLabel({ klass: "deduced", level: "high", count: 1 }, text)).toBe(
      "deduced, high",
    );
    expect(edgeLabel({ klass: "deduced", level: "low", count: 1 }, text)).toBe(
      "deduced, low",
    );
    expect(edgeLabel({ klass: "deduced", count: 1 }, text)).toBe("deduced");
  });

  it("counts several links on one line", () => {
    expect(
      edgeLabel({ klass: "deduced", level: "medium", count: 3 }, text),
    ).toBe("deduced, medium ×3");
    expect(edgeLabel({ klass: "observed", count: 2 }, text)).toBe("×2");
  });

  it("leaves a single declared or observed line unlabelled: its line style says it", () => {
    expect(edgeLabel({ klass: "declared", count: 1 }, text)).toBe("");
    expect(edgeLabel({ klass: "observed", count: 1 }, text)).toBe("");
  });
});

describe("the three kinds of line", () => {
  it("differ by line style as well as colour", () => {
    const styles = Object.values(EDGE_STYLE).map((s) => s.style);
    expect(new Set(styles).size).toBe(3);
    const colours = Object.values(EDGE_STYLE).map((s) => s.color);
    expect(new Set(colours).size).toBe(3);
    expect(EDGE_STYLE.declared.style).toBe("solid");
    expect(EDGE_STYLE.deduced.style).toBe("dashed");
  });

  it("are applied to the drawn edges", () => {
    const d = draw(null, graph(), text, () => undefined);
    const styleOf = (klass: string) => {
      const found = d.cy.edges().filter((e) => e.data("klass") === klass);
      expect(found.length).toBeGreaterThan(0);
      const e = found[0] as unknown as {
        pstyle(n: string): { strValue: string } | undefined;
      };
      return e.pstyle("line-style")?.strValue;
    };
    expect(styleOf("declared")).toBe("solid");
    expect(styleOf("observed")).toBe("dotted");
    expect(styleOf("deduced")).toBe("dashed");
    d.destroy();
  });
});

describe("elements", () => {
  it("are a node per device and an edge per line, carrying class, level and label", () => {
    const g = graph();
    const els = toElements(g, text);
    expect(els.filter((e) => e.group === "nodes")).toHaveLength(4);
    const edges = els.filter((e) => e.group === "edges").map((e) => e.data);
    expect(edges).toHaveLength(4);
    expect(
      edges.find((e) => e.klass === "deduced" && e.level === "high")?.label,
    ).toBe("deduced, high");
    expect(edges.find((e) => e.klass === "declared")?.label).toBe("");
  });
});

describe("the drawing", () => {
  it("opens the device that is clicked", () => {
    const onOpen = vi.fn();
    const d = draw(null, graph(), text, onOpen);
    d.cy.getElementById(T.swA1).emit("tap");
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(T.swA1);
    d.destroy();
  });

  it("does not open anything when a line is clicked", () => {
    const onOpen = vi.fn();
    const d = draw(null, graph(), text, onOpen);
    d.cy.edges().first().emit("tap");
    expect(onOpen).not.toHaveBeenCalled();
    d.destroy();
  });

  it("is ready at once with a preset layout, and draws every node and edge", async () => {
    const d = draw(null, graph(), text, () => undefined);
    await d.ready;
    expect(d.cy.nodes()).toHaveLength(4);
    expect(d.cy.edges()).toHaveLength(4);
    // Preset positions are distinct: nodes are not piled up on one point.
    const spots = new Set(
      d.cy.nodes().map((n) => `${n.position("x")},${n.position("y")}`),
    );
    expect(spots.size).toBe(4);
    d.destroy();
  });

  it("draws nothing for an empty graph", async () => {
    const d = draw(
      null,
      { nodes: [], edges: [], loops: 0 },
      text,
      () => undefined,
    );
    await d.ready;
    expect(d.cy.elements()).toHaveLength(0);
    d.destroy();
  });
});

describe("layout", () => {
  it("runs ELK and puts the nodes in layers, with no two on the same spot", async () => {
    graphEnv.layout = "elk";
    const d = draw(null, graph(), text, () => undefined);
    await d.ready;
    const pos = d.cy
      .nodes()
      .map((n) => ({ x: n.position("x"), y: n.position("y") }));
    expect(new Set(pos.map((p) => `${p.x},${p.y}`)).size).toBe(4);
    // Layered, top to bottom: more than one distinct row.
    expect(new Set(pos.map((p) => Math.round(p.y))).size).toBeGreaterThan(1);
    d.destroy();
  });

  it("is layered, top to bottom", () => {
    const o = elkOptions(10);
    expect(o.name).toBe("elk");
    expect(o.elk.algorithm).toBe("layered");
    expect(o.elk["elk.direction"]).toBe("DOWN");
  });

  it("spends less effort on a large graph", () => {
    expect(elkOptions(200).elk["elk.layered.thoroughness"]).toBeLessThan(
      elkOptions(20).elk["elk.layered.thoroughness"],
    );
  });
});

describe("export size", () => {
  it("is drawn at twice the screen's resolution when it fits", () => {
    expect(exportRatio(800, 600)).toBe(2);
  });

  it("shrinks an image that would exceed the pixel budget", () => {
    // The budget itself is a documented figure (what browsers accept for a canvas), not a free parameter.
    expect(MAX_PIXELS).toBe(16_000_000);
    expect(MAX_SIDE).toBe(8000);
    const r = exportRatio(6000, 6000);
    expect(r).toBeLessThan(2);
    expect(6000 * r * 6000 * r).toBeLessThanOrEqual(MAX_PIXELS + 1);
  });

  it("shrinks an image whose side would be too long", () => {
    const r = exportRatio(20000, 100);
    expect(20000 * r).toBeLessThanOrEqual(MAX_SIDE + 1e-6);
  });

  it("copes with an empty box", () => {
    expect(exportRatio(0, 0)).toBe(2);
  });
});

describe("the page's content security policy", () => {
  afterEach(() => document.getElementById(CYTOSCAPE_STYLE_ID)?.remove());

  it("claims the element cytoscape looks for, so that it injects no inline <style>", () => {
    claimCytoscapeStyle(document);
    claimCytoscapeStyle(document); // once is enough
    expect(document.querySelectorAll(`#${CYTOSCAPE_STYLE_ID}`)).toHaveLength(1);
    // Cytoscape's own check, word for word (BaseRenderer.init): the id is found, so it adds nothing.
    expect(document.getElementById(CYTOSCAPE_STYLE_ID)).not.toBeNull();
    // It is not a <style>: the policy would refuse its text.
    expect(document.getElementById(CYTOSCAPE_STYLE_ID)?.tagName).toBe("META");
  });

  it("leaves an element that is already there alone", () => {
    const mine = document.createElement("meta");
    mine.id = CYTOSCAPE_STYLE_ID;
    document.head.append(mine);
    claimCytoscapeStyle(document);
    expect(document.querySelectorAll(`#${CYTOSCAPE_STYLE_ID}`)).toHaveLength(1);
    expect(document.getElementById(CYTOSCAPE_STYLE_ID)).toBe(mine);
  });
});

describe("the drawing as a vector figure", () => {
  // Two devices joined by a deduced and an observed line, a third one
  // unresolved, joined by a declared line; the grid puts them 140 px apart.
  const g: Graph = {
    nodes: [
      { id: "a", name: "sw-a1", unresolved: false },
      { id: "b", name: "srv-02", unresolved: false },
      { id: "c", name: "unknown", unresolved: true },
    ],
    edges: [
      { id: "e1", source: "a", target: "b", klass: "deduced", level: "high", count: 1 },
      { id: "e2", source: "b", target: "a", klass: "observed", count: 1 },
      { id: "e3", source: "a", target: "c", klass: "declared", count: 2 },
    ],
    loops: 0,
  };
  const figure = async () => {
    const d = draw(null, g, text, () => undefined);
    const f = await d.vector();
    d.destroy();
    return f;
  };
  const of = <K extends Shape["kind"]>(shapes: Shape[], kind: K) =>
    shapes.filter((s): s is Extract<Shape, { kind: K }> => s.kind === kind);

  it("has a box and a label per device, where the layout put it", async () => {
    const f = await figure();
    const boxes = of(f.shapes, "box");
    expect(boxes).toHaveLength(3);
    const names = of(f.shapes, "text").filter((t) => t.size === 12);
    expect(names.map((t) => t.text)).toEqual(["sw-a1", "srv-02", "unknown"]);
    // Each label is centred in its box, the box as wide as the label and its padding.
    names.forEach((t, i) => {
      const b = boxes[i]!;
      expect(t.at.x).toBeCloseTo(b.x + b.w / 2);
      expect(t.at.y).toBeCloseTo(b.y + b.h / 2);
      expect(b.w).toBeCloseTo(textWidth(t.text, 12) + 16);
    });
    // sw-a1 and srv-02 are side by side on the grid, 140 px apart.
    expect(names[1]!.at.x - names[0]!.at.x).toBeCloseTo(140);
    expect(names[1]!.at.y).toBeCloseTo(names[0]!.at.y);
  });

  it("tells an unresolved device apart: grey, dashed", async () => {
    const boxes = of((await figure()).shapes, "box");
    expect(boxes[0]).toMatchObject({ fill: "#ddf4ff", stroke: { color: "#0550ae" } });
    expect(boxes[0]!.stroke.dash).toBeUndefined();
    expect(boxes[2]).toMatchObject({ fill: "#eaeef2", stroke: { color: "#424a53", dash: [6, 4] } });
  });

  it("draws each kind of link in its own line style and colour", async () => {
    const lines = of((await figure()).shapes, "line");
    expect(lines).toHaveLength(3);
    const byColour = new Map(lines.map((l) => [l.stroke.color, l.stroke]));
    expect(byColour.get(EDGE_STYLE.declared.color)).toEqual({ color: EDGE_STYLE.declared.color, width: 3, dash: undefined });
    expect(byColour.get(EDGE_STYLE.observed.color)).toEqual({ color: EDGE_STYLE.observed.color, width: 2.5, dash: [1, 4] });
    expect(byColour.get(EDGE_STYLE.deduced.color)).toEqual({ color: EDGE_STYLE.deduced.color, width: 2, dash: [6, 4] });
  });

  it("bends two lines between the same devices apart, whichever way they run, and leaves a lone one straight", async () => {
    const lines = of((await figure()).shapes, "line");
    const pair = lines.filter((l) => l.stroke.color !== EDGE_STYLE.declared.color);
    expect(pair.every((l) => l.via)).toBe(true);
    // On opposite sides of the straight line, as far from it each.
    const side = pair.map((l) => l.via!.y - l.from.y);
    expect(side[0]! * side[1]!).toBeLessThan(0);
    expect(Math.abs(side[0]!)).toBeCloseTo(Math.abs(side[1]!));
    expect(lines.find((l) => l.stroke.color === EDGE_STYLE.declared.color)?.via).toBeUndefined();
  });

  it("draws the lines first, then the devices, then the labels of the lines on white", async () => {
    const shapes = (await figure()).shapes;
    const lastLine = shapes.map((s) => s.kind).lastIndexOf("line");
    const firstBox = shapes.findIndex((s) => s.kind === "box");
    expect(lastLine).toBeLessThan(firstBox);
    const labels = of(shapes, "text").filter((t) => t.size === 10);
    expect(labels.map((t) => t.text).sort()).toEqual(["deduced, high", "×2"]);
    expect(labels.every((t) => t.background === "#ffffff")).toBe(true);
    expect(shapes.indexOf(labels[0]!)).toBeGreaterThan(shapes.map((s) => s.kind).lastIndexOf("box"));
  });

  it("puts everything on the page, a margin from the edges", async () => {
    const f = await figure();
    const xs: number[] = [];
    const ys: number[] = [];
    for (const s of f.shapes) {
      if (s.kind === "box") {
        xs.push(s.x, s.x + s.w);
        ys.push(s.y, s.y + s.h);
      }
      if (s.kind === "line") {
        for (const p of [s.from, s.to, s.via]) {
          if (!p) continue;
          xs.push(p.x);
          ys.push(p.y);
        }
      }
    }
    expect(Math.min(...xs)).toBeCloseTo(20);
    expect(Math.min(...ys)).toBeGreaterThanOrEqual(19.99);
    expect(Math.max(...xs)).toBeLessThanOrEqual(f.width - 20 + 0.01);
    expect(Math.max(...ys)).toBeLessThanOrEqual(f.height - 20 + 0.01);
  });

  it("is an empty sheet for an empty graph", async () => {
    const d = draw(null, { nodes: [], edges: [], loops: 0 }, text, () => undefined);
    expect(await d.vector()).toEqual({ width: 40, height: 40, shapes: [] });
    expect(figureOf(d.cy).shapes).toEqual([]);
    d.destroy();
  });
});
