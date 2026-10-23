// The layer-2 topology, kept free of React, of the DOM and of cytoscape so that
// it is tested on its own. What the schema says (internal/schema/builtin.yaml):
//
//  - a documented cable is a `cable` object with two `connects` relations to
//    ports (attribute `end` a | b); its relations are declared;
//  - `cabled_to` joins two ports; the deducer (ADR 0010) writes it with
//    confidence `deduced` and the attributes `evidence` and `level`
//    (high | medium | low); another source may write it observed or declared;
//  - `neighbor_of` is an LLDP/CDP adjacency seen from one port;
//  - a port is an `interface` whose parent is a device (or a module of one);
//  - `uplinked_to` joins two devices, no port: a controller says which device
//    another one reaches the network through (ADR 0060); it is drawn as a
//    device-level line, only where no port-level link joins the same two
//    devices (ADR 0062);
//  - `member_of` puts an interface in a VLAN, or a port in an aggregate (LAG:
//    member_of from an interface to an interface); `mounted_in` puts a device in a rack;
//  - two switches of one MLAG pair share `mlag_domain`.
//
// Every link keeps the confidence it was stored with: the picture must not
// make a deduced cable look like a documented one.

import type { RelationEnd, RelationLink } from "../api/types";
import { SEVERITY, type Health } from "../inventory/health";

export const LINK_TYPES = ["cabled_to", "connects", "neighbor_of", "uplinked_to"] as const;
export const CONTEXT_TYPES = ["mounted_in", "member_of"] as const;

/** How well a link is known: the confidence the relation was stored with. */
export type LinkClass = "declared" | "observed" | "deduced";
export const LINK_CLASSES: readonly LinkClass[] = ["declared", "observed", "deduced"];

export type Level = "high" | "medium" | "low";
export const LEVELS: readonly Level[] = ["high", "medium", "low"];

export type LinkKind = "cable" | "cabled_to" | "neighbor_of" | "uplinked_to";

/** An uplink names two devices and no port: the ends of its row carry no port name. */
export const isUplink = (r: Pick<LinkRow, "kind">): boolean => r.kind === "uplinked_to";

export interface Port {
  /** The interface object. */
  id: string;
  name: string;
  /** The node it belongs to: its device, or the module's device. */
  deviceId: string;
}

export interface LinkRow {
  key: string;
  kind: LinkKind;
  klass: LinkClass;
  /** Only for deduced links, and only when the deducer said so. */
  level?: Level;
  evidence?: string;
  source: string;
  a: Port;
  b: Port;
  cable?: { id: string; label: string };
}

export interface DeviceInfo {
  id: string;
  name: string;
  siteId?: string;
  role?: string;
  /** The device's own `health`. */
  health?: Health;
  /** `mlag_domain`: two switches sharing it are one MLAG pair. */
  mlagDomain?: string;
  /** `log_critical_count`: critical entries of the hardware event log. */
  critical?: number;
}

/** Everything the page loaded, as the API gave it. */
export interface TopologyInput {
  /** cabled_to, connects, neighbor_of and uplinked_to. */
  links: readonly RelationLink[];
  /** mounted_in and member_of. */
  context: readonly RelationLink[];
  devices: readonly DeviceInfo[];
  /** Ports hosted by a module: module id -> the id of its device. */
  moduleDevice: ReadonlyMap<string, string>;
  sites: ReadonlyMap<string, string>;
  /** The health of the parts of each device (modules, power supplies, fans), when the page read them. */
  partsHealth?: ReadonlyMap<string, readonly Health[]>;
}

export interface Node {
  id: string;
  name: string;
  siteId?: string;
  rackId?: string;
  role?: string;
  /** The worst of the device's health and its parts' (unknown when nothing says). */
  health?: Health;
  mlagDomain?: string;
  critical?: number;
  /** The id is not a device the page knows: it was named only by a port's parent. */
  unresolved: boolean;
}

/** The worst of some healths: critical before warning before unknown; unknown when there is none. */
export function worstHealth(hs: readonly (Health | undefined)[]): Health {
  let worst: Health | undefined;
  for (const h of hs) {
    if (h === undefined || h === "absent") continue;
    if (worst === undefined || SEVERITY[h] < SEVERITY[worst]) worst = h;
  }
  return worst ?? "unknown";
}

export interface Topology {
  rows: LinkRow[];
  nodes: Map<string, Node>;
  racks: Map<string, string>;
  vlans: Map<string, string>;
  sites: Map<string, string>;
  /** Interface id -> ids of the VLANs it is a member of. */
  vlanOf: Map<string, Set<string>>;
  /** A member port -> the aggregate (LAG) interface it is a member of. */
  lagOf: Map<string, string>;
  /** An aggregate interface -> its name. */
  lagName: Map<string, string>;
  /** Cables with other than two usable ends, and links between ports other than interfaces: not drawn. */
  skipped: { cables: number; ends: number };
}

const text = (v: unknown): string | undefined => (typeof v === "string" && v !== "" ? v : undefined);
const level = (v: unknown): Level | undefined => LEVELS.find((l) => l === v);

/** An unknown confidence is treated as the least trusted one, never as a better one. */
export function classOf(confidence: string): LinkClass {
  return LINK_CLASSES.find((c) => c === confidence) ?? "deduced";
}

export function buildTopology(input: TopologyInput): Topology {
  const deviceOf = (e: RelationEnd): string => {
    if (!e.parent_id) return e.id;
    return input.moduleDevice.get(e.parent_id) ?? e.parent_id;
  };
  const port = (e: RelationEnd): Port => ({ id: e.id, name: e.display, deviceId: deviceOf(e) });
  const rows: LinkRow[] = [];
  const skipped = { cables: 0, ends: 0 };
  const isPort = (e: RelationEnd) => e.type === "interface";

  const cables = new Map<string, { end: RelationEnd; at: string }[]>();
  const cableLabel = new Map<string, string>();
  for (const l of input.links) {
    if (l.type === "connects") {
      const list = cables.get(l.from_id) ?? [];
      list.push({ end: l.to, at: text(l.attributes?.end) ?? "" });
      cables.set(l.from_id, list);
      cableLabel.set(l.from_id, l.from.display);
      continue;
    }
    if (l.type === "uplinked_to") {
      if (l.from.type !== "device" || l.to.type !== "device") {
        skipped.ends++;
        continue;
      }
      const klass = classOf(l.confidence);
      // The ends are the devices themselves: never remapped to a parent, as a port is.
      const dev = (e: RelationEnd): Port => ({ id: e.id, name: "", deviceId: e.id });
      rows.push({
        key: `uplinked_to:${l.id}`,
        kind: "uplinked_to",
        klass,
        level: klass === "deduced" ? level(l.attributes?.level) : undefined,
        source: l.source,
        a: dev(l.from),
        b: dev(l.to),
      });
      continue;
    }
    if (l.type !== "cabled_to" && l.type !== "neighbor_of") continue;
    if (!isPort(l.from) || !isPort(l.to)) {
      skipped.ends++;
      continue;
    }
    const klass = classOf(l.confidence);
    rows.push({
      key: `${l.type}:${l.id}`,
      kind: l.type,
      klass,
      level: klass === "deduced" ? level(l.attributes?.level) : undefined,
      evidence: text(l.attributes?.evidence),
      source: l.source,
      a: port(l.from),
      b: port(l.to),
    });
  }
  // A documented cable is two relations with the same cable; the confidence is the one of its relations.
  const confidenceOfCable = new Map<string, LinkClass>();
  const sourceOfCable = new Map<string, string>();
  for (const l of input.links) {
    if (l.type !== "connects") continue;
    const prev = confidenceOfCable.get(l.from_id);
    const c = classOf(l.confidence);
    // The weakest end decides: a cable is only as certain as its least certain end.
    if (prev === undefined || LINK_CLASSES.indexOf(c) > LINK_CLASSES.indexOf(prev)) confidenceOfCable.set(l.from_id, c);
    sourceOfCable.set(l.from_id, l.source);
  }
  for (const [cableId, ends] of cables) {
    const usable = ends.filter((e) => isPort(e.end));
    if (usable.length !== 2 || ends.length !== 2) {
      skipped.cables++;
      continue;
    }
    // End a first, so that the same cable always reads the same way.
    const sorted = [...usable].sort((p, q) => p.at.localeCompare(q.at));
    const [x, y] = sorted;
    if (!x || !y) continue;
    rows.push({
      key: `cable:${cableId}`,
      kind: "cable",
      klass: confidenceOfCable.get(cableId) ?? "deduced",
      source: sourceOfCable.get(cableId) ?? "",
      a: port(x.end),
      b: port(y.end),
      cable: { id: cableId, label: cableLabel.get(cableId) ?? cableId },
    });
  }

  const racks = new Map<string, string>();
  const rackOf = new Map<string, string>();
  const vlans = new Map<string, string>();
  const vlanOf = new Map<string, Set<string>>();
  const siteOf = new Map<string, string>();
  const lagOf = new Map<string, string>();
  const lagName = new Map<string, string>();
  for (const l of input.context) {
    if (l.type === "mounted_in" && l.from.type === "device" && l.to.type === "rack") {
      rackOf.set(l.from_id, l.to_id);
      racks.set(l.to_id, l.to.display);
    } else if (l.type === "member_of" && l.from.type === "interface" && l.to.type === "interface" && l.from_id !== l.to_id) {
      lagOf.set(l.from_id, l.to_id);
      lagName.set(l.to_id, l.to.display);
    } else if (l.type === "member_of" && l.from.type === "interface" && l.to.type === "vlan") {
      vlans.set(l.to_id, l.to.display);
      const set = vlanOf.get(l.from_id) ?? new Set<string>();
      set.add(l.to_id);
      vlanOf.set(l.from_id, set);
    }
  }
  // A device is in the site its interfaces say; a port's site is the one of its device.
  for (const l of input.links) {
    for (const e of [l.from, l.to]) {
      if (e.parent_id && e.site_id && !siteOf.has(e.parent_id)) siteOf.set(e.parent_id, e.site_id);
      if (e.type === "device" && e.site_id && !siteOf.has(e.id)) siteOf.set(e.id, e.site_id);
    }
  }

  const known = new Map(input.devices.map((d) => [d.id, d]));
  const nodes = new Map<string, Node>();
  for (const r of rows) {
    for (const p of [r.a, r.b]) {
      if (nodes.has(p.deviceId)) continue;
      const d = known.get(p.deviceId);
      nodes.set(p.deviceId, {
        id: p.deviceId,
        name: d?.name ?? p.deviceId.slice(0, 8),
        siteId: d?.siteId ?? siteOf.get(p.deviceId),
        rackId: rackOf.get(p.deviceId),
        role: d?.role,
        health: worstHealth([d?.health, ...(input.partsHealth?.get(p.deviceId) ?? [])]),
        mlagDomain: d?.mlagDomain,
        critical: d?.critical,
        unresolved: d === undefined,
      });
    }
  }
  return { rows, nodes, racks, vlans, sites: new Map(input.sites), vlanOf, lagOf, lagName, skipped };
}

export interface Filters {
  site?: string;
  rack?: string;
  vlan?: string;
  classes: ReadonlySet<LinkClass>;
}

export const ALL_CLASSES: ReadonlySet<LinkClass> = new Set(LINK_CLASSES);

/**
 * The links a reader asked for. A site, rack or VLAN keeps a link when at
 * least one end is in it, so that a link leaving the site is seen going
 * somewhere instead of vanishing.
 */
export function filterRows(t: Topology, f: Filters): LinkRow[] {
  return t.rows.filter((r) => {
    if (!f.classes.has(r.klass)) return false;
    const ends = [r.a, r.b];
    if (f.site !== undefined && !ends.some((p) => t.nodes.get(p.deviceId)?.siteId === f.site)) return false;
    if (f.rack !== undefined && !ends.some((p) => t.nodes.get(p.deviceId)?.rackId === f.rack)) return false;
    if (f.vlan !== undefined && !ends.some((p) => t.vlanOf.get(p.id)?.has(f.vlan as string))) return false;
    return true;
  });
}

/** One drawn line: the links of one class (and level) between two devices. */
export interface Edge {
  id: string;
  source: string;
  target: string;
  klass: LinkClass;
  level?: Level;
  /** Distinct port pairs it stands for. */
  count: number;
  /** The links it stands for (always set by toGraph). */
  rows?: LinkRow[];
  /**
   * The aggregate (LAG) the line belongs to: folded, one line for all its
   * members between the two devices; unfolded, one line per member.
   */
  lag?: { group: string; name: string; folded: boolean };
  /**
   * A device-level uplink (ADR 0062), oriented: `source` is the device,
   * `target` the one it reaches the network through. It stands for no port.
   */
  uplink?: boolean;
  /** Uplinks between the same two devices that this port-level line stands for: not drawn on their own. */
  uplinks?: LinkRow[];
}

/** Two switches of one MLAG pair, drawn inside one box. */
export interface Group {
  id: string;
  domain: string;
  members: string[];
}

export interface Graph {
  nodes: Node[];
  edges: Edge[];
  /** Links whose two ends are on the same device: in the table, not drawn. */
  loops: number;
  /** MLAG pairs among the nodes (a domain shared by at least two of them). */
  groups?: Group[];
  /** Uplinks not drawn on their own because a port-level line joins the same two devices. */
  covered?: number;
}

export interface GraphOptions {
  /** Aggregate groups (Edge.lag.group) drawn member by member instead of as one line. */
  expanded?: ReadonlySet<string>;
}

/**
 * The graph of some rows: a node per device that has a link, a line per
 * (device pair, class, level). A deduced line keeps its level because
 * "deduced from both ends" and "deduced from a forwarding table" are not the
 * same claim; parallel lines of different classes are kept apart. The member
 * ports of one aggregate between the same two devices are one line (the
 * aggregate's), unless the reader expanded it.
 */
export function toGraph(t: Topology, rows: readonly LinkRow[], opts: GraphOptions = {}): Graph {
  const edges = new Map<string, Edge>();
  const pairsOf = new Map<string, Set<string>>();
  const used = new Set<string>();
  let loops = 0;
  const uplinks: LinkRow[] = [];
  // The first port-level line of each unordered device pair: what an uplink between them is attached to.
  const lineOf = new Map<string, Edge>();
  const pairKey = (x: string, y: string) => (x <= y ? `${x}|${y}` : `${y}|${x}`);
  for (const r of rows) {
    if (isUplink(r)) {
      uplinks.push(r);
      continue;
    }
    const [s, d] = r.a.deviceId <= r.b.deviceId ? [r.a.deviceId, r.b.deviceId] : [r.b.deviceId, r.a.deviceId];
    used.add(s).add(d);
    if (s === d) {
      loops++;
      continue;
    }
    const lvl = r.klass === "deduced" ? (r.level ?? undefined) : undefined;
    // The aggregate of each end, the source's side first: a LAG is named as its first device calls it.
    const [ps, pd] = r.a.deviceId === s ? [r.a, r.b] : [r.b, r.a];
    const lagS = t.lagOf.get(ps.id);
    const lagD = t.lagOf.get(pd.id);
    let lag: Edge["lag"];
    if (lagS !== undefined || lagD !== undefined) {
      const group = `${s}|${d}|lag:${lagS ?? ""}|${lagD ?? ""}`;
      const name = t.lagName.get((lagS ?? lagD) as string) ?? "";
      if (opts.expanded?.has(group)) {
        // Unfolded: each member is its own line, still keyed by its group so that it can be folded again.
        const id = `${s}|${d}|${r.klass}|${lvl ?? ""}|row:${r.key}`;
        const e: Edge = { id, source: s, target: d, klass: r.klass, level: lvl, count: 1, rows: [r], lag: { group, name, folded: false } };
        edges.set(id, e);
        if (!lineOf.has(pairKey(s, d))) lineOf.set(pairKey(s, d), e);
        continue;
      }
      lag = { group, name, folded: true };
    }
    const id = `${s}|${d}|${r.klass}|${lvl ?? ""}${lag ? `|${lag.group}` : ""}`;
    const e = edges.get(id) ?? { id, source: s, target: d, klass: r.klass, level: lvl, count: 0, rows: [], lag };
    const pairs = pairsOf.get(id) ?? new Set<string>();
    pairs.add([r.a.id, r.b.id].sort().join("|"));
    pairsOf.set(id, pairs);
    e.count = pairs.size;
    (e.rows ??= []).push(r);
    edges.set(id, e);
    if (!lineOf.has(pairKey(s, d))) lineOf.set(pairKey(s, d), e);
  }
  // An uplink between two devices a visible port-level line already joins is that line's, not a second one.
  let covered = 0;
  for (const r of uplinks) {
    const from = r.a.deviceId;
    const to = r.b.deviceId;
    used.add(from).add(to);
    if (from === to) {
      loops++;
      continue;
    }
    const line = lineOf.get(pairKey(from, to));
    if (line) {
      (line.uplinks ??= []).push(r);
      covered++;
      continue;
    }
    const lvl = r.klass === "deduced" ? (r.level ?? undefined) : undefined;
    const id = `${from}|${to}|uplink|${r.klass}|${lvl ?? ""}`;
    const e = edges.get(id) ?? { id, source: from, target: to, klass: r.klass, level: lvl, count: 1, rows: [], uplink: true };
    (e.rows ??= []).push(r);
    edges.set(id, e);
  }
  const nodes = [...used].map((id) => t.nodes.get(id)).filter((n): n is Node => n !== undefined);
  const byDomain = new Map<string, string[]>();
  for (const n of nodes) if (n.mlagDomain) byDomain.set(n.mlagDomain, [...(byDomain.get(n.mlagDomain) ?? []), n.id]);
  const groups = [...byDomain]
    .filter(([, members]) => members.length >= 2)
    .map(([domain, members]) => ({ id: `mlag:${domain}`, domain, members: members.sort() }))
    .sort((a, b) => a.id.localeCompare(b.id));
  return { nodes, edges: [...edges.values()], loops, groups, covered };
}
