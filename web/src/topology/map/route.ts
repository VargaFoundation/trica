// Geometry of the lines: orthogonal paths with rounded corners, parallel
// offsets, the fan of lines that share a side, the lanes of a cable tray. Pure
// arithmetic on points, tested without a DOM.

import type { Pt } from "./iso";

export type Side = "n" | "s" | "e" | "w";

const fmt = (n: number) => Math.round(n * 10) / 10;
const dist = (a: Pt, b: Pt) => Math.hypot(b.x - a.x, b.y - a.y);

/** Consecutive duplicates and points on a straight run removed. */
export function simplify(pts: readonly Pt[]): Pt[] {
  const out: Pt[] = [];
  for (const p of pts) {
    const last = out[out.length - 1];
    if (last && dist(last, p) < 0.01) continue;
    out.push(p);
  }
  const keep: Pt[] = [];
  out.forEach((p, i) => {
    const a = out[i - 1];
    const b = out[i + 1];
    if (a && b && Math.abs((p.x - a.x) * (b.y - p.y) - (p.y - a.y) * (b.x - p.x)) < 0.01) return;
    keep.push(p);
  });
  return keep;
}

/** An SVG path through the points whose corners are rounded with radius r (less where a segment is short). */
export function roundedPath(pts: readonly Pt[], r: number): string {
  const p = simplify(pts);
  const first = p[0];
  if (!first) return "";
  let d = `M${fmt(first.x)},${fmt(first.y)}`;
  for (let i = 1; i < p.length; i++) {
    const cur = p[i] as Pt;
    const next = p[i + 1];
    if (!next) {
      d += `L${fmt(cur.x)},${fmt(cur.y)}`;
      break;
    }
    const prev = p[i - 1] as Pt;
    const rr = Math.min(r, dist(prev, cur) / 2, dist(cur, next) / 2);
    const u = (a: Pt, b: Pt) => ({ x: (a.x - b.x) / (dist(a, b) || 1), y: (a.y - b.y) / (dist(a, b) || 1) });
    const ua = u(prev, cur);
    const ub = u(next, cur);
    d += `L${fmt(cur.x + ua.x * rr)},${fmt(cur.y + ua.y * rr)}Q${fmt(cur.x)},${fmt(cur.y)} ${fmt(cur.x + ub.x * rr)},${fmt(cur.y + ub.y * rr)}`;
  }
  return d;
}

export function polylineLength(pts: readonly Pt[]): number {
  let n = 0;
  for (let i = 1; i < pts.length; i++) n += dist(pts[i - 1] as Pt, pts[i] as Pt);
  return n;
}

/** The point `s` px along the polyline (clamped to its ends). */
export function pointAtDistance(pts: readonly Pt[], s: number): Pt {
  const first = pts[0];
  if (!first) return { x: 0, y: 0 };
  let left = Math.max(0, s);
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1] as Pt;
    const b = pts[i] as Pt;
    const len = dist(a, b);
    if (left <= len) return len === 0 ? a : { x: a.x + ((b.x - a.x) * left) / len, y: a.y + ((b.y - a.y) * left) / len };
    left -= len;
  }
  return pts[pts.length - 1] as Pt;
}

/** The point at a fraction (0 to 1) of the polyline's length. */
export const pointAt = (pts: readonly Pt[], t: number): Pt => pointAtDistance(pts, polylineLength(pts) * t);

/** The polyline moved sideways by `off` px (positive: to the right of its direction, y pointing down); corners stay mitred. */
export function offsetPolyline(pts: readonly Pt[], off: number): Pt[] {
  const p = simplify(pts);
  if (p.length < 2 || off === 0) return p;
  const lines = p.slice(1).map((b, i) => {
    const a = p[i] as Pt;
    const len = dist(a, b) || 1;
    const nx = -(b.y - a.y) / len;
    const ny = (b.x - a.x) / len;
    return { a: { x: a.x + nx * off, y: a.y + ny * off }, b: { x: b.x + nx * off, y: b.y + ny * off } };
  });
  const out: Pt[] = [(lines[0] as (typeof lines)[number]).a];
  for (let i = 1; i < lines.length; i++) {
    const l1 = lines[i - 1] as (typeof lines)[number];
    const l2 = lines[i] as (typeof lines)[number];
    const d1 = { x: l1.b.x - l1.a.x, y: l1.b.y - l1.a.y };
    const d2 = { x: l2.b.x - l2.a.x, y: l2.b.y - l2.a.y };
    const den = d1.x * d2.y - d1.y * d2.x;
    if (Math.abs(den) < 1e-9) out.push(l2.a);
    else {
      const t = ((l2.a.x - l1.a.x) * d2.y - (l2.a.y - l1.a.y) * d2.x) / den;
      out.push({ x: l1.a.x + d1.x * t, y: l1.a.y + d1.y * t });
    }
  }
  out.push((lines[lines.length - 1] as (typeof lines)[number]).b);
  return out;
}

/** n offsets centred on 0, `gap` apart: the fan of lines that leave by one side. */
export function fanOffsets(n: number, gap: number): number[] {
  return Array.from({ length: Math.max(0, n) }, (_, i) => fmt((i - (n - 1) / 2) * gap));
}

const DIR: Record<Side, Pt> = { n: { x: 0, y: -1 }, s: { x: 0, y: 1 }, e: { x: 1, y: 0 }, w: { x: -1, y: 0 } };

/**
 * An orthogonal route from a point leaving by side `aSide` to one entering by `bSide`:
 * a stub out of each device, then at most one elbow pair. `mid` fixes the channel
 * (y for vertical sides, x for horizontal ones) the two stubs are joined through.
 */
export function orthogonalRoute(a: Pt, aSide: Side, b: Pt, bSide: Side, opts: { stub?: number; mid?: number } = {}): Pt[] {
  const stub = opts.stub ?? 14;
  const sa = { x: a.x + DIR[aSide].x * stub, y: a.y + DIR[aSide].y * stub };
  const sb = { x: b.x + DIR[bSide].x * stub, y: b.y + DIR[bSide].y * stub };
  const vertical = (s: Side) => s === "n" || s === "s";
  let via: Pt[];
  if (vertical(aSide) && vertical(bSide)) {
    const same = aSide === bSide;
    const mid = opts.mid ?? (same ? (aSide === "s" ? Math.max(sa.y, sb.y) : Math.min(sa.y, sb.y)) + DIR[aSide].y * stub : (sa.y + sb.y) / 2);
    via = [{ x: sa.x, y: mid }, { x: sb.x, y: mid }];
  } else if (!vertical(aSide) && !vertical(bSide)) {
    const same = aSide === bSide;
    const mid = opts.mid ?? (same ? (aSide === "e" ? Math.max(sa.x, sb.x) : Math.min(sa.x, sb.x)) + DIR[aSide].x * stub : (sa.x + sb.x) / 2);
    via = [{ x: mid, y: sa.y }, { x: mid, y: sb.y }];
  } else via = [vertical(aSide) ? { x: sa.x, y: sb.y } : { x: sb.x, y: sa.y }];
  return simplify([a, sa, ...via, sb, b]);
}

/**
 * A route from a card to a gate that stands to the right of every card: out of the card's east side
 * (when no card is to its right) or its top (into the channel above its row), along that line to the
 * lane `laneX`, along the lane to the height of the gate's port, and into the port. Never crosses a card.
 */
export function gateRoute(a: Pt, aSide: "e" | "n", g: Pt, opts: { laneX: number; chanY?: number; stub?: number }): Pt[] {
  const stub = opts.stub ?? 12;
  if (aSide === "e") return simplify([a, { x: a.x + stub, y: a.y }, { x: Math.max(opts.laneX, a.x + stub), y: a.y }, { x: Math.max(opts.laneX, a.x + stub), y: g.y }, g]);
  const y = opts.chanY ?? a.y - stub;
  return simplify([a, { x: a.x, y }, { x: opts.laneX, y }, { x: opts.laneX, y: g.y }, g]);
}

/** A route along a cable tray: from a up (or down) to the tray line, along it, then to b. Coordinates are those of the floor plane. */
export function trayRoute(a: Pt, b: Pt, trayY: number): Pt[] {
  return simplify([a, { x: a.x, y: trayY }, { x: b.x, y: trayY }, b]);
}

/**
 * Lanes of a tray: spans that overlap on the tray take different lanes, the lowest
 * free one, in the order given. Returns each span's lane.
 */
export function assignLanes(spans: readonly { from: number; to: number }[]): number[] {
  const order = spans.map((s, i) => ({ lo: Math.min(s.from, s.to), hi: Math.max(s.from, s.to), i })).sort((p, q) => p.lo - q.lo || p.hi - q.hi || p.i - q.i);
  const ends: number[] = [];
  const out: number[] = new Array(spans.length).fill(0);
  for (const s of order) {
    let lane = ends.findIndex((e) => e < s.lo - 0.05);
    if (lane < 0) lane = ends.length;
    ends[lane] = s.hi;
    out[s.i] = lane;
  }
  return out;
}

/** The point of a quadratic curve (p0, control c, p1) at t. */
export function quadAt(p0: Pt, c: Pt, p1: Pt, t: number): Pt {
  const u = 1 - t;
  return { x: u * u * p0.x + 2 * u * t * c.x + t * t * p1.x, y: u * u * p0.y + 2 * u * t * c.y + t * t * p1.y };
}

/** The length of a quadratic curve, from 16 chords. */
export function quadLength(p0: Pt, c: Pt, p1: Pt): number {
  let n = 0;
  let prev = p0;
  for (let i = 1; i <= 16; i++) {
    const q = quadAt(p0, c, p1, i / 16);
    n += dist(prev, q);
    prev = q;
  }
  return n;
}

/** Distance between two lanes of a tray, in cells. */
export const TRAY_GAP = 0.14;

/** Where lane k runs: y of the floor plane, on the far side of the racks (minY is the racks' top edge). */
export const trayLaneY = (minY: number, k: number): number => minY - 0.45 - k * TRAY_GAP;

/** The footprint of the tray that carries `lanes` lanes over the span x0 to x1. */
export function trayRect(x0: number, x1: number, minY: number, lanes: number): { x: number; y: number; w: number; d: number } {
  const n = Math.max(1, lanes);
  const top = trayLaneY(minY, n - 1) - 0.2;
  return { x: x0 - 0.3, y: top, w: x1 - x0 + 0.6, d: trayLaneY(minY, 0) + 0.2 - top };
}
