// A busy rack, shaped on a real one: three switches (a management one and two
// leaves), seven servers cabled to all three (data to each leaf, BMC to the
// management switch), one more server on the management switch only, a bastion
// firewall linked to nothing but the Internet, a server with a tunnel to another
// site. Synthesised: the names, models and ports are made up; the shape is the
// one that tangled a real rack's tree.
import type { MapEdge, MapNode, TopologyMap } from "../../../api/types";

const counts = { rooms: 0, racks: 0, devices: 0, internal_links: 0 };
const health = (worst = "ok") => ({ worst, ok: 1, warning: 0, critical: 0, unknown: 0, critical_events: 0 });
const dev = (id: string, role: string, model: string, position: number, u = 1): MapNode => ({
  id,
  kind: "device",
  display: id,
  role,
  model,
  mount: { position, u_height: u, full_depth: true },
  counts,
  health: health(),
});
const place = (id: string, type = "device") => ({ id, type, display: id });
/** A physical edge between two devices, one member per pair of ports. */
const cable = (a: string, b: string, ports: [string, string][]): MapEdge => {
  const [x, y, ps] = a < b ? [a, b, ports] : [b, a, ports.map(([p, q]) => [q, p] as [string, string])];
  return {
    id: `${x}~${y}~physical`,
    a: x,
    b: y,
    medium: "physical",
    kinds: { cable: ps.length, cabled_to: 0, neighbor_of: 0, tunnel: 0, uplink: 0, uplinked_to: 0 },
    links: ps.length,
    confidence: "observed",
    capacity_mbps: 10000 * ps.length,
    capacity_partial: false,
    oper: "up",
    measured: false,
    members: ps.map(([p, q]) => ({ a_device: place(x), a_port: { id: `${x}/${p}`, type: "port", display: p }, b_device: place(y), b_port: { id: `${y}/${q}`, type: "port", display: q }, kind: "cable", confidence: "observed" })),
    members_truncated: false,
  };
};

const servers = ["comp-svr1", "comp-svr2", "comp-svr3", "mgmt-svr1", "mgmt-svr2", "build-svr1", "data-svr1"];
const nodes: MapNode[] = [
  dev("sw-mgmt", "switch", "CRS354-48G-4S+2Q+", 42),
  dev("sw-leaf1", "switch", "DCS-7050SX-64", 41),
  dev("sw-leaf2", "switch", "DCS-7280SE-64-R", 40),
  dev("bastion", "firewall", "", 38),
  ...servers.map((s, i) => dev(s, "server", "ProLiant DL360 Gen10", 30 - i * 2, 2)),
  dev("infrasrv1", "server", "PowerEdge R330", 14),
  { id: "portal:internet", kind: "portal", place_type: "internet", display: "Internet", counts, health: health("unknown") },
  { id: "portal:<pereire>", kind: "portal", place_type: "site", display: "Pereire Paris", drill: { level: "site", id: "<pereire>" }, counts, health: health("unknown") },
];
const edges: MapEdge[] = [
  cable("sw-leaf1", "sw-leaf2", [["Et49/1", "Et49/1"], ["Et50/1", "Et50/1"]]),
  cable("sw-mgmt", "sw-leaf1", [["sfp-plus1", "Et1"]]),
  cable("sw-mgmt", "sw-leaf2", [["sfp-plus2", "Et1"]]),
  ...servers.flatMap((s, i) => [
    cable(s, "sw-leaf1", [["ens1", `Et${5 + i * 2}`]]),
    cable(s, "sw-leaf2", [["ens1d1", `Et${5 + i * 2}`]]),
    cable(s, "sw-mgmt", [["bmc", `ether${10 + i * 2}`]]),
  ]),
  cable("infrasrv1", "sw-mgmt", [["eno1", "ether34"]]),
  {
    id: "bastion~portal:internet~uplink",
    a: "bastion",
    b: "portal:internet",
    medium: "uplink",
    kinds: { cable: 0, cabled_to: 0, neighbor_of: 0, tunnel: 0, uplink: 1, uplinked_to: 0 },
    links: 1,
    confidence: "declared",
    capacity_mbps: 1000,
    capacity_partial: false,
    oper: "up",
    measured: false,
    members: [],
    members_truncated: false,
  },
  {
    id: "data-svr1~portal:<pereire>~tunnel",
    a: "data-svr1",
    b: "portal:<pereire>",
    medium: "tunnel",
    kinds: { cable: 0, cabled_to: 0, neighbor_of: 0, tunnel: 1, uplink: 0, uplinked_to: 0 },
    links: 1,
    confidence: "declared",
    capacity_partial: false,
    oper: "up",
    measured: false,
    tunnel: { kind: "wireguard", status: "up", resolved: true },
    members: [],
    members_truncated: false,
  },
];

export const BUSY_RACK: TopologyMap = {
  level: "rack",
  focus: { id: "<baie6>", type: "rack", display: "Baie 6" },
  trail: [
    { id: "<site>", type: "site", display: "site" },
    { id: "<room>", type: "room", display: "room" },
  ],
  inventory_at: "2026-03-01T08:00:00Z",
  truncated: false,
  live: { enabled: false, stale: false, refresh_seconds: 15 },
  notes: { ambiguous_placements: 0, links_without_place: 0, unresolved_tunnels: 0, unplaced_devices: 0 },
  nodes,
  edges,
} as unknown as TopologyMap;
