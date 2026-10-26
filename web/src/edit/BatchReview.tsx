// Preview, then submit. The page never says what a change will do on its own
// authority: it sends the batch to POST /batches/preview and shows what the
// engine answers (the objects it would create or recognise, the values and
// relations it would open or close, its warnings). Submitting sends the same
// batch to POST /batches; the answer says whether it was applied (200) or
// queued for approval (202), and a refusal is shown as the server wrote it.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type {
  ApplyResult,
  Batch,
  BatchOutcome,
  PlannedChange,
} from "../api/types";
import { ObjectLink } from "../components/ObjectLink";
import { formatValue } from "../components/Value";
import { useSchemaLookup } from "../hooks";
import { useI18n, type MessageKey } from "../i18n";

/** What the page knows about the objects a batch creates, keyed by the batch's local ref. */
export type RefNames = Record<string, string>;

/** Every query a declaration can change; refreshed after an applied batch. */
export const INVENTORY_QUERIES = [
  "object",
  "objects",
  "rooms",
  "room-contents",
  "rack-contents",
  "relations",
  "provenance",
  "timeline",
  "search",
  "discrepancies",
  "placement",
  "placement-history",
  "rack-removed",
  "faces",
  "device-type-candidates",
];

function errorText(e: unknown): MessageKey {
  if (!(e instanceof ApiError)) return "edit.failed.generic";
  switch (e.status) {
    case 0:
      return "error.network";
    case 400:
      return "edit.failed.400";
    case 403:
      return "edit.failed.403";
    case 409:
      return e.type.endsWith(":name-taken")
        ? "edit.failed.nameTaken"
        : "edit.failed.409";
    case 422:
      return "edit.failed.422";
    default:
      return "edit.failed.generic";
  }
}

export function Failure({ error, title }: { error: unknown; title: string }) {
  const { t } = useI18n();
  const detail = error instanceof ApiError ? error.detail || error.title : "";
  return (
    <div
      role="alert"
      className="notice notice-error"
      data-status={error instanceof ApiError ? error.status : undefined}
    >
      <p>
        <strong>{title}</strong>
      </p>
      <p>{t(errorText(error))}</p>
      {detail ? <p className="detail">{detail}</p> : null}
    </div>
  );
}

/** One planned change, in words. */
function ChangeLine({
  c,
  created,
  names,
}: {
  c: PlannedChange;
  created: Map<string, string>;
  names: RefNames;
}) {
  const { t } = useI18n();
  const lookup = useSchemaLookup();
  const f = (v: unknown) =>
    formatValue(v, t("app.yes"), t("app.no")) || t("app.none");
  const obj = (id: string | undefined, type?: string) => {
    if (!id) return <>{t("app.unknown")}</>;
    const ref = created.get(id);
    if (ref !== undefined)
      return (
        <strong>
          {names[ref] ??
            t("edit.change.newObject", { type: lookup.typeLabel(type ?? "") })}
        </strong>
      );
    return <ObjectLink id={id} />;
  };
  switch (c.op) {
    case "create":
      return (
        <>
          {t("edit.change.create", { type: lookup.typeLabel(c.type) })}{" "}
          {obj(c.object_id, c.type)}
        </>
      );
    case "relate":
      return (
        <>
          {obj(c.object_id, c.type)} {lookup.relationLabel(c.key ?? "", "out")}{" "}
          {obj(c.other_id)}
          {c.before !== undefined && c.before !== null
            ? ` ${t("edit.change.was", { was: mountText(c.before, t, f).replace(/^, /, "") || t("app.none") })}`
            : ""}
          {mountText(c.after, t, f)}
        </>
      );
    case "unrelate":
      return (
        <>
          {t("edit.change.unrelate", {
            relation: lookup.relationLabel(c.key ?? "", "out"),
          })}{" "}
          {obj(c.object_id, c.type)} → {obj(c.other_id)}
        </>
      );
    case "move":
      return (
        <>
          {obj(c.object_id, c.type)} {t("edit.change.move")}{" "}
          {c.before ? obj(String(c.before)) : t("app.none")} →{" "}
          {c.after ? obj(String(c.after)) : t("app.none")}
        </>
      );
    default:
      return (
        <>
          {obj(c.object_id, c.type)} —{" "}
          {lookup.attributeLabel(c.type, c.key ?? "")}:{" "}
          {c.before === undefined || c.before === null
            ? t("app.none")
            : f(c.before)}{" "}
          →{" "}
          {c.after === undefined || c.after === null
            ? t("app.none")
            : f(c.after)}
        </>
      );
  }
}

function mountText(
  after: unknown,
  t: (k: MessageKey, p?: Record<string, string | number>) => string,
  f: (v: unknown) => string,
): string {
  if (!after || typeof after !== "object") return "";
  const a = after as Record<string, unknown>;
  const parts: string[] = [];
  if (a.position !== undefined)
    parts.push(t("edit.change.atU", { u: f(a.position) }));
  if (typeof a.face === "string")
    parts.push(t("edit.change.onFace", { face: a.face }));
  return parts.length ? `, ${parts.join(", ")}` : "";
}

/** The changes worth reading: system bookkeeping of a creation (_status, _parent) is left out. */
export function visibleChanges(r: ApplyResult): PlannedChange[] {
  return (r.changes ?? []).filter(
    (c) => !(c.op !== "move" && c.key?.startsWith("_")),
  );
}

export function ResultChanges({
  result,
  names,
}: {
  result: ApplyResult;
  names: RefNames;
}) {
  const { t } = useI18n();
  const created = useMemo(() => {
    const byId = new Map<string, string>();
    const refOf = new Map(
      Object.entries(result.objects ?? {}).map(([ref, id]) => [id, ref]),
    );
    for (const c of result.changes ?? [])
      if (c.op === "create")
        byId.set(c.object_id, refOf.get(c.object_id) ?? "");
    return byId;
  }, [result]);
  const changes = visibleChanges(result);
  if (changes.length === 0) return <p>{t("edit.preview.nothing")}</p>;
  return (
    <ul className="changes">
      {changes.map((c, i) => (
        <li
          key={`${c.op}-${c.object_id}-${c.key ?? ""}-${c.other_id ?? ""}-${i}`}
          data-op={c.op}
        >
          <ChangeLine c={c} created={created} names={names} />
        </li>
      ))}
    </ul>
  );
}

export interface BatchReviewProps {
  /** The batch to send, or null while the form has errors. */
  batch: Batch | null;
  /** Names of the objects the batch creates, by local ref, for the preview. */
  names?: RefNames;
  /** Warnings the page found itself (an overlap, a tile already taken), shown with the preview. */
  warnings?: string[];
  /** Warnings that depend on the engine's answer (a new device that matches one already there). */
  resultWarnings?: (result: ApplyResult) => string[];
  onDone?: (outcome: BatchOutcome) => void;
  onCancel: () => void;
  /** Preview the first batch the form hands over without waiting for the button (once). */
  autoPreview?: boolean;
}

export function BatchReview({
  batch,
  names = {},
  warnings: own = [],
  resultWarnings,
  onDone,
  onCancel,
  autoPreview,
}: BatchReviewProps) {
  const { t, tp } = useI18n();
  const client = useQueryClient();
  const [reason, setReason] = useState("");
  const key = useMemo(
    () => (batch ? JSON.stringify({ ...batch, observed_at: "" }) : ""),
    [batch],
  );
  const preview = useMutation({
    mutationFn: (b: Batch) => api.previewBatch(b),
  });
  const submit = useMutation({
    mutationFn: (b: Batch) =>
      api.submitBatch(
        { ...b, observed_at: new Date().toISOString() },
        reason.trim() || undefined,
      ),
    onSuccess: (out) => {
      if (out.status === "applied")
        for (const k of INVENTORY_QUERIES)
          void client.invalidateQueries({ queryKey: [k] });
      void client.invalidateQueries({ queryKey: ["proposals"] });
      onDone?.(out);
    },
  });
  const [previewedKey, setPreviewedKey] = useState("");
  const autoDone = useRef(false);
  const outcomeRef = useRef<HTMLDivElement>(null);
  const previewRef = useRef<HTMLHeadingElement>(null);

  // A preview describes one batch: change the form and it no longer holds.
  const stale = previewedKey !== key;
  const { reset: resetPreview } = preview;
  const { reset: resetSubmit, isSuccess: submitted } = submit;
  useEffect(() => {
    if (stale && !submitted) {
      resetPreview();
      resetSubmit();
    }
  }, [stale, submitted, resetPreview, resetSubmit]);

  const { mutate: runPreview } = preview;
  useEffect(() => {
    if (!autoPreview || autoDone.current || !batch) return;
    autoDone.current = true;
    setPreviewedKey(key);
    runPreview(batch);
  }, [autoPreview, batch, key, runPreview]);

  useEffect(() => {
    if (preview.isSuccess && !stale) previewRef.current?.focus();
  }, [preview.isSuccess, stale]);
  useEffect(() => {
    if (submit.isSuccess || submit.isError) outcomeRef.current?.focus();
  }, [submit.isSuccess, submit.isError]);

  const result = !stale ? preview.data?.result : undefined;
  const changes = result ? visibleChanges(result) : [];
  const warnings = [
    ...own,
    ...(result && resultWarnings ? resultWarnings(result) : []),
    ...(result?.warnings ?? []),
  ];

  if (submit.isSuccess) {
    const out = submit.data;
    return (
      <div
        ref={outcomeRef}
        tabIndex={-1}
        role="status"
        className="decision-notice"
        data-outcome={out.status}
      >
        {out.status === "proposed" ? (
          <p>
            {t("edit.outcome.proposed")}{" "}
            {out.result.proposal_id ? (
              <Link to={`/proposals/${out.result.proposal_id}`}>
                {t("edit.outcome.proposalLink", { id: out.result.proposal_id })}
              </Link>
            ) : null}
          </p>
        ) : (
          <>
            <p>{t("edit.outcome.applied")}</p>
            {Object.entries(out.result.objects ?? {})
              .filter(([ref]) => names[ref])
              .map(([ref, id]) => (
                <p key={ref}>
                  <ObjectLink
                    id={id}
                    display={t("edit.outcome.open", { name: names[ref] ?? "" })}
                  />
                </p>
              ))}
          </>
        )}
        <button type="button" onClick={onCancel}>
          {t("edit.close")}
        </button>
      </div>
    );
  }

  return (
    <div className="batch-review">
      <div className="actions">
        <button
          type="button"
          className={result ? undefined : "primary"}
          disabled={!batch || preview.isPending}
          onClick={() => {
            if (!batch) return;
            setPreviewedKey(key);
            preview.mutate(batch);
          }}
        >
          {t("edit.preview")}
        </button>{" "}
        <button type="button" onClick={onCancel}>
          {t("edit.cancel")}
        </button>
      </div>
      {preview.isPending ? (
        <p role="status" className="loading">
          {t("app.loading")}
        </p>
      ) : null}
      {preview.isError && !stale ? (
        <Failure error={preview.error} title={t("edit.preview.failed")} />
      ) : null}
      {result ? (
        <section aria-labelledby="preview-result" className="preview">
          <h3 id="preview-result" ref={previewRef} tabIndex={-1}>
            {t("edit.preview.title")}
          </h3>
          <p className="muted">
            {t("edit.preview.as", { source: preview.data?.source ?? "" })}
          </p>
          <ResultChanges result={result} names={names} />
          {warnings.length > 0 ? (
            <div role="note" className="notice">
              <p>
                <strong>{tp("edit.warnings", warnings.length)}</strong>
              </p>
              <ul>
                {warnings.map((w, i) => (
                  <li key={`${i}-${w}`}>{w}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {changes.length > 0 ? (
            <>
              <div className="field">
                <label htmlFor="edit-reason">{t("edit.reason")}</label>
                <input
                  id="edit-reason"
                  type="text"
                  maxLength={500}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                />
              </div>
              <p className="hint">{t("edit.submitHint")}</p>
              <button
                type="button"
                className="primary"
                disabled={submit.isPending || !batch}
                onClick={() => batch && submit.mutate(batch)}
              >
                {t("edit.submit")}
              </button>
            </>
          ) : null}
        </section>
      ) : null}
      {submit.isError ? (
        <div ref={outcomeRef} tabIndex={-1}>
          <Failure error={submit.error} title={t("edit.submit.failed")} />
        </div>
      ) : null}
    </div>
  );
}
