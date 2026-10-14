import { describe, expect, it } from "vitest";
import {
  A1E1,
  A1E2,
  A1E3,
  A2E1,
  A2E2,
  CABLE1,
  ENO1,
  L1E1,
  T,
  end,
  port,
  rel,
  sampleInput,
  sampleLinks,
} from "./fixtures";
import { ALL_CLASSES, buildTopology, classOf, filterRows, toGraph, type LinkClass } from "./model";

const build = (over = {}) => buildTopology(sampleInput(over));
const row = (t: ReturnType<typeof build>, key: string) => t.rows.find((r) => r.key.startsWith(key));

describe("what a link is known as", () => {
  it("keeps the confidence the relation was stored with", () => {
    expect(classOf("declared")).toBe("declared");
    expect(classOf("observed")).toBe("observed");
    expect(classOf("deduced")).toBe("deduced");
  });

  it("never reads an unknown confidence as a better one", () => {
    expect(classOf("")).toBe("deduced");
    expect(classOf("certain")).toBe("deduced");
  });

  it("gives each row of the sample its own class", () => {
    const t = build();
    const by = Object.fromEntries(t.rows.map((r) => [r.kind + ":" + r.a.name + ":" + r.b.name, r.klass]));
    expect(by["cable:Ethernet1:Ethernet1"]).toBe("declared");
    expect(by["cabled_to:Ethernet2:eno1"]).toBe("deduced");
    expect(by["neighbor_of:Ethernet3:Ethernet2"]).toBe("observed");
  });

  it("carries the level and the evidence of a deduced link, and nothing for the others", () => {
    const t = build();
    const high = t.rows.find((r) => r.b.name === "eno1");
    expect(high).toMatchObject({ klass: "deduced", level: "high", evidence: "lldp", source: "deduce:cabling" });
    const low = t.rows.find((r) => r.b.name === "Ethernet1" && r.a.name === "Ethernet2" && r.kind === "cabled_to");
    expect(low).toMatchObject({ level: "low", evidence: "mac_table" });
    expect(t.rows.find((r) => r.kind === "neighbor_of")?.level).toBeUndefined();
  });

  it("ignores a level on a link that is not deduced (a level says how well a deduction is backed)", () => {
    const links = [rel("cabled_to", A1E2, ENO1, { confidence: "observed", attributes: { level: "high" } })];
    expect(build({ links }).rows[0]?.level).toBeUndefined();
  });

  it("does not invent a level the deducer did not say", () => {
    const links = [
      rel("cabled_to", A1E2, ENO1, { confidence: "deduced", attributes: {} }),
      rel("cabled_to", A1E3, A2E1, { confidence: "deduced", attributes: { level: "certain" } }),
    ];
    const t = build({ links });
    expect(t.rows.map((r) => r.level)).toEqual([undefined, undefined]);
  });
});

describe("cables", () => {
  it("is one link between the two ports a documented cable connects, end a first", () => {
    const links = [
      rel("connects", CABLE1, A2E1, { confidence: "declared", attributes: { end: "b" } }),
      rel("connects", CABLE1, A1E1, { confidence: "declared", attributes: { end: "a" } }),
    ];
    const t = build({ links });
    expect(t.rows).toHaveLength(1);
    expect(t.rows[0]).toMatchObject({ kind: "cable", klass: "declared", cable: { id: T.cable1, label: "C-0001" } });
    expect(t.rows[0]?.a.id).toBe(T.a1e1);
    expect(t.rows[0]?.b.id).toBe(T.a2e1);
  });

  it("is only as certain as its least certain end", () => {
    const links = [
      rel("connects", CABLE1, A1E1, { confidence: "declared", attributes: { end: "a" } }),
      rel("connects", CABLE1, A2E1, { confidence: "observed", attributes: { end: "b" } }),
    ];
    expect(build({ links }).rows[0]?.klass).toBe("observed");
  });

  it("is not drawn with one end, and the page is told", () => {
    const links = [rel("connects", CABLE1, A1E1, { confidence: "declared", attributes: { end: "a" } })];
    const t = build({ links });
    expect(t.rows).toHaveLength(0);
    expect(t.skipped.cables).toBe(1);
  });

  it("is not drawn with three ends", () => {
    const links = [
      rel("connects", CABLE1, A1E1, { attributes: { end: "a" } }),
      rel("connects", CABLE1, A2E1, { attributes: { end: "b" } }),
      rel("connects", CABLE1, A1E2, { attributes: { end: "c" } }),
    ];
    const t = build({ links });
    expect(t.rows).toHaveLength(0);
    expect(t.skipped.cables).toBe(1);
  });

  it("is not drawn when an end is not a port", () => {
    const links = [
      rel("connects", CABLE1, A1E1, { attributes: { end: "a" } }),
      rel("connects", CABLE1, end(T.srv01, "device", "srv-01"), { attributes: { end: "b" } }),
    ];
    const t = build({ links });
    expect(t.rows).toHaveLength(0);
    expect(t.skipped.cables).toBe(1);
  });
});

describe("ends that are not ports", () => {
  it("counts a link that does not join two interfaces instead of drawing it", () => {
    const links = [rel("cabled_to", A1E1, end(T.srv01, "device", "srv-01"))];
    const t = build({ links });
    expect(t.rows).toHaveLength(0);
    expect(t.skipped.ends).toBe(1);
  });

  it("puts a port of a chassis module on the module's device", () => {
    const modPort = port(T.modPort, "Ethernet9/1", T.mod);
    const links = [rel("cabled_to", modPort, ENO1)];
    const t = build({ links, moduleDevice: new Map([[T.mod, T.swA1]]) });
    expect(t.rows[0]?.a.deviceId).toBe(T.swA1);
    expect(t.nodes.get(T.swA1)?.name).toBe("sw-a1");
  });

  it("keeps a device the page does not know, marked, under a short name", () => {
    const stray = port("p-if-stray", "Ethernet1", "0123456789abcdef");
    const t = build({ links: [rel("cabled_to", stray, ENO1)] });
    expect(t.nodes.get("0123456789abcdef")).toMatchObject({ unresolved: true, name: "01234567" });
    expect(t.nodes.get(T.srv01)?.unresolved).toBe(false);
  });
});

describe("where a device stands", () => {
  it("knows its rack and its site, and which VLANs a port is in", () => {
    const t = build();
    expect(t.nodes.get(T.swA1)).toMatchObject({ siteId: T.paris, rackId: T.rackP });
    expect(t.nodes.get(T.swL1)).toMatchObject({ siteId: T.lyon, rackId: T.rackL });
    expect(t.vlanOf.get(T.a1e2)).toEqual(new Set([T.vlan10]));
    expect([...t.racks.values()].sort()).toEqual(["lyon-1", "paris-1"]);
    expect([...t.vlans.values()].sort()).toEqual(["VLAN 10", "VLAN 20"]);
  });

  it("falls back to the site its ports say when the device list has none", () => {
    const devices = [{ id: T.srv01, name: "srv-01" }];
    const t = build({ devices });
    expect(t.nodes.get(T.srv01)?.siteId).toBe(T.paris);
  });
});

const keys = (t: ReturnType<typeof build>, f: Partial<Parameters<typeof filterRows>[1]> = {}) =>
  filterRows(t, { classes: ALL_CLASSES, ...f }).map((r) => `${r.a.name}>${r.b.name}`).sort();

describe("filters", () => {
  const t = build();

  it("shows everything with no filter", () => {
    expect(t.rows).toHaveLength(4);
    expect(keys(t)).toHaveLength(4);
  });

  it("keeps the classes asked for", () => {
    const only = (...c: LinkClass[]) => filterRows(t, { classes: new Set(c) }).map((r) => r.klass);
    expect(only("declared")).toEqual(["declared"]);
    expect(only("observed")).toEqual(["observed"]);
    expect(only("deduced")).toEqual(["deduced", "deduced"]);
    expect(only("declared", "observed")).toHaveLength(2);
  });

  it("keeps a link with at least one end in the site, so one that leaves it is still seen", () => {
    expect(keys(t, { site: T.lyon })).toEqual(["Ethernet2>Ethernet1"]); // sw-a2 Ethernet2 to sw-l1 Ethernet1
    expect(keys(t, { site: T.paris })).toHaveLength(4);
  });

  it("filters by rack", () => {
    expect(keys(t, { rack: T.rackL })).toEqual(["Ethernet2>Ethernet1"]);
    expect(keys(t, { rack: T.rackP })).toHaveLength(4);
  });

  it("filters by VLAN, through the ports that are members", () => {
    expect(keys(t, { vlan: T.vlan10 })).toEqual(["Ethernet2>eno1"]);
    expect(keys(t, { vlan: T.vlan20 }).sort()).toEqual(["Ethernet2>Ethernet1", "Ethernet3>Ethernet2"]);
  });

  it("combines filters: all of them must hold", () => {
    expect(keys(t, { vlan: T.vlan20, site: T.lyon })).toEqual(["Ethernet2>Ethernet1"]);
    expect(keys(t, { vlan: T.vlan10, site: T.lyon })).toEqual([]);
    expect(keys(t, { vlan: T.vlan20, classes: new Set<LinkClass>(["observed"]) })).toEqual(["Ethernet3>Ethernet2"]);
  });
});

describe("the graph", () => {
  const graphOf = (links = sampleLinks(), over = {}) => {
    const t = build({ links, ...over });
    return toGraph(t, t.rows);
  };

  it("has a node per device that has a link, and no other", () => {
    const g = graphOf();
    expect(g.nodes.map((n) => n.name).sort()).toEqual(["sw-a1", "sw-a2", "sw-l1", "srv-01"].sort());
    const none = graphOf([]);
    expect(none.nodes).toHaveLength(0);
  });

  it("has a line per device pair, class and level", () => {
    const g = graphOf();
    expect(g.edges).toHaveLength(4);
    const between = (a: string, b: string) => g.edges.filter((e) => [e.source, e.target].sort().join() === [a, b].sort().join());
    // sw-a1 and sw-a2 are joined by a documented cable and by an observed neighbour: two lines, not one.
    expect(between(T.swA1, T.swA2).map((e) => e.klass).sort()).toEqual(["declared", "observed"]);
    expect(between(T.swA1, T.srv01)).toMatchObject([{ klass: "deduced", level: "high" }]);
    expect(between(T.swA2, T.swL1)).toMatchObject([{ klass: "deduced", level: "low" }]);
  });

  it("keeps parallel links of different classes apart", () => {
    const links = [rel("cabled_to", A1E2, A2E2, { confidence: "observed" }), rel("cabled_to", A1E3, A2E1, { confidence: "deduced", attributes: { level: "high" } })];
    const g = graphOf(links);
    expect(g.edges.map((e) => e.klass).sort()).toEqual(["deduced", "observed"]);
  });

  it("keeps deduced links of different levels apart", () => {
    const links = [
      rel("cabled_to", A1E2, A2E2, { confidence: "deduced", attributes: { level: "high" } }),
      rel("cabled_to", A1E3, A2E1, { confidence: "deduced", attributes: { level: "low" } }),
    ];
    expect(graphOf(links).edges.map((e) => e.level).sort()).toEqual(["high", "low"]);
  });

  it("merges the links of one class between two devices and counts the distinct port pairs", () => {
    const links = [
      rel("cabled_to", A1E2, A2E2, { confidence: "observed" }),
      rel("cabled_to", A1E3, A2E1, { confidence: "observed" }),
      // The same two ports again, from the other side: one pair, not two.
      rel("neighbor_of", A2E2, A1E2, { confidence: "observed" }),
    ];
    const g = graphOf(links);
    expect(g.edges).toHaveLength(1);
    expect(g.edges[0]?.count).toBe(2);
  });

  it("does not care which way a link was written", () => {
    const forward = graphOf([rel("cabled_to", A1E2, A2E2)]);
    const backward = graphOf([rel("cabled_to", A2E2, A1E2)]);
    expect(forward.edges[0]?.id).toBe(backward.edges[0]?.id);
  });

  it("leaves a link inside one device out of the drawing and counts it", () => {
    const g = graphOf([rel("cabled_to", A1E1, A1E2), rel("cabled_to", A1E3, A2E1)]);
    expect(g.loops).toBe(1);
    expect(g.edges).toHaveLength(1);
  });

  it("draws only the rows it is given", () => {
    const t = build();
    const g = toGraph(t, filterRows(t, { classes: new Set<LinkClass>(["declared"]) }));
    expect(g.edges).toHaveLength(1);
    expect(g.nodes.map((n) => n.name).sort()).toEqual(["sw-a1", "sw-a2"]);
  });

  it("is built from rows whose ports are on a known device", () => {
    expect(row(build(), "cable:")?.cable?.label).toBe("C-0001");
    expect(L1E1.parent_id).toBe(T.swL1);
  });
});
