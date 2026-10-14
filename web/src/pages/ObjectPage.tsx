import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { api } from "../api/endpoints";
import type { Change, ObjectView, Provenance, ProvenanceEntry, RelationView } from "../api/types";
import { Async, ErrorBox } from "../components/Async";
import { Confidence, Status } from "../components/Badge";
import { ObjectLink } from "../components/ObjectLink";
import { Time } from "../components/Time";
import { formatValue, Value } from "../components/Value";
import { useSchemaLookup, type SchemaLookup } from "../hooks";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { NotFoundPage } from "./NotFoundPage";
import { ApiError } from "../api/client";

export function ObjectPage() {
  const { id = "" } = useParams();
  const query = useQuery({
    queryKey: ["object", id],
    queryFn: ({ signal }) => api.object(id, signal),
    staleTime: 5 * 60_000,
  });
  if (query.isError && query.error instanceof ApiError && query.error.status === 404) return <NotFoundPage />;
  return <Async query={query}>{(o) => <ObjectDetail obj={o} />}</Async>;
}

function ObjectDetail({ obj }: { obj: ObjectView }) {
  const { t } = useI18n();
  const lookup = useSchemaLookup();
  usePageTitle(obj.display);
  return (
    <>
      <h1>{obj.display}</h1>
      {obj.type === "rack" ? (
        <p>
          <Link to={`/racks/${encodeURIComponent(obj.id)}`}>{t("object.openElevation")}</Link>
        </p>
      ) : null}
      {obj.type === "room" ? (
        <p>
          <Link to={`/rooms/${encodeURIComponent(obj.id)}`}>{t("object.openFloorPlan")}</Link>
        </p>
      ) : null}
      <dl className="facts">
        <div>
          <dt>{t("object.type")}</dt>
          <dd>{lookup.typeLabel(obj.type)}</dd>
        </div>
        <div>
          <dt>{t("object.status")}</dt>
          <dd>
            <Status value={obj.status} />
          </dd>
        </div>
        <div>
          <dt>{t("object.id")}</dt>
          <dd>
            <code>{obj.id}</code>
          </dd>
        </div>
        {obj.site_id ? (
          <div>
            <dt>{t("object.site")}</dt>
            <dd>
              <ObjectLink id={obj.site_id} />
            </dd>
          </div>
        ) : null}
        {obj.parent_id ? (
          <div>
            <dt>{t("object.parent")}</dt>
            <dd>
              <ObjectLink id={obj.parent_id} />
            </dd>
          </div>
        ) : null}
        <div>
          <dt>{t("object.firstSeen")}</dt>
          <dd>
            <Time iso={obj.first_seen} />
          </dd>
        </div>
        <div>
          <dt>{t("object.lastSeen")}</dt>
          <dd>
            <Time iso={obj.last_seen} />
          </dd>
        </div>
        <div>
          <dt>{t("object.createdAt")}</dt>
          <dd>
            <Time iso={obj.created_at} />
          </dd>
        </div>
        <div>
          <dt>{t("object.observed")}</dt>
          <dd>{obj.observed ? t("app.yes") : t("object.notObserved")}</dd>
        </div>
      </dl>
      <AttributesSection obj={obj} lookup={lookup} />
      <RelationsSection id={obj.id} lookup={lookup} />
      <TimelineSection id={obj.id} lookup={lookup} />
    </>
  );
}

/* ---------------------------------------------------------------- attributes */

function attributeKeys(obj: ObjectView, prov: Provenance, lookup: SchemaLookup): string[] {
  const keys = new Set<string>();
  for (const k of Object.keys(obj.attributes)) keys.add(k);
  for (const e of prov.attributes) keys.add(e.key);
  const visible = [...keys].filter((k) => !k.startsWith("_"));
  const order = new Map((lookup.typeDoc(obj.type)?.attributes ?? []).map((a, i) => [a.name, i]));
  return visible.sort((a, b) => (order.get(a) ?? 1e6) - (order.get(b) ?? 1e6) || a.localeCompare(b));
}

function AttributesSection({ obj, lookup }: { obj: ObjectView; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["provenance", obj.id],
    queryFn: ({ signal }) => api.provenance(obj.id, signal),
  });
  return (
    <section aria-labelledby="attrs-h">
      <h2 id="attrs-h">{t("attrs.title")}</h2>
      <p className="muted">{t("confidence.help")}</p>
      <Async query={query}>
        {(prov) => {
          const keys = attributeKeys(obj, prov, lookup);
          if (keys.length === 0) return <p>{t("attrs.empty")}</p>;
          const byKey = new Map(prov.attributes.map((e) => [e.key, e]));
          return (
            <table className="attributes">
              <caption className="sr-only">{t("attrs.caption", { name: obj.display })}</caption>
              <thead>
                <tr>
                  <th scope="col">{t("attrs.attribute")}</th>
                  <th scope="col">{t("attrs.value")}</th>
                  <th scope="col">{t("attrs.source")}</th>
                  <th scope="col">{t("attrs.confidence")}</th>
                  <th scope="col">{t("attrs.firstSeen")}</th>
                  <th scope="col">{t("attrs.lastSeen")}</th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <AttributeRow key={k} name={k} obj={obj} entry={byKey.get(k)} lookup={lookup} />
                ))}
              </tbody>
            </table>
          );
        }}
      </Async>
    </section>
  );
}

function AttributeRow({ name, obj, entry, lookup }: { name: string; obj: ObjectView; entry: ProvenanceEntry | undefined; lookup: SchemaLookup }) {
  const { t, tp } = useI18n();
  const doc = lookup.attribute(obj.type, name);
  const label = lookup.attributeLabel(obj.type, name);
  const value = name in obj.attributes ? obj.attributes[name] : entry?.value;
  const others = entry?.other_claims ?? [];
  return (
    <tr data-attribute={name} data-has-provenance={entry ? "true" : "false"}>
      <th scope="row" data-label={t("attrs.attribute")}>
        {label}
        {label !== name ? <code className="key">{name}</code> : null}
      </th>
      <td data-label={t("attrs.value")}>
        <Value value={value} unit={doc?.unit} />
        {others.length > 0 ? (
          <details className="claims">
            <summary>{tp("attrs.otherClaims", others.length)}</summary>
            <ul>
              {others.map((c, i) => (
                <li key={`${c.source}-${i}`}>
                  <strong>{c.source}</strong>: <Value value={c.value} unit={doc?.unit} /> <Confidence value={c.confidence} />{" "}
                  <span className="muted">
                    <Time iso={c.first_seen} /> – <Time iso={c.last_seen} />
                  </span>
                </li>
              ))}
            </ul>
          </details>
        ) : null}
      </td>
      {entry ? (
        <>
          <td data-label={t("attrs.source")}>
            {entry.source}
            {entry.author ? <span className="muted"> ({entry.author})</span> : null}
          </td>
          <td data-label={t("attrs.confidence")}>
            <Confidence value={entry.confidence} />
          </td>
          <td data-label={t("attrs.firstSeen")}>
            <Time iso={entry.first_seen ?? entry.valid_from} />
          </td>
          <td data-label={t("attrs.lastSeen")}>
            <Time iso={entry.last_seen} />
          </td>
        </>
      ) : (
        <td colSpan={4} className="warn" data-label={t("attrs.source")}>
          {t("attrs.noProvenance")}
        </td>
      )}
    </tr>
  );
}

/* ----------------------------------------------------------------- relations */

function RelationsSection({ id, lookup }: { id: string; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["relations", id],
    queryFn: ({ signal }) => api.relations(id, { limit: 200 }, signal),
  });
  return (
    <section aria-labelledby="rel-h">
      <h2 id="rel-h">{t("relations.title")}</h2>
      <Async query={query}>
        {(page) => {
          if (page.items.length === 0) return <p>{t("relations.empty")}</p>;
          const groups = new Map<string, RelationView[]>();
          for (const r of page.items) {
            const k = `${r.direction}:${r.type}`;
            groups.set(k, [...(groups.get(k) ?? []), r]);
          }
          return (
            <>
              {[...groups.entries()].map(([k, rels]) => {
                const first = rels[0];
                if (!first) return null;
                const heading = lookup.relationLabel(first.type, first.direction);
                return (
                  <div key={k} className="relation-group" data-relation={first.type} data-direction={first.direction}>
                    <h3>{heading}</h3>
                    <table>
                      <caption className="sr-only">{heading}</caption>
                      <thead>
                        <tr>
                          <th scope="col">{t("relations.object")}</th>
                          <th scope="col">{t("relations.type")}</th>
                          <th scope="col">{t("relations.source")}</th>
                          <th scope="col">{t("relations.confidence")}</th>
                          <th scope="col">{t("relations.since")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {rels.map((r) => (
                          <tr key={r.id}>
                            <td data-label={t("relations.object")}>
                              <ObjectLink id={r.other.id} display={r.other.display} />
                            </td>
                            <td data-label={t("relations.type")}>{lookup.typeLabel(r.other.type)}</td>
                            <td data-label={t("relations.source")}>{r.source}</td>
                            <td data-label={t("relations.confidence")}>
                              <Confidence value={r.confidence} />
                            </td>
                            <td data-label={t("relations.since")}>
                              <Time iso={r.valid_from} />
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                );
              })}
              {page.total > page.items.length ? <p className="muted">{page.items.length} / {page.total}</p> : null}
            </>
          );
        }}
      </Async>
    </section>
  );
}

/* ------------------------------------------------------------------ timeline */

const TIMELINE_PAGE = 50;

function Sentence({ c, lookup }: { c: Change; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const yes = t("app.yes");
  const no = t("app.no");
  const f = (v: unknown) => formatValue(v, yes, no) || t("app.none");
  const hasBefore = c.before !== undefined && c.before !== null;
  const hasAfter = c.after !== undefined && c.after !== null;

  if (c.relation) {
    const relation = lookup.relationLabel(c.relation, "out");
    const key = hasAfter && !hasBefore ? "timeline.relationAdded" : hasBefore && !hasAfter ? "timeline.relationRemoved" : "timeline.relationChanged";
    return (
      <>
        {t(key, { relation })} {c.other_id ? <> {t("timeline.with")} <ObjectLink id={c.other_id} /></> : null}
      </>
    );
  }
  const key = c.key ?? "";
  const name = lookup.attributeLabel(c.type, key);
  if (hasBefore && hasAfter) return <>{t("timeline.set", { key: name, before: f(c.before), after: f(c.after) })}</>;
  if (hasAfter) return <>{t("timeline.added", { key: name, after: f(c.after) })}</>;
  return <>{t("timeline.removed", { key: name, before: f(c.before) })}</>;
}

function TimelineSection({ id, lookup }: { id: string; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const query = useInfiniteQuery({
    queryKey: ["timeline", id],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => api.timeline(id, { limit: TIMELINE_PAGE, offset: pageParam }, signal),
    getNextPageParam: (last) => (last.offset + last.items.length < last.total ? last.offset + last.items.length : undefined),
  });
  return (
    <section aria-labelledby="tl-h">
      <h2 id="tl-h">{t("timeline.title")}</h2>
      {query.isPending ? (
        <p role="status">{t("app.loading")}</p>
      ) : query.isError ? (
        <ErrorBox error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          {query.data.pages.every((p) => p.items.length === 0) ? <p>{t("timeline.empty")}</p> : null}
          <ol className="timeline">
            {query.data.pages.flatMap((p) =>
              p.items.map((c, i) => (
                <li key={`${c.changeset_id}-${c.key ?? c.relation}-${p.offset}-${i}`}>
                  <Time iso={c.at} /> <Sentence c={c} lookup={lookup} />{" "}
                  <span className="muted">{t("timeline.by", { source: c.source })}</span>
                </li>
              )),
            )}
          </ol>
          {query.hasNextPage ? (
            <button type="button" onClick={() => void query.fetchNextPage()} disabled={query.isFetchingNextPage}>
              {t("timeline.more")}
            </button>
          ) : null}
        </>
      )}
    </section>
  );
}
