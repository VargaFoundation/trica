import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { ObjectView, Page } from "../api/types";
import { buildTopology, CONTEXT_TYPES, LINK_TYPES, type DeviceInfo, type Topology } from "./model";

const PAGE = 1000;
/** Pages read per list: 10 000 rows. Beyond it the page says it stopped. */
export const MAX_PAGES = 10;

export interface TopologyData {
  topology: Topology;
  /** A list was longer than what the page reads (or the server stopped at its own bound): the picture is partial. */
  truncated: boolean;
}

/** Reads every page of a list, up to MAX_PAGES. */
export async function readAll<T>(
  get: (offset: number) => Promise<Page<T>>,
): Promise<{ items: T[]; truncated: boolean }> {
  const items: T[] = [];
  // The server says "truncated" when it stopped at its own scan bound: its total is then a lower bound.
  let serverStopped = false;
  for (let n = 0; n < MAX_PAGES; n++) {
    const p = await get(items.length);
    items.push(...p.items);
    serverStopped ||= p.truncated === true;
    if (p.items.length === 0 || items.length >= p.total) return { items, truncated: serverStopped };
  }
  return { items, truncated: true };
}

const deviceOf = (o: ObjectView): DeviceInfo => ({
  id: o.id,
  name: o.display,
  siteId: o.site_id,
  role: typeof o.attributes?.role === "string" ? o.attributes.role : undefined,
});

/**
 * Everything the topology needs, from the public API only: the links (one
 * bulk call per page, see GET /relations), the racks and VLANs they sit in,
 * the devices (for their names and sites), the modules (a port on a chassis
 * module belongs to the module's device) and the sites. The UI only reads.
 */
export async function loadTopology(signal?: AbortSignal): Promise<TopologyData> {
  const rels = (types: readonly string[]) =>
    readAll((offset) => api.relationLinks({ type: types.join(","), limit: PAGE, offset }, signal));
  const objects = (type: string) => readAll((offset) => api.objects({ type, limit: PAGE, offset }, signal));
  const [links, context, devices, modules, sites] = await Promise.all([
    rels(LINK_TYPES),
    rels(CONTEXT_TYPES),
    objects("device"),
    objects("module"),
    objects("site"),
  ]);
  const moduleDevice = new Map<string, string>();
  for (const m of modules.items) if (m.parent_id) moduleDevice.set(m.id, m.parent_id);
  const topology = buildTopology({
    links: links.items,
    context: context.items,
    devices: devices.items.map(deviceOf),
    moduleDevice,
    sites: new Map(sites.items.map((s) => [s.id, s.display])),
  });
  return { topology, truncated: [links, context, devices, modules, sites].some((r) => r.truncated) };
}

export function useTopology() {
  return useQuery({ queryKey: ["topology"], queryFn: ({ signal }) => loadTopology(signal), staleTime: 30_000 });
}
