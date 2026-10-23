// The drawn scene of a map level: every box turned and projected, the edges as
// arcs between anchors, depth-ordered. Pure data, no React: IsoScene.tsx only
// paints it.

import type { MapEdge, MapNode, TopologyMap } from "../../api/types";
import { isoBox, project, turnPoint, turnRect, union, viewBoxOf, type Bounds, type IsoBox, type Pt, type Rot } from "./iso";
import { isGate, layoutWorld, UZ, type ItemRole, type Plate, type WorldItem, type WorldLayout } from "./layout";
import { edgeUtilClass, type Direction } from "./flow";

export interface SceneItem {
  id: string;
  node: MapNode;
  role: ItemRole;
  iso: IsoBox;
  minis: IsoBox[];
  /** Little triangle on top of a rack pointing the way it faces. */
  arrow?: string;
  /** Where lines meet the item. */
  port: Pt;
  label: string;
  sub?: string;
  /** Screen position of the label: below the item, or beside the rack for a device. */
  labelAt: Pt;
  /** The label is written at its start (beside the rack) rather than centred. */
  labelStart: boolean;
  /** The label is always shown; otherwise only when the item is hovered or focused. */
  labelShown: boolean;
  /** The caption under the label is drawn; dropped when it would run into a neighbour's label. */
  subShown: boolean;
  /** Items the visitor can open a level from. */
  drillable: boolean;
  /** World centre, for spatial keyboard navigation (turned). */
  centre: Pt;
  /** The posts and lintel of a gate (portals and the Internet). */
  parts: IsoBox[];
  /** A caption exists for this item (its sub text, or a summary the page writes). */
  hasSub: boolean;
  /** The label's pill, in screen px, once placed. */
  pill: Bounds;
}

export interface SceneFrame {
  /** Posts behind the devices, and those in front of them. */
  back: IsoBox[];
  front: IsoBox[];
  /** The top rails, as polygon points. */
  top: string;
  /** U ticks along the front-left post, and the numbers of some of them. */
  ticks: string;
  marks: { x: number; y: number; text: string }[];
}

export interface ScenePlate {
  id: string;
  iso: IsoBox;
  grid: string[];
  dashed: boolean;
  label?: string;
}

export interface SceneLane {
  dir: Direction;
  d: string;
}

export interface SceneEdge {
  edge: MapEdge;
  /** The path of the line's centre (hit target and base stroke). */
  d: string;
  lanes: SceneLane[];
  util: ReturnType<typeof edgeUtilClass>;
  mid: Pt;
}

export interface Scene {
  items: SceneItem[];
  plates: ScenePlate[];
  edges: SceneEdge[];
  bounds: Bounds;
  viewBox: string;
  units?: number;
  frame?: SceneFrame;
}

const fmt = (n: number) => Math.round(n * 10) / 10;

function plateIso(p: Plate, layout: WorldLayout, rot: Rot): ScenePlate {
  const world = { w: layout.w, d: layout.d };
  const iso = isoBox(p.box, world, rot);
  const grid: string[] = [];
  if (p.cols && p.rows) {
    const r = turnRect(p.box, world, rot);
    const at = (x: number, y: number) => project(x, y, p.box.z + p.box.h);
    const cell = (a: number, b: number, c: number, d: number) => {
      const s = at(a, b);
      const e = at(c, d);
      return `M${fmt(s.x)},${fmt(s.y)}L${fmt(e.x)},${fmt(e.y)}`;
    };
    for (let i = 1; i < r.w; i++) grid.push(cell(r.x + i, r.y, r.x + i, r.y + r.d));
    for (let j = 1; j < r.d; j++) grid.push(cell(r.x, r.y + j, r.x + r.w, r.y + j));
  }
  return { id: p.id, iso, grid, dashed: !!p.dashed, label: p.label };
}

function itemIso(it: WorldItem, layout: WorldLayout, rot: Rot): SceneItem {
  const world = { w: layout.w, d: layout.d };
  const iso = isoBox(it.box, world, rot);
  const b = it.box;
  const minis: IsoBox[] = [];
  if (it.minis > 0) {
    const cols = Math.ceil(Math.sqrt(it.minis * (b.w / b.d)));
    const rows = Math.ceil(it.minis / cols);
    const pad = 0.5;
    const cw = (b.w - pad * 2) / cols;
    const cd = (b.d - pad * 2) / rows;
    for (let i = 0; i < it.minis; i++) {
      const cx = b.x + pad + (i % cols) * cw + cw * 0.15;
      const cy = b.y + pad + Math.floor(i / cols) * cd + cd * 0.15;
      minis.push(isoBox({ x: cx, y: cy, w: cw * 0.7, d: cd * 0.7, z: b.z + b.h, h: 0.5 }, world, rot));
    }
    minis.sort((a, c) => a.depth - c.depth);
  }
  let arrow: string | undefined;
  if (it.facing) {
    const cx = b.x + b.w / 2;
    const cy = b.y + b.d / 2;
    const f = it.facing;
    const tip = { x: cx + f.x * b.w * 0.42, y: cy + f.y * b.d * 0.42 };
    const l = { x: cx + f.x * b.w * 0.1 - f.y * b.w * 0.3, y: cy + f.y * b.d * 0.1 + f.x * b.d * 0.3 };
    const r = { x: cx + f.x * b.w * 0.1 + f.y * b.w * 0.3, y: cy + f.y * b.d * 0.1 - f.x * b.d * 0.3 };
    const z = b.z + b.h;
    arrow = [tip, l, r]
      .map((p) => {
        const q = turnPoint(p, world, rot);
        const s = project(q.x, q.y, z);
        return `${fmt(s.x)},${fmt(s.y)}`;
      })
      .join(" ");
  }
  const r = turnRect(b, world, rot);
  const gate = isGate(it.role);
  let port = iso.anchor;
  if (it.role === "device") port = it.portSide === "east" ? project(r.x + r.w, r.y + r.d / 2, b.z + b.h / 2) : project(r.x + r.w / 2, r.y + r.d, b.z + b.h / 2);
  else if (gate) port = project(r.x + r.w / 2, r.y + r.d / 2, b.z + b.h / 2);
  const parts: IsoBox[] = [];
  if (gate) {
    const P = 0.2;
    const lintel = Math.min(0.16, b.h / 4);
    for (const box of [
      { x: b.x - 0.03, y: b.y - 0.03, w: P, d: P, z: b.z, h: b.h },
      { x: b.x - 0.03, y: b.y + b.d - P + 0.03, w: P, d: P, z: b.z, h: b.h },
      { x: b.x - 0.03, y: b.y - 0.03, w: P, d: b.d + 0.06, z: b.z + b.h - lintel, h: lintel },
    ])
      parts.push(isoBox(box, world, rot));
  }
  const summarised = it.role === "site" || it.role === "room" || it.role === "area" || it.role === "rack";
  const centre = turnPoint({ x: b.x + b.w / 2, y: b.y + b.d / 2 }, world, rot);
  return {
    id: it.id,
    node: it.node,
    role: it.role,
    iso,
    minis,
    arrow,
    port,
    label: it.label,
    sub: it.sub,
    labelAt: { x: iso.base.x, y: iso.bounds.y1 + 13 },
    labelStart: false,
    labelShown: true,
    subShown: true,
    drillable: !!it.node.drill,
    centre,
    parts,
    hasSub: it.sub !== undefined || summarised,
    pill: { x0: 0, y0: 0, x1: 0, y1: 0 },
  };
}

/** A quadratic arc between two points, bowed to one side; `shift` moves it sideways. */
export function arc(a: Pt, b: Pt, shift = 0, reverse = false): { d: string; mid: Pt } {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.hypot(dx, dy) || 1;
  const nx = -dy / len;
  const ny = dx / len;
  const bow = Math.min(70, len * 0.2);
  const sa = { x: a.x + nx * shift, y: a.y + ny * shift };
  const sb = { x: b.x + nx * shift, y: b.y + ny * shift };
  const c = { x: (sa.x + sb.x) / 2 + nx * bow, y: (sa.y + sb.y) / 2 + ny * bow - Math.min(30, len * 0.1) };
  // The bow is fixed by the a to b direction, so a reversed lane runs along the same curve backwards.
  const [from, to] = reverse ? [sb, sa] : [sa, sb];
  return {
    d: `M${fmt(from.x)},${fmt(from.y)}Q${fmt(c.x)},${fmt(c.y)} ${fmt(to.x)},${fmt(to.y)}`,
    mid: { x: fmt(0.25 * sa.x + 0.5 * c.x + 0.25 * sb.x), y: fmt(0.25 * sa.y + 0.5 * c.y + 0.25 * sb.y) },
  };
}

export const LANE_GAP = 3;

const CHAR = 6.9;

/** The size of a label's pill, from an estimate of the text (it is not measured). */
function pillSize(i: SceneItem, withSub: boolean): { w: number; h: number } {
  const sub = withSub ? (i.sub?.length ?? 18) * 6.2 : 0;
  return { w: Math.round(Math.max(i.label.length * CHAR, sub) + 30), h: withSub ? 34 : 21 };
}

function pillAt(i: SceneItem, withSub: boolean, dy: number): Bounds {
  const { w, h } = pillSize(i, withSub);
  const x0 = i.labelStart ? i.labelAt.x : i.labelAt.x - w / 2;
  const y0 = i.labelAt.y + dy - 14;
  return { x0, x1: x0 + w, y0, y1: y0 + h };
}

/**
 * Where each label goes. The nearest item places its label first; a farther
 * one that would be written over it first loses its caption, then moves down a
 * step or two (a rack's devices keep the height of their unit instead), then
 * is hidden until pointed at. Widths are estimated, not measured.
 */
function placeLabels(items: SceneItem[]): void {
  const taken: Bounds[] = [];
  const hit = (r: Bounds) => taken.some((t) => r.x0 - 2 < t.x1 && t.x0 < r.x1 + 2 && r.y0 - 2 < t.y1 && t.y0 < r.y1 + 2);
  for (const i of [...items].reverse()) {
    if (!i.labelShown) continue;
    const steps = i.labelStart ? [0] : [0, 38, 76, 114];
    let done = false;
    for (const dy of steps) {
      for (const withSub of i.hasSub ? [true, false] : [false]) {
        const r = pillAt(i, withSub, dy);
        if (hit(r)) continue;
        i.labelAt = { x: i.labelAt.x, y: i.labelAt.y + dy };
        i.subShown = withSub;
        i.pill = r;
        taken.push(r);
        done = true;
        break;
      }
      if (done) break;
    }
    if (!done) {
      i.labelShown = false;
      i.subShown = false;
      i.pill = pillAt(i, false, 0);
    }
  }
  for (const i of items) if (!i.labelShown && i.pill.x1 === 0) i.pill = pillAt(i, false, 0);
}

function frameScene(f: NonNullable<WorldLayout["frame"]>, layout: WorldLayout, rot: Rot): SceneFrame {
  const world = { w: layout.w, d: layout.d };
  const { x, y, w, d, h } = f.box;
  const P = 0.14;
  const posts = [
    { x, y, w: P, d: P, z: 0, h },
    { x: x + w - P, y, w: P, d: P, z: 0, h },
    { x, y: y + d - P, w: P, d: P, z: 0, h },
    { x: x + w - P, y: y + d - P, w: P, d: P, z: 0, h },
  ]
    .map((b) => isoBox(b, world, rot))
    .sort((a, c) => a.depth - c.depth);
  const r = turnRect(f.box, world, rot);
  const corner = (cx: number, cy: number, z: number) => {
    const p = project(cx, cy, z);
    return `${fmt(p.x)},${fmt(p.y)}`;
  };
  const top = [corner(r.x, r.y, h), corner(r.x + r.w, r.y, h), corner(r.x + r.w, r.y + r.d, h), corner(r.x, r.y + r.d, h)].join(" ");
  // The ticks sit on the post at the left of the front, one per unit, a number every few.
  const step = f.units <= 24 ? 4 : 6;
  let ticks = "";
  const marks: SceneFrame["marks"] = [];
  for (let u = 1; u <= f.units; u++) {
    const p = project(r.x, r.y + r.d, u * UZ);
    ticks += `M${fmt(p.x - (u % step === 0 ? 8 : 5))},${fmt(p.y)}L${fmt(p.x)},${fmt(p.y)}`;
    if (u === 1 || u % step === 0) {
      const c = project(r.x, r.y + r.d, (u - 0.5) * UZ);
      marks.push({ x: fmt(c.x - 11), y: fmt(c.y + 3), text: String(u) });
    }
  }
  return { back: posts.slice(0, 1), front: posts.slice(1), top, ticks, marks };
}

export function buildScene(m: TopologyMap, rot: Rot): Scene {
  const layout = layoutWorld(m);
  const items = layout.items.map((i) => itemIso(i, layout, rot)).sort((a, b) => a.iso.depth - b.iso.depth);
  const plates = layout.plates.map((p) => plateIso(p, layout, rot));
  const at = new Map(items.map((i) => [i.id, i]));
  // Devices of a rack are labelled in a column beside it; the thin ones only when pointed at.
  const linked = new Set(m.edges.flatMap((e) => [e.a, e.b]));
  const right = Math.max(...items.filter((i) => i.role === "device").map((i) => i.iso.bounds.x1), 0);
  for (const i of items) {
    if (i.role !== "device") continue;
    i.labelAt = { x: right + 16, y: (i.iso.bounds.y0 + i.iso.bounds.y1) / 2 + 3 };
    i.labelStart = true;
    i.labelShown = (i.node.mount?.u_height ?? 1) >= 2 || linked.has(i.id);
  }
  placeLabels(items);
  const edges: SceneEdge[] = [];
  for (const e of m.edges) {
    const a = at.get(e.a);
    const b = at.get(e.b);
    if (!a || !b) continue;
    const base = arc(a.port, b.port);
    const lanes: SceneLane[] = [];
    if (e.measured) {
      // Two lanes side by side: a to b on one side of the line, b to a on the other, each drawn in its own direction.
      lanes.push({ dir: "ab", d: arc(a.port, b.port, -LANE_GAP).d });
      lanes.push({ dir: "ba", d: arc(a.port, b.port, LANE_GAP, true).d });
    }
    edges.push({ edge: e, d: base.d, lanes, util: edgeUtilClass(e), mid: base.mid });
  }
  const frame = layout.frame ? frameScene(layout.frame, layout, rot) : undefined;
  const bounds = union(
    [
      ...items.map((i) => i.iso.bounds),
      ...items.flatMap((i) => i.parts.map((p) => p.bounds)),
      ...items.map((i) => (i.pill.x1 > 0 ? i.pill : { x0: i.iso.bounds.x0, x1: i.iso.bounds.x1, y0: i.iso.bounds.y1, y1: i.iso.bounds.y1 + 20 })),
      ...plates.map((p) => p.iso.bounds),
      ...(frame ? [...frame.back, ...frame.front].map((p) => p.bounds) : []),
      ...(frame ? frame.marks.map((m) => ({ x0: m.x - 20, x1: m.x, y0: m.y - 10, y1: m.y + 2 })) : []),
    ],
    36,
  );
  return { items, plates, edges, bounds, viewBox: viewBoxOf(bounds), units: layout.units, frame };
}
