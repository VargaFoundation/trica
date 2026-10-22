// The drawn scene of a map level: every box turned and projected, the edges as
// arcs between anchors, depth-ordered. Pure data, no React: IsoScene.tsx only
// paints it.

import type { MapEdge, MapNode, TopologyMap } from "../../api/types";
import { isoBox, project, turnPoint, turnRect, union, viewBoxOf, type Bounds, type IsoBox, type Pt, type Rot } from "./iso";
import { layoutWorld, type ItemRole, type Plate, type WorldItem, type WorldLayout } from "./layout";
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
  const port = it.role === "device" ? project(r.x + r.w / 2, r.y + r.d, b.z + b.h / 2) : iso.anchor;
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

/**
 * Labels that would be written over one another: the nearest item keeps its
 * label, a farther one loses its caption, then its label (it reappears when
 * pointed at). Widths are estimated, not measured.
 */
function cullLabels(items: SceneItem[]): void {
  const taken: Bounds[] = [];
  const hit = (r: Bounds) => taken.some((t) => r.x0 < t.x1 && t.x0 < r.x1 && r.y0 < t.y1 && t.y0 < r.y1);
  const rect = (i: SceneItem, withSub: boolean): Bounds => {
    const w = Math.max(i.label.length, withSub ? (i.sub ?? "").length : 0) * 6.4 + 14;
    const x0 = i.labelStart ? i.labelAt.x : i.labelAt.x - w / 2;
    return { x0, x1: x0 + w, y0: i.labelAt.y - 11, y1: i.labelAt.y + (withSub ? 16 : 3) };
  };
  for (const i of [...items].reverse()) {
    if (!i.labelShown) continue;
    if (hit(rect(i, true))) {
      i.subShown = false;
      if (hit(rect(i, false))) {
        i.labelShown = false;
        continue;
      }
    }
    taken.push(rect(i, i.subShown));
  }
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
  cullLabels(items);
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
  const bounds = union(
    [
      ...items.map((i) => i.iso.bounds),
      ...items.map((i) =>
        i.labelStart
          ? { x0: i.labelAt.x, x1: i.labelAt.x + 120, y0: i.labelAt.y - 12, y1: i.labelAt.y + 4 }
          : { x0: i.labelAt.x - 60, x1: i.labelAt.x + 60, y0: i.labelAt.y - 12, y1: i.labelAt.y + 16 },
      ),
      ...plates.map((p) => p.iso.bounds),
    ],
    36,
  );
  return { items, plates, edges, bounds, viewBox: viewBoxOf(bounds), units: layout.units };
}
