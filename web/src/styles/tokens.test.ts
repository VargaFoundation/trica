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

// [foreground, background, minimum ratio] (names without the --trica- prefix): 4.5 for
// text (WCAG 1.4.3), 3 for interface components and focus indicators (WCAG 1.4.11). The
// list is shared with the Go validator of theme archives (ADR 0054), which refuses a
// theme whose palette fails the same pairs: one file, so the two cannot drift.
const PAIRS = (
  JSON.parse(readFileSync(resolve(import.meta.dirname, "../../../internal/theme/contrast.json"), "utf8")) as [string, string, number][]
).map(([fg, bg, min]): [string, string, number] => [`trica-${fg}`, `trica-${bg}`, min]);

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
  it("declares the tokens a theme restates (ADR 0054)", () => {
    for (const t of ["font-title", "font-title-weight", "logo", "logo-width", "brand-icon", "brand-ground", "signal-width"]) {
      expect(css, `--trica-${t}`).toMatch(new RegExp(`--trica-${t}:`));
    }
    expect(dark["trica-signal"]).toBeDefined();
  });
  it("the shared contrast list names only declared tokens, and is not trivially short", () => {
    expect(PAIRS.length).toBeGreaterThan(50);
    for (const [fg, bg] of PAIRS) {
      expect(dark[fg], fg).toBeDefined();
      expect(dark[bg], bg).toBeDefined();
    }
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
    expect(`${css}\n${app}`).not.toMatch(/@font-face|@import|url\((?!#)/);
  });
});
