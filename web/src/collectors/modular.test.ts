import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

// The point of ADR 0055: a collector added to the server appears in these
// pages from its descriptor alone. Naming one in the code that renders them
// would be the first step towards a page per collector, so this fails on it.
const KINDS = ["arista", "kubernetes", "maas", "mikrotik", "pfsense", "redfish", "unifi"];

const here = import.meta.dirname;
const files = [
  ...readdirSync(here)
    .filter((f) => /\.tsx?$/.test(f) && !f.includes(".test."))
    .map((f) => resolve(here, f)),
  resolve(here, "../pages/HealthPage.tsx"),
];

describe("the collectors pages name no collector", () => {
  it("covers the pages and the status helper", () => {
    const names = files.map((f) => f.split("/").pop());
    expect(names).toEqual(expect.arrayContaining(["CollectorsPage.tsx", "CollectorPage.tsx", "CollectorInstancePage.tsx", "status.ts", "HealthPage.tsx"]));
  });

  it.each(files.map((f) => [f.split("/").pop(), f]))("%s", (_name, file) => {
    const text = readFileSync(file as string, "utf8").toLowerCase();
    for (const kind of KINDS) expect(text, `${_name} mentions ${kind}`).not.toContain(kind);
  });
});
