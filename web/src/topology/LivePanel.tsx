import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import type { LiveTopology } from "../api/types";
import { ObjectLink } from "../components/ObjectLink";
import { HealthPill } from "../components/ui";
import { useI18n, type MessageKey } from "../i18n";
import { isKnownRole } from "../inventory/kind";
import { prefersReducedMotion } from "./graph";
import { ageOf, edgeLive, formatBps, formatSpeed, formatUtil, isStale, lagLabel, rowLive } from "./live";
import type { Edge, Graph, Topology } from "./model";

/** What the reader clicked in the drawing. */
export type Selection = {
  kind: "node" | "edge";
  id: string;
  /** For a line of an aggregate: its group, so that the details follow it when it folds or unfolds. */
  group?: string;
};

/** The clock of the "n s ago" line: it moves every few seconds, not with each render. */
function useNow(everyMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), everyMs);
    return () => clearInterval(t);
  }, [everyMs]);
  return now;
}

/** "Live data 12 s ago", or stale, and the flow switch. */
export function LiveStatus({ live, flow, setFlow }: { live: LiveTopology; flow: boolean; setFlow: (on: boolean) => void }) {
  const { t, number } = useI18n();
  const now = useNow(5000);
  const age = ageOf(live, now);
  const stale = isStale(live, now);
  const reduced = prefersReducedMotion();
  return (
    <div className="live-status">
      <p role="status" className={stale ? "live-age live-age-stale" : "live-age"} data-stale={stale ? "true" : "false"}>
        <span className="live-dot" aria-hidden="true" />
        {age === undefined
          ? t("topology.live.never")
          : stale
            ? t("topology.live.stale", { age: number(age) })
            : t("topology.live.fresh", { age: number(age) })}
      </p>
      <label className="check">
        <input type="checkbox" checked={flow && !reduced} disabled={reduced} onChange={(e) => setFlow(e.target.checked)} />
        <span>{t("topology.live.flow")}</span>
      </label>
      {reduced ? <p className="hint">{t("topology.live.flowReduced")}</p> : null}
    </div>
  );
}

interface PanelProps {
  selection: Selection;
  graph: Graph;
  topology: Topology;
  live: LiveTopology;
  expanded: ReadonlySet<string>;
  onClose: () => void;
  onToggleLag: (group: string) => void;
}

/** The side panel: a line's ends and figures, or a device's health. */
export function DetailsPanel(p: PanelProps) {
  const { t } = useI18n();
  const heading = useRef<HTMLHeadingElement>(null);
  // Whatever opened it (a click in the drawing, a button of the table), the reader is taken to it.
  useEffect(() => heading.current?.focus(), [p.selection.kind, p.selection.id]);
  return (
    <aside className="topology-panel card" aria-labelledby="topology-panel-title">
      <div className="card-head">
        <h2 id="topology-panel-title" ref={heading} tabIndex={-1}>
          {t("topology.panel.title")}
        </h2>
        <button type="button" className="button" onClick={p.onClose}>
          {t("topology.panel.close")}
        </button>
      </div>
      {p.selection.kind === "edge" ? <EdgeDetails {...p} /> : <NodeDetails {...p} />}
    </aside>
  );
}

function EdgeDetails({ selection, graph, topology, live, expanded, onToggleLag }: PanelProps) {
  const { t, locale } = useI18n();
  const e = graph.edges.find((x) => x.id === selection.id) as Edge;
  const name = (id: string) => topology.nodes.get(id)?.name ?? id;
  const rows = e.rows ?? [];
  const folded = e.lag?.folded ? e.lag : undefined;
  const total = live.enabled ? edgeLive(e, live.links) : undefined;
  const now = useNow(5000);
  if (e.uplink) {
    const r = rows[0];
    return (
      <>
        <h3>{t("topology.panel.uplink", { from: name(e.source), to: name(e.target) })}</h3>
        <p>{t(`topology.class.${e.klass}`)}</p>
        <p>{t("topology.panel.uplinkWhat", { from: name(e.source), to: name(e.target), source: r?.source ?? "" })}</p>
        <p className="hint">{t("topology.panel.uplinkNoLive")}</p>
      </>
    );
  }
  return (
    <>
      <h3>{t("topology.panel.link",{ from: name(e.source), to: name(e.target) })}</h3>
      <p>{t(`topology.class.${e.klass}`)}</p>
      {folded ? (
        <p>
          <strong>{t("topology.panel.lag", { name: live.enabled ? lagLabel(folded.name, rows, live.links, locale) : `${folded.name} (${rows.length}×)` })}</strong>{" "}
          <button type="button" className="button" onClick={() => onToggleLag(folded.group)}>
            {t("topology.panel.unfold")}
          </button>
        </p>
      ) : e.lag && expanded.has(e.lag.group) ? (
        <p>
          <button type="button" className="button" onClick={() => e.lag && onToggleLag(e.lag.group)}>
            {t("topology.panel.fold", { name: e.lag.name })}
          </button>
        </p>
      ) : null}
      {!live.enabled ? <p className="hint">{t("topology.panel.liveOff")}</p> : null}
      {live.enabled && isStale(live, now) ? (
        <p role="note" className="notice">
          {t("topology.panel.stale")}
        </p>
      ) : null}
      {total && rows.length > 1 ? <Figures from={name(e.source)} to={name(e.target)} out={total.out} back={total.back} util={total.util} speed={total.speedMbps} /> : null}
      {(e.uplinks ?? []).map((u) => (
        <p key={u.key} className="hint">
          {t("topology.panel.alsoUplink", { from: name(u.a.deviceId), to: name(u.b.deviceId), source: u.source })}
        </p>
      ))}
      <ul className="topology-panel-links">
        {rows.map((r) => {
          const forward = r.a.deviceId === e.source;
          const [ps, pd] = forward ? [r.a, r.b] : [r.b, r.a];
          const m = live.enabled ? rowLive(r, live.links) : undefined;
          return (
            <li key={r.key}>
              <p>
                <ObjectLink id={ps.id} display={ps.name} /> ↔ <ObjectLink id={pd.id} display={pd.name} />
              </p>
              {live.enabled && !m ? <p className="hint">{t("topology.panel.noLive")}</p> : null}
              {m ? (
                <Figures
                  from={name(e.source)}
                  to={name(e.target)}
                  out={forward ? m.out : m.back}
                  back={forward ? m.back : m.out}
                  util={m.util}
                  speed={m.speedMbps}
                  state={m.oper}
                  source={m.source}
                />
              ) : null}
            </li>
          );
        })}
      </ul>
    </>
  );
}

function Figures(f: { from: string; to: string; out?: number; back?: number; util?: number; speed?: number; state?: string; source?: string }) {
  const { t, locale } = useI18n();
  const dash = "—";
  return (
    <dl className="attr-list topology-figures">
      <dt>{t("topology.panel.speed")}</dt>
      <dd>{f.speed ? formatSpeed(f.speed, locale) : dash}</dd>
      <dt>{t("topology.panel.towards", { from: f.from, to: f.to })}</dt>
      <dd className="num">{f.out !== undefined ? formatBps(f.out, locale) : dash}</dd>
      <dt>{t("topology.panel.towards", { from: f.to, to: f.from })}</dt>
      <dd className="num">{f.back !== undefined ? formatBps(f.back, locale) : dash}</dd>
      <dt>{t("topology.panel.util")}</dt>
      <dd className="num">{f.util !== undefined ? formatUtil(f.util, locale) : dash}</dd>
      {f.state ? (
        <>
          <dt>{t("topology.panel.state")}</dt>
          <dd>{t(`topology.state.${f.state}` as MessageKey)}</dd>
        </>
      ) : null}
      {f.source ? (
        <>
          <dt>{t("topology.panel.source")}</dt>
          <dd>{f.source}</dd>
        </>
      ) : null}
    </dl>
  );
}

function NodeDetails({ selection, topology }: PanelProps) {
  const { t, number } = useI18n();
  const n = topology.nodes.get(selection.id);
  if (!n) return null;
  const role = n.role ? (isKnownRole(n.role) ? t((n.role === "other" ? "role.otherDevice" : `role.${n.role}`) as MessageKey) : n.role) : undefined;
  return (
    <>
      <h3>{n.name}</h3>
      <dl className="attr-list topology-figures">
        {role ? (
          <>
            <dt>{t("topology.panel.kind")}</dt>
            <dd>{role}</dd>
          </>
        ) : null}
        <dt>{t("topology.panel.health")}</dt>
        <dd>
          <HealthPill value={n.health ?? "unknown"} />
        </dd>
        {n.critical ? (
          <>
            <dt>{t("topology.panel.critical")}</dt>
            <dd className="num">{number(n.critical)}</dd>
          </>
        ) : null}
        {n.mlagDomain ? (
          <>
            <dt>{t("topology.panel.mlag")}</dt>
            <dd>{n.mlagDomain}</dd>
          </>
        ) : null}
      </dl>
      {n.unresolved ? null : (
        <p>
          <Link to={`/objects/${encodeURIComponent(n.id)}`}>{t("topology.panel.open", { name: n.name })}</Link>
        </p>
      )}
    </>
  );
}
