// The topology over uplinks a collector wrote. The answers in
// testdata/uplinks-api.json are not hand-written: internal/api
// TestUplinksReachTheTopologyAsWritten writes the uplinks through the engine,
// reads GET /relations and GET /objects back through the real API and fails
// when this file no longer matches what they return. Only the ids are renamed.
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/endpoints";
import type { ObjectView, Page, RelationLink, SearchPage } from "../api/types";
import snapshot from "./testdata/uplinks-api.json";
import { isUplink, toGraph } from "./model";
import { loadTopology } from "./useTopology";

const links = snapshot.links as unknown as RelationLink[];
const objects: Record<string, ObjectView[]> = {
  device: snapshot.devices as unknown as ObjectView[],
  site: snapshot.sites as unknown as ObjectView[],
};

const page = <T>(items: T[], offset = 0): Page<T> => ({ total: items.length, limit: 1000, offset, items: items.slice(offset) });

afterEach(() => vi.restoreAllMocks());

async function load() {
  const asked: string[] = [];
  vi.spyOn(api, "relationLinks").mockImplementation(async (q) => {
    const types = new Set(String(q.type).split(","));
    asked.push(...types);
    return page(
      links.filter((l) => types.has(l.type)),
      q.offset ?? 0,
    );
  });
  vi.spyOn(api, "objects").mockImplementation(async (q) => page(objects[q.type ?? ""] ?? [], q.offset ?? 0) as unknown as SearchPage);
  return { ...(await loadTopology()), asked };
}

describe("the topology over the uplinks a collector wrote", () => {
  it("asks for uplinked_to and reads each uplink as observed, from its source, between two devices", async () => {
    const { topology, asked } = await load();
    expect(asked).toContain("uplinked_to");
    const rows = topology.rows.filter(isUplink);
    const name = (id: string) => topology.nodes.get(id)?.name;
    expect(rows.map((r) => `${name(r.a.deviceId)}>${name(r.b.deviceId)} ${r.klass} ${r.source}`).sort()).toEqual([
      "ap-01>sw-a2 observed unifi:office-lan",
      "srv-01>sw-a1 observed unifi:office-lan",
      "sw-a2>sw-a1 observed unifi:office-lan",
    ]);
    expect(rows.every((r) => r.a.name === "" && r.b.name === "")).toBe(true);
    expect(topology.skipped.ends).toBe(0);
    // Every device the uplinks name is one the page knows, with its site.
    expect([...topology.nodes.values()].every((n) => !n.unresolved && n.siteId)).toBe(true);
  });

  it("draws one line per device pair: the cable's where one joins the pair, the uplink's otherwise, oriented towards the uplink", async () => {
    const { topology } = await load();
    const name = (id: string) => topology.nodes.get(id)?.name;
    const g = toGraph(topology, topology.rows);
    const lines = g.edges.map((e) => `${name(e.source)}>${name(e.target)}${e.uplink ? " uplink" : ""}`).sort();
    // The cable between sw-a1 and sw-a2 carries sw-a2's uplink; the other two uplinks are lines of their own.
    expect(lines).toHaveLength(3);
    expect(lines).toEqual(expect.arrayContaining(["ap-01>sw-a2 uplink", "srv-01>sw-a1 uplink"]));
    expect(lines.filter((l) => !l.endsWith("uplink"))).toHaveLength(1);
    expect(g.covered).toBe(1);
  });
});
