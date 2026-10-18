// The live overlay of the topology (ADR 0041): what GET /topology/live says
// of each interface, folded onto the drawn lines. Kept free of React, of the
// DOM and of cytoscape, like model.ts. The server reads the measurements; the
// page never talks to a metrics store.

import type { LiveLink, LiveTopology } from "../api/types";
import type { Edge, LinkRow } from "./model";

export type { LiveLink, LiveTopology };

/** What a line says, from its source device towards its target. */
export interface EdgeLive {
  /** Bits per second from the source device to the target, summed over the line's links. */
  out?: number;
  /** Bits per second from the target to the source. */
  back?: number;
  /** max(out, back) over the line's speed, 0 to 1 (and beyond on a misreported speed). */
  util?: number;
  /** Summed speed of the measured links, Mb/s. */
  speedMbps?: number;
  /** up, down, partial (some links down), unknown (nothing measured). */
  oper: "up" | "down" | "partial" | "unknown";
  /** Links of the line with a measurement, and in all. */
  measured: number;
  total: number;
  sources: string[];
}

/** One link's figures, from its `a` end towards its `b` end. */
export interface RowLive {
  out?: number;
  back?: number;
  util?: number;
  speedMbps?: number;
  oper?: "up" | "down";
  source?: string;
  /** The interface the figures were read on. */
  at: "a" | "b";
  aggregated?: boolean;
}

const up = (o: string | undefined) => (o === "up" || o === "down" ? o : undefined);

/**
 * A link's figures: the `a` port's when it is measured, otherwise the `b`
 * port's turned around (what b sends, a receives). A port down at either end
 * is a link down.
 */
export function rowLive(r: LinkRow, links: Readonly<Record<string, LiveLink>>): RowLive | undefined {
  const a = links[r.a.id];
  const b = links[r.b.id];
  const pick = a ?? b;
  if (!pick) return undefined;
  const oper = up(a?.oper) === "down" || up(b?.oper) === "down" ? "down" : (up(a?.oper) ?? up(b?.oper));
  if (a) return { out: a.tx_bps, back: a.rx_bps, util: a.util, speedMbps: a.speed_mbps, oper, source: a.source, at: "a", aggregated: a.aggregated };
  return { out: pick.rx_bps, back: pick.tx_bps, util: pick.util, speedMbps: pick.speed_mbps, oper, source: pick.source, at: "b", aggregated: pick.aggregated };
}

/** Folds the links of a line, each turned to read from the line's source device. */
export function edgeLive(e: Edge, links: Readonly<Record<string, LiveLink>>): EdgeLive {
  const rows = e.rows ?? [];
  const out: EdgeLive = { oper: "unknown", measured: 0, total: rows.length, sources: [] };
  let sumOut = 0;
  let sumBack = 0;
  let hasOut = false;
  let hasBack = false;
  let speed = 0;
  let maxUtil: number | undefined;
  let ups = 0;
  let downs = 0;
  for (const r of rows) {
    const m = rowLive(r, links);
    if (!m) continue;
    out.measured++;
    if (m.source && !out.sources.includes(m.source)) out.sources.push(m.source);
    const forward = r.a.deviceId === e.source;
    const o = forward ? m.out : m.back;
    const b = forward ? m.back : m.out;
    if (o !== undefined) [sumOut, hasOut] = [sumOut + o, true];
    if (b !== undefined) [sumBack, hasBack] = [sumBack + b, true];
    if (m.speedMbps) speed += m.speedMbps;
    if (m.util !== undefined) maxUtil = Math.max(maxUtil ?? 0, m.util);
    if (m.oper === "up") ups++;
    if (m.oper === "down") downs++;
  }
  if (hasOut) out.out = sumOut;
  if (hasBack) out.back = sumBack;
  if (speed > 0) out.speedMbps = speed;
  // Over the summed speed when every measured link has one; otherwise the worst link's own figure.
  if (speed > 0 && (hasOut || hasBack)) out.util = Math.max(sumOut, sumBack) / (speed * 1e6);
  else out.util = maxUtil;
  if (downs > 0 && ups === 0) out.oper = "down";
  else if (downs > 0) out.oper = "partial";
  else if (ups > 0 || hasOut || hasBack) out.oper = "up";
  return out;
}

/** The colour class of a line: neutral under 50 %, amber to 80 %, red beyond, red when down, grey when unknown. */
export type UtilClass = "normal" | "warn" | "crit" | "down" | "unknown";

export const WARN_AT = 0.5;
export const CRIT_AT = 0.8;

export function utilClass(l: EdgeLive): UtilClass {
  if (l.oper === "down") return "down";
  if (l.measured === 0) return "unknown";
  const u = l.util;
  if (u !== undefined && u >= CRIT_AT) return "crit";
  if ((u !== undefined && u >= WARN_AT) || l.oper === "partial") return "warn";
  return "normal";
}

/** Line width, px: 1 at 1 kb/s or less, 8 at 100 Gb/s or more, logarithmic between. */
export function widthOf(bps: number | undefined): number {
  if (bps === undefined || !(bps > 0)) return 1;
  const lo = 3; // 1 kb/s
  const hi = 11; // 100 Gb/s
  const x = Math.min(1, Math.max(0, (Math.log10(bps) - lo) / (hi - lo)));
  return Math.round((1 + 7 * x) * 10) / 10;
}

const UNITS = ["b/s", "kb/s", "Mb/s", "Gb/s", "Tb/s"];

/** "3,2 Gb/s" (French), "3.2 Gb/s" (English): one decimal under 10, none above. */
export function formatBps(bps: number, locale: string): string {
  let v = Math.max(0, bps);
  let i = 0;
  while (v >= 1000 && i < UNITS.length - 1) {
    v /= 1000;
    i++;
  }
  const n = new Intl.NumberFormat(locale, { maximumFractionDigits: v < 10 && i > 0 ? 1 : 0 }).format(v);
  return `${n} ${UNITS[i]}`;
}

/** "10G", "2.5G", "100M": a port speed the way a switch writes it. */
export function formatSpeed(mbps: number, locale: string): string {
  if (mbps >= 1000) return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(mbps / 1000)}G`;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(mbps)}M`;
}

export function formatUtil(u: number, locale: string): string {
  return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: u < 0.1 ? 1 : 0 }).format(u);
}

/** "↑ 3.2 Gb/s ↓ 1.1 Gb/s": out is from the line's source device. */
export function liveLabel(l: EdgeLive, locale: string): string {
  const parts: string[] = [];
  if (l.out !== undefined) parts.push(`↑ ${formatBps(l.out, locale)}`);
  if (l.back !== undefined) parts.push(`↓ ${formatBps(l.back, locale)}`);
  return parts.join(" ");
}

/**
 * "Po11 (2×10G)": an aggregate's name, its member count and, when every
 * member's speed is known and the same, that speed.
 */
export function lagLabel(name: string, rows: readonly LinkRow[], links: Readonly<Record<string, LiveLink>>, locale: string): string {
  const speeds = rows.map((r) => links[r.a.id]?.speed_mbps ?? links[r.b.id]?.speed_mbps);
  const n = rows.length;
  const first = speeds[0];
  const same = first !== undefined && speeds.every((s) => s === first);
  return same ? `${name} (${n}×${formatSpeed(first, locale)})` : `${name} (${n}×)`;
}

/** How old the measurements are, in seconds; undefined before the first. */
export function ageOf(data: Pick<LiveTopology, "generated_at">, now: number): number | undefined {
  if (!data.generated_at) return undefined;
  const t = Date.parse(data.generated_at);
  return Number.isNaN(t) ? undefined : Math.max(0, Math.round((now - t) / 1000));
}

/** Stale when the server says so, or when nothing new came for three refresh periods. */
export function isStale(data: LiveTopology, now: number): boolean {
  if (data.stale) return true;
  const age = ageOf(data, now);
  if (age === undefined) return true;
  return age > 3 * Math.max(data.refresh_seconds, 1);
}
