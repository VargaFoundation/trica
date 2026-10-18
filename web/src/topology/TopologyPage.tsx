import { useEffect, useMemo, useRef, useState, type MutableRefObject } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { ExportButtons } from "../export/ExportButtons";
import { useI18n, type MessageKey } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { draw, EDGE_STYLE, graphEnv, type Drawing } from "./graph";
import {
  ALL_CLASSES,
  filterRows,
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
  const { t, tp, number } = useI18n();
  const { topology } = data;
  const f = useFilters(topology);
  const rows = useMemo(() => filterRows(topology, f.filters), [topology, f.filters]);
  const graph = useMemo(() => toGraph(topology, rows), [topology, rows]);
  const drawingRef = useRef<Drawing | null>(null);
  const narrowed = f.filters.site !== undefined || f.filters.rack !== undefined || f.filters.vlan !== undefined || f.filters.classes.size < LINK_CLASSES.length;

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
      <Notices topology={topology} graph={graph} />
      {topology.rows.length === 0 ? (
        <p>{t("topology.empty")}</p>
      ) : rows.length === 0 ? (
        <p>{narrowed ? t("topology.emptyFiltered") : t("topology.empty")}</p>
      ) : (
        <>
          <Legend />
          <GraphView graph={graph} drawingRef={drawingRef} />
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
          <LinksTable rows={rows} topology={topology} />
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

function Legend() {
  const { t } = useI18n();
  return (
    <ul className="topology-legend" aria-label={t("topology.legend")}>
      {LINK_CLASSES.map((c) => (
        <li key={c}>
          <svg width="44" height="10" aria-hidden="true" focusable="false">
            <line className={`legend-line-${c}`} x1="1" y1="5" x2="43" y2="5" stroke={EDGE_STYLE[c].color} strokeWidth={EDGE_STYLE[c].width} strokeDasharray={LEGEND_DASH[c]} />
          </svg>
          <span>{t(`topology.legend.${c}`)}</span>
        </li>
      ))}
    </ul>
  );
}

function GraphView({ graph, drawingRef }: { graph: Graph; drawingRef: MutableRefObject<Drawing | null> }) {
  const { t, tp } = useI18n();
  const navigate = useNavigate();
  const box = useRef<HTMLDivElement>(null);
  // The outcome belongs to the graph it was computed for: another graph is "laying out" until its own outcome arrives.
  const [outcome, setOutcome] = useState<{ of: Graph; kind: "ready" | "failed"; ms?: number; detail?: string } | null>(null);
  const state = outcome?.of === graph ? outcome : { kind: "layout" as const, ms: undefined, detail: undefined };
  // The labels are read when the drawing is made; a language change redraws.
  const levelText = (l: string) => t(`topology.level.${l}` as MessageKey);
  const deducedText = t("topology.level.unknown");

  useEffect(() => {
    const started = performance.now();
    const d = draw(box.current, graph, { deduced: deducedText, level: levelText }, (id) => void navigate(`/objects/${encodeURIComponent(id)}`));
    drawingRef.current = d;
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
    // levelText only reads `t`, which changes with the language.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [graph, deducedText]);

  return (
    <>
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
        data-headless={graphEnv.headless ? "true" : undefined}
      />
    </>
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

function LinksTable({ rows, topology }: { rows: readonly LinkRow[]; topology: Topology }) {
  const { t } = useI18n();
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
          </tr>
        </thead>
        <tbody>
          {sorted.map((r) => (
            <tr key={r.key} data-class={r.klass}>
              <td data-label={t("topology.col.from")}>
                <End device={{ id: r.a.deviceId, name: nameOf(r.a.deviceId) }} port={r.a} />
              </td>
              <td data-label={t("topology.col.to")}>
                <End device={{ id: r.b.deviceId, name: nameOf(r.b.deviceId) }} port={r.b} />
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
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

/** A device and the port on it, each a link to its own page. */
function End({ device, port }: { device: { id: string; name: string }; port: { id: string; name: string } }) {
  return (
    <>
      <ObjectLink id={device.id} display={device.name} />
      {" / "}
      <ObjectLink id={port.id} display={port.name} />
    </>
  );
}
