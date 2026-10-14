import type { UseQueryResult } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { ApiError } from "../api/client";
import { useI18n } from "../i18n";

export function ErrorBox({ error, title, onRetry }: { error: unknown; title?: string; onRetry?: () => void }) {
  const { t } = useI18n();
  let message = t("error.generic");
  let detail = "";
  if (error instanceof ApiError) {
    if (error.status === 0) message = t("error.network");
    else if (error.status === 403) message = t("error.forbidden");
    else if (error.status === 404) message = t("error.notFound");
    detail = error.detail || error.title;
  }
  return (
    <div role="alert" className="notice notice-error">
      <p>
        <strong>{title ?? t("error.title")}</strong>
      </p>
      <p>{message}</p>
      {detail && detail !== message ? <p className="detail">{detail}</p> : null}
      {onRetry ? (
        <button type="button" onClick={onRetry}>
          {t("app.retry")}
        </button>
      ) : null}
    </div>
  );
}

export function Loading() {
  const { t } = useI18n();
  return (
    <p role="status" className="loading">
      {t("app.loading")}
    </p>
  );
}

/** Renders a query's three states: loading, failed (with retry), loaded. */
export function Async<T>({ query, children }: { query: UseQueryResult<T, Error>; children: (data: T) => ReactNode }) {
  if (query.isPending) return <Loading />;
  if (query.isError) return <ErrorBox error={query.error} onRetry={() => void query.refetch()} />;
  return <>{children(query.data)}</>;
}
