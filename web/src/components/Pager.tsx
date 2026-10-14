import { Link, useSearchParams } from "react-router";
import { useI18n } from "../i18n";

export const PAGE_SIZE = 50;

/** Reads ?offset= as a non-negative integer. */
export function useOffset(): number {
  const [params] = useSearchParams();
  const n = Number.parseInt(params.get("offset") ?? "0", 10);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

/** Previous / next links that keep the other query parameters, so every page of a list has its own URL. */
export function Pager({ total, limit, offset }: { total: number; limit: number; offset: number }) {
  const { t, number } = useI18n();
  const [params] = useSearchParams();
  if (total <= limit && offset === 0) return null;

  const to = (o: number) => {
    const p = new URLSearchParams(params);
    if (o <= 0) p.delete("offset");
    else p.set("offset", String(o));
    const s = p.toString();
    return { search: s ? `?${s}` : "" };
  };
  const prev = offset > 0 ? Math.max(0, offset - limit) : null;
  const next = offset + limit < total ? offset + limit : null;

  return (
    <nav className="pager" aria-label={t("pager.label")}>
      {prev === null ? (
        <span aria-disabled="true" className="pager-link disabled">
          {t("pager.previous")}
        </span>
      ) : (
        <Link to={to(prev)} rel="prev" className="pager-link">
          {t("pager.previous")}
        </Link>
      )}
      <span className="pager-range">
        {t("pager.range", { from: number(offset + 1), to: number(Math.min(offset + limit, total)), total: number(total) })}
      </span>
      {next === null ? (
        <span aria-disabled="true" className="pager-link disabled">
          {t("pager.next")}
        </span>
      ) : (
        <Link to={to(next)} rel="next" className="pager-link">
          {t("pager.next")}
        </Link>
      )}
    </nav>
  );
}
