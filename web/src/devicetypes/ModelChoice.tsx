// The device page's "Device model" section (ADR 0048): how sure the
// inventory is of the device's model, and, when it is not sure which chassis
// the device is, its candidate chassis side by side to choose from. The
// choice is a declared instance_of, previewed then submitted like every
// declaration of the UI (ADR 0039).
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import { api } from "../api/endpoints";
import type { DeviceTypeCandidate, DeviceTypeChoice, ObjectView } from "../api/types";
import { ErrorBox } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { Alert, Card, Pill } from "../components/ui";
import { BatchReview } from "../edit/BatchReview";
import { callerSource, chooseModel } from "../edit/batch";
import { useCan, useMe } from "../hooks";
import { useI18n, type MessageKey } from "../i18n";
import { useFaceImage } from "../racks/faceImages";
import "./variants.css";

const DECIDED: Record<string, MessageKey> = {
  sku: "variant.decided.sku",
  model: "variant.decided.model",
  mounting: "variant.decided.mounting",
  part_number: "variant.decided.part_number",
  declared: "variant.decided.declared",
};

const REASON: Record<string, MessageKey> = {
  base: "variant.reason.base",
  airflow: "variant.reason.airflow",
  ambiguous: "variant.reason.ambiguous",
};

export function useModelChoice(obj: ObjectView) {
  return useQuery({
    queryKey: ["device-type-candidates", obj.id],
    queryFn: ({ signal }) => api.deviceTypeCandidates(obj.id, signal),
    enabled: obj.type === "device",
    staleTime: 60_000,
  });
}

/** Whether the page should ask a person to choose: not certain, and something to choose from. */
export const needsChoice = (c: DeviceTypeChoice | undefined): boolean => !!c && !c.certain && c.candidates.length > 0;

/** The header's pill: said only when the variant is not certain. */
export function VariantPill({ obj }: { obj: ObjectView }) {
  const { t } = useI18n();
  const q = useModelChoice(obj);
  if (!needsChoice(q.data)) return null;
  return (
    <a href="#device-model" className="variant-pill-link">
      <Pill tone="warn" value="variant-uncertain">
        {t("variant.pill")}
      </Pill>
    </a>
  );
}

export function ModelChoiceSection({ obj }: { obj: ObjectView }) {
  const { t } = useI18n();
  const q = useModelChoice(obj);
  if (obj.type !== "device") return null;
  if (q.isError)
    return (
      <Card title={t("variant.title")} titleId="device-model">
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      </Card>
    );
  const c = q.data;
  if (!c || (!c.current && c.candidates.length === 0)) return null;
  return <ModelChoice obj={obj} choice={c} />;
}

function ModelChoice({ obj, choice: c }: { obj: ObjectView; choice: DeviceTypeChoice }) {
  const { t } = useI18n();
  const isAdmin = useCan("admin");
  const source = callerSource(useMe().data);
  const [changing, setChanging] = useState<string | null>(null);
  const [applied, setApplied] = useState(false);
  const uncertain = needsChoice(c);
  const current = c.current;
  const currentName = current ? current.model_name || current.model.display : "";
  // "Change" is offered to an administrator, and to whoever made the declaration that decides.
  const mayChange = c.can_choose && c.candidates.length > 1 && (isAdmin || (c.reason === "declared" && !!source && c.declared_by === source));
  const conflict = c.sku_conflict;
  return (
    <Card title={t("variant.title")} titleId="device-model" className="variant-card">
      {uncertain ? (
        <Alert tone="warn" role="note" title={t("variant.uncertain")}>
          <p>{t(REASON[c.reason] ?? "variant.reason.ambiguous", { model: c.model ?? "" })}</p>
          <p>{current ? t("variant.current", { model: currentName }) : t("variant.noCurrent")}</p>
          {c.can_choose ? null : <p className="hint">{t("variant.cannotChoose")}</p>}
        </Alert>
      ) : (
        <p className="variant-decided" data-reason={c.reason}>
          {current ? (
            <>
              <ObjectLink id={current.model.id} display={currentName} />.{" "}
            </>
          ) : null}
          {DECIDED[c.reason] ? t(DECIDED[c.reason] as MessageKey, { source: c.declared_by ?? "" }) : null}
        </p>
      )}
      {conflict ? (
        <Alert tone="info" role="note">
          <p>{t("variant.skuConflict", { model: conflict.model })}</p>
          {c.can_choose && changing === null ? (
            <p>
              <button type="button" onClick={() => setChanging(conflict.slug)}>
                {t("variant.useSku", { model: conflict.model })}
              </button>
            </p>
          ) : null}
        </Alert>
      ) : null}
      {!applied && ((uncertain && c.can_choose) || changing !== null) ? (
        <Chooser
          obj={obj}
          choice={c}
          source={source}
          initial={changing ?? undefined}
          onClose={uncertain ? undefined : () => setChanging(null)}
          onApplied={() => {
            setChanging(null);
            setApplied(true);
          }}
        />
      ) : mayChange ? (
        <p className="actions">
          <button
            type="button"
            onClick={() => {
              setApplied(false);
              setChanging(current?.slug ?? "");
            }}
          >
            {t("variant.change")}
          </button>
        </p>
      ) : null}
      {applied ? (
        <p role="status" className="decision-notice">
          {t("edit.outcome.applied")}
        </p>
      ) : null}
    </Card>
  );
}

function Chooser({
  obj,
  choice: c,
  source,
  initial,
  onClose,
  onApplied,
}: {
  obj: ObjectView;
  choice: DeviceTypeChoice;
  source: string | undefined;
  initial?: string;
  onClose?: () => void;
  /** The choice was applied (not queued for approval): the section shows the new model. */
  onApplied: () => void;
}) {
  const { t } = useI18n();
  const name = useId();
  const legendRef = useRef<HTMLLegendElement>(null);
  const [slug, setSlug] = useState<string>(initial && c.candidates.some((k) => k.slug === initial && !k.current) ? initial : "");
  const chosen = c.candidates.find((k) => k.slug === slug);
  useEffect(() => {
    if (onClose) legendRef.current?.focus();
  }, [onClose]);
  // This caller's earlier declarations of another model are withdrawn; others' stay (ADR 0004).
  const withdraw = c.declarations.filter((d) => d.slug !== slug && (!source || d.source === source)).map((d) => d.model.id);
  const batch = chosen && !chosen.current ? chooseModel({ deviceId: obj.id, slug: chosen.slug, withdraw }) : null;
  const conflict = c.sku_conflict;
  return (
    <div className="variant-chooser">
      <fieldset>
        <legend ref={legendRef} tabIndex={-1}>
          {t("variant.legend")}
        </legend>
        <p className="hint">{t("variant.hint")}</p>
        <div className="variant-options">
          {c.candidates.map((k) => (
            <label key={k.slug} className={`variant-option${k.slug === slug ? " variant-option-selected" : ""}`} data-slug={k.slug}>
              <input type="radio" name={name} value={k.slug} checked={k.slug === slug} onChange={() => setSlug(k.slug)} />
              <FaceThumb url={k.front_image} />
              <span className="variant-option-name">{k.model}</span>
              <span className="variant-option-facts">
                {t("variant.units", { units: k.u_height })}
                {k.full_depth ? ` · ${t("variant.fullDepth")}` : ""}
              </span>
              {k.current || k.library_match ? (
                <span className="variant-option-tags">
                  {k.current ? <Pill tone="neutral">{t("variant.isCurrent")}</Pill> : null}
                  {k.library_match ? <Pill tone="info">{t("variant.isMatch")}</Pill> : null}
                </span>
              ) : null}
            </label>
          ))}
        </div>
      </fieldset>
      {chosen ? <Preview candidate={chosen} /> : null}
      {batch && chosen ? (
        <>
          <p>{t("variant.declares", { device: obj.display, model: chosen.model })}</p>
          <BatchReview
            key={chosen.slug}
            batch={batch}
            names={{ model: chosen.model }}
            warnings={conflict && conflict.slug !== chosen.slug ? [t("variant.skuConflict", { model: conflict.model })] : []}
            onDone={(out) => {
              if (out.status === "applied") onApplied();
            }}
            onCancel={() => (onClose ? onClose() : setSlug(""))}
          />
        </>
      ) : onClose ? (
        <p className="actions">
          <button type="button" onClick={onClose}>
            {t("edit.cancel")}
          </button>
        </p>
      ) : null}
    </div>
  );
}

function Preview({ candidate: k }: { candidate: DeviceTypeCandidate }) {
  const { t } = useI18n();
  return (
    <section className="variant-preview" aria-label={k.model} data-slug={k.slug}>
      <figure>
        <figcaption>{t("rack.front")}</figcaption>
        <FaceThumb url={k.front_image} large />
      </figure>
      {k.rear_image ? (
        <figure>
          <figcaption>{t("rack.rear")}</figcaption>
          <FaceThumb url={k.rear_image} large />
        </figure>
      ) : null}
    </section>
  );
}

/**
 * A face image, painted on a canvas from the bitmap the rack's loader decoded
 * (faceImages.ts): the CSP allows no blob: or data: image. Decorative: the
 * option's text says which model it is.
 */
export function FaceThumb({ url, large = false }: { url?: string; large?: boolean }) {
  const { t } = useI18n();
  const bitmap = useFaceImage(url);
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    if (!ctx || !bitmap) return;
    ctx.clearRect(0, 0, bitmap.width, bitmap.height);
    ctx.drawImage(bitmap, 0, 0);
  }, [bitmap]);
  const cls = `variant-thumb${large ? " variant-thumb-large" : ""}`;
  if (!bitmap)
    return (
      <span className={`${cls} variant-thumb-none`} data-drawn="none" aria-hidden="true">
        {url ? "" : t("variant.noImage")}
      </span>
    );
  return <canvas ref={ref} className={cls} width={bitmap.width} height={bitmap.height} aria-hidden="true" data-drawn="image" />;
}
