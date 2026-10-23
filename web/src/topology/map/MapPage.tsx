import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { MapNode, TopologyMap } from "../../api/types";
import { Async } from "../../components/Async";
import { useCrumbs } from "../../components/crumbs";
import { Alert, EmptyState, type Crumb } from "../../components/ui";
import { useI18n, type MessageKey, type PluralKey } from "../../i18n";
import { prefersReducedMotion } from "../../motion";
import { ageOf, isStale } from "../live";
import { IsoScene } from "./IsoScene";
import { MapLegend } from "./MapLegend";
import { MapLinksTable } from "./MapLinksTable";
import { MapPanel } from "./MapPanel";
import { mapSearch, parseAddress, type Level, type MapAddress } from "./url";
import { useTopologyMap } from "./useTopologyMap";
import type { Rot } from "./iso";

/** Where "up" goes from a level: the nearest place of the trail, or the world. */
export function parentOf(m: TopologyMap): { level: Level; id?: string; from?: string } {
  const last = m.trail[m.trail.length - 1];
  if (m.level === "room" && m.focus?.type === "site") return { level: "site", id: m.focus.id, from: `area:${m.focus.id}` };
  if (!last) return { level: "global", from: m.level === "site" ? m.focus?.id : m.level === "unplaced" ? "unplaced" : undefined };
  return { level: last.type === "room" ? "room" : "site", id: last.id, from: m.focus?.id };
}

const levelLabel = (t: ReturnType<typeof useI18n>["t"], m: TopologyMap) =>
  t(`topology.map.level.${m.level}` as MessageKey, { name: m.focus?.display ?? "" });

function useNow(everyMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), everyMs);
    return () => window.clearInterval(id);
  }, [everyMs]);
  return now;
}

/** The map view of /topology: one level at a time, drilled down from the world to a rack. */
export function MapPage() {
  const { t, tp } = useI18n();
  const [params, setParams] = useSearchParams();
  const addr = useMemo(() => parseAddress(params), [params]);
  const q = useTopologyMap(addr.level, addr.id);
  const [flow, setFlow] = useState(true);
  const [nav, setNav] = useState<{ from?: string; move: boolean }>({ move: false });
  const stage = useRef<HTMLDivElement>(null);

  const go = (next: Partial<MapAddress> & { level: Level }, opts: { push: boolean; from?: string; move: boolean }) => {
    const s = mapSearch({ level: next.level, id: next.id, sel: next.sel, rot: next.rot ?? addr.rot });
    setNav({ from: opts.from, move: opts.move });
    setParams(s ? new URLSearchParams(s) : new URLSearchParams(), { replace: !opts.push });
  };
  const here = { level: addr.level, id: addr.id, rot: addr.rot };
  const select = (id: string | undefined) => go({ ...here, sel: id }, { push: false, move: false });
  const drill = (n: MapNode) => {
    if (n.drill) go({ level: n.drill.level as Level, id: n.drill.id }, { push: true, move: true });
  };
  const turn = (by: number) => go({ ...here, sel: addr.sel, rot: ((addr.rot + by + 4) % 4) as Rot }, { push: false, move: false });

  const m = q.data;
  const trail: Crumb[] = [{ label: t("topology.title"), to: m && m.level !== "global" ? "/topology" : undefined }];
  if (m) {
    for (const p of m.trail) trail.push({ label: p.display, to: `/topology?${mapSearch({ level: p.type === "room" ? "room" : "site", id: p.id, rot: 0 })}` });
    if (m.level === "room" && m.focus?.type === "site") trail.push({ label: m.focus.display, to: `/topology?${mapSearch({ level: "site", id: m.focus.id, rot: 0 })}` }, { label: t("topology.map.area") });
    else if (m.focus) trail.push({ label: m.focus.display });
    else if (m.level === "unplaced") trail.push({ label: t("topology.map.level.unplaced") });
  }
  useCrumbs(trail);

  const now = useNow(5000);
  const reduced = prefersReducedMotion();

  const closePanel = () => {
    const id = addr.sel;
    select(undefined);
    if (id) window.setTimeout(() => [...(stage.current?.querySelectorAll<SVGGElement>(".iso-item") ?? [])].find((el) => el.dataset.id === id)?.focus(), 0);
  };

  return (
    <>
      <p>{t("topology.map.intro")}</p>
      {addr.invalid ? (
        <Alert tone="info" role="status">
          {t("topology.map.invalid")}
        </Alert>
      ) : null}
      <Async query={q}>
        {(map) => {
          const up = parentOf(map);
          const selected = addr.sel && (map.nodes.some((n) => n.id === addr.sel) || map.edges.some((e) => e.id === addr.sel)) ? addr.sel : undefined;
          const age = ageOf(map.live, now);
          const stale = isStale({ ...map.live, links: {}, sources: [] }, now);
          const notes = map.notes;
          return (
            <div className="iso-stage" ref={stage}>
              <div className="iso-main">
                <div className="iso-bar">
                  <h2 className="iso-title">{levelLabel(t, map)}</h2>
                  <p className="iso-live" role="status" data-stale={map.live.enabled ? String(stale) : "off"}>
                    {map.live.enabled ? (
                      <>
                        <span className="live-dot" aria-hidden="true" />
                        {age === undefined ? t("topology.live.never") : stale ? t("topology.live.stale", { age }) : t("topology.map.live.fresh", { age })}
                      </>
                    ) : (
                      t("topology.map.live.off")
                    )}
                  </p>
                </div>
                <div className="iso-card card" data-busy={q.isPlaceholderData}>
                  <div className="iso-tools" role="group" aria-label={t("topology.map.controls")}>
                    <button type="button" className="button" disabled={map.level === "global"} onClick={() => go(up, { push: true, from: up.from, move: true })}>
                      {t("topology.map.up")}
                    </button>
                    <button type="button" className="button" onClick={() => turn(-1)} aria-label={t("topology.map.turnLeft")}>
                      ↶
                    </button>
                    <button type="button" className="button" onClick={() => turn(1)} aria-label={t("topology.map.turnRight")}>
                      ↷
                    </button>
                    <button type="button" className="button" aria-label={t("topology.map.zoomIn")} onClick={(e) => zoom(e.currentTarget, "in")}>
                      +
                    </button>
                    <button type="button" className="button" aria-label={t("topology.map.zoomOut")} onClick={(e) => zoom(e.currentTarget, "out")}>
                      −
                    </button>
                    <button type="button" className="button" onClick={(e) => zoom(e.currentTarget, "fit")}>
                      {t("topology.map.fit")}
                    </button>
                    {map.live.enabled ? (
                      <label className="check">
                        <input type="checkbox" checked={flow && !reduced} disabled={reduced} onChange={(e) => setFlow(e.target.checked)} />
                        <span>{t("topology.live.flow")}</span>
                      </label>
                    ) : null}
                  </div>
                  {map.nodes.length === 0 ? (
                    <EmptyState icon="network">{t("topology.map.empty")}</EmptyState>
                  ) : (
                    <IsoScene
                      map={map}
                      rot={addr.rot}
                      selected={selected}
                      onSelect={select}
                      onDrill={drill}
                      onUp={() => map.level !== "global" && go(up, { push: true, from: up.from, move: true })}
                      flowOn={flow && map.live.enabled}
                      restoreFocus={nav.from}
                      moveFocus={nav.move}
                      busy={q.isPlaceholderData}
                      since={q.dataUpdatedAt}
                      label={t("topology.map.scene", { level: levelLabel(t, map) })}
                    />
                  )}
                  <p className="hint iso-help">{t("topology.map.help")}</p>
                </div>
                {reduced && map.live.enabled ? <p className="hint">{t("topology.live.flowReduced")}</p> : null}
                {map.truncated ? <Alert tone="warn" role="status">{t("topology.map.truncated")}</Alert> : null}
                <ul className="iso-notes" aria-label={t("topology.map.notes")}>
                  {notes.unplaced_devices > 0 ? (
                    <li>
                      {tpNote("unplaced", notes.unplaced_devices)}
                      {map.level !== "unplaced" ? (
                        <>
                          {" "}
                          <Link to="/topology?level=unplaced">{t("topology.map.notes.unplacedOpen")}</Link>
                        </>
                      ) : null}
                    </li>
                  ) : null}
                  {notes.links_without_place > 0 ? <li>{tpNote("linksWithoutPlace", notes.links_without_place)}</li> : null}
                  {notes.unresolved_tunnels > 0 ? <li>{tpNote("unresolvedTunnels", notes.unresolved_tunnels)}</li> : null}
                  {notes.ambiguous_placements > 0 ? <li>{tpNote("ambiguous", notes.ambiguous_placements)}</li> : null}
                </ul>
                <MapLegend live={map.live.enabled} uplinks={map.edges.some((e) => e.medium === "uplinked_to")} tunnels={map.edges.some((e) => e.medium === "tunnel")} />
                <MapLinksTable map={map} selected={selected} onSelect={select} />
              </div>
              {selected ? (
                <div
                  className="iso-side"
                  onKeyDown={(e) => {
                    if (e.key === "Escape") {
                      e.stopPropagation();
                      closePanel();
                    }
                  }}
                >
                  <MapPanel map={map} selected={selected} onClose={closePanel} onDrill={drill} />
                </div>
              ) : null}
            </div>
          );
        }}
      </Async>
    </>
  );

  function tpNote(kind: "unplaced" | "linksWithoutPlace" | "unresolvedTunnels" | "ambiguous", n: number) {
    return tp(`topology.map.notes.${kind}` as PluralKey, n);
  }
}

/** The zoom buttons talk to the scene through its card (the scene owns the viewBox). */
function zoom(from: HTMLElement, action: "in" | "out" | "fit") {
  from.closest(".iso-card")?.dispatchEvent(new CustomEvent("iso-zoom", { detail: action }));
}
