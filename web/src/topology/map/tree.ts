// The tree of a rack's linked devices, as UniFi draws its topology: the gateway
// on top, the switches under it, the rest below. A device's rank comes from its
// role, then from the `uplinked_to` edges (the device a controller says another
// reaches the network through is above it). Pure: no DOM, no React.

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
  gapX: number;
  gapY: number;
  /** Cards to a row; a longer layer wraps onto further rows. */
  perRow: number;
}
export interface Slot {
  x: number;
  y: number;
  layer: number;
  row: number;
}

/** Top-left of each card, layers centred on the widest one; w and h are the whole tree's size. */
export function layoutCards(layers: readonly (readonly string[])[], s: CardSize): { slots: Map<string, Slot>; w: number; h: number } {
  const slots = new Map<string, Slot>();
  const width = (n: number) => n * s.w + Math.max(0, n - 1) * s.gapX;
  const widest = Math.max(0, ...layers.map((l) => width(Math.min(l.length, s.perRow))));
  let y = 0;
  layers.forEach((ids, layer) => {
    const rows = Math.ceil(ids.length / s.perRow);
    for (let r = 0; r < rows; r++) {
      const row = ids.slice(r * s.perRow, (r + 1) * s.perRow);
      const x0 = (widest - width(row.length)) / 2;
      row.forEach((id, i) => slots.set(id, { x: x0 + i * (s.w + s.gapX), y, layer, row: r }));
      y += s.h + (r < rows - 1 ? s.gapY / 2 : s.gapY);
    }
  });
  return { slots, w: widest, h: Math.max(0, y - s.gapY) };
}
