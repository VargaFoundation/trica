// Uplinks (ADR 0060, 0062) for the topology tests: a controller names the
// device another one reaches the network through, without any port. Built on
// the sample network of fixtures.ts (documentation-only names), written by
// hand, not recorded from a server.
import type { RelationLink } from "../api/types";
import { DEVICES, T, end, rel } from "./fixtures";
import type { DeviceInfo } from "./model";

export const UPLINK = { ap01: "p-dev-ap-01" };
export const AP01 = end(UPLINK.ap01, "device", "ap-01", undefined, T.paris);
export const device = (id: string, name: string, site: string) => end(id, "device", name, undefined, site);

/**
 * Three uplinks: ap-01 to sw-a2 (no cable between them: drawn), sw-a2 to sw-a1
 * (the declared cable C-0001 joins them already: attached to it), sw-l1 (Lyon)
 * to sw-a1 (no cable, across sites: drawn).
 */
export function sampleUplinks(): RelationLink[] {
  const o = { confidence: "observed", source: "unifi:office-lan" };
  return [
    rel("uplinked_to", AP01, device(T.swA2, "sw-a2", T.paris), o),
    rel("uplinked_to", device(T.swA2, "sw-a2", T.paris), device(T.swA1, "sw-a1", T.paris), o),
    rel("uplinked_to", device(T.swL1, "sw-l1", T.lyon), device(T.swA1, "sw-a1", T.paris), o),
  ];
}

export const DEVICES_WITH_AP: DeviceInfo[] = [...DEVICES, { id: UPLINK.ap01, name: "ap-01", siteId: T.paris, role: "access_point" }];
