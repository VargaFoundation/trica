// The rack elevation drawn on a canvas, for the PNG and PDF exports. The
// screen figure is SVG styled with the theme's variables; an export must look
// the same whichever theme the reader had, and a canvas cannot read CSS
// variables from an SVG, so the sheet has its own fixed light palette. The
// geometry is the screen figure's (same constants), so the two agree.

import { BODY, clipName, GUTTER, LABEL_X, PAD, STRIP_PX, UNIT_PX } from "../racks/RackElevation";
import { faceKind, genericFace, type PrimClass } from "../racks/faces";
import { containBox, formatRange, overallHealth, slotImage, type Health, type Slot } from "../racks/layout";
import { canvasBlob, type Raster, type RasterFormat } from "./download";

export const SHEET = {
  paper: "#ffffff",
  ink: "#1f2328",
  muted: "#57606a",
  grid: "#d0d7de",
  frameFill: "#f6f8fa",
  frame: "#6e7781",
  slotFill: "#ddf4ff",
  slotInk: "#0550ae",
  shadowFill: "#eaeef2",
  shadowInk: "#424a53",
  problemFill: "#ffebe9",
  problemInk: "#a40e26",
  labelFill: "rgba(255, 255, 255, 0.88)",
} as const;

/** The health strip, as on screen (green, amber, red, grey). */
export const HEALTH_INK: Record<Health, string> = {
  ok: "#116329",
  warning: "#7d4e00",
  critical: "#a40e26",
  unknown: "#424a53",
};

/** Generic faces: fill and stroke of each primitive class ("" for none). */
const FACE_INK: Record<PrimClass, { fill: string; stroke: string }> = {
  plate: { fill: "#f6f8fa", stroke: "#6e7781" },
  bay: { fill: "#eaeef2", stroke: "#424a53" },
  port: { fill: "#424a53", stroke: "" },
  led: { fill: "#116329", stroke: "" },
  vent: { fill: "", stroke: "#d0d7de" },
  accent: { fill: "#ddf4ff", stroke: "#0550ae" },
};

export interface SheetFace {
  caption: string;
  slots: readonly Slot[];
}

export interface RackSheet {
  title: string;
  uHeight: number;
  faces: readonly SheetFace[];
  /** "Exported from trica on 2026-10-14", already translated. */
  footer: string;
  /** The face images already decoded, by API path (see racks/faceImages.ts); a slot whose image is missing gets its generic face. */
  images?: ReadonlyMap<string, CanvasImageSource>;
}

const MARGIN = 16;
const TITLE_H = 30;
const CAPTION_H = 22;
const FOOTER_H = 26;
const FACE_GAP = 24;

const faceWidth = GUTTER + BODY + 2 * PAD;

export function sheetSize(s: RackSheet): { width: number; height: number } {
  const n = s.faces.length;
  return {
    width: 2 * MARGIN + n * faceWidth + (n - 1) * FACE_GAP,
    height: 2 * MARGIN + TITLE_H + CAPTION_H + s.uHeight * UNIT_PX + 2 * PAD + FOOTER_H,
  };
}

/** The part of CanvasRenderingContext2D the drawing uses, so that a test can record the calls. */
export type Ctx = Pick<
  CanvasRenderingContext2D,
  | "fillRect"
  | "strokeRect"
  | "beginPath"
  | "moveTo"
  | "lineTo"
  | "stroke"
  | "fillText"
  | "setLineDash"
  | "save"
  | "restore"
  | "translate"
  | "drawImage"
  | "arc"
  | "fill"
> & {
  fillStyle: string | CanvasGradient | CanvasPattern;
  strokeStyle: string | CanvasGradient | CanvasPattern;
  lineWidth: number;
  globalAlpha: number;
  font: string;
  textAlign: CanvasTextAlign;
};

export function drawRackSheet(ctx: Ctx, s: RackSheet): void {
  const { width, height } = sheetSize(s);
  ctx.fillStyle = SHEET.paper;
  ctx.fillRect(0, 0, width, height);
  ctx.fillStyle = SHEET.ink;
  ctx.font = "600 16px system-ui, sans-serif";
  ctx.textAlign = "left";
  ctx.fillText(s.title, MARGIN, MARGIN + 16);

  s.faces.forEach((face, i) => {
    ctx.save();
    ctx.translate(MARGIN + i * (faceWidth + FACE_GAP), MARGIN + TITLE_H);
    ctx.fillStyle = SHEET.ink;
    ctx.font = "600 13px system-ui, sans-serif";
    ctx.textAlign = "left";
    ctx.fillText(face.caption, GUTTER, 14);
    ctx.translate(0, CAPTION_H);
    drawFace(ctx, s.uHeight, face.slots, s.images);
    ctx.restore();
  });

  ctx.fillStyle = SHEET.muted;
  ctx.font = "11px system-ui, sans-serif";
  ctx.textAlign = "left";
  ctx.fillText(s.footer, MARGIN, height - MARGIN);
}

function drawFace(ctx: Ctx, uHeight: number, slots: readonly Slot[], images?: ReadonlyMap<string, CanvasImageSource>): void {
  const yOf = (topU: number) => Math.min(Math.max(PAD + (uHeight - topU) * UNIT_PX, PAD), PAD + uHeight * UNIT_PX);
  ctx.fillStyle = SHEET.frameFill;
  ctx.fillRect(GUTTER, PAD, BODY, uHeight * UNIT_PX);
  ctx.strokeStyle = SHEET.frame;
  ctx.lineWidth = 1.5;
  ctx.strokeRect(GUTTER, PAD, BODY, uHeight * UNIT_PX);
  ctx.setLineDash([]);
  ctx.lineWidth = 1;
  ctx.font = "9px system-ui, sans-serif";
  for (let u = 1; u <= uHeight; u++) {
    const y = yOf(u);
    ctx.strokeStyle = SHEET.grid;
    ctx.beginPath();
    ctx.moveTo(GUTTER, y + UNIT_PX);
    ctx.lineTo(GUTTER + BODY, y + UNIT_PX);
    ctx.stroke();
    ctx.fillStyle = SHEET.muted;
    ctx.textAlign = "right";
    ctx.fillText(String(u), GUTTER - 6, y + UNIT_PX / 2 + 3);
  }
  ctx.textAlign = "left";
  for (const slot of slots) {
    const y = yOf(slot.top);
    const h = Math.max(slot.height * UNIT_PX - 2, 6);
    const bad = slot.issues.some((i) => i === "overlap" || i === "outOfRange");
    // Same three looks as the screen: a device, the other face of a full-depth one (dashed), a problem (heavier, red).
    const look = bad
      ? { fill: SHEET.problemFill, ink: SHEET.problemInk, width: 2, dash: [] as number[] }
      : slot.shadow
        ? { fill: SHEET.shadowFill, ink: SHEET.shadowInk, width: 1, dash: [4, 3] }
        : { fill: SHEET.slotFill, ink: SHEET.slotInk, width: 1, dash: [] as number[] };
    const x = GUTTER + 1;
    const w = BODY - 2;
    ctx.fillStyle = look.fill;
    ctx.fillRect(x, y + 1, w, h);
    const url = slotImage(slot);
    const image = url ? images?.get(url) : undefined;
    const faced = !!image || !slot.shadow;
    if (image) {
      // The other face of a full-depth device is faded, as on screen.
      ctx.globalAlpha = slot.shadow ? 0.55 : 1;
      // A desktop device keeps its proportions on its shelf (ADR 0068); a rack unit's face fills it.
      const box = slot.item.desktop ? containBox(w, h, (image as { width?: number }).width ?? 0, (image as { height?: number }).height ?? 0) : { x: 0, y: 0, width: w, height: h };
      ctx.drawImage(image, x + box.x, y + 1 + box.y, box.width, box.height);
      ctx.globalAlpha = 1;
    } else if (!slot.shadow) {
      drawGenericFace(ctx, faceKind(slot.item.role), x, y + 1, w, h);
    }
    const health = overallHealth(slot.item);
    if (!slot.shadow) {
      ctx.fillStyle = HEALTH_INK[health];
      ctx.fillRect(x, y + 1, STRIP_PX, h);
    }
    const alarm = !bad && !slot.shadow && (health === "warning" || health === "critical");
    ctx.strokeStyle = alarm ? HEALTH_INK[health] : look.ink;
    ctx.lineWidth = alarm ? 2 : look.width;
    ctx.setLineDash(look.dash);
    ctx.strokeRect(x, y + 1, w, h);
    ctx.setLineDash([]);
    const label = `${bad ? "! " : ""}${clipName(slot.item.name)}  ${formatRange(slot.bottom, slot.height)}`;
    const labelY = y + 1 + Math.min(h, UNIT_PX) / 2;
    if (faced) {
      ctx.fillStyle = SHEET.labelFill;
      ctx.fillRect(LABEL_X - 3, labelY - 7, label.length * 6.2 + 6, 13);
    }
    ctx.fillStyle = faced && !bad ? SHEET.ink : look.ink;
    ctx.font = `${slot.shadow ? 400 : 600} 11px system-ui, sans-serif`;
    ctx.fillText(label, LABEL_X, labelY + 4);
  }
}

function drawGenericFace(ctx: Ctx, kind: ReturnType<typeof faceKind>, x: number, y: number, w: number, h: number): void {
  ctx.save();
  ctx.translate(x, y);
  ctx.lineWidth = 0.75;
  for (const p of genericFace(kind, w, h)) {
    const ink = FACE_INK[p.c];
    if (p.k === "rect") {
      if (ink.fill) {
        ctx.fillStyle = ink.fill;
        ctx.fillRect(p.x, p.y, p.w, p.h);
      }
      if (ink.stroke) {
        ctx.strokeStyle = ink.stroke;
        ctx.strokeRect(p.x, p.y, p.w, p.h);
      }
    } else if (p.k === "circle") {
      ctx.fillStyle = ink.fill || ink.stroke;
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.r, 0, 2 * Math.PI);
      ctx.fill();
    } else {
      ctx.strokeStyle = ink.stroke || ink.fill;
      ctx.beginPath();
      ctx.moveTo(p.x1, p.y1);
      ctx.lineTo(p.x2, p.y2);
      ctx.stroke();
    }
  }
  ctx.restore();
}

/** The sheet on a canvas of the given pixel ratio. Browser only. */
export function renderRackSheet(s: RackSheet, ratio: number): HTMLCanvasElement {
  const { width, height } = sheetSize(s);
  const canvas = document.createElement("canvas");
  canvas.width = Math.ceil(width * ratio);
  canvas.height = Math.ceil(height * ratio);
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("canvas is not available");
  ctx.scale(ratio, ratio);
  drawRackSheet(ctx, s);
  return canvas;
}

/** Pixels per CSS pixel of the exported sheet: sharp when printed or zoomed, small enough to stay light. */
export const SHEET_RATIO = 2;

export async function rackRaster(s: RackSheet, format: RasterFormat): Promise<Raster> {
  const canvas = renderRackSheet(s, SHEET_RATIO);
  const blob = await canvasBlob(canvas, format);
  return { blob, width: canvas.width, height: canvas.height, ratio: SHEET_RATIO };
}
