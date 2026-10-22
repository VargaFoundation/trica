import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const css = readFileSync(resolve(import.meta.dirname, "tokens.css"), "utf8");

/** The declarations of the first block whose selector contains `selector`. */
function block(selector: string): Record<string, string> {
  const start = css.indexOf(selector);
  if (start < 0) throw new Error(`no block for ${selector}`);
  const open = css.indexOf("{", start);
  const close = css.indexOf("}", open);
  const out: Record<string, string> = {};
  for (const m of css.slice(open + 1, close).matchAll(/--([\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) out[m[1] as string] = (m[2] as string).toLowerCase();
  return out;
}

// Dark is the default (the bare :root block); light is explicit, or the system preference.
const dark = block(":root {");
const light = block(':root[data-theme="light"]');
const system = block(':root:not([data-theme="light"])');

function luminance(hex: string): number {
  const ch = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = ch.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

// [foreground, background, minimum ratio]: 4.5 for text (WCAG 1.4.3), 3 for
// interface components and focus indicators (WCAG 1.4.11).
const PAIRS: [string, string, number][] = [
  ["trica-ink", "trica-surface", 4.5],
  ["trica-ink", "trica-raised", 4.5],
  ["trica-ink", "trica-sidebar", 4.5],
  ["trica-ink", "trica-surface-muted", 4.5],
  ["trica-ink-muted", "trica-surface", 4.5],
  ["trica-ink-muted", "trica-raised", 4.5],
  ["trica-ink-muted", "trica-sidebar", 4.5],
  ["trica-ink-muted", "trica-surface-muted", 4.5],
  ["trica-link", "trica-surface", 4.5],
  ["trica-link", "trica-raised", 4.5],
  ["trica-link", "trica-surface-muted", 4.5],
  ["trica-accent-ink", "trica-accent", 4.5],
  ["trica-accent-ink", "trica-accent-hover", 4.5],
  ["trica-link", "trica-accent-soft", 4.5],
  ["trica-ink", "trica-accent-soft", 4.5],
  // A pill's text on its own tinted ground.
  ...(["ok", "warn", "danger", "info", "declared", "neutral"] as const).map((t): [string, string, number] => [`trica-${t}-ink`, `trica-${t}-soft`, 4.5]),
  // Status text without a ground, on a card.
  ...(["ok", "warn", "danger", "info", "declared", "neutral"] as const).map((t): [string, string, number] => [`trica-${t}-ink`, `trica-raised`, 4.5]),
  // Interface parts: a control's border, the focus ring, a status dot.
  ["trica-line-strong", "trica-surface", 3],
  ["trica-line-strong", "trica-raised", 3],
  ["trica-focus", "trica-surface", 3],
  ["trica-focus", "trica-raised", 3],
  ["trica-focus", "trica-sidebar", 3],
  // The red row of a failing source carries ordinary text.
  ["trica-ink", "trica-danger-soft", 4.5],
  ["trica-ink-muted", "trica-danger-soft", 4.5],
  ["trica-link", "trica-danger-soft", 4.5],
  // The topology canvas: labels on nodes and on the paper, lines on the paper.
  ["trica-graph-ink", "trica-graph-node", 4.5],
  ["trica-graph-ink", "trica-graph-unresolved", 4.5],
  ["trica-graph-ink", "trica-graph-paper", 4.5],
  ["trica-graph-node-line", "trica-graph-paper", 3],
  ["trica-graph-unresolved-line", "trica-graph-paper", 3],
  ["trica-graph-declared", "trica-graph-paper", 3],
  ["trica-graph-observed", "trica-graph-paper", 3],
  ["trica-graph-deduced", "trica-graph-paper", 3],
  // The live overlay: lines and device borders on the paper, the MLAG box's name on its ground.
  ...(["util-normal", "util-warn", "util-crit", "down", "util-unknown", "health-ok", "health-warn", "health-crit", "group-line"] as const).map(
    (k): [string, string, number] => [`trica-graph-${k}`, "trica-graph-paper", 3],
  ),
  ["trica-graph-ink", "trica-graph-group", 4.5],
  ["trica-graph-health-ok", "trica-graph-node", 3],
  ["trica-graph-health-warn", "trica-graph-node", 3],
  ["trica-graph-health-crit", "trica-graph-node", 3],
  // The isometric map: labels on the floor, on the halo behind them and on every shaded face.
  ...(["floor", "label-bg", "top", "south", "east", "neutral-top", "neutral-south", "neutral-east"] as const).map(
    (k): [string, string, number] => ["trica-graph-ink", `trica-iso-${k}`, 4.5],
  ),
  // Lines and health outlines on the floor, health outlines on a raised top face.
  ...(["util-normal", "util-warn", "util-crit", "down", "util-unknown", "health-ok", "health-warn", "health-crit"] as const).map(
    (k): [string, string, number] => [`trica-graph-${k}`, "trica-iso-floor", 3],
  ),
  ...(["health-ok", "health-warn", "health-crit"] as const).map((k): [string, string, number] => [`trica-graph-${k}`, "trica-iso-top", 3]),
  ["trica-iso-edge", "trica-iso-floor", 3],
  ["trica-iso-neutral-edge", "trica-iso-floor", 3],
  // The small caption under a label is drawn in the muted ink, on the label halo or the floor.
  ["trica-ink-muted", "trica-iso-label-bg", 4.5],
  ["trica-ink-muted", "trica-iso-floor", 4.5],
];

describe.each([
  ["light", light],
  ["dark", dark],
])("contrast in the %s theme", (_name, tokens) => {
  it.each(PAIRS)("%s on %s is at least %d:1", (fg, bg, min) => {
    const f = tokens[fg];
    const b = tokens[bg];
    expect(f, `--${fg} is missing`).toBeDefined();
    expect(b, `--${bg} is missing`).toBeDefined();
    expect(contrast(f as string, b as string), `${fg} ${f} on ${bg} ${b}`).toBeGreaterThanOrEqual(min);
  });
});

describe("theme blocks", () => {
  it("define the same tokens in light and dark", () => {
    expect(Object.keys(dark).sort()).toEqual(Object.keys(light).sort());
  });
  it("the system-preference block is the explicit light block", () => {
    expect(system).toEqual(light);
  });
  it("dark is the default: the bare :root block is dark", () => {
    expect(css.slice(0, css.indexOf("}"))).toMatch(/color-scheme:\s*dark/);
  });
  it("names every colour token --trica-*", () => {
    expect(Object.keys(dark).every((k) => k.startsWith("trica-"))).toBe(true);
    expect(Object.keys(dark).length).toBeGreaterThan(40);
  });
  it("the contrast function is right (white on black is 21:1)", () => {
    expect(contrast("#ffffff", "#000000")).toBeCloseTo(21, 5);
  });
});

describe("the stylesheets", () => {
  const app = readFileSync(resolve(import.meta.dirname, "app.css"), "utf8");
  const declared = new Set([...css.matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]));

  it("read only tokens that tokens.css declares", () => {
    const used = new Set([...app.matchAll(/var\((--[\w-]+)/g)].map((m) => m[1]));
    expect([...used].filter((v) => !declared.has(v))).toEqual([]);
  });

  it("app.css holds no literal colour: every colour is a token", () => {
    const withoutComments = app.replace(/\/\*[\s\S]*?\*\//g, "");
    expect(withoutComments.match(/#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/g)).toBeNull();
  });

  it("loads no font: the system stacks only (ADR 0018)", () => {
    expect(`${css}\n${app}`).not.toMatch(/@font-face|@import|url\(/);
  });
});
