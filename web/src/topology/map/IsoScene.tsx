import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointer } from "react";
import type { MapNode, TopologyMap } from "../../api/types";
import { useI18n, type MessageKey } from "../../i18n";
import { prefersReducedMotion } from "../../motion";
import { capacityWidth, planFlows, type Direction, type SpeedClass } from "./flow";
import type { IsoBox, Rot } from "./iso";
import { nextSpot, type Arrow } from "./nav";
import { buildScene, type SceneEdge, type SceneFrame, type SceneItem } from "./scene";

export interface IsoSceneProps {
  map: TopologyMap;
  rot: Rot;
  selected?: string;
  onSelect: (id: string | undefined) => void;
  onDrill: (node: MapNode) => void;
  onUp: () => void;
  /** The visitor's animation switch. */
  flowOn: boolean;
  /** The id to give the focus back to after a change of level (the place one came from). */
  restoreFocus?: string;
  /** The change of level was asked by the visitor: the focus follows. */
  moveFocus: boolean;
  busy: boolean;
  /** When the map's data arrived (ms since the epoch). */
  since: number;
  label: string;
}

const DASH: Record<string, string | undefined> = { declared: undefined, observed: "7 4", deduced: "2 5" };
const ZOOM_MIN = 0.5;
const ZOOM_MAX = 6;
const CLOUD = "M-20,8 a9,9 0 0 1 3,-17 a12,12 0 0 1 23,-4 a10,10 0 0 1 12,21 z";

function usePageHidden(): boolean {
  const [hidden, setHidden] = useState(() => typeof document !== "undefined" && document.hidden);
  useEffect(() => {
    const on = () => setHidden(document.hidden);
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, []);
  return hidden;
}

/** The isometric scene of one map level, painted from scene.ts's data. */
export function IsoScene(p: IsoSceneProps) {
  const { t, tp } = useI18n();
  const hidden = usePageHidden();
  const scene = useMemo(() => buildScene(p.map, p.rot), [p.map, p.rot]);
  const svg = useRef<SVGSVGElement>(null);
  const els = useRef(new Map<string, SVGGElement>());
  const [hover, setHover] = useState<string | undefined>();
  const [focused, setFocused] = useState<string | undefined>();
  const sceneKey = `${p.map.level}|${p.map.focus?.id ?? ""}|${p.rot}`;
  // View and roving focus belong to one scene: another level or rotation starts them afresh.
  const [viewState, setViewState] = useState({ key: sceneKey, k: 1, px: 0, py: 0 });
  const [curState, setCurState] = useState<{ key: string; id?: string }>({ key: sceneKey });
  const first = scene.items[0]?.id;
  const initial = p.restoreFocus && scene.items.some((i) => i.id === p.restoreFocus) ? p.restoreFocus : first;
  const cur = curState.key === sceneKey && curState.id !== undefined && scene.items.some((i) => i.id === curState.id) ? curState.id : initial;
  const setCur = (id: string) => setCurState({ key: sceneKey, id });
  const view = viewState.key === sceneKey ? viewState : { key: sceneKey, k: 1, px: 0, py: 0 };
  const setView = useCallback(
    (f: { k: number; px: number; py: number } | ((v: { k: number; px: number; py: number }) => { k: number; px: number; py: number })) =>
      setViewState((cur) => {
        const base = cur.key === sceneKey ? cur : { key: sceneKey, k: 1, px: 0, py: 0 };
        return { key: sceneKey, ...(typeof f === "function" ? f(base) : f) };
      }),
    [sceneKey],
  );
  const suppress = useRef(false);
  const drag = useRef<{ x: number; y: number; px: number; py: number; moved: boolean } | null>(null);

  const reduced = prefersReducedMotion();
  const lanes = useMemo(
    () => planFlows(scene.edges.map((e) => e.edge), { on: p.flowOn, reduced, hidden }),
    [scene, p.flowOn, reduced, hidden],
  );
  const laneOf = useMemo(() => {
    const m = new Map<string, Map<Direction, SpeedClass>>();
    for (const l of lanes) {
      const d = m.get(l.edge) ?? new Map<Direction, SpeedClass>();
      d.set(l.dir, l.cls);
      m.set(l.edge, d);
    }
    return m;
  }, [lanes]);

  // A visitor who changed level gets the focus on the place they came from, or on the first one.
  useLayoutEffect(() => {
    if (p.moveFocus && initial) els.current.get(initial)?.focus({ preventScroll: true });
    // Only a new scene moves the focus, not a refresh of the same one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sceneKey]);
  // How long the scene took from the data's arrival to the commit (read by the end-to-end tests).
  useLayoutEffect(() => {
    if (svg.current) svg.current.dataset.drawnMs = String(Math.max(0, Date.now() - p.since));
  }, [scene, p.since]);

  const b = scene.bounds;
  const W = Math.max(b.x1 - b.x0, 1);
  const H = Math.max(b.y1 - b.y0, 1);
  const vw = W / view.k;
  const vh = H / view.k;
  const vb = { x: (b.x0 + b.x1) / 2 + view.px - vw / 2, y: (b.y0 + b.y1) / 2 + view.py - vh / 2, w: vw, h: vh };
  const viewBox = `${vb.x.toFixed(1)} ${vb.y.toFixed(1)} ${vw.toFixed(1)} ${vh.toFixed(1)}`;

  const zoomBy = useCallback((f: number, at?: { x: number; y: number }) => {
    setView((v) => {
      const k = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, v.k * f));
      if (!at) return { ...v, k };
      // Keep the point under the pointer where it is.
      const ratio = 1 - v.k / k;
      return { k, px: v.px + (at.x - (b.x0 + b.x1) / 2 - v.px) * ratio, py: v.py + (at.y - (b.y0 + b.y1) / 2 - v.py) * ratio };
    });
  }, [b, setView]);
  // Exposed to the toolbar through the DOM: the buttons live in MapPage.
  useEffect(() => {
    const el = svg.current?.closest(".iso-card");
    if (!el) return;
    const on = (e: Event) => {
      const a = (e as CustomEvent<string>).detail;
      if (a === "in") zoomBy(1.25);
      else if (a === "out") zoomBy(0.8);
      else setView({ k: 1, px: 0, py: 0 });
    };
    el.addEventListener("iso-zoom", on);
    return () => el.removeEventListener("iso-zoom", on);
  }, [zoomBy, setView]);

  const toScene = (cx: number, cy: number) => {
    const r = svg.current?.getBoundingClientRect() ?? { left: 0, top: 0, width: vb.w, height: vb.h };
    const s = Math.min(r.width / vb.w, r.height / vb.h) || 1;
    return { x: vb.x + (cx - r.left - (r.width - vb.w * s) / 2) / s, y: vb.y + (cy - r.top - (r.height - vb.h * s) / 2) / s };
  };
  useEffect(() => {
    const el = svg.current;
    if (!el) return;
    const on = (e: WheelEvent) => {
      e.preventDefault();
      zoomBy(Math.exp(-e.deltaY * 0.0015), toScene(e.clientX, e.clientY));
    };
    el.addEventListener("wheel", on, { passive: false });
    return () => el.removeEventListener("wheel", on);
  });

  const onDown = (e: ReactPointer<SVGSVGElement>) => {
    if (e.button !== 0) return;
    drag.current = { x: e.clientX, y: e.clientY, px: view.px, py: view.py, moved: false };
  };
  const onMove = (e: ReactPointer<SVGSVGElement>) => {
    const d = drag.current;
    if (!d) return;
    const dx = e.clientX - d.x;
    const dy = e.clientY - d.y;
    if (!d.moved && Math.hypot(dx, dy) < 5) return;
    if (!d.moved) svg.current?.setPointerCapture(e.pointerId);
    d.moved = true;
    const r = svg.current?.getBoundingClientRect() ?? { width: vb.w, height: vb.h };
    const s = Math.min(r.width / vb.w, r.height / vb.h) || 1;
    setView((v) => ({ ...v, px: d.px - dx / s, py: d.py - dy / s }));
  };
  const onUpPointer = () => {
    // The click that ends a drag must not open what is under the pointer.
    const moved = drag.current?.moved;
    drag.current = null;
    if (!moved) return;
    suppress.current = true;
    window.setTimeout(() => (suppress.current = false), 0);
  };

  const act = (it: SceneItem) => {
    if (suppress.current) return;
    if (it.drillable) p.onDrill(it.node);
    else p.onSelect(it.id);
  };
  const onKey = (e: KeyboardEvent<SVGGElement>, it: SceneItem) => {
    if (e.key === "Enter") {
      e.preventDefault();
      act(it);
    } else if (e.key === " ") {
      e.preventDefault();
      p.onSelect(p.selected === it.id ? undefined : it.id);
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      if (p.selected) p.onSelect(undefined);
      else p.onUp();
    } else if (e.key === "ArrowUp" || e.key === "ArrowDown" || e.key === "ArrowLeft" || e.key === "ArrowRight") {
      e.preventDefault();
      const next = nextSpot(scene.items.map((i) => ({ id: i.id, at: i.iso.anchor })), it.id, e.key as Arrow);
      if (next) {
        setCur(next);
        els.current.get(next)?.focus({ preventScroll: true });
      }
    } else if (e.key === "+" || e.key === "=") zoomBy(1.25);
    else if (e.key === "-") zoomBy(0.8);
    else if (e.key === "0") setView({ k: 1, px: 0, py: 0 });
  };

  const active = hover ?? p.selected;
  const hotEdge = (e: SceneEdge) => (active === undefined ? "" : active === e.edge.id || active === e.edge.a || active === e.edge.b ? " is-hot" : " is-dim");
  const kindLabel = (n: MapNode) => t(`topology.map.kind.${n.kind}` as MessageKey);
  const healthLabel = (n: MapNode) => t(`healthState.${n.health.worst}` as MessageKey);
  const summary = (n: MapNode): string | undefined => {
    const c = n.counts;
    if (n.kind === "site") return `${tp("topology.map.count.rooms", c.rooms)} · ${tp("topology.map.count.racks", c.racks)}`;
    if (n.kind === "room" || n.kind === "area") return tp("topology.map.count.racks", c.racks);
    if (n.kind === "rack") return tp("topology.map.count.devices", c.devices);
    return undefined;
  };

  return (
    <svg
      ref={svg}
      className="iso-svg"
      role="group"
      aria-label={p.label}
      aria-busy={p.busy}
      viewBox={viewBox}
      preserveAspectRatio="xMidYMid meet"
      data-state={p.busy ? "loading" : "drawn"}
      data-level={p.map.level}
      data-items={scene.items.length}
      data-lanes={lanes.length}
      data-rot={p.rot}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUpPointer}
      onPointerCancel={onUpPointer}
      onClick={(e) => {
        if (e.target === e.currentTarget || (e.target as Element).classList.contains("iso-plate")) {
          if (!suppress.current) p.onSelect(undefined);
        }
      }}
    >
      <defs>
        <filter id="iso-blur" x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="5" />
        </filter>
        <linearGradient id="iso-grad-top" x1="0" y1="0" x2="1" y2="1">
          <stop className="iso-stop-hi" offset="0" />
          <stop className="iso-stop-lo" offset="1" />
        </linearGradient>
      </defs>
      <g className="iso-plates">
        {scene.plates.map((pl) => (
          <g key={pl.id} className={pl.dashed ? "iso-plate-group is-dashed" : "iso-plate-group"}>
            <PlateFaces iso={pl.iso} />
            {pl.grid.length > 0 ? <path className="iso-grid" d={pl.grid.join("")} /> : null}
          </g>
        ))}
      </g>
      {scene.frame ? (
        <g className="iso-frame iso-frame-back">
          {scene.frame.back.map((b, i) => (
            <Post key={i} box={b} />
          ))}
        </g>
      ) : null}
      <g className="iso-items">
        {scene.items.map((it, n) => (
          <g
            key={it.id}
            ref={(el) => {
              if (el) els.current.set(it.id, el);
              else els.current.delete(it.id);
            }}
            className={`iso-item iso-${it.role} h-${it.node.health.worst}${p.selected === it.id ? " is-selected" : ""}${hover === it.id ? " is-hover" : ""}${it.drillable ? " is-drill" : ""}`}
            data-id={it.id}
            data-kind={it.node.kind}
            data-i={n % 12}
            role="button"
            tabIndex={cur === it.id ? 0 : -1}
            aria-label={t(it.drillable ? "topology.map.itemDrill" : "topology.map.item", { kind: kindLabel(it.node), name: it.label, health: healthLabel(it.node) })}
            aria-pressed={p.selected === it.id}
            onClick={() => act(it)}
            onKeyDown={(e) => onKey(e, it)}
            onFocus={() => {
              setCur(it.id);
              setFocused(it.id);
            }}
            onBlur={() => setFocused((f) => (f === it.id ? undefined : f))}
            onPointerEnter={() => setHover(it.id)}
            onPointerLeave={() => setHover(undefined)}
          >
            {it.role !== "device" && it.role !== "shelf" && it.parts.length === 0 ? <polygon className="iso-shadow" points={it.iso.foot} transform="translate(12 8)" filter="url(#iso-blur)" /> : null}
            <Faces item={it} />
            {it.parts.map((b, i) => (
              <Post key={i} box={b} />
            ))}
            {it.minis.map((m, i) => (
              <g key={i} className="iso-mini">
                <polygon className="iso-face iso-south" points={m.south} />
                <polygon className="iso-face iso-east" points={m.east} />
                <polygon className="iso-face iso-top" points={m.top} />
              </g>
            ))}
            {it.role === "internet" ? <path className="iso-cloud" d={CLOUD} transform={`translate(${it.iso.anchor.x} ${it.iso.anchor.y - 18})`} /> : null}
            {it.arrow ? <polygon className="iso-front" points={it.arrow} /> : null}
            <g
              className="iso-info"
              aria-hidden="true"
              transform={`translate(${it.iso.bounds.x1 - 4} ${it.iso.bounds.y0 + 2})`}
              onClick={(e) => {
                e.stopPropagation();
                p.onSelect(p.selected === it.id ? undefined : it.id);
              }}
            >
              <circle r="9" />
              <text textAnchor="middle" y="4">i</text>
            </g>
          </g>
        ))}
      </g>
      {scene.frame ? <FrameFront frame={scene.frame} /> : null}
      <g className="iso-edges">
        {scene.edges.map((e) => {
          const lane = laneOf.get(e.edge.id);
          const w = capacityWidth(e.edge.capacity_mbps);
          const conf = DASH[e.edge.confidence];
          return (
            <g
              key={e.edge.id}
              className={`iso-edge u-${e.util} m-${e.edge.medium}${p.selected === e.edge.id ? " is-selected" : ""}${hotEdge(e)}`}
              data-id={e.edge.id}
              data-medium={e.edge.medium}
              aria-hidden="true"
              onPointerEnter={() => setHover(e.edge.id)}
              onPointerLeave={() => setHover(undefined)}
              onClick={(ev) => {
                ev.stopPropagation();
                p.onSelect(p.selected === e.edge.id ? undefined : e.edge.id);
              }}
            >
              <path className="iso-edge-hit" d={e.d} />
              <path className="iso-edge-casing" d={e.d} strokeWidth={w + 3} />
              <path className="iso-edge-base" d={e.d} strokeWidth={w} strokeDasharray={e.util === "down" ? "5 4" : conf} />
              {e.util === "down" ? <circle className="iso-edge-x" cx={e.mid.x} cy={e.mid.y} r="5" /> : null}
              {e.lanes.map((l) => {
                const cls = lane?.get(l.dir);
                return cls ? (
                  <g key={l.dir}>
                    <path className={`iso-flow-glow s${cls} ${l.dir}`} d={l.d} strokeWidth={Math.min(w, 3) + 6} />
                    <path className={`iso-flow s${cls} ${l.dir}`} d={l.d} strokeWidth={Math.min(w, 3) + 1} />
                  </g>
                ) : null;
              })}
            </g>
          );
        })}
      </g>
      <g className="iso-label-layer">
        {scene.items.map((it) => (
          <Label
            key={it.id}
            item={it}
            shown={it.labelShown || hover === it.id || p.selected === it.id || focused === it.id}
            sub={it.subShown && it.hasSub ? (it.sub ?? summary(it.node)) : undefined}
          />
        ))}
      </g>
    </svg>
  );
}

function Post({ box }: { box: IsoBox }) {
  return (
    <g className="iso-post">
      <polygon className="iso-face iso-south" points={box.south} />
      <polygon className="iso-face iso-east" points={box.east} />
      <polygon className="iso-face iso-top" points={box.top} />
    </g>
  );
}

/** The posts at the front of a rack, its top rail and the U ticks: drawn after the devices. */
function FrameFront({ frame }: { frame: SceneFrame }) {
  return (
    <g className="iso-frame iso-frame-front" aria-hidden="true">
      <polygon className="iso-rail" points={frame.top} />
      {frame.front.map((b, i) => (
        <Post key={i} box={b} />
      ))}
      <path className="iso-ticks" d={frame.ticks} />
      {frame.marks.map((m) => (
        <text key={m.text} className="iso-tick-label" x={m.x} y={m.y} textAnchor="end">
          {m.text}
        </text>
      ))}
    </g>
  );
}

function PlateFaces({ iso }: { iso: IsoBox }) {
  return (
    <>
      <polygon className="iso-plate iso-plate-south" points={iso.south} />
      <polygon className="iso-plate iso-plate-east" points={iso.east} />
      <polygon className="iso-plate iso-plate-top" points={iso.top} />
    </>
  );
}

function Faces({ item }: { item: SceneItem }) {
  const { iso } = item;
  return (
    <>
      <polygon className="iso-face iso-south" points={iso.south} />
      <polygon className="iso-face iso-east" points={iso.east} />
      <polygon className="iso-face iso-top" points={iso.top} />
    </>
  );
}

function Label({ item, sub, shown }: { item: SceneItem; sub?: string; shown: boolean }) {
  const pl = item.pill;
  const h = sub ? 34 : 21;
  const x = pl.x0;
  const y = item.labelAt.y - 14;
  return (
    <g className={`iso-labels h-${item.node.health.worst}${shown ? "" : " is-hidden"}`} data-for={item.id} aria-hidden="true">
      {item.labelStart ? <line className="iso-leader" x1={item.iso.bounds.x1} y1={item.labelAt.y - 3} x2={x} y2={item.labelAt.y - 3} /> : null}
      <rect className="iso-pill" x={x} y={y} width={Math.max(pl.x1 - pl.x0, 1)} height={h} rx={h > 25 ? 10 : 10.5} />
      <circle className="iso-lamp" cx={x + 11} cy={y + 10.5} r="3.5" />
      <text className="iso-label" x={x + 20} y={y + 14.5}>
        {item.label}
      </text>
      {sub ? (
        <text className="iso-sub" x={x + 20} y={y + 28}>
          {sub}
        </text>
      ) : null}
    </g>
  );
}
