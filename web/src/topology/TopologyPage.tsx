import { useEffect, useMemo, useRef, useState, type MutableRefObject } from "react";
import type { LiveTopology } from "../api/types";
import { useSearchParams } from "react-router";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { ExportButtons } from "../export/ExportButtons";
import { useI18n, type MessageKey } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { draw, EDGE_STYLE, graphEnv, UPLINK_STYLE, UTIL_CLASSES, type Drawing, type LiveStyle } from "./graph";
import { edgeLive, formatBps, formatUtil, lagLabel, liveLabel, rowLive, utilClass, widthOf } from "./live";
import { DetailsPanel, LiveStatus, type Selection } from "./LivePanel";
import { useLiveTopology, LIVE_OFF } from "./useLiveTopology";
import {
  ALL_CLASSES,
  filterRows,
  isUplink,
  LINK_CLASSES,
  toGraph,
  type Graph,
  type LinkClass,
  type LinkRow,
  type Topology,
} from "./model";
import { useTopology, type TopologyData } from "./useTopology";

/** /topology — the layer-2 links of the inventory, as a graph and as a table. */
export function TopologyPage() {
  const { t } = useI18n();
  usePageTitle(t("topology.title"));
  const data = useTopology();
  return (
    <>
      <h1>{t("topology.title")}</h1>
      <p>{t("topology.intro")}</p>
      <Async query={data}>{(d) => <Topo data={d} />}</Async>
    </>
  );
}

const named = (m: ReadonlyMap<string, string>, ids: Iterable<string>) =>
  [...new Set(ids)]
    .map((id) => ({ id, name: m.get(id) ?? id }))
    .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));

function useFilters(t: Topology) {
  const [params, setParams] = useSearchParams();
  const sites = useMemo(() => named(t.sites, [...t.nodes.values()].flatMap((n) => (n.siteId ? [n.siteId] : []))), [t]);
  const racks = useMemo(() => named(t.racks, [...t.nodes.values()].flatMap((n) => (n.rackId ? [n.rackId] : []))), [t]);
  const vlans = useMemo(() => named(t.vlans, t.vlans.keys()), [t]);
  // A value in the address that matches nothing the data has is ignored, not obeyed.
  const pick = (name: string, options: { id: string }[]) => {
    const v = params.get(name);
    return v !== null && options.some((o) => o.id === v) ? v : undefined;
  };
  const asked = (params.get("classes") ?? "").split(",").filter((c): c is LinkClass => LINK_CLASSES.includes(c as LinkClass));
  const classes: ReadonlySet<LinkClass> = asked.length > 0 ? new Set(asked) : ALL_CLASSES;
  const site = pick("site", sites);
  const rack = pick("rack", racks);
  const vlan = pick("vlan", vlans);
  const classKey = [...classes].join(",");
  // One object per set of choices: the graph is drawn again only when they change.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const filters = useMemo(() => ({ site, rack, vlan, classes }), [site, rack, vlan, classKey]);
  const set = (name: string, value: string | undefined) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === undefined || value === "") next.delete(name);
        else next.set(name, value);
        return next;
      },
      { replace: true },
    );
  return {
    options: { sites, racks, vlans },
    filters,
    set,
    toggleClass: (c: LinkClass) => {
      const next = new Set(classes);
      if (next.has(c)) next.delete(c);
      else next.add(c);
      // Nothing ticked would be an empty page: that is "all" again, so the last box cannot be cleared.
      if (next.size === 0) return;
      set("classes", next.size === LINK_CLASSES.length ? undefined : LINK_CLASSES.filter((x) => next.has(x)).join(","));
    },
  };
}

function Topo({ data }: { data: TopologyData }) {
  const { t, tp, number, locale } = useI18n();
  const { topology } = data;
  const f = useFilters(topology);
  const liveQuery = useLiveTopology();
  const live: LiveTopology = liveQuery.data ?? LIVE_OFF;
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const [selection, setSelection] = useState<Selection | null>(null);
  const [flow, setFlow] = useState(false);
  const rows = useMemo(() => filterRows(topology, f.filters), [topology, f.filters]);
  const graph = useMemo(() => toGraph(topology, rows, { expanded }), [topology, rows, expanded]);
  const drawingRef = useRef<Drawing | null>(null);
  const narrowed = f.filters.site !== undefined || f.filters.rack !== undefined || f.filters.vlan !== undefined || f.filters.classes.size < LINK_CLASSES.length;
  // What the overlay sets on each line; recomputed when the measurements or the lines change, never redrawn.
  const liveStyles = useMemo(() => {
    if (!live.enabled) return null;
    const out = new Map<string, LiveStyle>();
    for (const e of graph.edges) {
      // An uplink names no port: no figure, and no "not measured" grey over its class colour (ADR 0062).
      if (e.uplink) continue;
      const l = edgeLive(e, live.links);
      out.set(e.id, {
        util: utilClass(l),
        width: widthOf(Math.max(l.out ?? 0, l.back ?? 0) || undefined),
        label: liveLabel(l, locale),
        title: e.lag?.folded ? lagLabel(e.lag.name, e.rows ?? [], live.links, locale) : undefined,
        flowing: (l.out ?? 0) + (l.back ?? 0) > 0,
      });
    }
    return out;
  }, [graph, live, locale]);
  const toggleLag = (group: string) => {
    // The details stay open on the aggregate: on its first member once unfolded, on its line once folded.
    setSelection((prev) => (prev && prev.kind === "edge" ? { ...prev, group } : prev));
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(group)) next.delete(group);
      else next.add(group);
      return next;
    });
  };
  // A selection the drawing no longer has (filtered out, folded) is let go.
  const selected =
    selection === null
      ? null
      : selection.kind === "node"
        ? topology.nodes.has(selection.id) && graph.nodes.some((n) => n.id === selection.id)
          ? selection
          : null
        : graph.edges.some((e) => e.id === selection.id)
          ? selection
          : (() => {
              const e = selection.group ? graph.edges.find((x) => x.lag?.group === selection.group) : undefined;
              return e ? { ...selection, id: e.id } : null;
            })();

  return (
    <>
      {data.truncated ? (
        <p role="note" className="notice">
          {t("topology.truncated")}
        </p>
      ) : null}
      <Filters f={f} />
      <p role="status" className="summary">
        {tp("topology.links", rows.length)} · {tp("topology.devices", graph.nodes.length)}
      </p>
      {live.enabled ? <LiveStatus live={live} flow={flow} setFlow={setFlow} /> : null}
      <Notices topology={topology} graph={graph} />
      {topology.rows.length === 0 ? (
        <p>{t("topology.empty")}</p>
      ) : rows.length === 0 ? (
        <p>{narrowed ? t("topology.emptyFiltered") : t("topology.empty")}</p>
      ) : (
        <>
          <Legend live={live.enabled} uplinks={graph.edges.some((e) => e.uplink)} />
          <div className={selected ? "topology-stage with-panel" : "topology-stage"}>
            <GraphView
              graph={graph}
              drawingRef={drawingRef}
              liveStyles={liveStyles}
              flow={flow}
              selected={selected?.id ?? null}
              onSelect={setSelection}
              onToggleLag={(id) => {
                const g = graph.edges.find((e) => e.id === id)?.lag?.group;
                if (g) toggleLag(g);
              }}
            />
            {selected ? (
              <DetailsPanel
                selection={selected}
                graph={graph}
                topology={topology}
                live={live}
                onClose={() => setSelection(null)}
                onToggleLag={toggleLag}
                expanded={expanded}
              />
            ) : null}
          </div>
          <ExportButtons
            basename={`trica-topology-${new Date().toISOString().slice(0, 10)}`}
            title={t("topology.title")}
            render={(format) => {
              const d = drawingRef.current;
              if (!d) return Promise.reject(new Error(t("topology.notDrawn")));
              return d.raster(format);
            }}
            vector={() => {
              const d = drawingRef.current;
              if (!d) return Promise.reject(new Error(t("topology.notDrawn")));
              return d.vector();
            }}
          />
          <p className="hint">{t("topology.keyboard", { count: number(rows.length) })}</p>
          <LinksTable rows={rows} topology={topology} live={live} graph={graph} onSelect={setSelection} />
        </>
      )}
    </>
  );
}

function Notices({ topology, graph }: { topology: Topology; graph: Graph }) {
  const { tp } = useI18n();
  const { cables, ends } = topology.skipped;
  return (
    <>
      {cables > 0 ? (
        <p role="note" className="notice">
          {tp("topology.skippedCables", cables)}
        </p>
      ) : null}
      {ends > 0 ? (
        <p role="note" className="notice">
          {tp("topology.skippedEnds", ends)}
        </p>
      ) : null}
      {graph.loops > 0 ? (
        <p role="note" className="notice">
          {tp("topology.loops", graph.loops)}
        </p>
      ) : null}
    </>
  );
}

function Filters({ f }: { f: ReturnType<typeof useFilters> }) {
  const { t } = useI18n();
  const select = (name: "site" | "rack" | "vlan", label: MessageKey, options: { id: string; name: string }[]) => (
    <label>
      <span>{t(label)}</span>
      <select value={f.filters[name] ?? ""} onChange={(e) => f.set(name, e.target.value)}>
        <option value="">{t("topology.filter.all")}</option>
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </select>
    </label>
  );
  return (
    <form className="topology-filters" role="search" aria-label={t("topology.filters")} onSubmit={(e) => e.preventDefault()}>
      {select("site", "topology.filter.site", f.options.sites)}
      {select("rack", "topology.filter.rack", f.options.racks)}
      {select("vlan", "topology.filter.vlan", f.options.vlans)}
      <fieldset>
        <legend>{t("topology.filter.classes")}</legend>
        {LINK_CLASSES.map((c) => (
          <label key={c} className="check">
            <input type="checkbox" checked={f.filters.classes.has(c)} onChange={() => f.toggleClass(c)} />
            <span>{t(`topology.class.${c}`)}</span>
          </label>
        ))}
      </fieldset>
      <p className="hint">{t("topology.filter.help")}</p>
    </form>
  );
}

const LEGEND_DASH: Record<LinkClass, string | undefined> = { declared: undefined, observed: "2 4", deduced: "8 4" };

function Legend({ live, uplinks }: { live: boolean; uplinks: boolean }) {
  const { t } = useI18n();
  return (
    <>
      <ul className="topology-legend" aria-label={t("topology.legend")}>
        {LINK_CLASSES.map((c) => (
          <li key={c}>
            <svg width="44" height="10" aria-hidden="true" focusable="false">
              <line className={`legend-line-${c}`} x1="1" y1="5" x2="43" y2="5" strokeWidth={EDGE_STYLE[c].width} strokeDasharray={LEGEND_DASH[c]} />
            </svg>
            <span>{t(`topology.legend.${c}`)}</span>
          </li>
        ))}
        {uplinks ? (
          <li>
            <svg width="44" height="10" aria-hidden="true" focusable="false">
              <line className="legend-line-uplink" x1="1" y1="5" x2="37" y2="5" strokeWidth={UPLINK_STYLE.width} strokeDasharray={UPLINK_STYLE.dash.join(" ")} />
              <path className="legend-arrow-uplink" d="M43 5 L36 1.5 L36 8.5 Z" />
            </svg>
            <span>{t("topology.legend.uplink")}</span>
          </li>
        ) : null}
      </ul>
      {live ? (
        <ul className="topology-legend" aria-label={t("topology.live.legend")}>
          {UTIL_CLASSES.map((c) => (
            <li key={c}>
              <svg width="44" height="10" aria-hidden="true" focusable="false">
                <line className={`legend-live-${c}`} x1={c === "down" ? 3 : 1} y1="5" x2={c === "down" ? 41 : 43} y2="5" strokeWidth="3" />
                {c === "down" ? (
                  <>
                    <line className="legend-live-down" x1="2" y1="0" x2="2" y2="10" strokeWidth="2" />
                    <line className="legend-live-down" x1="42" y1="0" x2="42" y2="10" strokeWidth="2" />
                  </>
                ) : null}
              </svg>
              <span>{t(`topology.live.util.${c}`)}</span>
            </li>
          ))}
          <li>
            <span>{t("topology.live.width")}</span>
          </li>
        </ul>
      ) : null}
    </>
  );
}

function GraphView({
  graph,
  drawingRef,
  liveStyles,
  flow,
  selected,
  onSelect,
  onToggleLag,
}: {
  graph: Graph;
  drawingRef: MutableRefObject<Drawing | null>;
  liveStyles: ReadonlyMap<string, LiveStyle> | null;
  flow: boolean;
  selected: string | null;
  onSelect: (s: Selection | null) => void;
  onToggleLag: (edgeId: string) => void;
}) {
  const { t, tp } = useI18n();
  const box = useRef<HTMLDivElement>(null);
  // The outcome belongs to the graph it was computed for: another graph is "laying out" until its own outcome arrives.
  const [outcome, setOutcome] = useState<{ of: Graph; kind: "ready" | "failed"; ms?: number; detail?: string } | null>(null);
  const state = outcome?.of === graph ? outcome : { kind: "layout" as const, ms: undefined, detail: undefined };
  // The labels are read when the drawing is made; a language change redraws.
  const levelText = (l: string) => t(`topology.level.${l}` as MessageKey);
  const deducedText = t("topology.level.unknown");
  const uplinkText = t("topology.uplink.label");
  // The handlers change with every render; the drawing calls the latest ones.
  const handlers = useRef({ onSelect, onToggleLag });
  const latest = useRef({ liveStyles, flow, selected });
  // Declared before the drawing's effect, so that a new drawing reads the values of this render.
  useEffect(() => {
    handlers.current = { onSelect, onToggleLag };
    latest.current = { liveStyles, flow, selected };
  });

  useEffect(() => {
    const started = performance.now();
    const d = draw(
      box.current,
      graph,
      {
        deduced: deducedText,
        uplink: uplinkText,
        level: levelText,
        lag: (e) => `${e.lag?.name ?? ""} (${e.count}×)`,
        group: (domain) => t("topology.group", { domain }),
      },
      (id) => handlers.current.onSelect({ kind: "node", id }),
      {
        onEdge: (id) => handlers.current.onSelect({ kind: "edge", id }),
        onEdgeToggle: (id) => handlers.current.onToggleLag(id),
        onClear: () => handlers.current.onSelect(null),
      },
    );
    drawingRef.current = d;
    // A new drawing starts from the overlay and the selection of the moment.
    d.live(latest.current.liveStyles);
    d.flow(latest.current.flow);
    d.select(latest.current.selected);
    let live = true;
    d.ready.then(
      () => live && setOutcome({ of: graph, kind: "ready", ms: Math.round(performance.now() - started) }),
      (e: unknown) => live && setOutcome({ of: graph, kind: "failed", detail: e instanceof Error ? e.message : String(e) }),
    );
    return () => {
      live = false;
      if (drawingRef.current === d) drawingRef.current = null;
      d.destroy();
    };
    // levelText and t only change with the language, which deducedText follows.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [graph, deducedText, uplinkText]);

  useEffect(() => drawingRef.current?.live(liveStyles), [liveStyles, drawingRef]);
  useEffect(() => drawingRef.current?.flow(flow), [flow, drawingRef]);
  useEffect(() => drawingRef.current?.select(selected), [selected, drawingRef]);

  return (
    <div className="topology-figure">
      {state.kind === "layout" ? (
        <p role="status" className="loading">
          {t("topology.layouting")}
        </p>
      ) : null}
      {state.kind === "failed" ? (
        <p role="alert" className="notice notice-error">
          {t("topology.layoutFailed", { detail: state.detail ?? "" })}
        </p>
      ) : null}
      <div
        ref={box}
        className="topology-canvas"
        role="img"
        aria-label={t("topology.figureLabel", { devices: tp("topology.devices", graph.nodes.length), links: tp("topology.links", graph.edges.length) })}
        data-state={state.kind}
        data-layout-ms={state.ms}
        data-nodes={graph.nodes.length}
        data-edges={graph.edges.length}
        data-live={liveStyles ? "on" : undefined}
        data-headless={graphEnv.headless ? "true" : undefined}
      />
    </div>
  );
}

/** What the deducer says it saw; any other word is shown as it came. */
const EVIDENCE = ["lldp", "lldp_one_sided", "mac_table"];

function detailOf(r: LinkRow, t: ReturnType<typeof useI18n>["t"]): string {
  const parts: string[] = [];
  if (r.klass === "deduced") parts.push(r.level ? t(`topology.level.${r.level}` as MessageKey) : t("topology.level.unknown"));
  if (r.evidence) parts.push(EVIDENCE.includes(r.evidence) ? t(`topology.evidence.${r.evidence}` as MessageKey) : r.evidence);
  return parts.length === 0 ? "—" : parts.join(" · ");
}

/** A link's live figures in words, for the table: "↑ 3.2 Gb/s ↓ 1.1 Gb/s · 32 %", or its state. */
export function liveCell(r: LinkRow, live: LiveTopology, locale: string, t: ReturnType<typeof useI18n>["t"]): string {
  const m = rowLive(r, live.links);
  if (!m) return "—";
  if (m.oper === "down") return t("topology.state.down");
  const parts: string[] = [];
  const flows = [m.out !== undefined ? `↑ ${formatBps(m.out, locale)}` : "", m.back !== undefined ? `↓ ${formatBps(m.back, locale)}` : ""].filter(Boolean);
  if (flows.length) parts.push(flows.join(" "));
  if (m.util !== undefined) parts.push(formatUtil(m.util, locale));
  return parts.length ? parts.join(" · ") : m.oper ? t(`topology.state.${m.oper}`) : "—";
}

function LinksTable({
  rows,
  topology,
  live,
  graph,
  onSelect,
}: {
  rows: readonly LinkRow[];
  topology: Topology;
  live: LiveTopology;
  graph: Graph;
  onSelect: (s: Selection) => void;
}) {
  const { t, locale } = useI18n();
  // The line each link is drawn on: the keyboard way to the same details as a click in the drawing.
  const edgeOf = useMemo(() => {
    const m = new Map<string, string>();
    for (const e of graph.edges) for (const r of [...(e.rows ?? []), ...(e.uplinks ?? [])]) m.set(r.key, e.id);
    return m;
  }, [graph]);
  const nameOf = (id: string) => topology.nodes.get(id)?.name ?? id;
  const sorted = useMemo(
    () => [...rows].sort((a, b) => a.a.name.localeCompare(b.a.name) || a.b.name.localeCompare(b.b.name) || a.key.localeCompare(b.key)),
    [rows],
  );
  return (
    <section aria-labelledby="links-title">
      <h2 id="links-title">{t("topology.table.title")}</h2>
      <table>
        <caption className="sr-only">{t("topology.table.title")}</caption>
        <thead>
          <tr>
            <th scope="col">{t("topology.col.from")}</th>
            <th scope="col">{t("topology.col.to")}</th>
            <th scope="col">{t("topology.col.class")}</th>
            <th scope="col">{t("topology.col.detail")}</th>
            <th scope="col">{t("topology.col.kind")}</th>
            <th scope="col">{t("topology.col.source")}</th>
            {live.enabled ? <th scope="col">{t("topology.live.col")}</th> : null}
            <th scope="col">{t("topology.col.details")}</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((r) => (
            <tr key={r.key} data-class={r.klass}>
              <td data-label={t("topology.col.from")}>
                <End device={{ id: r.a.deviceId, name: nameOf(r.a.deviceId) }} port={isUplink(r) ? undefined : r.a} />
              </td>
              <td data-label={t("topology.col.to")}>
                <End device={{ id: r.b.deviceId, name: nameOf(r.b.deviceId) }} port={isUplink(r) ? undefined : r.b} />
              </td>
              <td data-label={t("topology.col.class")}>{t(`topology.class.${r.klass}`)}</td>
              <td data-label={t("topology.col.detail")}>{detailOf(r, t)}</td>
              <td data-label={t("topology.col.kind")}>
                {t(`topology.kind.${r.kind}`)}
                {r.cable ? (
                  <>
                    {" "}
                    <ObjectLink id={r.cable.id} display={r.cable.label} />
                  </>
                ) : null}
              </td>
              <td data-label={t("topology.col.source")}>{r.source || "—"}</td>
              {live.enabled ? (
                <td data-label={t("topology.live.col")} className="num">
                  {liveCell(r, live, locale, t)}
                </td>
              ) : null}
              <td data-label={t("topology.col.details")}>
                {edgeOf.has(r.key) ? (
                  <button
                    type="button"
                    className="button"
                    aria-label={
                      isUplink(r)
                        ? t("topology.panel.showUplinkLabel", { from: nameOf(r.a.deviceId), to: nameOf(r.b.deviceId) })
                        : t("topology.panel.showLabel", { from: nameOf(r.a.deviceId), fromPort: r.a.name, to: nameOf(r.b.deviceId), toPort: r.b.name })
                    }
                    onClick={() => onSelect({ kind: "edge", id: edgeOf.get(r.key) as string })}
                  >
                    {t("topology.panel.show")}
                  </button>
                ) : (
                  "—"
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

/** A device and the port on it, each a link to its own page. */
function End({ device, port }: { device: { id: string; name: string }; port?: { id: string; name: string } }) {
  const { t } = useI18n();
  return (
    <>
      <ObjectLink id={device.id} display={device.name} />
      {port === undefined ? (
        <>
          {" / "}
          <span className="hint">{t("topology.port.unknown")}</span>
        </>
      ) : null}
      {port ? (
        <>
          {" / "}
          <ObjectLink id={port.id} display={port.name} />
        </>
      ) : null}
    </>
  );
}
