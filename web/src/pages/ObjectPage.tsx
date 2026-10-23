import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useParams } from "react-router";
import { api } from "../api/endpoints";
import type { Change, ObjectView, Provenance, ProvenanceEntry, RelationView } from "../api/types";
import { Async, ErrorBox } from "../components/Async";
import { Confidence, Status } from "../components/Badge";
import { ObjectLink } from "../components/ObjectLink";
import { Time } from "../components/Time";
import { formatValue, Value } from "../components/Value";
import { useCrumbs } from "../components/crumbs";
import { Quantity } from "../components/Quantity";
import { ButtonLink, Card, HealthPill, Icon, KindIcon, Table, TabPanel, Tabs, useKindLabel, useTabParam, type TabDef } from "../components/ui";
import { healthOf } from "../inventory/health";
import { ComponentsSection, OverviewSection, useChildren } from "./ObjectOverview";
import { useSchemaLookup, type SchemaLookup } from "../hooks";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { NotFoundPage } from "./NotFoundPage";
import { ApiError } from "../api/client";
import { Measurements } from "../charts/Measurements";
import { FirmwareSection } from "../firmware/FirmwareCard";
import { DeviceFacesSection } from "../faces/DeviceFaces";
import { ModelChoiceSection, VariantPill } from "../devicetypes/ModelChoice";
import { DeviceLocation, LocatedHere } from "../edit/DeviceLocation";
import { DevicePlacement } from "../edit/DevicePlacement";
import { AirflowSection } from "../aisles/Airflow";
import { EditPanel, useCanDeclare } from "../edit/EditPanel";
import { ObjectForm } from "../edit/ObjectForm";
import { CONTAINER_TYPES } from "../edit/rules";
import { useFloorContext, useRoomOf } from "../edit/useFloor";

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

/** Attributes put forward in the header when the object has them (names of the schema's device type). */
const KEY_FACTS = ["vendor", "model", "serial", "mgmt_ip", "primary_ip", "uptime_s", "power_w", "inlet_temp_c"] as const;

const TABS = ["overview", "components", "relations", "history", "provenance"] as const;

function ObjectDetail({ obj }: { obj: ObjectView }) {
  const { t, number } = useI18n();
  const lookup = useSchemaLookup();
  const kindLabel = useKindLabel();
  usePageTitle(obj.display);
  const { kind, label, typeLabel } = kindLabel(obj);
  const [tab, setTab] = useTabParam(TABS, "overview");
  const relations = useRelations(obj.id);
  const children = useChildren(obj.id);
  const site = useObjectName(obj.site_id);
  const parent = useObjectName(obj.parent_id !== obj.site_id ? obj.parent_id : undefined);
  const mount = relations.data?.items.find((r) => r.type === "mounted_in" && r.direction === "out");
  // A device in no rack stands where a person placed it (ADR 0058); a rack wins.
  const placed = mount ? undefined : relations.data?.items.find((r) => r.type === "located_in" && r.direction === "out");
  const hasHealth = "health" in obj.attributes || lookup.attribute(obj.type, "health") !== undefined;

  useCrumbs([
    { label: t("nav.overview"), to: "/" },
    ...(obj.site_id && obj.site_id !== obj.id ? [{ label: site ?? "…", to: `/objects/${encodeURIComponent(obj.site_id)}` }] : []),
    ...(obj.parent_id && obj.parent_id !== obj.site_id ? [{ label: parent ?? "…", to: `/objects/${encodeURIComponent(obj.parent_id)}` }] : []),
    { label: obj.display },
  ]);

  const tabs: TabDef[] = [
    { id: "overview", label: t("tab.overview") },
    { id: "components", label: t("tab.components"), count: children.data?.items.length },
    { id: "relations", label: t("tab.relations"), count: relations.data?.total },
    { id: "history", label: t("tab.history") },
    { id: "provenance", label: t("tab.provenance") },
  ];
  const a = obj.attributes;
  const os = [a.os_name, a.os_version].filter((v) => typeof v === "string" && v !== "").join(" ");

  return (
    <>
      <Card as="div" className="object-card">
        <div className="object-head">
          <KindIcon kind={kind} size="lg" />
          <div className="object-head-text">
            <h1>{obj.display}</h1>
            <div className="object-head-pills">
              <span className="kind-label" data-kind={kind.key}>
                {label}
                {label !== typeLabel ? <span className="muted"> · {typeLabel}</span> : null}
              </span>
              {hasHealth ? <HealthPill value={healthOf(obj)} /> : null}
              <Status value={obj.status} />
              {obj.type === "device" ? <VariantPill obj={obj} /> : null}
            </div>
          </div>
          <div className="object-head-actions">
            {obj.type === "rack" ? (
              <ButtonLink to={`/racks/${encodeURIComponent(obj.id)}`}>
                <Icon name="rack" />
                {t("object.openElevation")}
              </ButtonLink>
            ) : null}
            {obj.type === "room" ? (
              <ButtonLink to={`/rooms/${encodeURIComponent(obj.id)}`}>
                <Icon name="room" />
                {t("object.openFloorPlan")}
              </ButtonLink>
            ) : null}
          </div>
        </div>
        <dl className="facts" aria-label={t("object.facts")}>
          {KEY_FACTS.filter((k) => a[k] !== undefined && a[k] !== null && a[k] !== "").map((k) => (
            <div key={k} data-fact={k}>
              <dt>{lookup.attributeLabel(obj.type, k)}</dt>
              <dd className={k === "serial" || k.endsWith("_ip") ? "mono" : undefined}>
                <Quantity value={a[k]} unit={lookup.attribute(obj.type, k)?.unit} />
              </dd>
            </div>
          ))}
          {os ? (
            <div data-fact="os">
              <dt>{lookup.attributeLabel(obj.type, a.os_name ? "os_name" : "os_version")}</dt>
              <dd>{os}</dd>
            </div>
          ) : null}
          {mount ? (
            <div data-fact="location">
              <dt>{t("object.location")}</dt>
              <dd>
                <ObjectLink id={mount.other.id} display={mount.other.display} />
                {typeof mount.attributes?.position === "number" ? (
                  <span className="muted">
                    {" "}
                    {t("object.position", {
                      position: number(mount.attributes.position),
                      face: mount.attributes.face === "rear" ? t("face.rear") : mount.attributes.face === "front" ? t("face.front") : String(mount.attributes.face ?? "").trim() || "–",
                    })}
                  </span>
                ) : null}
              </dd>
            </div>
          ) : null}
          {placed ? (
            <div data-fact="location">
              <dt>{t("object.location")}</dt>
              <dd>
                <ObjectLink id={placed.other.id} display={placed.other.display} />
              </dd>
            </div>
          ) : null}
          <div>
            <dt>{t("object.type")}</dt>
            <dd>{typeLabel}</dd>
          </div>
          {obj.site_id && obj.site_id !== obj.id ? (
            <div>
              <dt>{t("object.site")}</dt>
              <dd>
                <ObjectLink id={obj.site_id} />
              </dd>
            </div>
          ) : null}
          {obj.parent_id && obj.parent_id !== mount?.other.id ? (
            <div>
              <dt>{t("object.parent")}</dt>
              <dd>
                <ObjectLink id={obj.parent_id} />
              </dd>
            </div>
          ) : null}
          <div>
            <dt>{t("object.lastSeen")}</dt>
            <dd>
              <Time iso={obj.last_seen} />
            </dd>
          </div>
          <div>
            <dt>{t("object.firstSeen")}</dt>
            <dd>
              <Time iso={obj.first_seen} />
            </dd>
          </div>
          <div>
            <dt>{t("object.observed")}</dt>
            <dd>{obj.observed ? t("app.yes") : t("object.notObserved")}</dd>
          </div>
          <div>
            <dt>{t("object.id")}</dt>
            <dd>
              <code>{obj.id}</code>
            </dd>
          </div>
        </dl>
      </Card>
      <Tabs tabs={tabs} active={tab} onSelect={setTab} label={t("object.sections", { name: obj.display })} idPrefix="object" />
      <TabPanel idPrefix="object" id={tab}>
        {tab === "overview" ? (
          <>
            {CONTAINER_TYPES.includes(obj.type) ? <EditContainer obj={obj} /> : null}
            <OverviewSection obj={obj} lookup={lookup} onProvenance={() => setTab("provenance")} />
            <ModelChoiceSection obj={obj} />
            <DeviceFacesSection obj={obj} lookup={lookup} />
            <Measurements obj={obj} lookup={lookup} />
            <FirmwareSection obj={obj} lookup={lookup} />
            {obj.type === "device" ? <DevicePlacement device={obj} /> : null}
            {obj.type === "device" ? <DeviceLocation device={obj} /> : null}
            {obj.type === "site" || obj.type === "building" || obj.type === "room" ? <LocatedHere obj={obj} /> : null}
            {obj.type === "device" ? <AirflowSection obj={obj} /> : null}
          </>
        ) : tab === "components" ? (
          <ComponentsSection obj={obj} lookup={lookup} />
        ) : tab === "relations" ? (
          <RelationsSection id={obj.id} lookup={lookup} />
        ) : tab === "history" ? (
          <TimelineSection id={obj.id} lookup={lookup} />
        ) : (
          <AttributesSection obj={obj} lookup={lookup} />
        )}
      </TabPanel>
    </>
  );
}

function useRelations(id: string) {
  return useQuery({
    queryKey: ["relations", id],
    queryFn: ({ signal }) => api.relations(id, { limit: 200 }, signal),
  });
}

function useObjectName(id: string | undefined): string | undefined {
  const q = useQuery({
    queryKey: ["object", id],
    queryFn: ({ signal }) => api.object(id as string, signal),
    enabled: !!id,
    staleTime: 5 * 60_000,
  });
  return q.data?.display;
}

/** "Edit" on a site, building, room, row or rack: rename it, set a rack's height or floor tile. */
function EditContainer({ obj }: { obj: ObjectView }) {
  const { t } = useI18n();
  const can = useCanDeclare();
  const [open, setOpen] = useState(false);
  const floor = useFloorContext(useRoomOf(open && obj.type === "rack" ? obj : undefined));
  if (!can) return null;
  if (!open)
    return (
      <p className="actions">
        <button type="button" onClick={() => setOpen(true)}>
          {t("edit.object.button")}
        </button>
      </p>
    );
  return (
    <EditPanel title={t("edit.object.title", { name: obj.display })}>
      <ObjectForm mode="edit" object={obj} floor={floor} onClose={() => setOpen(false)} />
    </EditPanel>
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
            <Table className="attributes">
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
            </Table>
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
        <Quantity value={value} unit={doc?.unit} />
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
  const query = useRelations(id);
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
                    <Table>
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
                    </Table>
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
