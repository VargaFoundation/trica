import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TopologyMap } from "../../api/types";
import { I18nProvider } from "../../i18n";
import { deviceFront } from "./decor";
import { IsoScene } from "./IsoScene";
import { buildScene } from "./scene";
import { BUSY_RACK } from "./testdata/busyRack";
import { MAPS } from "./testdata/maps";

// Things on the map read as what they are (ADR 0066, amendment "visual language"): a device's front by
// its role, a rack as a cabinet with blanking plates, the Internet and other places as badges.
const R = { x: 0, y: 0, w: 2.4, d: 2.4 };

describe("the front of a device", () => {
  it.each([
    ["switch", "switch"],
    ["patch_panel", "switch"],
    ["firewall", "gateway"],
    ["router", "gateway"],
    ["server", "server"],
    ["storage", "storage"],
    ["nas", "storage"],
    ["pdu", "pdu"],
    ["ups", "pdu"],
    [undefined, "generic"],
    ["camera", "generic"],
  ])("a %s shows the front of a %s", (role, kind) => {
    const f = deviceFront(role, R, 0, 0.32);
    expect(f.kind).toBe(kind);
    expect(f.holes).toMatch(/^M/);
    expect(f.screen !== undefined).toBe(kind === "gateway");
  });

  it("gives a switch more ports than a firewall, and a tall one more rows", () => {
    const count = (d: string) => (d.match(/M/g) ?? []).length;
    expect(count(deviceFront("switch", R, 0, 0.32).holes)).toBeGreaterThan(count(deviceFront("firewall", R, 0, 0.32).holes));
    expect(count(deviceFront("switch", R, 0, 0.7).holes)).toBeGreaterThan(count(deviceFront("switch", R, 0, 0.32).holes));
    expect(deviceFront("server", R, 0, 0.32).lines).toMatch(/^M/);
  });

  it("draws the front of every device of a rack, by its role", () => {
    const s = buildScene(BUSY_RACK, 0);
    for (const i of s.items.filter((x) => x.role === "device")) expect(i.front?.kind, i.id).toBe(i.node.role === "switch" ? "switch" : i.node.role === "firewall" ? "gateway" : "server");
  });
});

describe("the rack's cabinet", () => {
  it("has its far panels and blanking plates over its empty units only", () => {
    const f = buildScene(MAPS.rack, 0).frame;
    expect(f?.panels).toHaveLength(2);
    expect(f?.blanks).toMatch(/^M/);
    // One device per unit from 1 to 12: nothing to blank.
    const full: TopologyMap = {
      ...MAPS.rack,
      edges: [],
      nodes: Array.from({ length: 12 }, (_, k) => ({ ...MAPS.rack.nodes[0]!, id: `d${k}`, display: `d${k}`, mount: { position: k + 1, u_height: 1, full_depth: true } })),
    };
    expect(buildScene(full, 0).units).toBe(12);
    expect(buildScene(full, 0).frame?.blanks).toBe("");
  });
});

describe("the places at the end of the lines", () => {
  const draw = (m: TopologyMap) =>
    render(
      <I18nProvider initial="en">
        <IsoScene map={m} rot={0} onSelect={() => {}} onDrill={() => {}} onUp={() => {}} flowOn={false} moveFocus={false} busy={false} since={0} label="map" />
      </I18nProvider>,
    ).container;

  it("draws the Internet and another place as round badges, without the old panel and posts", () => {
    const c = draw(MAPS.rack);
    const gates = c.querySelectorAll(".iso-internet, .iso-portal");
    expect(gates.length).toBeGreaterThan(1);
    for (const g of gates) {
      expect(g.querySelector(".iso-gate-disc"), g.getAttribute("data-id") ?? "").not.toBeNull();
      expect(g.querySelector(".iso-post, .iso-face")).toBeNull();
    }
    expect(c.querySelector(".iso-internet .iso-cloud")).not.toBeNull();
  });

  it("joins no slab to its card by a line: pointing at either lights both", () => {
    const c = draw(BUSY_RACK);
    expect(c.querySelectorAll(".iso-item.has-card").length).toBeGreaterThan(5);
    expect(c.querySelector(".iso-leader-line")).toBeNull();
  });
});
