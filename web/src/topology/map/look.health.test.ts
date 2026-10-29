import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { buildScene } from "./scene";
import { MAPS } from "./testdata/maps";

// Health is a status LED on the thing and a glow under what is critical, never a colour across a
// whole block (ADR 0066, amendment "visual language").
const css = readFileSync("src/styles/app.css", "utf8");

describe("health on the map", () => {
  it("no health class colours the faces of a block", () => {
    const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].filter(([, sel]) => /\.iso-item\.h-(ok|warning|critical|unknown)/.test(sel ?? ""));
    for (const [, sel, body] of rules) {
      if (/\.iso-(face|top|south|east)\b/.test(sel ?? "")) expect(body, sel).not.toMatch(/\b(fill|stroke)\s*:/);
    }
  });

  it("gives every block a status LED but racks, which show it on their door, and gates", () => {
    for (const lv of ["global", "site", "room", "rack", "unplaced"] as const) {
      for (const i of buildScene(MAPS[lv], 0).items) {
        const has = i.led !== undefined;
        const want = i.role !== "rack" && i.role !== "internet" && i.role !== "portal";
        expect(has, `${lv}: ${i.id} (${i.role})`).toBe(want);
        if (i.role === "rack") expect(i.door?.leds.length, i.id).toBeGreaterThan(0);
      }
    }
  });

  it("puts the LED on the front of the block it belongs to", () => {
    for (const i of buildScene(MAPS.rack, 0).items.filter((x) => x.led)) {
      const b = i.iso.bounds;
      expect(i.led!.x, i.id).toBeGreaterThanOrEqual(b.x0);
      expect(i.led!.x, i.id).toBeLessThanOrEqual(b.x1);
      expect(i.led!.y, i.id).toBeGreaterThanOrEqual(b.y0);
      expect(i.led!.y, i.id).toBeLessThanOrEqual(b.y1);
    }
  });
});
