// Label de-collision: a label takes the first position that does not run into
// one already placed, nor into a block of the map. Pure rectangles, tested
// without a DOM.

import type { Bounds, Pt } from "./iso";

/** Whether two rectangles touch or come closer than `pad` px. */
export function overlaps(a: Bounds, b: Bounds, pad = 2): boolean {
  return a.x0 - pad < b.x1 && b.x0 < a.x1 + pad && a.y0 - pad < b.y1 && b.y0 < a.y1 + pad;
}

/** Whether a rectangle runs into any of the others. */
export const hitsAny = (r: Bounds, others: readonly Bounds[], pad = 2): boolean => others.some((o) => overlaps(r, o, pad));

/** The index of the first candidate that is clear of every taken rectangle, or -1. */
export function firstFree(taken: readonly Bounds[], candidates: readonly Bounds[], pad = 2): number {
  return candidates.findIndex((c) => !hitsAny(c, taken, pad));
}

/** The smallest rectangle that holds both. */
export const merge = (a: Bounds, b: Bounds): Bounds => ({ x0: Math.min(a.x0, b.x0), y0: Math.min(a.y0, b.y0), x1: Math.max(a.x1, b.x1), y1: Math.max(a.y1, b.y1) });

/** The size of a label's pill, from an estimate of its text (it is not measured). */
export type PillSize = (withSub: boolean) => { w: number; h: number };

export interface PillSpec {
  /** The default spot: the pill's centre x and top y, or its left x and top y when it is `start`. */
  at: Pt;
  /** The pill is written from `at.x` rightwards rather than centred on it. */
  start: boolean;
  /** Top of the block, where a pill that stands above it ends (a little above it). */
  top: number;
  size: PillSize;
  /** The item has a caption to try first. */
  hasSub: boolean;
  /** The pill stays beside its block, at the height of its unit (a rack's devices): it loses its caption, then is hidden. */
  beside: boolean;
  /** How much bigger than its design size the text is drawn (the steps between spots grow with it); 1 by default. */
  scale?: number;
  /** The pill may only stand under its block, or one step lower: a name far from what it names helps nobody in a crowd. */
  near?: boolean;
}

export interface PillSlot {
  rect: Bounds;
  withSub: boolean;
  /** Where it went, relative to the default spot (0, 0 is the default). */
  dx: number;
  dy: number;
  above: boolean;
}

const DX = [0, 56, -56, 112, -112, 168, -168, 224, -224, 280, -280];
const DY = [0, 24, 48, 72, 96, 120, 144];
const DY_ABOVE = [0, 24, 48, 72];
const DY_BESIDE = [0];

/**
 * Where a pill may stand, nearest to its default first: straight below the block, then lower, then
 * sideways (the farther the dearer), then above it. With its caption first, then without, at each one.
 * A pill that stays beside its block has no other spot than its own.
 */
export function pillSlots(spec: PillSpec): PillSlot[] {
  const k = spec.scale ?? 1;
  const offsets: { dx: number; dy: number; above: boolean; cost: number }[] = [];
  if (spec.near && !spec.beside) {
    for (const dy of DY.slice(0, 2)) offsets.push({ dx: 0, dy: dy * k, above: false, cost: dy });
  } else if (spec.beside) {
    for (const dy of DY_BESIDE) offsets.push({ dx: 0, dy: dy * k, above: false, cost: Math.abs(dy) });
  } else {
    for (const dx of DX) for (const dy of DY) offsets.push({ dx: dx * k, dy: dy * k, above: false, cost: dy + Math.abs(dx) * 1.2 });
    for (const dx of DX) for (const dy of DY_ABOVE) offsets.push({ dx: dx * k, dy: dy * k, above: true, cost: 60 + dy + Math.abs(dx) * 1.2 });
  }
  offsets.sort((a, b) => a.cost - b.cost);
  const out: PillSlot[] = [];
  for (const o of offsets) {
    for (const withSub of spec.hasSub ? [true, false] : [false]) {
      const { w, h } = spec.size(withSub);
      const x0 = (spec.start ? spec.at.x : spec.at.x - w / 2) + o.dx;
      const y0 = o.above ? spec.top - 6 * k - o.dy - h : spec.at.y + o.dy;
      out.push({ rect: { x0, y0, x1: x0 + w, y1: y0 + h }, withSub, dx: o.dx, dy: o.dy, above: o.above });
    }
  }
  return out;
}

/** The first slot clear of every pill already placed and of every block (the pill's own block is left out by the caller). */
export function placePill(spec: PillSpec, taken: readonly Bounds[], obstacles: readonly Bounds[], pad = 3): PillSlot | undefined {
  return pillSlots(spec).find((s) => !hitsAny(s.rect, taken, pad) && !hitsAny(s.rect, obstacles, pad));
}

/** The line that joins a point of a block to the nearest edge of its pill. */
export function leaderBetween(from: Pt, pill: Bounds): { x1: number; y1: number; x2: number; y2: number } {
  const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));
  if (from.y <= pill.y0) return { x1: from.x, y1: from.y, x2: clamp(from.x, pill.x0 + 10, pill.x1 - 10), y2: pill.y0 };
  if (from.y >= pill.y1) return { x1: from.x, y1: from.y, x2: clamp(from.x, pill.x0 + 10, pill.x1 - 10), y2: pill.y1 };
  return { x1: from.x, y1: from.y, x2: from.x <= pill.x0 ? pill.x0 : pill.x1, y2: clamp(from.y, pill.y0 + 4, pill.y1 - 4) };
}

/** The box of a badge centred on a point, its text drawn `k` times its design size. */
export const badgeBox = (p: Pt, w: number, k = 1): Bounds => ({ x0: p.x - w / 2, y0: p.y - 8 * k, x1: p.x + w / 2, y1: p.y + 7 * k });

/**
 * Distances along a line, in px from the end it is measured from, where a badge may sit: its
 * preferred one first, then the following ones every `step`, up to `limit`.
 */
export function badgeDistances(first: number, limit: number, step = 22): number[] {
  const out: number[] = [];
  for (let s = first; s <= limit; s += step) out.push(s);
  return out.length > 0 ? out : [first];
}
