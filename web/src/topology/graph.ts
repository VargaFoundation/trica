// The drawing of the topology: cytoscape for the canvas and the interaction,
// ELK (layered) for the layout. This file is the only one that touches them.
//
// On the screen the graph follows the theme: its colours are the page's
// --trica-graph-* tokens (styles/tokens.css), read when it is drawn and again
// when the theme changes. An exported picture or PDF is always on white paper
// with the fixed light palette below, whatever the theme. What tells the three
// kinds of link apart is the line (solid, dotted, dashed) and the label, never
// the colour alone.

import cytoscape, {
  type Core,
  type ElementDefinition,
  type StylesheetJson,
} from "cytoscape";
import elk from "cytoscape-elk";
import type { Raster } from "../export/download";
import { textWidth, type Figure, type Shape } from "../export/vectorPdf";
import { kindOf } from "../inventory/kind";
import { TOPOLOGY_ICONS, iconUrl, type IconTheme } from "./icons";
import type { Graph, LinkClass } from "./model";
import type { UtilClass } from "./live";

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

/** The colours of a drawing. */
export interface Palette {
  paper: string;
  ink: string;
  node: string;
  nodeLine: string;
  unresolved: string;
  unresolvedLine: string;
  declared: string;
  observed: string;
  deduced: string;
  /** Live lines (ADR 0041): by utilisation, down, not measured. */
  utilNormal: string;
  utilWarn: string;
  utilCrit: string;
  down: string;
  utilUnknown: string;
  /** A device's border by its worst health. */
  healthOk: string;
  healthWarn: string;
  healthCrit: string;
  /** The box around an MLAG pair. */
  group: string;
  groupLine: string;
}

/** The exports' palette, and the screen's when the page's tokens cannot be read. */
export const PAPER_PALETTE: Palette = {
  paper: PAPER,
  ink: "#1f2328",
  node: "#ddf4ff",
  nodeLine: "#0550ae",
  unresolved: "#eaeef2",
  unresolvedLine: "#424a53",
  declared: "#1f2328",
  observed: "#0550ae",
  deduced: "#9a5b00",
  utilNormal: "#60646c",
  utilWarn: "#8f560a",
  utilCrit: "#ce2c31",
  down: "#ce2c31",
  utilUnknown: "#8b8d98",
  healthOk: "#1f6d4b",
  healthWarn: "#8f560a",
  healthCrit: "#ce2c31",
  group: "#f9f9fb",
  groupLine: "#8b8d98",
};

const TOKENS: Record<keyof Palette, string> = {
  paper: "--trica-graph-paper",
  ink: "--trica-graph-ink",
  node: "--trica-graph-node",
  nodeLine: "--trica-graph-node-line",
  unresolved: "--trica-graph-unresolved",
  unresolvedLine: "--trica-graph-unresolved-line",
  declared: "--trica-graph-declared",
  observed: "--trica-graph-observed",
  deduced: "--trica-graph-deduced",
  utilNormal: "--trica-graph-util-normal",
  utilWarn: "--trica-graph-util-warn",
  utilCrit: "--trica-graph-util-crit",
  down: "--trica-graph-down",
  utilUnknown: "--trica-graph-util-unknown",
  healthOk: "--trica-graph-health-ok",
  healthWarn: "--trica-graph-health-warn",
  healthCrit: "--trica-graph-health-crit",
  group: "--trica-graph-group",
  groupLine: "--trica-graph-group-line",
};

/** The icon set of the screen: the one drawn for the theme's colour scheme (styles/tokens.css sets color-scheme). */
export function screenIconTheme(doc: Document = document): IconTheme {
  const scheme = doc.defaultView?.getComputedStyle(doc.documentElement).colorScheme ?? "";
  return scheme.includes("light") ? "light" : "dark";
}

/** The screen's palette: the page's --trica-graph-* tokens as the theme resolves them now. */
export function screenPalette(doc: Document = document): Palette {
  const css = doc.defaultView?.getComputedStyle(doc.documentElement);
  const out = { ...PAPER_PALETTE };
  for (const k of Object.keys(TOKENS) as (keyof Palette)[]) {
    const v = css?.getPropertyValue(TOKENS[k]).trim();
    if (v) out[k] = v;
  }
  return out;
}

/** How each class of link is drawn in the exports (and the line style everywhere). */
export const EDGE_STYLE: Record<
  LinkClass,
  { color: string; style: "solid" | "dotted" | "dashed"; width: number }
> = {
  declared: { color: PAPER_PALETTE.declared, style: "solid", width: 3 },
  observed: { color: PAPER_PALETTE.observed, style: "dotted", width: 2.5 },
  deduced: { color: PAPER_PALETTE.deduced, style: "dashed", width: 2 },
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
  /** Label of an aggregate's line ("Po11 (2×10G)"). */
  lag?: (e: Graph["edges"][number]) => string;
  /** Label of the box around an MLAG pair. */
  group?: (domain: string) => string;
}

/** The label of a device: its name, and "⚠ n" when its event log holds critical entries. */
export function nodeLabel(n: { name: string; critical?: number }): string {
  return n.critical && n.critical > 0 ? `${n.name}  ⚠ ${n.critical}` : n.name;
}

const NODE_ICON = 16;

export function toElements(g: Graph, text: EdgeText): ElementDefinition[] {
  const parentOf = new Map<string, string>();
  for (const grp of g.groups ?? []) for (const m of grp.members) parentOf.set(m, grp.id);
  return [
    ...(g.groups ?? []).map((grp) => ({
      group: "nodes" as const,
      data: { id: grp.id, label: text.group ? text.group(grp.domain) : grp.domain, compound: true },
    })),
    ...g.nodes.map((n) => {
      const label = nodeLabel(n);
      const icon = kindOf({ type: "device", attributes: { role: n.role } }).icon;
      return {
        group: "nodes" as const,
        data: {
          id: n.id,
          label,
          name: n.name,
          unresolved: n.unresolved,
          parent: parentOf.get(n.id),
          icon: (TOPOLOGY_ICONS as readonly string[]).includes(icon) ? icon : "device",
          health: n.health ?? "unknown",
          // Room for the icon on the left of the name: cytoscape's "label" width does not count it.
          w: Math.ceil(textWidth(label, NODE_FONT) + 2 * NODE_PAD + NODE_ICON + 6),
        },
      };
    }),
    ...g.edges.map((e) => ({
      group: "edges" as const,
      data: {
        id: e.id,
        source: e.source,
        target: e.target,
        klass: e.klass,
        level: e.level ?? "",
        count: e.count,
        label: e.lag?.folded ? (text.lag?.(e) ?? e.lag.name) : edgeLabel(e, text),
        lag: e.lag?.group ?? "",
      },
    })),
  ];
}

/** The live classes of a line (applyLive), each with its colour. */
export const UTIL_CLASSES: readonly UtilClass[] = ["normal", "warn", "crit", "down", "unknown"];

/** The cytoscape style of a palette; device icons from the set of a theme. */
export function stylesheetFor(p: Palette, icons: IconTheme = "light"): StylesheetJson {
  const utilColor: Record<UtilClass, string> = {
    normal: p.utilNormal,
    warn: p.utilWarn,
    crit: p.utilCrit,
    down: p.down,
    unknown: p.utilUnknown,
  };
  return [
  {
    selector: "node",
    style: {
      label: "data(label)",
      "font-family": "system-ui, sans-serif",
      "font-size": 12,
      color: p.ink,
      "text-valign": "center",
      "text-halign": "center",
      shape: "round-rectangle",
      width: "label",
      height: 26,
      padding: "8px",
      "background-color": p.node,
      "border-width": 1.5,
      "border-color": p.nodeLine,
    },
  },
  {
    selector: "node[w]",
    style: {
      width: "data(w)",
      padding: "0px",
      "text-margin-x": (NODE_ICON + 6) / 2,
      "background-width": NODE_ICON,
      "background-height": NODE_ICON,
      "background-position-x": NODE_PAD,
      "background-position-y": "50%",
      "background-clip": "none",
      "background-fit": "none",
    },
  },
  ...TOPOLOGY_ICONS.map((icon) => ({
    selector: `node[icon = "${icon}"]`,
    style: { "background-image": iconUrl(icons, icon) },
  })),
  { selector: 'node[health = "ok"]', style: { "border-color": p.healthOk } },
  { selector: 'node[health = "warning"]', style: { "border-color": p.healthWarn, "border-width": 2.5 } },
  { selector: 'node[health = "critical"]', style: { "border-color": p.healthCrit, "border-width": 3 } },
  {
    selector: "node[?compound]",
    style: {
      shape: "round-rectangle",
      "background-color": p.group,
      "background-opacity": 1,
      "border-color": p.groupLine,
      "border-style": "dashed",
      "border-width": 1,
      "text-valign": "top",
      "text-halign": "center",
      "font-size": 10,
      padding: "10px",
      width: "auto",
      height: "auto",
    },
  },
  {
    selector: "node[?unresolved]",
    style: {
      "border-style": "dashed",
      "background-color": p.unresolved,
      "border-color": p.unresolvedLine,
    },
  },
  { selector: "node:selected", style: { "border-width": 3 } },
  {
    selector: "edge",
    style: {
      label: "data(label)",
      "font-family": "system-ui, sans-serif",
      "font-size": 10,
      color: p.ink,
      "text-background-color": p.paper,
      "text-background-opacity": 1,
      "text-background-padding": "2px",
      "curve-style": "bezier",
      "target-arrow-shape": "none",
    },
  },
  ...(Object.keys(EDGE_STYLE) as LinkClass[]).map((k) => ({
    selector: `edge[klass = "${k}"]`,
    style: {
      "line-color": p[k],
      "line-style": EDGE_STYLE[k].style,
      width: EDGE_STYLE[k].width,
    },
  })),
  // The live overlay: the width says the throughput, the colour the utilisation; the line style still says the confidence.
  { selector: "edge.live", style: { width: "data(lw)" } },
  ...UTIL_CLASSES.map((c) => ({ selector: `edge.live-${c}`, style: { "line-color": utilColor[c] } })),
  // Down is not told by the colour alone: a bar across each end.
  {
    selector: "edge.live-down",
    style: { "source-arrow-shape": "tee", "target-arrow-shape": "tee", "source-arrow-color": p.down, "target-arrow-color": p.down },
  },
  { selector: "edge.has-live.show-live", style: { label: "data(liveLabel)" } },
  { selector: "edge:selected", style: { "underlay-color": p.nodeLine, "underlay-opacity": 0.25, "underlay-padding": 4 } },
  {
    selector: "edge.flowing",
    style: {
      "underlay-color": p.observed,
      "underlay-padding": 3,
      "underlay-opacity": 0,
      "transition-property": "underlay-opacity",
      "transition-duration": 900,
    },
  },
  { selector: "edge.flowing.pulse", style: { "underlay-opacity": 0.35 } },
];
}

export function elkOptions(nodes: number, groups = 0) {
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
      // MLAG boxes: one layout across the levels, so that a line leaving a box is routed with the others, and room inside it.
      ...(groups > 0 ? { "elk.hierarchyHandling": "INCLUDE_CHILDREN", "elk.padding": "[top=24,left=16,bottom=16,right=16]", "elk.spacing.nodeNode": 56 } : {}),
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
  /** Lays the live state over the lines, without moving anything; null takes it away. */
  live(update: ReadonlyMap<string, LiveStyle> | null): void;
  /** Turns the flow pulse on or off (never on when the reader asked for reduced motion). */
  flow(on: boolean): void;
  /** Selects one element (a node or a line), or nothing. */
  select(id: string | null): void;
}

/** What the live overlay sets on one line. */
export interface LiveStyle {
  util: UtilClass;
  /** Width, px (widthOf). */
  width: number;
  /** "↑ 3.2 Gb/s ↓ 1.1 Gb/s"; empty when nothing is measured. */
  label: string;
  /** A new label for the line itself (an aggregate's "Po11 (2×10G)"). */
  title?: string;
  /** Some traffic: the pulse may show it. */
  flowing: boolean;
}

/** What the drawing reports besides a click on a device. */
export interface DrawHandlers {
  /** A line was clicked. */
  onEdge?: (id: string) => void;
  /** A line was double-clicked (an aggregate unfolds or folds). */
  onEdgeToggle?: (id: string) => void;
  /** The background was clicked: nothing is selected any more. */
  onClear?: () => void;
}

/** A small graph is not blown up to fill the canvas: fitted, it stays at most this large. */
export const MAX_FIT_ZOOM = 1.6;

/** Past this zoom, every measured line shows its throughput, not only the one under the pointer. */
export const LABEL_ZOOM = 1.8;

/** True when the reader asked the system for less motion. */
export function prefersReducedMotion(win: Window | null | undefined = typeof window === "undefined" ? undefined : window): boolean {
  return win?.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
}

/** Applies a live update to a graph's lines; exported for the headless tests. */
export function applyLive(cy: Core, update: ReadonlyMap<string, LiveStyle> | null): void {
  cy.batch(() => {
    cy.edges().forEach((e) => {
      e.removeClass(["live", "has-live", "flowing", "pulse", ...UTIL_CLASSES.map((c) => `live-${c}`)]);
      const base = e.data("baseLabel") as string | undefined;
      if (base !== undefined) e.data("label", base);
      const l = update?.get(e.id());
      if (!l) return;
      if (base === undefined) e.data("baseLabel", e.data("label"));
      if (l.title !== undefined) e.data("label", l.title);
      e.data({ lw: l.width, liveLabel: l.label });
      e.addClass(["live", `live-${l.util}`]);
      if (l.label) e.addClass("has-live");
      if (l.flowing) e.addClass("flowing");
    });
  });
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
  const nodes = cy.nodes().filter((n) => !n.data("compound")).toArray().map((el) => {
    const n = el as cytoscape.NodeSingular;
    // The name, without the event badge: the PDF's Helvetica has no warning sign.
    const label = String(n.data("name") ?? n.data("label") ?? "");
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
        labels.push({ kind: "text", at: mid, text: label, size: EDGE_FONT, color: PAPER_PALETTE.ink, background: PAPER });
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
      fill: n.unresolved ? PAPER_PALETTE.unresolved : PAPER_PALETTE.node,
      stroke: { color: n.unresolved ? PAPER_PALETTE.unresolvedLine : PAPER_PALETTE.nodeLine, width: 1.5, dash: n.unresolved ? DASH.dashed : undefined },
    },
    { kind: "text", at: { x: n.x, y: n.y }, text: n.label, size: NODE_FONT, color: PAPER_PALETTE.ink },
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
  handlers: DrawHandlers = {},
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
    style: stylesheetFor(screenPalette(container?.ownerDocument), container ? screenIconTheme(container.ownerDocument) : "light"),
    layout: { name: "preset" },
    // The page has its own keyboard alternative (the table): the canvas takes no focus and no scroll.
    userZoomingEnabled: true,
    userPanningEnabled: true,
    boxSelectionEnabled: false,
    autoungrabify: true,
    minZoom: 0.05,
    maxZoom: 3,
  });
  // A device opens; the box of an MLAG pair is not a device.
  cy.on("tap", "node", (e) => {
    if (!e.target.data("compound")) onOpen(e.target.id() as string);
  });
  cy.on("tap", "edge", (e) => handlers.onEdge?.(e.target.id() as string));
  cy.on("dbltap", "edge", (e) => handlers.onEdgeToggle?.(e.target.id() as string));
  cy.on("tap", (e) => {
    if (e.target === cy) handlers.onClear?.();
  });
  // A line's throughput shows under the pointer, and on every line once zoomed in.
  cy.on("mouseover", "edge", (e) => void e.target.addClass("show-live"));
  cy.on("mouseout", "edge", (e) => {
    if (cy.zoom() < LABEL_ZOOM) e.target.removeClass("show-live");
  });
  cy.on("zoom", () => {
    if (cy.zoom() >= LABEL_ZOOM) cy.edges().addClass("show-live");
    else cy.edges().removeClass("show-live");
  });
  // Follow the theme: the reader's choice (data-theme) or the system's.
  const doc = container?.ownerDocument;
  const restyle = () => void cy.style(stylesheetFor(screenPalette(doc), doc ? screenIconTheme(doc) : "light"));
  // The canvas narrows when the details open beside it: the picture is fitted to its new size.
  const fitCapped = () => {
    cy.resize();
    cy.fit(undefined, 24);
    if (cy.zoom() > MAX_FIT_ZOOM) {
      cy.zoom(MAX_FIT_ZOOM);
      cy.center();
    }
  };
  let lastWidth = container?.clientWidth ?? 0;
  const resizer =
    !graphEnv.headless && container && typeof ResizeObserver !== "undefined"
      ? new ResizeObserver(() => {
          if (container.clientWidth === lastWidth) return;
          lastWidth = container.clientWidth;
          fitCapped();
        })
      : undefined;
  if (container) resizer?.observe(container);
  let pulse: ReturnType<typeof setInterval> | undefined;
  const stopFlow = () => {
    if (pulse !== undefined) clearInterval(pulse);
    pulse = undefined;
    cy.edges().removeClass("pulse");
  };
  const observer = doc && typeof MutationObserver !== "undefined" ? new MutationObserver(restyle) : undefined;
  if (doc) observer?.observe(doc.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  const media = doc?.defaultView?.matchMedia?.("(prefers-color-scheme: light)");
  media?.addEventListener?.("change", restyle);
  let ready: Promise<void>;
  if (graphEnv.layout === "preset" || g.nodes.length === 0) {
    const pos = gridPositions(g);
    cy.nodes().forEach((n) => void n.position(pos[n.id()] ?? { x: 0, y: 0 }));
    ready = Promise.resolve();
  } else {
    ready = new Promise<void>((resolve, reject) => {
      const layout = cy.layout(
        elkOptions(g.nodes.length, g.groups?.length ?? 0) as cytoscape.LayoutOptions,
      );
      layout.one("layoutstop", () => {
        if (cy.zoom() > MAX_FIT_ZOOM) {
          cy.zoom(MAX_FIT_ZOOM);
          cy.center();
        }
        resolve();
      });
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
    live: (update) => applyLive(cy, update),
    flow: (on) => {
      stopFlow();
      if (!on || prefersReducedMotion(doc?.defaultView)) return;
      pulse = setInterval(() => void cy.edges(".flowing").toggleClass("pulse"), 900);
    },
    select: (id) => {
      cy.elements().unselect();
      if (id) cy.getElementById(id).select();
    },
    destroy: () => {
      stopFlow();
      resizer?.disconnect();
      observer?.disconnect();
      media?.removeEventListener?.("change", restyle);
      cy.destroy();
    },
    raster: async (format) => {
      await ready;
      const bb = cy.elements().boundingBox();
      const ratio = exportRatio(bb.w + 2 * 20, bb.h + 2 * 20);
      // The size is given as maxWidth/maxHeight, not as `scale`: cytoscape
      // multiplies a scale by the screen's pixel ratio, so on a high-density
      // screen (2 on a Retina display) the image would have four times the
      // pixels allowed, and Safari refuses a canvas that large.
      const opts = {
        full: true,
        bg: PAPER,
        maxWidth: Math.ceil(bb.w * ratio),
        maxHeight: Math.ceil(bb.h * ratio),
        output: "blob-promise" as const,
      };
      // The picture is on paper whatever the theme: draw it with the paper palette, then put the screen's back.
      cy.style(stylesheetFor(PAPER_PALETTE, "light"));
      let blob: Blob;
      try {
        blob =
          format === "png"
            ? await cy.png(opts)
            : await cy.jpg({ ...opts, quality: 0.92 });
      } finally {
        restyle();
      }
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
