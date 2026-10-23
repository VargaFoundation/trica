import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// The map must open without loading the graph engine: the Network view is the
// only importer of cytoscape, and it is a lazy chunk.
describe("the map chunk", () => {
  const files = readdirSync(__dirname).filter((f) => /\.(ts|tsx)$/.test(f) && !/\.test\./.test(f));

  it("sees its own sources", () => {
    expect(files).toContain("MapPage.tsx");
    expect(files.length).toBeGreaterThan(8);
  });

  it.each(files)("%s imports neither the graph module nor cytoscape", (f) => {
    const src = readFileSync(join(__dirname, f), "utf8");
    const imports = [...src.matchAll(/(?:from|import)\s*\(?\s*["']([^"']+)["']/g)].map((m) => m[1] ?? "");
    expect(imports.filter((i) => /(^|\/)graph(\.ts)?$/.test(i) || /cytoscape/.test(i))).toEqual([]);
  });
});
