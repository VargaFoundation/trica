// The topology over cables the real deducer wrote (#49). The answers in
// testdata/deduced-api.json are not hand-written: internal/api
// TestDeducedCablesReachTheTopologyAsWritten runs the deducer on observed LLDP
// and forwarding tables, reads GET /relations and GET /objects back through
// the real API and fails when this file no longer matches what they return.
// Only the ids are renamed (they are random per database).
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/endpoints";
import type { ObjectView, Page, RelationLink, SearchPage } from "../api/types";
import snapshot from "./testdata/deduced-api.json";
import { toGraph } from "./model";
import { loadTopology } from "./useTopology";

const links = snapshot.links as unknown as RelationLink[];
const objects: Record<string, ObjectView[]> = {
  device: snapshot.devices as unknown as ObjectView[],
  site: snapshot.sites as unknown as ObjectView[],
};

const page = <T>(items: T[], offset = 0): Page<T> => ({
  total: items.length,
  limit: 1000,
  offset,
  items: items.slice(offset),
});

afterEach(() => vi.restoreAllMocks());

async function load() {
  vi.spyOn(api, "relationLinks").mockImplementation(async (q) => {
    const types = new Set(String(q.type).split(","));
    return page(
      links.filter((l) => types.has(l.type)),
      q.offset ?? 0,
    );
  });
  vi.spyOn(api, "objects").mockImplementation(
    async (q) =>
      page(objects[q.type ?? ""] ?? [], q.offset ?? 0) as unknown as SearchPage,
  );
  return loadTopology();
}

describe("the topology over the deducer's own cables", () => {
  it("shows each deduced cable as deduced, with the level and evidence the deducer gave", async () => {
    const { topology } = await load();
    const deduced = topology.rows
      .filter((r) => r.source === "deduce:cabling")
      .map((r) => ({
        klass: r.klass,
        level: r.level,
        evidence: r.evidence,
        ends: [r.a.name, r.b.name].sort().join("-"),
      }));
    expect(deduced).toHaveLength(3);
    expect(deduced).toEqual(
      expect.arrayContaining([
        {
          klass: "deduced",
          level: "high",
          evidence: "lldp",
          ends: "Ethernet1-Ethernet1",
        },
        {
          klass: "deduced",
          level: "medium",
          evidence: "lldp_one_sided",
          ends: "Ethernet2-eno2",
        },
        {
          klass: "deduced",
          level: "low",
          evidence: "mac_table",
          ends: "Ethernet3-eno1",
        },
      ]),
    );
  });

  it("draws them between the devices the ports are on, one line per level", async () => {
    const { topology } = await load();
    const name = (id: string) => topology.nodes.get(id)?.name;
    const g = toGraph(
      topology,
      topology.rows.filter((r) => r.klass === "deduced"),
    );
    const edges = g.edges
      .map(
        (e) =>
          `${[name(e.source), name(e.target)].sort().join("|")} ${e.level}`,
      )
      .sort();
    expect(edges).toEqual([
      "srv-02|sw-a2 low",
      "srv-02|sw-a2 medium",
      "sw-a1|sw-a2 high",
    ]);
    // Every end resolved to a device the page knows, with its site.
    expect(g.nodes.every((n) => !n.unresolved && n.siteId)).toBe(true);
  });

  // The topology also takes a device's site from its links' ends, which the
  // real API always fills, so this agrees with or without the device's own
  // site; what it pins is that the real list carries it (#100).
  it("reads each device's site from GET /objects, as the real API now gives it", async () => {
    const devices = snapshot.devices as unknown as ObjectView[];
    // The real API's list says where each device is.
    expect(devices.every((d) => typeof d.site_id === "string")).toBe(true);
    const { topology } = await load();
    for (const d of devices) {
      const node = topology.nodes.get(d.id);
      if (node) expect([d.display, node.siteId]).toEqual([d.display, d.site_id]);
    }
  });
});
