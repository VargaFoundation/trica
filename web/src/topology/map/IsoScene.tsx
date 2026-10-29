import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointer } from "react";
import type { MapNode, TopologyMap } from "../../api/types";
import { useI18n, type MessageKey } from "../../i18n";
import { prefersReducedMotion } from "../../motion";
import { IconGlyph } from "../../components/ui/icons";
import { planFlows, sheen, STATE_DASH, type Direction, type SpeedClass } from "./flow";
import type { IsoBox, Rot } from "./iso";
import { isGate } from "./layout";
import { nextSpot, type Arrow } from "./nav";
import { buildScene, MEDIUM_DASH, textScale, type SceneBadge, type SceneEdge, type SceneFrame, type SceneItem } from "./scene";

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

/** How deep a level is: going to a deeper one flies in, to a shallower one backs out. */
const LEVEL_DEPTH: Record<string, number> = { global: 0, site: 1, unplaced: 1, room: 2, rack: 3 };
const ZOOM_MIN = 0.5;
const ZOOM_MAX = 6;
/** The most screen px one scene px is drawn at when the view fits a level: a lone device stays its size. */
export const FIT_MAX = 1.6;
/** Room kept round the framed blocks for their names, in scene px; more on top, where the controls float. */
const FIT_PAD = 48;
const FIT_PAD_TOP = 88;
/** Below the blocks: their names, and the hint that floats at the bottom. */
const FIT_PAD_BOTTOM = 96;

/** The stage's size in px, once it is laid out (0 by 0 before, and where nothing is laid out). */
function useSize(el: React.RefObject<Element | null>): { w: number; h: number } {
  const [size, setSize] = useState({ w: 0, h: 0 });
  useLayoutEffect(() => {
    const node = el.current;
    if (!node) return;
    const read = () => {
      const r = node.getBoundingClientRect();
      setSize((s) => (Math.abs(s.w - r.width) < 1 && Math.abs(s.h - r.height) < 1 ? s : { w: r.width, h: r.height }));
    };
    read();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(read);
    ro.observe(node);
    return () => ro.disconnect();
  }, [el]);
  return size;
}

/**
 * What the view frames at zoom 1: the level's blocks with room for their names, widened when it would
 * draw them bigger than FIT_MAX on a stage of `stage` px (unknown: the blocks alone).
 */
export function fitFrame(core: { x0: number; y0: number; x1: number; y1: number }, stage: { w: number; h: number }) {
  const x0 = core.x0 - FIT_PAD;
  const x1 = core.x1 + FIT_PAD;
  const y0 = core.y0 - FIT_PAD_TOP;
  const y1 = core.y1 + FIT_PAD_BOTTOM;
  const cx = (x0 + x1) / 2;
  const cy = (y0 + y1) / 2;
  let w = x1 - x0;
  let h = y1 - y0;
  if (stage.w > 0 && stage.h > 0) {
    const s = Math.min(stage.w / w, stage.h / h);
    if (s > FIT_MAX) {
      w = stage.w / FIT_MAX;
      h = stage.h / FIT_MAX;
    }
  }
  return { x0: cx - w / 2, x1: cx + w / 2, y0: cy - h / 2, y1: cy + h / 2 };
}
const CLOUD = "M-30,12 a12,12 0 0 1 4,-23 a17,17 0 0 1 33,-6 a14,14 0 0 1 17,29 z";

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
  const svg = useRef<SVGSVGElement>(null);
  const size = useSize(svg);
  // The scene at its design size gives the frame; the one drawn has its text at one size on screen.
  const base = useMemo(() => buildScene(p.map, p.rot), [p.map, p.rot]);
  const els = useRef(new Map<string, SVGGElement>());
  const [hover, setHover] = useState<string | undefined>();
  const [focused, setFocused] = useState<string | undefined>();
  const sceneKey = `${p.map.level}|${p.map.focus?.id ?? ""}|${p.rot}`;
  // View and roving focus belong to one scene: another level or rotation starts them afresh.
  const [viewState, setViewState] = useState({ key: sceneKey, k: 1, px: 0, py: 0 });
  const [curState, setCurState] = useState<{ key: string; id?: string }>({ key: sceneKey });
  const first = base.items[0]?.id;
  const initial = p.restoreFocus && base.items.some((i) => i.id === p.restoreFocus) ? p.restoreFocus : first;
  const cur = curState.key === sceneKey && curState.id !== undefined && base.items.some((i) => i.id === curState.id) ? curState.id : initial;
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
  const b = fitFrame(base.core, size);
  const W = Math.max(b.x1 - b.x0, 1);
  const H = Math.max(b.y1 - b.y0, 1);
  const screen = size.w > 0 && size.h > 0 ? Math.min(size.w / W, size.h / H) * view.k : 1;
  const text = size.w > 0 && size.h > 0 ? textScale(screen) : 1;
  const scene = useMemo(() => (text === 1 ? base : buildScene(p.map, p.rot, { text })), [base, text, p.map, p.rot]);
  const suppress = useRef(false);
  // Where the place just opened was, as a share of the view; and how the new level comes in.
  const drillFrom = useRef<{ fx: number; fy: number } | undefined>(undefined);
  const lastLevel = useRef<{ key: string; depth: number } | undefined>(undefined);
  const [enter, setEnter] = useState<{ dir: "in" | "out"; n: number; fx: number; fy: number } | undefined>(undefined);
  useLayoutEffect(() => {
    const depth = LEVEL_DEPTH[p.map.level] ?? 0;
    const prev = lastLevel.current;
    lastLevel.current = { key: sceneKey, depth };
    const from = drillFrom.current;
    drillFrom.current = undefined;
    if (!prev || prev.key === sceneKey || prev.depth === depth) return;
    const dir = depth > prev.depth ? "in" : "out";
    setEnter((e) => ({ dir, n: (e?.n ?? 0) + 1, fx: dir === "in" && from ? from.fx : 0.5, fy: dir === "in" && from ? from.fy : 0.5 }));
    // Only a new level moves the camera, not a refresh of the same one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sceneKey]);
  // The origin of the flight, through the CSSOM (the CSP forbids style attributes).
  useLayoutEffect(() => {
    if (!enter || !svg.current) return;
    svg.current.style.setProperty("transform-origin", `${(enter.fx * 100).toFixed(1)}% ${(enter.fy * 100).toFixed(1)}%`);
  }, [enter]);
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
    if (it.drillable) {
      // The next level flies in from where this place is on screen.
      const at = it.navAt;
      drillFrom.current = { fx: Math.min(1, Math.max(0, (at.x - vb.x) / vb.w)), fy: Math.min(1, Math.max(0, (at.y - vb.y) / vb.h)) };
      p.onDrill(it.node);
    }
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
      const next = nextSpot(scene.items.map((i) => ({ id: i.id, at: i.navAt })), it.id, e.key as Arrow);
      if (next) {
        setCur(next);
        els.current.get(next)?.focus({ preventScroll: true });
      }
    } else if (e.key === "+" || e.key === "=") zoomBy(1.25);
    else if (e.key === "-") zoomBy(0.8);
    else if (e.key === "0") setView({ k: 1, px: 0, py: 0 });
  };

  const active = hover ?? p.selected;
  // What the pointed thing is linked to stays lit; the rest dims.
  const linkedToActive = useMemo(() => {
    if (active === undefined) return undefined;
    const keep = new Set<string>([active]);
    for (const e of scene.edges) {
      if (e.edge.id === active) {
        keep.add(e.edge.a);
        keep.add(e.edge.b);
      } else if (e.edge.a === active) keep.add(e.edge.b);
      else if (e.edge.b === active) keep.add(e.edge.a);
    }
    return keep;
  }, [active, scene.edges]);
  const hotEdge = (e: SceneEdge) => (active === undefined ? "" : active === e.edge.id || active === e.edge.a || active === e.edge.b ? " is-hot" : " is-dim");
  const kindLabel = (n: MapNode) => t(`topology.map.kind.${n.kind}` as MessageKey);
  const healthLabel = (n: MapNode) => t(`healthState.${n.health.worst}` as MessageKey);
  const summary = (n: MapNode): string | undefined => {
    const c = n.counts;
    if (n.kind === "site") return `${tp("topology.map.count.rooms", c.rooms)} · ${tp("topology.map.count.racks", c.racks)} · ${tp("topology.map.count.devices", c.devices)}`;
    if (n.kind === "room" || n.kind === "area") return tp("topology.map.count.racks", c.racks);
    if (n.kind === "rack") return tp("topology.map.count.devices", c.devices);
    return undefined;
  };

  const moving = p.flowOn && !reduced && !hidden;
  const edgeEl = (e: SceneEdge) => {
    const lane = laneOf.get(e.edge.id);
    return (
      <g
        key={e.edge.id}
        className={`iso-edge u-${e.util} b-${e.band} st-${e.state} m-${e.edge.medium}${p.selected === e.edge.id ? " is-selected" : ""}${hotEdge(e)}`}
        data-id={e.edge.id}
        data-medium={e.edge.medium}
        data-band={e.band}
        data-state={e.state}
        aria-hidden="true"
        onPointerEnter={() => setHover(e.edge.id)}
        onPointerLeave={() => setHover(undefined)}
        onClick={(ev) => {
          ev.stopPropagation();
          p.onSelect(p.selected === e.edge.id ? undefined : e.edge.id);
        }}
      >
        <path className="iso-edge-hit" d={e.d} />
        <path className="iso-edge-halo" d={e.d} strokeWidth={e.width + 6} />
        <path className="iso-edge-casing" d={e.d} strokeWidth={e.width + 3} />
        <path className="iso-edge-base" d={e.d} strokeWidth={e.width} strokeDasharray={MEDIUM_DASH[e.edge.medium] ?? STATE_DASH[e.state]} />
        {e.edge.medium === "tunnel" ? <path className="iso-edge-core" d={e.d} strokeWidth={Math.max(e.width - 2.4, 0.8)} strokeDasharray={MEDIUM_DASH.tunnel} /> : null}
        {e.arrows.map((pts, i) => (
          <polygon key={i} className="iso-edge-arrow" points={pts} />
        ))}
        {e.lanes.map((l) => {
          const cls = lane?.get(l.dir);
          if (!cls) return null;
          const sh = sheen(l.len, cls);
          // A soft streak runs the lane in its direction, as light in a fibre: no beads on a string.
          return (
            <g key={l.dir} className={`iso-flow s${cls} ${l.dir}`}>
              <path className={`iso-flow-glow s${cls} ${l.dir}`} d={l.d} strokeWidth={1.2} />
              <path className="iso-sheen-flow" d={l.d} strokeWidth={Math.min(e.width, 4.4) * 0.45 + 0.8} strokeDasharray={sh.dash}>
                <animate attributeName="stroke-dashoffset" from="0" to={String(-sh.period)} dur={`${sh.seconds}s`} repeatCount="indefinite" />
              </path>
            </g>
          );
        })}
        {e.pulse && moving ? (
          <circle className="iso-pulse" r="3.2">
            <animateMotion dur="3.6s" repeatCount="indefinite" path={e.d} />
          </circle>
        ) : null}
      </g>
    );
  };
  const badgeOf = (b: SceneBadge, cls: string) => (
    <g key={cls} className={`iso-badge ${cls}${b.drop ? " is-dropped" : ""}`} transform={`translate(${b.x} ${b.y}) scale(${scene.text})`}>
      <rect x={-b.w / scene.text / 2} y="-8" width={b.w / scene.text} height="15" rx="7.5" />
      <text y="3.5" textAnchor="middle">
        {b.text}
      </text>
    </g>
  );
  const edgeBadges = (e: SceneEdge) => {
    const hot = hotEdge(e) === " is-hot" || p.selected === e.edge.id;
    return (
      <g key={e.edge.id} className={`iso-edge-badges${hot ? " is-hot" : ""}${e.portsShown ? " is-shown" : ""}${hotEdge(e) === " is-dim" ? " is-dim" : ""}`} data-for={e.edge.id} aria-hidden="true">
        {e.ports.a ? badgeOf(e.ports.a, "iso-badge-port") : null}
        {e.ports.b ? badgeOf(e.ports.b, "iso-badge-port b") : null}
        {e.bundle ? badgeOf(e.bundle, "iso-badge-bundle") : null}
        {e.state === "down" ? (
          <g className="iso-badge-x" transform={`translate(${e.mid.x} ${e.mid.y})`}>
            <circle r="7" />
            <path d="M-3,-3L3,3M3,-3L-3,3" />
          </g>
        ) : null}
      </g>
    );
  };
  const itemEl = (it: SceneItem, n: number) => (
      <g
        key={it.id}
        ref={(el) => {
          if (el) els.current.set(it.id, el);
          else els.current.delete(it.id);
        }}
        className={`iso-item iso-${it.role} h-${it.node.health.worst}${p.selected === it.id ? " is-selected" : ""}${hover === it.id ? " is-hover" : ""}${it.drillable ? " is-drill" : ""}${it.card ? " has-card" : ""}${linkedToActive && !linkedToActive.has(it.id) ? " is-dim" : ""}`}
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
        {it.parts.length === 0 ? (
          it.role === "device" || it.role === "shelf" ? (
            <polygon className="iso-shadow iso-shadow-flat" points={it.iso.foot} transform="translate(5 3)" />
          ) : (
            <polygon className="iso-shadow" points={it.iso.foot} transform="translate(12 8)" filter="url(#iso-blur)" />
          )
        ) : (
          <polygon className="iso-shadow iso-shadow-flat" points={it.iso.foot} transform="translate(8 5)" />
        )}
        {it.node.health.worst === "critical" && !isGate(it.role) ? <polygon className="iso-alarm" points={it.iso.foot} filter="url(#iso-blur)" /> : null}
        <g className="iso-lift">
          {isGate(it.role) ? (
            <GateBadge item={it} />
          ) : (
            <>
              <Body item={it} />
              {it.parts.map((b, i) => (
                <Post key={i} box={b} />
              ))}
              {it.arrow ? <polygon className="iso-front" points={it.arrow} /> : null}
              <polygon className="iso-hull" points={it.iso.hull} />
            </>
          )}
        </g>
        {it.card ? <Card item={it} /> : null}
        <g
          className="iso-info"
          aria-hidden="true"
          transform={`translate(${(it.card ? it.card.x + it.card.w : it.iso.bounds.x1) - 4} ${(it.card ? it.card.y : it.iso.bounds.y0) + 2})`}
          onClick={(e) => {
            e.stopPropagation();
            p.onSelect(p.selected === it.id ? undefined : it.id);
          }}
        >
          <circle r="9" />
          <text textAnchor="middle" y="4">i</text>
        </g>
      </g>
  );
  const floorEdges = scene.edges.filter((e) => e.floor);
  const airEdges = scene.edges.filter((e) => !e.floor);

  return (
    <svg
      ref={svg}
      role="group"
      aria-label={p.label}
      aria-busy={p.busy}
      viewBox={viewBox}
      preserveAspectRatio="xMidYMid meet"
      data-state={p.busy ? "loading" : "drawn"}
      data-enter={enter?.dir}
      className={`iso-svg${enter ? ` is-enter-${enter.dir}-${enter.n % 2}` : ""}`}
      data-level={p.map.level}
      data-items={scene.items.length}
      data-lanes={lanes.length}
      data-rot={p.rot}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUpPointer}
      onPointerCancel={onUpPointer}
      onClick={(e) => {
        if (e.target === e.currentTarget || (e.target as Element).classList.contains("iso-plate") || (e.target as Element).classList.contains("iso-ground")) {
          if (!suppress.current) p.onSelect(undefined);
        }
      }}
    >
      <defs>
        <filter id="iso-blur" x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="5" />
        </filter>
        {/* Light from the back left: the side faces darken towards the ground, the tops catch a sheen at the back. */}
        <linearGradient id="iso-shade-side" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#000" stopOpacity="0" />
          <stop offset="1" stopColor="#000" stopOpacity="0.24" />
        </linearGradient>
        <linearGradient id="iso-sheen" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#fff" stopOpacity="0.16" />
          <stop offset="0.6" stopColor="#fff" stopOpacity="0" />
        </linearGradient>
        <linearGradient id="iso-grad-top" x1="0" y1="0" x2="1" y2="1">
          <stop className="iso-stop-hi" offset="0" />
          <stop className="iso-stop-lo" offset="1" />
        </linearGradient>
        <radialGradient id="iso-ground-fade" gradientUnits="userSpaceOnUse" cx={scene.ground.cx} cy={scene.ground.cy} r={scene.ground.r}>
          <stop offset="0.45" stopColor="#fff" />
          <stop offset="1" stopColor="#000" />
        </radialGradient>
        <mask id="iso-ground-mask" maskUnits="userSpaceOnUse" x={scene.bounds.x0 - 400} y={scene.bounds.y0 - 400} width={scene.bounds.x1 - scene.bounds.x0 + 800} height={scene.bounds.y1 - scene.bounds.y0 + 800}>
          <rect x={scene.bounds.x0 - 400} y={scene.bounds.y0 - 400} width={scene.bounds.x1 - scene.bounds.x0 + 800} height={scene.bounds.y1 - scene.bounds.y0 + 800} fill="url(#iso-ground-fade)" />
        </mask>
      </defs>
      <g className="iso-ground-layer" mask="url(#iso-ground-mask)" aria-hidden="true">
        <polygon className="iso-ground" points={scene.ground.poly} />
        <path className="iso-ground-grid" d={scene.ground.grid} />
      </g>
      <g className="iso-plates">
        {scene.plates.map((pl) => (
          <g key={pl.id} className={pl.dashed ? "iso-plate-group is-dashed" : "iso-plate-group"}>
            <PlateFaces iso={pl.iso} />
            {pl.grid.length > 0 ? <path className="iso-grid" d={pl.grid.join("")} /> : null}
          </g>
        ))}
        {scene.trays.map((t, i) => (
          <g key={i} className="iso-tray">
            <polygon className="iso-tray-side" points={t.south} />
            <polygon className="iso-tray-top" points={t.top} />
          </g>
        ))}
      </g>
      {floorEdges.length > 0 ? <g className="iso-edges iso-edges-floor">{floorEdges.map(edgeEl)}</g> : null}
      {scene.frame ? (
        <g className="iso-frame iso-frame-back">
          {scene.frame.panels.map((pts, i) => (
            <polygon key={`p${i}`} className="iso-panel" points={pts} />
          ))}
          {scene.frame.back.map((b, i) => (
            <Post key={i} box={b} />
          ))}
        </g>
      ) : null}
      <g className="iso-items">{scene.items.map((it, n) => (isGate(it.role) ? null : itemEl(it, n)))}</g>
      {scene.frame ? <FrameFront frame={scene.frame} /> : null}
      <g className="iso-edges">{airEdges.map(edgeEl)}</g>
      {/* The Internet and the other places are badges at the end of their lines: drawn over them. */}
      <g className="iso-items iso-gates">{scene.items.map((it, n) => (isGate(it.role) ? itemEl(it, n) : null))}</g>
      <g className="iso-badge-layer">{scene.edges.map(edgeBadges)}</g>
      <g className="iso-label-layer">
        {scene.items.map((it) =>
          it.card ? null : (
            <Label
              key={it.id}
              item={it}
              text={scene.text}
              shown={it.labelShown || hover === it.id || p.selected === it.id || focused === it.id}
              sub={it.subShown && it.hasSub ? (it.sub ?? summary(it.node)) : undefined}
            />
          ),
        )}
      </g>
    </svg>
  );
}

/** The radius of a gate's badge, from the size of the gate it stands for. */
export const gateRadius = (it: Pick<SceneItem, "iso">) => Math.max(14, Math.min(26, Math.min(it.iso.bounds.x1 - it.iso.bounds.x0, it.iso.bounds.y1 - it.iso.bounds.y0) * 0.42));

/** The Internet, as a cloud, or another place, as a building: a round badge where its lines end. */
function GateBadge({ item: it }: { item: SceneItem }) {
  const r = gateRadius(it);
  const { x, y } = it.port;
  const k = (r * 1.25) / 64;
  return (
    <g className="iso-gate">
      <ellipse className="iso-gate-shadow" cx={x + 2} cy={y + r * 0.9} rx={r * 0.85} ry={r * 0.28} />
      <circle className="iso-gate-disc" cx={x} cy={y} r={r} />
      {it.role === "internet" ? (
        <path className="iso-cloud" d={CLOUD} transform={`translate(${x} ${y}) scale(${k}) translate(2 6)`} />
      ) : (
        <g className="iso-gate-icon">
          <IconGlyph name="site" x={x - r * 0.55} y={y - r * 0.55} size={r * 1.1} />
        </g>
      )}
      <circle className="iso-hull" cx={x} cy={y} r={r + 2} />
    </g>
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
      {frame.blanks ? <path className="iso-blank" d={frame.blanks} /> : null}
      {frame.blankLines ? <path className="iso-blank-line" d={frame.blankLines} /> : null}
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

const LED_BLINK = new Set(["warning", "critical"]);

/** The faces of a block and what is drawn on them: a building's roof and windows, a room's walls and name, a cabinet's door. */
function Body({ item: it }: { item: SceneItem }) {
  const { iso } = it;
  const faces = (
    <>
      <polygon className="iso-face iso-south" points={iso.south} />
      <polygon className="iso-face iso-east" points={iso.east} />
      <polygon className="iso-face iso-top" points={iso.top} />
      <polygon className="iso-shade" points={iso.south} fill="url(#iso-shade-side)" />
      <polygon className="iso-shade" points={iso.east} fill="url(#iso-shade-side)" />
      <polygon className="iso-shade" points={iso.top} fill="url(#iso-sheen)" />
      <polygon className="iso-rim" points={iso.top} />
    </>
  );
  return (
    <>
      {it.walls ? (
        <g className="iso-wall iso-wall-back">
          {it.walls.back.map((w, i) => (
            <Wall key={i} box={w} />
          ))}
        </g>
      ) : null}
      {faces}
      {it.roof ? <polygon className="iso-roof" points={it.roof} /> : null}
      {it.plant ? <Wall box={it.plant} /> : null}
      {it.windows.length > 0 ? <path className="iso-windows" d={it.windows.map((w) => `M${w.split(" ").join("L")}Z`).join("")} /> : null}
      {it.floorText ? (
        <text className="iso-floor-text" transform={it.floorText.transform} textAnchor="middle">
          {it.floorText.text}
        </text>
      ) : null}
      {it.cabinets.map((c, i) => (
        <g key={i} className="iso-mini">
          <polygon className="iso-face iso-south" points={c.iso.south} />
          <polygon className="iso-face iso-east" points={c.iso.east} />
          <polygon className="iso-face iso-top" points={c.iso.top} />
          <polygon className="iso-glass" points={c.door} />
        </g>
      ))}
      {it.walls ? (
        <g className="iso-wall iso-wall-front">
          {it.walls.front.map((w, i) => (
            <Wall key={i} box={w} />
          ))}
        </g>
      ) : null}
      {it.front ? (
        <g className={`iso-front-face f-${it.front.kind}`}>
          {it.front.screen ? <polygon className="iso-screen" points={it.front.screen} /> : null}
          {it.front.holes ? <path className="iso-holes" d={it.front.holes} /> : null}
          {it.front.lines ? <path className="iso-ports" d={it.front.lines} /> : null}
        </g>
      ) : null}
      {it.led ? (
        <g className={`iso-status l-${it.node.health.worst}`}>
          <circle className="iso-status-glow" cx={it.led.x} cy={it.led.y} r="5" />
          <circle className={`iso-led l-${it.node.health.worst}${LED_BLINK.has(it.node.health.worst) ? " is-blink" : ""}`} cx={it.led.x} cy={it.led.y} r="2.4" />
        </g>
      ) : null}
      {it.door ? (
        <g className="iso-door">
          <polygon className="iso-glass" points={it.door.glass} />
          <path className="iso-vents" d={it.door.vents} />
          {it.door.leds.map((l, i) => (
            <circle key={i} className={`iso-led l-${l.state}${LED_BLINK.has(l.state) ? " is-blink" : ""}`} cx={l.x} cy={l.y} r="1.7" />
          ))}
        </g>
      ) : null}
    </>
  );
}

function Wall({ box }: { box: IsoBox }) {
  return (
    <g className="iso-wall-part">
      <polygon className="iso-face iso-south" points={box.south} />
      <polygon className="iso-face iso-east" points={box.east} />
      <polygon className="iso-face iso-top" points={box.top} />
    </g>
  );
}

/** A linked device of a rack, as UniFi draws it: its role icon, its name and model, and its health. */
function Card({ item }: { item: SceneItem }) {
  const c = item.card;
  if (!c) return null;
  // Drawn at its design size and scaled, so that it keeps one size on screen.
  const w = c.w / c.k;
  const h = c.h / c.k;
  return (
    <g className={`iso-card-box h-${item.node.health.worst}`} transform={`translate(${c.x} ${c.y}) scale(${c.k})`}>
      <rect className="iso-card-bg" width={w} height={h} rx="9" />
      <circle className="iso-card-icon-bg" cx="22" cy={h / 2} r="14" />
      <g className="iso-card-icon">
        <IconGlyph name={c.icon} x={14} y={h / 2 - 8} size={16} />
      </g>
      <text className="iso-card-name" x="44" y={c.model ? 19 : 27}>
        {c.name}
      </text>
      {c.model ? (
        <text className="iso-card-model" x="44" y="34">
          {c.model}
        </text>
      ) : null}
      <circle className="iso-lamp" cx={w - 14} cy={h - 13} r="4" />
    </g>
  );
}

function Label({ item, sub, shown, text }: { item: SceneItem; sub?: string; shown: boolean; text: number }) {
  const pl = item.pill;
  const h = sub ? 34 : 21;
  const lead = item.pillLeader;
  // The pill is drawn at its design size and scaled, so that it keeps one size on screen.
  return (
    <g className={`iso-labels h-${item.node.health.worst}${shown ? "" : " is-hidden"}`} data-for={item.id} aria-hidden="true">
      {lead ? (
        <g className="iso-leader-group">
          <line className="iso-leader" x1={lead.x1} y1={lead.y1} x2={lead.x2} y2={lead.y2} />
          <circle className="iso-leader-dot" cx={lead.x1} cy={lead.y1} r="2" />
        </g>
      ) : null}
      <g transform={`translate(${pl.x0} ${pl.y0}) scale(${text})`}>
        <rect className="iso-pill" width={Math.max((pl.x1 - pl.x0) / text, 1)} height={h} rx={h > 25 ? 10 : 10.5} />
        <circle className="iso-lamp" cx={11} cy={10.5} r="3.5" />
        <text className="iso-label" x={20} y={14.5}>
          {item.label}
        </text>
        {sub ? (
          <text className="iso-sub" x={20} y={28}>
            {sub}
          </text>
        ) : null}
      </g>
    </g>
  );
}
