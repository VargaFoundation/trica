// One measurement drawn over a period: the average as a line, the lowest and
// highest readings of each bucket as a band behind it, and nothing where there
// was no reading. The drawing is in real pixels (the CSP forbids inline styles,
// so nothing is laid over it): its width follows the card. Pointer or arrow
// keys move a cursor whose bucket is read out under the drawing.
import { useEffect, useId, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import type { Series, SeriesPoint } from "../api/types";
import { useI18n } from "../i18n";
import { bandPath, linePath, middle, nearest, runs, scale, valueRange, type Box } from "./geometry";

const HEIGHT = 144;
const FALLBACK_WIDTH = 600;
const DAY = 86_400_000;

/** The width of the element, followed as the layout changes. */
function useWidth() {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(FALLBACK_WIDTH);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([e]) => {
      const w = Math.round(e?.contentRect.width ?? 0);
      if (w > 0) setWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

export function SeriesChart({ series, label, format }: { series: Series; label: string; format: (v: number) => string }) {
  const { t, locale } = useI18n();
  const summaryId = useId();
  const [ref, width] = useWidth();
  const pts = series.points;
  const from = Date.parse(series.from);
  const to = Date.parse(series.to);
  const step = series.step_seconds;
  const box: Box = { width, height: HEIGHT, left: 0, right: 0, top: 18, bottom: 6 };
  const s = scale(box, from, to, valueRange(pts));
  const rs = runs(pts, step);
  const [active, setActive] = useState<number | null>(null);

  const dates = new Intl.DateTimeFormat(locale, to - from > 2 * DAY ? { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" } : { hour: "2-digit", minute: "2-digit" });
  const when = (ms: number) => dates.format(new Date(ms));

  if (pts.length === 0) return <p className="series-empty">{t("series.empty")}</p>;

  const lo = Math.min(...pts.map((p) => p.min));
  const hi = Math.max(...pts.map((p) => p.max));
  const last = pts[pts.length - 1] as SeriesPoint;

  const move = (e: PointerEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (rect.width <= 0) return;
    setActive(nearest(pts, step, from + ((e.clientX - rect.left) / rect.width) * (to - from)));
  };
  const key = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = active ?? pts.length;
    const next = { ArrowLeft: Math.max(0, i - 1), ArrowRight: Math.min(pts.length - 1, i + 1), Home: 0, End: pts.length - 1 }[e.key];
    if (next !== undefined) {
      e.preventDefault();
      setActive(next);
    } else if (e.key === "Escape") setActive(null);
  };

  const p = active === null ? undefined : pts[active];
  const px = p ? s.x(middle(p, step)) : 0;
  return (
    <div className="series">
      <div ref={ref} className="series-plot" role="group" tabIndex={0} aria-label={label} aria-describedby={summaryId} onKeyDown={key} onBlur={() => setActive(null)}>
        <svg width={width} height={HEIGHT} viewBox={`0 0 ${width} ${HEIGHT}`} aria-hidden="true" onPointerMove={move} onPointerLeave={() => setActive(null)}>
          <line className="series-guide" x1={0} x2={width} y1={s.y(hi)} y2={s.y(hi)} />
          <line className="series-guide" x1={0} x2={width} y1={s.y(lo)} y2={s.y(lo)} />
          <path className="series-band" d={bandPath(rs, step, s)} />
          <path className="series-line" d={linePath(rs, step, s)} />
          <text className="series-tick" x={2} y={s.y(hi) - 4}>
            {format(hi)}
          </text>
          {lo !== hi ? (
            <text className="series-tick" x={2} y={s.y(lo) - 4}>
              {format(lo)}
            </text>
          ) : null}
          {p ? (
            <g className="series-cursor">
              <line x1={px} x2={px} y1={0} y2={HEIGHT} />
              <circle cx={px} cy={s.y(p.avg)} r={3.5} />
            </g>
          ) : null}
        </svg>
      </div>
      <div className="series-axis" aria-hidden="true">
        <span>{when(from)}</span>
        <span>{when(from + (to - from) / 2)}</span>
        <span>{when(to)}</span>
      </div>
      <p className="series-readout" aria-live="polite">
        {p ? t("series.point", { time: when(Date.parse(p.t)), avg: format(p.avg), min: format(p.min), max: format(p.max) }) : null}
      </p>
      <p className="series-summary" id={summaryId}>
        {t("series.summary", { last: format(last.avg), min: format(lo), max: format(hi) })}
      </p>
    </div>
  );
}
