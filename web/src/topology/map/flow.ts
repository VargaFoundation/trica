// Traffic flows of the map: how thick a line is, which way and how fast its
// dashes run. Animation is a cost, so at most MAX_LANES lanes move (the
// busiest first) and none does when the visitor asked for reduced motion,
// switched the animation off or looks at another tab.

import type { MapEdge } from "../../api/types";
import { utilClass, type EdgeLive } from "../live";

export const MAX_LANES = 150;
export type SpeedClass = 1 | 2 | 3 | 4 | 5;
export type Direction = "ab" | "ba";

/** Line width, px: capacity on a log scale from 1.5 px (10 Mb/s) to 6 px (400 Gb/s). */
export function capacityWidth(mbps: number | undefined): number {
  if (mbps === undefined || !(mbps > 0)) return 1.5;
  const x = Math.min(1, Math.max(0, (Math.log10(mbps) - 1) / (Math.log10(400_000) - 1)));
  return Math.round((1.5 + 4.5 * x) * 10) / 10;
}

/** The speed class of a rate: one class per decade from 1 Mb/s up. */
export function speedClass(bps: number): SpeedClass {
  if (bps < 1e6) return 1;
  if (bps < 1e7) return 2;
  if (bps < 1e8) return 3;
  if (bps < 1e9) return 4;
  return 5;
}

/** The colour class of an edge, with the same thresholds as the network view. */
export function edgeUtilClass(e: MapEdge): ReturnType<typeof utilClass> {
  const live: EdgeLive = {
    oper: e.oper === "up" || e.oper === "down" || e.oper === "partial" ? e.oper : "unknown",
    measured: e.measured ? 1 : 0,
    total: e.links,
    util: e.util,
    sources: [],
  };
  return utilClass(live);
}

export interface Lane {
  edge: string;
  dir: Direction;
  cls: SpeedClass;
  bps: number;
}

export interface FlowOptions {
  /** The visitor's animation switch. */
  on: boolean;
  reduced: boolean;
  hidden: boolean;
}

/** The lanes that move: measured, running, carrying traffic, busiest first, capped. */
export function planFlows(edges: readonly MapEdge[], o: FlowOptions): Lane[] {
  if (!o.on || o.reduced || o.hidden) return [];
  const lanes: Lane[] = [];
  for (const e of edges) {
    if (!e.measured || e.oper === "down") continue;
    const ab = e.a_to_b_bps ?? 0;
    const ba = e.b_to_a_bps ?? 0;
    if (ab > 0) lanes.push({ edge: e.id, dir: "ab", cls: speedClass(ab), bps: ab });
    if (ba > 0) lanes.push({ edge: e.id, dir: "ba", cls: speedClass(ba), bps: ba });
  }
  lanes.sort((a, b) => b.bps - a.bps || a.edge.localeCompare(b.edge) || a.dir.localeCompare(b.dir));
  return lanes.slice(0, MAX_LANES);
}
