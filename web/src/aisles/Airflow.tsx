// The airflow check in words (ADR 0049): a pill for its status, the reason as
// a sentence, the warnings, the rack's summary and the device page's card.
// The server computes the check; nothing here decides whether a device is
// turned the right way.
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { AirflowCheck, AirflowWarning, ObjectView } from "../api/types";
import { ErrorBox } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { Alert, Card, Pill } from "../components/ui";
import { useI18n, type MessageKey } from "../i18n";
import { en } from "../i18n/en";
import type { Mounted } from "../racks/layout";
import { aisleOf, faceAisle, statusOf, summarize, type AirflowStatus } from "./aisles";
import "./aisles.css";

const TONE: Record<AirflowStatus, "ok" | "warn" | "neutral"> = { ok: "ok", mismatch: "warn", unknown: "neutral" };

/** A message key built from a value the server sends, or undefined when the catalogue has no such message. */
const known = (key: string): MessageKey | undefined => (Object.hasOwn(en, key) ? (key as MessageKey) : undefined);

/** The words of an airflow value ("front to rear"), the raw value when unknown here. */
export function useAirflowValue() {
  const { t } = useI18n();
  return (v: string | undefined): string => {
    if (!v) return t("airflow.card.none");
    const k = known(`airflow.value.${v}`);
    return k ? t(k) : v;
  };
}

/** The reason of a check, as a sentence. */
export function useAirflowReason() {
  const { t } = useI18n();
  return (c: AirflowCheck | undefined): string => {
    const k = c ? known(`airflow.reason.${c.reason}`) : undefined;
    return k ? t(k) : t("airflow.reason.airflow_unknown");
  };
}

/** The status of a device's airflow, in a word: correct, reversed, not checked. */
export function AirflowPill({ check }: { check: AirflowCheck | undefined }) {
  const { t } = useI18n();
  const reason = useAirflowReason();
  const status = statusOf(check);
  return (
    <span title={reason(check)}>
      <Pill tone={TONE[status]} value={status} className="airflow-pill">
        <span className="sr-only">{t("airflow.label")}: </span>
        {t(`airflow.status.${status}`)}
      </Pill>
    </span>
  );
}

/** The warnings of a check: a contradiction with the model, parts turned another way. */
export function AirflowWarnings({ warnings }: { warnings: readonly AirflowWarning[] | undefined }) {
  const { t } = useI18n();
  const value = useAirflowValue();
  if (!warnings || warnings.length === 0) return null;
  return (
    <ul className="airflow-warnings">
      {warnings.map((w, i) => (
        <li key={`${w.code}-${i}`} data-warning={w.code}>
          {w.code === "airflow_contradicts_model" ? (
            <>
              {t("airflow.warn.contradiction", {
                airflow: value(w.airflow),
                from: t(known(`airflow.from.${w.from ?? ""}`) ?? "airflow.from.device"),
                model: value(w.model),
              })}
              {w.model_declared ? ` ${t("airflow.warn.contradictionDeclared")}` : ""}
            </>
          ) : w.code === "parts_disagree" ? (
            <>
              {t("airflow.warn.parts", {
                parts: (w.parts ?? []).map((p) => `${p.part.display} (${value(p.airflow)})`).join(", "),
              })}
            </>
          ) : (
            w.code
          )}
        </li>
      ))}
    </ul>
  );
}

/**
 * What the rack page says above its elevations: how many devices take their
 * air in from the hot aisle, which ones and why; how many are fine and how
 * many cannot be checked.
 */
export function AirflowSummary({ items, frontAisle }: { items: readonly Mounted[]; frontAisle: unknown }) {
  const { t, tp } = useI18n();
  const reason = useAirflowReason();
  if (items.length === 0) return null;
  if (!aisleOf(frontAisle)) {
    return (
      <p role="note" className="notice" data-airflow-summary="no-aisle">
        {t("airflow.summary.noAisle")}
      </p>
    );
  }
  const s = summarize(items.map((m) => m.airflow));
  const bad = items.filter((m) => statusOf(m.airflow) === "mismatch");
  return (
    <div className="airflow-summary" data-airflow-summary={s.mismatch > 0 ? "mismatch" : "ok"}>
      {s.mismatch > 0 ? (
        <Alert tone="warn" role="note" title={tp("airflow.summary.mismatch", s.mismatch)}>
          <ul>
            {bad.map((m) => (
              <li key={m.id}>
                <ObjectLink id={m.id} display={m.name} /> — {reason(m.airflow)}
                <AirflowWarnings warnings={m.airflow?.warnings} />
              </li>
            ))}
          </ul>
        </Alert>
      ) : null}
      {s.ok > 0 ? <p className="airflow-ok">{tp("airflow.summary.ok", s.ok)}</p> : null}
      {s.unknown > 0 ? <p className="muted">{tp("airflow.summary.unknown", s.unknown)}</p> : null}
    </div>
  );
}

export function useObjectAirflow(obj: ObjectView) {
  return useQuery({
    queryKey: ["airflow", obj.id],
    queryFn: ({ signal }) => api.objectAirflow(obj.id, signal),
    enabled: obj.type === "device",
    staleTime: 30_000,
  });
}

/**
 * The device page's "Airflow" card: where the device is mounted, which aisle
 * that face is on, what its model, the device and its fans and power supplies
 * say, and the check. Shown for a device that is mounted or says an airflow.
 */
export function AirflowSection({ obj }: { obj: ObjectView }) {
  const { t } = useI18n();
  const q = useObjectAirflow(obj);
  const value = useAirflowValue();
  const reason = useAirflowReason();
  if (obj.type !== "device") return null;
  // A server older than the check has no such endpoint: nothing to say.
  if (q.isError && q.error instanceof ApiError && q.error.status === 404) return null;
  if (q.isError)
    return (
      <Card title={t("airflow.label")} titleId="airflow">
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      </Card>
    );
  const a = q.data;
  if (!a || (!a.rack && !a.check.airflow)) return null;
  const front = aisleOf(a.front_aisle);
  const face = a.face === "front" || a.face === "rear" ? a.face : undefined;
  const onAisle = face ? faceAisle(front, face) : undefined;
  return (
    <Card title={t("airflow.label")} titleId="airflow" actions={<AirflowPill check={a.check} />} className="airflow-card">
      <p data-airflow-reason={a.check.reason}>{reason(a.check)}</p>
      <AirflowWarnings warnings={a.check.warnings} />
      <dl className="facts">
        {a.rack ? (
          <div>
            <dt>{t("airflow.card.rack")}</dt>
            <dd>
              <ObjectLink id={a.rack.id} display={a.rack.display} />
            </dd>
          </div>
        ) : null}
        {a.rack ? (
          <div>
            <dt>{t("airflow.card.face")}</dt>
            <dd>{face ? t(face === "front" ? "face.front" : "face.rear") : t("airflow.card.none")}</dd>
          </div>
        ) : null}
        {a.rack ? (
          <div>
            <dt>{t("airflow.card.aisle")}</dt>
            <dd>{onAisle ? <span className={`aisle-chip aisle-${onAisle}`}>{t(onAisle === "cold" ? "aisle.cold" : "aisle.hot")}</span> : t("airflow.card.none")}</dd>
          </div>
        ) : null}
        <div>
          <dt>{t("airflow.card.model")}</dt>
          <dd>
            {value(a.model_airflow)}
            {a.model ? (
              <>
                {" "}
                <span className="muted">
                  (<ObjectLink id={a.model.id} display={a.model.display} />)
                </span>
              </>
            ) : null}
          </dd>
        </div>
        <div>
          <dt>{t("airflow.card.device")}</dt>
          <dd>{value(a.device_airflow)}</dd>
        </div>
        {a.parts.length > 0 ? (
          <div>
            <dt>{t("airflow.card.parts")}</dt>
            <dd>
              <ul className="airflow-parts">
                {a.parts.map((p) => (
                  <li key={p.part.id}>
                    <ObjectLink id={p.part.id} display={p.part.display} />: {value(p.airflow)}
                  </li>
                ))}
              </ul>
            </dd>
          </div>
        ) : null}
        <div>
          <dt>{t("airflow.card.used")}</dt>
          <dd>
            {value(a.check.airflow)}
            {a.check.airflow_from && known(`airflow.from.${a.check.airflow_from}`) ? (
              <span className="muted"> ({t(known(`airflow.from.${a.check.airflow_from}`) as MessageKey)})</span>
            ) : null}
          </dd>
        </div>
        {a.check.intake && known(`airflow.intake.${a.check.intake}`) ? (
          <div>
            <dt>{t("airflow.card.intake")}</dt>
            <dd>{t(known(`airflow.intake.${a.check.intake}`) as MessageKey)}</dd>
          </div>
        ) : null}
      </dl>
    </Card>
  );
}
