// Generic device faces, for models the device-type library has no image of.
// Pure geometry: a list of primitives in the slot's own coordinates, each
// tagged with a class, so that the screen (SVG, styled by the theme) and the
// export sheet (canvas, fixed palette) draw the same face. Each role reads
// differently at a glance: drive bays for a server, rows of ports for a
// switch, outlets for a PDU, keystone jacks for a patch panel.

export type FaceKind = "server" | "switch" | "router" | "firewall" | "storage" | "pdu" | "ups" | "patch_panel" | "kvm" | "other";

/** plate: the chassis; bay: a drive bay or a module; port: a socket; led: a status light; vent: grille; accent: a display. */
export type PrimClass = "plate" | "bay" | "port" | "led" | "vent" | "accent";

export type Prim =
  | { k: "rect"; x: number; y: number; w: number; h: number; c: PrimClass; r?: number }
  | { k: "circle"; x: number; y: number; r: number; c: PrimClass }
  | { k: "line"; x1: number; y1: number; x2: number; y2: number; c: PrimClass };

const KINDS: Record<string, FaceKind> = {
  server: "server",
  switch: "switch",
  router: "router",
  firewall: "firewall",
  storage: "storage",
  nas: "storage",
  pdu: "pdu",
  ups: "ups",
  patch_panel: "patch_panel",
  kvm: "kvm",
};

/** The face a role is drawn as; an unknown or missing role is a blank plate. */
export const faceKind = (role: string | undefined): FaceKind => (role ? (KINDS[role] ?? "other") : "other");

/** The left edges of n items of width w spread evenly over [x0, x1] (one item is centred). */
function spread(x0: number, x1: number, n: number, w: number): number[] {
  if (n <= 0) return [];
  const step = (x1 - x0 - w) / Math.max(n - 1, 1);
  return Array.from({ length: n }, (_, i) => (n === 1 ? (x0 + x1 - w) / 2 : x0 + i * step));
}

function vents(out: Prim[], x0: number, x1: number, y0: number, y1: number, step = 3): void {
  for (let x = x0; x <= x1; x += step) out.push({ k: "line", x1: x, y1: y0, x2: x, y2: y1, c: "vent" });
}

/** Rows of square ports between x0 and x1, the usual top-and-bottom pairs of a switch. */
function portRows(out: Prim[], x0: number, x1: number, h: number, perRow: number, rows: number): void {
  const size = Math.min(5, (h - 4) / rows - 1.5);
  const groups = Math.max(1, Math.round(perRow / 12));
  const groupW = (x1 - x0 - (groups - 1) * 4) / groups;
  const perGroup = Math.ceil(perRow / groups);
  for (let g = 0; g < groups; g++) {
    const gx = x0 + g * (groupW + 4);
    const xs = spread(gx, gx + groupW, perGroup, size);
    for (let r = 0; r < rows; r++) {
      const y = 2 + r * ((h - 4) / rows) + ((h - 4) / rows - size) / 2;
      for (const x of xs) out.push({ k: "rect", x, y, w: size, h: size, c: "port" });
    }
  }
}

/** The primitives of a face of width w and height h (both in SVG units, the slot's own box). */
export function genericFace(kind: FaceKind, w: number, h: number): Prim[] {
  const out: Prim[] = [{ k: "rect", x: 0, y: 0, w, h, c: "plate", r: 1.5 }];
  const units = Math.max(1, Math.round(h / 22));
  const ear = 10; // rack ears and handles
  switch (kind) {
    case "server": {
      // Bezel vents on the left, a row of drive bays per U, the control panel on the right.
      vents(out, 3, ear + 26, 3, h - 3);
      const rows = units >= 2 ? 2 * Math.min(units, 2) - 1 : 1;
      const bayH = (h - 4) / rows - 1.5;
      const bays = units >= 2 ? 8 : 10;
      const xs = spread(ear + 32, w - ear - 26, bays, 15);
      for (let r = 0; r < rows; r++) {
        const y = 2 + r * ((h - 4) / rows) + 0.75;
        for (const x of xs) {
          out.push({ k: "rect", x, y, w: 15, h: bayH, c: "bay", r: 1 });
          out.push({ k: "circle", x: x + 12.5, y: y + bayH / 2, r: 0.9, c: "led" });
        }
      }
      out.push({ k: "circle", x: w - ear - 12, y: h / 2, r: 2.2, c: "led" });
      out.push({ k: "rect", x: w - ear - 8, y: h / 2 - 3, w: 5, h: 6, c: "port" });
      break;
    }
    case "switch":
    case "router": {
      // Two rows of ports, uplinks apart on the right, console and management at the left.
      out.push({ k: "rect", x: ear, y: h / 2 - 3, w: 6, h: 6, c: "port" });
      out.push({ k: "circle", x: ear + 11, y: h / 2, r: 1.2, c: "led" });
      const right = w - ear - 34;
      portRows(out, ear + 17, right, h, kind === "router" ? 8 : 24, Math.min(2 * units, 4));
      for (const x of spread(right + 4, w - ear, 2, 12)) out.push({ k: "rect", x, y: h / 2 - 4, w: 12, h: 8, c: "bay", r: 1 });
      break;
    }
    case "firewall": {
      // A handful of ports, an accent band, the status lights.
      out.push({ k: "rect", x: ear, y: 3, w: 46, h: h - 6, c: "accent", r: 1 });
      for (const x of spread(ear + 54, ear + 54 + 8 * 9, 8, 6)) out.push({ k: "rect", x, y: h / 2 - 3, w: 6, h: 6, c: "port" });
      for (const x of spread(w - ear - 30, w - ear - 4, 3, 2)) out.push({ k: "circle", x, y: h / 2, r: 1.3, c: "led" });
      vents(out, w - ear - 70, w - ear - 38, 4, h - 4);
      break;
    }
    case "storage": {
      // Drive carriers across the whole width, a light on each.
      const per = 12;
      const rows = units;
      const xs = spread(ear, w - ear, per, (w - 2 * ear) / per - 2);
      const bw = (w - 2 * ear) / per - 2;
      for (let r = 0; r < rows; r++) {
        const y = 2 + r * ((h - 4) / rows);
        const bh = (h - 4) / rows - 1.5;
        for (const x of xs) {
          out.push({ k: "rect", x, y, w: bw, h: bh, c: "bay", r: 1 });
          out.push({ k: "circle", x: x + bw / 2, y: y + bh - 2.2, r: 0.9, c: "led" });
        }
      }
      break;
    }
    case "pdu": {
      // Outlets in a row, a meter on the left.
      out.push({ k: "rect", x: ear, y: 3, w: 18, h: h - 6, c: "accent", r: 1 });
      for (const x of spread(ear + 26, w - ear, 12, 12)) out.push({ k: "rect", x, y: h / 2 - 4.5, w: 12, h: 9, c: "port", r: 2 });
      break;
    }
    case "ups": {
      // A display, buttons, a large grille.
      out.push({ k: "rect", x: ear + 6, y: Math.min(4, h / 4), w: 34, h: Math.min(12, h / 2), c: "accent", r: 1 });
      for (const x of spread(ear + 46, ear + 70, 3, 4)) out.push({ k: "circle", x: x + 2, y: h / 2, r: 2, c: "port" });
      vents(out, ear + 84, w - ear - 6, 3, h - 3);
      out.push({ k: "circle", x: w - ear - 2, y: h / 2, r: 1.5, c: "led" });
      break;
    }
    case "patch_panel": {
      // Keystone jacks in groups of six, one row per U.
      for (let r = 0; r < units; r++) {
        const y = 2 + r * ((h - 4) / units) + ((h - 4) / units - 6) / 2;
        for (let g = 0; g < 4; g++) {
          const gx0 = ear + 6 + g * ((w - 2 * ear - 12) / 4);
          for (const x of spread(gx0 + 2, gx0 + (w - 2 * ear - 12) / 4 - 6, 6, 6)) out.push({ k: "rect", x, y, w: 6, h: 6, c: "port" });
        }
      }
      break;
    }
    case "kvm": {
      out.push({ k: "rect", x: ear, y: 3, w: 40, h: h - 6, c: "accent", r: 1 });
      for (const x of spread(ear + 50, w - ear - 10, 16, 5)) out.push({ k: "rect", x, y: h / 2 - 2.5, w: 5, h: 5, c: "port" });
      out.push({ k: "circle", x: w - ear - 3, y: h / 2, r: 1.3, c: "led" });
      break;
    }
    case "other":
      // A blank plate with a grille at each end: a device nobody said the role of.
      vents(out, ear, ear + 24, 4, h - 4);
      vents(out, w - ear - 24, w - ear, 4, h - 4);
      break;
  }
  return out;
}
