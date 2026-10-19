// The geometry of a measurement curve, kept apart from React so that it is
// tested on numbers: where each bucket falls, where the readings stop (a gap
// is drawn as a gap, never bridged), and a value scale with some air.
import type { SeriesPoint } from "../api/types";

export interface Box {
  width: number;
  height: number;
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export interface Scale {
  x: (t: number) => number;
  y: (v: number) => number;
  lo: number;
  hi: number;
}

/** Points in runs with no missing bucket between them: a run is one stretch of line. */
export function runs(points: SeriesPoint[], stepSeconds: number): SeriesPoint[][] {
  const out: SeriesPoint[][] = [];
  let run: SeriesPoint[] = [];
  let prev: number | undefined;
  for (const p of points) {
    const t = Date.parse(p.t);
    if (prev === undefined || t - prev > stepSeconds * 1000 * 1.5) {
      run = [];
      out.push(run);
    }
    run.push(p);
    prev = t;
  }
  return out;
}

/** The value range drawn: the readings' lowest and highest, widened by a tenth, never flat. */
export function valueRange(points: SeriesPoint[]): [number, number] {
  let lo = Infinity;
  let hi = -Infinity;
  for (const p of points) {
    lo = Math.min(lo, p.min);
    hi = Math.max(hi, p.max);
  }
  if (!Number.isFinite(lo)) return [0, 1];
  const pad = hi > lo ? (hi - lo) / 10 : Math.max(Math.abs(hi) / 10, 1);
  return [lo - pad, hi + pad];
}

export function scale(box: Box, from: number, to: number, [lo, hi]: [number, number]): Scale {
  const w = box.width - box.left - box.right;
  const h = box.height - box.top - box.bottom;
  return {
    x: (t) => box.left + ((t - from) / (to - from)) * w,
    y: (v) => box.top + (1 - (v - lo) / (hi - lo)) * h,
    lo,
    hi,
  };
}

/** The middle of a bucket, where its point is drawn. */
export const middle = (p: SeriesPoint, stepSeconds: number): number => Date.parse(p.t) + (stepSeconds * 1000) / 2;

const r = (n: number) => Math.round(n * 10) / 10;

/** The average as a polyline path, one sub-path per run. */
export function linePath(rs: SeriesPoint[][], stepSeconds: number, s: Scale): string {
  return rs
    .map((run) => run.map((p, i) => `${i === 0 ? "M" : "L"}${r(s.x(middle(p, stepSeconds)))},${r(s.y(p.avg))}`).join(""))
    .join("");
}

/** The band from the lowest to the highest reading, one closed shape per run. */
export function bandPath(rs: SeriesPoint[][], stepSeconds: number, s: Scale): string {
  return rs
    .map((run) => {
      const top = run.map((p, i) => `${i === 0 ? "M" : "L"}${r(s.x(middle(p, stepSeconds)))},${r(s.y(p.max))}`).join("");
      const bottom = [...run]
        .reverse()
        .map((p) => `L${r(s.x(middle(p, stepSeconds)))},${r(s.y(p.min))}`)
        .join("");
      return `${top}${bottom}Z`;
    })
    .join("");
}

/** The index of the point nearest to time t, or -1 when there is none. */
export function nearest(points: SeriesPoint[], stepSeconds: number, t: number): number {
  let best = -1;
  let dist = Infinity;
  points.forEach((p, i) => {
    const d = Math.abs(middle(p, stepSeconds) - t);
    if (d < dist) {
      dist = d;
      best = i;
    }
  });
  return best;
}
