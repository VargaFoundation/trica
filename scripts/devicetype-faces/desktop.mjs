// Faces of devices that are not rack-mounted (desktop towers), drawn at the
// device's own size. Counts, grouping and order of the parts come from the
// vendor's public text; positions the text does not give are schematic, the
// same for the model, and said so. No vendor picture or stencil was traced.
//
// The drawings are dedicated to the public domain under CC0-1.0 (see
// internal/devicetypes/library/README.md); the code is Apache-2.0.

import { button, c14, circle, deskDoc, hotplugCarrier, led, mark, pinhole, rect, recess, rj45, text, usb } from "./draw.mjs";

// Synology DS1821+: 166 x 343 x 243 mm (H x W x D), eight 3.5-inch / 2.5-inch
// SATA bays in a row, a power button and status LEDs, two 120 mm fans, four
// 1 GbE RJ-45, one PCIe 3.0 x8 slot (E10G21-F2 / E25G21-F2 cards), two eSATA
// (DX517 expansion units), two USB 3.2, an internal 250 W supply, a reset
// button and a K-lock slot; two M.2 2280 NVMe slots on the underside. The
// numbers are from the vendor's DS1821+ specification as the author recalled
// it: the page was not fetched (see library/Synology/README or the model
// file). The bay and port positions are schematic.
const W = 343;
const H = 166;
const BODY = "#2a2c30";

/** A column of the front: the power button and the status LEDs, from the top. */
function frontControls(x) {
  let s = button(x + 9, 20, 4.2, "green");
  s += text(x + 9, 31, "POWER", { size: 1.9, anchor: "middle", fill: "#aeb3b9" });
  const rows = [
    ["STATUS", "green"],
    ["ALERT", "off"],
    ["LAN 1", "green"],
    ["LAN 2", "off"],
    ["LAN 3", "off"],
    ["LAN 4", "off"],
  ];
  rows.forEach(([label, colour], i) => {
    const y = 46 + i * 15.5;
    s += led(x + 4, y, colour, 1.1);
    s += text(x + 8.4, y + 0.7, label, { size: 1.9, fill: "#aeb3b9" });
  });
  return s;
}

export function ds1821Front() {
  let s = rect(0.4, 0.4, W - 0.8, H - 0.8, { fill: BODY, rx: 4, stroke: "#4a4e54", "stroke-width": 0.8 });
  s += rect(2, 2, W - 4, H - 4, { fill: "none", rx: 3, stroke: "#1b1c1f", "stroke-width": 0.6 });
  s += frontControls(8);
  // Eight vertical trays in a row, drive 1 on the left.
  const x0 = 38;
  const w = 35;
  const gap = 1.8;
  const y = 14;
  const h = 140;
  s += rect(x0 - 1.4, y - 1.4, 8 * w + 7 * gap + 2.8, h + 2.8, { fill: "#16171a", rx: 1.4, stroke: "#55595f", "stroke-width": 0.35 });
  for (let i = 0; i < 8; i++) {
    const x = x0 + i * (w + gap);
    s += hotplugCarrier(x, y, w, h, { horizontal: false, num: i + 1 }) + mark("bay", String(i + 1), x, y, w, h);
  }
  return deskDoc(W, H, "Synology DS1821+, front (schematic)", s);
}

/** A 120 mm fan seen through its grille. */
function fan120(x, y, size) {
  const c = size / 2;
  let s = rect(x, y, size, size, { fill: "#1d1f22", rx: 5, stroke: "#4a4e54", "stroke-width": 0.5 });
  for (const rad of [c - 6, c - 16, c - 26, c - 36, c - 46]) {
    if (rad > 8) s += circle(x + c, y + c, rad, { fill: "none", stroke: "#5b6067", "stroke-width": 0.9 });
  }
  s += rect(x + c - 0.5, y + 6, 1, size - 12, { fill: "#5b6067" }) + rect(x + 6, y + c - 0.5, size - 12, 1, { fill: "#5b6067" });
  s += circle(x + c, y + c, 13, { fill: "#33363b", stroke: "#5b6067", "stroke-width": 0.6 });
  s += circle(x + c, y + c, 5, { fill: "#4a4f55" });
  return s;
}

export function ds1821Rear() {
  let s = rect(0.4, 0.4, W - 0.8, H - 0.8, { fill: BODY, rx: 4, stroke: "#4a4e54", "stroke-width": 0.8 });
  s += rect(2, 2, W - 4, H - 4, { fill: "none", rx: 3, stroke: "#1b1c1f", "stroke-width": 0.6 });
  // Two 120 mm fans, 1 on the left.
  const fanSize = 124;
  const fy = (H - fanSize) / 2;
  s += fan120(6, fy, fanSize) + mark("fan", "1", 6, fy, fanSize, fanSize);
  s += fan120(6 + fanSize + 4, fy, fanSize) + mark("fan", "2", 6 + fanSize + 4, fy, fanSize, fanSize);
  // The I/O column.
  const x = 268;
  // The PCIe slot: an opening with two port positions, LAN 5 and LAN 6. The
  // card is an option (E10G21-F2: 2 x 10GbE SFP+, E25G21-F2: 2 x 25GbE SFP28)
  // and DSM names its ports eth4 and eth5, LAN 5 and LAN 6.
  s += recess(x, 8, 70, 44, 1);
  for (let i = 0; i < 2; i++) {
    const px = x + 3 + i * 33;
    s += rect(px, 12, 31, 24, { fill: "#1b1d20", rx: 0.8, stroke: "#5b6067", "stroke-width": 0.3, "stroke-dasharray": "1 0.8" });
    s += text(px + 15.5, 27, String(i + 5), { size: 2.2, anchor: "middle", fill: "#8a9098" });
    s += mark("port", `LAN ${i + 5}`, px, 12, 31, 24);
  }
  s += text(x + 35, 46, "PCIe slot (option card)", { size: 1.9, anchor: "middle", fill: "#aeb3b9" });
  // Four RJ-45, LAN 1 to 4.
  for (let i = 0; i < 4; i++) {
    const px = x + 2 + i * 16.8;
    s += rj45(px, 62, { up: false }) + mark("port", `LAN ${i + 1}`, px, 62, 11.6, 9.6);
    s += text(px + 5.8, 78, String(i + 1), { size: 2, anchor: "middle", fill: "#aeb3b9" });
  }
  // Two eSATA (expansion units) and two USB 3.2.
  for (let i = 0; i < 2; i++) {
    const px = x + 2 + i * 16.8;
    s += rect(px - 0.4, 89.6, 12.6, 6, { fill: "#9ba1a7", rx: 0.5 }) + rect(px, 90, 11.8, 5.2, { fill: "#151719", rx: 0.4 }) + rect(px + 1.4, 91.4, 9, 1, { fill: "#b8bcc1" });
  }
  s += text(x + 12, 101, "eSATA", { size: 1.9, anchor: "middle", fill: "#aeb3b9" });
  s += usb(x + 37, 90, { usb3: true }) + usb(x + 54, 90, { usb3: true });
  s += text(x + 55, 101, "USB 3.2", { size: 1.9, anchor: "middle", fill: "#aeb3b9" });
  // Reset button and K-lock slot.
  s += pinhole(x + 6, 117) + text(x + 11, 117.7, "RESET", { size: 1.9, fill: "#aeb3b9" });
  s += rect(x + 3, 131, 7, 2.6, { fill: "#101113", rx: 1.3, stroke: "#7c8188", "stroke-width": 0.3 }) + text(x + 12.4, 133, "K-lock", { size: 1.9, fill: "#aeb3b9" });
  // The internal 250 W supply: only its inlet shows.
  s += rect(x + 28, 111, 42, 46, { fill: "#33363b", rx: 1.2, stroke: "#55595f", "stroke-width": 0.4 });
  s += c14(x + 42, 124, { w: 13, h: 10 });
  s += text(x + 49, 152, "250 W", { size: 1.9, anchor: "middle", fill: "#aeb3b9" });
  s += mark("psu", "1", x + 28, 111, 42, 46);
  return deskDoc(W, H, "Synology DS1821+, rear (schematic)", s);
}
