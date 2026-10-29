// Traffic flows of the map: how thick a line is, which way and how fast its
// dashes run. Animation is a cost, so at most MAX_LANES lanes move (the
// busiest first) and none does when the visitor asked for reduced motion,
// switched the animation off or looks at another tab.

import type { MapEdge } from "../../api/types";
import { utilClass, type EdgeLive } from "../live";

export const MAX_LANES = 150;
export type SpeedClass = 1 | 2 | 3 | 4 | 5;
export type Direction = "ab" | "ba";

export type LinkState = "up" | "down" | "unknown";

/** What a line says about the link: running, down, or not known (no state reported). */
export function linkState(e: Pick<MapEdge, "oper">): LinkState {
  return e.oper === "down" ? "down" : e.oper === "up" || e.oper === "partial" ? "up" : "unknown";
}

/** Dashes of a state: a down link is dashed, an unknown one dotted, a running one solid. */
export const STATE_DASH: Readonly<Record<LinkState, string | undefined>> = { up: undefined, down: "6 4", unknown: "1.5 5" };

/** Seconds a particle takes to cross a line, by the speed class of the rate it stands for. */
export const PARTICLE_SECONDS: Readonly<Record<SpeedClass, number>> = { 1: 7, 2: 5.5, 3: 4, 4: 2.8, 5: 1.8 };
/** px between two particles of a lane, by speed class: the busier the closer. */
export const PARTICLE_SPACING: Readonly<Record<SpeedClass, number>> = { 1: 110, 2: 80, 3: 58, 4: 40, 5: 28 };
export const MAX_PARTICLES = 8;

/** How many particles travel a lane of `len` px: one at least, more as the rate grows, at most MAX_PARTICLES. */
export function particleCount(len: number, cls: SpeedClass): number {
  return Math.max(1, Math.min(MAX_PARTICLES, Math.round(len / PARTICLE_SPACING[cls])));
}

/**
 * The traffic of a lane as a moving sheen: a soft streak every `period` px (as many as particles would
 * be), `streak` px long, that runs the lane at the particles' speed. `seconds` is the time one period takes.
 */
export function sheen(len: number, cls: SpeedClass): { dash: string; period: number; seconds: number } {
  const n = particleCount(len, cls);
  const period = Math.max(len / n, 1);
  const streak = Math.min(period * 0.3, 16);
  const fmt = (x: number) => Math.round(x * 10) / 10;
  return { dash: `${fmt(streak)} ${fmt(period - streak)}`, period: fmt(period), seconds: fmt((PARTICLE_SECONDS[cls] * period) / Math.max(len, 1)) || 0.1 };
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
