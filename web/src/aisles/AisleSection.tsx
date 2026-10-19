// The rack page's "Aisles and airflow" card (ADR 0049): which aisle the
// rack's front faces, the airflow a device should follow through it, the
// devices that take their air in from the hot aisle, and, for a person who
// may propose, the one choice that says it: "the front of this rack faces the
// cold aisle | the hot aisle | not set". The choice is a declared
// rack.front_aisle, previewed then submitted like every declaration of the UI
// (ADR 0039).
import { useQuery } from "@tanstack/react-query";
import { useId, useState } from "react";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Card } from "../components/ui";
import { BatchReview } from "../edit/BatchReview";
import { useCanDeclare } from "../edit/EditPanel";
import { callerSource, declareFrontAisle } from "../edit/batch";
import { editRules } from "../edit/rules";
import { useMe, useSchema } from "../hooks";
import { useI18n } from "../i18n";
import type { Mounted } from "../racks/layout";
import { AirflowSummary } from "./Airflow";
import { AISLES, aisleOf, type Aisle } from "./aisles";
import "./aisles.css";

type Choice = Aisle | "unset";

/** Who said the rack's front_aisle: the source that wins, and whether this caller said it too. */
function useAisleClaims(rack: ObjectView, said: boolean) {
  const prov = useQuery({
    queryKey: ["provenance", rack.id],
    queryFn: ({ signal }) => api.provenance(rack.id, signal),
    enabled: said,
    staleTime: 30_000,
  });
  const mine = callerSource(useMe().data);
  const entry = prov.data?.attributes.find((a) => a.key === "front_aisle");
  const sources = entry ? [entry.source, ...(entry.other_claims ?? []).map((c) => c.source)] : [];
  return {
    saidBy: entry?.source,
    own: !!mine && sources.includes(mine),
    other: sources.find((s) => s !== mine),
  };
}

export function AisleSection({ rack, items }: { rack: ObjectView; items?: readonly Mounted[] }) {
  const { t } = useI18n();
  const current = aisleOf(rack.attributes?.front_aisle);
  const claims = useAisleClaims(rack, current !== undefined);
  const can = useCanDeclare();
  const schema = useSchema();
  const declarable = can && !!editRules(schema.data).attribute("rack", "front_aisle");
  const [open, setOpen] = useState(false);
  return (
    <Card title={t("aisle.title")} titleId="aisles" className="aisle-card">
      <p data-front-aisle={current ?? "unset"}>
        {current ? (
          <>
            <span className={`aisle-chip aisle-${current}`}>{t(`aisle.${current}`)}</span>{" "}
          </>
        ) : null}
        {t(current ? `aisle.current.${current}` : "aisle.current.none")}
        {current && claims.saidBy ? <span className="muted"> {t("aisle.saidBy", { source: claims.saidBy })}</span> : null}
      </p>
      {current ? <AirflowDiagram rack={rack.display} frontAisle={current} /> : null}
      {items ? <AirflowSummary items={items} frontAisle={current} /> : null}
      {declarable && !open ? (
        <p className="actions">
          <button type="button" onClick={() => setOpen(true)}>
            {t(current ? "aisle.change" : "aisle.declare")}
          </button>
        </p>
      ) : null}
      {declarable && open ? <AisleForm rack={rack} current={current} own={claims.own} other={claims.other} onClose={() => setOpen(false)} /> : null}
    </Card>
  );
}

function AisleForm({ rack, current, own, other, onClose }: { rack: ObjectView; current?: Aisle; own: boolean; other?: string; onClose: () => void }) {
  const { t } = useI18n();
  const name = useId();
  const [chosen, setChosen] = useState<Choice>(current ?? "unset");
  const unchanged = chosen === (current ?? "unset");
  // "Not set" withdraws this caller's claim: with none, there is nothing to send.
  const batch = unchanged ? null : chosen === "unset" ? (own ? declareFrontAisle(rack.id, null) : null) : declareFrontAisle(rack.id, chosen);
  const warnings = chosen === "unset" && other ? [t("aisle.withdrawOther", { source: other })] : [];
  return (
    <div className="aisle-form">
      <fieldset>
        <legend>{t("aisle.frontFaces")}</legend>
        {[...AISLES, "unset" as const].map((c) => (
          <label key={c} className={`aisle-option aisle-option-${c}`}>
            <input type="radio" name={name} value={c} checked={chosen === c} onChange={() => setChosen(c)} /> {t(`aisle.option.${c}`)}
          </label>
        ))}
      </fieldset>
      <p className="hint">{t("aisle.hint")}</p>
      {chosen === "unset" && !unchanged && !own ? <p role="note" className="notice">{t("aisle.nothingOwn")}</p> : null}
      <BatchReview batch={batch} warnings={warnings} onCancel={onClose} />
    </div>
  );
}

/**
 * The rack seen from the side, the air going through it: from the cold aisle
 * on one face to the hot aisle on the other. Its words are in its accessible
 * name; the colours only repeat them.
 */
export function AirflowDiagram({ rack, frontAisle }: { rack: string; frontAisle: Aisle }) {
  const { t } = useI18n();
  const W = 420;
  const H = 96;
  const zone = 120;
  const rackX = zone + 30;
  const rackW = W - 2 * rackX;
  // The front is drawn on the left: air goes left to right when the front is on the cold aisle.
  const ltr = frontAisle === "cold";
  const rearAisle: Aisle = frontAisle === "cold" ? "hot" : "cold";
  const front = t("face.front");
  const rear = t("face.rear");
  const label = t("aisle.diagramLabel", { rack, cold: ltr ? front : rear, hot: ltr ? rear : front });
  const Front = t("rack.front");
  const Rear = t("rack.rear");
  const arrows = [30, 70].map((y) => {
    const x1 = ltr ? 18 : W - 18;
    const x2 = ltr ? W - 18 : 18;
    const head = ltr ? -1 : 1;
    return (
      <g key={y} className="airflow-arrow">
        <line x1={x1} y1={y} x2={x2 + head * 6} y2={y} />
        <polygon points={`${x2},${y} ${x2 + head * 9},${y - 5} ${x2 + head * 9},${y + 5}`} />
      </g>
    );
  });
  return (
    <svg className="airflow-diagram" viewBox={`0 0 ${W} ${H}`} width={W} height={H} role="img" aria-label={label} data-front-aisle={frontAisle}>
      <title>{label}</title>
      <rect className={`aisle-zone aisle-zone-${frontAisle}`} x={0} y={0} width={zone} height={H} rx={6} />
      <rect className={`aisle-zone aisle-zone-${rearAisle}`} x={W - zone} y={0} width={zone} height={H} rx={6} />
      <rect className="airflow-rack" x={rackX} y={8} width={rackW} height={H - 16} rx={3} />
      <text className="airflow-rack-name" x={W / 2} y={H / 2 + 4} textAnchor="middle">
        {rack.length > 12 ? `${rack.slice(0, 11)}…` : rack}
      </text>
      {arrows}
      <text className={`aisle-zone-label aisle-zone-label-${frontAisle}`} x={8} y={14}>
        {`${Front} · ${t(`aisle.${frontAisle}`)}`}
      </text>
      <text className={`aisle-zone-label aisle-zone-label-${rearAisle}`} x={W - 8} y={14} textAnchor="end">
        {`${Rear} · ${t(`aisle.${rearAisle}`)}`}
      </text>
      <text className="aisle-zone-note" x={ltr ? 8 : W - 8} y={H - 6} textAnchor={ltr ? "start" : "end"}>
        {t("aisle.airIn")}
      </text>
      <text className="aisle-zone-note" x={ltr ? W - 8 : 8} y={H - 6} textAnchor={ltr ? "end" : "start"}>
        {t("aisle.airOut")}
      </text>
    </svg>
  );
}
