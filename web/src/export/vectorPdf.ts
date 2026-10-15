// A figure drawn as a vector PDF: lines, boxes and text, so that the text can
// be selected and searched and the drawing stays sharp at any zoom. The font is
// Helvetica, one of the fonts every PDF viewer carries: nothing is embedded.
// Its encoding (WinAnsi) holds Latin-1 only; any other character is written
// "?" (docs/reference/ui.md says so).

import { ascii, assemblePdf, MAX_PAGE_PT, num, pdfTitle } from "./pdf";

/** CSS pixels are 1/96 inch, a PDF point is 1/72. */
export const PT_PER_PX = 0.75;

export interface Point {
  x: number;
  y: number;
}

export interface Stroke {
  /** #rrggbb */
  color: string;
  width: number;
  /** Lengths of dashes and gaps, in px; none for a solid line. */
  dash?: number[];
}

/** A figure in CSS pixels, origin at the top left, y downwards: the screen's coordinates. */
export type Shape =
  /** A straight line, or a quadratic curve through the control point `via`. */
  | { kind: "line"; from: Point; to: Point; via?: Point; stroke: Stroke }
  | { kind: "box"; x: number; y: number; w: number; h: number; radius: number; fill: string; stroke: Stroke }
  /** Text centred on `at`, on a background rectangle when one is given. */
  | { kind: "text"; at: Point; text: string; size: number; color: string; background?: string };

export interface Figure {
  width: number;
  height: number;
  /** Drawn in order: the later ones on top. */
  shapes: Shape[];
}

// Helvetica advance widths (Adobe's AFM, per 1000 units of the font size) for
// the characters 32 to 126; the others are taken as 556, a digit's width.
const HELVETICA = [
  278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556,
  556, 556, 278, 278, 584, 584, 584, 556, 1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
  667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556, 333, 556, 556, 500, 556, 556, 278, 556,
  556, 222, 222, 500, 222, 833, 556, 556, 556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
];

/** The width of a text set in Helvetica at this size, in the same unit as the size. */
export function textWidth(text: string, size: number): number {
  let units = 0;
  for (const ch of latin1Text(text)) {
    const c = ch.charCodeAt(0);
    units += c >= 32 && c <= 126 ? (HELVETICA[c - 32] as number) : 556;
  }
  return (units * size) / 1000;
}

/** What the font can write: printable Latin-1 kept, anything else "?". */
export function latin1Text(text: string): string {
  let out = "";
  for (const ch of text) {
    const c = ch.codePointAt(0) as number;
    out += (c >= 0x20 && c <= 0x7e) || (c >= 0xa0 && c <= 0xff) ? ch : "?";
  }
  return out;
}

/** A PDF string literal: the three characters with a meaning are escaped. */
export function pdfString(text: string): string {
  return `(${latin1Text(text).replace(/[\\()]/g, (c) => `\\${c}`)})`;
}

function rgb(hex: string): string {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
  if (!m) throw new Error(`not a colour: ${hex}`);
  return m
    .slice(1)
    .map((h) => num(parseInt(h, 16) / 255))
    .join(" ");
}

// The vertical offset from a text's centre to its baseline, per unit of size:
// half the height of a capital in Helvetica (718/1000).
const BASELINE = 0.359;
// The control points of a quarter circle drawn as a cubic curve.
const KAPPA = 0.5523;

export function pdfFromVector(fig: Figure, opts: { title: string }): Uint8Array {
  let s = PT_PER_PX;
  const over = (Math.max(fig.width, fig.height) * s) / MAX_PAGE_PT;
  if (over > 1) s /= over;
  const W = fig.width * s;
  const H = fig.height * s;
  // The page's y goes upwards, from the bottom.
  const X = (x: number) => num(x * s);
  const Y = (y: number) => num(H - y * s);
  const P = (p: Point) => `${X(p.x)} ${Y(p.y)}`;

  const ops: string[] = ["1 J 1 j"];
  const stroke = (k: Stroke) =>
    ops.push(`${rgb(k.color)} RG ${num(k.width * s)} w [${(k.dash ?? []).map((d) => num(d * s)).join(" ")}] 0 d`);

  for (const sh of fig.shapes) {
    if (sh.kind === "line") {
      stroke(sh.stroke);
      if (sh.via) {
        // A quadratic curve written as the cubic PDF draws: control points two thirds of the way to `via`.
        const c1 = { x: sh.from.x + (2 / 3) * (sh.via.x - sh.from.x), y: sh.from.y + (2 / 3) * (sh.via.y - sh.from.y) };
        const c2 = { x: sh.to.x + (2 / 3) * (sh.via.x - sh.to.x), y: sh.to.y + (2 / 3) * (sh.via.y - sh.to.y) };
        ops.push(`${P(sh.from)} m ${P(c1)} ${P(c2)} ${P(sh.to)} c S`);
      } else {
        ops.push(`${P(sh.from)} m ${P(sh.to)} l S`);
      }
    } else if (sh.kind === "box") {
      stroke(sh.stroke);
      ops.push(`${rgb(sh.fill)} rg`);
      const r = Math.min(sh.radius, sh.w / 2, sh.h / 2);
      const k = r * (1 - KAPPA);
      const [x0, y0, x1, y1] = [sh.x, sh.y, sh.x + sh.w, sh.y + sh.h];
      const pt = (x: number, y: number) => `${X(x)} ${Y(y)}`;
      ops.push(
        [
          `${pt(x0 + r, y0)} m`,
          `${pt(x1 - r, y0)} l ${pt(x1 - k, y0)} ${pt(x1, y0 + k)} ${pt(x1, y0 + r)} c`,
          `${pt(x1, y1 - r)} l ${pt(x1, y1 - k)} ${pt(x1 - k, y1)} ${pt(x1 - r, y1)} c`,
          `${pt(x0 + r, y1)} l ${pt(x0 + k, y1)} ${pt(x0, y1 - k)} ${pt(x0, y1 - r)} c`,
          `${pt(x0, y0 + r)} l ${pt(x0, y0 + k)} ${pt(x0 + k, y0)} ${pt(x0 + r, y0)} c`,
          "h B",
        ].join(" "),
      );
    } else {
      const w = textWidth(sh.text, sh.size);
      if (sh.background) {
        const pad = 2;
        ops.push(
          `${rgb(sh.background)} rg ${X(sh.at.x - w / 2 - pad)} ${Y(sh.at.y + sh.size / 2 + pad)} ${num((w + 2 * pad) * s)} ${num((sh.size + 2 * pad) * s)} re f`,
        );
      }
      ops.push(
        `BT /F1 ${num(sh.size * s)} Tf ${rgb(sh.color)} rg ${X(sh.at.x - w / 2)} ${Y(sh.at.y + sh.size * BASELINE)} Td ${pdfString(sh.text)} Tj ET`,
      );
    }
  }

  const content = ops.join("\n");
  // Latin-1 bytes, one per character: the strings are WinAnsi, not UTF-8.
  const bytes = Uint8Array.from(content, (c) => c.charCodeAt(0));
  return assemblePdf([
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${num(W)} ${num(H)}] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`,
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
    [ascii(`<< /Length ${bytes.length} >>\nstream\n`), bytes, ascii("\nendstream")],
    `<< /Title ${pdfTitle(opts.title)} /Producer ${pdfTitle("trica")} >>`,
  ]);
}
