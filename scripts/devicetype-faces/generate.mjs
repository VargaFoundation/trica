#!/usr/bin/env node
// Generates the face drawings of the device-type library.
//
//   node scripts/devicetype-faces/generate.mjs          # SVG sources only
//   node scripts/devicetype-faces/generate.mjs --png    # and the PNG the library serves
//   node scripts/devicetype-faces/generate.mjs --inspect DIR  # 3000 px PNG in DIR, to check counts
//
// SVG sources go to internal/devicetypes/library/drawings/<Vendor>/<slug>.<face>.svg,
// the spots marked on them (bays, power supplies, fans, ports, slots: ADR 0047)
// to internal/devicetypes/library/layouts/<Vendor>/<slug>.<face>.json, PNG to internal/devicetypes/library/elevation-images/<Vendor>/<slug>.<face>.png,
// 1000 pixels wide (the library refuses SVG: served from the application's
// origin it could carry script). --png needs the web/ dependencies (it uses
// the Playwright Chromium of the e2e suite); run it in the Playwright image
// the e2e suite pins, so that the bytes are reproducible:
//
//   docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/playwright:v1.63.0-noble \
//     bash -c "apt-get update -qq && apt-get install -y -qq pngquant >/dev/null && \
//              npm --prefix web ci && node scripts/devicetype-faces/generate.mjs --png"
//
// The PNG are then reduced to a 256-colour palette by pngquant (2.x, --speed 1,
// deterministic): the catalogue holds a few hundred faces embedded in the
// binary. Without pngquant the PNG stay as Chromium wrote them, and the
// generator says so.
//
// The drawings are original works of the trica project from vendors' public
// facts (see faces.mjs), dedicated to the public domain under CC0-1.0.

import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { withMarks } from "./draw.mjs";
import { catalogueOutputs } from "./catalogue.mjs";
import * as f from "./faces.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const lib = join(root, "internal", "devicetypes", "library");

// Every face drawn: manufacturer and slug exactly as in the model file.
export const outputs = [
  ["HPE", "hpe-proliant-dl360-gen9-8sff", "front", f.dl360Gen9Front8SFF],
  ["HPE", "hpe-proliant-dl360-gen9-8sff", "rear", f.dl360Gen9Rear],
  ["HPE", "hpe-proliant-dl380-gen9-8sff", "front", f.dl380Gen9Front8SFF],
  ["HPE", "hpe-proliant-dl380-gen9-8sff", "rear", f.dl380Gen9Rear],
  ["HPE", "hpe-proliant-dl380-gen9-24sff", "front", f.dl380Gen9Front24SFF],
  ["HPE", "hpe-proliant-dl380-gen9-24sff", "rear", f.dl380Gen9Rear],
  ["HPE", "hpe-proliant-dl380-gen10-8sff", "front", f.dl380Gen10Front8SFF],
  ["HPE", "hpe-proliant-dl380-gen10-8sff", "rear", f.dl380Gen10Rear],
  ["HPE", "hpe-proliant-dl380-gen10-8sff-nvme", "front", f.dl380Gen10Front8SFFNVMe],
  ["HPE", "hpe-proliant-dl380-gen10-8sff-nvme", "rear", f.dl380Gen10Rear],
  ["HPE", "hpe-proliant-dl380-gen10-24sff", "front", f.dl380Gen10Front24SFF],
  ["HPE", "hpe-proliant-dl380-gen10-24sff", "rear", f.dl380Gen10Rear],
  ["HPE", "hpe-proliant-dl380-gen10-24sff-nvme", "front", f.dl380Gen10Front24SFFNVMe],
  ["HPE", "hpe-proliant-dl380-gen10-24sff-nvme", "rear", f.dl380Gen10Rear],
  ["HPE", "hpe-proliant-dl380-gen10-12lff", "rear", f.dl380Gen10Rear],
  ["HPE", "hpe-proliant-dl380-gen10-8lff", "rear", f.dl380Gen10Rear],
  ["HPE", "hpe-proliant-dl180-gen9-8sff", "front", f.dl180Gen9Front8SFF],
  ["HPE", "hpe-proliant-dl180-gen9-8sff", "rear", f.dl180Gen9Rear],
  ["HPE", "hpe-proliant-dl180-gen9-8lff", "front", f.dl180Gen9Front8LFF],
  ["HPE", "hpe-proliant-dl180-gen9-8lff", "rear", f.dl180Gen9Rear],
  ["HPE", "hpe-proliant-dl180-gen9-12lff", "front", f.dl180Gen9Front12LFF],
  ["HPE", "hpe-proliant-dl180-gen9-12lff", "rear", f.dl180Gen9Rear],
  ["Dell", "dell-poweredge-r330-8sff", "front", () => f.r330Front("8sff")],
  ["Dell", "dell-poweredge-r330-8sff", "rear", f.r330Rear],
  ["Dell", "dell-poweredge-r330-4lff-hot-plug", "front", () => f.r330Front("4lff-hot-plug")],
  ["Dell", "dell-poweredge-r330-4lff-hot-plug", "rear", f.r330Rear],
  ["Dell", "dell-poweredge-r330-4lff-cabled", "front", () => f.r330Front("4lff-cabled")],
  ["Dell", "dell-poweredge-r330-4lff-cabled", "rear", f.r330Rear],
  ["Arista", "arista-dcs-7280se-64-f", "front", () => f.aristaPortSide("DCS-7280SE-64")],
  ["Arista", "arista-dcs-7280se-64-f", "rear", () => f.aristaPowerSide("DCS-7280SE-64-F", "front-to-rear")],
  ["Arista", "arista-dcs-7280se-64-r", "front", () => f.aristaPortSide("DCS-7280SE-64")],
  ["Arista", "arista-dcs-7280se-64-r", "rear", () => f.aristaPowerSide("DCS-7280SE-64-R", "rear-to-front")],
  ["Arista", "arista-dcs-7050sx-64-f", "rear", () => f.aristaPowerSide("DCS-7050SX-64-F", "front-to-rear")],
  ["Arista", "arista-dcs-7050sx-64-r", "rear", () => f.aristaPowerSide("DCS-7050SX-64-R", "rear-to-front")],
  ["MikroTik", "mikrotik-crs354-48g-4s-plus-2q-plus-rm", "front", f.crs354Front],
  ["MikroTik", "mikrotik-crs354-48g-4s-plus-2q-plus-rm", "rear", f.crs354Rear],
  // The catalogue of common models (catalogue.mjs).
  ...catalogueOutputs,
];

const LIBRARY_WIDTH_PX = 1000;

// The layout of a face: its size in millimetres (the SVG viewBox) and its
// spots, one per line so that a change reads as a diff.
function layoutJSON(svg, spots) {
  const [, w, h] = /viewBox="0 0 ([\d.]+) ([\d.]+)"/.exec(svg);
  const lines = spots.map((s) => "    " + JSON.stringify(s));
  return `{\n  "width_mm": ${w},\n  "height_mm": ${h},\n  "spots": [\n${lines.join(",\n")}\n  ]\n}\n`;
}

// quantize reduces each PNG to a palette with pngquant, in place, when it
// is installed; a file pngquant cannot make smaller is left as it is.
function quantize(files) {
  try {
    execFileSync("pngquant", ["--version"], { stdio: "ignore" });
  } catch {
    console.warn("pngquant not found: the PNG are left as Chromium wrote them (see the header of this file)");
    return;
  }
  let before = 0;
  let after = 0;
  for (const f of files) {
    before += readFileSync(f).length;
    try {
      execFileSync("pngquant", ["--force", "--skip-if-larger", "--strip", "--speed", "1", "--quality", "70-95", "--output", f, f], { stdio: "ignore" });
    } catch {
      // exit 98/99: no smaller file at that quality; keep the original.
    }
    after += readFileSync(f).length;
  }
  console.log(`pngquant: ${files.length} PNG, ${before} B -> ${after} B`);
}

async function main() {
  const inspect = process.argv.indexOf("--inspect");
  const inspectDir = inspect >= 0 ? process.argv[inspect + 1] : undefined;
  const png = process.argv.includes("--png") || inspectDir !== undefined;
  const WIDTH_PX = inspectDir ? 3000 : LIBRARY_WIDTH_PX;
  const written = [];
  for (const [vendor, slug, face, draw] of outputs) {
    const { svg, spots } = withMarks(draw);
    const file = join(lib, "drawings", vendor, `${slug}.${face}.svg`);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, svg);
    // A face with nothing to place (the LFF fronts, whose bay numbers the
    // vendor's text does not give) has no layout.
    if (spots.length > 0) {
      const layout = join(lib, "layouts", vendor, `${slug}.${face}.json`);
      mkdirSync(dirname(layout), { recursive: true });
      writeFileSync(layout, layoutJSON(svg, spots));
    }
    written.push({ vendor, slug, face, file, svg });
  }
  console.log(`${written.length} SVG and layouts written under ${join(lib, "drawings")} and ${join(lib, "layouts")}`);
  if (!png) return;

  const require = createRequire(join(root, "web", "package.json"));
  const { chromium } = require("@playwright/test");
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage({ deviceScaleFactor: 1 });
    for (const w of written) {
      const [, vw, vh] = /viewBox="0 0 ([\d.]+) ([\d.]+)"/.exec(w.svg);
      const height = Math.round((WIDTH_PX * Number(vh)) / Number(vw));
      await page.setViewportSize({ width: WIDTH_PX, height });
      const sized = w.svg.replace(/width="[\d.]+mm" height="[\d.]+mm"/, `width="${WIDTH_PX}" height="${height}"`);
      await page.setContent(
        `<!doctype html><html><head><style>html,body{margin:0;background:transparent}svg{display:block}</style></head><body>${sized}</body></html>`,
      );
      const out = inspectDir
        ? join(inspectDir, `${w.slug}.${w.face}.png`)
        : join(lib, "elevation-images", w.vendor, `${w.slug}.${w.face}.png`);
      mkdirSync(dirname(out), { recursive: true });
      const buf = await page.screenshot({ clip: { x: 0, y: 0, width: WIDTH_PX, height }, omitBackground: true });
      writeFileSync(out, buf);
      console.log(`${out}  ${WIDTH_PX}x${height}  ${buf.length} B`);
    }
  } finally {
    await browser.close();
  }
  if (inspectDir) return;
  quantize(written.map((w) => join(lib, "elevation-images", w.vendor, `${w.slug}.${w.face}.png`)));
  // Fail loudly if a file came out empty.
  for (const w of written) {
    const out = join(lib, "elevation-images", w.vendor, `${w.slug}.${w.face}.png`);
    if (readFileSync(out).length < 1000) throw new Error(`${out}: suspiciously small`);
  }
}

await main();
