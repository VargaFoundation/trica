// The drawing of the topology: cytoscape for the canvas and the interaction,
// ELK (layered) for the layout. This file is the only one that touches them.
//
// The graph sits on its own white "paper" with a fixed light palette, whatever
// the theme: an exported picture is then the screen's, and nothing here reads
// the page's CSS variables. What tells the three kinds of link apart is the
// line (solid, dotted, dashed) and the label, never the colour alone.

import cytoscape, {
  type Core,
  type ElementDefinition,
  type StylesheetJson,
} from "cytoscape";
import elk from "cytoscape-elk";
import type { Raster } from "../export/download";
import { textWidth, type Figure, type Shape } from "../export/vectorPdf";
import type { Graph, LinkClass } from "./model";

let registered = false;
function register() {
  if (registered) return;
  cytoscape.use(elk);
  registered = true;
}

/** Tests run without a canvas and without ELK: they draw headless, with a preset layout. */
export const graphEnv: { headless: boolean; layout: "elk" | "preset" } = {
  headless: false,
  layout: "elk",
};

export const PAPER = "#ffffff";

export const EDGE_STYLE: Record<
  LinkClass,
  { color: string; style: "solid" | "dotted" | "dashed"; width: number }
> = {
  declared: { color: "#1f2328", style: "solid", width: 3 },
  observed: { color: "#0550ae", style: "dotted", width: 2.5 },
  deduced: { color: "#9a5b00", style: "dashed", width: 2 },
};

/** Text of an edge: the level of a deduced link, the number of links when there are several. */
export function edgeLabel(
  e: { klass: LinkClass; level?: string; count: number },
  text: EdgeText,
): string {
  const parts: string[] = [];
  if (e.klass === "deduced")
    parts.push(e.level ? text.level(e.level) : text.deduced);
  if (e.count > 1) parts.push(`×${e.count}`);
  return parts.join(" ");
}

export interface EdgeText {
  /** Label of a deduced link whose level is unknown. */
  deduced: string;
  level: (l: string) => string;
}

export function toElements(g: Graph, text: EdgeText): ElementDefinition[] {
  return [
    ...g.nodes.map((n) => ({
      group: "nodes" as const,
      data: { id: n.id, label: n.name, unresolved: n.unresolved },
    })),
    ...g.edges.map((e) => ({
      group: "edges" as const,
      data: {
        id: e.id,
        source: e.source,
        target: e.target,
        klass: e.klass,
        level: e.level ?? "",
        count: e.count,
        label: edgeLabel(e, text),
      },
    })),
  ];
}

const stylesheet: StylesheetJson = [
  {
    selector: "node",
    style: {
      label: "data(label)",
      "font-family": "system-ui, sans-serif",
      "font-size": 12,
      color: "#1f2328",
      "text-valign": "center",
      "text-halign": "center",
      shape: "round-rectangle",
      width: "label",
      height: 26,
      padding: "8px",
      "background-color": "#ddf4ff",
      "border-width": 1.5,
      "border-color": "#0550ae",
    },
  },
  {
    selector: "node[?unresolved]",
    style: {
      "border-style": "dashed",
      "background-color": "#eaeef2",
      "border-color": "#424a53",
    },
  },
  { selector: "node:selected", style: { "border-width": 3 } },
  {
    selector: "edge",
    style: {
      label: "data(label)",
      "font-family": "system-ui, sans-serif",
      "font-size": 10,
      color: "#1f2328",
      "text-background-color": PAPER,
      "text-background-opacity": 1,
      "text-background-padding": "2px",
      "curve-style": "bezier",
      "target-arrow-shape": "none",
    },
  },
  ...(Object.keys(EDGE_STYLE) as LinkClass[]).map((k) => ({
    selector: `edge[klass = "${k}"]`,
    style: {
      "line-color": EDGE_STYLE[k].color,
      "line-style": EDGE_STYLE[k].style,
      width: EDGE_STYLE[k].width,
    },
  })),
];

export function elkOptions(nodes: number) {
  return {
    name: "elk",
    fit: true,
    padding: 24,
    animate: false,
    nodeDimensionsIncludeLabels: true,
    elk: {
      algorithm: "layered",
      "elk.direction": "DOWN",
      "elk.layered.spacing.nodeNodeBetweenLayers": 60,
      "elk.spacing.nodeNode": 28,
      // The default (7) is for small graphs; past a hundred nodes the time is better spent elsewhere.
      "elk.layered.thoroughness": nodes > 100 ? 2 : 7,
    },
  };
}

/** A grid: the layout of the tests and of the graph's fall-back when ELK fails. */
function gridPositions(g: Graph): Record<string, { x: number; y: number }> {
  const cols = Math.max(1, Math.ceil(Math.sqrt(g.nodes.length)));
  return Object.fromEntries(
    g.nodes.map((n, i) => [
      n.id,
      { x: (i % cols) * 140, y: Math.floor(i / cols) * 70 },
    ]),
  );
}

export interface Drawing {
  cy: Core;
  /** Resolves when the layout has run (and been fitted). */
  ready: Promise<void>;
  destroy(): void;
  /** The whole graph as an image, on white. */
  raster(format: "png" | "jpeg"): Promise<Raster>;
  /** The whole graph as lines, boxes and text, for a vector PDF. */
  vector(): Promise<Figure>;
}

/** The largest image exported: beyond it browsers refuse a canvas. */
export const MAX_PIXELS = 16_000_000;
export const MAX_SIDE = 8000;

/** The ratio an image of this size is drawn at: 2 when it fits, less when it would be too large, never above 2. */
export function exportRatio(width: number, height: number): number {
  const w = Math.max(width, 1);
  const h = Math.max(height, 1);
  return Math.min(
    2,
    Math.sqrt(MAX_PIXELS / (w * h)),
    MAX_SIDE / Math.max(w, h),
  );
}

/** The id under which cytoscape looks for the one rule it injects (`.__________cytoscape_container { position: relative }`). */
export const CYTOSCAPE_STYLE_ID = "__________cytoscape_stylesheet";

/**
 * Cytoscape adds an inline <style> to the page unless an element with that id
 * is already there. The page's policy (style-src 'self') refuses inline
 * styles, and the same rule is in app.css, so a placeholder stands in: the
 * policy stays as strict as it is.
 */
export function claimCytoscapeStyle(doc: Document): void {
  if (doc.getElementById(CYTOSCAPE_STYLE_ID)) return;
  const m = doc.createElement("meta");
  m.id = CYTOSCAPE_STYLE_ID;
  m.name = "trica-cytoscape-style";
  m.content = "the rule is in the application's stylesheet";
  doc.head.append(m);
}

const NODE_FONT = 12;
const NODE_PAD = 8;
const NODE_HEIGHT = 26;
const EDGE_FONT = 10;
/** How far apart two lines between the same devices are drawn: cytoscape's own step. */
const PARALLEL_STEP = 40;
const DASH: Record<"solid" | "dotted" | "dashed", number[] | undefined> = {
  solid: undefined,
  dotted: [1, 4],
  dashed: [6, 4],
};
const MARGIN = 20;

/**
 * The drawing as shapes: the nodes where the layout put them, the lines from
 * centre to centre, the lines between the same two devices bent apart as on
 * the screen, the labels on white. The boxes are sized for the PDF's font
 * (Helvetica), not measured on the screen, so that a headless drawing gives
 * the same figure.
 */
export function figureOf(cy: Core): Figure {
  const nodes = cy.nodes().map((n) => {
    const label = String(n.data("label") ?? "");
    const { x, y } = n.position();
    const w = textWidth(label, NODE_FONT) + 2 * NODE_PAD;
    return { id: n.id(), label, x, y, w, h: NODE_HEIGHT, unresolved: Boolean(n.data("unresolved")) };
  });
  const at = new Map(nodes.map((n) => [n.id, n]));

  // Lines between the same pair, whatever their direction, are fanned out.
  const pairs = new Map<string, cytoscape.EdgeSingular[]>();
  cy.edges().forEach((e) => {
    const key = [e.source().id(), e.target().id()].sort().join("\u0000");
    pairs.set(key, [...(pairs.get(key) ?? []), e]);
  });

  const lines: Shape[] = [];
  const labels: Shape[] = [];
  const bounds: { x: number; y: number }[] = nodes.flatMap((n) => [
    { x: n.x - n.w / 2, y: n.y - n.h / 2 },
    { x: n.x + n.w / 2, y: n.y + n.h / 2 },
  ]);
  for (const group of pairs.values()) {
    group.sort((a, b) => (a.id() < b.id() ? -1 : 1));
    group.forEach((e, i) => {
      // Oriented the same way for the whole group, so that the offsets do not cancel out.
      const [a, b] = [e.source().id(), e.target().id()].sort().map((id) => at.get(id));
      if (!a || !b) return;
      const from = { x: a.x, y: a.y };
      const to = { x: b.x, y: b.y };
      const len = Math.hypot(to.x - from.x, to.y - from.y) || 1;
      const offset = (i - (group.length - 1) / 2) * PARALLEL_STEP;
      // The curve's middle sits `offset` away from the straight line, its control point twice as far.
      const mid = {
        x: (from.x + to.x) / 2 - ((to.y - from.y) / len) * offset,
        y: (from.y + to.y) / 2 + ((to.x - from.x) / len) * offset,
      };
      const via = offset === 0 ? undefined : { x: 2 * mid.x - (from.x + to.x) / 2, y: 2 * mid.y - (from.y + to.y) / 2 };
      const style = EDGE_STYLE[e.data("klass") as LinkClass] ?? EDGE_STYLE.declared;
      lines.push({ kind: "line", from, to, via, stroke: { color: style.color, width: style.width, dash: DASH[style.style] } });
      if (via) bounds.push(via);
      const label = String(e.data("label") ?? "");
      if (label) {
        labels.push({ kind: "text", at: mid, text: label, size: EDGE_FONT, color: "#1f2328", background: PAPER });
        const half = textWidth(label, EDGE_FONT) / 2 + 2;
        bounds.push({ x: mid.x - half, y: mid.y - EDGE_FONT }, { x: mid.x + half, y: mid.y + EDGE_FONT });
      }
    });
  }

  const boxes: Shape[] = nodes.flatMap((n): Shape[] => [
    {
      kind: "box",
      x: n.x - n.w / 2,
      y: n.y - n.h / 2,
      w: n.w,
      h: n.h,
      radius: 6,
      fill: n.unresolved ? "#eaeef2" : "#ddf4ff",
      stroke: { color: n.unresolved ? "#424a53" : "#0550ae", width: 1.5, dash: n.unresolved ? DASH.dashed : undefined },
    },
    { kind: "text", at: { x: n.x, y: n.y }, text: n.label, size: NODE_FONT, color: "#1f2328" },
  ]);

  // The page starts a margin away from the leftmost and topmost ink.
  const xs = bounds.map((p) => p.x);
  const ys = bounds.map((p) => p.y);
  const [minX, maxX] = xs.length ? [Math.min(...xs), Math.max(...xs)] : [0, 0];
  const [minY, maxY] = ys.length ? [Math.min(...ys), Math.max(...ys)] : [0, 0];
  const dx = MARGIN - minX;
  const dy = MARGIN - minY;
  const move = (p: { x: number; y: number }) => ({ x: p.x + dx, y: p.y + dy });
  const shapes = [...lines, ...boxes, ...labels].map((sh): Shape => {
    if (sh.kind === "line") return { ...sh, from: move(sh.from), to: move(sh.to), via: sh.via && move(sh.via) };
    if (sh.kind === "box") return { ...sh, x: sh.x + dx, y: sh.y + dy };
    return { ...sh, at: move(sh.at) };
  });
  return {
    width: maxX - minX + 2 * MARGIN,
    height: maxY - minY + 2 * MARGIN,
    shapes,
  };
}

export function draw(
  container: HTMLElement | null,
  g: Graph,
  text: EdgeText,
  onOpen: (id: string) => void,
): Drawing {
  register();
  if (!graphEnv.headless && container)
    claimCytoscapeStyle(container.ownerDocument);
  const cy = cytoscape({
    container: graphEnv.headless ? undefined : container,
    headless: graphEnv.headless,
    // A headless graph computes no style unless asked; the tests read it.
    styleEnabled: true,
    elements: toElements(g, text),
    style: stylesheet,
    layout: { name: "preset" },
    // The page has its own keyboard alternative (the table): the canvas takes no focus and no scroll.
    userZoomingEnabled: true,
    userPanningEnabled: true,
    boxSelectionEnabled: false,
    autoungrabify: true,
    minZoom: 0.05,
    maxZoom: 3,
  });
  cy.on("tap", "node", (e) => onOpen(e.target.id() as string));
  let ready: Promise<void>;
  if (graphEnv.layout === "preset" || g.nodes.length === 0) {
    const pos = gridPositions(g);
    cy.nodes().forEach((n) => void n.position(pos[n.id()] ?? { x: 0, y: 0 }));
    ready = Promise.resolve();
  } else {
    ready = new Promise<void>((resolve, reject) => {
      const layout = cy.layout(
        elkOptions(g.nodes.length) as cytoscape.LayoutOptions,
      );
      layout.one("layoutstop", () => resolve());
      try {
        layout.run();
      } catch (err) {
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    });
  }
  return {
    cy,
    ready,
    destroy: () => cy.destroy(),
    raster: async (format) => {
      await ready;
      const bb = cy.elements().boundingBox();
      const ratio = exportRatio(bb.w + 2 * 20, bb.h + 2 * 20);
      const opts = {
        full: true,
        bg: PAPER,
        scale: ratio,
        output: "blob-promise" as const,
      };
      const blob =
        format === "png"
          ? await cy.png(opts)
          : await cy.jpg({ ...opts, quality: 0.92 });
      return {
        blob,
        width: Math.ceil(bb.w * ratio),
        height: Math.ceil(bb.h * ratio),
        ratio,
      };
    },
    vector: async () => {
      await ready;
      return figureOf(cy);
    },
  };
}
