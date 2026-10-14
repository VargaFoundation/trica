// The rack elevation drawn on a canvas, for the PNG and PDF exports. The
// screen figure is SVG styled with the theme's variables; an export must look
// the same whichever theme the reader had, and a canvas cannot read CSS
// variables from an SVG, so the sheet has its own fixed light palette. The
// geometry is the screen figure's (same constants), so the two agree.

import { BODY, CHAR_PX, GUTTER, PAD, UNIT_PX } from "../racks/RackElevation";
import { formatRange, type Slot } from "../racks/layout";
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
} as const;

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
> & {
  fillStyle: string | CanvasGradient | CanvasPattern;
  strokeStyle: string | CanvasGradient | CanvasPattern;
  lineWidth: number;
  font: string;
  textAlign: CanvasTextAlign;
};

const clip = (name: string) => {
  const max = Math.max(3, Math.floor((BODY - 12) / CHAR_PX));
  return name.length > max ? `${name.slice(0, max - 1)}…` : name;
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
    drawFace(ctx, s.uHeight, face.slots);
    ctx.restore();
  });

  ctx.fillStyle = SHEET.muted;
  ctx.font = "11px system-ui, sans-serif";
  ctx.textAlign = "left";
  ctx.fillText(s.footer, MARGIN, height - MARGIN);
}

function drawFace(ctx: Ctx, uHeight: number, slots: readonly Slot[]): void {
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
    ctx.fillStyle = look.fill;
    ctx.fillRect(GUTTER + 1, y + 1, BODY - 2, h);
    ctx.strokeStyle = look.ink;
    ctx.lineWidth = look.width;
    ctx.setLineDash(look.dash);
    ctx.strokeRect(GUTTER + 1, y + 1, BODY - 2, h);
    ctx.setLineDash([]);
    ctx.fillStyle = look.ink;
    ctx.font = `${slot.shadow ? 400 : 600} 11px system-ui, sans-serif`;
    ctx.fillText(`${bad ? "! " : ""}${clip(slot.item.name)}  ${formatRange(slot.bottom, slot.height)}`, GUTTER + 8, y + 1 + Math.min(h, UNIT_PX) / 2 + 4);
  }
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
