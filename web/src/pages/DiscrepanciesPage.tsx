import { useQuery } from "@tanstack/react-query";
import type { FormEvent } from "react";
import { useSearchParams } from "react-router";
import { api } from "../api/endpoints";
import type { DiscrepancyStatus } from "../api/types";
import { Async } from "../components/Async";
import { Status } from "../components/Badge";
import { ObjectLink } from "../components/ObjectLink";
import { Pager, PAGE_SIZE, useOffset } from "../components/Pager";
import { Empty, Time } from "../components/Time";
import { LocationConflict } from "../components/LocationConflict";
import { Value } from "../components/Value";
import { DISCREPANCY_KINDS, isKnownKind, kindLabel, locationConflictOf } from "../discrepancies";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";

const STATUSES: DiscrepancyStatus[] = ["open", "resolved", "accepted"];

export function DiscrepanciesPage() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const offset = useOffset();
  const status = params.get("status") ?? "open";
  const kind = params.get("kind") ?? "";
  usePageTitle(t("discrepancies.title"));

  const query = useQuery({
    queryKey: ["discrepancies", status, kind, offset],
    queryFn: ({ signal }) =>
      api.discrepancies({ status: status === "all" ? undefined : (status as DiscrepancyStatus), kind, limit: PAGE_SIZE, offset }, signal),
    placeholderData: (prev) => prev,
  });

  const apply = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const s = form.get("status");
    const k = form.get("kind");
    const next: Record<string, string> = {};
    if (typeof s === "string" && s !== "open") next.status = s;
    if (typeof k === "string" && k.trim() !== "") next.kind = k.trim();
    setParams(next);
  };

  return (
    <>
      <h1>{t("discrepancies.title")}</h1>
      <p>{t("discrepancies.intro")}</p>
      <form className="filters" onSubmit={apply}>
        <label>
          {t("discrepancies.filterStatus")}
          <select name="status" defaultValue={status}>
            <option value="all">{t("filter.all")}</option>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`status.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("discrepancies.filterKind")}
          <select name="kind" defaultValue={kind}>
            <option value="">{t("discrepancies.allKinds")}</option>
            {DISCREPANCY_KINDS.map((k) => (
              <option key={k} value={k}>
                {kindLabel(t, k)}
              </option>
            ))}
            {kind !== "" && !isKnownKind(kind) ? <option value={kind}>{kind}</option> : null}
          </select>
        </label>
        <button type="submit">{t("filter.apply")}</button>
      </form>
      <Async query={query}>
        {(page) =>
          page.items.length === 0 ? (
            <p>{t("discrepancies.empty")}</p>
          ) : (
            <>
              <table>
                <caption className="sr-only">{t("discrepancies.title")}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t("discrepancies.kind")}</th>
                    <th scope="col">{t("discrepancies.object")}</th>
                    <th scope="col">{t("discrepancies.key")}</th>
                    <th scope="col">{t("discrepancies.declared")}</th>
                    <th scope="col">{t("discrepancies.observed")}</th>
                    <th scope="col">{t("discrepancies.status")}</th>
                    <th scope="col">{t("discrepancies.lastSeen")}</th>
                    <th scope="col">{t("discrepancies.openedAt")}</th>
                  </tr>
                </thead>
                <tbody>
                  {page.items.map((d) => (
                    <tr key={d.id}>
                      <th scope="row" data-label={t("discrepancies.kind")}>
                        {isKnownKind(d.kind) ? <div>{kindLabel(t, d.kind)}</div> : null}
                        <code>{d.kind}</code>
                      </th>
                      <td data-label={t("discrepancies.object")}>{d.object_id ? <ObjectLink id={d.object_id} /> : <Empty />}</td>
                      <td data-label={t("discrepancies.key")}>{d.key ? <code>{d.key}</code> : <Empty />}</td>
                      <td data-label={t("discrepancies.declared")}>
                        <Declared kind={d.kind} declared={d.declared} details={d.details} />
                      </td>
                      <td data-label={t("discrepancies.observed")}>
                        <Value value={d.observed} />
                      </td>
                      <td data-label={t("discrepancies.status")}>
                        <Status value={d.status} />
                        {d.note ? <div className="muted">{d.note}</div> : null}
                      </td>
                      <td data-label={t("discrepancies.lastSeen")}>
                        <Time iso={d.last_seen} />
                      </td>
                      <td data-label={t("discrepancies.openedAt")}>
                        <Time iso={d.opened_at} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <Pager total={page.total} limit={page.limit} offset={page.offset} />
            </>
          )
        }
      </Async>
    </>
  );
}

/** What was declared: a value, or, for a location conflict, the places that disagree. */
function Declared({ kind, declared, details }: { kind: string; declared: unknown; details: unknown }) {
  const conflict = kind === "location_conflict" ? locationConflictOf(details) : null;
  return conflict ? <LocationConflict details={conflict} /> : <Value value={declared} />;
}
