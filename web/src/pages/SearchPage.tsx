import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { ObjectView, SearchPage as SearchResult } from "../api/types";
import { Async, ErrorBox, Loading } from "../components/Async";
import { Pager, PAGE_SIZE, useOffset } from "../components/Pager";
import { Status } from "../components/Badge";
import { Empty, Time } from "../components/Time";
import { useSchemaLookup, type SchemaLookup } from "../hooks";
import { useI18n } from "../i18n";
import { SearchExamples } from "./HomePage";
import { usePageTitle } from "../usePageTitle";

/** How many results the grouped view asks for; the rest of a type is one click away. */
export const GROUPED_LIMIT = 100;

function ResultTable({ items, caption }: { items: ObjectView[]; caption: string }) {
  const { t } = useI18n();
  return (
    <table>
      <caption className="sr-only">{caption}</caption>
      <thead>
        <tr>
          <th scope="col">{t("relations.object")}</th>
          <th scope="col">{t("object.status")}</th>
          <th scope="col">{t("object.lastSeen")}</th>
        </tr>
      </thead>
      <tbody>
        {items.map((o) => (
          <tr key={o.id}>
            <td data-label={t("relations.object")}>
              <Link to={`/objects/${encodeURIComponent(o.id)}`}>{o.display}</Link>
            </td>
            <td data-label={t("object.status")}>
              <Status value={o.status} />
            </td>
            <td data-label={t("object.lastSeen")}>{o.last_seen ? <Time iso={o.last_seen} /> : <Empty />}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function searchUrl(q: string, type?: string): string {
  const p = new URLSearchParams({ q });
  if (type) p.set("type", type);
  return `/search?${p.toString()}`;
}

function Grouped({ q, data, lookup }: { q: string; data: SearchResult; lookup: SchemaLookup }) {
  const { t, tp, number } = useI18n();
  const byType = new Map<string, ObjectView[]>();
  for (const o of data.items) byType.set(o.type, [...(byType.get(o.type) ?? []), o]);
  // A type can be counted but absent from the first page of results.
  const types = new Set([...Object.keys(data.summary.by_type), ...byType.keys()]);
  const groups = [...types]
    .map((type) => ({ type, label: lookup.typeLabel(type), total: data.summary.by_type[type] ?? byType.get(type)?.length ?? 0 }))
    .sort((a, b) => b.total - a.total || a.label.localeCompare(b.label));

  return (
    <>
      <p className="summary" role="status">
        {tp("search.summary", data.total, { ms: data.summary.took_ms })}
      </p>
      {data.summary.fuzzy ? <p className="notice">{t("search.fuzzy", { q })}</p> : null}
      {data.total === 0 ? <p>{t("search.empty", { q })}</p> : null}
      {groups.map((g) => {
        const shown = byType.get(g.type) ?? [];
        const id = `group-${g.type}`;
        return (
          <section key={g.type} aria-labelledby={id} className="group" data-type={g.type}>
            <h2 id={id}>
              {g.label} <span className="count">({number(g.total)})</span>
            </h2>
            {shown.length > 0 ? <ResultTable items={shown} caption={g.label} /> : null}
            {shown.length < g.total ? (
              <p>
                {shown.length > 0 ? <span className="muted">{t("search.groupCount", { count: number(shown.length), total: number(g.total) })} </span> : null}
                <Link to={searchUrl(q, g.type)}>{t("search.showAll", { total: number(g.total), type: g.label })}</Link>
              </p>
            ) : null}
          </section>
        );
      })}
    </>
  );
}

function OneType({ q, type, lookup }: { q: string; type: string; lookup: SchemaLookup }) {
  const offset = useOffset();
  const { t, tp } = useI18n();
  const query = useQuery({
    queryKey: ["objects", q, type, offset],
    queryFn: ({ signal }) => api.objects({ q, type, limit: PAGE_SIZE, offset }, signal),
    placeholderData: (prev) => prev,
  });
  const label = lookup.typeLabel(type);
  return (
    <>
      <h2>
        {label}
      </h2>
      <p>
        <Link to={searchUrl(q)}>{t("search.backToAll")}</Link>
      </p>
      <Async query={query}>
        {(data) => (
          <>
            <p className="summary" role="status">
              {tp("search.summary", data.total, { ms: data.summary.took_ms })}
            </p>
            <ResultTable items={data.items} caption={label} />
            <Pager total={data.total} limit={data.limit} offset={data.offset} />
          </>
        )}
      </Async>
    </>
  );
}

export function SearchPage() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const q = params.get("q")?.trim() ?? "";
  const type = params.get("type") ?? "";
  const lookup = useSchemaLookup();
  const title = q === "" ? t("search.titleEmpty") : t("search.title", { q });
  usePageTitle(title);

  const grouped = useQuery({
    queryKey: ["search", q],
    queryFn: ({ signal }) => api.search({ q, limit: GROUPED_LIMIT }, signal),
    enabled: q !== "" && type === "",
  });

  return (
    <>
      <h1>{title}</h1>
      {q === "" ? (
        <>
          <p className="lead">
            {t("home.searchIntro")} {t("search.hint")}
          </p>
          <SearchExamples />
        </>
      ) : type !== "" ? (
        <OneType q={q} type={type} lookup={lookup} />
      ) : grouped.isPending ? (
        <Loading />
      ) : grouped.isError ? (
        <ErrorBox
          error={grouped.error}
          title={grouped.error instanceof ApiError && grouped.error.status === 400 ? t("search.queryError") : undefined}
          onRetry={() => void grouped.refetch()}
        />
      ) : (
        <Grouped q={q} data={grouped.data} lookup={lookup} />
      )}
    </>
  );
}
