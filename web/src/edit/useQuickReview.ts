// The preview of a compact dialog (ADR 0052). The full form asks for a click on
// "Preview"; a dialog that places one device has nothing to wait for, so the
// batch is sent to POST /batches/preview as soon as it is valid and has stopped
// changing. It is still the engine's answer that is shown, and the confirm
// button is open only under a preview of the batch that would be sent: the page
// never says what a change does on its own authority (ADR 0039).
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/endpoints";
import type { ApplyResult, Batch, BatchOutcome } from "../api/types";
import { INVENTORY_QUERIES, visibleChanges } from "./BatchReview";

export const PREVIEW_DELAY_MS = 300;

export interface QuickReview {
  /** The engine's answer for the batch as it stands now, or undefined while there is none (pending, stale, failed). */
  result?: ApplyResult;
  pending: boolean;
  /** The preview failed (the error as thrown). */
  error?: unknown;
  /** The preview holds and would change something: confirming is allowed. */
  canConfirm: boolean;
  confirm: (onOutcome?: (o: BatchOutcome) => void) => void;
  submitting: boolean;
  submitError?: unknown;
  outcome?: BatchOutcome;
}

export function useQuickReview(
  batch: Batch | null,
  delay = PREVIEW_DELAY_MS,
): QuickReview {
  const client = useQueryClient();
  const key = useMemo(
    () => (batch ? JSON.stringify({ ...batch, observed_at: "" }) : ""),
    [batch],
  );
  const latest = useRef(batch);
  useEffect(() => {
    latest.current = batch;
  });
  const preview = useMutation({
    mutationFn: (b: Batch) => api.previewBatch(b),
  });
  const submit = useMutation({
    mutationFn: (b: Batch) =>
      api.submitBatch({ ...b, observed_at: new Date().toISOString() }),
    onSuccess: (out) => {
      if (out.status === "applied")
        for (const k of INVENTORY_QUERIES)
          void client.invalidateQueries({ queryKey: [k] });
      void client.invalidateQueries({ queryKey: ["proposals"] });
    },
  });
  const [previewedKey, setPreviewedKey] = useState("");
  const { mutate: runPreview, reset: resetPreview } = preview;

  useEffect(() => {
    if (key === "") {
      resetPreview();
      return;
    }
    const timer = setTimeout(() => {
      const b = latest.current;
      if (!b) return;
      setPreviewedKey(key);
      runPreview(b);
    }, delay);
    return () => clearTimeout(timer);
  }, [key, delay, runPreview, resetPreview]);

  const fresh = previewedKey === key && key !== "";
  const result = fresh ? preview.data?.result : undefined;
  const pending = key !== "" && (!fresh || preview.isPending);
  const hasChanges = result ? visibleChanges(result).length > 0 : false;
  return {
    result,
    pending: pending && !(fresh && preview.isError),
    error: fresh && preview.isError ? preview.error : undefined,
    canConfirm:
      !!result && hasChanges && !submit.isPending && !submit.isSuccess,
    confirm: (onOutcome) => {
      if (batch) submit.mutate(batch, { onSuccess: (o) => onOutcome?.(o) });
    },
    submitting: submit.isPending,
    submitError: submit.isError ? submit.error : undefined,
    outcome: submit.data,
  };
}
