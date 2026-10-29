// The small things that make a block read as a building, a cabinet or a floor:
// windows, door glass, vent lines, status LEDs, cut-away walls, text painted on
// the floor. Everything works on a footprint already turned (x east, y south)
// and returns screen points: pure arithmetic, tested without a DOM.

import { CELL, project, type Box, type Pt } from "./iso";

export interface Rect {
  x: number;
  y: number;
  w: number;
  d: number;
}
/** The two faces the viewer sees: the south one (y = y + d) and the east one (x = x + w). */
export type Face = "south" | "east";

const fmt = (n: number) => Math.round(n * 10) / 10;
const str = (ps: readonly Pt[]) => ps.map((p) => `${fmt(p.x)},${fmt(p.y)}`).join(" ");

/** A point of a face: u from 0 to 1 along it (left to right on the south face, back to front on the east one), at height z. */
export function facePt(r: Rect, face: Face, u: number, z: number): Pt {
  return face === "south" ? project(r.x + u * r.w, r.y + r.d, z) : project(r.x + r.w, r.y + u * r.d, z);
}

/** The quadrilateral of a part of a face, as polygon points. */
export function facePoly(r: Rect, face: Face, u0: number, u1: number, z0: number, z1: number): string {
  return str([facePt(r, face, u0, z1), facePt(r, face, u1, z1), facePt(r, face, u1, z0), facePt(r, face, u0, z0)]);
}

/** The length of a face in cells. */
const span = (r: Rect, face: Face) => (face === "south" ? r.w : r.d);

/** Rows and columns of windows on both faces of a building of height h (base z0). */
export function windows(r: Rect, z0: number, h: number): string[] {
  const rows = Math.max(1, Math.floor((h - 0.25) / 0.5));
  const out: string[] = [];
  for (const face of ["south", "east"] as const) {
    const cols = Math.max(1, Math.floor(span(r, face) / 0.6));
    for (let c = 0; c < cols; c++) {
      for (let k = 0; k < rows; k++) {
        const u0 = (c + 0.22) / cols;
        const u1 = (c + 0.78) / cols;
        const lo = z0 + 0.22 + k * 0.5;
        out.push(facePoly(r, face, u0, u1, lo, lo + 0.28));
      }
    }
  }
  return out;
}

/** The glass of a cabinet's door: the face inset by a frame. */
export function doorGlass(r: Rect, face: Face, z0: number, h: number, inset = 0.08): string {
  return facePoly(r, face, inset / span(r, face), 1 - inset / span(r, face), z0 + h * 0.06, z0 + h * 0.94);
}

/** Horizontal vent slits across a door, as one path: n lines between the heights z0 and z1. */
export function ventLines(r: Rect, face: Face, z0: number, z1: number, n: number): string {
  let d = "";
  for (let i = 0; i < n; i++) {
    const z = z0 + ((i + 0.5) / n) * (z1 - z0);
    const a = facePt(r, face, 0.2, z);
    const b = facePt(r, face, 0.8, z);
    d += `M${fmt(a.x)},${fmt(a.y)}L${fmt(b.x)},${fmt(b.y)}`;
  }
  return d;
}

/** At most this many LEDs are drawn on a door. */
export const MAX_LEDS = 12;

/** n LED positions in rows of `per` across the top of a door; n beyond MAX_LEDS is cut. */
export function ledDots(r: Rect, face: Face, zTop: number, n: number, per = 6): Pt[] {
  const count = Math.max(0, Math.min(n, MAX_LEDS));
  return Array.from({ length: count }, (_, i) => facePt(r, face, 0.22 + ((i % per) / Math.max(per - 1, 1)) * 0.56, zTop - Math.floor(i / per) * 0.13));
}

/** Health of each LED of a cabinet: the worst first, so that a problem is the first light one sees. */
export function ledStates(h: { ok: number; warning: number; critical: number; unknown: number }): string[] {
  const all = [
    ...Array<string>(Math.max(0, h.critical)).fill("critical"),
    ...Array<string>(Math.max(0, h.warning)).fill("warning"),
    ...Array<string>(Math.max(0, h.unknown)).fill("unknown"),
    ...Array<string>(Math.max(0, h.ok)).fill("ok"),
  ];
  if (all.length <= MAX_LEDS) return all;
  // Too many devices for the door: keep every problem, fill the rest with ok.
  const bad = all.filter((s) => s === "critical" || s === "warning").slice(0, MAX_LEDS);
  return [...bad, ...all.filter((s) => s !== "critical" && s !== "warning").slice(0, MAX_LEDS - bad.length)];
}

/** Which face of a footprint a facing vector (already turned) shows, if it is visible. */
export function faceOf(facing: Pt | undefined): Face | undefined {
  if (!facing) return "south";
  if (facing.y > 0.5) return "south";
  if (facing.x > 0.5) return "east";
  return undefined;
}

/** The SVG transform that lays text flat on the floor, along the x axis, centred on the footprint. */
export function floorMatrix(r: Rect, z: number, scale = 0.9): string {
  const c = project(r.x + r.w / 2, r.y + r.d / 2, z);
  const k = scale;
  return `matrix(${fmt(k)} ${fmt(k / 2)} ${fmt(-k)} ${fmt(k / 2)} ${fmt(c.x)} ${fmt(c.y)})`;
}

/** The longest text (in px of 12 px font) that fits flat along a footprint's x axis. */
export const floorRoom = (r: Rect): number => Math.max(0, r.w * CELL * 0.9);

/**
 * Cut-away walls of a room, in turned coordinates: the two on the far sides (north
 * and west) tall, the two on the near sides low, so that the inside stays in view.
 */
export function wallBoxes(r: Rect, z: number, hBack: number, hFront: number, t = 0.14): { back: Box[]; front: Box[] } {
  return {
    back: [
      { x: r.x, y: r.y, w: r.w, d: t, z, h: hBack },
      { x: r.x, y: r.y + t, w: t, d: Math.max(r.d - t, 0.01), z, h: hBack },
    ],
    front: [
      { x: r.x + t, y: r.y + r.d - t, w: Math.max(r.w - t, 0.01), d: t, z, h: hFront },
      { x: r.x + r.w - t, y: r.y + t, w: t, d: Math.max(r.d - 2 * t, 0.01), z, h: hFront },
    ],
  };
}

/** The ports of a device's front as one path of small squares, n across the middle of the face. */
export function portStrip(r: Rect, z0: number, h: number, n: number): string {
  const count = Math.max(0, n);
  let d = "";
  for (let i = 0; i < count; i++) {
    const u0 = 0.1 + (i / Math.max(count, 1)) * 0.62;
    const u1 = u0 + 0.62 / Math.max(count, 1) * 0.6;
    const p = facePoly(r, "south", u0, u1, z0 + h * 0.3, z0 + h * 0.7).split(" ");
    d += `M${p.join("L")}Z`;
  }
  return d;
}

/** What the front of a device shows, as a product's does: dark openings (ports, bays, outlets), light lines, a screen. */
export interface DeviceFront {
  kind: "switch" | "gateway" | "server" | "storage" | "pdu" | "generic";
  /** Openings, one path of closed shapes. */
  holes: string;
  /** Vent slits and bay handles, one path of lines. */
  lines: string;
  /** A small display (a firewall's or a router's), as polygon points. */
  screen?: string;
}

/** A grid of small openings on the south face: `cols` by `rows` between u0 and u1, in the middle of the face's height. */
export function portGrid(r: Rect, z0: number, h: number, cols: number, rows: number, u0: number, u1: number): string {
  let d = "";
  const cw = (u1 - u0) / Math.max(cols, 1);
  const band = h * 0.56;
  const rh = band / Math.max(rows, 1);
  for (let k = 0; k < rows; k++) {
    for (let i = 0; i < cols; i++) {
      const a = u0 + i * cw + cw * 0.18;
      const b = u0 + (i + 1) * cw - cw * 0.18;
      const lo = z0 + h * 0.22 + k * rh + rh * 0.16;
      const p = facePoly(r, "south", a, b, lo, lo + rh * 0.68).split(" ");
      d += `M${p.join("L")}Z`;
    }
  }
  return d;
}

/** Drive bays: `n` tall slots side by side between u0 and u1, `rows` high. */
export function driveBays(r: Rect, z0: number, h: number, n: number, rows: number, u0: number, u1: number): string {
  let d = "";
  const cw = (u1 - u0) / Math.max(n, 1);
  const rh = (h * 0.8) / Math.max(rows, 1);
  for (let k = 0; k < rows; k++) {
    for (let i = 0; i < n; i++) {
      const lo = z0 + h * 0.1 + k * rh + rh * 0.08;
      const p = facePoly(r, "south", u0 + i * cw + cw * 0.08, u0 + (i + 1) * cw - cw * 0.08, lo, lo + rh * 0.84).split(" ");
      d += `M${p.join("L")}Z`;
    }
  }
  return d;
}

/** `n` vertical slits between u0 and u1, as one path of lines. */
export function slits(r: Rect, z0: number, h: number, n: number, u0: number, u1: number): string {
  let d = "";
  for (let i = 0; i < n; i++) {
    const u = u0 + ((i + 0.5) / Math.max(n, 1)) * (u1 - u0);
    const a = facePt(r, "south", u, z0 + h * 0.25);
    const b = facePt(r, "south", u, z0 + h * 0.75);
    d += `M${fmt(a.x)},${fmt(a.y)}L${fmt(b.x)},${fmt(b.y)}`;
  }
  return d;
}

/** How many of a thing fit along a face of width w (cells), one every `pitch` cells, within [lo, hi]. */
const fit = (w: number, pitch: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, Math.floor(w / pitch)));

/** The front of a device of a role, on the south face of its footprint (height h from z0). */
export function deviceFront(role: string | undefined, r: Rect, z0: number, h: number): DeviceFront {
  const tall = h >= 0.6;
  switch (role) {
    case "switch":
    case "patch_panel":
      return { kind: "switch", holes: portGrid(r, z0, h, fit(r.w, 0.085, 6, 24), tall ? 4 : 2, 0.06, 0.8), lines: "" };
    case "firewall":
    case "router":
    case "access_control":
      return { kind: "gateway", holes: portGrid(r, z0, h, fit(r.w, 0.2, 4, 8), 1, 0.42, 0.82), lines: "", screen: facePoly(r, "south", 0.08, 0.32, z0 + h * 0.28, z0 + h * 0.72) };
    case "server":
      return { kind: "server", holes: driveBays(r, z0, h, fit(r.w, 0.16, 4, 12), tall ? 2 : 1, 0.05, 0.6), lines: slits(r, z0, h, fit(r.w, 0.09, 4, 14), 0.64, 0.86) };
    case "storage":
    case "nas":
      return { kind: "storage", holes: driveBays(r, z0, h, fit(r.w, 0.14, 4, 16), tall ? 3 : 2, 0.05, 0.86), lines: "" };
    case "pdu":
    case "ups":
      return { kind: "pdu", holes: portGrid(r, z0, h, fit(r.w, 0.16, 4, 12), 1, 0.06, 0.86), lines: "" };
    default:
      return { kind: "generic", holes: portStrip(r, z0, h, Math.max(4, Math.min(16, Math.floor(r.w / 0.16)))), lines: "" };
  }
}
