// The catalogue faces: the most common refurbished and data-centre models,
// drawn by parametric builders from counts the vendors publish (owner's
// manuals, QuickSpecs, data sheets) and from the port templates of the model
// files (themselves taken from the vendors' tables). Which models are in the
// catalogue, and why, is in docs/reference/devicetypes.md ("Catalogue").
//
// These faces are schematic: they show how many drive bays, power supplies,
// fans, slots and ports a face has and how they are grouped, never a vendor's
// styling. Positions the documents do not give are the builder's, the same
// for every model of a kind. Nothing here was traced or derived from a vendor
// photograph, render or stencil (VisioCafe included), or from a reseller's
// pictures. Unlike the hand-made faces of faces.mjs, catalogue faces mark no
// spots (no layout): their bay and port numbering is not taken from the
// vendors' text, so the object page lists the parts beside them instead of
// guessing their places.
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
  dsub,
  fanModule,
  led,
  lffCarrier,
  pcie,
  pinhole,
  psu,
  pullTab,
  qsfp,
  RACK_W,
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

// ───────────────────────── small parts ─────────────────────────

/** IEC 60320 C13 (10 A) or C19 (16 A) receptacle, as on a PDU or a UPS. */
function outlet(x, y, kind = "c13") {
  const big = kind === "c19";
  const w = big ? 14 : 11.6;
  const h = big ? 10.6 : 9.4;
  let s = rect(x - 0.7, y - 0.7, w + 1.4, h + 1.4, { fill: "#2a2d31", rx: 0.8 });
  s += rect(x, y, w, h, { fill: "#16181a", rx: big ? 0.6 : 1.6 });
  if (big) {
    for (const px of [x + 3.2, x + w - 3.2]) s += rect(px - 1.4, y + 2.4, 2.8, 1, { fill: "#8d9298" });
    s += rect(x + w / 2 - 1.4, y + h - 3.4, 2.8, 1, { fill: "#8d9298" });
  } else {
    for (const px of [x + 3, x + w - 3]) s += rect(px - 0.5, y + 4.4, 1, 2.6, { fill: "#8d9298" });
    s += rect(x + w / 2 - 0.5, y + 1.6, 1, 2.6, { fill: "#8d9298" });
  }
  return s;
}

/** NEMA 5-15R (North American) receptacle. */
function nema515(x, y) {
  let s = rect(x - 0.7, y - 0.7, 13.4, 13.4, { fill: "#2a2d31", rx: 2 });
  s += rect(x, y, 12, 12, { fill: "#d7d9dc", rx: 1.8 });
  s += rect(x + 3, y + 2.6, 1, 4, { fill: "#1d1f22" }) + rect(x + 8, y + 2.6, 1, 4, { fill: "#1d1f22" });
  s += circle(x + 6, y + 9, 1, { fill: "#1d1f22" });
  return s;
}

/** IEC 60320 C20 inlet (16 A). */
function c20(x, y) {
  let s = rect(x - 0.8, y - 0.8, 15.6, 12.2, { fill: "#1d1f22", rx: 1 });
  s += rect(x, y, 14, 10.6, { fill: "#0f1012", rx: 0.6, stroke: "#3a3d42", "stroke-width": 0.3 });
  for (const px of [x + 3.2, x + 10.8]) s += rect(px - 1.4, y + 2.4, 2.8, 1, { fill: "#b8bcc1" });
  s += rect(x + 5.6, y + 7, 2.8, 1, { fill: "#b8bcc1" });
  return s;
}

/** A small monochrome display with a few buttons beside it. */
function display(x, y, w, h, buttons = 3) {
  let s = rect(x, y, w + buttons * 4.6 + 2, h, { fill: "#8d9298", rx: 1 });
  s += rect(x + 1.2, y + 1.2, w - 2.4, h - 2.4, { fill: "url(#lcd)", rx: 0.5 });
  for (let i = 0; i < buttons; i++) s += circle(x + w + 2.6 + i * 4.6, y + h / 2, 1.5, { fill: "#5c6168" });
  return s;
}

/** A slim optical drive bay (or its blank). */
function optical(x, y, w, h) {
  return recess(x, y, w, h) + rect(x + 1.2, y + 1.2, w - 2.4, h - 2.4, { fill: "#b5b9be", rx: 0.4 }) +
    rect(x + w - 10, y + h / 2 - 0.6, 6, 1.2, { fill: "#7d838a", rx: 0.5 });
}

/** Neutral model text, bottom right of an area. */
const label = (x, y, s, anchor = "end") => text(x, y, s, { size: 1.9, anchor, fill: "#3b3f45" });

// ───────────────────────── drive bays ─────────────────────────

// Drive bays in an area, laid out the way every 19-inch server lays them:
// 2.5-inch drives lying in two rows in 1U and standing in one row in 2U and
// more, 3.5-inch drives lying four across. Returns the SVG and the free part
// of the area (to the right of the bays, or above them) for the rest of the
// front: an optical drive, a display, a blank.
function bays(area, b, heightU) {
  const { x0, x1, y0, y1 } = area;
  const W = x1 - x0;
  const H = y1 - y0;
  const hot = b.hot !== false;
  let s = "";
  if (b.kind === "sff" && heightU === 1) {
    const cols = Math.ceil(b.n / 2);
    const cw = Math.min(70, (W - (b.reserve ?? 0)) / cols - 1.2);
    const ch = (H - 0.8) / 2;
    for (let i = 0; i < b.n; i++) {
      const col = Math.floor(i / 2);
      const row = i % 2;
      s += sffCarrier(x0 + col * (cw + 1.2), y0 + row * (ch + 0.8), cw, ch);
    }
    return { svg: s, free: { x0: x0 + cols * (cw + 1.2) + 1, x1, y0, y1 } };
  }
  if (b.kind === "sff") {
    // Standing carriers; 2U and up.
    const pitch = Math.min(17.6, W / b.n);
    const ch = Math.min(H, heightU === 2 ? 76 : H);
    for (let i = 0; i < b.n; i++) s += sffCarrier(x0 + i * pitch + 0.25, y1 - ch, pitch - 0.5, ch, { horizontal: false });
    return { svg: s, free: { x0: x0 + b.n * pitch + 1.5, x1, y0, y1 } };
  }
  // 3.5-inch: four across, rows from the bottom.
  const cols = b.cols ?? 4;
  const cw = W / cols - 0.8;
  const ch = Math.min(27, (H - 0.4 * 3) / Math.max(1, Math.ceil(b.n / cols)));
  const rows = Math.ceil(b.n / cols);
  for (let i = 0; i < b.n; i++) {
    const row = rows - 1 - Math.floor(i / cols);
    const col = i % cols;
    s += lffCarrier(x0 + col * (cw + 0.8), y1 - (rows - row) * (ch + 0.4) + 0.4, cw, ch, { hotplug: hot });
  }
  return { svg: s, free: { x0, x1, y0, y1: y1 - rows * (ch + 0.4) - 0.4 } };
}

// What fills the free part of a front: optical drive, display, blank.
function fill(area, { optical: opt = false, lcd = false, text: t } = {}) {
  const { x0, x1, y0, y1 } = area;
  const W = x1 - x0;
  const H = y1 - y0;
  if (W < 8 || H < 6) return "";
  let s = "";
  if (H < 16) {
    // A strip above 3.5-inch bays.
    let x = x0;
    if (lcd) {
      s += display(x, y0 + 0.4, 24, Math.min(10, H - 0.8));
      x += 40;
    }
    if (opt && W - (x - x0) > 132) s += optical(x1 - 128, y0 + 0.4, 128, Math.min(11, H - 0.8));
    if (t) s += label(opt ? x1 - 132 : x1, y0 + H - 1.2, t);
    return s;
  }
  // An area beside the bays.
  let y = y0;
  if (opt && W >= 60) {
    s += optical(x0, y, Math.min(W, 132), 11);
    y += 13;
  }
  if (lcd && W >= 40) {
    s += display(x0, y, Math.min(28, W - 16), 10);
    y += 12;
  }
  if (y1 - y > 6) s += blank(x0, y, W, y1 - y);
  if (t) s += label(x1 - 2, y1 - 2, t);
  return s;
}

// ───────────────────────── server fronts ─────────────────────────

// Controls on the ears of a Dell PowerEdge: system health / ID on the left,
// power button and USB on the right (owner's manuals, "Front view of the
// system": left and right control panels).
function dellEars(c, heightU) {
  const ex = c.ear / 2;
  const rx = RACK_W - ex;
  let s = led(ex, 8, "blue", 1) + button(ex, 15, 1.7, "blue") + text(ex, 21, "ID", { size: 1.5, anchor: "middle" });
  if (heightU > 1) for (let i = 0; i < 5; i++) s += led(ex, 30 + i * 4.6, "off", 0.8);
  s += button(rx, 8.5, 2.3, "green");
  s += usb(rx - 2.3, 14, { vertical: true });
  if (heightU > 1) s += usb(rx - 2.3, 30, { vertical: true }) + dsubV(rx - 4.5, 47);
  return s;
}

// A VGA connector standing on an ear.
function dsubV(x, y) {
  let s = rect(x, y, 9, 22, { fill: "#a5abb1", rx: 1.2, stroke: "#7c8288", "stroke-width": 0.3 });
  s += rect(x + 1.8, y + 3, 5.4, 16, { fill: "#2c4f7c", rx: 0.8 });
  return s;
}

// The control column of an HPE ProLiant (QuickSpecs front legends: power
// button and LED, health LED, NIC status LED, UID button, USB 3.0, iLO
// service port, serial label pull tab).
function hpeColumn(x, heightU) {
  const step = heightU === 1 ? 8.2 : 8.5;
  const y = heightU === 1 ? 7 : 10;
  let s = button(x + 4, y, 2, "green") + led(x + 4, y + step, "green") + led(x + 4, y + 2 * step, "green") + button(x + 4, y + 3 * step, 1.6, "blue");
  if (heightU === 1) {
    s += usb(x + 9.5, 4, { usb3: true, vertical: true });
    s += pullTab(x + 15.5, 3.2, 3.4, 37.6);
  } else {
    s += usb(x + 1.8, 50, { usb3: true, vertical: true }) + usb(x + 1.8, 66, { vertical: true });
    s += pullTab(x + 10, 8, 5, 72);
  }
  return s;
}

// A generic control cluster: power button and two status LEDs.
function genericColumn(x, h) {
  return button(x + 5, 8, 2.1, "green") + led(x + 5, 15, "green") + led(x + 5, 20, "off") + (h > 50 ? usb(x + 2.7, 26, { vertical: true }) : "");
}

/**
 * A server (or storage enclosure) front.
 * style: "dell" (controls on the ears), "hpe" (control column on the right),
 * "lenovo"/"generic" (a small cluster on the right).
 */
export function serverFront(o) {
  const { U: heightU, title, style = "generic", bodyW = 434 } = o;
  const c = chassis(heightU, bodyW, { earText: o.earText, leftSlot: style !== "dell" });
  let s = c.svg;
  if (style === "dell") s += dellEars(c, heightU);
  let x1 = c.x1 - 2.4;
  if (style === "hpe") {
    x1 -= heightU === 1 ? 21 : 17;
    s += hpeColumn(x1 + 1.5, heightU);
  } else if (style !== "dell") {
    x1 -= 11;
    s += genericColumn(x1 + 0.6, c.h);
  }
  const area = { x0: c.x0 + 2.4, x1, y0: 2.4, y1: c.h - 2.4 };
  const b = bays(area, o.bays, heightU);
  s += b.svg + fill(b.free, { optical: o.optical, lcd: o.lcd, text: o.label });
  return svgDoc(heightU, `${title}, front (schematic)`, s);
}

// ───────────────────────── server rears ─────────────────────────

// The rear I/O of a server, left to right from x: serial, VGA, USB, BMC
// port, the network ports; returns the SVG and the x after it.
function rearIO(x, y, o) {
  let s = "";
  if (o.serial) {
    s += dsub(x, y - 0.6, "db9");
    x += 33;
  }
  if (o.vga !== false) {
    s += dsub(x, y - 0.6, "vga");
    x += 33;
  }
  for (let i = 0; i < Math.ceil((o.usb ?? 2) / 2); i++) {
    s += usb(x, y - 1.4, { usb3: o.usb3 }) + ((o.usb ?? 2) > 2 * i + 1 ? usb(x, y + 5.6, { usb3: o.usb3 }) : "");
    x += 15;
  }
  if (o.bmc !== false) {
    s += rj45(x, y, { up: false }) + text(x + 5.8, y + 15.6, o.bmcLabel ?? "BMC", { size: 1.6, anchor: "middle" });
    x += 15;
  }
  const n = o.nics ?? 2;
  for (let i = 0; i < n; i++) s += o.nicKind === "sfp" ? sfp(x + i * 14.6, y, { up: false }) : rj45(x + i * 12.6, y, { up: false });
  x += n * (o.nicKind === "sfp" ? 14.6 : 12.6) + 2;
  if (o.id !== false) {
    s += button(x + 2.4, y + 4.8, 1.6, "blue");
    x += 7;
  }
  return { svg: s, x };
}

/**
 * A server rear: PCIe brackets, the I/O row, power supplies on the right.
 * pcie: number of slots; psus: number of hot-plug supplies (a "cabled" one is
 * drawn as a fixed inlet); nics: on-board ports; ocp: an OCP/LOM mezzanine
 * bay beside them.
 */
export function serverRear(o) {
  const { U: heightU, title, bodyW = 434 } = o;
  const c = rearChassis(heightU, bodyW);
  let s = c.svg;
  const h = c.h;
  const psus = o.psus ?? 2;
  const pw = heightU === 1 ? 60 : 54;
  const psuW = o.cabledPsu ? 70 : psus * (pw + 1.5);
  const xR = c.x1 - 2 - psuW;
  const x0 = c.x0 + 2;
  // power supplies
  if (o.cabledPsu) {
    s += vent(xR, 2.2, 70, h - 4.4) + c14(xR + 50, h / 2 - 5);
  } else if (heightU === 1) {
    for (let i = 0; i < psus; i++) s += psu(xR + 1.5 + i * (pw + 1.5) - 1.5, 2.2, pw, 39.6, { label: String(i + 1) });
  } else {
    for (let i = 0; i < psus; i++) s += psu(xR + i * (pw + 1.5), h - 42.4, pw, 40, { label: String(i + 1) });
    s += vent(xR, 2.4, psuW - 1.5, h - 47);
  }
  // PCIe slots: one row in 1U, risers of three in 2U and up.
  const n = o.pcie ?? 2;
  const avail = xR - 3 - x0;
  if (heightU === 1) {
    const sw = Math.min(110, avail / Math.max(n, 2) - 2);
    for (let i = 0; i < n; i++) s += pcie(x0 + i * (sw + 2), 2.2, sw, 15.6);
    if (n * (sw + 2) < avail - 20) s += vent(x0 + n * (sw + 2), 2.2, avail - n * (sw + 2), 15.6);
  } else {
    const cols = Math.max(2, Math.ceil(n / 3));
    const sw = Math.min(120, avail / cols - 3);
    for (let i = 0; i < n; i++) s += pcie(x0 + Math.floor(i / 3) * (sw + 3), 2.4 + (i % 3) * 14.6, sw, 13.6);
    const used = cols * (sw + 3);
    if (avail - used > 20) s += vent(x0 + used, 2.4, avail - used, 42);
  }
  const y = heightU === 1 ? 22 : 54;
  const io = rearIO(x0 + 1, y, o);
  s += io.svg;
  if (o.ocp) {
    s += recess(io.x + 2, y - 1.2, 54, 12.4) + text(io.x + 29, y + 15.6, o.ocp, { size: 1.6, anchor: "middle" });
    io.x += 58;
  }
  if (xR - 4 - io.x > 14) s += vent(io.x + 2, y - 2, xR - 4 - io.x - 2, heightU === 1 ? 18 : Math.min(32, h - y));
  return svgDoc(heightU, `${title}, rear (schematic)`, s);
}

// ───────────────────────── switches ─────────────────────────

const PITCH = { rj45: 13.15, sfp: 14.05, qsfp: 20.4, stack: 16 };
const SUB = { rj45: 6, sfp: 4, qsfp: 6 }; // columns per sub-group

/**
 * A switch, firewall or router port side: groups of ports in two rows
 * (odd on top), management and console on one side, status LEDs.
 * groups: [{kind: "rj45"|"sfp"|"qsfp", n}]
 */
export function switchFront(o) {
  const { title, bodyW = 440, side = "left", heightU = 1 } = o;
  const c = chassis(heightU, bodyW);
  let s = c.svg;
  const yTop = heightU === 1 ? 9.6 : 30;
  const yBot = yTop + 11;
  const mgmtW = o.mgmt === false && o.console === false ? 0 : 16;
  const ledW = 12;
  // width of the port groups at full pitch
  const cols = o.groups.map((gr) => Math.ceil(gr.n / 2));
  let need = 0;
  o.groups.forEach((gr, i) => {
    need += cols[i] * PITCH[gr.kind] + Math.max(0, Math.floor((cols[i] - 1) / SUB[gr.kind])) * 2.4 + 5;
  });
  const avail = c.x1 - c.x0 - 8 - mgmtW - ledW - (o.display ? 34 : 0);
  const k = Math.min(1, avail / need);
  let x = c.x0 + 4;
  const mgmt = (mx) => {
    let m = "";
    if (o.mgmt !== false) m += rj45(mx, yTop, { w: 11.4 * Math.min(1, k + 0.1), h: 9.4, up: true }) + text(mx + 5.7, yTop - 2, "MGMT", { size: 1.4, anchor: "middle" });
    if (o.console !== false) m += rj45(mx, yBot, { w: 11.4 * Math.min(1, k + 0.1), h: 9.4, up: false, leds: false }) + text(mx + 5.7, yBot + 12.1, "CON", { size: 1.4, anchor: "middle" });
    return m;
  };
  if (side === "left" && mgmtW) {
    s += mgmt(x);
    x += mgmtW;
  }
  if (o.display) {
    s += display(x, yTop + 2, 18, 12, 2);
    x += 34;
  }
  o.groups.forEach((gr, gi) => {
    const p = PITCH[gr.kind] * k;
    const draw = gr.kind === "rj45" ? rj45 : gr.kind === "sfp" ? sfp : qsfp;
    const w = (gr.kind === "rj45" ? 11.8 : gr.kind === "sfp" ? 13.2 : 18.8) * Math.min(1, k * 1.02);
    const hp = 9.4;
    if (gr.kind === "rj45") s += rect(x - 0.8, yTop - 0.8, cols[gi] * p + Math.floor((cols[gi] - 1) / SUB.rj45) * 2.4 * k + 0.4, 21.6, { fill: "#9ea3a9", rx: 0.6 });
    for (let col = 0; col < cols[gi]; col++) {
      const px = x + col * p + Math.floor(col / SUB[gr.kind]) * 2.4 * k;
      const top = gr.kind === "rj45" ? rj45(px, yTop, { w, h: hp, up: true }) : draw(px, yTop, { w, h: hp, up: true });
      s += top;
      if (col * 2 + 1 < gr.n) s += gr.kind === "rj45" ? rj45(px, yBot, { w, h: hp, up: false }) : draw(px, yBot, { w, h: hp, up: false });
    }
    x += cols[gi] * p + Math.floor((cols[gi] - 1) / SUB[gr.kind]) * 2.4 * k + 5 * k;
  });
  if (side === "right" && mgmtW) {
    s += mgmt(x);
    x += mgmtW;
  }
  if (o.usb !== false && x + 6 < c.x1 - ledW) s += usb(x + 1, yBot - 1.2, { vertical: true });
  const lx = c.x1 - ledW + 2;
  ["SYS", "PWR", "FAN"].forEach((l, i) => {
    s += led(lx, yTop + i * 5, i === 2 ? "off" : "green", 0.65) + text(lx + 1.6, yTop + 0.7 + i * 5, l, { size: 1.3 });
  });
  if (o.label) s += label(c.x1 - 3, c.h - 2.6, o.label);
  return svgDoc(heightU, `${title}, port side (schematic)`, s);
}

/**
 * A switch power side. psus: hot-swap supply modules; inlets: fixed C14
 * inlets (integrated supplies); fans: fan modules; grilles: fixed fans;
 * stack: stacking ports; slot: a network-module slot on this side.
 */
export function switchRear(o) {
  const { title, bodyW = 440, heightU = 1 } = o;
  const c = rearChassis(heightU, bodyW);
  let s = c.svg;
  const h = c.h - 4.8;
  const y = 2.4;
  let x = c.x0 + 3;
  let xr = c.x1 - 3;
  const handle = o.airflow === "rear-to-front" ? "#4d78b4" : o.airflow === "front-to-rear" ? "#b5524f" : "#6b7178";
  for (let i = 0; i < (o.psus ?? 0); i++) {
    const pw = o.psuW ?? 70;
    s += psu(xr - pw, y, pw, h, { handle, label: `PS${(o.psus ?? 0) - i}` });
    xr -= pw + 2;
  }
  for (let i = 0; i < (o.inlets ?? 0); i++) {
    s += rect(xr - 30, y, 30, h, { fill: "#b7bbc1", rx: 0.8, stroke: "#8a8f95", "stroke-width": 0.3 }) + c14(xr - 21.5, y + h / 2 - 5);
    xr -= 32;
  }
  if (o.stack) {
    for (let i = 0; i < o.stack; i++) s += recess(x + i * 22, y + h / 2 - 5, 20, 10) + rect(x + i * 22 + 3, y + h / 2 - 2, 14, 4, { fill: "#6c7279" });
    s += text(x + o.stack * 11, y + h / 2 + 9, "STACK", { size: 1.5, anchor: "middle" });
    x += o.stack * 22 + 4;
  }
  if (o.slot) {
    s += blank(x, y, 90, h) + text(x + 45, y + h - 2, o.slot, { size: 1.6, anchor: "middle", fill: "#4a4e54" });
    x += 92;
  }
  const nf = o.fans ?? 0;
  if (nf > 0) {
    const fw = Math.min(46, (xr - x - 2 * nf) / nf);
    const fx0 = o.fansLeft ? x : xr - nf * (fw + 2);
    for (let i = 0; i < nf; i++) s += fanModule(fx0 + i * (fw + 2), y, fw, h, { handle });
    if (o.fansLeft) x += nf * (fw + 2);
    else xr = fx0 - 2;
  }
  for (let i = 0; i < (o.grilles ?? 0); i++) {
    const cx = x + 22 + i * 44;
    s += circle(cx, y + h / 2, Math.min(18, h / 2 - 1), { fill: "url(#hexdark)", stroke: "#4a4e54", "stroke-width": 0.6 });
    s += circle(cx, y + h / 2, 4, { fill: "#4a4f55" });
    if (i === (o.grilles ?? 0) - 1) x = cx + 24;
  }
  if (xr - x > 20) s += vent(x, y + 4, xr - x - 2, h - 8);
  return svgDoc(heightU, `${title}, power side (schematic)`, s);
}

// ───────────────────────── power ─────────────────────────

/** A rack UPS front: display and buttons, the battery behind a vented bezel. */
export function upsFront(o) {
  const { title, heightU = 2, bodyW = 432 } = o;
  const c = chassis(heightU, bodyW, { leftSlot: true });
  let s = c.svg;
  s += vent(c.x0 + 6, 5, c.x1 - c.x0 - 120, c.h - 10);
  const dx = c.x1 - 108;
  s += rect(dx, 5, 102, c.h - 10, { fill: "#5f656c", rx: 1.2 });
  s += display(dx + 6, 12, 42, 18, 4);
  s += button(dx + 84, 50, 3, "green") + text(dx + 84, 58, "ON/OFF", { size: 1.6, anchor: "middle", fill: "#e1e3e6" });
  for (let i = 0; i < 3; i++) s += led(dx + 10 + i * 9, 50, i === 0 ? "green" : "off", 1);
  if (o.label) s += text(dx + 6, c.h - 9, o.label, { size: 2, fill: "#e1e3e6" });
  return svgDoc(heightU, `${title}, front (schematic)`, s);
}

/** A rack UPS rear: outlets in groups, the input, communication ports. */
export function upsRear(o) {
  const { title, heightU = 2, bodyW = 432 } = o;
  const c = rearChassis(heightU, bodyW);
  let s = c.svg;
  let x = c.x0 + 4;
  // input
  s += rect(x, 4, 30, c.h - 8, { fill: "#b7bbc1", rx: 0.8 });
  s += o.input === "c20" ? c20(x + 8, c.h / 2 - 5) : c14(x + 8.5, c.h / 2 - 5);
  s += text(x + 15, c.h - 7, "INPUT", { size: 1.6, anchor: "middle" });
  x += 36;
  // communication: USB, serial (RJ-45), an expansion slot
  s += usb(x, 14, { vertical: true }) + rj45(x + 7, 14, { up: false, leds: false });
  s += text(x + 9, 30, "USB  SERIAL", { size: 1.5, anchor: "middle" });
  if (o.network) s += rj45(x + 7, 36, { up: false }) + text(x + 12.8, 52, "NET", { size: 1.5, anchor: "middle" });
  x += 24;
  if (o.slot !== false) {
    s += blank(x, 8, 52, 30) + text(x + 26, 42, o.slot ?? "card slot", { size: 1.6, anchor: "middle" });
    x += 58;
  }
  // battery connector (extended-run models)
  if (o.battery) {
    s += recess(x, 50, 22, 18) + text(x + 11, 74, "BATT", { size: 1.5, anchor: "middle" });
  }
  // outlets on the right, in groups
  const groups = o.groups ?? [{ c13: o.c13 ?? 0, c19: o.c19 ?? 0 }];
  let xr = c.x1 - 6;
  for (let gi = groups.length - 1; gi >= 0; gi--) {
    const gr = groups[gi];
    const n13 = gr.c13 ?? 0;
    const n19 = gr.c19 ?? 0;
    const perRow = Math.ceil(n13 / 2);
    const gw = perRow * 15 + n19 * 18 + 4;
    const gx = xr - gw;
    s += rect(gx, 6, gw, c.h - 12, { fill: "#5f656c", rx: 1 });
    for (let i = 0; i < n13; i++) s += outlet(gx + 3 + Math.floor(i / 2) * 15, 14 + (i % 2) * 18, "c13");
    for (let i = 0; i < n19; i++) s += outlet(gx + 3 + perRow * 15 + i * 18, 22, "c19");
    xr = gx - 4;
  }
  return svgDoc(heightU, `${title}, rear (schematic)`, s);
}

/**
 * A 1U rack PDU, outlets on the face: C13 / C19 (or NEMA 5-15R), a network
 * and serial port, a load display, the input.
 */
export function pduFront(o) {
  const { title, bodyW = 432 } = o;
  const c = chassis(1, bodyW);
  let s = c.svg;
  let x = c.x0 + 4;
  if (o.input === "c14") s += c14(x, 17) + text(x + 6.5, 34, "IN", { size: 1.5, anchor: "middle" });
  else if (o.input === "c20") s += c20(x, 16) + text(x + 7, 34, "IN", { size: 1.5, anchor: "middle" });
  else s += circle(x + 7, 22, 6.5, { fill: "#1d1f22" }) + circle(x + 7, 22, 4, { fill: "#3a3d42" }) + text(x + 7, 34, "CORD", { size: 1.5, anchor: "middle" });
  x += 20;
  if (o.network !== false) {
    s += rj45(x, 8, { up: true }) + text(x + 5.8, 6.4, "NET", { size: 1.4, anchor: "middle" });
    s += rect(x + 1.6, 22, 8.4, 7, { fill: "#16181a", rx: 0.6 }) + text(x + 5.8, 34, "SERIAL", { size: 1.4, anchor: "middle" });
    x += 16;
  }
  if (o.display !== false) {
    s += rect(x, 9, 20, 12, { fill: "#2a2d31", rx: 0.8 }) + text(x + 10, 18.2, "8.8", { size: 6.4, anchor: "middle", fill: "#c94f4f" });
    s += pinhole(x + 10, 27);
    x += 26;
  }
  const n13 = o.c13 ?? 0;
  const n19 = o.c19 ?? 0;
  const nn = o.nema ?? 0;
  const per = (c.x1 - 4 - x) / Math.max(1, n13 + n19 + nn);
  for (let i = 0; i < nn; i++) s += nema515(x + i * per + (per - 12) / 2, 15) + text(x + i * per + per / 2, 12.6, String(i + 1), { size: 1.6, anchor: "middle" });
  for (let i = 0; i < n13; i++) s += outlet(x + (nn + i) * per + (per - 11.6) / 2, 17, "c13") + text(x + (nn + i) * per + per / 2, 13.6, String(i + 1), { size: 1.6, anchor: "middle" });
  for (let i = 0; i < n19; i++) s += outlet(x + (nn + n13 + i) * per + (per - 14) / 2, 16, "c19") + text(x + (nn + n13 + i) * per + per / 2, 13, String(n13 + i + 1), { size: 1.6, anchor: "middle" });
  if (o.label) s += label(c.x1 - 3, 41.6, o.label);
  return svgDoc(1, `${title}, front (schematic)`, s);
}

/** The back of a 1U PDU: a plain vented case. */
export function pduRear(o) {
  const c = rearChassis(1, o.bodyW ?? 432);
  const s = c.svg + vent(c.x0 + 20, 8, c.x1 - c.x0 - 40, 28);
  return svgDoc(1, `${o.title}, rear (schematic)`, s);
}

// ───────────────────────── storage shelves ─────────────────────────

/** A disk shelf rear: I/O modules between power supplies. */
export function shelfRear(o) {
  const { title, heightU, psus, ioms } = o;
  const c = rearChassis(heightU, 447);
  let s = c.svg;
  const half = (c.h - 4.8) / 2;
  const pw = 90;
  if (psus === 4) {
    for (let i = 0; i < 4; i++) {
      const left = i % 2 === 0;
      const top = i < 2;
      s += psu(left ? c.x0 + 2 : c.x1 - 2 - pw, 2.4 + (top ? 0 : half), pw, half - 1, { label: `PSU${i + 1}` });
    }
  } else {
    s += psu(c.x0 + 2, 2.4, pw, c.h - 4.8, { label: "PSU1" }) + psu(c.x1 - 2 - pw, 2.4, pw, c.h - 4.8, { label: "PSU2" });
  }
  const mx = c.x0 + pw + 6;
  const mw = c.x1 - c.x0 - 2 * pw - 12;
  for (let i = 0; i < ioms; i++) {
    const my = 2.4 + i * ((c.h - 4.8) / ioms);
    const mh = (c.h - 4.8) / ioms - 1.5;
    s += rect(mx, my, mw, mh, { fill: "url(#module)", rx: 0.8, stroke: "#3b3f44", "stroke-width": 0.35 });
    // two SAS ports (in, out) and two ACP ports per IOM6 (installation guide)
    for (let p = 0; p < 2; p++) s += recess(mx + 8 + p * 22, my + mh / 2 - 4, 18, 8);
    s += rj45(mx + 58, my + mh / 2 - 4.8, { up: false }) + rj45(mx + 72, my + mh / 2 - 4.8, { up: false });
    s += led(mx + mw - 10, my + 5, "green");
    s += text(mx + mw - 4, my + mh - 2, `IOM ${String.fromCharCode(65 + i)}`, { size: 1.8, anchor: "end", fill: "#1f2226" });
  }
  return svgDoc(heightU, `${title}, rear (schematic)`, s);
}

// ───────────────────────── the catalogue ─────────────────────────

// Each entry: manufacturer and slug exactly as in the model file, and its
// faces. The facts behind each, and the document they come from, are listed
// beside the entry and in the library README.

const dellFront = (U1, model, bays, extra = {}) => () =>
  serverFront({ U: U1, title: `Dell PowerEdge ${model}`, style: "dell", bays, label: `PowerEdge ${model}`, ...extra });
const dellRear = (U1, model, r) => () =>
  serverRear({ U: U1, title: `Dell PowerEdge ${model}`, serial: true, bmcLabel: "iDRAC", ...r });

const sff = (n, extra = {}) => ({ kind: "sff", n, ...extra });
const lff = (n, extra = {}) => ({ kind: "lff", n, ...extra });

// Dell PowerEdge chassis variants. Drive counts per chassis: the system's
// Owner's Manual (dl.dell.com/topicspdf/poweredge-<model>_owners-manual_en-us.pdf),
// "Front view of the system" / "Front-panel features" / "Supported
// configurations". Rear: the same manual, "Back panel features" / "Rear view
// of the system" (PCIe slots, power supplies, embedded NICs, iDRAC port,
// serial, VGA, USB). Optical bay where the manual lists an optional optical
// drive for that chassis.
const dell = [
  // model, U, variants [suffix, bays, front extras], rear
  ["R230", 1, [["4lff-cabled", lff(4, { hot: false }), { optical: true }], ["4lff-hot-plug", lff(4), { optical: true }]], { pcie: 2, psus: 1, cabledPsu: true, nics: 2 }],
  ["R320", 1, [["4lff-hot-plug", lff(4), { optical: true, lcd: true }], ["8sff", sff(8, { reserve: 140 }), { optical: true, lcd: true }]], { pcie: 2, psus: 2, nics: 2 }],
  ["R420", 1, [["4lff-hot-plug", lff(4), { optical: true, lcd: true }], ["8sff", sff(8, { reserve: 140 }), { optical: true, lcd: true }]], { pcie: 2, psus: 2, nics: 2 }],
  ["R340", 1, [["4lff", lff(4), { optical: true }], ["8sff", sff(8, { reserve: 140 }), { optical: true }]], { pcie: 2, psus: 2, nics: 2 }],
  ["R430", 1, [["4lff", lff(4), { optical: true, lcd: true }], ["8sff", sff(8, { reserve: 140 }), { optical: true, lcd: true }], ["10sff", sff(10), {}]], { pcie: 2, psus: 2, nics: 4 }],
  ["R620", 1, [["8sff", sff(8, { reserve: 140 }), { optical: true, lcd: true }], ["10sff", sff(10), {}]], { pcie: 3, psus: 2, nics: 4 }],
  ["R630", 1, [["8sff", sff(8, { reserve: 140 }), { optical: true, lcd: true }], ["10sff", sff(10), {}]], { pcie: 3, psus: 2, nics: 4 }],
  ["R640", 1, [["4lff", lff(4), { optical: true }], ["8sff", sff(8, { reserve: 140 }), { optical: true }], ["10sff", sff(10), {}]], { pcie: 3, psus: 2, nics: 4 }],
  ["R650", 1, [["4lff", lff(4), {}], ["8sff", sff(8, { reserve: 140 }), {}], ["10sff", sff(10), {}]], { pcie: 3, psus: 2, nics: 2, ocp: "OCP 3.0" }],
  ["R520", 2, [["4lff", lff(4), { optical: true, lcd: true }], ["8lff", lff(8), { optical: true, lcd: true }]], { pcie: 4, psus: 2, nics: 2 }],
  ["R720", 2, [["8lff", lff(8), { optical: true, lcd: true }], ["8sff", sff(8), { optical: true, lcd: true }], ["16sff", sff(16), { optical: true, lcd: true }]], { pcie: 7, psus: 2, nics: 4 }],
  ["R720xd", 2, [["12lff", lff(12), {}], ["24sff", sff(24), {}]], { pcie: 6, psus: 2, nics: 4 }],
  ["R730", 2, [["8lff", lff(8), { optical: true, lcd: true }], ["8sff", sff(8), { optical: true, lcd: true }], ["16sff", sff(16), { optical: true, lcd: true }]], { pcie: 7, psus: 2, nics: 4 }],
  ["R730xd", 2, [["12lff", lff(12), {}], ["24sff", sff(24), {}]], { pcie: 6, psus: 2, nics: 4 }],
  ["R740", 2, [["8lff", lff(8), { optical: true }], ["8sff", sff(8), { optical: true }], ["16sff", sff(16), { optical: true }]], { pcie: 8, psus: 2, nics: 4 }],
  ["R740xd", 2, [["12lff", lff(12), {}], ["24sff", sff(24), {}]], { pcie: 8, psus: 2, nics: 4 }],
];

const dellOutputs = dell.flatMap(([model, u, variants, rear]) =>
  variants.flatMap(([suffix, b, extra]) => {
    const slug = `dell-poweredge-${model.toLowerCase()}-${suffix}`;
    return [
      ["Dell", slug, "front", dellFront(u, model, b, extra)],
      ["Dell", slug, "rear", dellRear(u, model, rear)],
    ];
  }),
);

// HPE ProLiant chassis variants, from the QuickSpecs of each server (front
// view legends, chassis types, CTO table; rear view legends):
// DL360p Gen8 c04128242, DL380p Gen8 c04128241, DL360 Gen9 c04346229,
// DL380 Gen9 c04346247, DL360 Gen10 a00008159enw, DL380 Gen10 a00008180enw.
const hpeFront = (u, model, b, extra = {}) => () =>
  serverFront({ U: u, title: `HPE ProLiant ${model}`, style: "hpe", bays: b, earText: `ProLiant\n${model.replace(" ", "\n")}`, ...extra });
const hpeRear = (u, model, r) => () => serverRear({ U: u, title: `HPE ProLiant ${model}`, bmcLabel: "iLO", serial: true, usb3: true, ...r });

const hpe = [
  // DL360p Gen8: PCIe x16 FH and x8 LP, FlexibleLOM 4x1GbE, video, serial, iLO, UID, 4 USB, two supplies.
  ["DL360p Gen8", 1, [["4lff", lff(4), { optical: true }], ["8sff", sff(8, { reserve: 120 }), { optical: true }], ["10sff", sff(10), {}]], { pcie: 2, nics: 4, usb: 4 }],
  // DL380p Gen8: slots 1-3 and 4-6, two supplies, UID, 4 USB, video, iLO, serial, FlexibleLOM 4x1GbE.
  ["DL380p Gen8", 2, [["8lff", lff(8), { optical: true }], ["8sff", sff(8), { optical: true }], ["12lff", lff(12), {}], ["25sff", sff(25), {}]], { pcie: 6, nics: 4, usb: 4 }],
  ["DL360 Gen9", 1, [["4lff", lff(4), { optical: true }]], { pcie: 3, nics: 4 }],
  ["DL380 Gen9", 2, [["12lff", lff(12), {}]], { pcie: 6, nics: 4 }],
  // DL360 Gen10: slots 1-3, PSU 1-2, VGA, embedded 4x1GbE, iLO, optional serial, USB 3.0, FlexibleLOM.
  ["DL360 Gen10", 1, [["4lff", lff(4), { optical: true }], ["8sff", sff(8, { reserve: 120 }), { optical: true }]], { pcie: 3, nics: 4, ocp: "FlexibleLOM" }],
  // DL380 Gen10: the LFF fronts only. Its SFF fronts (boxes 1-3) and its rear,
  // with their layouts, are drawn in faces.mjs (dl380Gen10*).
  ["DL380 Gen10", 2, [["8lff", lff(8), { optical: true }], ["12lff", lff(12), {}]], null],
];

const hpeOutputs = hpe.flatMap(([model, u, variants, rear]) =>
  variants.flatMap(([suffix, b, extra]) => {
    const slug = `hpe-proliant-${model.toLowerCase().replace(/ /g, "-")}-${suffix}`;
    return [
      ["HPE", slug, "front", hpeFront(u, model, b, extra)],
      ...(rear ? [["HPE", slug, "rear", hpeRear(u, model, rear)]] : []),
    ];
  }),
);

// Network, storage and power models without a picture (or with a
// port side only). Port counts and groups: the model file (from the
// vendor's data sheet) and the data sheet named beside each entry.
const sw = (title, groups, extra = {}) => () => switchFront({ title, groups, ...extra });
const swr = (title, extra) => () => switchRear({ title, ...extra });

const network = [
  // Cisco Catalyst 3750-X data sheet: 24/48 10/100/1000 ports, network module slot (4 x 1G SFP or 2 x 10G SFP+), two field-replaceable supplies, StackWise Plus, StackPower.
  ["Cisco", "cisco-ws-c3750x-24t-s", sw("Cisco WS-C3750X-24T-S", [{ kind: "rj45", n: 24 }], { side: "left", label: "WS-C3750X-24T-S" }), swr("Cisco WS-C3750X-24T-S", { psus: 2, fans: 1, stack: 2, psuW: 80 })],
  ["Cisco", "cisco-ws-c3750x-48p-s", sw("Cisco WS-C3750X-48P-S", [{ kind: "rj45", n: 48 }], { side: "left", label: "WS-C3750X-48P-S" }), swr("Cisco WS-C3750X-48P-S", { psus: 2, fans: 1, stack: 2, psuW: 80 })],
  // Catalyst 3850 data sheet: 24/48 ports, network module (4 x 1G or 2/4 x 10G), two supplies, three fans, StackWise-480.
  ["Cisco", "cisco-ws-c3850-24t-s", sw("Cisco WS-C3850-24T-S", [{ kind: "rj45", n: 24 }, { kind: "sfp", n: 4 }], { side: "left", label: "WS-C3850-24T-S" }), swr("Cisco WS-C3850-24T-S", { psus: 2, fans: 3, stack: 2, psuW: 80 })],
  ["Cisco", "cisco-ws-c3850-48t-s", sw("Cisco WS-C3850-48T-S", [{ kind: "rj45", n: 48 }, { kind: "sfp", n: 4 }], { side: "left", label: "WS-C3850-48T-S" }), swr("Cisco WS-C3850-48T-S", { psus: 2, fans: 3, stack: 2, psuW: 80 })],
  ["Cisco", "cisco-ws-c3850-48p-s", sw("Cisco WS-C3850-48P-S", [{ kind: "rj45", n: 48 }, { kind: "sfp", n: 4 }], { side: "left", label: "WS-C3850-48P-S" }), swr("Cisco WS-C3850-48P-S", { psus: 2, fans: 3, stack: 2, psuW: 80 })],
  // Catalyst 9300 data sheet: 24 x 1G, network module slot, two supplies, three fans, StackWise-480.
  ["Cisco", "cisco-c9300-24t", sw("Cisco C9300-24T", [{ kind: "rj45", n: 24 }, { kind: "sfp", n: 4 }], { side: "left", label: "C9300-24T" }), swr("Cisco C9300-24T", { psus: 2, fans: 3, stack: 2, psuW: 80 })],
  // Catalyst 2960-X data sheet: one fixed supply (C14), fixed fans; the rear the pictures lack.
  ...["24ts-l", "48ts-l", "48fps-l", "48td-l"].map((m) => ["Cisco", `cisco-ws-c2960x-${m}`, null, swr(`Cisco WS-C2960X-${m.toUpperCase()}`, { inlets: 1, grilles: m.startsWith("48fps") ? 2 : 1 })]),
  // Nexus 3048 data sheet: 48 x 1GbE, 4 x 10G SFP+, two supplies, one fan tray (4 fans), mgmt and console.
  ["Cisco", "cisco-n3k-c3048tp-1ge", sw("Cisco N3K-C3048TP-1GE", [{ kind: "rj45", n: 48 }, { kind: "sfp", n: 4 }], { side: "right", label: "N3K-C3048TP-1GE" }), swr("Cisco N3K-C3048TP-1GE", { psus: 2, fans: 1, psuW: 70 })],
  // Nexus 3064-X data sheet: 48 x SFP+, 4 x QSFP+, two supplies, one fan tray.
  ["Cisco", "cisco-n3k-c3064pq-10gx", sw("Cisco N3K-C3064PQ-10GX", [{ kind: "sfp", n: 48 }, { kind: "qsfp", n: 4 }], { side: "right", label: "N3K-C3064PQ-10GX" }), swr("Cisco N3K-C3064PQ-10GX", { psus: 2, fans: 1, psuW: 70 })],
  // Arista 7050QX-32S, 7050TX-64, 7280SR-48C6: two supplies, four fans (data sheets); rears the pictures lack.
  ["Arista", "arista-dcs-7050qx-32s", null, swr("Arista DCS-7050QX-32S", { psus: 2, fans: 4, fansLeft: false, psuW: 74 })],
  ["Arista", "arista-dcs-7050tx-64", null, swr("Arista DCS-7050TX-64", { psus: 2, fans: 4, psuW: 74 })],
  ["Arista", "arista-dcs-7050tx-48", null, swr("Arista DCS-7050TX-48", { psus: 2, fans: 4, psuW: 74 })],
  ["Arista", "arista-dcs-7280sr-48c6-f", null, swr("Arista DCS-7280SR-48C6-F", { psus: 2, fans: 4, psuW: 74, airflow: "front-to-rear" })],
  ["Arista", "arista-dcs-7280sr-48c6-r", null, swr("Arista DCS-7280SR-48C6-R", { psus: 2, fans: 4, psuW: 74, airflow: "rear-to-front" })],
  // Juniper EX2200 / EX3300 / EX4300 hardware guides.
  ["Juniper", "juniper-ex2200-24t-4g", sw("Juniper EX2200-24T-4G", [{ kind: "rj45", n: 24 }, { kind: "sfp", n: 4 }], { side: "right", mgmt: false, label: "EX2200-24T-4G" }), swr("Juniper EX2200-24T-4G", { inlets: 1, grilles: 1 })],
  ["Juniper", "juniper-ex2200-48t-4g", null, swr("Juniper EX2200-48T-4G", { inlets: 1, grilles: 2 })],
  ["Juniper", "juniper-ex3300-48t", sw("Juniper EX3300-48T", [{ kind: "rj45", n: 48 }, { kind: "sfp", n: 4 }], { side: "right", label: "EX3300-48T" }), swr("Juniper EX3300-48T", { inlets: 1, grilles: 2 })],
  ["Juniper", "juniper-ex4300-48t", sw("Juniper EX4300-48T", [{ kind: "rj45", n: 48 }], { side: "right", label: "EX4300-48T" }), swr("Juniper EX4300-48T", { psus: 2, fans: 2, stack: 4, psuW: 70 })],
  // MikroTik product pages and quick guides.
  ["MikroTik", "mikrotik-crs326-24s-plus-2q-plus-rm", sw("MikroTik CRS326-24S+2Q+RM", [{ kind: "sfp", n: 24 }, { kind: "qsfp", n: 2 }], { side: "left", label: "CRS326-24S+2Q+RM" }), swr("MikroTik CRS326-24S+2Q+RM", { inlets: 2, grilles: 2 })],
  ["MikroTik", "mikrotik-crs312-4c-plus-8xg-rm", sw("MikroTik CRS312-4C+8XG-RM", [{ kind: "rj45", n: 12 }, { kind: "sfp", n: 4 }], { side: "left", label: "CRS312-4C+8XG-RM" }), swr("MikroTik CRS312-4C+8XG-RM", { inlets: 2, grilles: 2 })],
  ["MikroTik", "mikrotik-crs354-48p-4s-plus-2q-plus-rm", sw("MikroTik CRS354-48P-4S+2Q+RM", [{ kind: "rj45", n: 48 }, { kind: "sfp", n: 4 }, { kind: "qsfp", n: 2 }], { side: "left", label: "CRS354-48P-4S+2Q+RM" }), swr("MikroTik CRS354-48P-4S+2Q+RM", { inlets: 1, grilles: 3 })],
  ["MikroTik", "mikrotik-crs328-24p-4s-plus-rm", null, swr("MikroTik CRS328-24P-4S+RM", { inlets: 1, grilles: 2 })],
  // Ubiquiti UniFi switch data sheets.
  ["Ubiquiti", "ubiquiti-unifi-switch-24-gen2", sw("Ubiquiti USW-24", [{ kind: "rj45", n: 24 }, { kind: "sfp", n: 2 }], { side: "right", mgmt: false, console: false, display: true }), swr("Ubiquiti USW-24", { inlets: 1 })],
  ["Ubiquiti", "ubiquiti-unifi-switch-48-gen2", sw("Ubiquiti USW-48", [{ kind: "rj45", n: 48 }, { kind: "sfp", n: 4 }], { side: "right", mgmt: false, console: false, display: true }), swr("Ubiquiti USW-48", { inlets: 1 })],
  ["Ubiquiti", "ubiquiti-es-48-lite", sw("Ubiquiti ES-48-LITE", [{ kind: "rj45", n: 48 }, { kind: "sfp", n: 4 }], { side: "left", mgmt: false }), swr("Ubiquiti ES-48-LITE", { inlets: 1, grilles: 2 })],
  ["Ubiquiti", "ubiquiti-unifi-switch-aggregation", null, swr("Ubiquiti USW-Aggregation", { inlets: 1 })],
  // Aruba 2530 / 2930F installation guides: one internal supply (C14), fans or fanless.
  ...["2530-24g", "2530-48g", "2930f-24g-4sfpp", "2930f-48g-4sfpp"].map((m) => ["HPE", `hpe-aruba-${m}`, null, swr(`Aruba ${m.toUpperCase()}`, { inlets: 1, grilles: m.includes("48") ? 2 : 1 })]),
  // FortiGate data sheets.
  ["Fortinet", "fortinet-fg-100f", null, swr("Fortinet FG-100F", { inlets: 2, grilles: 2 })],
  ["Fortinet", "fortinet-fg-200f", sw("Fortinet FG-200F", [{ kind: "rj45", n: 18 }, { kind: "sfp", n: 8 }, { kind: "sfp", n: 4 }], { side: "left", label: "FG-200F" }), swr("Fortinet FG-200F", { inlets: 2, grilles: 2 })],
  ["Fortinet", "fortinet-fg-600e", sw("Fortinet FG-600E", [{ kind: "rj45", n: 10 }, { kind: "sfp", n: 8 }, { kind: "sfp", n: 2 }], { side: "left", label: "FG-600E", display: true }), swr("Fortinet FG-600E", { psus: 2, grilles: 3, psuW: 70 })],
];

const networkOutputs = network.flatMap(([vendor, slug, front, rear]) => [
  ...(front ? [[vendor, slug, "front", front]] : []),
  ...(rear ? [[vendor, slug, "rear", rear]] : []),
]);

// Storage: Synology and QNAP rack units (hardware installation guides: drive
// bays, LAN ports, supplies), NetApp shelves (installation guides: DS4246 4U
// 24 x 3.5-inch, four PSUs, two IOM6; DS2246 2U 24 x 2.5-inch, two PSUs, two IOM6).
const nas = (title, u, b, extra = {}) => () => serverFront({ U: u, title, style: "generic", bays: b, label: extra.label, ...extra });
const storage = [
  ["Synology", "synology-rs820-plus", nas("Synology RS820+", 1, lff(4)), () => serverRear({ U: 1, title: "Synology RS820+", psus: 1, cabledPsu: true, pcie: 1, nics: 4, vga: false, bmc: false, serial: false })],
  ["Synology", "synology-rs2421-plus", nas("Synology RS2421+", 2, lff(12)), () => serverRear({ U: 2, title: "Synology RS2421+", psus: 1, cabledPsu: true, pcie: 1, nics: 4, vga: false, bmc: false, serial: false })],
  ["Synology", "synology-rs1221-plus", null, () => serverRear({ U: 2, title: "Synology RS1221+", psus: 1, cabledPsu: true, pcie: 1, nics: 4, vga: false, bmc: false, serial: false })],
  ["QNAP", "qnap-ts-1263u-rp", nas("QNAP TS-1263U-RP", 2, lff(12)), () => serverRear({ U: 2, title: "QNAP TS-1263U-RP", psus: 2, pcie: 1, nics: 4, vga: false, bmc: false, serial: false })],
  ["QNAP", "qnap-ts-863u-rp", nas("QNAP TS-863U-RP", 2, lff(8), { optical: false }), () => serverRear({ U: 2, title: "QNAP TS-863U-RP", psus: 2, pcie: 2, nics: 4, vga: true, bmc: false, serial: false })],
  ["NetApp", "netapp-ds4246", nas("NetApp DS4246", 4, lff(24)), () => shelfRear({ title: "NetApp DS4246", heightU: 4, psus: 4, ioms: 2 })],
  ["NetApp", "netapp-ds2246", nas("NetApp DS2246", 2, sff(24)), () => shelfRear({ title: "NetApp DS2246", heightU: 2, psus: 2, ioms: 2 })],
];
const storageOutputs = storage.flatMap(([vendor, slug, front, rear]) => [...(front ? [[vendor, slug, "front", front]] : []), [vendor, slug, "rear", rear]]);

// Power: APC Smart-UPS (SMT, SMX, SRT) and switched rack PDUs (AP79xx),
// Eaton 5PX; outlets, inputs and ports from the model files (from
// the vendors' data sheets) and the user manuals.
const power = [
  ["APC", "apc-smt1500rmi2u", () => upsFront({ title: "APC SMT1500RMI2U", label: "Smart-UPS 1500" }), () => upsRear({ title: "APC SMT1500RMI2U", input: "c14", groups: [{ c13: 2 }, { c13: 2 }], slot: "SmartSlot" })],
  ["APC", "apc-smt3000rmi2u", () => upsFront({ title: "APC SMT3000RMI2U", label: "Smart-UPS 3000" }), () => upsRear({ title: "APC SMT3000RMI2U", input: "c20", groups: [{ c13: 4 }, { c13: 4, c19: 1 }], slot: "SmartSlot" })],
  ["APC", "apc-smx1500rmi2u", () => upsFront({ title: "APC SMX1500RMI2U", label: "Smart-UPS X 1500" }), () => upsRear({ title: "APC SMX1500RMI2U", input: "c14", groups: [{ c13: 3 }, { c13: 4, c19: 1 }], slot: "SmartSlot", battery: true })],
  ["APC", "apc-smart-ups-srt-3000va-rm", () => upsFront({ title: "APC SRT3000RMXLI", label: "Smart-UPS SRT 3000" }), () => upsRear({ title: "APC SRT3000RMXLI", input: "c20", groups: [{ c13: 4, c19: 1 }, { c13: 4, c19: 1 }], slot: "SmartSlot", network: true, battery: true })],
  ["Eaton", "eaton-5px3000irt2u", () => upsFront({ title: "Eaton 5PX3000IRT2U", label: "5PX 3000" }), () => upsRear({ title: "Eaton 5PX3000IRT2U", input: "c20", groups: [{ c13: 4 }, { c13: 4, c19: 1 }], slot: "network card", network: true, battery: true })],
  ["APC", "apc-ap7920", () => pduFront({ title: "APC AP7920", c13: 8, input: "c14", label: "AP7920" }), () => pduRear({ title: "APC AP7920" })],
  ["APC", "apc-ap7921", () => pduFront({ title: "APC AP7921", c13: 8, input: "c20", label: "AP7921" }), () => pduRear({ title: "APC AP7921" })],
  ["APC", "apc-ap7900b", () => pduFront({ title: "APC AP7900B", nema: 8, input: "cord", label: "AP7900B" }), () => pduRear({ title: "APC AP7900B" })],
];
const powerOutputs = power.flatMap(([vendor, slug, front, rear]) => [
  [vendor, slug, "front", front],
  [vendor, slug, "rear", rear],
]);

/** Every catalogue face: [manufacturer, slug, face, draw]. */
export const catalogueOutputs = [...dellOutputs, ...hpeOutputs, ...networkOutputs, ...storageOutputs, ...powerOutputs];

