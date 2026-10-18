import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Icon } from "../components/ui/icons";
import { kindOf, KNOWN_ROLES } from "../inventory/kind";
import { TOPOLOGY_ICONS, iconUrl } from "./icons";

const css = readFileSync(resolve(import.meta.dirname, "../styles/tokens.css"), "utf8");
const ink = (selector: string) => {
  const start = css.indexOf(selector);
  const block = css.slice(start, css.indexOf("}", start));
  const m = /--trica-graph-ink:\s*(#[0-9a-f]{6})/i.exec(block);
  if (!m) throw new Error(`no --trica-graph-ink in ${selector}`);
  return m[1] as string;
};
const INK = { dark: ink(":root {"), light: ink(':root[data-theme="light"]') };
const PUBLIC = resolve(import.meta.dirname, "../../public/topology-icons");

/** The file for an icon: the application's own paths, stroked as .icon is in app.css. */
function iconFile(icon: string, color: string): string {
  const inner = renderToStaticMarkup(createElement(Icon, { name: icon as never }))
    .replace(/^<svg[^>]*>/, "")
    .replace(/<\/svg>$/, "");
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="${color}" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">${inner}</svg>\n`;
}

describe("the canvas icons", () => {
  it("cover every device role", () => {
    for (const role of KNOWN_ROLES) expect(TOPOLOGY_ICONS).toContain(kindOf({ type: "device", attributes: { role } }).icon);
  });

  it.each(["dark", "light"] as const)("are files of the %s theme, drawn from the application's icons in the graph's ink", (theme) => {
    for (const icon of TOPOLOGY_ICONS) {
      const path = resolve(PUBLIC, theme, `${icon}.svg`);
      const want = iconFile(icon, INK[theme]);
      if (process.env.TRICA_UPDATE_ICONS) {
        mkdirSync(resolve(PUBLIC, theme), { recursive: true });
        writeFileSync(path, want);
      }
      expect(existsSync(path), `${path} is missing: run TRICA_UPDATE_ICONS=1 npx vitest run src/topology/icons.test.ts`).toBe(true);
      expect(readFileSync(path, "utf8"), `${theme}/${icon}.svg drifted`).toBe(want);
    }
  });

  it("are served from the application, never as data: URLs", () => {
    expect(iconUrl("dark", "server")).toBe("/topology-icons/dark/server.svg");
  });
});
