import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import { Async } from "../components/Async";
import { Empty, Time } from "../components/Time";
import { useI18n } from "../i18n";
import { Fleet } from "../inventory/Fleet";
import { usePageTitle } from "../usePageTitle";

export function HealthPage() {
  const { t, tp, number } = useI18n();
  usePageTitle(t("health.title"));
  const query = useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => api.health(signal),
    refetchInterval: 60_000,
  });

  return (
    <>
      <h1>{t("health.title")}</h1>
      <p className="lead">{t("health.intro")}</p>
      <Fleet defaultGroup="kind" />
      <section aria-labelledby="collection-h">
        <h2 id="collection-h">{t("health.collection")}</h2>
        <Async query={query}>
          {(page) => {
            if (page.items.length === 0) return <p>{t("health.empty")}</p>;
            // Failing sources first: that is what the page is for.
            const rows = [...page.items].sort((a, b) => Number(a.ok) - Number(b.ok) || a.source.localeCompare(b.source));
            const failing = rows.filter((r) => !r.ok).length;
            return (
              <>
                <p role="status" className="summary">
                  {tp("health.summary", rows.length, { failing })}
                </p>
                <table>
                  <caption className="sr-only">{t("health.collection")}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t("health.source")}</th>
                      <th scope="col">{t("health.state")}</th>
                      <th scope="col">{t("health.collector")}</th>
                      <th scope="col">{t("health.target")}</th>
                      <th scope="col">{t("health.lastAttempt")}</th>
                      <th scope="col">{t("health.lastSuccess")}</th>
                      <th scope="col">{t("health.duration")}</th>
                      <th scope="col">{t("health.items")}</th>
                      <th scope="col">{t("health.warnings")}</th>
                      <th scope="col">{t("health.failures")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((r) => (
                      <tr key={`${r.source}/${r.collector}/${r.target}`} data-ok={r.ok ? "true" : "false"}>
                        <th scope="row" data-label={t("health.source")}>
                          {r.source}
                        </th>
                        <td data-label={t("health.state")}>
                          <span className={r.ok ? "badge badge-ok" : "badge badge-failing"} data-value={r.ok ? "ok" : "failing"}>
                            {r.ok ? t("health.ok") : t("health.failing")}
                          </span>
                          {r.last_error ? (
                            <div className="muted" title={t("health.lastError")}>
                              {r.last_error}
                            </div>
                          ) : null}
                        </td>
                        <td data-label={t("health.collector")}>
                          {r.collector}
                          {r.version ? <span className="muted"> {r.version}</span> : null}
                        </td>
                        <td data-label={t("health.target")}>{r.target || <Empty />}</td>
                        <td data-label={t("health.lastAttempt")}>
                          <Time iso={r.last_attempt} />
                        </td>
                        <td data-label={t("health.lastSuccess")}>{r.last_success ? <Time iso={r.last_success} /> : t("health.never")}</td>
                        <td data-label={t("health.duration")}>{t("unit.ms", { value: number(r.duration_ms) })}</td>
                        <td data-label={t("health.items")}>{number(r.items)}</td>
                        <td data-label={t("health.warnings")}>{number(r.warnings?.length ?? 0)}</td>
                        <td data-label={t("health.failures")}>{number(r.failures)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </>
            );
          }}
        </Async>
      </section>
    </>
  );
}
