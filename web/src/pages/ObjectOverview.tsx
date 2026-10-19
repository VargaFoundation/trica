// The object page's Overview and Components tabs: the attributes grouped by
// what the schema says they are, and what the object contains (power
// supplies, fans, disks, memory, processors, modules, interfaces…), each kind
// in its own compact table with its health. Grouping and columns come from the
// schema (kind, unit, identity, the order of the type's attributes), never from
// a list of attribute names.
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { AttributeDoc, ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { Quantity } from "../components/Quantity";
import { Card, EmptyState, HealthPill, KindIcon, Table, useKindLabel } from "../components/ui";
import { Time } from "../components/Time";
import { useSchema, type SchemaLookup } from "../hooks";
import { useI18n, type MessageKey } from "../i18n";
import { HEALTHS, healthOf, SEVERITY } from "../inventory/health";
import { kindOf } from "../inventory/kind";
import { isNameVersionList, versionRecords } from "../firmware/versions";

export const COMPONENTS_LIMIT = 1000;

/** The objects whose parent is this one: a device's power supplies, fans, disks, interfaces… */
export function useChildren(id: string) {
  return useQuery({
    queryKey: ["children", id],
    queryFn: ({ signal }) => api.search({ q: `parent:"${id}"`, limit: COMPONENTS_LIMIT }, signal),
    // The search matches a parent by id or by name: keep only what really sits in this object.
    select: (page) => ({ ...page, items: page.items.filter((o) => o.parent_id === id) }),
  });
}

export type AttrGroup = "identity" | "measurements" | "network" | "state" | "dates" | "details" | "lists";
const GROUP_ORDER: AttrGroup[] = ["identity", "state", "measurements", "network", "details", "dates", "lists"];

/** Which group an attribute belongs to, from its schema description alone. */
export function groupOf(doc: AttributeDoc | undefined, value: unknown): AttrGroup {
  if (!doc) return Array.isArray(value) ? "lists" : typeof value === "number" ? "measurements" : "details";
  if (doc.identity === "strong" || doc.identity === "weak") return "identity";
  switch (doc.kind) {
    case "ip":
    case "mac":
    case "prefix":
      return "network";
    case "enum":
    case "bool":
      return "state";
    case "time":
      return "dates";
    case "list":
    case "json":
      return "lists";
    case "int":
    case "float":
    case "number":
      return "measurements";
    default:
      return doc.unit ? "measurements" : "details";
  }
}

/** An enum whose values are health states is shown as a health pill. */
export function isHealthEnum(doc: AttributeDoc | undefined): boolean {
  return !!doc && doc.kind === "enum" && !!doc.values?.length && doc.values.every((v) => (HEALTHS as readonly string[]).includes(v));
}

function AttrValue({ type, name, value, lookup }: { type: string; name: string; value: unknown; lookup: SchemaLookup }) {
  const doc = lookup.attribute(type, name);
  if (isHealthEnum(doc) && typeof value === "string") return <HealthPill value={value} />;
  if (doc?.kind === "time" && typeof value === "string") return <Time iso={value} />;
  return <Quantity value={value} unit={doc?.unit} />;
}

export function OverviewSection({ obj, lookup, onProvenance }: { obj: ObjectView; lookup: SchemaLookup; onProvenance: () => void }) {
  const { t } = useI18n();
  const order = new Map((lookup.typeDoc(obj.type)?.attributes ?? []).map((a, i) => [a.name, i]));
  // Version records have their own table (FirmwareSection), and the
  // "name=version" list that makes them searchable says the same thing.
  const tabled = Object.values(obj.attributes).some((v) => versionRecords(v) !== undefined);
  const keys = Object.keys(obj.attributes)
    .filter((k) => !k.startsWith("_"))
    .filter((k) => !tabled || (versionRecords(obj.attributes[k]) === undefined && !isNameVersionList(obj.attributes[k])))
    .sort((a, b) => (order.get(a) ?? 1e6) - (order.get(b) ?? 1e6) || a.localeCompare(b));
  const groups = new Map<AttrGroup, string[]>();
  for (const k of keys) {
    const g = groupOf(lookup.attribute(obj.type, k), obj.attributes[k]);
    groups.set(g, [...(groups.get(g) ?? []), k]);
  }
  return (
    <section aria-labelledby="overview-h">
      <h2 id="overview-h" className="sr-only">
        {t("tab.overview")}
      </h2>
      {keys.length === 0 ? (
        <EmptyState icon="document">{t("attrs.empty")}</EmptyState>
      ) : (
        <div className="attr-groups">
          {GROUP_ORDER.filter((g) => groups.has(g)).map((g) => (
            <Card key={g} title={t(`group.${g}` as MessageKey)} titleId={`group-${g}-h`} level={3}>
              <dl className="attr-list" data-group={g}>
                {(groups.get(g) ?? []).map((k) => {
                  const label = lookup.attributeLabel(obj.type, k);
                  return (
                    <div key={k} data-attribute={k}>
                      <dt>{label}</dt>
                      <dd>
                        <AttrValue type={obj.type} name={k} value={obj.attributes[k]} lookup={lookup} />
                      </dd>
                    </div>
                  );
                })}
              </dl>
            </Card>
          ))}
        </div>
      )}
      <p className="hint">
        {t("overview.provenanceHint")}{" "}
        <button type="button" className="ghost" onClick={onProvenance}>
          {t("tab.provenance")}
        </button>
      </p>
    </section>
  );
}

/** At most this many attribute columns per component table, after the name and the health. */
const MAX_COLUMNS = 6;

/** The columns of a component table: the type's attributes in schema order that at least one component has. */
export function columnsOf(items: ObjectView[], docs: AttributeDoc[] | undefined): string[] {
  const present = new Set(items.flatMap((o) => Object.keys(o.attributes)));
  const ordered = (docs ?? []).map((d) => d.name).filter((n) => present.has(n));
  const extra = [...present].filter((n) => !ordered.includes(n)).sort();
  return [...ordered, ...extra].filter((n) => n !== "name" && n !== "health" && !n.startsWith("_")).slice(0, MAX_COLUMNS);
}

export function ComponentsSection({ obj, lookup }: { obj: ObjectView; lookup: SchemaLookup }) {
  const { t, tp, number } = useI18n();
  const kindLabel = useKindLabel();
  const schema = useSchema().data;
  const typeOrder = new Map((schema?.types ?? []).map((ty, i) => [ty.name, i]));
  const query = useChildren(obj.id);
  return (
    <section aria-labelledby="components-h">
      <h2 id="components-h" className="sr-only">
        {t("components.title")}
      </h2>
      <Async query={query}>
        {(page) => {
          if (page.items.length === 0) return <EmptyState icon="module">{t("components.empty")}</EmptyState>;
          const byType = new Map<string, ObjectView[]>();
          for (const o of page.items) byType.set(o.type, [...(byType.get(o.type) ?? []), o]);
          const types = [...byType.keys()].sort((a, b) => (typeOrder.get(a) ?? 1e6) - (typeOrder.get(b) ?? 1e6) || a.localeCompare(b));
          const bad = page.items.filter((o) => {
            const h = healthOf(o);
            return h === "critical" || h === "warning";
          }).length;
          return (
            <>
              <p role="status" className="summary">
                {tp("components.summary", page.items.length, { bad: number(bad) })}
              </p>
              {page.total > page.items.length && page.items.length >= COMPONENTS_LIMIT ? (
                <p className="notice">{t("components.truncated", { count: number(page.items.length) })}</p>
              ) : null}
              {types.map((type) => {
                const items = [...(byType.get(type) ?? [])].sort(
                  (a, b) => SEVERITY[healthOf(a)] - SEVERITY[healthOf(b)] || a.display.localeCompare(b.display, undefined, { numeric: true }),
                );
                const docs = lookup.typeDoc(type)?.attributes;
                const cols = columnsOf(items, docs);
                const withHealth = items.some((o) => "health" in o.attributes) || !!docs?.some((d) => d.name === "health");
                const label = lookup.typeLabel(type);
                const kind = kindOf({ type }, lookup.typeDoc(type)?.layer);
                return (
                  <Card
                    key={type}
                    level={3}
                    titleId={`components-${type}-h`}
                    title={
                      <span className="kind-label">
                        <KindIcon kind={kind} /> {label} <span className="count">{number(items.length)}</span>
                      </span>
                    }
                    className="component-group"
                  >
                    <Table data-type={type}>
                      <caption className="sr-only">{t("components.caption", { type: label, name: obj.display })}</caption>
                      <thead>
                        <tr>
                          <th scope="col">{t("components.name")}</th>
                          {withHealth ? <th scope="col">{t("health.label")}</th> : null}
                          {cols.map((c) => (
                            <th key={c} scope="col">
                              {lookup.attributeLabel(type, c)}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {items.map((o) => (
                          <tr key={o.id} data-health={healthOf(o)}>
                            <th scope="row" data-label={t("components.name")}>
                              <ObjectLink id={o.id} display={o.display} />
                              {kindLabel(o).kind.role ? <span className="key">{kindLabel(o).label}</span> : null}
                            </th>
                            {withHealth ? (
                              <td data-label={t("health.label")}>
                                <HealthPill value={healthOf(o)} />
                              </td>
                            ) : null}
                            {cols.map((c) => (
                              <td key={c} data-label={lookup.attributeLabel(type, c)}>
                                <AttrValue type={type} name={c} value={o.attributes[c]} lookup={lookup} />
                              </td>
                            ))}
                          </tr>
                        ))}
                      </tbody>
                    </Table>
                  </Card>
                );
              })}
            </>
          );
        }}
      </Async>
    </section>
  );
}
