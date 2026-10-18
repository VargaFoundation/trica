import { describe, expect, it } from "vitest";
import type { LiveTopology } from "../api/types";
import { DEVICES, L, T, lagContext, lagLinks, sampleContext, sampleInput, sampleLinks, sampleLive } from "./fixtures";
import {
  ageOf,
  edgeLive,
  formatBps,
  formatSpeed,
  formatUtil,
  isStale,
  lagLabel,
  liveLabel,
  rowLive,
  utilClass,
  widthOf,
  type EdgeLive,
} from "./live";
import { buildTopology, toGraph } from "./model";

const live = sampleLive() as LiveTopology;
const topo = () => buildTopology(sampleInput({ links: [...sampleLinks(), ...lagLinks()], context: [...sampleContext(), ...lagContext()] }));

describe("the figures of one link", () => {
  it("read the a end when it is measured", () => {
    const t = topo();
    const r = t.rows.find((x) => x.a.id === T.a1e2);
    expect(rowLive(r!, live.links)).toMatchObject({ out: 3.2e9, back: 1.1e9, util: 0.32, speedMbps: 10000, at: "a", oper: "up" });
  });
  it("turn the b end around when only it is measured: what b sends, a receives", () => {
    const t = topo();
    const r = t.rows.find((x) => x.a.id === L.a2e6);
    // a is sw-a2 Ethernet6 here (the relation's direction): measured on a.
    expect(rowLive(r!, live.links)).toMatchObject({ out: 1e9, back: 8e9, at: "a" });
    const swapped = { ...r!, a: r!.b, b: r!.a };
    expect(rowLive(swapped, live.links)).toMatchObject({ out: 8e9, back: 1e9, at: "b" });
  });
  it("are down when either end is down, and absent when neither is measured", () => {
    const t = topo();
    expect(rowLive(t.rows.find((x) => x.b.id === T.l1e1)!, live.links)?.oper).toBe("down");
    expect(rowLive(t.rows.find((x) => x.kind === "cable")!, live.links)).toBeUndefined();
  });
});

describe("an aggregate", () => {
  it("is one line between the two switches, named as the first device calls it", () => {
    const g = toGraph(topo(), topo().rows);
    const lag = g.edges.filter((e) => e.lag);
    expect(lag).toHaveLength(1);
    expect(lag[0]).toMatchObject({ count: 2, lag: { name: "Port-Channel11", folded: true } });
  });
  it("sums its members' traffic, read from the line's source, and its utilisation is over the summed speed", () => {
    const g = toGraph(topo(), topo().rows);
    const e = g.edges.find((x) => x.lag)!;
    const l = edgeLive(e, live.links);
    const fromA1 = e.source === T.swA1;
    // a1 -> a2: 7 Gb/s on Ethernet5, 8 Gb/s on Ethernet6 (what a2 receives); back: 2 + 1.
    expect(fromA1 ? [l.out, l.back] : [l.back, l.out]).toEqual([15e9, 3e9]);
    expect(l.speedMbps).toBe(20000);
    expect(l.util).toBeCloseTo(0.75);
    expect(l).toMatchObject({ measured: 2, total: 2, oper: "up", sources: ["arista"] });
    expect(lagLabel("Po11", e.rows ?? [], live.links, "en")).toBe("Po11 (2×10G)");
    expect(lagLabel("Po11", e.rows ?? [], {}, "en")).toBe("Po11 (2×)");
  });
  it("unfolds into its members when asked, and folds back", () => {
    const t = topo();
    const group = toGraph(t, t.rows).edges.find((e) => e.lag)!.lag!.group;
    const open = toGraph(t, t.rows, { expanded: new Set([group]) });
    const members = open.edges.filter((e) => e.lag?.group === group);
    expect(members).toHaveLength(2);
    expect(members.every((e) => e.count === 1 && e.lag?.folded === false)).toBe(true);
  });
});

describe("the colour of a line", () => {
  const l = (o: Partial<EdgeLive>): EdgeLive => ({ oper: "up", measured: 1, total: 1, sources: [], ...o });
  it.each([
    [l({ util: 0.1 }), "normal"],
    [l({ util: 0.49 }), "normal"],
    [l({ util: 0.5 }), "warn"],
    [l({ util: 0.79 }), "warn"],
    [l({ util: 0.8 }), "crit"],
    [l({ util: 1.2 }), "crit"],
    [l({ oper: "down", util: 0.9 }), "down"],
    [l({ oper: "partial", util: 0.1 }), "warn"],
    [l({ measured: 0, oper: "unknown" }), "unknown"],
    [l({ util: undefined }), "normal"],
  ])("%o is %s", (x, want) => expect(utilClass(x)).toBe(want));
});

describe("the width of a line", () => {
  it("is logarithmic, 1 px to 8 px", () => {
    expect(widthOf(undefined)).toBe(1);
    expect(widthOf(0)).toBe(1);
    expect(widthOf(1e3)).toBe(1);
    expect(widthOf(1e11)).toBe(8);
    expect(widthOf(1e13)).toBe(8);
    expect(widthOf(1e7)).toBe(4.5);
    expect(widthOf(1e9)).toBeGreaterThan(widthOf(1e8));
  });
});

describe("the words", () => {
  it("say a throughput in the reader's language", () => {
    expect(formatBps(3.2e9, "fr")).toBe("3,2 Gb/s");
    expect(formatBps(3.2e9, "en")).toBe("3.2 Gb/s");
    expect(formatBps(950, "en")).toBe("950 b/s");
    expect(formatBps(12_345_678, "en")).toBe("12 Mb/s");
    expect(formatSpeed(10000, "en")).toBe("10G");
    expect(formatSpeed(2500, "fr")).toBe("2,5G");
    expect(formatSpeed(100, "en")).toBe("100M");
    expect(formatUtil(0.32, "en")).toBe("32%");
    expect(liveLabel({ oper: "up", measured: 1, total: 1, sources: [], out: 3.2e9, back: 1.1e9 }, "fr")).toBe("↑ 3,2 Gb/s ↓ 1,1 Gb/s");
  });
});

describe("the age of the measurements", () => {
  const at = Date.parse("2026-10-18T10:00:00Z");
  it("counts seconds since generated_at", () => {
    expect(ageOf(live, at + 12_000)).toBe(12);
    expect(ageOf({ generated_at: undefined }, at)).toBeUndefined();
  });
  it("is stale when the server says so, when nothing ever came, or past three periods", () => {
    expect(isStale(live, at + 60_000)).toBe(false);
    expect(isStale(live, at + 91_000)).toBe(true);
    expect(isStale({ ...live, stale: true }, at)).toBe(true);
    expect(isStale({ ...live, generated_at: undefined }, at)).toBe(true);
  });
});

describe("an MLAG pair and the health of a device", () => {
  it("puts two switches sharing a domain in one box, not a lone one", () => {
    const devices = DEVICES.map((d) => (d.id === T.swA1 || d.id === T.swA2 ? { ...d, mlagDomain: "pair-1" } : d.id === T.swL1 ? { ...d, mlagDomain: "alone" } : d));
    const t = buildTopology(sampleInput({ devices }));
    expect(toGraph(t, t.rows).groups).toEqual([{ id: "mlag:pair-1", domain: "pair-1", members: [T.swA1, T.swA2].sort() }]);
  });
  it("is the worst of the device and its parts", () => {
    const devices = DEVICES.map((d) => (d.id === T.srv01 ? { ...d, health: "ok" as const, critical: 2 } : d));
    const t = buildTopology(sampleInput({ devices, partsHealth: new Map([[T.srv01, ["ok", "critical"] as const]]) }));
    expect(t.nodes.get(T.srv01)).toMatchObject({ health: "critical", critical: 2 });
    expect(t.nodes.get(T.swA1)?.health).toBe("unknown");
  });
});
