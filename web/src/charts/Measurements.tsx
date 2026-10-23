// The "Trends" of an object: one curve per measurement its type declares as a
// series and the object holds as a number now or read in the last 30 days (a
// withdrawn measurement keeps its curve, ending in a gap, #167), over the last
// day, week or month. The range lives in the address so that a curve can be
// shared as a link.
import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { useDurationWords } from "../components/Quantity";
import { Card } from "../components/ui";
import type { SchemaLookup } from "../hooks";
import { useI18n, type MessageKey } from "../i18n";
import { formatQuantity } from "../inventory/format";
import { SeriesChart } from "./SeriesChart";

const HOUR = 3_600_000;
export const RANGES = { "24h": 24 * HOUR, "7d": 7 * 24 * HOUR, "30d": 30 * 24 * HOUR } as const;
export type Range = keyof typeof RANGES;
const POINTS = 120;

export const readRange = (v: string | null): Range => (v !== null && v in RANGES ? (v as Range) : "24h");

/** How far back a withdrawn measurement still gets its curve: the longest range offered. */
export const HISTORY_SPAN = RANGES["30d"];

/** The measurements the type of obj declares as a series, in schema order. */
export function declaredSeries(obj: ObjectView, lookup: SchemaLookup): string[] {
  return (lookup.typeDoc(obj.type)?.attributes ?? []).filter((a) => a.series).map((a) => a.name);
}

/** The measurements of obj worth a curve: declared as a series, holding a number now or in withHistory. */
export function seriesKeys(obj: ObjectView, lookup: SchemaLookup, withHistory: readonly string[] = []): string[] {
  const had = new Set(withHistory);
  return declaredSeries(obj, lookup).filter((n) => typeof obj.attributes[n] === "number" || had.has(n));
}

export function Measurements({ obj, lookup }: { obj: ObjectView; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const range = readRange(params.get("range"));
  const declared = declaredSeries(obj, lookup);
  const missing = declared.filter((k) => typeof obj.attributes[k] !== "number");
  // Asked only when a declared series has no current value, and always over the
  // longest range, so that the curves do not depend on the one selected.
  const history = useQuery({
    queryKey: ["series-keys", obj.id],
    enabled: missing.length > 0,
    staleTime: 60_000,
    queryFn: ({ signal }) => {
      const to = Math.floor(Date.now() / 60_000) * 60_000;
      return api.seriesKeys(obj.id, { from: new Date(to - HISTORY_SPAN).toISOString(), to: new Date(to).toISOString() }, signal);
    },
  });
  const keys = seriesKeys(obj, lookup, history.data?.keys);
  if (keys.length === 0) return null;

  const choose = (r: Range) =>
    setParams(
      (p) => {
        const next = new URLSearchParams(p);
        if (r === "24h") next.delete("range");
        else next.set("range", r);
        return next;
      },
      { replace: true },
    );

  return (
    <Card
      title={t("series.title")}
      titleId="series-title"
      className="series-card"
      actions={
        <div className="series-range" role="group" aria-label={t("series.range")}>
          {(Object.keys(RANGES) as Range[]).map((r) => (
            <button key={r} type="button" className="ghost" aria-pressed={r === range} onClick={() => choose(r)}>
              {t(`series.range.${r}` as MessageKey)}
            </button>
          ))}
        </div>
      }
    >
      <p className="hint">{t("series.hint")}</p>
      <div className="series-grid">
        {keys.map((k) => (
          <MeasurementCurve key={k} obj={obj} name={k} range={range} lookup={lookup} current={typeof obj.attributes[k] === "number"} />
        ))}
      </div>
    </Card>
  );
}

function MeasurementCurve({ obj, name, range, lookup, current }: { obj: ObjectView; name: string; range: Range; lookup: SchemaLookup; current: boolean }) {
  const { t, locale } = useI18n();
  const w = useDurationWords();
  const label = lookup.attributeLabel(obj.type, name);
  const q = useQuery({
    queryKey: ["series", obj.id, name, range],
    queryFn: ({ signal }) => {
      // To the minute, so that two curves asked together cover the same range.
      const to = Math.floor(Date.now() / 60_000) * 60_000;
      const from = to - RANGES[range];
      return api.series(obj.id, { key: name, from: new Date(from).toISOString(), to: new Date(to).toISOString(), points: POINTS }, signal);
    },
    staleTime: 60_000,
  });
  const unit = q.data?.unit ?? lookup.attribute(obj.type, name)?.unit;
  const plain = new Intl.NumberFormat(locale, { maximumFractionDigits: 2 });
  const format = (v: number) => formatQuantity(v, unit, locale, w) ?? (unit ? `${plain.format(v)} ${unit}` : plain.format(v));
  const headId = `series-${name}`;
  return (
    <Card as="section" level={3} title={label} titleId={headId} className="series-item">
      {current ? null : <p className="hint">{t("series.noCurrent")}</p>}
      <Async query={q}>
        {(s) => (
          <>
            <SeriesChart series={s} label={t("series.chart", { name: label })} format={format} />
            {s.truncated ? <p className="hint">{t("series.truncated")}</p> : null}
          </>
        )}
      </Async>
    </Card>
  );
}
