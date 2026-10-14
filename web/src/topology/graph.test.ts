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
  graphEnv,
  MAX_PIXELS,
  MAX_SIDE,
  toElements,
} from "./graph";
import { buildTopology, toGraph } from "./model";

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
