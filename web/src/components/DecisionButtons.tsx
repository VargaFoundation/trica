import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { Proposal } from "../api/types";
import { useCan } from "../hooks";
import { useI18n } from "../i18n";
import { ErrorBox } from "./Async";

export type Decision = "approve" | "reject";

/**
 * Approve / reject a pending proposal. The buttons exist only when the caller
 * holds the "approve" action (GET /me) and the proposal is still pending; the
 * API checks both again and answers 403 or 409, which is shown as it comes.
 * Each decision asks for a confirmation first: it cannot be taken back.
 */
export function DecisionButtons({ proposal, onDecided }: { proposal: Proposal; onDecided?: (d: Decision, id: number) => void }) {
  const { t } = useI18n();
  const can = useCan("approve");
  const client = useQueryClient();
  const [asking, setAsking] = useState<Decision | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  const mutation = useMutation({
    mutationFn: (d: Decision) => (d === "approve" ? api.approve(proposal.id) : api.reject(proposal.id)),
    onSuccess: (_out, d) => {
      setAsking(null);
      // What changed: the proposal itself, the lists, and (on approval) the inventory.
      void client.invalidateQueries({ queryKey: ["proposals"] });
      void client.invalidateQueries({ queryKey: ["proposal", proposal.id] });
      if (d === "approve") {
        void client.invalidateQueries({ queryKey: ["object"] });
        void client.invalidateQueries({ queryKey: ["provenance"] });
        void client.invalidateQueries({ queryKey: ["relations"] });
        void client.invalidateQueries({ queryKey: ["timeline"] });
        void client.invalidateQueries({ queryKey: ["discrepancies"] });
      }
      onDecided?.(d, proposal.id);
    },
    onError: (e) => {
      setAsking(null);
      // 409: someone decided first. Refresh so the list shows the real status.
      if (e instanceof ApiError && e.status === 409) void client.invalidateQueries({ queryKey: ["proposals"] });
    },
  });

  useEffect(() => {
    if (asking) confirmRef.current?.focus();
  }, [asking]);

  const pending = proposal.status === "pending";
  if (!can) return null;

  const id = proposal.id;
  const hidden = (
    <>
      {" "}
      <span className="sr-only">#{id}</span>
    </>
  );
  return (
    <div className="decision" data-proposal={id}>
      {!pending ? null : asking ? (
        <div role="group" aria-label={asking === "approve" ? t("proposals.confirmApprove", { id }) : t("proposals.confirmReject", { id })}>
          <span>{asking === "approve" ? t("proposals.confirmApprove", { id }) : t("proposals.confirmReject", { id })}</span>{" "}
          <button type="button" ref={confirmRef} className={asking === "reject" ? "danger" : "primary"} disabled={mutation.isPending} onClick={() => mutation.mutate(asking)}>
            {t("proposals.confirm")}
          </button>{" "}
          <button type="button" onClick={() => setAsking(null)} disabled={mutation.isPending}>
            {t("proposals.cancel")}
          </button>
        </div>
      ) : (
        <>
          <button type="button" className="primary" onClick={() => setAsking("approve")}>
            {t("proposals.approve")}
            {hidden}
          </button>{" "}
          <button type="button" onClick={() => setAsking("reject")}>
            {t("proposals.reject")}
            {hidden}
          </button>
        </>
      )}
      {mutation.isError ? <ErrorBox error={mutation.error} title={t("proposals.decisionFailed")} /> : null}
    </div>
  );
}
