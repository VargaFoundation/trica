import { useQuery } from "@tanstack/react-query";
import type { FormEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { api } from "../api/endpoints";
import type { CollectorModule, CollectorStatus } from "../api/types";
import { Async } from "../components/Async";
import { useCrumbs } from "../components/crumbs";
import { Pager, PAGE_SIZE, useOffset } from "../components/Pager";
import { Empty, Time } from "../components/Time";
import { Card, Pill, Table } from "../components/ui";
import { useSchemaLookup } from "../hooks";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { Counts } from "./CollectorsPage";
import { STATUS_ORDER, STATUS_TONE, useWord } from "./status";

/** One collector module: what it says about itself, and its instances. Whatever the descriptor holds is shown; nothing is specific to a module. */
export function CollectorPage() {
  const { kind = "" } = useParams();
  const { t } = useI18n();
  usePageTitle(t("collectors.title"));
  const query = useQuery({
    queryKey: ["collector", kind],
    queryFn: ({ signal }) => api.collector(kind, signal),
  });
  return <Async query={query}>{(m) => <ModuleBody module={m} />}</Async>;
}

function ModuleBody({ module: m }: { module: CollectorModule }) {
  const { t, label } = useI18n();
  const word = useWord();
  const lookup = useSchemaLookup();
  usePageTitle(m.title);
  useCrumbs([{ label: t("nav.collectors"), to: "/collectors" }, { label: m.title }]);
  const isTarget = m.deployment === "target";

  return (
    <>
      <h1>{m.title}</h1>
      <p className="lead">{label(m.summary, "")}</p>
      <dl className="facts">
        <div>
          <dt>{t("collectors.col.vendors")}</dt>
          <dd>{m.vendors.join(", ")}</dd>
        </div>
        <div>
          <dt>{t("collectors.col.protocol")}</dt>
          <dd>{m.protocol}</dd>
        </div>
        <div>
          <dt>{t("collectors.col.deployment")}</dt>
          <dd>{word("deployment", m.deployment)}</dd>
        </div>
        <div>
          <dt>{t("collectors.col.instances")}</dt>
          <dd>
            <Counts module={m} />
          </dd>
        </div>
      </dl>

      <Card title={t("collectors.readOnly")} titleId="ro-h">
        <p>{m.read_only}</p>
      </Card>

      <Card title={t("collectors.collects")} titleId="collects-h">
        <ul className="inline-list">
          {m.collects.map((type) => (
            <li key={type}>
              <Link to={`/objects?type=${encodeURIComponent(type)}`}>{lookup.typeLabel(type)}</Link>
            </li>
          ))}
        </ul>
        <p>
          <Link to={`/search?q=${encodeURIComponent(`source:${m.kind}:*`)}`}>{t("collectors.objectsOf")}</Link>
        </p>
      </Card>

      <Card title={t("collectors.configuration")} titleId="config-h">
        {isTarget ? (
          <>
            <dl className="facts">
              <div>
                <dt>{t("collectors.address")}</dt>
                <dd>{m.address || <Empty />}</dd>
              </div>
              {m.credentials ? (
                <>
                  <div>
                    <dt>{t("collectors.username")}</dt>
                    <dd>
                      {word("use", m.credentials.username.use)}
                      {m.credentials.username.meaning ? <span className="muted"> · {m.credentials.username.meaning}</span> : null}
                    </dd>
                  </div>
                  <div>
                    <dt>{t("collectors.secret")}</dt>
                    <dd>
                      {word("use", m.credentials.secret.use)}
                      {m.credentials.secret.meaning ? <span className="muted"> · {m.credentials.secret.meaning}</span> : null}
                    </dd>
                  </div>
                </>
              ) : null}
            </dl>
            <h3>{t("collectors.options")}</h3>
            {m.options.length === 0 ? (
              <p>{t("collectors.noOptions")}</p>
            ) : (
              <Table>
                <caption className="sr-only">{t("collectors.options")}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t("collectors.opt.name")}</th>
                    <th scope="col">{t("collectors.opt.type")}</th>
                    <th scope="col">{t("collectors.opt.default")}</th>
                    <th scope="col">{t("collectors.opt.values")}</th>
                    <th scope="col">{t("collectors.opt.description")}</th>
                  </tr>
                </thead>
                <tbody>
                  {m.options.map((o) => (
                    <tr key={o.name}>
                      <th scope="row" data-label={t("collectors.opt.name")}>
                        <code>{o.name}</code>
                      </th>
                      <td data-label={t("collectors.opt.type")}>{word("optionType", o.type)}{o.value_kind ? <> ({word("optionType", o.value_kind)})</> : null}</td>
                      <td data-label={t("collectors.opt.default")}>{o.default ? <code>{o.default}</code> : <Empty />}</td>
                      <td data-label={t("collectors.opt.values")}>{o.values?.length ? o.values.join(", ") : <Empty />}</td>
                      <td data-label={t("collectors.opt.description")}>{o.description}</td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            )}
          </>
        ) : (
          <p>{t("collectors.hostNote")}</p>
        )}
      </Card>

      <Card title={t("collectors.tested")} titleId="tested-h">
        <ul className="inline-list">
          {m.tested.map((v) => (
            <li key={v}>{v}</li>
          ))}
        </ul>
        <p>
          <Pill tone={m.evidence === "synthesised" ? "warn" : "ok"} value={m.evidence}>
            {word("evidence", m.evidence)}
          </Pill>{" "}
          <a href={m.docs_url} target="_blank" rel="noreferrer noopener">
            {t("collectors.docsLink")}
          </a>{" "}
          <code className="muted">{m.docs}</code>
        </p>
      </Card>

      {isTarget || m.counts.instances > 0 ? <Instances module={m} /> : null}
    </>
  );
}

function Instances({ module: m }: { module: CollectorModule }) {
  const { t, tp, number } = useI18n();
  const word = useWord();
  const [params, setParams] = useSearchParams();
  const offset = useOffset();
  const status = params.get("status") ?? "";
  const query = useQuery({
    queryKey: ["collector-instances", m.kind, status, offset],
    queryFn: ({ signal }) =>
      api.collectorInstances(m.kind, { status: (status || undefined) as CollectorStatus | undefined, limit: PAGE_SIZE, offset }, signal),
    placeholderData: (prev) => prev,
    refetchInterval: 60_000,
  });

  const apply = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const s = new FormData(e.currentTarget).get("status");
    setParams(typeof s === "string" && s !== "" ? { status: s } : {});
  };

  return (
    <section aria-labelledby="instances-h">
      <h2 id="instances-h">{t("collectors.instances")}</h2>
      <form className="filters" onSubmit={apply}>
        <label>
          {t("collectors.filterStatus")}
          <select name="status" defaultValue={status}>
            <option value="">{t("filter.all")}</option>
            {STATUS_ORDER.map((s) => (
              <option key={s} value={s}>
                {word("status", s)}
              </option>
            ))}
          </select>
        </label>
        <button type="submit">{t("filter.apply")}</button>
      </form>
      <Async query={query}>
        {(p) =>
          p.items.length === 0 ? (
            <p>{t("collectors.noInstances")}</p>
          ) : (
            <>
              <p role="status" className="summary">
                {tp("collectors.instanceSummary", p.total)}
              </p>
              <Table>
                <caption className="sr-only">{t("collectors.instances")}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t("collectors.name")}</th>
                    <th scope="col">{t("health.state")}</th>
                    <th scope="col">{t("collectors.origin")}</th>
                    <th scope="col">{t("collectors.address")}</th>
                    <th scope="col">{t("health.lastAttempt")}</th>
                    <th scope="col">{t("health.items")}</th>
                  </tr>
                </thead>
                <tbody>
                  {p.items.map((i) => (
                    <tr key={i.source} data-status={i.status}>
                      <th scope="row" data-label={t("collectors.name")}>
                        <Link to={`/collectors/${encodeURIComponent(i.kind)}/${encodeURIComponent(i.name)}`}>{i.name}</Link>
                      </th>
                      <td data-label={t("health.state")}>
                        <Pill tone={STATUS_TONE[i.status] ?? "neutral"} value={i.status}>
                          {word("status", i.status)}
                        </Pill>
                        {i.health?.last_error ? <div className="muted">{i.health.last_error}</div> : null}
                      </td>
                      <td data-label={t("collectors.origin")}>{word("origin", i.origin)}</td>
                      <td data-label={t("collectors.address")}>{i.config?.address || <Empty />}</td>
                      <td data-label={t("health.lastAttempt")}>{i.health ? <Time iso={i.health.last_attempt} /> : t("health.never")}</td>
                      <td data-label={t("health.items")}>{i.health ? number(i.health.items) : <Empty />}</td>
                    </tr>
                  ))}
                </tbody>
              </Table>
              <Pager total={p.total} limit={p.limit} offset={p.offset} />
            </>
          )
        }
      </Async>
    </section>
  );
}
