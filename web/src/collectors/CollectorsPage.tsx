import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api } from "../api/endpoints";
import type { CollectorModule } from "../api/types";
import { Async } from "../components/Async";
import { Empty, Time } from "../components/Time";
import { Pill, Table } from "../components/ui";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { STATUS_ORDER, STATUS_TONE, useWord } from "./status";

/** The instances of a module by status: "2 OK", "1 Failing"; or that none is configured. */
export function Counts({ module }: { module: CollectorModule }) {
  const { t, number } = useI18n();
  const word = useWord();
  const c = module.counts;
  if (c.instances === 0) return <span className="muted">{t("collectors.notConfigured")}</span>;
  return (
    <span className="pills">
      {STATUS_ORDER.filter((s) => c[s] > 0).map((s) => (
        <Pill key={s} tone={STATUS_TONE[s]} value={s}>
          {number(c[s])} {word("status", s)}
        </Pill>
      ))}
    </span>
  );
}

/** Every collector module this server knows, what it is and how many instances run; nothing here names a module. */
export function CollectorsPage() {
  const { t, tp, label } = useI18n();
  const word = useWord();
  usePageTitle(t("collectors.title"));
  const query = useQuery({
    queryKey: ["collectors"],
    queryFn: ({ signal }) => api.collectors(signal),
    refetchInterval: 60_000,
  });

  return (
    <>
      <h1>{t("collectors.title")}</h1>
      <p className="lead">{t("collectors.intro")}</p>
      <Async query={query}>
        {(cat) => (
          <>
            <p role="status" className="summary">
              {tp("collectors.summary", cat.modules.length)}
            </p>
            <Table>
              <caption className="sr-only">{t("collectors.title")}</caption>
              <thead>
                <tr>
                  <th scope="col">{t("collectors.col.collector")}</th>
                  <th scope="col">{t("collectors.col.vendors")}</th>
                  <th scope="col">{t("collectors.col.protocol")}</th>
                  <th scope="col">{t("collectors.col.deployment")}</th>
                  <th scope="col">{t("collectors.col.instances")}</th>
                  <th scope="col">{t("collectors.col.evidence")}</th>
                </tr>
              </thead>
              <tbody>
                {cat.modules.map((m) => (
                  <tr key={m.kind}>
                    <th scope="row" data-label={t("collectors.col.collector")}>
                      <Link to={`/collectors/${encodeURIComponent(m.kind)}`}>{m.title}</Link> <code className="muted">{m.kind}</code>
                      <div className="muted">{label(m.summary, "")}</div>
                    </th>
                    <td data-label={t("collectors.col.vendors")}>{m.vendors.join(", ")}</td>
                    <td data-label={t("collectors.col.protocol")}>{m.protocol}</td>
                    <td data-label={t("collectors.col.deployment")}>{word("deployment", m.deployment)}</td>
                    <td data-label={t("collectors.col.instances")}>
                      <Counts module={m} />
                    </td>
                    <td data-label={t("collectors.col.evidence")}>
                      <Pill tone={m.evidence === "synthesised" ? "warn" : "ok"} value={m.evidence}>
                        {word("evidence", m.evidence)}
                      </Pill>
                    </td>
                  </tr>
                ))}
              </tbody>
            </Table>

            {cat.unregistered.length > 0 ? (
              <section aria-labelledby="unregistered-h">
                <h2 id="unregistered-h">{t("collectors.unregistered")}</h2>
                <p>{t("collectors.unregisteredIntro")}</p>
                <Table>
                  <caption className="sr-only">{t("collectors.unregistered")}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t("health.source")}</th>
                      <th scope="col">{t("health.state")}</th>
                      <th scope="col">{t("health.collector")}</th>
                      <th scope="col">{t("health.lastAttempt")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {cat.unregistered.map((h) => (
                      <tr key={h.source}>
                        <th scope="row" data-label={t("health.source")}>
                          {h.source}
                        </th>
                        <td data-label={t("health.state")}>
                          <Pill tone={h.ok ? "ok" : "danger"} value={h.ok ? "ok" : "failing"}>
                            {h.ok ? t("health.ok") : t("health.failing")}
                          </Pill>
                        </td>
                        <td data-label={t("health.collector")}>
                          {h.collector || <Empty />}
                          {h.version ? <span className="muted"> {h.version}</span> : null}
                        </td>
                        <td data-label={t("health.lastAttempt")}>
                          <Time iso={h.last_attempt} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </Table>
              </section>
            ) : null}
          </>
        )}
      </Async>
    </>
  );
}
