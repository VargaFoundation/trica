// A small anonymised network for the topology tests (documentation-only
// names: sw-a1, sw-a2, srv-01, paris-1, lyon-1). Typed by the generated API
// shapes, written by hand, not recorded from a server.
import type { RelationEnd, RelationLink } from "../api/types";
import type { DeviceInfo, TopologyInput } from "./model";

export const T = {
  paris: "p-site-paris",
  lyon: "p-site-lyon",
  rackP: "p-rack-paris-1",
  rackL: "p-rack-lyon-1",
  vlan10: "p-vlan-10",
  vlan20: "p-vlan-20",
  swA1: "p-dev-sw-a1",
  swA2: "p-dev-sw-a2",
  srv01: "p-dev-srv-01",
  swL1: "p-dev-sw-l1",
  mod: "p-mod-linecard-1",
  a1e1: "p-if-a1-e1",
  a1e2: "p-if-a1-e2",
  a1e3: "p-if-a1-e3",
  a2e1: "p-if-a2-e1",
  a2e2: "p-if-a2-e2",
  srvEno1: "p-if-srv-eno1",
  l1e1: "p-if-l1-e1",
  modPort: "p-if-mod-1",
  cable1: "p-cable-1",
};

export const end = (id: string, type: string, display: string, parent?: string, site?: string): RelationEnd => ({
  id,
  type,
  display,
  parent_id: parent,
  site_id: site,
});

export const port = (id: string, display: string, device: string, site: string | undefined = T.paris) => end(id, "interface", display, device, site);

let seq = 0;
export function rel(
  type: string,
  from: RelationEnd,
  to: RelationEnd,
  o: { confidence?: string; source?: string; attributes?: Record<string, unknown> } = {},
): RelationLink {
  seq++;
  return {
    id: seq,
    type,
    from_id: from.id,
    to_id: to.id,
    from,
    to,
    source: o.source ?? "collector:lldp",
    confidence: o.confidence ?? "observed",
    attributes: o.attributes ?? {},
    valid_from: "2026-10-01T00:00:00Z",
  };
}

export const A1E1 = port(T.a1e1, "Ethernet1", T.swA1);
export const A1E2 = port(T.a1e2, "Ethernet2", T.swA1);
export const A1E3 = port(T.a1e3, "Ethernet3", T.swA1);
export const A2E1 = port(T.a2e1, "Ethernet1", T.swA2);
export const A2E2 = port(T.a2e2, "Ethernet2", T.swA2);
export const ENO1 = port(T.srvEno1, "eno1", T.srv01);
export const L1E1 = port(T.l1e1, "Ethernet1", T.swL1, T.lyon);
export const CABLE1 = end(T.cable1, "cable", "C-0001");

/** Every kind of link once: a documented cable, a deduced one with a level, an observed one, a low-confidence one across sites. */
export function sampleLinks(): RelationLink[] {
  return [
    rel("connects", CABLE1, A1E1, { confidence: "declared", source: "declaration:cabling", attributes: { end: "a" } }),
    rel("connects", CABLE1, A2E1, { confidence: "declared", source: "declaration:cabling", attributes: { end: "b" } }),
    rel("cabled_to", A1E2, ENO1, { confidence: "deduced", source: "deduce:cabling", attributes: { level: "high", evidence: "lldp" } }),
    rel("cabled_to", A2E2, L1E1, { confidence: "deduced", source: "deduce:cabling", attributes: { level: "low", evidence: "mac_table" } }),
    rel("neighbor_of", A1E3, A2E2, { confidence: "observed", attributes: { protocol: "lldp" } }),
  ];
}

export function sampleContext(): RelationLink[] {
  const rackP = end(T.rackP, "rack", "paris-1", undefined, T.paris);
  const rackL = end(T.rackL, "rack", "lyon-1", undefined, T.lyon);
  const dev = (id: string, name: string, site: string) => end(id, "device", name, T.rackP, site);
  return [
    rel("mounted_in", dev(T.swA1, "sw-a1", T.paris), rackP, { confidence: "declared" }),
    rel("mounted_in", dev(T.swA2, "sw-a2", T.paris), rackP, { confidence: "declared" }),
    rel("mounted_in", dev(T.swL1, "sw-l1", T.lyon), rackL, { confidence: "declared" }),
    rel("member_of", A1E2, end(T.vlan10, "vlan", "VLAN 10"), { confidence: "declared", attributes: { tagged: false } }),
    rel("member_of", ENO1, end(T.vlan10, "vlan", "VLAN 10"), { confidence: "declared", attributes: { tagged: false } }),
    rel("member_of", A2E2, end(T.vlan20, "vlan", "VLAN 20"), { confidence: "declared", attributes: { tagged: true } }),
  ];
}

export const DEVICES: DeviceInfo[] = [
  { id: T.swA1, name: "sw-a1", siteId: T.paris, role: "switch" },
  { id: T.swA2, name: "sw-a2", siteId: T.paris, role: "switch" },
  { id: T.srv01, name: "srv-01", siteId: T.paris, role: "server" },
  { id: T.swL1, name: "sw-l1", siteId: T.lyon, role: "switch" },
];

export function sampleInput(over: Partial<TopologyInput> = {}): TopologyInput {
  return {
    links: sampleLinks(),
    context: sampleContext(),
    devices: DEVICES,
    moduleDevice: new Map(),
    sites: new Map([
      [T.paris, "paris-1"],
      [T.lyon, "lyon-1"],
    ]),
    ...over,
  };
}
