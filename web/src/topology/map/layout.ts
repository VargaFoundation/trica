// Where each thing of a map level stands on the isometric floor. Pure: it
// reads the API's map and returns boxes in world cells, independent of the
// rotation (scene.ts turns and projects them).

import type { MapNode, TopologyMap } from "../../api/types";
import { layoutFloor, tileOf, type Facing } from "../../racks/floor";
import type { Box } from "./iso";

export type ItemRole = "site" | "room" | "area" | "rack" | "device" | "internet" | "portal" | "unplaced" | "shelf";

export interface WorldItem {
  id: string;
  node: MapNode;
  role: ItemRole;
  box: Box;
  /** Little rack blocks drawn on the top of a slab, to say "this holds racks". */
  minis: number;
  /** The way a rack's front faces, as a world vector. */
  facing?: { x: number; y: number };
  label: string;
  sub?: string;
}

export interface Plate {
  id: string;
  box: Box;
  /** A grid of cells drawn on top. */
  cols?: number;
  rows?: number;
  dashed?: boolean;
  label?: string;
}

export interface WorldLayout {
  w: number;
  d: number;
  items: WorldItem[];
  plates: Plate[];
  /** Number of rack units drawn, for the rack level. */
  units?: number;
}

const MARGIN = 2;
const FACING: Record<Facing, { x: number; y: number }> = {
  north: { x: 0, y: -1 },
  east: { x: 1, y: 0 },
  south: { x: 0, y: 1 },
  west: { x: -1, y: 0 },
};

const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));
const byName = (a: MapNode, b: MapNode) => a.display.localeCompare(b.display) || a.id.localeCompare(b.id);

/** Shifts everything so that the content starts at the margin; returns the world size. */
function settle(items: WorldItem[], plates: Plate[]): WorldLayout {
  const boxes = [...items.map((i) => i.box), ...plates.map((p) => p.box)];
  if (boxes.length === 0) return { w: MARGIN * 2, d: MARGIN * 2, items, plates };
  const x0 = Math.min(...boxes.map((b) => b.x));
  const y0 = Math.min(...boxes.map((b) => b.y));
  const x1 = Math.max(...boxes.map((b) => b.x + b.w));
  const y1 = Math.max(...boxes.map((b) => b.y + b.d));
  const dx = MARGIN - x0;
  const dy = MARGIN - y0;
  const move = (b: Box): Box => ({ ...b, x: b.x + dx, y: b.y + dy });
  return {
    w: x1 - x0 + MARGIN * 2,
    d: y1 - y0 + MARGIN * 2,
    items: items.map((i) => ({ ...i, box: move(i.box) })),
    plates: plates.map((p) => ({ ...p, box: move(p.box) })),
  };
}

function item(n: MapNode, role: ItemRole, box: Box, extra: Partial<WorldItem> = {}): WorldItem {
  return { id: n.id, node: n, role, box, minis: 0, label: n.display, ...extra };
}

/** Portals stand in a line along the north edge of the content, below the horizon. */
function portals(nodes: MapNode[], y: number, x0: number): WorldItem[] {
  return nodes
    .filter((n) => n.kind === "portal")
    .sort((a, b) => (a.place_type === "internet" ? -1 : b.place_type === "internet" ? 1 : byName(a, b)))
    .map((n, i) =>
      item(n, n.place_type === "internet" ? "internet" : "portal", { x: x0 + i * 2.8, y, w: 1.6, d: 1.6, z: 0, h: 2.2 }),
    );
}

function layoutGlobal(m: TopologyMap): WorldLayout {
  const sites = m.nodes.filter((n) => n.kind === "site").sort(byName);
  const internet = m.nodes.find((n) => n.kind === "internet");
  const unplaced = m.nodes.find((n) => n.kind === "unplaced");
  const n = sites.length;
  const radius = n <= 1 ? 0 : Math.max(5.5, n * 1.9);
  const items: WorldItem[] = [];
  sites.forEach((s, i) => {
    const a = (2 * Math.PI * i) / Math.max(n, 1) - Math.PI / 2 + (n === 2 ? Math.PI / 4 : 0);
    const cx = radius * Math.cos(a);
    const cy = radius * Math.sin(a);
    const racks = s.counts.racks;
    items.push(
      item(s, "site", { x: cx - 1.8, y: cy - 1.8, w: 3.6, d: 3.6, z: 0, h: 0.7 + Math.min(2.2, Math.log2(1 + racks) * 0.4) }, { minis: Math.min(racks, 9), sub: s.group?.display }),
    );
  });
  const top = -(radius + 5.5);
  if (internet) items.push(item(internet, "internet", { x: -1.4, y: top, w: 2.8, d: 2.8, z: 3, h: 0.5 }));
  if (unplaced) items.push(item(unplaced, "unplaced", { x: radius + 4, y: radius + 3, w: 2.8, d: 2.8, z: 0, h: 0.4 }));
  return settle(items, []);
}

/** Rooms of a site packed in rows, grouped by building. */
function layoutSite(m: TopologyMap): WorldLayout {
  const slabs = m.nodes.filter((n) => n.kind === "room" || n.kind === "area");
  const order = [...slabs].sort((a, b) => {
    const ra = a.kind === "area" ? 1 : 0;
    const rb = b.kind === "area" ? 1 : 0;
    return ra - rb || (a.group?.display ?? "").localeCompare(b.group?.display ?? "") || byName(a, b);
  });
  const MAXW = 20;
  const GAP = 1.6;
  const items: WorldItem[] = [];
  let x = 0;
  let y = 0;
  let rowD = 0;
  let lastGroup: string | undefined;
  for (const n of order) {
    const tile = 0.5;
    const w = n.size?.width_mm ? clamp((n.size.width_mm / 600) * tile, 4, 10) : clamp(3 + Math.sqrt(n.counts.racks) * 1.4, 4, 9);
    const d = n.size?.depth_mm ? clamp((n.size.depth_mm / 600) * tile, 3.5, 9) : clamp(w * 0.75, 3.5, 8);
    const g = n.group?.id;
    // A row ends when the next slab does not fit, or when the building changes.
    if (x > 0 && (x + w > MAXW || (lastGroup !== undefined && g !== lastGroup))) {
      x = 0;
      y += rowD + GAP;
      rowD = 0;
    }
    lastGroup = g;
    items.push(item(n, n.kind === "area" ? "area" : "room", { x, y, w, d, z: 0, h: 0.5 }, { minis: Math.min(n.counts.racks, 14), sub: n.group?.display }));
    x += w + GAP;
    rowD = Math.max(rowD, d);
  }
  const top = -4.6;
  const ps = portals(m.nodes, top, 0);
  return settle([...items, ...ps], []);
}

function layoutRoom(m: TopologyMap): WorldLayout {
  const racks = m.nodes.filter((n) => n.kind === "rack").sort(byName);
  const tile = tileOf(m.focus?.tile_mm);
  const plan = layoutFloor(
    m.focus?.width_mm,
    m.focus?.depth_mm,
    racks.map((r) => ({ id: r.id, name: r.display, x: r.floor?.x, y: r.floor?.y, facing: r.floor?.facing as Facing | undefined })),
    tile,
  );
  const byId = new Map(racks.map((r) => [r.id, r]));
  const items: WorldItem[] = [];
  const plates: Plate[] = [];
  const rackBox = (x: number, y: number): Box => ({ x: x + 0.12, y: y + 0.12, w: 0.76, d: 0.76, z: 0, h: 2.4 });
  let rows = plan.rows;
  let cols = plan.cols;
  const loose = plan.unplaced;
  if (plan.placed.length === 0) {
    // No coordinates anywhere: racks in lines, eight to a row.
    const per = 8;
    cols = Math.min(per, Math.max(loose.length, 1));
    rows = Math.max(1, Math.ceil(loose.length / per));
    plates.push({ id: "floor", box: { x: 0, y: 0, w: cols, d: rows, z: -0.15, h: 0.15 }, cols, rows });
    loose.forEach((r, i) => {
      const n = byId.get(r.id);
      if (n) items.push(item(n, "rack", rackBox(i % per, Math.floor(i / per)), { facing: FACING.south }));
    });
  } else {
    plates.push({ id: "floor", box: { x: 0, y: 0, w: cols, d: rows, z: -0.15, h: 0.15 }, cols, rows });
    for (const r of plan.placed) {
      const n = byId.get(r.id);
      if (n) items.push(item(n, "rack", rackBox(r.x, r.y), { facing: FACING[r.facing ?? "south"] ?? FACING.south }));
    }
    if (loose.length > 0) {
      const per = Math.max(cols, 4);
      const lr = Math.ceil(loose.length / per);
      plates.push({ id: "loose", box: { x: 0, y: rows + 1.2, w: Math.min(per, loose.length), d: lr, z: -0.15, h: 0.15 }, dashed: true, label: "unplaced" });
      loose.forEach((r, i) => {
        const n = byId.get(r.id);
        if (n) items.push(item(n, "rack", rackBox(i % per, rows + 1.2 + Math.floor(i / per)), { facing: FACING.south }));
      });
    }
  }
  // Devices declared in the room or the site but in no rack: small units on a
  // dashed strip below the racks, twelve to a line at least.
  const loosed = m.nodes.filter((n) => n.kind === "device").sort(byName);
  if (loosed.length > 0) {
    const below = items.reduce((y, i) => Math.max(y, i.box.y + i.box.d), rows) + 1.4;
    const per = Math.max(cols, 12);
    plates.push({ id: "devices", box: { x: 0, y: below, w: Math.min(per, loosed.length), d: Math.ceil(loosed.length / per), z: -0.15, h: 0.15 }, dashed: true });
    loosed.forEach((n, i) => {
      items.push(item(n, "shelf", { x: (i % per) + 0.15, y: below + Math.floor(i / per) + 0.15, w: 0.7, d: 0.7, z: 0, h: 0.35 }, { sub: n.role }));
    });
  }
  const ps = portals(m.nodes, -4.4, 0);
  return settle([...items, ...ps], plates);
}

/** The usual rack heights; a rack is drawn at the first one that holds its devices. */
const HEIGHTS = [12, 24, 42, 48, 52];
const UZ = 0.36;

function layoutRack(m: TopologyMap): WorldLayout {
  const devs = m.nodes.filter((n) => n.kind === "device").sort(byName);
  const mounted = devs.filter((n) => n.mount && n.mount.position > 0);
  const loose = devs.filter((n) => !(n.mount && n.mount.position > 0));
  const top = Math.max(0, ...mounted.map((n) => (n.mount?.position ?? 0) + (n.mount?.u_height ?? 1) - 1));
  const units = HEIGHTS.find((h) => h >= top) ?? Math.ceil(top / 6) * 6;
  const W = 2.6;
  const D = 2.6;
  const items: WorldItem[] = [];
  for (const n of mounted) {
    const mt = n.mount;
    if (!mt) continue;
    const u = Math.max(1, mt.u_height ?? 1);
    const rear = mt.face === "rear";
    const half = !mt.full_depth;
    const d = half ? D / 2 - 0.05 : D - 0.1;
    const y = half ? (rear ? 0.05 : D / 2) : 0.05;
    items.push(
      item(n, "device", { x: 0.1, y, w: W - 0.2, d, z: (mt.position - 1) * UZ + 0.02, h: u * UZ - 0.04 }, { sub: n.role }),
    );
  }
  loose.forEach((n, i) => {
    items.push(item(n, "shelf", { x: W + 1.6 + (i % 4) * 1.5, y: 0.6 + Math.floor(i / 4) * 1.5, w: 1.2, d: 1.2, z: 0, h: 0.4 }, { sub: n.role }));
  });
  const plates: Plate[] = [{ id: "base", box: { x: -0.1, y: -0.1, w: W + 0.2, d: D + 0.2, z: -0.12, h: 0.12 } }];
  const ps = portals(m.nodes, 0, 0).map((p, i) => ({ ...p, box: { ...p.box, x: -4.4, y: 0.2 + i * 2.6 } }));
  const out = settle([...items, ...ps], plates);
  return { ...out, units };
}

function layoutUnplaced(m: TopologyMap): WorldLayout {
  const devs = m.nodes.filter((n) => n.kind === "device").sort(byName);
  const per = 6;
  const items = devs.map((n, i) => item(n, "shelf", { x: (i % per) * 2.2, y: Math.floor(i / per) * 2.2, w: 1.7, d: 1.7, z: 0, h: 0.5 }, { sub: n.role }));
  const cols = Math.min(per, Math.max(devs.length, 1));
  const rows = Math.max(1, Math.ceil(devs.length / per));
  const plates: Plate[] = [{ id: "floor", box: { x: -0.4, y: -0.4, w: cols * 2.2 + 0.2, d: rows * 2.2 + 0.2, z: -0.15, h: 0.15 }, dashed: true }];
  return settle(items, plates);
}

export function layoutWorld(m: TopologyMap): WorldLayout {
  switch (m.level) {
    case "site":
      return layoutSite(m);
    case "room":
      return layoutRoom(m);
    case "rack":
      return layoutRack(m);
    case "unplaced":
      return layoutUnplaced(m);
    default:
      return layoutGlobal(m);
  }
}
