// The drawn scene of a map level: every box turned and projected, the ground,
// the walls, cabinets and cards, the lines as arcs (global, site) or as routed
// paths (room trays, rack tree), depth-ordered. Pure data, no React:
// IsoScene.tsx only paints it.

import type { MapEdge, MapNode, TopologyMap } from "../../api/types";
import type { IconName } from "../../components/ui/icons";
import { kindOf } from "../../inventory/kind";
import { deviceFront, doorGlass, faceOf, facePoly, floorMatrix, ledDots, ledStates, ventLines, wallBoxes, windows, type DeviceFront, type Face, type Rect } from "./decor";
import { CELL, columnShifts, isoBox, project, projectBox, shiftIso, turnedWorld, turnPoint, turnRect, union, viewBoxOf, type Bounds, type IsoBox, type Pt, type Rot } from "./iso";
import { isGate, layoutWorld, UZ, type ItemRole, type Plate, type WorldItem, type WorldLayout } from "./layout";
import { edgeUtilClass, linkState, type Direction, type LinkState } from "./flow";
import { badgeBox, badgeDistances, hitsAny, leaderBetween, merge, placePill, type PillSize, type PillSpec } from "./labels";
import { bundleMark, endBadge } from "./port";
import { assignLanes, fanOffsets, offsetPolyline, pointAt, pointAtDistance, polylineLength, quadAt, quadLength, roundedPath, trayLaneY, trayRect, trayRoute } from "./route";
import { BAND_WIDTH, speedBand, type SpeedBand } from "./speed";
import { orderLayers, planTree, rankTree, type TreeEdge, type TreeNode, type TreePlan } from "./tree";

export interface Cabinet {
  iso: IsoBox;
  /** The door's glass on its south face. */
  door: string;
}

/** The front of a rack of a room: door glass, vents and one LED per device. */
export interface SceneDoor {
  face: Face;
  glass: string;
  vents: string;
  leds: { x: number; y: number; state: string }[];
}

/** The card of a linked device in a rack's tree. */
export interface SceneCard {
  x: number;
  y: number;
  w: number;
  h: number;
  icon: IconName;
  name: string;
  model?: string;
  layer: number;
}

export interface SceneItem {
  id: string;
  node: MapNode;
  role: ItemRole;
  iso: IsoBox;
  /** The footprint once turned, and the base and height of the block. */
  rect: Rect;
  z: number;
  h: number;
  /** Small cabinets standing on a room's slab. */
  cabinets: Cabinet[];
  /** Window quadrilaterals of a building (site) and its roof panel and plant. */
  windows: string[];
  roof?: string;
  plant?: IsoBox;
  /** Cut-away walls of a room: the far ones tall, the near ones low. */
  walls?: { back: IsoBox[]; front: IsoBox[] };
  /** The name painted on the floor of a room. */
  floorText?: { text: string; transform: string };
  /** The front of a room's rack. */
  door?: SceneDoor;
  /** What a device's front shows, by its role. */
  front?: DeviceFront;
  /** Its status LED, on the right of its front: the colour of its health, as a product shows it. */
  led?: Pt;
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
  /** Where the arrow keys find the item on screen: its card when it has one. */
  navAt: Pt;
  /** The posts and lintel of a gate (portals and the Internet). */
  parts: IsoBox[];
  /** A caption exists for this item (its sub text, or a summary the page writes). */
  hasSub: boolean;
  /** The label's pill, in screen px, once placed. */
  pill: Bounds;
  /** The line from the block to its pill, when the pill is not right under (or beside) it. */
  pillLeader?: { x1: number; y1: number; x2: number; y2: number };
  /** Its card in the rack's tree. */
  card?: SceneCard;
}

export interface SceneFrame {
  /** Posts behind the devices, and those in front of them. */
  back: IsoBox[];
  front: IsoBox[];
  /** The top rails, as polygon points. */
  top: string;
  /** U ticks along the front-left post, and the numbers of some of them. */
  ticks: string;
  /** The cabinet's back and left side panels, behind the devices (polygon points). */
  panels: string[];
  /** Blanking plates over the empty units of the front, one closed shape per run of empty units, and a line between units. */
  blanks: string;
  blankLines: string;
  marks: { x: number; y: number; text: string }[];
}

export interface ScenePlate {
  id: string;
  iso: IsoBox;
  grid: string[];
  dashed: boolean;
  label?: string;
}

/** The ground the scene stands on: a plane with a grid that fades out from its centre. */
export interface SceneGround {
  poly: string;
  grid: string;
  cx: number;
  cy: number;
  r: number;
}

export interface SceneLane {
  dir: Direction;
  d: string;
  /** Length of the lane in px, for the number of particles. */
  len: number;
}

/** A small pill laid on a line: a port name, the size of a bundle. */
export interface SceneBadge {
  x: number;
  y: number;
  text: string;
  w: number;
  /** No room was found for it beside the other labels: it shows only when its line is pointed at. */
  drop?: boolean;
}

export interface SceneEdge {
  edge: MapEdge;
  /** The path of the line's centre (hit target and base stroke). */
  d: string;
  lanes: SceneLane[];
  util: ReturnType<typeof edgeUtilClass>;
  mid: Pt;
  /** Arrowheads (polygon points) of an uplinked_to edge, one per end its uplink devices are on. */
  arrows: string[];
  /** Speed class and line width from the capacity. */
  band: SpeedBand;
  width: number;
  state: LinkState;
  /** The line runs on the floor (a cable tray): painted under the items. */
  floor: boolean;
  /** Length of the line in px. */
  len: number;
  /** The point `s` px along the line from its first end. */
  at: (s: number) => Pt;
  /** Port names at both ends, and the size of a bundle. */
  ports: { a?: SceneBadge; b?: SceneBadge };
  bundle?: SceneBadge;
  /** The ports are shown without pointing at the line (it is long enough). */
  portsShown: boolean;
  /** A pulse travels along a tunnel. */
  pulse: boolean;
}

export interface Scene {
  items: SceneItem[];
  plates: ScenePlate[];
  edges: SceneEdge[];
  bounds: Bounds;
  viewBox: string;
  units?: number;
  frame?: SceneFrame;
  ground: SceneGround;
  /** Cable trays of a room, on the floor behind its racks. */
  trays: IsoBox[];
  /** What the view frames: the blocks, cards and frame, not the floor they stand on nor the labels. */
  core: Bounds;
  /** How much bigger than its design size the text of this scene is drawn. */
  text: number;
}

/**
 * The text scales a scene is built at, 1.18 apart around 1 (so that text stays within 9 % of its size
 * on screen): a zoom only rebuilds it when it crosses one of them. From the deepest zoom in (ZOOM_MAX of
 * a view at FIT_MAX) to a far zoom out.
 */
export const TEXT_STEPS: readonly number[] = Array.from({ length: 21 }, (_, i) => Math.round(1.18 ** (i - 14) * 1000) / 1000);

/**
 * The text scale for a view that draws one scene px as `screen` screen px: the step nearest to its
 * inverse, so that text keeps its design size on screen (within the steps' range).
 */
export function textScale(screen: number): number {
  const want = 1 / Math.max(screen, 1e-6);
  let best = TEXT_STEPS[0] ?? 1;
  for (const t of TEXT_STEPS) if (Math.abs(Math.log(t / want)) < Math.abs(Math.log(best / want))) best = t;
  return best;
}

/** Below this many screen px, a device's block is too small for its name to be written until pointed at. */
export const LABEL_MIN_PX = 18;

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

const GROUND_Z = -0.15;

/** The ground plane under the whole scene, in turned coordinates, with a grid that fades from its centre. */
function groundOf(layout: WorldLayout, rot: Rot): SceneGround {
  const w = turnedWorld({ w: layout.w, d: layout.d }, rot);
  const corners = [project(0, 0, GROUND_Z), project(w.w, 0, GROUND_Z), project(w.w, w.d, GROUND_Z), project(0, w.d, GROUND_Z)];
  const step = Math.max(1, Math.ceil(Math.max(w.w, w.d) / 40));
  let grid = "";
  for (let x = 0; x <= w.w; x += step) {
    const a = project(x, 0, GROUND_Z);
    const b = project(x, w.d, GROUND_Z);
    grid += `M${fmt(a.x)},${fmt(a.y)}L${fmt(b.x)},${fmt(b.y)}`;
  }
  for (let y = 0; y <= w.d; y += step) {
    const a = project(0, y, GROUND_Z);
    const b = project(w.w, y, GROUND_Z);
    grid += `M${fmt(a.x)},${fmt(a.y)}L${fmt(b.x)},${fmt(b.y)}`;
  }
  const c = project(w.w / 2, w.d / 2, GROUND_Z);
  return { poly: corners.map((p) => `${fmt(p.x)},${fmt(p.y)}`).join(" "), grid, cx: fmt(c.x), cy: fmt(c.y), r: fmt(((w.w + w.d) * CELL) / 2) };
}

/** A label cut to what fits flat along a footprint (about 6.2 px a character at the painted size). */
function fitText(text: string, room: number): string {
  const max = Math.max(3, Math.floor(room / 6.2));
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

const sub = (a: Pt, b: Pt): Pt => ({ x: a.x - b.x, y: a.y - b.y });

function itemIso(it: WorldItem, layout: WorldLayout, rot: Rot): SceneItem {
  const world = { w: layout.w, d: layout.d };
  const iso = isoBox(it.box, world, rot);
  const b = it.box;
  const r = turnRect(b, world, rot);
  const z1 = b.z + b.h;
  const cabinets: Cabinet[] = [];
  // Racks stand on a room's slab as small cabinets, laid out on the turned footprint so that the front strip stays free for the room's name.
  if (it.minis > 0 && it.role !== "site") {
    const reserve = it.role === "room" || it.role === "area" ? 1.1 : 0;
    const pad = 0.5;
    const usable = { w: r.w - pad * 2, d: Math.max(r.d - pad * 2 - reserve, 0.8) };
    const cols = Math.max(1, Math.ceil(Math.sqrt(it.minis * (usable.w / usable.d))));
    const rows = Math.ceil(it.minis / cols);
    const cw = usable.w / cols;
    const cd = usable.d / rows;
    for (let i = 0; i < it.minis; i++) {
      const box = { x: r.x + pad + (i % cols) * cw + cw * 0.15, y: r.y + pad + Math.floor(i / cols) * cd + cd * 0.15, w: cw * 0.7, d: cd * 0.7, z: z1, h: 0.55 };
      cabinets.push({ iso: projectBox(box), door: doorGlass(box, "south", z1, 0.55, 0.06) });
    }
    cabinets.sort((a, c) => a.iso.depth - c.iso.depth);
  }
  let arrow: string | undefined;
  if (it.facing) {
    const cx = b.x + b.w / 2;
    const cy = b.y + b.d / 2;
    const f = it.facing;
    const tip = { x: cx + f.x * b.w * 0.42, y: cy + f.y * b.d * 0.42 };
    const l = { x: cx + f.x * b.w * 0.1 - f.y * b.w * 0.3, y: cy + f.y * b.d * 0.1 + f.x * b.d * 0.3 };
    const rr = { x: cx + f.x * b.w * 0.1 + f.y * b.w * 0.3, y: cy + f.y * b.d * 0.1 - f.x * b.d * 0.3 };
    arrow = [tip, l, rr]
      .map((p) => {
        const q = turnPoint(p, world, rot);
        const s = project(q.x, q.y, z1);
        return `${fmt(s.x)},${fmt(s.y)}`;
      })
      .join(" ");
  }
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
  let windowList: string[] = [];
  let roof: string | undefined;
  let plant: IsoBox | undefined;
  if (it.role === "site") {
    windowList = windows(r, b.z, b.h);
    const k = 0.22;
    roof = [project(r.x + k, r.y + k, z1), project(r.x + r.w - k, r.y + k, z1), project(r.x + r.w - k, r.y + r.d - k, z1), project(r.x + k, r.y + r.d - k, z1)].map((p) => `${fmt(p.x)},${fmt(p.y)}`).join(" ");
    plant = projectBox({ x: r.x + r.w * 0.55, y: r.y + r.d * 0.2, w: r.w * 0.22, d: r.d * 0.22, z: z1, h: 0.28 });
  }
  let walls: SceneItem["walls"];
  let floorText: SceneItem["floorText"];
  if (it.role === "room") {
    const w = wallBoxes(r, z1, 1.15, 0.3);
    walls = { back: w.back.map(projectBox), front: w.front.map(projectBox) };
  }
  if (it.role === "room" || it.role === "area") {
    const strip = { x: r.x, y: r.y + r.d - 1.0, w: r.w, d: 0.8 };
    floorText = { text: fitText(it.label, r.w * CELL * 0.85), transform: floorMatrix(strip, z1 + 0.01, 0.9) };
  }
  let door: SceneDoor | undefined;
  if (it.role === "rack") {
    const origin = turnPoint({ x: 0, y: 0 }, world, rot);
    const face = it.facing ? faceOf(sub(turnPoint(it.facing, world, rot), origin)) : "south";
    if (face) {
      const states = ledStates(it.node.health);
      const dots = ledDots(r, face, b.z + b.h - 0.3, states.length);
      door = {
        face,
        glass: doorGlass(r, face, b.z, b.h),
        vents: ventLines(r, face, b.z + 0.35, b.z + b.h - 0.75, 7),
        leds: dots.map((p, i) => ({ x: fmt(p.x), y: fmt(p.y), state: states[i] ?? "unknown" })),
      };
    }
  }
  // A device's front shows what it is: a switch's ports, a server's bays, a PDU's outlets, a firewall's screen.
  const front = (it.role === "device" || it.role === "shelf" || it.role === "unplaced") && it.node.kind === "device" && b.h >= 0.25 ? deviceFront(it.node.role, r, b.z, b.h) : undefined;
  // A rack shows its health on its door; a gate is not a thing with a health.
  const led = it.role === "rack" || isGate(it.role) ? undefined : project(r.x + r.w - Math.min(0.3, r.w * 0.15), r.y + r.d, b.z + b.h * 0.55);
  const summarised = it.role === "site" || it.role === "room" || it.role === "area" || it.role === "rack";
  const centre = turnPoint({ x: b.x + b.w / 2, y: b.y + b.d / 2 }, world, rot);
  return {
    id: it.id,
    node: it.node,
    role: it.role,
    iso,
    rect: r,
    z: b.z,
    h: b.h,
    cabinets,
    windows: windowList,
    roof,
    plant,
    walls,
    floorText,
    door,
    front,
    led: led ? { x: fmt(led.x), y: fmt(led.y) } : undefined,
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
    navAt: iso.anchor,
    parts,
    hasSub: it.sub !== undefined || summarised,
    pill: { x0: 0, y0: 0, x1: 0, y1: 0 },
  };
}

/** A quadratic arc between two points, bowed to one side; `shift` moves it sideways. */
export function arc(a: Pt, b: Pt, shift = 0, reverse = false): { d: string; mid: Pt; c: Pt; ends: [Pt, Pt] } {
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
    c,
    ends: [sa, sb],
  };
}

/** The dash-dot-dot of a device uplink (the tunnel's dash-dot is taken) and the tunnel's dash-dot. */
export const UPLINKED_DASH = "10 3 2 3 2 3";
export const TUNNEL_DASH = "10 4 2 4";
/** The dash a medium draws with, whatever the confidence or the state; media without one follow the confidence. */
export const MEDIUM_DASH: Readonly<Record<string, string>> = { tunnel: TUNNEL_DASH, uplinked_to: UPLINKED_DASH };
/**
 * px along the arc's normal (positive: the side it bows to). Media that can share a pair are at least 8 apart, over
 * half the 14 px hit stroke. physical and uplink share 0: an uplink always ends at the Internet, a cable never does.
 */
export const MEDIUM_SHIFT: Readonly<Record<string, number>> = { physical: 0, uplink: 0, uplinked_to: 8, tunnel: -8 };
export const mediumShift = (m: string): number => MEDIUM_SHIFT[m] ?? 0;

/** The points of an arrowhead whose tip is `tip`, pointing along the tangent `tip - ctrl`. */
export function arrowHead(tip: Pt, ctrl: Pt, size = 8): string {
  const dx = tip.x - ctrl.x;
  const dy = tip.y - ctrl.y;
  const len = Math.hypot(dx, dy) || 1;
  const ux = dx / len;
  const uy = dy / len;
  const bx = tip.x - ux * size * 1.4;
  const by = tip.y - uy * size * 1.4;
  const l = { x: bx - uy * size * 0.55, y: by + ux * size * 0.55 };
  const r = { x: bx + uy * size * 0.55, y: by - ux * size * 0.55 };
  return [tip, l, r].map((p) => `${fmt(p.x)},${fmt(p.y)}`).join(" ");
}

export const LANE_GAP = 3;

const CHAR = 6.9;
/**
 * How much bigger than its design size text is drawn in the scene being built: the inverse of the
 * screen scale, so that labels and badges keep one size on screen whatever the zoom (see `textScale`).
 * Set by `buildScene` for the time it runs.
 */
let TEXT = 1;

/** The size of a label's pill, from an estimate of the text (it is not measured). */
function pillSizeOf(i: SceneItem): PillSize {
  return (withSub) => {
    const sub = withSub ? (i.sub?.length ?? 18) * 6.2 : 0;
    return { w: Math.round((Math.max(i.label.length * CHAR, sub) + 30) * TEXT), h: (withSub ? 34 : 21) * TEXT };
  };
}

/** Everything a block draws, in screen px: its faces, posts, walls, cabinets and plant; a gate's badge stays inside its box. */
export function blockBounds(i: SceneItem): Bounds {
  let b: Bounds = i.iso.bounds;
  for (const p of i.parts) b = merge(b, p.bounds);
  if (i.walls) for (const w of [...i.walls.back, ...i.walls.front]) b = merge(b, w.bounds);
  for (const c of i.cabinets) b = merge(b, c.iso.bounds);
  if (i.plant) b = merge(b, i.plant.bounds);
  return b;
}

/** An item moved on the screen: its faces, its line ends, its label. */
function shiftItem(i: SceneItem, dx: number, dy: number): void {
  i.iso = shiftIso(i.iso, dx, dy);
  i.parts = i.parts.map((p) => shiftIso(p, dx, dy));
  i.port = { x: i.port.x + dx, y: i.port.y + dy };
  i.navAt = { x: i.navAt.x + dx, y: i.navAt.y + dy };
  i.labelAt = { x: i.labelAt.x + dx, y: i.labelAt.y + dy };
}

/** A block other labels must not be written over. */
export interface Obstacle {
  id: string;
  box: Bounds;
}

/**
 * Where each label goes. The nearest item places its label first, straight under its block; one that
 * would be written over a label already placed or over another block loses its caption, then moves
 * down and sideways (a leader line then joins it to its block) or above, a rack's devices slide along
 * their column instead, and when nothing is free it is hidden until pointed at. Widths are estimated.
 */
/** A device: its name stays next to it, or waits until it is pointed at. */
const nearOnly = (i: SceneItem) => (i.role === "device" || i.role === "shelf") && !i.drillable;
/** A device that is not in trouble: zoomed out, its name waits until it is pointed at. */
const crowdable = (i: SceneItem) => nearOnly(i) && i.node.health.worst !== "critical" && i.node.health.worst !== "warning";
/** Past this many lines at a level, port names wait until a line or one of its ends is pointed at. */
const PORTS_MAX = 12;

function placeLabels(items: SceneItem[], obstacles: readonly Obstacle[], taken: Bounds[]): void {
  for (const i of [...items].reverse()) {
    if (!i.labelShown) continue;
    const mine = blockBounds(i);
    const spec: PillSpec = { at: { x: i.labelAt.x, y: i.labelAt.y - 14 * TEXT }, start: i.labelStart, top: mine.y0, size: pillSizeOf(i), hasSub: i.hasSub, beside: i.labelStart, scale: TEXT, near: nearOnly(i) };
    const won = placePill(
      spec,
      taken,
      obstacles.filter((o) => o.id !== i.id).map((o) => o.box),
    );
    if (won) {
      i.labelAt = { x: won.rect.x0 + (i.labelStart ? 0 : (won.rect.x1 - won.rect.x0) / 2), y: won.rect.y0 + 14 * TEXT };
      i.subShown = won.withSub;
      i.pill = won.rect;
      taken.push(won.rect);
      if (i.labelStart) i.pillLeader = leaderBetween({ x: i.iso.bounds.x1, y: spec.at.y + 11 * TEXT }, won.rect);
      else if (won.dx !== 0 || won.dy !== 0 || won.above) i.pillLeader = leaderBetween(won.above ? { x: i.iso.base.x, y: mine.y0 + 3 } : { x: i.iso.base.x, y: i.iso.bounds.y1 - 3 }, won.rect);
    } else {
      i.labelShown = false;
      i.subShown = false;
    }
  }
  for (const i of items) {
    if (i.labelShown || i.pill.x1 !== 0) continue;
    // Hidden: where it shows when pointed at.
    const { w, h } = pillSizeOf(i)(false);
    const x0 = i.labelStart ? i.labelAt.x : i.labelAt.x - w / 2;
    const y0 = i.labelAt.y - 14 * TEXT;
    i.pill = { x0, x1: x0 + w, y0, y1: y0 + h };
  }
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
  // The far panels of the cabinet, and blanking plates where no device is mounted.
  const H = h;
  const panels = [
    [corner(r.x, r.y, 0), corner(r.x + r.w, r.y, 0), corner(r.x + r.w, r.y, H), corner(r.x, r.y, H)].join(" "),
    [corner(r.x, r.y, 0), corner(r.x, r.y + r.d, 0), corner(r.x, r.y + r.d, H), corner(r.x, r.y, H)].join(" "),
  ];
  const filled = new Set(f.filled);
  const inner = { x: r.x + P, y: r.y, w: r.w - 2 * P, d: r.d };
  let blanks = "";
  let blankLines = "";
  for (let u = 1; u <= f.units; u++) {
    if (filled.has(u)) continue;
    let v = u;
    while (v + 1 <= f.units && !filled.has(v + 1)) v++;
    blanks += `M${facePoly(inner, "south", 0, 1, (u - 1) * UZ + 0.02, v * UZ - 0.02).split(" ").join("L")}Z`;
    for (let k = u; k < v; k++) {
      const a = project(inner.x, inner.y + inner.d, k * UZ);
      const b = project(inner.x + inner.w, inner.y + inner.d, k * UZ);
      blankLines += `M${fmt(a.x)},${fmt(a.y)}L${fmt(b.x)},${fmt(b.y)}`;
    }
    u = v;
  }
  return { back: posts.slice(0, 1), front: posts.slice(1), top, ticks, marks, panels, blanks, blankLines };
}

const badge = (p: Pt, text: string): SceneBadge => ({ x: fmt(p.x), y: fmt(p.y), text, w: Math.round((text.length * 5.6 + 10) * TEXT) });

/** What a path gives the finished edge: its centre line, its two lanes, where badges sit. */
interface Geometry {
  d: string;
  mid: Pt;
  len: number;
  lanes: SceneLane[];
  ends: [Pt, Pt];
  /** The point near each end where its port badge sits. */
  near: [Pt, Pt];
  /** The point `s` px along the line from its first end. */
  at: (s: number) => Pt;
  arrows: string[];
}

function arcGeometry(e: MapEdge, a: SceneItem, b: SceneItem): Geometry {
  const shift = mediumShift(e.medium);
  const base = arc(a.port, b.port, shift);
  const arrows: string[] = [];
  if (e.medium === "uplinked_to") {
    if (e.toward === "a" || e.toward === "both") arrows.push(arrowHead(base.ends[0], base.c));
    if (e.toward === "b" || e.toward === "both") arrows.push(arrowHead(base.ends[1], base.c));
  }
  const len = quadLength(base.ends[0], base.c, base.ends[1]);
  const lanes: SceneLane[] = e.measured
    ? [
        // Two lanes side by side: a to b on one side of the line, b to a on the other, each drawn in its own direction.
        { dir: "ab", d: arc(a.port, b.port, shift - LANE_GAP).d, len },
        { dir: "ba", d: arc(a.port, b.port, shift + LANE_GAP, true).d, len },
      ]
    : [];
  return { d: base.d, mid: base.mid, len, lanes, ends: base.ends, near: [quadAt(base.ends[0], base.c, base.ends[1], 0.14), quadAt(base.ends[0], base.c, base.ends[1], 0.86)], at: (d) => quadAt(base.ends[0], base.c, base.ends[1], Math.min(1, Math.max(0, d / (len || 1)))), arrows };
}

const CORNER = 9;

function pathGeometry(e: MapEdge, pts: Pt[]): Geometry {
  const len = polylineLength(pts);
  const reversed = [...pts].reverse();
  const lanes: SceneLane[] = e.measured
    ? [
        { dir: "ab", d: roundedPath(offsetPolyline(pts, -LANE_GAP), CORNER), len },
        // The way back runs the same line reversed, on the other side of it.
        { dir: "ba", d: roundedPath(offsetPolyline(reversed, -LANE_GAP), CORNER), len },
      ]
    : [];
  const arrows: string[] = [];
  const n = pts.length;
  if (e.medium === "uplinked_to" && n >= 2) {
    const first = pts[0] as Pt;
    const last = pts[n - 1] as Pt;
    if (e.toward === "a" || e.toward === "both") arrows.push(arrowHead(first, pts[1] as Pt));
    if (e.toward === "b" || e.toward === "both") arrows.push(arrowHead(last, pts[n - 2] as Pt));
  }
  return {
    d: roundedPath(pts, CORNER),
    mid: pointAt(pts, 0.5),
    len,
    lanes,
    ends: [pts[0] as Pt, pts[n - 1] as Pt],
    near: [pointAtDistance(pts, 22), pointAtDistance(pts, len - 22)],
    at: (d) => pointAtDistance(pts, d),
    arrows,
  };
}

/** The finished edge: its geometry plus what the speed, the state and the members say. */
function finish(e: MapEdge, g: Geometry, floor: boolean, ports: boolean): SceneEdge {
  const band = speedBand(e.capacity_mbps);
  const state = linkState(e);
  const a = ports ? endBadge(e.members, "a") : "";
  const bb = ports ? endBadge(e.members, "b") : "";
  const mark = bundleMark(e.links);
  return {
    edge: e,
    d: g.d,
    lanes: g.lanes,
    util: edgeUtilClass(e),
    mid: g.mid,
    arrows: g.arrows,
    band,
    width: BAND_WIDTH[band],
    state,
    floor,
    len: g.len,
    at: g.at,
    ports: { a: a ? badge(g.near[0], a) : undefined, b: bb ? badge(g.near[1], bb) : undefined },
    bundle: mark ? badge(state === "down" ? pointAtLerp(g.ends, 0.36, g.mid) : g.mid, mark) : undefined,
    portsShown: g.len >= 150,
    pulse: e.medium === "tunnel",
  };
}

/**
 * Keeps the badges of the lines clear of each other, of the labels and of the cards: one that would lie
 * on another slides along its line (a bundle's count around the middle, a port name away from its end),
 * and a port name that finds no room waits until its line is pointed at. `taken` grows with what is placed.
 */
export function placeBadges(edges: SceneEdge[], taken: Bounds[]): void {
  const put = (b: SceneBadge, spots: Pt[]): boolean => {
    for (const p of spots) {
      const box = badgeBox(p, b.w, TEXT);
      if (hitsAny(box, taken, 1)) continue;
      b.x = fmt(p.x);
      b.y = fmt(p.y);
      taken.push(box);
      return true;
    }
    return false;
  };
  for (const e of edges) {
    const b = e.bundle;
    if (!b) continue;
    const mid = e.len / 2;
    const spots: Pt[] = [{ x: b.x, y: b.y }];
    const step = 22 * TEXT;
    for (let k = 1; k * step <= mid - 20 * TEXT; k++) spots.push(e.at(mid + k * step), e.at(mid - k * step));
    // A count is never dropped: with no room it stays where it was.
    if (!put(b, spots)) taken.push(badgeBox(b, b.w, TEXT));
  }
  for (const e of edges) {
    if (!e.portsShown) continue;
    const ds = badgeDistances(22 * TEXT, e.len / 2 - 14 * TEXT, 22 * TEXT);
    for (const [badge, from] of [
      [e.ports.a, (s: number) => e.at(s)],
      [e.ports.b, (s: number) => e.at(e.len - s)],
    ] as const) {
      if (!badge) continue;
      if (!put(badge, [{ x: badge.x, y: badge.y }, ...ds.map(from)])) badge.drop = true;
    }
  }
}

/** A point a fraction of the way from the first end to the centre. */
const pointAtLerp = (ends: [Pt, Pt], t: number, mid: Pt): Pt => ({ x: ends[0].x + (mid.x - ends[0].x) * t * 2, y: ends[0].y + (mid.y - ends[0].y) * t * 2 });

const CARD = { w: 158, h: 46, gapX: 40, gapY: 44, perRow: 6, track: 10 };
/** Past this many lines a tree's port names wait until a line or a card is pointed at. */
const TREE_PORTS = 8;

/**
 * Places the cards of a rack's tree right of the drawing and routes the links between them (`planTree`).
 * A card and its slab are one item: pointing at either lights both, so no line joins them. Returns the
 * routes, in the scene's coordinates, and the lanes of the links to the gates (finished once the gates stand).
 */
function placeCards(items: SceneItem[], layers: string[][], links: MapEdge[], gateRank: ReadonlyMap<string, number>): TreePlan {
  const at = new Map(items.map((i) => [i.id, i]));
  const rackItems = items.filter((i) => i.role === "device" || i.role === "shelf");
  const pills = items.filter((i) => i.labelShown && !i.card && i.pill.x1 > 0).map((i) => i.pill.x1);
  const x0 = Math.max(0, ...rackItems.map((i) => i.iso.bounds.x1), ...pills) + 64;
  const y0f = Math.min(...items.filter((i) => i.role === "device").map((i) => i.iso.bounds.y0), Infinity);
  const y0 = Number.isFinite(y0f) ? y0f : 0;
  const inTree = new Set(layers.flat());
  const between = links.filter((e) => inTree.has(e.a) && inTree.has(e.b));
  const toGates = links.flatMap((e) => (inTree.has(e.a) && gateRank.has(e.b) ? [{ key: e.id, card: e.a, gate: gateRank.get(e.b) as number }] : inTree.has(e.b) && gateRank.has(e.a) ? [{ key: e.id, card: e.b, gate: gateRank.get(e.a) as number }] : []));
  const plan = planTree(orderLayers(layers, between), between.map((e) => ({ key: e.id, a: e.a, b: e.b })), toGates, CARD);
  const move = (p: Pt): Pt => ({ x: fmt(p.x + x0), y: fmt(p.y + y0) });
  const routes = new Map([...plan.routes].map(([k, pts]) => [k, pts.map(move)] as const));
  const exits = new Map([...plan.exits].map(([k, x]) => [k, { pts: x.pts.map(move), laneX: fmt(x.laneX + x0) }] as const));
  const cards = new Map([...plan.cards].map(([k, c]) => [k, { ...c, x: fmt(c.x + x0), y: fmt(c.y + y0) }] as const));
  for (const [id, c] of cards) {
    const it = at.get(id);
    if (!it) continue;
    it.card = {
      x: c.x,
      y: c.y,
      w: CARD.w,
      h: CARD.h,
      icon: kindOf({ type: "device", attributes: { role: it.node.role } }).icon,
      name: it.label,
      model: it.node.model,
      layer: c.layer,
    };
    it.navAt = { x: c.x + CARD.w / 2, y: c.y + CARD.h / 2 };
    it.labelShown = false;
    it.subShown = false;
  }
  return { ...plan, routes, exits, cards, laneRight: plan.laneRight > 0 ? fmt(plan.laneRight + x0) : 0 };
}

/** The scene's edges of a rack's tree: the planned routes, and the links to the gates finished into their ports. */
function cardEdges(edges: MapEdge[], plan: TreePlan, gates: ReadonlyMap<string, Pt>): SceneEdge[] {
  const ports = edges.length <= TREE_PORTS;
  // The links that end on one gate arrive on its west side, spread along it.
  const arriving = new Map<string, MapEdge[]>();
  for (const e of edges) {
    const g = gates.has(e.a) ? e.a : gates.has(e.b) ? e.b : undefined;
    if (g && plan.exits.has(e.id)) arriving.set(g, [...(arriving.get(g) ?? []), e]);
  }
  const into = new Map<string, Pt>();
  for (const [id, list] of arriving) {
    const g = gates.get(id) as Pt;
    const sorted = [...list].sort((p, q) => (plan.exits.get(p.id)?.laneX ?? 0) - (plan.exits.get(q.id)?.laneX ?? 0));
    const off = fanOffsets(sorted.length, 7);
    sorted.forEach((e, k) => into.set(e.id, { x: g.x, y: g.y + (off[k] ?? 0) }));
  }
  const out: SceneEdge[] = [];
  for (const e of edges) {
    let pts = plan.routes.get(e.id);
    const exit = plan.exits.get(e.id);
    const to = into.get(e.id);
    if (!pts && exit && to) {
      const run = [...exit.pts, { x: exit.laneX, y: to.y }, to];
      pts = gates.has(e.a) ? run.reverse() : run;
    }
    if (!pts) continue;
    const se = finish(e, pathGeometry(e, pts), false, true);
    if (!ports) se.portsShown = false;
    out.push(se);
  }
  return out;
}

/** Routes of the cables of a room: up from a rack to a tray behind the racks, along it, down to the other rack. */
function trayEdges(edges: MapEdge[], items: Map<string, SceneItem>): { edges: SceneEdge[]; trays: IsoBox[]; rest: MapEdge[] } {
  const routed = edges.filter((e) => items.get(e.a)?.role === "rack" && items.get(e.b)?.role === "rack" && e.a !== e.b);
  const rest = edges.filter((e) => !routed.includes(e));
  const racks = [...items.values()].filter((i) => i.role === "rack");
  if (routed.length === 0 || racks.length === 0) return { edges: [], trays: [], rest: edges };
  const minY = Math.min(...racks.map((i) => i.rect.y));
  const lanes = assignLanes(routed.map((e) => ({ from: (items.get(e.a) as SceneItem).rect.x, to: (items.get(e.b) as SceneItem).rect.x })));
  // The lines that leave one rack are spread along its back.
  const touching = new Map<string, number[]>();
  routed.forEach((e, i) => {
    for (const id of [e.a, e.b]) touching.set(id, [...(touching.get(id) ?? []), i]);
  });
  const off = (id: string, i: number) => fanOffsets(touching.get(id)?.length ?? 1, 0.12)[(touching.get(id) ?? [i]).indexOf(i)] ?? 0;
  const out = routed.map((e, i) => {
    const a = items.get(e.a) as SceneItem;
    const b = items.get(e.b) as SceneItem;
    const y = trayLaneY(minY, lanes[i] as number);
    const floorPts = trayRoute({ x: a.rect.x + a.rect.w / 2 + off(e.a, i), y: a.rect.y }, { x: b.rect.x + b.rect.w / 2 + off(e.b, i), y: b.rect.y }, y);
    const pts = floorPts.map((p) => project(p.x, p.y, 0.03));
    return finish(e, pathGeometry(e, pts), true, true);
  });
  const x0 = Math.min(...racks.map((i) => i.rect.x));
  const x1 = Math.max(...racks.map((i) => i.rect.x + i.rect.w));
  const t = trayRect(x0, x1, minY, Math.max(...lanes) + 1);
  return { edges: out, trays: [projectBox({ ...t, z: -0.15, h: 0.12 })], rest };
}

/**
 * The scene of a map level turned by `rot`. `text` (1 by default) is how much bigger than its design
 * size text is drawn: labels and badges are sized and placed at that scale, and a device whose block
 * would be under LABEL_MIN_PX on screen keeps its name until pointed at.
 */
export function buildScene(m: TopologyMap, rot: Rot, opts: { text?: number } = {}): Scene {
  TEXT = opts.text ?? 1;
  try {
    return sceneOf(m, rot);
  } finally {
    TEXT = 1;
  }
}

function sceneOf(m: TopologyMap, rot: Rot): Scene {
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
  // A rack whose devices are cabled shows them as a tree of cards, the gateway on top. A device cabled
  // to nothing but a gate (a bastion to the Internet) is in the tree too: the gates rank above it.
  const gates = m.level === "rack" ? items.filter((i) => isGate(i.role)) : [];
  let layers: string[][] = [];
  if (m.level === "rack") {
    const nodes: TreeNode[] = items
      .filter((i) => i.node.kind === "device" || isGate(i.role))
      .map((i) => ({ id: i.id, kind: i.node.kind, role: i.node.role, u: i.node.mount?.position, name: i.label }));
    const tree: TreeEdge[] = m.edges.map((e) => ({ a: e.a, b: e.b, medium: e.medium, toward: e.toward }));
    const gateIds = new Set(gates.map((g) => g.id));
    layers = rankTree(nodes, tree)
      .map((l) => l.filter((id) => !gateIds.has(id)))
      .filter((l) => l.length > 0);
    for (const id of layers.flat()) {
      const it = at.get(id);
      if (it) it.labelShown = false;
    }
  }
  const frame = layout.frame ? frameScene(layout.frame, layout, rot) : undefined;
  // At the rack level the gates (the Internet, other places) stand in a column of their own, right of the
  // cards: they are placed once the rack, its labels and its cards are, so that nothing is drawn over them.
  const rest = items.filter((i) => !gates.includes(i));
  const blocks = (list: SceneItem[]): Obstacle[] => list.map((i) => ({ id: i.id, box: blockBounds(i) }));
  const frameBox = frame ? union([...frame.back, ...frame.front].map((p) => p.bounds)) : undefined;
  const fixed: Obstacle[] = [...blocks(rest), ...(frameBox ? [{ id: "frame", box: frameBox }] : [])];
  const taken: Bounds[] = [];
  // Zoomed out, the names of small things wait until pointed at; places, and what is in trouble, keep theirs.
  if (TEXT > 1)
    for (const i of rest) {
      const small = (i.iso.bounds.x1 - i.iso.bounds.x0) / TEXT < LABEL_MIN_PX;
      if (small && crowdable(i)) i.labelShown = false;
    }
  placeLabels(rest, fixed, taken);
  const gateRank = new Map(gates.map((g, k) => [g.id, k]));
  const inTree = new Set(layers.flat());
  const treeLinks = m.edges.filter((e) => e.a !== e.b && at.has(e.a) && at.has(e.b) && (inTree.has(e.a) || inTree.has(e.b)) && (inTree.has(e.a) || gateRank.has(e.a)) && (inTree.has(e.b) || gateRank.has(e.b)));
  const plan = layers.length > 0 ? placeCards(items, layers, treeLinks, gateRank) : undefined;
  const cards = new Map(items.flatMap((i) => (i.card ? [[i.id, i.card] as const] : [])));
  const gatePorts = new Map<string, Pt>();
  if (gates.length > 0) {
    const right = Math.max(0, ...[...cards.values()].map((c) => c.x + c.w), ...rest.map((i) => i.iso.bounds.x1), ...rest.filter((i) => !i.card && i.pill.x1 > 0 && i.labelShown).map((i) => i.pill.x1), frameBox?.x1 ?? 0);
    const top = Math.min(...[...cards.values()].map((c) => c.y), ...(frameBox ? [frameBox.y0] : []), Infinity);
    const from = Math.max(right + (cards.size > 0 ? 130 : 90), (plan?.laneRight ?? 0) + 60);
    const moves = columnShifts(gates.map(blockBounds), from, Number.isFinite(top) ? top : 0, 64);
    gates.forEach((g, k) => shiftItem(g, moves[k]?.x ?? 0, moves[k]?.y ?? 0));
    placeLabels(gates, [...fixed, ...blocks(gates), ...[...cards.values()].map((c, k) => ({ id: `card${k}`, box: { x0: c.x, y0: c.y, x1: c.x + c.w, y1: c.y + c.h } }))], taken);
    for (const g of gates) gatePorts.set(g.id, { x: blockBounds(g).x0 - 3, y: g.port.y });
  }
  for (const c of cards.values()) taken.push({ x0: c.x, y0: c.y, x1: c.x + c.w, y1: c.y + c.h });
  const trays: IsoBox[] = [];
  const edges: SceneEdge[] = [];
  let arcs = m.edges.filter((e) => at.has(e.a) && at.has(e.b));
  if (m.level === "room") {
    const t = trayEdges(arcs, at);
    edges.push(...t.edges);
    trays.push(...t.trays);
    arcs = t.rest;
  } else if (plan) {
    edges.push(...cardEdges(treeLinks, plan, gatePorts));
    arcs = arcs.filter((e) => !treeLinks.includes(e));
  }
  for (const e of arcs) {
    const a = at.get(e.a) as SceneItem;
    const b = at.get(e.b) as SceneItem;
    // Arcs between places carry no port names of their own unless one cable is all there is.
    edges.push(finish(e, arcGeometry(e, a, b), false, e.links === 1 && m.level !== "global" && m.level !== "site"));
  }
  // Without any live state in the map (no source reports), "unknown" says nothing about a link: it is drawn as a running one.
  const known = m.edges.some((e) => e.oper !== undefined && e.oper !== "unknown");
  if (!known) for (const e of edges) if (e.state === "unknown") e.state = "up";
  // The edges keep the order the API gave them.
  const order = new Map(m.edges.map((e, i) => [e.id, i]));
  edges.sort((p, q) => (order.get(p.edge.id) ?? 0) - (order.get(q.edge.id) ?? 0));
  if (edges.length > PORTS_MAX) for (const e of edges) e.portsShown = false;
  placeBadges(edges, taken);

  const bounds = union(
    [
      ...items.map((i) => i.iso.bounds),
      ...items.flatMap((i) => i.parts.map((p) => p.bounds)),
      ...items.flatMap((i) => (i.walls ? [...i.walls.back, ...i.walls.front].map((w) => w.bounds) : [])),
      ...items.map((i) =>
        i.card
          ? { x0: i.card.x, x1: i.card.x + i.card.w, y0: i.card.y, y1: i.card.y + i.card.h }
          : i.pill.x1 > 0
            ? i.pill
            : { x0: i.iso.bounds.x0, x1: i.iso.bounds.x1, y0: i.iso.bounds.y1, y1: i.iso.bounds.y1 + 20 },
      ),
      ...plates.map((p) => p.iso.bounds),
      ...(frame ? [...frame.back, ...frame.front].map((p) => p.bounds) : []),
      ...(frame ? frame.marks.map((mk) => ({ x0: mk.x - 20, x1: mk.x, y0: mk.y - 10, y1: mk.y + 2 })) : []),
    ],
    36,
  );
  const core = union(
    [
      ...items.map((i) => blockBounds(i)),
      ...items.flatMap((i) => (i.card ? [{ x0: i.card.x, x1: i.card.x + i.card.w, y0: i.card.y, y1: i.card.y + i.card.h }] : [])),
      ...(frame ? [...frame.back, ...frame.front].map((p) => p.bounds) : []),
    ],
    24,
  );
  return { items, plates, edges, bounds, viewBox: viewBoxOf(bounds), units: layout.units, frame, ground: groundOf(layout, rot), trays, core, text: TEXT };
}
