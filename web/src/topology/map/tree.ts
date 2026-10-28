// The tree of a rack's linked devices, as UniFi draws its topology: the gateway
// on top, the switches under it, the rest below. A device's rank comes from its
// role, then from the `uplinked_to` edges (the device a controller says another
// reaches the network through is above it). Pure: no DOM, no React.

import { assignLanes } from "./route";

export interface TreeNode {
  id: string;
  kind: string;
  role?: string;
  place_type?: string;
  /** Position of the lowest unit in the rack; higher devices come first within a layer. */
  u?: number;
  name: string;
}
export interface TreeEdge {
  a: string;
  b: string;
  medium: string;
  toward?: string;
}

/** The rank a role gives: outside places 0, gateways 1, switches 2, everything else 3. */
export function roleRank(n: Pick<TreeNode, "kind" | "role">): number {
  if (n.kind === "portal" || n.kind === "internet") return 0;
  switch (n.role) {
    case "firewall":
    case "router":
    case "gateway":
    case "access_control":
      return 1;
    case "switch":
      return 2;
    default:
      return 3;
  }
}

/** The ids that take part in a tree: the ends of an edge whose two ends are both given. */
export function linkedIds(nodes: readonly TreeNode[], edges: readonly TreeEdge[]): Set<string> {
  const have = new Set(nodes.map((n) => n.id));
  const out = new Set<string>();
  for (const e of edges) {
    if (have.has(e.a) && have.has(e.b) && e.a !== e.b) {
      out.add(e.a);
      out.add(e.b);
    }
  }
  return out;
}

/**
 * The layers of the tree, top first, each ordered (highest unit first, then by name).
 * Only linked nodes are placed; with no link at all the result is empty and the rack
 * keeps its order by unit.
 */
export function rankTree(nodes: readonly TreeNode[], edges: readonly TreeEdge[]): string[][] {
  const linked = linkedIds(nodes, edges);
  const live = nodes.filter((n) => linked.has(n.id));
  const rank = new Map(live.map((n) => [n.id, roleRank(n)]));
  // A device uplinked through another sits at least one layer under it.
  for (let pass = 0; pass < live.length; pass++) {
    let moved = false;
    for (const e of edges) {
      if (e.medium !== "uplinked_to" || !e.toward || !rank.has(e.a) || !rank.has(e.b)) continue;
      const up = e.toward === "a" ? e.a : e.toward === "b" ? e.b : undefined;
      if (!up) continue;
      const down = up === e.a ? e.b : e.a;
      if ((rank.get(down) as number) <= (rank.get(up) as number)) {
        rank.set(down, (rank.get(up) as number) + 1);
        moved = true;
      }
    }
    if (!moved) break;
  }
  const distinct = [...new Set(rank.values())].sort((a, b) => a - b);
  const layers: TreeNode[][] = distinct.map(() => []);
  for (const n of live) (layers[distinct.indexOf(rank.get(n.id) as number)] as TreeNode[]).push(n);
  return layers.map((l) => l.sort((a, b) => (b.u ?? 0) - (a.u ?? 0) || a.name.localeCompare(b.name) || a.id.localeCompare(b.id)).map((n) => n.id));
}

export interface CardSize {
  w: number;
  h: number;
  /** The least room between two cards of a row; it grows when the lines that run down between them need it. */
  gapX: number;
  /** The least height of the channel between two rows; it grows with the lines that run along it. */
  gapY: number;
  /** Cards to a row at most; a longer layer wraps onto further rows of even length. */
  perRow: number;
  /** Distance between two parallel lines. */
  track: number;
}

/** A link between two cards of the tree. */
export interface PlanLink {
  key: string;
  a: string;
  b: string;
}
/** A link from a card to a gate (the Internet, another place) that stands right of the tree. */
export interface PlanGateLink {
  key: string;
  card: string;
  /** Rank of the gate in its column, top first: the lanes are taken in that order. */
  gate: number;
}

export interface PlannedCard {
  x: number;
  y: number;
  layer: number;
  row: number;
}

type P = { x: number; y: number };

export interface TreePlan {
  cards: Map<string, PlannedCard>;
  /** Card to card, from end a to end b. */
  routes: Map<string, P[]>;
  /** Card to gate: from the card to its own lane right of the tree; the scene finishes it once the gates stand. */
  exits: Map<string, { pts: P[]; laneX: number }>;
  w: number;
  h: number;
  /** The rightmost lane of the gate links (0 without any). */
  laneRight: number;
}

/**
 * The layers reordered to cross fewer links: each node moves to the mean position of its
 * neighbours in the layers above (one sweep down), then below (one sweep up). Stable: a node
 * without neighbours on the side looked at keeps its place.
 */
export function orderLayers(layers: readonly (readonly string[])[], links: readonly { a: string; b: string }[]): string[][] {
  const out = layers.map((l) => [...l]);
  const layerOf = new Map<string, number>();
  out.forEach((l, i) => l.forEach((id) => layerOf.set(id, i)));
  const near = new Map<string, string[]>();
  for (const e of links) {
    if (!layerOf.has(e.a) || !layerOf.has(e.b) || e.a === e.b) continue;
    near.set(e.a, [...(near.get(e.a) ?? []), e.b]);
    near.set(e.b, [...(near.get(e.b) ?? []), e.a]);
  }
  const pos = () => {
    const m = new Map<string, number>();
    for (const l of out) l.forEach((id, i) => m.set(id, i - (l.length - 1) / 2));
    return m;
  };
  const sweep = (li: number, above: boolean) => {
    const at = pos();
    const l = out[li] as string[];
    const bary = l.map((id, i) => {
      const ns = (near.get(id) ?? []).filter((n) => (above ? (layerOf.get(n) as number) < li : (layerOf.get(n) as number) > li));
      const own = at.get(id) as number;
      return { id, i, b: ns.length > 0 ? ns.reduce((s, n) => s + (at.get(n) as number), 0) / ns.length : own };
    });
    bary.sort((p, q) => p.b - q.b || p.i - q.i);
    out[li] = bary.map((x) => x.id);
  };
  for (let li = 1; li < out.length; li++) sweep(li, true);
  for (let li = out.length - 2; li >= 0; li--) sweep(li, false);
  return out;
}

/** A layer cut into as few rows as `per` allows, their lengths differing by one at most (the longer first). */
function rowsOf(ids: readonly string[], per: number): string[][] {
  const n = Math.ceil(ids.length / Math.max(1, per));
  const out: string[][] = [];
  let at = 0;
  for (let r = 0; r < n; r++) {
    const len = Math.floor(ids.length / n) + (r < ids.length % n ? 1 : 0);
    out.push(ids.slice(at, at + len));
    at += len;
  }
  return out;
}

type Side = "n" | "s" | "e" | "w";
/** A height not known until the channels are sized: a point of a row's cards, or a lane of a channel. */
type Y = { row: number; dy: number } | { ch: number; seg: number };
type Way = { x: number; y: Y };

/** Margin kept free round a card by a line that runs past it. */
const CLEAR = 8;
/** Room between a card and the first lane of a channel. */
const PAD = 12;

/**
 * The tree's cards and the routes of its links. The layers keep their order (see `orderLayers`), are
 * cut into rows and centred on the widest; every line is orthogonal and never crosses a card: between
 * two rows it runs along the channel between them, each line on a lane of its own; past rows in its
 * way it runs down a corridor between cards (or beside them), each line on a track of its own; the
 * channels and the corridors widen to hold their lines. Coordinates start at the tree's top left.
 */
export function planTree(layers: readonly (readonly string[])[], links: readonly PlanLink[], gateLinks: readonly PlanGateLink[], size: CardSize): TreePlan {
  let s = size;
  let plan = planOnce(layers, links, gateLinks, s);
  // A corridor too narrow for its tracks widens every gap, and the routes are made again.
  for (let i = 0; i < 3 && plan.needGapX > s.gapX; i++) {
    s = { ...s, gapX: Math.ceil(plan.needGapX) };
    plan = planOnce(layers, links, gateLinks, s);
  }
  return plan.out;
}

function planOnce(layers: readonly (readonly string[])[], links: readonly PlanLink[], gateLinks: readonly PlanGateLink[], s: CardSize): { out: TreePlan; needGapX: number } {
  const rows: { ids: string[]; layer: number }[] = [];
  layers.forEach((l, layer) => {
    for (const ids of rowsOf(l, s.perRow)) rows.push({ ids, layer });
  });
  const width = (n: number) => n * s.w + Math.max(0, n - 1) * s.gapX;
  const widest = Math.max(0, ...rows.map((r) => width(r.ids.length)));
  const card = new Map<string, { x: number; row: number; col: number; layer: number }>();
  rows.forEach((r, row) => {
    const x0 = (widest - width(r.ids.length)) / 2;
    r.ids.forEach((id, col) => card.set(id, { x: x0 + col * (s.w + s.gapX), row, col, layer: r.layer }));
  });
  const cx = (id: string) => (card.get(id) as { x: number }).x + s.w / 2;
  const rightmost = (id: string) => {
    const c = card.get(id) as { row: number; col: number };
    return c.col === (rows[c.row] as { ids: string[] }).ids.length - 1;
  };

  // What each link does: across to a neighbour, over its row, down to another row (past the rows
  // between through a corridor), or out to the gates.
  type Plan =
    | { kind: "across" | "over"; key: string; a: string; b: string; sa: Side; sb: Side }
    | { kind: "down"; key: string; up: string; low: string; flip: boolean }
    | { kind: "gate"; key: string; card: string; side: "e" | "n"; gate: number };
  const plans: Plan[] = [];
  for (const l of links) {
    const ca = card.get(l.a);
    const cb = card.get(l.b);
    if (!ca || !cb || l.a === l.b) continue;
    if (ca.row === cb.row) {
      if (Math.abs(ca.col - cb.col) === 1) plans.push({ kind: "across", key: l.key, a: l.a, b: l.b, sa: ca.col < cb.col ? "e" : "w", sb: ca.col < cb.col ? "w" : "e" });
      else plans.push({ kind: "over", key: l.key, a: l.a, b: l.b, sa: "n", sb: "n" });
    } else plans.push({ kind: "down", key: l.key, up: ca.row < cb.row ? l.a : l.b, low: ca.row < cb.row ? l.b : l.a, flip: ca.row > cb.row });
  }
  const gates = [...gateLinks].filter((g) => card.has(g.card)).sort((p, q) => p.gate - q.gate || (card.get(p.card) as { row: number }).row - (card.get(q.card) as { row: number }).row);
  for (const g of gates) plans.push({ kind: "gate", key: g.key, card: g.card, side: rightmost(g.card) ? "e" : "n", gate: g.gate });

  // The links that leave one side of a card are spread along it, ordered by where they go.
  const sides = new Map<string, { key: string; end: string; other: number }[]>();
  const touch = (id: string, side: Side, key: string, end: string, other: number) => {
    const k = `${id}|${side}`;
    sides.set(k, [...(sides.get(k) ?? []), { key, end, other }]);
  };
  for (const p of plans) {
    if (p.kind === "down") {
      touch(p.up, "s", p.key, "up", cx(p.low));
      touch(p.low, "n", p.key, "low", cx(p.up));
    } else if (p.kind === "gate") touch(p.card, p.side, p.key, "card", Infinity);
    else {
      touch(p.a, p.sa, p.key, "a", p.kind === "over" ? cx(p.b) : 0);
      touch(p.b, p.sb, p.key, "b", p.kind === "over" ? cx(p.a) : 0);
    }
  }
  const attach = new Map<string, Way>();
  for (const [k, list] of sides) {
    const [id, side] = k.split("|") as [string, Side];
    const c = card.get(id) as { x: number; row: number };
    list.sort((p, q) => p.other - q.other);
    list.forEach((t, i) => {
      const f = (i + 1) / (list.length + 1);
      const at: Way =
        side === "n" ? { x: c.x + s.w * f, y: { row: c.row, dy: 0 } } : side === "s" ? { x: c.x + s.w * f, y: { row: c.row, dy: s.h } } : { x: side === "e" ? c.x + s.w : c.x, y: { row: c.row, dy: s.h * f } };
      attach.set(`${t.key}|${t.end}`, at);
    });
  }

  // Corridors: the x ranges free of cards in every row a line runs past.
  const spans = rows.map((r) => r.ids.map((id) => [(card.get(id) as { x: number }).x - CLEAR, (card.get(id) as { x: number }).x + s.w + CLEAR] as const));
  const free = (from: number, to: number): [number, number][] => {
    let out: [number, number][] = [[-Infinity, Infinity]];
    for (let r = from; r <= to; r++) {
      for (const [lo, hi] of spans[r] ?? []) {
        out = out.flatMap(([a, b]): [number, number][] => (hi <= a || lo >= b ? [[a, b]] : ([[a, Math.min(b, lo)], [Math.max(a, hi), b]] as [number, number][]).filter(([x, y]) => y - x > 0.5)));
      }
    }
    return out;
  };
  const corridor = new Map<string, { lo: number; hi: number; users: { key: string; want: number }[] }>();
  const corridorOf = new Map<string, string>();
  for (const p of plans) {
    if (p.kind !== "down") continue;
    const ru = (card.get(p.up) as { row: number }).row;
    const rl = (card.get(p.low) as { row: number }).row;
    if (rl - ru < 2) continue;
    const ux = (attach.get(`${p.key}|up`) as Way).x;
    const lx = (attach.get(`${p.key}|low`) as Way).x;
    const want = (ux + lx) / 2;
    let best: [number, number] | undefined;
    let cost = Infinity;
    for (const [lo, hi] of free(ru + 1, rl - 1)) {
      const x = Math.min(hi, Math.max(lo, want));
      const c = Math.abs(ux - x) + Math.abs(x - lx);
      if (c < cost - 0.01) [best, cost] = [[lo, hi], c];
    }
    const [lo, hi] = best ?? [-Infinity, 0];
    const k = `${Math.round(lo)}|${Math.round(hi)}`;
    const c = corridor.get(k) ?? { lo, hi, users: [] };
    c.users.push({ key: p.key, want });
    corridor.set(k, c);
    corridorOf.set(p.key, k);
  }
  const trackX = new Map<string, number>();
  let needGapX = 0;
  for (const c of corridor.values()) {
    c.users.sort((p, q) => p.want - q.want);
    const n = c.users.length;
    if (Number.isFinite(c.lo) && Number.isFinite(c.hi)) {
      const mid = (c.lo + c.hi) / 2;
      c.users.forEach((u, i) => trackX.set(u.key, mid + (i - (n - 1) / 2) * s.track));
      needGapX = Math.max(needGapX, (n - 1) * s.track + 2 * CLEAR + 2 * PAD);
    } else if (Number.isFinite(c.hi)) c.users.forEach((u, i) => trackX.set(u.key, c.hi - PAD - (n - 1 - i) * s.track));
    else c.users.forEach((u, i) => trackX.set(u.key, c.lo + PAD + i * s.track));
  }

  // A line that rises into a card must not run on one that drops out of the card above it, nor on a
  // corridor's track: a top attach that falls on one of them moves aside.
  const below = new Map<number, number[]>();
  for (const [k, w] of attach) {
    const end = k.slice(k.lastIndexOf("|") + 1);
    if (end === "up" && "row" in w.y) below.set(w.y.row + 1, [...(below.get(w.y.row + 1) ?? []), w.x]);
  }
  const tracks = [...trackX.values()];
  for (const [k, list] of sides) {
    const [id, side] = k.split("|") as [string, Side];
    if (side !== "n") continue;
    const c = card.get(id) as { x: number; row: number };
    const busy = [...(below.get(c.row) ?? []), ...tracks];
    for (const t of list) {
      const w = attach.get(`${t.key}|${t.end}`) as Way;
      for (let i = 0; i < 4 && busy.some((x) => Math.abs(x - w.x) < 3); i++) w.x = w.x + (w.x + s.track / 2 < c.x + s.w - 4 ? s.track / 2 : -s.track / 2);
    }
  }

  // The gate links run down lanes of their own, right of the cards and of the corridors beside them.
  const lane0 = Math.max(widest, ...[...trackX.values()].map((x) => x + PAD)) + 18;
  const laneX = new Map<string, number>();
  gates.forEach((g, k) => laneX.set(g.key, lane0 + k * s.track));
  const laneRight = gates.length > 0 ? lane0 + (gates.length - 1) * s.track : 0;

  // The ways of each link, the horizontal runs filed by channel (channel r is the one above row r).
  const segs = new Map<number, { from: number; to: number }[]>();
  const seg = (ch: number, from: number, to: number): Y => {
    const list = segs.get(ch) ?? [];
    list.push({ from, to });
    segs.set(ch, list);
    return { ch, seg: list.length - 1 };
  };
  const ways = new Map<string, { pts: Way[]; flip: boolean }>();
  const exits = new Map<string, Way[]>();
  for (const p of plans) {
    if (p.kind === "across") {
      const a = attach.get(`${p.key}|a`) as Way;
      const b = attach.get(`${p.key}|b`) as Way;
      const mid = (a.x + b.x) / 2;
      ways.set(p.key, { pts: [a, { x: mid, y: a.y }, { x: mid, y: b.y }, b], flip: false });
    } else if (p.kind === "over") {
      const a = attach.get(`${p.key}|a`) as Way;
      const b = attach.get(`${p.key}|b`) as Way;
      const y = seg((card.get(p.a) as { row: number }).row, a.x, b.x);
      ways.set(p.key, { pts: [a, { x: a.x, y }, { x: b.x, y }, b], flip: false });
    } else if (p.kind === "down") {
      const u = attach.get(`${p.key}|up`) as Way;
      const l = attach.get(`${p.key}|low`) as Way;
      const ru = (card.get(p.up) as { row: number }).row;
      const rl = (card.get(p.low) as { row: number }).row;
      const tx = trackX.get(p.key);
      if (tx === undefined) {
        const y = seg(rl, u.x, l.x);
        ways.set(p.key, { pts: [u, { x: u.x, y }, { x: l.x, y }, l], flip: p.flip });
      } else {
        const y1 = seg(ru + 1, u.x, tx);
        const y2 = seg(rl, tx, l.x);
        ways.set(p.key, { pts: [u, { x: u.x, y: y1 }, { x: tx, y: y1 }, { x: tx, y: y2 }, { x: l.x, y: y2 }, l], flip: p.flip });
      }
    } else if (p.kind === "gate") {
      const a = attach.get(`${p.key}|card`) as Way;
      const lx = laneX.get(p.key) as number;
      if (p.side === "e") exits.set(p.key, [a, { x: lx, y: a.y }]);
      else {
        const y = seg((card.get(p.card) as { row: number }).row, a.x, lx);
        exits.set(p.key, [a, { x: a.x, y }, { x: lx, y }]);
      }
    }
  }

  // Lanes of each channel, then the channels' heights and the rows' tops.
  const lanes = new Map<number, number[]>();
  for (const [ch, list] of segs) lanes.set(ch, assignLanes(list));
  const chTop: number[] = [];
  const chH: number[] = [];
  const rowTop: number[] = [];
  let y = 0;
  for (let r = 0; r < rows.length; r++) {
    const n = Math.max(0, ...(lanes.get(r) ?? [-1])) + 1;
    const h = n === 0 ? (r === 0 ? 0 : s.gapY) : Math.max(r === 0 ? 0 : s.gapY, 2 * PAD + (n - 1) * s.track);
    chTop.push(y);
    chH.push(h);
    y += h;
    rowTop.push(y);
    y += s.h;
  }
  const yOf = (v: Y): number => {
    if ("row" in v) return (rowTop[v.row] as number) + v.dy;
    const n = Math.max(...(lanes.get(v.ch) as number[])) + 1;
    const lane = (lanes.get(v.ch) as number[])[v.seg] as number;
    return (chTop[v.ch] as number) + ((chH[v.ch] as number) - (n - 1) * s.track) / 2 + lane * s.track;
  };

  // Lines past the left of the cards push the whole tree right, so that it starts at 0.
  const xs = [...trackX.values()];
  const left = Math.min(0, ...xs.map((x) => x - PAD));
  const pt = (w: Way): P => ({ x: w.x - left, y: yOf(w.y) });
  const cards = new Map<string, PlannedCard>();
  for (const [id, c] of card) cards.set(id, { x: c.x - left, y: rowTop[c.row] as number, layer: c.layer, row: c.row });
  const routes = new Map<string, P[]>();
  for (const [key, w] of ways) {
    const pts = w.pts.map(pt);
    routes.set(key, w.flip ? pts.reverse() : pts);
  }
  const outExits = new Map<string, { pts: P[]; laneX: number }>();
  for (const [key, w] of exits) outExits.set(key, { pts: w.map(pt), laneX: (laneX.get(key) as number) - left });
  const right = Math.max(widest, ...xs.map((x) => x + PAD));
  return { out: { cards, routes, exits: outExits, w: right - left, h: rows.length > 0 ? y : 0, laneRight: laneRight > 0 ? laneRight - left : 0 }, needGapX };
}
