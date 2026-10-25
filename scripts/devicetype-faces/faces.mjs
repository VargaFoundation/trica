// The faces drawn by the trica project, one function per face. Each says
// where its facts come from: counts, grouping and order are taken from the
// vendor's public text; positions that the text does not give are schematic
// and said so. No vendor picture or stencil was traced or used.
//
// The drawings this code produces are dedicated to the public domain under
// CC0-1.0 (see internal/devicetypes/library/README.md); the code itself is
// Apache-2.0, like the rest of the repository.

import {
  blank,
  button,
  c14,
  chassis,
  circle,
  driveCage,
  dsub,
  fanModule,
  hotplugCarrier,
  led,
  lffCarrier,
  mark,
  pcie,
  pinhole,
  psu,
  pullTab,
  qsfp,
  rearChassis,
  recess,
  rect,
  rj45,
  sfp,
  sffCarrier,
  svgDoc,
  text,
  usb,
  vent,
} from "./draw.mjs";

// ───────────────────────── HPE ProLiant Gen9 ─────────────────────────

// Front controls of an HPE Gen9 server, top to bottom: power button and
// system power LED, health LED, NIC status LED, UID button and LED.
// Source: QuickSpecs front-view legends (DL360, DL380, DL180 Gen9).
function hpeControls(x, y, step) {
  return button(x, y, 2, "green") + led(x, y + step, "green") + led(x, y + 2 * step, "green") + button(x, y + 3 * step, 1.6, "blue");
}

// DL360 Gen9 8SFF (CTO 755258-B21). QuickSpecs c04346229: "8 SFF with
// optional Universal Media bay", drive bays 1-8, power button, health LED,
// NIC LED, USB 3.0 connector, UID button, serial label pull tab; 4.32 x
// 43.47 cm (H x W). Bays are numbered by column, odd on top.
export function dl360Gen9Front8SFF() {
  const c = chassis(1, 434.7, { earText: "ProLiant\nDL360\nGen9" });
  let s = c.svg;
  const x0 = c.x0 + 3;
  const cw = 70.2;
  const ch = 19.4;
  s += driveCage(x0 + 1.4, 1.5, 4 * (cw + 1.2) + 0.8, 2 * ch + 2.6);
  for (let col = 0; col < 4; col++) {
    for (let row = 0; row < 2; row++) {
      const [x, y, n] = [x0 + 2.4 + col * (cw + 1.2), 2.4 + row * (ch + 0.8), col * 2 + row + 1];
      // iLO reports these bays as box 1 (1I:1:1 ... 2I:1:8).
      s += hotplugCarrier(x, y, cw, ch, { num: n }) + mark("bay", `1:${n}`, x, y, cw, ch);
    }
  }
  // Universal Media Bay: a blank on the CTO chassis (VGA, USB 2.0 and
  // optical, or 2 SFF, are options).
  const umbX = x0 + 2.4 + 4 * (cw + 1.2) + 1.2;
  s += blank(umbX, 2.4, 64, 39.6);
  s += text(umbX + 32, 41, "media bay", { size: 1.8, anchor: "middle", fill: "#4a4e54" });
  // controls, USB 3.0, pull tab
  const px = umbX + 64 + 6;
  s += hpeControls(px, 7, 8.2);
  s += usb(px + 5.5, 4, { usb3: true, vertical: true });
  s += pullTab(px + 12, 3.2, 5, 37.6);
  return svgDoc(1, "HPE ProLiant DL360 Gen9 8SFF, front (schematic)", s);
}

// The FlexibleLOM bay of an HPE ProLiant: an option card whose ports the
// chassis does not fix (4 x 1GbE 331FLR, 2 x 10GbE 530FLR-SFP+, 2 x 40GbE
// 544+FLR-QSFP...). The bay is drawn as one opening with four port positions,
// flexlom1-4 from left to right; a two-port card fills the first two. Linux
// names its ports after the onboard index the firmware gives the card (eno49
// on Gen9, eno5 on Gen10; eno49d1 for the second port of one function).
function flexLomBay(x, y, caption = { dx: 0, dy: 15.8 }) {
  let s = recess(x - 0.6, y - 1.2, 4 * 12.6 + 0.2, 12, 0.8);
  for (let i = 0; i < 4; i++) {
    const px = x + i * 12.6;
    s += rect(px + 0.4, y, 11.6, 9.6, { fill: "#1b1d20", rx: 0.6, stroke: "#5b6067", "stroke-width": 0.3, "stroke-dasharray": "1 0.8" });
    s += text(px + 6.2, y + 6.4, String(i + 1), { size: 1.6, anchor: "middle", fill: "#8a9098" });
    s += mark("port", `flexlom${i + 1}`, px + 0.4, y, 11.6, 9.6);
  }
  s += text(x + caption.dx, y + caption.dy, "FlexibleLOM (option card)", { size: 1.7 });
  return s;
}

// DL360 Gen9 rear. QuickSpecs c04346229 rear legend: PCIe 3.0 slots 1-3
// (slot 1 full height, 2 and 3 low profile), two Flexible Slot power
// supplies, video connector, embedded 4x1GbE, dedicated iLO, optional
// serial port, two USB 3.0, UID LED, optional FlexibleLOM bay. The left to
// right order follows the CC0 picture in elevation-images/HPE/.
export function dl360Gen9Rear() {
  const c = rearChassis(1, 434.7);
  let s = c.svg;
  const x = c.x0 + 2;
  // PCIe: 1 full height, 2 and 3 low profile, above the I/O row.
  s += pcie(x, 2.2, 108, 15.6, { label: "1" }) + mark("slot", 1, x, 2.2, 108, 15.6);
  s += pcie(x + 110, 2.2, 96, 15.6, { label: "2" }) + mark("slot", 2, x + 110, 2.2, 96, 15.6);
  s += pcie(x + 208, 2.2, 96, 15.6, { label: "3" }) + mark("slot", 3, x + 208, 2.2, 96, 15.6);
  // I/O row
  const y = 21.5;
  s += flexLomBay(x + 0.6, y, { dx: 0.4, dy: 15.8 });
  s += led(x + 55, y + 5, "blue");
  s += text(x + 55, y + 11.2, "UID", { size: 1.6, anchor: "middle" });
  s += usb(x + 59, y - 1.4, { usb3: true });
  s += usb(x + 59, y + 5.6, { usb3: true });
  s += dsub(x + 74, y - 0.6, "db9");
  s += rj45(x + 108, y, { up: false }) + mark("port", "bmc", x + 108, y, 11.6, 9.6);
  s += text(x + 113.8, y + 15.8, "iLO", { size: 1.7, anchor: "middle" });
  for (let i = 0; i < 4; i++) s += rj45(x + 123 + i * 12.6, y, { up: false }) + mark("port", `nic${i + 1}`, x + 123 + i * 12.6, y, 11.6, 9.6);
  s += text(x + 123, y + 15.8, "1", { size: 1.7 }) + text(x + 171, y + 15.8, "4", { size: 1.7, anchor: "end" });
  s += dsub(x + 176, y - 0.6, "vga");
  s += vent(x + 210, y - 1.4, 94, 18);
  // two power supplies on the right
  const pw = 60;
  s += psu(c.x1 - 2 - 2 * pw - 1.5, 2.2, pw, 39.6) + mark("psu", 1, c.x1 - 2 - 2 * pw - 1.5, 2.2, pw, 39.6);
  s += psu(c.x1 - 2 - pw, 2.2, pw, 39.6) + mark("psu", 2, c.x1 - 2 - pw, 2.2, pw, 39.6);
  return svgDoc(1, "HPE ProLiant DL360 Gen9, rear (schematic)", s);
}

// A 2U HPE drive box of 8 standing SFF carriers (Box 1, 2 or 3).
// iLO names a bay by its box: Port=1I:Box=3:Bay=5, or 1I:3:5.
// nvme: the 8 NVMe cage of the DL380 Gen10 (826689-B21), same bays.
function hpeSffBox(x, y, w, h, label, box, { nvme = false } = {}) {
  let s = driveCage(x - 0.6, y - 0.6, w + 1.2, h + 1.2);
  const pitch = w / 8;
  for (let i = 0; i < 8; i++) {
    s += hotplugCarrier(x + i * pitch + 0.3, y + 3.6, pitch - 0.6, h - 3.9, { horizontal: false, num: i + 1, nvme });
    s += mark("bay", `${box}:${i + 1}`, x + i * pitch + 0.3, y + 3.6, pitch - 0.6, h - 3.9);
  }
  s += text(x + w, y - 1.4, label, { size: 1.8, anchor: "end", fill: "#3b3f45" });
  return s;
}

// A 2U box position closed by a blank (box 1 and 2 of an 8SFF chassis).
function hpeBlankBox(x, y, w, h, label, note) {
  let s = blank(x, y, w, h);
  s += text(x + w, y - 1.2, label, { size: 1.8, anchor: "end", fill: "#3b3f45" });
  if (note) s += text(x + w / 2, y + h - 2, note, { size: 1.8, anchor: "middle", fill: "#4a4e54" });
  return s;
}

// The right-hand control column of a 2U HPE Gen9 SFF chassis.
function hpe2uControls(x, { usb3 = true } = {}) {
  let s = hpeControls(x + 4, 10, 8.5);
  s += usb(x + 1.8, 50, { usb3, vertical: true });
  s += pullTab(x + 10, 8, 5, 72);
  return s;
}

// DL380 Gen9 front. QuickSpecs c04346247: 8.73 x 44.55 cm; three drive
// boxes (Bay 1, 2, 3), 8 SFF each; the 8SFF chassis has its drives in one
// box and the Universal Media Bay (optional) only in Bay 1; the 24SFF
// chassis (767032-B21) is full. Power button, health LED, NIC status, UID
// button, USB 3.0, serial label pull tag.
function dl380Gen9Front(full) {
  const c = chassis(2, 445.5, { earText: "ProLiant\nDL380\nGen9" });
  let s = c.svg;
  const bw = 125;
  const y = 6;
  const h = 79.5;
  const x0 = c.x0 + 3;
  const xs = [x0, x0 + bw + 3, x0 + 2 * (bw + 3)];
  s += full ? hpeSffBox(xs[0], y, bw, h, "Box 1", 1) : hpeBlankBox(xs[0], y, bw, h, "Box 1", "media bay");
  s += full ? hpeSffBox(xs[1], y, bw, h, "Box 2", 2) : hpeBlankBox(xs[1], y, bw, h, "Box 2");
  s += hpeSffBox(xs[2], y, bw, h, "Box 3", 3);
  s += hpe2uControls(xs[2] + bw + 6);
  return svgDoc(2, `HPE ProLiant DL380 Gen9 ${full ? "24SFF" : "8SFF"}, front (schematic)`, s);
}
export const dl380Gen9Front8SFF = () => dl380Gen9Front(false);
export const dl380Gen9Front24SFF = () => dl380Gen9Front(true);

// DL380 Gen9 rear. QuickSpecs c04346247 rear legend: PCIe slots 1-3 (primary
// riser, top to bottom) and 4-6 (second riser), optional serial port,
// optional rear 2 SFF, two Flexible Slot power supplies, VGA, embedded
// 4x1GbE, dedicated iLO, two USB 3.0, UID LED, optional FlexibleLOM.
// Positions are schematic.
export function dl380Gen9Rear() {
  const c = rearChassis(2, 445.5);
  let s = c.svg;
  const x = c.x0 + 2;
  for (let i = 0; i < 3; i++) s += pcie(x, 2.4 + i * 14.6, 118, 13.6, { label: String(i + 1) }) + mark("slot", i + 1, x, 2.4 + i * 14.6, 118, 13.6);
  for (let i = 0; i < 3; i++) s += pcie(x + 121, 2.4 + i * 14.6, 118, 13.6, { label: String(i + 4) }) + mark("slot", i + 4, x + 121, 2.4 + i * 14.6, 118, 13.6);
  s += blank(x + 242, 2.4, 78, 42);
  s += text(x + 281, 42, "rear 2 SFF (option)", { size: 1.8, anchor: "middle", fill: "#4a4e54" });
  // I/O row along the bottom
  const y = 52;
  s += flexLomBay(x + 0.6, y, { dx: 0.4, dy: 15.8 });
  s += dsub(x + 55, y - 0.6, "db9");
  s += dsub(x + 89, y - 0.6, "vga");
  for (let i = 0; i < 4; i++) s += rj45(x + 124 + i * 12.6, y, { up: false }) + mark("port", `nic${i + 1}`, x + 124 + i * 12.6, y, 11.6, 9.6);
  s += text(x + 124, y + 15.8, "1", { size: 1.7 }) + text(x + 172, y + 15.8, "4", { size: 1.7, anchor: "end" });
  s += rj45(x + 178, y, { up: false }) + mark("port", "bmc", x + 178, y, 11.6, 9.6);
  s += text(x + 183.8, y + 15.8, "iLO", { size: 1.7, anchor: "middle" });
  s += usb(x + 194, y - 1.4, { usb3: true });
  s += usb(x + 194, y + 5.6, { usb3: true });
  s += led(x + 211, y + 5, "blue");
  s += text(x + 211, y + 11.2, "UID", { size: 1.6, anchor: "middle" });
  s += vent(x + 216, y - 3, 104, 32);
  const pw = 54;
  s += psu(c.x1 - 2 - 2 * pw - 1.5, 46, pw, 40, { label: "1" }) + mark("psu", 1, c.x1 - 2 - 2 * pw - 1.5, 46, pw, 40);
  s += psu(c.x1 - 2 - pw, 46, pw, 40, { label: "2" }) + mark("psu", 2, c.x1 - 2 - pw, 46, pw, 40);
  s += vent(c.x1 - 2 - 2 * pw - 1.5, 2.4, 2 * pw + 1.5, 41.5);
  return svgDoc(2, "HPE ProLiant DL380 Gen9, rear (schematic)", s);
}

// ───────────────────────── HPE ProLiant DL380 Gen10 ─────────────────────────
//
// QuickSpecs a00008180enw. Chassis 8.73 x 44.54 cm (H x W). Front view
// legend (SFF): Box 1, Box 2, Box 3; the Universal Media Bay (826708-B21)
// "can only be populated in Box 1"; the 8SFF CTO ships its "Primary 8SFF cage
// in Box 3" (873763-B21 removes it); the 8 NVMe Express Bay kit (826689-B21)
// "can only be populated in Box 1, Box 2 and Box 3", and the 8 NVMe primary
// riser (867807-B21) "can be connected to an 8SFF NVMe drive cage in box 3".
// Controls: power button and system power LED, health LED, NIC status, UID
// button, iLO front service port, USB 3.0, serial label pull tag. Box 1 is the
// left one, Box 3 the one beside the controls: the order of the CC0 picture in
// elevation-images/HPE/ (UMB in Box 1, drives beside the controls).
// iLO names a bay by box and bay: 1I:3:5, Box=3:Bay=5, or 3:5 for an NVMe
// drive; the spots are named <box>:<bay>.

// The right-hand control column of the DL380 Gen10: the Gen9 controls plus
// the iLO front service port above the USB 3.0 port.
function dl380Gen10Controls(x) {
  let s = hpeControls(x + 4, 10, 8.5);
  s += rect(x + 1.6, 43, 5, 3.2, { fill: "#16181a", rx: 0.5, stroke: "#9aa0a6", "stroke-width": 0.4 });
  s += text(x + 4.1, 49.4, "iLO", { size: 1.5, anchor: "middle", fill: "#3b3f45" });
  s += usb(x + 1.8, 53, { usb3: true, vertical: true });
  s += pullTab(x + 10, 8, 5, 72);
  return s;
}

// boxes: what each of Box 1, 2 and 3 holds, "sff", "nvme" or "blank".
function dl380Gen10Front(title, boxes) {
  const c = chassis(2, 445.4, { earText: "ProLiant\nDL380\nGen10" });
  let s = c.svg;
  const bw = 125;
  const y = 6;
  const h = 79.5;
  const x0 = c.x0 + 3;
  boxes.forEach((kind, i) => {
    const x = x0 + i * (bw + 3);
    const box = i + 1;
    if (kind === "blank") s += hpeBlankBox(x, y, bw, h, `Box ${box}`, box === 1 ? "media bay (option)" : undefined);
    else s += hpeSffBox(x, y, bw, h, kind === "nvme" ? `Box ${box} \u00b7 8 NVMe` : `Box ${box}`, box, { nvme: kind === "nvme" });
  });
  s += dl380Gen10Controls(x0 + 3 * (bw + 3) + 3);
  return svgDoc(2, `HPE ProLiant DL380 Gen10 ${title}, front (schematic)`, s);
}
// 8SFF CTO (868703-B21 and the SKUs of its model file): drives in Box 3.
export const dl380Gen10Front8SFF = () => dl380Gen10Front("8SFF", ["blank", "blank", "sff"]);
// The same chassis with the 8 NVMe cage in Box 3 instead (826689-B21).
export const dl380Gen10Front8SFFNVMe = () => dl380Gen10Front("8SFF with 8 NVMe in Box 3", ["blank", "blank", "nvme"]);
// 24SFF CTO (868704-B21): the three boxes.
export const dl380Gen10Front24SFF = () => dl380Gen10Front("24SFF", ["sff", "sff", "sff"]);
// The same chassis with the 8 NVMe cage in Box 3 instead (826689-B21).
export const dl380Gen10Front24SFFNVMe = () => dl380Gen10Front("24SFF with 8 NVMe in Box 3", ["sff", "sff", "nvme"]);

// DL380 Gen10 rear. QuickSpecs a00008180enw rear view legend: primary riser
// (slots 1-3, top to bottom), secondary riser (slots 4-6, top to bottom),
// optional serial port, tertiary riser (slots 7-8), "HPE Flexible Slot Power
// Supply bay 1" and "bay 2", VGA, embedded 4 x 1GbE, dedicated iLO port, two
// USB 3.0, unit ID LED, FlexibleLOM ports. The legend numbers them around the
// panel: the risers along the top, then bay 1, bay 2, VGA, NIC, iLO, USB, UID,
// FlexibleLOM from right to left; the CC0 picture in elevation-images/HPE/ has
// the same order, power supply bay 1 on the far right. Six fans, hot-plug,
// are inside the chassis: not on a face. Positions are schematic.
export function dl380Gen10Rear() {
  const c = rearChassis(2, 445.4);
  let s = c.svg;
  const x = c.x0 + 2;
  const pw = 72;
  const psu1 = c.x1 - 2 - pw;
  const psu2 = psu1 - 1.5 - pw;
  for (let i = 0; i < 3; i++) s += pcie(x, 2.4 + i * 14.6, 118, 13.6, { label: String(i + 1) }) + mark("slot", i + 1, x, 2.4 + i * 14.6, 118, 13.6);
  for (let i = 0; i < 3; i++) s += pcie(x + 121, 2.4 + i * 14.6, 100, 13.6, { label: String(i + 4) }) + mark("slot", i + 4, x + 121, 2.4 + i * 14.6, 100, 13.6);
  // Between the secondary riser and the power supplies: the optional serial port.
  const mid = x + 224;
  s += vent(mid, 2.4, psu2 - mid - 2, 42);
  s += dsub(mid + (psu2 - mid - 2 - 31) / 2, 6, "db9");
  s += text(mid + (psu2 - mid - 2) / 2, 23.5, "serial (option)", { size: 1.7, anchor: "middle", fill: "#2b2e32" });
  // Tertiary riser above the power supplies.
  for (let i = 0; i < 2; i++) s += pcie(psu2, 2.4 + i * 14.6, c.x1 - 2 - psu2, 13.6, { label: String(i + 7) }) + mark("slot", i + 7, psu2, 2.4 + i * 14.6, c.x1 - 2 - psu2, 13.6);
  s += vent(psu2, 32, c.x1 - 2 - psu2, 11.5);
  // I/O row along the bottom, left to right.
  const y = 54;
  s += flexLomBay(x, y, { dx: 0, dy: 17.6 });
  s += usb(x + 56, y - 1.4, { usb3: true });
  s += usb(x + 56, y + 5.6, { usb3: true });
  s += led(x + 73, y + 5, "blue");
  s += text(x + 73, y + 11.2, "UID", { size: 1.6, anchor: "middle" });
  s += rj45(x + 79, y, { up: false }) + mark("port", "bmc", x + 79, y, 11.6, 9.6);
  s += text(x + 84.8, y + 15.8, "iLO", { size: 1.7, anchor: "middle" });
  for (let i = 0; i < 4; i++) s += rj45(x + 96 + i * 12.6, y, { up: false }) + mark("port", `nic${i + 1}`, x + 96 + i * 12.6, y, 11.6, 9.6);
  s += text(x + 96, y + 15.8, "1", { size: 1.7 }) + text(x + 144, y + 15.8, "4", { size: 1.7, anchor: "end" });
  s += dsub(x + 150, y - 1.4, "vga");
  s += vent(x + 185, y - 6, psu2 - x - 187, 34);
  // Power supply bay 2, then bay 1 on the far right.
  s += psu(psu2, 46, pw, 40.5, { label: "2" }) + mark("psu", 2, psu2, 46, pw, 40.5);
  s += psu(psu1, 46, pw, 40.5, { label: "1" }) + mark("psu", 1, psu1, 46, pw, 40.5);
  return svgDoc(2, "HPE ProLiant DL380 Gen10, rear (schematic)", s);
}

// DL180 Gen9 front. QuickSpecs c04346227: 8.75 x 44.55 cm; 8SFF chassis =
// fixed 8-bay SFF cage (box 3), a bay for a second 8-bay cage (box 2) and a
// media bay (box 1); 8 LFF (+4 LFF field upgrade) or 12 LFF hot-plug
// chassis; power button, health LED, NIC status LED, UID button, USB 2.0
// connector (USB 3.0 on the quick-release ears of the LFF chassis), serial
// label pull tab. LFF bay positions and which row the 8LFF chassis leaves
// blank are schematic (the text does not say).
function dl180Gen9Front(kind) {
  const lff = kind !== "8sff";
  const c = chassis(2, 445.5, { earText: lff ? undefined : "ProLiant\nDL180\nGen9", latch: lff });
  let s = c.svg;
  if (!lff) {
    const bw = 125;
    const x0 = c.x0 + 3;
    s += hpeBlankBox(x0, 6, bw, 79.5, "Box 1", "media bay");
    s += hpeBlankBox(x0 + bw + 3, 6, bw, 79.5, "Box 2");
    s += hpeSffBox(x0 + 2 * (bw + 3), 6, bw, 79.5, "Box 3", 3);
    s += hpe2uControls(x0 + 3 * (bw + 3) + 3, { usb3: false });
    return svgDoc(2, "HPE ProLiant DL180 Gen9 8SFF, front (schematic)", s);
  }
  // LFF: three rows of four 3.5-inch carriers across the body; the
  // controls and the USB 3.0 port on the ears.
  const cw = 108.6;
  const ch = 27.6;
  for (let row = 0; row < 3; row++) {
    for (let col = 0; col < 4; col++) {
      const x = c.x0 + 1.2 + col * (cw + 0.6);
      const y = 1.9 + row * (ch + 0.4);
      s += kind === "8lff" && row === 2 ? blank(x, y, cw, ch) : lffCarrier(x, y, cw, ch);
    }
  }
  const ex = c.ear / 2;
  // left ear: controls; right ear: USB 3.0 and the pull tab
  s += rect(ex - 4, 11, 8, 32, { fill: "#9da2a8", rx: 1 });
  s += hpeControls(ex, 15.5, 7.6);
  s += rect(482.6 - c.ear + 2, 11, c.ear - 4, 30, { fill: "#9da2a8", rx: 1 });
  s += usb(482.6 - ex - 2.3, 15, { usb3: true, vertical: true });
  s += pullTab(482.6 - c.ear + 3, 31, c.ear - 6, 8);
  s += text(482.6 - ex, 84, "DL180", { size: 1.9, anchor: "middle" });
  s += text(482.6 - ex, 86.6, "Gen9", { size: 1.9, anchor: "middle" });
  return svgDoc(2, `HPE ProLiant DL180 Gen9 ${kind === "8lff" ? "8LFF" : "12LFF"}, front (schematic)`, s);
}
export const dl180Gen9Front8SFF = () => dl180Gen9Front("8sff");
export const dl180Gen9Front8LFF = () => dl180Gen9Front("8lff");
export const dl180Gen9Front12LFF = () => dl180Gen9Front("12lff");

// DL180 Gen9 rear. QuickSpecs c04346227 rear legend: PCIe3 slots 1-3
// (primary) and 4-6 (secondary), power supply 1 and 2 (the second with
// the redundant power supply), NIC connectors (embedded 2x1GbE), video,
// UID LED, two USB 3.0, dedicated iLO (optional). Positions schematic.
export function dl180Gen9Rear() {
  const c = rearChassis(2, 445.5);
  let s = c.svg;
  const x = c.x0 + 2;
  for (let i = 0; i < 3; i++) s += pcie(x, 2.4 + i * 14.6, 112, 13.6, { label: String(i + 1) }) + mark("slot", i + 1, x, 2.4 + i * 14.6, 112, 13.6);
  for (let i = 0; i < 3; i++) s += pcie(x + 115, 2.4 + i * 14.6, 112, 13.6, { label: String(i + 4) }) + mark("slot", i + 4, x + 115, 2.4 + i * 14.6, 112, 13.6);
  s += vent(x + 230, 2.4, 90, 42);
  const y = 52;
  s += dsub(x + 1, y - 0.6, "vga");
  s += usb(x + 36, y - 1.4, { usb3: true });
  s += usb(x + 36, y + 5.6, { usb3: true });
  s += led(x + 53, y + 5, "blue");
  s += text(x + 53, y + 11.2, "UID", { size: 1.6, anchor: "middle" });
  s += rj45(x + 60, y, { up: false }) + rj45(x + 72.6, y, { up: false });
  s += mark("port", "nic1", x + 60, y, 11.6, 9.6) + mark("port", "nic2", x + 72.6, y, 11.6, 9.6);
  s += text(x + 60, y + 15.8, "1", { size: 1.7 }) + text(x + 84.2, y + 15.8, "2", { size: 1.7, anchor: "end" });
  s += rj45(x + 92, y, { up: false }) + mark("port", "bmc", x + 92, y, 11.6, 9.6);
  s += text(x + 97.8, y + 15.8, "iLO (option)", { size: 1.7, anchor: "middle" });
  s += vent(x + 112, y - 3, 208, 32);
  const pw = 54;
  s += psu(c.x1 - 2 - 2 * pw - 1.5, 46, pw, 40, { label: "1" }) + mark("psu", 1, c.x1 - 2 - 2 * pw - 1.5, 46, pw, 40);
  s += psu(c.x1 - 2 - pw, 46, pw, 40, { label: "2" }) + mark("psu", 2, c.x1 - 2 - pw, 46, pw, 40);
  s += vent(c.x1 - 2 - 2 * pw - 1.5, 2.4, 2 * pw + 1.5, 41.5);
  return svgDoc(2, "HPE ProLiant DL180 Gen9, rear (schematic)", s);
}

// ───────────────────────── Dell PowerEdge R330 ─────────────────────────

// Owner's manual (dl.dell.com, PowerEdge R330 Owner's Manual): 482.4 mm
// with the ears, 434.0 mm without, 42.4 mm high. Front of every chassis:
// power button with power-on indicator, NMI button, system identification
// button, video connector, information tag (slide-out), optional slim
// optical drive. The 4 x 3.5-inch hot-plug and 8 x 2.5-inch chassis add an
// LCD panel with three menu buttons, a USB management (iDRAC Direct) port and
// a USB 2.0 port; the cabled chassis has two USB 2.0 ports and five
// diagnostic indicators (health, hard drive, electrical, temperature,
// memory) instead of the LCD. Positions are schematic.
function dellLeftEar(c) {
  const ex = c.ear / 2;
  return button(ex, 9.5, 2.3, "green") + pinhole(ex, 18.5) + button(ex, 26.5, 1.7, "blue") + text(ex, 33.6, "ID", { size: 1.6, anchor: "middle" });
}
function dellLcd(x, y) {
  let s = rect(x, y, 36, 11, { fill: "#8d9298", rx: 1 });
  s += rect(x + 1.2, y + 1.6, 22, 7.8, { fill: "url(#lcd)", rx: 0.5 });
  s += circle(x + 26.5, y + 5.5, 1.5, { fill: "#5c6168" }) + circle(x + 30, y + 5.5, 1.5, { fill: "#5c6168" }) + circle(x + 33.5, y + 5.5, 1.5, { fill: "#5c6168" });
  return s;
}
function dellOptical(x, y) {
  return recess(x, y, 128, 11) + rect(x + 1.2, y + 1.2, 125.6, 8.6, { fill: "#b5b9be", rx: 0.4 }) + text(x + 64, y + 7.2, "slim optical (option)", { size: 1.8, anchor: "middle", fill: "#4a4e54" });
}
function dellInfoTag(x, y) {
  return pullTab(x, y, 14, 5) + text(x + 7, y + 3.6, "i", { size: 2, anchor: "middle", fill: "#4a4e54", weight: 700 });
}

export function r330Front(kind) {
  const c = chassis(1, 434.0, { leftSlot: false });
  let s = c.svg + dellLeftEar(c);
  if (kind === "8sff") {
    const cw = 69;
    const ch = 19.2;
    for (let col = 0; col < 4; col++) {
      for (let row = 0; row < 2; row++) {
        // iDRAC numbers the bays from 0 (Disk.Bay.0, Solid State Disk 0:1:0).
        const [x, y, n] = [c.x0 + 4.4 + col * (cw + 1.4), 2.4 + row * (ch + 0.8), col * 2 + row];
        s += sffCarrier(x, y, cw, ch, { num: n }) + mark("bay", n, x, y, cw, ch);
      }
    }
    const x = c.x0 + 4.4 + 4 * (cw + 1.4) + 4;
    s += dellOptical(x, 2.6);
    s += dellLcd(x, 17.5);
    s += usb(x + 40, 21.5) + usb(x + 55, 21.5);
    s += text(x + 46.2, 31, "iDRAC", { size: 1.6, anchor: "middle" });
    s += dsub(x + 70, 17, "vga");
    s += dellInfoTag(x + 100, 33);
    s += text(c.x1 - 2, 41, "PowerEdge R330", { size: 1.9, anchor: "end" });
    return svgDoc(1, "Dell PowerEdge R330 8 x 2.5-inch, front (schematic)", s);
  }
  // 4 x 3.5-inch: one row of carriers along the bottom, the strip above
  // holds the optical drive, the LCD or the indicators, USB and video.
  const hot = kind === "4lff-hot-plug";
  const cw = 103.6;
  for (let i = 0; i < 4; i++) s += lffCarrier(c.x0 + 3 + i * (cw + 1), 15.2, cw, 26.6, { hotplug: hot, num: i }) + mark("bay", i, c.x0 + 3 + i * (cw + 1), 15.2, cw, 26.6);
  const y = 2.4;
  let x = c.x0 + 3;
  if (hot) {
    s += dellLcd(x, y);
    x += 39;
  } else {
    for (const [i, colour] of ["blue", "off", "off", "off", "off"].entries()) s += led(x + 2.5 + i * 4.6, y + 5.5, colour, 0.9);
    x += 26;
  }
  s += usb(x, y + 3.2) + usb(x + 15, y + 3.2);
  if (hot) s += text(x + 6.2, y + 11.6, "iDRAC", { size: 1.4, anchor: "middle" });
  s += dsub(x + 30, y - 0.6, "vga");
  s += dellInfoTag(x + 65, y + 3.2);
  s += dellOptical(c.x1 - 3 - 128, y);
  s += text(c.x1 - 135, y + 9.4, "PowerEdge R330", { size: 1.9, anchor: "end" });
  return svgDoc(1, `Dell PowerEdge R330 4 x 3.5-inch ${hot ? "hot-plug" : "cabled"}, front (schematic)`, s);
}

// R330 rear. Owner's manual back panel: serial connector, vFlash card slot
// and iDRAC port (optional card), two PCIe expansion slots, PSU1 and PSU2
// (up to two 350 W redundant), two USB 3.0, two Ethernet (10/100/1000),
// system identification button and connector, video connector. Positions
// schematic.
export function r330Rear() {
  const c = rearChassis(1, 434.0);
  let s = c.svg;
  const x = c.x0 + 2;
  s += pcie(x, 2.2, 96, 15.6, { label: "1" }) + pcie(x + 98, 2.2, 112, 15.6, { label: "2" });
  s += mark("slot", 1, x, 2.2, 96, 15.6) + mark("slot", 2, x + 98, 2.2, 112, 15.6);
  const y = 22;
  s += dsub(x, y - 0.6, "db9");
  s += recess(x + 34, y + 1, 12, 3) + text(x + 40, y + 8, "vFlash", { size: 1.5, anchor: "middle" });
  s += rj45(x + 48, y, { up: false }) + mark("port", "bmc", x + 48, y, 11.6, 9.6) + text(x + 53.8, y + 15.6, "iDRAC", { size: 1.6, anchor: "middle" });
  s += dsub(x + 64, y - 0.6, "vga");
  s += usb(x + 98, y - 1.2) + usb(x + 98, y + 5.6);
  s += rj45(x + 114, y, { up: false }) + rj45(x + 126.6, y, { up: false });
  s += mark("port", "nic1", x + 114, y, 11.6, 9.6) + mark("port", "nic2", x + 126.6, y, 11.6, 9.6);
  s += text(x + 114, y + 15.6, "1", { size: 1.6 }) + text(x + 138.2, y + 15.6, "2", { size: 1.6, anchor: "end" });
  s += button(x + 146, y + 4.5, 1.7, "blue") + recess(x + 151, y + 2, 6, 4.5, 0.4);
  s += vent(x + 160, y - 1.4, 50, 18);
  const pw = 72;
  s += vent(x + 212, 2.2, c.x1 - 2 - 2 * pw - 3.5 - (x + 212), 39.6);
  s += psu(c.x1 - 2 - 2 * pw - 1.5, 2.2, pw, 39.6, { label: "PSU1" }) + mark("psu", 1, c.x1 - 2 - 2 * pw - 1.5, 2.2, pw, 39.6);
  s += psu(c.x1 - 2 - pw, 2.2, pw, 39.6, { label: "PSU2" }) + mark("psu", 2, c.x1 - 2 - pw, 2.2, pw, 39.6);
  return svgDoc(1, "Dell PowerEdge R330, rear (schematic)", s);
}

// ───────────────────────── Arista ─────────────────────────

// Arista 1U fixed switch, port side. 7280E data sheet (7280SE-64): 48 SFP+
// and 4 QSFP+, one 100/1000 management port, one RS-232 serial port
// (RJ-45), one USB port; 7050SX data sheet: the same counts for the
// 7050SX-64. Three groups of 16 SFP+ (two rows of eight, odd on top), QSFP+
// 49-52 in two columns, management over console, USB and the status LEDs
// on the right, as on the CC0 pictures of the 7050SX-64 (elevation-images/Arista/)
// and of the 7280SE-68 (ADR 0063).
export function aristaPortSide(model) {
  const c = chassis(1, 444.5);
  let s = c.svg;
  const pitch = 14.05;
  const gap = 5.2;
  let x = c.x0 + 6.5;
  const yTop = 9.6;
  const yBot = 20.6;
  for (let grp = 0; grp < 3; grp++) {
    s += rect(x - 1.2, yTop - 1.2, 8 * pitch + 1.1, 22.8, { fill: "#9ea3a9", rx: 0.6 });
    for (let col = 0; col < 8; col++) {
      const n = grp * 16 + col * 2 + 1;
      const px = x + col * pitch;
      s += sfp(px, yTop, { w: 13.2, h: 9.4, up: true }) + mark("port", `Ethernet${n}`, px, yTop, 13.2, 9.4);
      s += sfp(px, yBot, { w: 13.2, h: 9.4, up: false }) + mark("port", `Ethernet${n + 1}`, px, yBot, 13.2, 9.4);
      s += text(px + 6.6, yTop - 2, String(n), { size: 1.8, anchor: "middle" });
      s += text(px + 6.6, yBot + 12.1, String(n + 1), { size: 1.8, anchor: "middle" });
    }
    x += 8 * pitch + gap;
  }
  // QSFP+ 49-52, two columns of two
  for (let col = 0; col < 2; col++) {
    const px = x + col * 20.4;
    s += qsfp(px, yTop, { w: 18.8, h: 9.4, up: true }) + mark("port", `Ethernet${49 + col * 2}`, px, yTop, 18.8, 9.4);
    s += qsfp(px, yBot, { w: 18.8, h: 9.4, up: false }) + mark("port", `Ethernet${50 + col * 2}`, px, yBot, 18.8, 9.4);
    s += text(px + 9.4, yTop - 2, String(49 + col * 2), { size: 1.8, anchor: "middle" });
    s += text(px + 9.4, yBot + 12.1, String(50 + col * 2), { size: 1.8, anchor: "middle" });
  }
  x += 2 * 20.4 + 4;
  // management over console, USB, status LEDs
  s += rj45(x, yTop, { w: 11.4, h: 9.4, up: true }) + mark("port", "Management1", x, yTop, 11.4, 9.4);
  s += rj45(x, yBot, { w: 11.4, h: 9.4, up: false, leds: false });
  s += text(x + 5.7, yTop - 2, "MGMT", { size: 1.6, anchor: "middle" });
  s += text(x + 5.7, yBot + 12.1, "CONSOLE", { size: 1.4, anchor: "middle" });
  x += 14;
  s += usb(x, yBot - 1.2, { vertical: true });
  x += 7.4;
  ["STATUS", "FAN", "PS1", "PS2"].forEach((l, i) => {
    s += led(x + 1, 9.4 + i * 4, "green", 0.65);
    s += text(x + 2.6, 10.1 + i * 4, l, { size: 1.4 });
  });
  s += text(c.x0 + 6, 41.4, model, { size: 2, fill: "#3b3f45" });
  return svgDoc(1, `Arista ${model}, port side (schematic)`, s);
}

// Arista 1U fixed switch, power side. 7280E and 7050SX data sheets: two
// hot-swap power supplies (1+1), four hot-swap fans (N+1), reversible
// airflow, colour-coded modules (the 7050X data sheet: blue for
// rear-to-front; red here for front-to-rear). PSU1, four fans, PSU2, as on
// the CC0 rear picture of the 7280SE-68 (ADR 0063).
export function aristaPowerSide(model, airflow) {
  const c = rearChassis(1, 444.5);
  let s = c.svg;
  const handle = airflow === "front-to-rear" ? "#b5524f" : "#4d78b4";
  const pw = 74;
  const y = 2.4;
  const h = 39.4;
  s += psu(c.x0 + 2, y, pw, h, { handle, label: "PS1" }) + mark("psu", 1, c.x0 + 2, y, pw, h);
  s += psu(c.x1 - 2 - pw, y, pw, h, { handle, label: "PS2" }) + mark("psu", 2, c.x1 - 2 - pw, y, pw, h);
  const fx0 = c.x0 + 2 + pw + 3;
  const fw = (c.x1 - 2 - pw - 3 - fx0 - 3 * 2) / 4;
  for (let i = 0; i < 4; i++) s += fanModule(fx0 + i * (fw + 2), y, fw, h, { handle, label: `FAN${i + 1}` }) + mark("fan", i + 1, fx0 + i * (fw + 2), y, fw, h);
  return svgDoc(1, `Arista ${model}, power side (schematic)`, s);
}

// ───────────────────────── MikroTik ─────────────────────────

// MikroTik CRS354-48G-4S+2Q+RM. Product page and user manual (MikroTik
// documentation): 443 x 297 x 44 mm; 48 Gigabit Ethernet ports, four 10G
// SFP+, two 40G QSFP+, one RJ-45 console port, one 10/100 Ethernet
// management port, three cooling fans, two integrated power supplies with
// AC inputs, a reset button; no display. Port groups and the side of the
// management ports are schematic (the text does not say).
export function crs354Front() {
  const c = chassis(1, 443);
  let s = c.svg;
  const yTop = 9.6;
  const yBot = 20.8;
  let x = c.x0 + 4;
  // console over management, reset
  s += rj45(x, yTop, { w: 11.4, h: 9.6, up: true, leds: false });
  // RouterOS names the management port ether49.
  s += rj45(x, yBot, { w: 11.4, h: 9.6, up: false }) + mark("port", "ether49", x, yBot, 11.4, 9.6);
  s += text(x + 5.7, yTop - 2, "SERIAL", { size: 1.4, anchor: "middle" });
  s += text(x + 5.7, yBot + 12.2, "MGMT", { size: 1.4, anchor: "middle" });
  s += pinhole(x + 15.6, 15) + text(x + 15.6, 19.4, "RST", { size: 1.3, anchor: "middle" });
  ["PWR1", "PWR2", "FAULT"].forEach((l, i) => {
    s += led(x + 14.6, 25 + i * 4, i < 2 ? "green" : "off", 0.6) + text(x + 16, 25.6 + i * 4, l, { size: 1.2 });
  });
  x += 24;
  // 48 x RJ-45, two rows, six groups of eight (four columns), odd on top
  const pitch = 13.15;
  for (let grp = 0; grp < 6; grp++) {
    for (let col = 0; col < 4; col++) {
      const n = grp * 8 + col * 2 + 1;
      const px = x + col * pitch;
      s += rj45(px, yTop, { w: 11.8, h: 9.6, up: true }) + mark("port", `ether${n}`, px, yTop, 11.8, 9.6);
      s += rj45(px, yBot, { w: 11.8, h: 9.6, up: false }) + mark("port", `ether${n + 1}`, px, yBot, 11.8, 9.6);
      s += text(px + 5.9, yTop - 2, String(n), { size: 1.7, anchor: "middle" });
      s += text(px + 5.9, yBot + 12.2, String(n + 1), { size: 1.7, anchor: "middle" });
    }
    x += 4 * pitch + 2.2;
  }
  x += 1.6;
  // SFP+ 1-4 (two columns), QSFP+ 1-2 (one column)
  for (let col = 0; col < 2; col++) {
    const px = x + col * 14.4;
    s += sfp(px, yTop, { w: 13.4, h: 9.6, up: true }) + sfp(px, yBot, { w: 13.4, h: 9.6, up: false });
    s += mark("port", `sfp-sfpplus${col * 2 + 1}`, px, yTop, 13.4, 9.6) + mark("port", `sfp-sfpplus${col * 2 + 2}`, px, yBot, 13.4, 9.6);
    s += text(px + 6.7, yTop - 2, `S${col * 2 + 1}`, { size: 1.6, anchor: "middle" });
    s += text(px + 6.7, yBot + 12.2, `S${col * 2 + 2}`, { size: 1.6, anchor: "middle" });
  }
  x += 2 * 14.4 + 2.4;
  s += qsfp(x, yTop, { w: 18.8, h: 9.6, up: true }) + qsfp(x, yBot, { w: 18.8, h: 9.6, up: false });
  // RouterOS names the lanes of a QSFP+ qsfpplus1-1 to qsfpplus1-4.
  s += mark("port", "qsfpplus1", x, yTop, 18.8, 9.6) + mark("port", "qsfpplus2", x, yBot, 18.8, 9.6);
  s += text(x + 9.4, yTop - 2, "Q1", { size: 1.6, anchor: "middle" }) + text(x + 9.4, yBot + 12.2, "Q2", { size: 1.6, anchor: "middle" });
  s += text(c.x1 - 3, 41.6, "CRS354-48G-4S+2Q+RM", { size: 1.9, anchor: "end" });
  return svgDoc(1, "MikroTik CRS354-48G-4S+2Q+RM, front (schematic)", s);
}

// CRS354 rear: two AC inputs (IEC C14, two integrated supplies) and three
// fans. Positions schematic.
export function crs354Rear() {
  const c = rearChassis(1, 443);
  let s = c.svg;
  const y = 2.4;
  const h = 39.4;
  s += rect(c.x0 + 4, y, 60, h, { fill: "#b7bbc1", rx: 0.8, stroke: "#8a8f95", "stroke-width": 0.3 });
  s += text(c.x0 + 34, y + h - 3, "AC 1", { size: 1.8, anchor: "middle" });
  s += rect(c.x1 - 64, y, 60, h, { fill: "#b7bbc1", rx: 0.8, stroke: "#8a8f95", "stroke-width": 0.3 });
  s += text(c.x1 - 34, y + h - 3, "AC 2", { size: 1.8, anchor: "middle" });
  s += c14(c.x0 + 27.5, y + 13.8) + c14(c.x1 - 40.5, y + 13.8);
  // RouterOS reports the two integrated supplies as psu1 and psu2.
  s += mark("psu", 1, c.x0 + 4, y, 60, h) + mark("psu", 2, c.x1 - 64, y, 60, h);
  const fx0 = c.x0 + 100;
  const span = c.x1 - 100 - fx0;
  for (let i = 0; i < 3; i++) {
    const cx = fx0 + (span * (i + 0.5)) / 3;
    s += circle(cx, y + h / 2, 17.4, { fill: "url(#hexdark)", stroke: "#4a4e54", "stroke-width": 0.6 }) + mark("fan", i + 1, cx - 17.4, y + h / 2 - 17.4, 34.8, 34.8);
    s += circle(cx, y + h / 2, 4.2, { fill: "#4a4f55" });
  }
  s += circle(c.x0 + 80, y + h / 2, 2.2, { fill: "#7c8188", stroke: "#4a4e54", "stroke-width": 0.4 });
  s += text(c.x0 + 80, y + h / 2 + 6, "GND", { size: 1.5, anchor: "middle" });
  return svgDoc(1, "MikroTik CRS354-48G-4S+2Q+RM, rear (schematic)", s);
}
