import { useEffect, useRef } from "react";
import { Link } from "react-router";
import { useI18n, type MessageKey } from "../i18n";
import { faceKind, genericFace, type Prim } from "./faces";
import { useFaceImage } from "./faceImages";
import { formatRange, intervalsOverlap, overallHealth, slotImage, type Face, type Health, type Slot } from "./layout";
import "./racks.css";

// Geometry, in SVG user units. U 1 is the bottom row.
export const UNIT_PX = 22;
export const GUTTER = 34; // room for the U numbers
export const BODY = 280; // width of the mounting area
export const PAD = 6;
export const CHAR_PX = 6.4; // average advance of the 11 px label font
/** Width of the health strip on the left edge of a device. */
export const STRIP_PX = 5;
/** Where a device's label starts, after the health strip. */
export const LABEL_X = GUTTER + STRIP_PX + 5;
/** Canvas pixels per SVG unit of a face image: sharp when the page is zoomed or printed. */
const IMAGE_SCALE = 3;

/** The words of a health, from the shared catalogue (the same as HealthPill's). */
export const HEALTH_TEXT: Record<Health, MessageKey> = {
  ok: "healthState.ok",
  warning: "healthState.warning",
  critical: "healthState.critical",
  unknown: "healthState.unknown",
};

/** The label of a device, clipped to the width of the rack. */
export function clipName(name: string): string {
  const max = Math.max(3, Math.floor((BODY - (LABEL_X - GUTTER) - 6) / CHAR_PX));
  return name.length > max ? `${name.slice(0, max - 1)}…` : name;
}

/**
 * One face of a rack, drawn from its slots. Devices are links to their object page.
 * With onPick (a caller who may propose), every free unit is a target: a click
 * opens "place a device here". It is a pointer shortcut only, kept out of the
 * tab order and of the accessibility tree: the page's "Place a device" button
 * is the keyboard path to the same form.
 *
 * A device shows its model's image of that face when the device-type library
 * has one, else a generic face drawn for its role; a strip on its left edge
 * says its health (the worst of its own and its parts'), in words in the
 * tooltip and the accessible name.
 */
export function RackElevation({
  rack,
  uHeight,
  face,
  slots,
  onPick,
}: {
  rack: string;
  uHeight: number;
  face: Face;
  slots: readonly Slot[];
  onPick?: (u: number, face: Face) => void;
}) {
  const { t } = useI18n();
  const width = GUTTER + BODY + 2 * PAD;
  const height = uHeight * UNIT_PX + 2 * PAD;
  const faceName = t(face === "front" ? "rack.front" : "rack.rear");
  // The U number of the row whose top edge is y: U `uHeight` is drawn first (top).
  const yOf = (topU: number) => Math.min(Math.max(PAD + (uHeight - topU) * UNIT_PX, PAD), PAD + uHeight * UNIT_PX);

  return (
    <svg
      className="rack-svg"
      viewBox={`0 0 ${width} ${height}`}
      width={width}
      height={height}
      role="group"
      aria-label={t("rack.figureLabel", { face: faceName, rack, units: uHeight })}
      data-face={face}
    >
      <rect className="rack-frame" x={GUTTER} y={PAD} width={BODY} height={uHeight * UNIT_PX} />
      {Array.from({ length: uHeight }, (_, i) => {
        const u = i + 1;
        const y = yOf(u);
        return (
          <g key={u}>
            <line className="rack-grid" x1={GUTTER} x2={GUTTER + BODY} y1={y + UNIT_PX} y2={y + UNIT_PX} />
            <text className="rack-u" x={GUTTER - 6} y={y + UNIT_PX / 2 + 4} textAnchor="end">
              {u}
            </text>
          </g>
        );
      })}
      {onPick
        ? Array.from({ length: uHeight }, (_, i) => i + 1)
            .filter((u) => !slots.some((s) => intervalsOverlap(u, 1, s.bottom, s.height)))
            .map((u) => (
              <rect
                key={`free-${u}`}
                className="rack-free"
                data-u={u}
                aria-hidden="true"
                x={GUTTER + 1}
                y={yOf(u) + 1}
                width={BODY - 2}
                height={UNIT_PX - 2}
                onClick={() => onPick(u, face)}
              >
                <title>{t("edit.place.here", { u, face: faceName })}</title>
              </rect>
            ))
        : null}
      {slots.map((s) => (
        <SlotView key={`${s.item.id}-${s.face}`} slot={s} y={yOf(s.top)} />
      ))}
    </svg>
  );
}

function SlotView({ slot: s, y }: { slot: Slot; y: number }) {
  const { t } = useI18n();
  const h = Math.max(s.height * UNIT_PX - 2, 6);
  const bad = s.issues.some((i) => i === "overlap" || i === "outOfRange");
  const health = overallHealth(s.item);
  const imageUrl = slotImage(s);
  const bitmap = useFaceImage(imageUrl);
  // The other face of a full-depth device is drawn from its image when there is one; else it
  // stays the plain dashed block: nothing says what the back of a generic device looks like.
  const drawn = bitmap ? "image" : s.shadow ? "none" : "generic";
  const klass = [
    "rack-slot",
    s.shadow ? "rack-shadow" : "",
    bad ? "rack-problem" : "",
    drawn !== "none" ? "rack-has-face" : "",
    `rack-health-${health}`,
  ]
    .filter(Boolean)
    .join(" ");
  const text = clipName(s.item.name);
  const range = formatRange(s.bottom, s.height);
  const healthText = `${t("health.label")}: ${t(HEALTH_TEXT[health])}`;
  const tooltip = [`${s.item.name} (${range})`, s.item.typeName, healthText].filter(Boolean).join(" · ");
  const x = GUTTER + 1;
  const w = BODY - 2;
  const labelY = y + 1 + Math.min(h, UNIT_PX) / 2;
  const body = (
    <>
      <title>{tooltip}</title>
      <rect x={x} y={y + 1} width={w} height={h} rx={2} />
      {drawn === "image" && bitmap ? <FaceCanvas bitmap={bitmap} x={x} y={y + 1} width={w} height={h} /> : null}
      {drawn === "generic" ? <GenericFace role={s.item.role} x={x} y={y + 1} width={w} height={h} /> : null}
      {s.shadow ? null : <rect className="rack-health-strip" x={x} y={y + 1} width={STRIP_PX} height={h} rx={1} />}
      {drawn !== "none" ? (
        <rect className="rack-label-bg" x={LABEL_X - 3} y={labelY - 7} width={((bad ? 2 : 0) + text.length) * CHAR_PX + 6} height={13} rx={2} />
      ) : null}
      <text x={LABEL_X} y={labelY + 4}>
        {bad ? "! " : ""}
        {text}
      </text>
      <rect className="rack-outline" x={x} y={y + 1} width={w} height={h} rx={2} />
    </>
  );
  // The other face of a full-depth device is a shadow: drawn, not a second link to the same page.
  return s.shadow ? (
    <g className={klass} aria-hidden="true">
      {body}
    </g>
  ) : (
    <Link
      to={`/objects/${encodeURIComponent(s.item.id)}`}
      className={klass}
      aria-label={`${s.item.name}, ${range}${health === "unknown" ? "" : `, ${healthText}`}`}
      data-id={s.item.id}
      data-health={health}
      data-face-drawn={drawn}
    >
      {body}
    </Link>
  );
}

/** A face image, painted on a canvas: the CSP allows no blob: or data: image (see faceImages.ts). */
function FaceCanvas({ bitmap, x, y, width, height }: { bitmap: ImageBitmap; x: number; y: number; width: number; height: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const cw = Math.round(width * IMAGE_SCALE);
  const ch = Math.round(height * IMAGE_SCALE);
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, cw, ch);
    ctx.drawImage(bitmap, 0, 0, cw, ch);
  }, [bitmap, cw, ch]);
  return (
    <foreignObject x={x} y={y} width={width} height={height} className="rack-face-image">
      <canvas ref={ref} width={cw} height={ch} className="rack-face-canvas" aria-hidden="true" />
    </foreignObject>
  );
}

function GenericFace({ role, x, y, width, height }: { role?: string; x: number; y: number; width: number; height: number }) {
  const kind = faceKind(role);
  return (
    <g className={`rack-face rack-face-${kind}`} transform={`translate(${x} ${y})`} data-kind={kind}>
      {genericFace(kind, width, height).map((p, i) => (
        <PrimView key={i} p={p} />
      ))}
    </g>
  );
}

function PrimView({ p }: { p: Prim }) {
  const cls = `rf-${p.c}`;
  switch (p.k) {
    case "rect":
      return <rect className={cls} x={p.x} y={p.y} width={p.w} height={p.h} rx={p.r} />;
    case "circle":
      return <circle className={cls} cx={p.x} cy={p.y} r={p.r} />;
    case "line":
      return <line className={cls} x1={p.x1} y1={p.y1} x2={p.x2} y2={p.y2} />;
  }
}
