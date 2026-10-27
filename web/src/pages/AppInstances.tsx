// An application's page: its instances (the component_instance objects under
// its components), grouped by the environment each is deployed in. The
// instance sits under a component, the component under the application, and
// the environment is the target of the instance's deployed_in relation.
import { useQueries, useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { Card, HealthPill, Table } from "../components/ui";
import { useI18n } from "../i18n";
import { healthOf } from "../inventory/health";
import { COMPONENTS_LIMIT, useChildren } from "./ObjectOverview";

const LINKS_LIMIT = 1000;

export interface InstanceRow {
  instance: ObjectView;
  component: ObjectView;
}

/** Instances grouped by environment name (the unknown ones last), each group sorted by component then name. */
export function groupByEnvironment(rows: InstanceRow[], envOf: Map<string, string>): [string, InstanceRow[]][] {
  const groups = new Map<string, InstanceRow[]>();
  for (const r of rows) {
    const env = envOf.get(r.instance.id) ?? "";
    groups.set(env, [...(groups.get(env) ?? []), r]);
  }
  const cmp = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true });
  return [...groups.entries()]
    .map(([env, rs]): [string, InstanceRow[]] => [env, [...rs].sort((a, b) => cmp(a.component.display, b.component.display) || cmp(a.instance.display, b.instance.display))])
    .sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : cmp(a, b)));
}

export function AppInstancesSection({ app }: { app: ObjectView }) {
  const { t } = useI18n();
  const comps = useChildren(app.id);
  const components = (comps.data?.items ?? []).filter((c) => c.type === "component");
  const perComponent = useQueries({
    queries: components.map((c) => ({
      queryKey: ["children", c.id],
      queryFn: ({ signal }: { signal: AbortSignal }) => api.search({ q: `parent:"${c.id}"`, limit: COMPONENTS_LIMIT }, signal),
      select: (page: { items: ObjectView[] }) => page.items.filter((o) => o.parent_id === c.id && o.type === "component_instance"),
    })),
  });
  const links = useQuery({
    queryKey: ["relation-links", "deployed_in"],
    queryFn: ({ signal }) => api.relationLinks({ type: "deployed_in", limit: LINKS_LIMIT }, signal),
    enabled: components.length > 0,
  });
  const rows: InstanceRow[] = components.flatMap((component, i) => (perComponent[i]?.data ?? []).map((instance) => ({ instance, component })));
  const envOf = new Map<string, string>();
  for (const l of links.data?.items ?? []) if (l.to.type === "environment") envOf.set(l.from_id, l.to.display);
  const loading = perComponent.some((q) => q.isLoading) || links.isLoading;
  return (
    <section aria-labelledby="instances-h">
      <h2 id="instances-h" className="sr-only">
        {t("instances.title")}
      </h2>
      <Async query={comps}>
        {() =>
          loading ? (
            <p role="status">{t("instances.loading")}</p>
          ) : rows.length === 0 ? (
            <p className="hint">{t("instances.empty")}</p>
          ) : (
            <>
              {groupByEnvironment(rows, envOf).map(([env, rs]) => {
                const name = env || t("instances.unknownEnvironment");
                return (
                  <Card key={env} level={3} titleId={`instances-${env || "none"}-h`} title={`${name} (${rs.length})`} className="component-group">
                    <Table data-environment={env}>
                      <caption className="sr-only">{t("instances.caption", { env: name, name: app.display })}</caption>
                      <thead>
                        <tr>
                          <th scope="col">{t("instances.component")}</th>
                          <th scope="col">{t("instances.instance")}</th>
                          <th scope="col">{t("health.label")}</th>
                          <th scope="col">{t("instances.version")}</th>
                          <th scope="col">{t("instances.sync")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {rs.map(({ instance, component }) => (
                          <tr key={instance.id} data-health={healthOf(instance)}>
                            <th scope="row" data-label={t("instances.component")}>
                              <ObjectLink id={component.id} display={component.display} />
                            </th>
                            <td data-label={t("instances.instance")}>
                              <ObjectLink id={instance.id} display={instance.display} />
                            </td>
                            <td data-label={t("health.label")}>
                              <HealthPill value={healthOf(instance)} />
                            </td>
                            <td data-label={t("instances.version")}>{String(instance.attributes.version ?? "")}</td>
                            <td data-label={t("instances.sync")}>{String(instance.attributes.sync_status ?? "")}</td>
                          </tr>
                        ))}
                      </tbody>
                    </Table>
                  </Card>
                );
              })}
            </>
          )
        }
      </Async>
    </section>
  );
}
