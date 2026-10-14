import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { Proposal, ProposalStatus } from "../api/types";
import { Async } from "../components/Async";
import { Status } from "../components/Badge";
import { DecisionButtons, type Decision } from "../components/DecisionButtons";
import { ObjectLink } from "../components/ObjectLink";
import { Pager, PAGE_SIZE, useOffset } from "../components/Pager";
import { Empty, Time } from "../components/Time";
import { formatValue } from "../components/Value";
import { useCan } from "../hooks";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { NotFoundPage } from "./NotFoundPage";

const STATUSES: ProposalStatus[] = ["pending", "applied", "rejected", "superseded"];

function useDecisionNotice() {
  const { t } = useI18n();
  const [notice, setNotice] = useState("");
  const onDecided = (d: Decision, id: number) => setNotice(d === "approve" ? t("proposals.approved", { id }) : t("proposals.rejectedDone", { id }));
  const element = (
    <div role="status" className="decision-notice">
      {notice}
    </div>
  );
  return { onDecided, element };
}

export function ProposalsPage() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const offset = useOffset();
  const can = useCan("approve");
  const status = params.get("status") ?? "pending";
  const decided = useDecisionNotice();
  usePageTitle(t("proposals.title"));

  const query = useQuery({
    queryKey: ["proposals", status, offset],
    queryFn: ({ signal }) =>
      api.proposals({ status: status === "all" ? undefined : (status as ProposalStatus), limit: PAGE_SIZE, offset }, signal),
    placeholderData: (prev) => prev,
  });

  const apply = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const v = new FormData(e.currentTarget).get("status");
    setParams(typeof v === "string" && v !== "pending" ? { status: v } : {});
  };

  return (
    <>
      <h1>{t("proposals.title")}</h1>
      <p>{t("proposals.intro")}</p>
      <form className="filters" onSubmit={apply}>
        <label>
          {t("proposals.filterStatus")}
          <select name="status" defaultValue={status}>
            <option value="all">{t("filter.all")}</option>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`status.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <button type="submit">{t("filter.apply")}</button>
      </form>
      {decided.element}
      <Async query={query}>
        {(page) =>
          page.items.length === 0 ? (
            <p>{t("proposals.empty")}</p>
          ) : (
            <>
              <table>
                <caption className="sr-only">{t("proposals.title")}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t("proposals.id")}</th>
                    <th scope="col">{t("proposals.createdAt")}</th>
                    <th scope="col">{t("proposals.source")}</th>
                    <th scope="col">{t("proposals.author")}</th>
                    <th scope="col">{t("proposals.reason")}</th>
                    <th scope="col">{t("proposals.status")}</th>
                    {can ? <th scope="col">{t("proposals.actions")}</th> : null}
                  </tr>
                </thead>
                <tbody>
                  {page.items.map((p) => (
                    <tr key={p.id} data-proposal={p.id}>
                      <th scope="row" data-label={t("proposals.id")}>
                        <Link to={`/proposals/${p.id}`}>#{p.id}</Link>
                      </th>
                      <td data-label={t("proposals.createdAt")}>
                        <Time iso={p.created_at} />
                      </td>
                      <td data-label={t("proposals.source")}>{p.source}</td>
                      <td data-label={t("proposals.author")}>{p.author}</td>
                      <td data-label={t("proposals.reason")}>{p.reason}</td>
                      <td data-label={t("proposals.status")}>
                        <Status value={p.status} />
                        <DecidedBy p={p} />
                      </td>
                      {can ? (
                        <td data-label={t("proposals.actions")}>
                          <DecisionButtons proposal={p} onDecided={decided.onDecided} />
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
              <Pager total={page.total} limit={page.limit} offset={page.offset} />
            </>
          )
        }
      </Async>
    </>
  );
}

function DecidedBy({ p }: { p: Proposal }) {
  const { t, dateTime } = useI18n();
  if (!p.decided_by && !p.decided_at) return null;
  return <div className="muted">{t("proposals.decidedBy", { who: p.decided_by ?? "", when: dateTime(p.decided_at) })}</div>;
}

export function ProposalPage() {
  const { id = "" } = useParams();
  const n = Number.parseInt(id, 10);
  const query = useQuery({
    queryKey: ["proposal", n],
    queryFn: ({ signal }) => api.proposal(n, signal),
    enabled: Number.isInteger(n) && n > 0,
  });
  if (!Number.isInteger(n) || n <= 0) return <NotFoundPage />;
  if (query.isError && query.error instanceof ApiError && query.error.status === 404) return <NotFoundPage />;
  return <Async query={query}>{(p) => <ProposalDetail proposal={p} />}</Async>;
}

function ProposalDetail({ proposal: p }: { proposal: Proposal }) {
  const { t } = useI18n();
  const decided = useDecisionNotice();
  usePageTitle(t("proposals.detail", { id: p.id }));
  const yes = t("app.yes");
  const no = t("app.no");
  const f = (v: unknown) => formatValue(v, yes, no);
  const changes = p.preview?.changes ?? [];

  return (
    <>
      <h1>{t("proposals.detail", { id: p.id })}</h1>
      <dl className="facts">
        <div>
          <dt>{t("proposals.status")}</dt>
          <dd>
            <Status value={p.status} />
          </dd>
        </div>
        <div>
          <dt>{t("proposals.createdAt")}</dt>
          <dd>
            <Time iso={p.created_at} />
          </dd>
        </div>
        <div>
          <dt>{t("proposals.source")}</dt>
          <dd>{p.source}</dd>
        </div>
        <div>
          <dt>{t("proposals.author")}</dt>
          <dd>{p.author}</dd>
        </div>
        <div>
          <dt>{t("proposals.reason")}</dt>
          <dd>{p.reason || <Empty />}</dd>
        </div>
        {p.decided_at || p.decided_by ? (
          <div>
            <dt>{t("proposals.decided")}</dt>
            <dd>
              {p.decided_by ? <>{p.decided_by}, </> : null}
              <Time iso={p.decided_at} />
            </dd>
          </div>
        ) : null}
        {p.changeset_id ? (
          <div>
            <dt>{t("proposals.changeset")}</dt>
            <dd>{p.changeset_id}</dd>
          </div>
        ) : null}
      </dl>
      {decided.element}
      <DecisionButtons proposal={p} onDecided={decided.onDecided} />
      <section aria-labelledby="preview-h">
        <h2 id="preview-h">{t("proposals.preview")}</h2>
        {!p.preview ? (
          <p>{t("proposals.noPreview")}</p>
        ) : changes.length === 0 ? (
          <p>{t("proposals.noChanges")}</p>
        ) : (
          <table>
            <caption className="sr-only">{t("proposals.preview")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("proposals.op")}</th>
                <th scope="col">{t("proposals.target")}</th>
                <th scope="col">{t("proposals.change")}</th>
              </tr>
            </thead>
            <tbody>
              {changes.map((c, i) => (
                <tr key={`${c.object_id}-${c.key ?? ""}-${i}`}>
                  <td data-label={t("proposals.op")}>
                    <code>{c.op}</code>
                  </td>
                  <td data-label={t("proposals.target")}>
                    <ObjectLink id={c.object_id} />
                  </td>
                  <td data-label={t("proposals.change")}>
                    {c.key ? <code>{c.key}</code> : null}
                    {c.before !== undefined || c.after !== undefined ? (
                      <>
                        {" "}
                        {f(c.before) || <Empty />} → {f(c.after) || <Empty />}
                      </>
                    ) : null}
                    {c.other_id ? (
                      <>
                        {" "}
                        <ObjectLink id={c.other_id} />
                      </>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
