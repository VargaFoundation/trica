import { useQuery } from "@tanstack/react-query";
import { useMemo, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Status } from "../components/Badge";
import { ErrorBox, Loading } from "../components/Async";
import { Pager, PAGE_SIZE, useOffset } from "../components/Pager";
import { Empty, Time } from "../components/Time";
import { HealthPill, KindIcon, Table, useKindLabel } from "../components/ui";
import { useSchema, useSchemaLookup } from "../hooks";
import { useI18n, type MessageKey } from "../i18n";
import { healthOf } from "../inventory/health";
import { usePageTitle } from "../usePageTitle";

/** The health values an object reports; "absent" is a presence, filtered apart. */
const HEALTH_FILTER = ["critical", "warning", "ok", "unknown"] as const;
const PRESENCE = ["active", "absent", "all"] as const;
type Presence = (typeof PRESENCE)[number];

/** Sort choices, as the API's sort parameter. */
export const SORTS: { value: string; label: MessageKey }[] = [
  { value: "display", label: "objects.sort.display" },
  { value: "type,display", label: "objects.sort.type" },
  { value: "-last_seen,display", label: "objects.sort.lastSeen" },
  { value: "-first_seen,display", label: "objects.sort.firstSeen" },
];

/** As many sites as one page lists: a deployment with more is not this page's concern. */
const SITE_LIMIT = 1000;

export interface ObjectFilters {
  text: string;
  type: string;
  site: string;
  health: string;
  presence: Presence;
  sort: string;
}

export function readFilters(params: URLSearchParams): ObjectFilters {
  const presence = params.get("presence");
  const sort = params.get("sort");
  return {
    text: params.get("q")?.trim() ?? "",
    type: params.get("type") ?? "",
    site: params.get("site") ?? "",
    health: params.get("health") ?? "",
    presence: (PRESENCE as readonly string[]).includes(presence ?? "") ? (presence as Presence) : "active",
    sort: SORTS.some((s) => s.value === sort) ? (sort as string) : "display",
  };
}

/** Quotes a value for TQL when it needs it. */
function quote(v: string): string {
  return /^[\w.:*-]+$/.test(v) ? v : `"${v.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

/**
 * The TQL query of the filters. The reader's own text is grouped, so that an OR
 * in it does not escape the other filters.
 */
export function filtersQuery(f: ObjectFilters): string {
  const terms: string[] = [];
  if (f.site) terms.push(`site:${quote(f.site)}`);
  if (f.health) terms.push(`health:${quote(f.health)}`);
  if (f.presence === "absent") terms.push("status:absent");
  if (f.presence === "all") terms.push("status:active,absent");
  if (f.text) terms.push(`(${f.text})`);
  return terms.join(" ");
}

function ObjectRows({ items, siteName }: { items: ObjectView[]; siteName: (id?: string) => string | undefined }) {
  const { t } = useI18n();
  const kindLabel = useKindLabel();
  const lookup = useSchemaLookup();
  return (
    <Table className="objects-table">
      <caption className="sr-only">{t("objects.title")}</caption>
      <thead>
        <tr>
          <th scope="col">{t("objects.name")}</th>
          <th scope="col">{t("objects.type")}</th>
          <th scope="col">{t("objects.parentOrSite")}</th>
          <th scope="col">{t("objects.health")}</th>
          <th scope="col">{t("object.status")}</th>
          <th scope="col">{t("object.lastSeen")}</th>
        </tr>
      </thead>
      <tbody>
        {items.map((o) => {
          const { kind, label } = kindLabel(o);
          const site = siteName(o.site_id);
          const hasHealth = !!lookup.attribute(o.type, "health") || o.status === "absent";
          return (
            <tr key={o.id}>
              <th scope="row" data-label={t("objects.name")}>
                <span className="object-cell">
                  <KindIcon kind={kind} />
                  <Link to={`/objects/${encodeURIComponent(o.id)}`}>{o.display}</Link>
                </span>
              </th>
              <td data-label={t("objects.type")}>{label}</td>
              <td data-label={t("objects.parentOrSite")}>
                {o.parent ? (
                  <Link to={`/objects/${encodeURIComponent(o.parent.id)}`}>{o.parent.display}</Link>
                ) : site && o.site_id && o.site_id !== o.id ? (
                  <Link to={`/objects/${encodeURIComponent(o.site_id)}`}>{site}</Link>
                ) : (
                  site ?? <Empty />
                )}
              </td>
              <td data-label={t("objects.health")}>{hasHealth ? <HealthPill value={healthOf(o)} /> : <Empty />}</td>
              <td data-label={t("object.status")}>
                <Status value={o.status} />
              </td>
              <td data-label={t("object.lastSeen")}>{o.last_seen ? <Time iso={o.last_seen} /> : <Empty />}</td>
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
}

/** Every object of the inventory, filtered by type, site, health and presence, sorted, a page at a time. */
export function ObjectsPage() {
  const { t, tp, number, label } = useI18n();
  const [params, setParams] = useSearchParams();
  const offset = useOffset();
  const f = readFilters(params);
  const q = filtersQuery(f);
  usePageTitle(t("objects.title"));

  const schema = useSchema();
  const types = useMemo(
    () => (schema.data?.types ?? []).map((ty) => ({ name: ty.name, label: label(ty.label, ty.name) })).sort((a, b) => a.label.localeCompare(b.label)),
    [schema.data, label],
  );
  const sites = useQuery({
    queryKey: ["objects", "", "site", "sites"],
    queryFn: ({ signal }) => api.objects({ type: "site", sort: "display", limit: SITE_LIMIT }, signal),
    staleTime: 60_000,
  });
  const siteNames = useMemo(() => new Map((sites.data?.items ?? []).map((s) => [s.id, s.display])), [sites.data]);

  const list = useQuery({
    queryKey: ["objects", q, f.type, f.sort, offset],
    queryFn: ({ signal }) => api.objects({ q, type: f.type || undefined, sort: f.sort, limit: PAGE_SIZE, offset }, signal),
    placeholderData: (prev) => prev,
  });

  const apply = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const next = new URLSearchParams();
    const set = (key: string, v: FormDataEntryValue | null, skip: string) => {
      if (typeof v === "string" && v.trim() !== "" && v !== skip) next.set(key, v.trim());
    };
    set("q", form.get("q"), "");
    set("type", form.get("type"), "");
    set("site", form.get("site"), "");
    set("health", form.get("health"), "");
    set("presence", form.get("presence"), "active");
    set("sort", form.get("sort"), "display");
    setParams(next);
  };
  const filtered = f.text !== "" || f.type !== "" || f.site !== "" || f.health !== "" || f.presence !== "active";

  return (
    <>
      <h1>{t("objects.title")}</h1>
      <p className="lead">{t("objects.intro")}</p>
      {/* The key resets the uncontrolled fields when the address changes (back, a cleared filter), and once
          the sites have loaded: the site chosen in the address is only an option of the list from then on. */}
      <form className="filters" onSubmit={apply} key={`${params.toString()}|${sites.isSuccess}`} role="search" aria-label={t("objects.title")}>
        <label>
          {t("objects.text")}
          <input name="q" type="search" defaultValue={f.text} autoComplete="off" spellCheck={false} />
        </label>
        <label>
          {t("objects.type")}
          <select name="type" defaultValue={f.type}>
            <option value="">{t("objects.anyType")}</option>
            {types.map((ty) => (
              <option key={ty.name} value={ty.name}>
                {ty.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("objects.site")}
          <select name="site" defaultValue={f.site}>
            <option value="">{t("objects.anySite")}</option>
            {(sites.data?.items ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.display}
              </option>
            ))}
            {f.site && !siteNames.has(f.site) ? <option value={f.site}>{f.site}</option> : null}
          </select>
        </label>
        <label>
          {t("objects.health")}
          <select name="health" defaultValue={f.health}>
            <option value="">{t("filter.all")}</option>
            {HEALTH_FILTER.map((h) => (
              <option key={h} value={h}>
                {t(`healthState.${h}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("objects.status")}
          <select name="presence" defaultValue={f.presence}>
            <option value="active">{t("status.active")}</option>
            <option value="absent">{t("status.absent")}</option>
            <option value="all">{t("filter.all")}</option>
          </select>
        </label>
        <label>
          {t("objects.sort")}
          <select name="sort" defaultValue={f.sort}>
            {SORTS.map((s) => (
              <option key={s.value} value={s.value}>
                {t(s.label)}
              </option>
            ))}
          </select>
        </label>
        <button type="submit">{t("filter.apply")}</button>
        {filtered ? (
          <Link to="/objects" className="filters-reset">
            {t("objects.reset")}
          </Link>
        ) : null}
      </form>
      {list.isPending ? (
        <Loading />
      ) : list.isError ? (
        <ErrorBox
          error={list.error}
          title={list.error instanceof ApiError && list.error.status === 400 ? t("search.queryError") : undefined}
          onRetry={() => void list.refetch()}
        />
      ) : (
        <>
          <p className="summary" role="status">
            {tp("objects.summary", list.data.total, { count: number(list.data.total) })}
          </p>
          {list.data.items.length === 0 ? (
            <p>{t("objects.empty")}</p>
          ) : (
            <>
              <ObjectRows items={list.data.items} siteName={(id) => (id ? siteNames.get(id) : undefined)} />
              <Pager total={list.data.total} limit={list.data.limit} offset={list.data.offset} />
            </>
          )}
        </>
      )}
    </>
  );
}
