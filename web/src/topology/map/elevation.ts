// A rack's cabling as a connection matrix laid on the real rack. A drawing of nodes and links cannot
// help crossings when three switches are each cabled to three servers or more (K3,3 is not planar), so
// the devices are drawn as they stand, one row each in the order of their units, and every hub (a
// switch, or the Internet, or another place) owns a riser, a column beside the rows: a dot where a row
// meets a riser says that device is cabled to that hub. Only hubs have a stub, from their row out to
// their riser, and the risers are ordered so that no stub runs across one: nothing crosses. Pure: no
// DOM, no React.

export interface ElevDevice {
  id: string;
  /** Position of the lowest unit in the rack (0 or less: not mounted). */
  u: number;
  uHeight: number;
  name: string;
  role?: string;
}
export interface ElevLink {
  key: string;
  a: string;
  b: string;
}
export interface ElevSize {
  /** A row: its width, height and the room between two rows. */
  rowW: number;
  rowH: number;
  rowGap: number;
  /** From the rows to the first riser, and between two risers. */
  firstRiser: number;
  riserGap: number;
  /** Room above the first row for the gates' badges, at the top of their risers. */
  head: number;
  /** Between two gates' risers (their names stand side by side above them); 1.6 risers by default. */
  gateGap?: number;
}

type P = { x: number; y: number };

export interface ElevRow {
  x: number;
  y: number;
  w: number;
  h: number;
  /** Rank from the top. */
  index: number;
}
export interface ElevRiser {
  id: string;
  gate: boolean;
  x: number;
  y0: number;
  y1: number;
}
export interface ElevationPlan {
  rows: Map<string, ElevRow>;
  /** Nearest the rows first. */
  risers: ElevRiser[];
  /** Where each link is drawn: a dot on its owner's riser, at the other end's row. */
  dots: Map<string, P & { riser: string }>;
  /** Each link's line from end a to end b: the owner's stub and riser, down or up to the dot. */
  routes: Map<string, P[]>;
  /** The stubs of the hubs, from their row to their riser. */
  stubs: Map<string, { x0: number; x1: number; y: number }>;
  /** Where a stub has to run across a riser after all (a cycle of hubs), drawn as a bridge. */
  bridges: P[];
  w: number;
  h: number;
}

/** Roles that own their links: the devices others are cabled to. */
const HUB_ROLES = new Set(["switch", "patch_panel", "router", "firewall", "access_control"]);

/** The highest unit a device takes, for the order of the rows (unmounted devices go last). */
const topOf = (d: ElevDevice) => (d.u > 0 ? d.u + Math.max(1, d.uHeight) - 1 : -Infinity);

export function planElevation(devices: readonly ElevDevice[], gates: readonly string[], links: readonly ElevLink[], s: ElevSize): ElevationPlan {
  const dev = new Map(devices.map((d) => [d.id, d]));
  const isGate = new Set(gates);
  const live = links.filter((l) => l.a !== l.b && (dev.has(l.a) || isGate.has(l.a)) && (dev.has(l.b) || isGate.has(l.b)) && !(isGate.has(l.a) && isGate.has(l.b)));

  // Rows: the devices with a link, top unit first.
  const linked = new Set(live.flatMap((l) => [l.a, l.b]).filter((id) => dev.has(id)));
  const order = devices.filter((d) => linked.has(d.id)).sort((p, q) => topOf(q) - topOf(p) || p.name.localeCompare(q.name) || p.id.localeCompare(q.id));
  const rows = new Map<string, ElevRow>();
  order.forEach((d, index) => rows.set(d.id, { x: 0, y: s.head + index * (s.rowH + s.rowGap), w: s.rowW, h: s.rowH, index }));
  const cy = (id: string) => (rows.get(id) as ElevRow).y + s.rowH / 2;

  // Owners: a gate owns its links; else the end with a hub's role; else the one with more links, then the higher.
  const degree = new Map<string, number>();
  for (const l of live) for (const id of [l.a, l.b]) degree.set(id, (degree.get(id) ?? 0) + 1);
  const hubby = (id: string) => (isGate.has(id) ? 2 : HUB_ROLES.has(dev.get(id)?.role ?? "") ? 1 : 0);
  const ownerOf = (l: ElevLink): string => {
    const [ha, hb] = [hubby(l.a), hubby(l.b)];
    if (ha !== hb) return ha > hb ? l.a : l.b;
    const [da, db] = [degree.get(l.a) ?? 0, degree.get(l.b) ?? 0];
    if (da !== db) return da > db ? l.a : l.b;
    const [ta, tb] = [topOf(dev.get(l.a) as ElevDevice), topOf(dev.get(l.b) as ElevDevice)];
    if (ta !== tb) return ta > tb ? l.a : l.b;
    return l.a < l.b ? l.a : l.b;
  };
  const owned = new Map<string, { key: string; other: string }[]>();
  for (const l of live) {
    const o = ownerOf(l);
    owned.set(o, [...(owned.get(o) ?? []), { key: l.key, other: o === l.a ? l.b : l.a }]);
  }

  // Spans: a device's riser runs from its own row to the farthest row it serves; a gate's from the top.
  const hubs = [...owned.keys()].filter((id) => dev.has(id));
  const span = new Map<string, [number, number]>();
  for (const h of hubs) {
    const ys = [cy(h), ...(owned.get(h) ?? []).map((x) => cy(x.other))];
    span.set(h, [Math.min(...ys), Math.max(...ys)]);
  }
  const dotRows = (h: string) => new Set((owned.get(h) ?? []).map((x) => cy(x.other)));
  // A stub of j runs across riser i, nearer the rows, when i spans j's row without a dot there (a dot
  // there is a junction: j is cabled to i). So such an i must stand farther than j.
  const covers = (i: string, j: string) => {
    const [lo, hi] = span.get(i) as [number, number];
    const y = cy(j);
    return i !== j && y > lo + 0.5 && y < hi - 0.5 && !dotRows(i).has(y);
  };
  const before = new Map(hubs.map((j) => [j, new Set(hubs.filter((i) => covers(i, j)))])); // j must be nearer than each of these
  const placed: string[] = [];
  const left = new Set(hubs);
  // The lowest hub nearest when nothing else decides: a switch stack then needs no bridge.
  const natural = (p: string, q: string) => cy(q) - cy(p) || p.localeCompare(q);
  while (left.size > 0) {
    // Free: no hub still to place must stand nearer than it.
    const free = [...left].filter((i) => ![...left].some((j) => j !== i && before.get(j)?.has(i)));
    const pool = free.length > 0 ? free : [...left].sort((p, q) => blockers(p) - blockers(q) || natural(p, q)).slice(0, 1);
    const next = pool.sort(natural)[0] as string;
    placed.push(next);
    left.delete(next);
  }
  function blockers(i: string): number {
    return [...left].filter((j) => j !== i && before.get(j)?.has(i)).length;
  }

  const risers: ElevRiser[] = [];
  const xOf = new Map<string, number>();
  placed.forEach((h, k) => {
    const x = s.rowW + s.firstRiser + k * s.riserGap;
    xOf.set(h, x);
    const [y0, y1] = span.get(h) as [number, number];
    risers.push({ id: h, gate: false, x, y0, y1 });
  });
  const gateIds = [...owned.keys()].filter((id) => isGate.has(id)).sort((p, q) => gates.indexOf(p) - gates.indexOf(q));
  const gateX0 = s.rowW + s.firstRiser + placed.length * s.riserGap + (placed.length > 0 ? s.riserGap * 0.6 : 0);
  gateIds.forEach((g, k) => {
    const x = gateX0 + k * (s.gateGap ?? s.riserGap * 1.6);
    xOf.set(g, x);
    risers.push({ id: g, gate: true, x, y0: s.head * 0.5, y1: Math.max(...(owned.get(g) ?? []).map((o) => cy(o.other))) });
  });

  const stubs = new Map<string, { x0: number; x1: number; y: number }>();
  const bridges: P[] = [];
  placed.forEach((j, k) => {
    const y = cy(j);
    stubs.set(j, { x0: s.rowW, x1: xOf.get(j) as number, y });
    for (const i of placed.slice(0, k)) if (covers(i, j)) bridges.push({ x: xOf.get(i) as number, y });
  });

  const dots = new Map<string, P & { riser: string }>();
  const routes = new Map<string, P[]>();
  for (const l of live) {
    const o = ownerOf(l);
    const t = o === l.a ? l.b : l.a;
    const x = xOf.get(o) as number;
    const dot = { x, y: cy(t), riser: o };
    dots.set(l.key, dot);
    const fromOwner = isGate.has(o) ? [{ x, y: s.head * 0.5 }, { x, y: dot.y }] : [{ x: s.rowW, y: cy(o) }, { x, y: cy(o) }, { x, y: dot.y }];
    routes.set(l.key, o === l.a ? fromOwner : [...fromOwner].reverse());
  }
  const w = Math.max(s.rowW, ...risers.map((r) => r.x)) + s.riserGap;
  const h = s.head + Math.max(0, order.length * (s.rowH + s.rowGap) - s.rowGap);
  return { rows, risers, dots, routes, stubs, bridges, w, h };
}

/** How many times a stub runs across a riser of the plan without a dot there (the bridges). */
export const crossings = (p: ElevationPlan): number => p.bridges.length;
