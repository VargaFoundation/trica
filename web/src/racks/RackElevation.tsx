import { Link } from "react-router";
import { useI18n } from "../i18n";
import { formatRange, intervalsOverlap, type Face, type Slot } from "./layout";

// Geometry, in SVG user units. U 1 is the bottom row.
export const UNIT_PX = 22;
export const GUTTER = 34; // room for the U numbers
export const BODY = 280; // width of the mounting area
export const PAD = 6;
export const CHAR_PX = 6.4; // average advance of the 11 px label font

/**
 * One face of a rack, drawn from its slots. Devices are links to their object page.
 * With onPick (a caller who may propose), every free unit is a target: a click
 * opens "place a device here". It is a pointer shortcut only, kept out of the
 * tab order and of the accessibility tree: the page's "Place a device" button
 * is the keyboard path to the same form.
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
      {slots.map((s) => {
        const y = yOf(s.top);
        const h = Math.max(s.height * UNIT_PX - 2, 6);
        const bad = s.issues.some((i) => i === "overlap" || i === "outOfRange");
        const klass = ["rack-slot", s.shadow ? "rack-shadow" : "", bad ? "rack-problem" : ""].filter(Boolean).join(" ");
        const maxChars = Math.max(3, Math.floor((BODY - 12) / CHAR_PX));
        const text = s.item.name.length > maxChars ? `${s.item.name.slice(0, maxChars - 1)}…` : s.item.name;
        const range = formatRange(s.bottom, s.height);
        const body = (
          <>
            <title>{`${s.item.name} (${range})`}</title>
            <rect x={GUTTER + 1} y={y + 1} width={BODY - 2} height={h} rx={2} />
            <text x={GUTTER + 8} y={y + 1 + Math.min(h, UNIT_PX) / 2 + 4}>
              {bad ? "! " : ""}
              {text}
            </text>
          </>
        );
        // The other face of a full-depth device is a shadow: drawn, not a second link to the same page.
        return s.shadow ? (
          <g key={`${s.item.id}-${s.face}`} className={klass} aria-hidden="true">
            {body}
          </g>
        ) : (
          <Link
            key={`${s.item.id}-${s.face}`}
            to={`/objects/${encodeURIComponent(s.item.id)}`}
            className={klass}
            aria-label={`${s.item.name}, ${range}`}
            data-id={s.item.id}
          >
            {body}
          </Link>
        );
      })}
    </svg>
  );
}
