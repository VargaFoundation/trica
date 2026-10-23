import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { api } from "../api/endpoints";
import type { CollectorInstance } from "../api/types";
import { Async } from "../components/Async";
import { useCrumbs } from "../components/crumbs";
import { Empty, Time } from "../components/Time";
import { Alert, Card, Pill } from "../components/ui";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { sourceQuery, STATUS_TONE, useWord } from "./status";

/** One instance of a module: how it is configured, who reads it, and its last run. */
export function CollectorInstancePage() {
  const { kind = "", name = "" } = useParams();
  const { t } = useI18n();
  usePageTitle(name);
  const module = useQuery({ queryKey: ["collector", kind], queryFn: ({ signal }) => api.collector(kind, signal) });
  const query = useQuery({
    queryKey: ["collector-instance", kind, name],
    queryFn: ({ signal }) => api.collectorInstance(kind, name, signal),
    refetchInterval: 60_000,
  });
  useCrumbs([
    { label: t("nav.collectors"), to: "/collectors" },
    { label: module.data?.title ?? kind, to: `/collectors/${encodeURIComponent(kind)}` },
    { label: name },
  ]);
  return <Async query={query}>{(i) => <InstanceBody instance={i} />}</Async>;
}

function InstanceBody({ instance: i }: { instance: CollectorInstance }) {
  const { t, number } = useI18n();
  const word = useWord();
  const cfg = i.config;
  const h = i.health;
  return (
    <>
      <h1>{i.name}</h1>
      <p className="lead">
        <Pill tone={STATUS_TONE[i.status] ?? "neutral"} value={i.status}>
          {word("status", i.status)}
        </Pill>{" "}
        <code>{i.source}</code> · {word("origin", i.origin)}
      </p>
      {i.origin === "reported" ? <Alert tone="info" role="note">{t("collectors.reportedNote")}</Alert> : null}

      {cfg ? (
        <Card title={t("collectors.configuration")} titleId="config-h">
          <dl className="facts">
            <div>
              <dt>{t("collectors.address")}</dt>
              <dd>{cfg.address || <Empty />}</dd>
            </div>
            <div>
              <dt>{t("collectors.interval")}</dt>
              <dd>{t("collectors.seconds", { value: number(cfg.interval_seconds) })}</dd>
            </div>
            <div>
              <dt>{t("collectors.timeout")}</dt>
              <dd>{t("collectors.seconds", { value: number(cfg.timeout_seconds) })}</dd>
            </div>
            <div>
              <dt>{t("collectors.domain")}</dt>
              <dd>{cfg.domain || <Empty />}</dd>
            </div>
            <div>
              <dt>{t("collectors.tls")}</dt>
              <dd>
                {cfg.tls.insecure ? (
                  <Pill tone="warn" value="insecure">
                    {t("collectors.tlsInsecure")}
                  </Pill>
                ) : (
                  t("collectors.tlsVerified")
                )}
                {cfg.tls.ca_file ? (
                  <div className="muted">
                    {t("collectors.tlsCa")}: <code>{cfg.tls.ca_file}</code>
                  </div>
                ) : null}
                {cfg.tls.min_version ? (
                  <div className="muted">
                    {t("collectors.tlsMin")}: {cfg.tls.min_version}
                  </div>
                ) : null}
              </dd>
            </div>
          </dl>

          <h3>{t("collectors.options")}</h3>
          {Object.keys(cfg.options).length === 0 ? (
            <p>{t("collectors.noOptionsSet")}</p>
          ) : (
            <dl className="facts">
              {Object.entries(cfg.options)
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([k, v]) => (
                  <div key={k}>
                    <dt>
                      <code>{k}</code>
                    </dt>
                    <dd>{typeof v === "string" ? v : JSON.stringify(v)}</dd>
                  </div>
                ))}
            </dl>
          )}

          <h3>{t("collectors.credentials")}</h3>
          {cfg.credentials ? (
            <dl className="facts">
              <div>
                <dt>{t("collectors.username")}</dt>
                <dd>{cfg.credentials.username || <Empty />}</dd>
              </div>
              <div>
                <dt>{t("collectors.secretRef")}</dt>
                <dd>{cfg.credentials.secret_ref ? <code>{cfg.credentials.secret_ref}</code> : <Empty />}</dd>
              </div>
            </dl>
          ) : (
            <p className="muted">{t("collectors.credentialsHidden")}</p>
          )}
        </Card>
      ) : null}

      <Card title={t("collectors.lastRun")} titleId="run-h">
        {h ? (
          <>
            <dl className="facts">
              <div>
                <dt>{t("health.lastAttempt")}</dt>
                <dd>
                  <Time iso={h.last_attempt} />
                </dd>
              </div>
              <div>
                <dt>{t("health.lastSuccess")}</dt>
                <dd>{h.last_success ? <Time iso={h.last_success} /> : t("health.never")}</dd>
              </div>
              <div>
                <dt>{t("health.duration")}</dt>
                <dd>{t("unit.ms", { value: number(h.duration_ms) })}</dd>
              </div>
              <div>
                <dt>{t("health.items")}</dt>
                <dd>{number(h.items)}</dd>
              </div>
              <div>
                <dt>{t("health.failures")}</dt>
                <dd>{number(h.failures)}</dd>
              </div>
              <div>
                <dt>{t("health.collector")}</dt>
                <dd>
                  {h.collector || <Empty />}
                  {h.version ? <span className="muted"> {h.version}</span> : null}
                </dd>
              </div>
            </dl>
            {h.last_error ? (
              <Alert tone="danger" role="note" title={t("health.lastError")}>
                <p>{h.last_error}</p>
              </Alert>
            ) : null}
            {h.warnings.length > 0 ? (
              <>
                <h3>{t("health.warnings")}</h3>
                <ul>
                  {h.warnings.map((w, n) => (
                    <li key={`${n}-${w}`}>{w}</li>
                  ))}
                </ul>
              </>
            ) : null}
          </>
        ) : (
          <p>{t("collectors.noRun")}</p>
        )}
        <p>
          <Link to={`/search?q=${encodeURIComponent(sourceQuery(i.source))}`}>{t("collectors.seeObjects")}</Link>
        </p>
      </Card>
    </>
  );
}
