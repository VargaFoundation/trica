// Drawing primitives for the device faces of the device-type library.
//
// Everything is in millimetres: a face is 482.6 mm wide (a 19-inch panel,
// ears included) and n x 44.45 mm high. Every function returns an SVG
// fragment as a string and is pure: the same arguments give the same bytes,
// so a regenerated file only changes when the drawing does.
//
// The drawings are original, schematic and vendor-neutral: greys, no logo, no
// trademark. Counts, grouping and order of the parts come from the vendors'
// public text (QuickSpecs, owner's manuals, data sheets), cited in faces.mjs.
// The drawings this code produces are dedicated to the public domain under
// CC0-1.0 (see internal/devicetypes/library/README.md); the code itself is
// Apache-2.0, like the rest of the repository.

export const RACK_W = 482.6;
export const U = 44.45;

/** Round to 0.01 mm so that the output does not depend on float noise. */
export const r = (v) => Math.round(v * 100) / 100;

// Spots: where the parts a device reports sit on a face (drive bays, power
// supplies, fans, ports, expansion slots), in the face's millimetres. A face
// function marks them beside what it draws (s += mark(...)); generate.mjs
// writes them next to the drawing, under library/layouts, and the object page
// lays the device's parts over the image with them (ADR 0047). Marking draws
// nothing: the SVG bytes are the same with or without it.
let marks = null;

/** Marks a spot of kind bay, psu, fan, port or slot; returns "". */
export function mark(kind, name, x, y, w, h) {
  if (marks) marks.push({ kind, name: String(name), x: r(x), y: r(y), w: r(w), h: r(h) });
  return "";
}

/** Runs a face function and returns its SVG with the spots it marked. */
export function withMarks(draw) {
  marks = [];
  try {
    const svg = draw();
    return { svg, spots: marks };
  } finally {
    marks = null;
  }
}

const attrs = (o) =>
  Object.entries(o)
    .filter(([, v]) => v !== undefined && v !== null && v !== false)
    .map(([k, v]) => ` ${k}="${typeof v === "number" ? r(v) : v}"`)
    .join("");

export const rect = (x, y, w, h, a = {}) => `<rect${attrs({ x, y, width: w, height: h, ...a })}/>`;
export const circle = (cx, cy, rad, a = {}) => `<circle${attrs({ cx, cy, r: rad, ...a })}/>`;
export const path = (d, a = {}) => `<path${attrs({ d, ...a })}/>`;
export const g = (body, a = {}) => `<g${attrs(a)}>${body}</g>`;

const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/** Small neutral text; size in mm. */
export const text = (x, y, s, { size = 2.2, anchor = "start", fill = "#3b3f45", weight = 400, rotate } = {}) =>
  `<text${attrs({
    x,
    y,
    "font-size": size,
    "text-anchor": anchor,
    fill,
    "font-weight": weight === 400 ? undefined : weight,
    transform: rotate ? `rotate(${rotate} ${r(x)} ${r(y)})` : undefined,
  })}>${esc(s)}</text>`;

// Shared definitions: gradients and patterns, referenced by id.
export const defs = `<defs>
<linearGradient id="metal" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#d3d6da"/><stop offset="0.55" stop-color="#c3c7cc"/><stop offset="1" stop-color="#aeb3b9"/></linearGradient>
<linearGradient id="ear" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="#a9aeb4"/><stop offset="0.5" stop-color="#bfc3c8"/><stop offset="1" stop-color="#a3a8ae"/></linearGradient>
<linearGradient id="carrier" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#5a5f66"/><stop offset="1" stop-color="#3d4147"/></linearGradient>
<linearGradient id="carrierv" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="#5a5f66"/><stop offset="1" stop-color="#3d4147"/></linearGradient>
<linearGradient id="handle" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#7b8189"/><stop offset="1" stop-color="#5b6067"/></linearGradient>
<linearGradient id="hole" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#121315"/><stop offset="1" stop-color="#2a2d31"/></linearGradient>
<linearGradient id="cage" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#b9bdc2"/><stop offset="1" stop-color="#8e949b"/></linearGradient>
<linearGradient id="module" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#8a9097"/><stop offset="1" stop-color="#6c7279"/></linearGradient>
<linearGradient id="lcd" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#36414d"/><stop offset="1" stop-color="#1f262e"/></linearGradient>
<pattern id="hex" width="2.6" height="2.25" patternUnits="userSpaceOnUse"><rect width="2.6" height="2.25" fill="#a7acb2"/><circle cx="0.65" cy="0.56" r="0.5" fill="#2b2e32"/><circle cx="1.95" cy="1.69" r="0.5" fill="#2b2e32"/></pattern>
<pattern id="hexdark" width="2.6" height="2.25" patternUnits="userSpaceOnUse"><rect width="2.6" height="2.25" fill="#6d737a"/><circle cx="0.65" cy="0.56" r="0.55" fill="#1b1d20"/><circle cx="1.95" cy="1.69" r="0.55" fill="#1b1d20"/></pattern>
<pattern id="slots" width="3" height="6" patternUnits="userSpaceOnUse"><rect width="3" height="6" fill="#b3b8be"/><rect x="0.9" y="0.8" width="1.2" height="4.4" rx="0.6" fill="#2b2e32"/></pattern>
</defs>`;

/** A whole face: the SVG document around a body. */
export function svgDoc(heightU, title, body) {
  const h = heightU * U;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${RACK_W} ${r(h)}" width="${RACK_W}mm" height="${r(h)}mm" ` +
    `font-family="DejaVu Sans, Liberation Sans, Arial, sans-serif">` +
    `<title>${esc(title)}</title>${defs}${body}</svg>\n`
  );
}

/**
 * The front of a chassis with rack ears: the ears, the body between them and
 * a thin shadow line. bodyW is the chassis width (vendor dimension).
 */
export function chassis(heightU, bodyW, { earText, earTextSide = "right", latch = false, leftSlot = true } = {}) {
  const h = heightU * U;
  const ear = (RACK_W - bodyW) / 2;
  let s = "";
  s += rect(0, 0.6, RACK_W, h - 1.2, { fill: "#8f949a", rx: 1.2 });
  for (const x of [0, RACK_W - ear]) {
    s += rect(x + 0.3, 0.8, ear - 0.6, h - 1.6, { fill: "url(#ear)", rx: 1.2, stroke: "#8a8f95", "stroke-width": 0.3 });
    // One captive-screw slot per U, centred like the rack holes.
    for (let i = 0; i < heightU && (x > 0 || leftSlot); i++) {
      const cy = i * U + U / 2;
      s += rect(x + ear / 2 - 1.6, cy - 3.6, 3.2, 7.2, { rx: 1.6, fill: "url(#hole)" });
      if (latch) s += rect(x + ear / 2 - 3.2, cy + 5.2, 6.4, 1.4, { rx: 0.7, fill: "#8d9298" });
    }
  }
  s += rect(ear, 0.8, bodyW, h - 1.6, { fill: "url(#metal)", stroke: "#8a8f95", "stroke-width": 0.3 });
  if (earText) {
    const x = earTextSide === "right" ? RACK_W - ear / 2 : ear / 2;
    const lines = earText.split("\n");
    lines.forEach((ln, i) => {
      s += text(x, h - 4 - (lines.length - 1 - i) * 2.6, ln, { size: 2, anchor: "middle", fill: "#3b3f45" });
    });
  }
  return { svg: s, ear, x0: ear, x1: RACK_W - ear, h };
}

/**
 * The rear of a chassis: no ears (rails instead), the body centred.
 */
export function rearChassis(heightU, bodyW) {
  const h = heightU * U;
  const side = (RACK_W - bodyW) / 2;
  let s = rect(0, 0.6, RACK_W, h - 1.2, { fill: "#4a4e54", rx: 1 });
  for (const x of [0, RACK_W - side]) {
    s += rect(x + 1, 2, side - 2, h - 4, { fill: "#5d6268", rx: 0.8 });
    s += rect(x + side / 2 - 1.2, h / 2 - 4, 2.4, 8, { rx: 1.2, fill: "#2a2d31" });
  }
  s += rect(side, 0.8, bodyW, h - 1.6, { fill: "url(#metal)", stroke: "#8a8f95", "stroke-width": 0.3 });
  return { svg: s, x0: side, x1: RACK_W - side, h };
}

/** A recessed area (dark opening with a bevel). */
export const recess = (x, y, w, h, rx = 0.6) =>
  rect(x, y, w, h, { rx, fill: "url(#hole)" }) + rect(x, y, w, h, { rx, fill: "none", stroke: "#7d838a", "stroke-width": 0.35 });

/** A vented panel (hexagonal perforation). */
export const vent = (x, y, w, h, dark = false) =>
  rect(x, y, w, h, { fill: dark ? "url(#hexdark)" : "url(#hex)", rx: 0.6, stroke: "#8a8f95", "stroke-width": 0.3 });

/** A plain blank cover with a finger notch. */
export const blank = (x, y, w, h) =>
  rect(x, y, w, h, { fill: "#b7bbc1", rx: 0.8, stroke: "#8a8f95", "stroke-width": 0.35 }) +
  vent(x + w * 0.12, y + h * 0.22, w * 0.76, h * 0.56);

/** An LED, colour given ("off" is a dark lens). */
export function led(cx, cy, colour = "off", rad = 0.75) {
  const fill = { off: "#4b4f55", green: "#3fae55", amber: "#d39a2c", blue: "#4a86c5", white: "#e8eaec" }[colour] ?? colour;
  return circle(cx, cy, rad + 0.35, { fill: "#7c8188" }) + circle(cx, cy, rad, { fill });
}

/** A round push button, optionally with a ring LED. */
export function button(cx, cy, rad = 2.2, ring = "off") {
  return (
    circle(cx, cy, rad + 0.5, { fill: ring === "off" ? "#80868d" : ring === "green" ? "#3fae55" : "#4a86c5" }) +
    circle(cx, cy, rad, { fill: "#5c6168", stroke: "#3c4045", "stroke-width": 0.3 }) +
    circle(cx - rad * 0.25, cy - rad * 0.25, rad * 0.45, { fill: "#6f757c" })
  );
}

/** A pin-hole button (NMI, reset). */
export const pinhole = (cx, cy) => circle(cx, cy, 0.9, { fill: "#7c8188" }) + circle(cx, cy, 0.55, { fill: "#1c1e21" });

/** USB type-A receptacle; usb3 colours the tongue. vertical rotates it. */
export function usb(x, y, { usb3 = false, vertical = false } = {}) {
  const w = vertical ? 4.6 : 12.4;
  const h = vertical ? 12.4 : 4.6;
  let s = rect(x - 0.5, y - 0.5, w + 1, h + 1, { fill: "#9aa0a6", rx: 0.4 });
  s += rect(x, y, w, h, { fill: "#16181a", rx: 0.3 });
  const tongue = usb3 ? "#3d6aa3" : "#d9dcdf";
  s += vertical ? rect(x + 0.7, y + 1, 1.8, h - 2, { fill: tongue }) : rect(x + 1, y + 0.7, w - 2, 1.8, { fill: tongue });
  return s;
}

/** RJ-45 jack; up = latch notch at the top. LEDs in the corners. */
export function rj45(x, y, { w = 11.6, h = 9.6, up = true, leds = true } = {}) {
  let s = rect(x - 0.4, y - 0.4, w + 0.8, h + 0.8, { fill: "#9ba1a7", rx: 0.5 });
  const notchW = w * 0.42;
  const notchH = h * 0.22;
  const nx = x + (w - notchW) / 2;
  const d = up
    ? `M${r(x)} ${r(y + notchH)}H${r(nx)}V${r(y)}H${r(nx + notchW)}V${r(y + notchH)}H${r(x + w)}V${r(y + h)}H${r(x)}Z`
    : `M${r(x)} ${r(y)}H${r(x + w)}V${r(y + h - notchH)}H${r(nx + notchW)}V${r(y + h)}H${r(nx)}V${r(y + h - notchH)}H${r(x)}Z`;
  s += path(d, { fill: "#151719" });
  // contacts
  const cy = up ? y + h - 2.2 : y + 0.9;
  for (let i = 0; i < 8; i++) s += rect(x + w * 0.2 + i * (w * 0.6) / 8, cy, w * 0.035, 1.3, { fill: "#b59a4a" });
  if (leds) {
    const ly = up ? y + h - 1.1 : y + 1.1;
    s += rect(x + 0.6, ly - 0.55, 1.6, 1.1, { fill: "#4f9a5c" }) + rect(x + w - 2.2, ly - 0.55, 1.6, 1.1, { fill: "#a08a3a" });
  }
  return s;
}

/** SFP/SFP+ cage opening. */
export function sfp(x, y, { w = 13.6, h = 9.4, up = true } = {}) {
  let s = rect(x, y, w, h, { fill: "url(#cage)", rx: 0.4 });
  s += rect(x + 0.9, y + 0.9, w - 1.8, h - 1.8, { fill: "#141618", rx: 0.3 });
  // latch tab
  s += up ? rect(x + w / 2 - 1.6, y + 0.9, 3.2, 1, { fill: "#6c7279" }) : rect(x + w / 2 - 1.6, y + h - 1.9, 3.2, 1, { fill: "#6c7279" });
  return s;
}

/** QSFP/QSFP+ cage opening. */
export function qsfp(x, y, { w = 18.8, h = 9.6, up = true } = {}) {
  let s = rect(x, y, w, h, { fill: "url(#cage)", rx: 0.5 });
  s += rect(x + 1, y + 1, w - 2, h - 2, { fill: "#141618", rx: 0.4 });
  s += up ? rect(x + 2, y + 1, w - 4, 0.9, { fill: "#5f656c" }) : rect(x + 2, y + h - 1.9, w - 4, 0.9, { fill: "#5f656c" });
  return s;
}

/** D-sub connector (VGA: 15 pins in 3 rows; DB-9: 9 pins in 2 rows). */
export function dsub(x, y, kind = "vga", { colour } = {}) {
  const shellW = kind === "vga" ? 16.9 : 16.9;
  const w = 31;
  const h = 12.5;
  let s = rect(x, y, w, h, { fill: "#a5abb1", rx: 1.5, stroke: "#7c8288", "stroke-width": 0.3 });
  s += circle(x + 3.2, y + h / 2, 1.5, { fill: "#2f3236" }) + circle(x + w - 3.2, y + h / 2, 1.5, { fill: "#2f3236" });
  const sx = x + (w - shellW) / 2;
  const sh = 8.4;
  const sy = y + (h - sh) / 2;
  const inset = 1.5;
  s += path(`M${r(sx)} ${r(sy)}H${r(sx + shellW)}L${r(sx + shellW - inset)} ${r(sy + sh)}H${r(sx + inset)}Z`, {
    fill: colour ?? (kind === "vga" ? "#2c4f7c" : "#3d4045"),
    stroke: "#25282c",
    "stroke-width": 0.3,
  });
  const rows = kind === "vga" ? [5, 5, 5] : [5, 4];
  rows.forEach((n, ri) => {
    const py = sy + 1.6 + ri * ((sh - 3.2) / (rows.length - 1));
    const span = shellW - 5 - ri * 1;
    for (let i = 0; i < n; i++) s += circle(sx + (shellW - span) / 2 + (span * i) / (n - 1), py, 0.45, { fill: "#d6d9dc" });
  });
  return s;
}

/** IEC 60320 C14 inlet (as on a power supply). */
export function c14(x, y, { w = 13, h = 10 } = {}) {
  const ch = 2.4;
  let s = rect(x - 0.8, y - 0.8, w + 1.6, h + 1.6, { fill: "#1d1f22", rx: 1 });
  s += path(
    `M${r(x + ch)} ${r(y)}H${r(x + w - ch)}L${r(x + w)} ${r(y + ch)}V${r(y + h)}H${r(x)}V${r(y + ch)}Z`,
    { fill: "#0f1012", stroke: "#3a3d42", "stroke-width": 0.3 },
  );
  for (const px of [x + w * 0.25, x + w * 0.5, x + w * 0.75]) {
    const py = px === x + w * 0.5 ? y + h * 0.28 : y + h * 0.6;
    s += rect(px - 0.5, py - 1.3, 1, 2.6, { fill: "#b8bcc1" });
  }
  return s;
}

/** A label/pull tab. */
export const pullTab = (x, y, w, h) =>
  rect(x, y, w, h, { fill: "#d6d9dd", rx: 0.4, stroke: "#9da3a9", "stroke-width": 0.3 }) +
  rect(x + 0.8, y + h * 0.3, w - 1.6, 0.35, { fill: "#a9aeb4" }) +
  rect(x + 0.8, y + h * 0.55, w * 0.6, 0.35, { fill: "#a9aeb4" });

/**
 * A 2.5-inch hot-plug drive carrier. horizontal: lying (1U servers);
 * otherwise standing (2U servers). num: the bay number printed beside it.
 */
export function sffCarrier(x, y, w, h, { horizontal = true, num } = {}) {
  let s = rect(x, y, w, h, { fill: horizontal ? "url(#carrier)" : "url(#carrierv)", rx: 0.7, stroke: "#2b2e32", "stroke-width": 0.3 });
  if (horizontal) {
    // Release lever along the bottom, status LED and button at the right.
    s += rect(x + 2, y + h - 4.6, w * 0.62, 3.2, { fill: "url(#handle)", rx: 1.2 });
    s += vent(x + 2, y + 1.6, w * 0.62, h - 7.4, true);
    s += circle(x + w - 6.5, y + h / 2 - 1.4, 2.6, { fill: "#2f3337", stroke: "#3fae55", "stroke-width": 0.5 });
    s += circle(x + w - 6.5, y + h / 2 - 1.4, 1.2, { fill: "#5f656c" });
    s += rect(x + w - 2.6, y + 1.6, 1.2, h - 3.2, { fill: "#2a2d31", rx: 0.4 });
  } else {
    s += circle(x + w / 2, y + 5, Math.min(2.6, w / 2 - 1.6), { fill: "#2f3337", stroke: "#3fae55", "stroke-width": 0.5 });
    s += circle(x + w / 2, y + 5, Math.min(1.2, w / 4), { fill: "#5f656c" });
    s += vent(x + 1.6, y + 9.5, w - 3.2, h * 0.42, true);
    s += rect(x + 1.6, y + h * 0.58, w - 3.2, h * 0.38, { fill: "url(#handle)", rx: 1 });
    s += rect(x + w / 2 - 1.4, y + h - 4.4, 2.8, 2.4, { fill: "#a54b4b", rx: 0.4 });
  }
  if (num !== undefined) {
    if (horizontal) s += text(x + w - 6.5, y + h - 1.2, String(num), { size: 1.9, anchor: "middle", fill: "#d4d7da" });
    else s += text(x + w / 2, y - 0.8, String(num), { size: 1.9, anchor: "middle", fill: "#33363b" });
  }
  return s;
}

/** A 3.5-inch drive carrier (lying), hot-plug or the bezel of a cabled bay. */
export function lffCarrier(x, y, w, h, { hotplug = true, num } = {}) {
  let s = rect(x, y, w, h, { fill: hotplug ? "url(#carrier)" : "#a9aeb4", rx: 0.8, stroke: "#2b2e32", "stroke-width": 0.3 });
  if (hotplug) {
    s += vent(x + 2.2, y + 2, w * 0.55, h - 4, true);
    s += rect(x + w * 0.55 + 4, y + h - 6.2, w * 0.45 - 12, 4.2, { fill: "url(#handle)", rx: 1.4 });
    s += circle(x + w - 5.6, y + 4.4, 1.9, { fill: "#2f3337", stroke: "#3fae55", "stroke-width": 0.45 });
    s += rect(x + w * 0.55 + 4, y + 2.4, 10, 1.2, { fill: "#2a2d31" });
  } else {
    s += rect(x + 3, y + h / 2 - 0.4, w - 6, 0.8, { fill: "#8c9298" });
  }
  if (num !== undefined) s += text(x + 2.4, y + h - 1.4, String(num), { size: 1.9, fill: hotplug ? "#d4d7da" : "#3b3f45" });
  return s;
}

/** A power supply module seen from the rear. */
export function psu(x, y, w, h, { handle = "#6b7178", fan = true, label } = {}) {
  let s = rect(x, y, w, h, { fill: "url(#module)", rx: 0.8, stroke: "#3b3f44", "stroke-width": 0.35 });
  const inletW = Math.min(13, w * 0.3);
  s += c14(x + 2.6, y + h / 2 - 5, { w: inletW, h: 10 });
  if (fan) {
    const fr = Math.min(h / 2 - 2.4, w * 0.2);
    const fx = x + 2.6 + inletW + 4 + fr;
    s += circle(fx, y + h / 2, fr, { fill: "url(#hexdark)", stroke: "#3b3f44", "stroke-width": 0.4 });
    s += circle(fx, y + h / 2, fr * 0.28, { fill: "#4a4f55" });
  }
  // pull handle on the right, latch, status LED
  s += rect(x + w - 6, y + 2.2, 3.6, h - 4.4, { fill: handle, rx: 1.4, stroke: "#2c2f33", "stroke-width": 0.3 });
  s += led(x + 2.6 + inletW / 2, y + h / 2 - 7.6, "green", 0.6);
  if (label) s += text(x + w - 7.4, y + h - 2.2, label, { size: 1.9, anchor: "end", fill: "#1f2226" });
  return s;
}

/** A fan module seen from the rear: a grille and a pull handle. */
export function fanModule(x, y, w, h, { handle = "#6b7178", label } = {}) {
  let s = rect(x, y, w, h, { fill: "url(#module)", rx: 0.8, stroke: "#3b3f44", "stroke-width": 0.35 });
  const fr = Math.min(h / 2 - 1.8, (w - 9) / 2);
  const fx = x + 1.8 + fr + 1;
  s += rect(fx - fr, y + h / 2 - fr, 2 * fr, 2 * fr, { fill: "url(#hexdark)", rx: 1 });
  s += rect(x + w - 5.4, y + 2, 3.4, h - 4, { fill: handle, rx: 1.3, stroke: "#2c2f33", "stroke-width": 0.3 });
  if (label) s += text(x + 1.4, y + h - 1.2, label, { size: 1.6, fill: "#1f2226" });
  return s;
}

/** A PCIe slot cover (bracket), with the screw tab at the top. */
export function pcie(x, y, w, h, { label } = {}) {
  let s = rect(x, y, w, h, { fill: "#b9bdc2", rx: 0.4, stroke: "#858b91", "stroke-width": 0.3 });
  s += rect(x + 2.5, y + h * 0.25, w - 5, h * 0.5, { fill: "url(#slots)" });
  s += circle(x + 1.6, y + h / 2, 0.8, { fill: "#6f757c" });
  if (label) s += text(x + w - 1.2, y + h - 0.8, label, { size: 1.7, anchor: "end", fill: "#3b3f45" });
  return s;
}
