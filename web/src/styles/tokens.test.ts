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

const light = block(":root {");
const dark = block(':root[data-theme="dark"]');
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
  ["text", "bg", 4.5],
  ["text", "surface", 4.5],
  ["muted", "bg", 4.5],
  ["muted", "surface", 4.5],
  ["link", "bg", 4.5],
  ["link", "surface", 4.5],
  ["primary-fg", "primary-bg", 4.5],
  ["danger-fg", "danger-bg", 4.5],
  ["button-fg", "button-bg", 4.5],
  ["notice-fg", "notice-bg", 4.5],
  ["error-fg", "error-bg", 4.5],
  ["green-fg", "green-bg", 4.5],
  ["red-fg", "red-bg", 4.5],
  ["amber-fg", "amber-bg", 4.5],
  ["blue-fg", "blue-bg", 4.5],
  ["gray-fg", "gray-bg", 4.5],
  ["input-border", "bg", 3],
  ["focus", "bg", 3],
  ["focus", "surface", 3],
  // The red row of a failing source carries ordinary text.
  ["text", "red-bg", 4.5],
  ["muted", "red-bg", 4.5],
  ["link", "red-bg", 4.5],
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
  it("the system-preference block is the explicit dark block", () => {
    expect(system).toEqual(dark);
  });
  it("the contrast function is right (white on black is 21:1)", () => {
    expect(contrast("#ffffff", "#000000")).toBeCloseTo(21, 5);
  });
});
