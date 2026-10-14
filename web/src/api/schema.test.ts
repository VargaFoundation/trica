import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import openapiTS, { astToString, COMMENT_HEADER } from "openapi-typescript";
import { api } from "./endpoints";
import { mockApi } from "../test/support";

const snapshot = resolve(import.meta.dirname, "../../openapi.json");
const generated = resolve(import.meta.dirname, "schema.gen.ts");

interface Doc {
  paths: Record<string, Record<string, { parameters?: { name: string; in: string }[] }>>;
}
const doc = JSON.parse(readFileSync(snapshot, "utf8")) as Doc;

describe("the generated types follow the committed OpenAPI snapshot", () => {
  it("schema.gen.ts is exactly what openapi-typescript makes of openapi.json", async () => {
    const out = COMMENT_HEADER + astToString(await openapiTS(pathToFileURL(snapshot)));
    // Failing here means openapi.json changed without `npm run gen:api`.
    expect(readFileSync(generated, "utf8")).toBe(out);
  });
});

/** Turns "/objects/abc/relations" into the template "/objects/{id}/relations" the document uses. */
function templateOf(path: string): string | undefined {
  return Object.keys(doc.paths).find((tpl) => new RegExp(`^${tpl.replace(/\{[^}]+\}/g, "[^/]+")}$`).test(path));
}

describe("every call the client makes exists in the document", () => {
  it("uses declared paths, methods and query parameters", async () => {
    const mock = mockApi({
      "GET /me": {},
      "GET /search": {},
      "GET /objects": {},
      "GET /objects/o-1": {},
      "GET /objects/o-1/provenance": {},
      "GET /objects/o-1/relations": {},
      "GET /objects/o-1/timeline": {},
      "GET /proposals": {},
      "GET /proposals/3": {},
      "POST /proposals/3/approve": {},
      "POST /proposals/3/reject": {},
      "GET /discrepancies": {},
      "GET /health/collection": {},
      "GET /schema": {},
    });
    await api.me();
    await api.search({ q: "sw", limit: 100, offset: 0 });
    await api.objects({ q: "sw", type: "device", sort: "display", limit: 50, offset: 50 });
    await api.object("o-1");
    await api.provenance("o-1");
    await api.relations("o-1", { limit: 200 });
    await api.timeline("o-1", { limit: 50, offset: 50 });
    await api.proposals({ status: "pending", limit: 50, offset: 0 });
    await api.proposal(3);
    await api.approve(3);
    await api.reject(3);
    await api.discrepancies({ status: "open", kind: "value_mismatch", limit: 50, offset: 0 });
    await api.health();
    await api.schema();

    expect(mock.calls).toHaveLength(14);
    for (const c of mock.calls) {
      const tpl = templateOf(c.path);
      expect(tpl, `${c.method} ${c.path} is not a path of openapi.json`).toBeDefined();
      const op = doc.paths[tpl as string]?.[c.method.toLowerCase()];
      expect(op, `${c.method} ${tpl} is not an operation of openapi.json`).toBeDefined();
      const declared = new Set((op?.parameters ?? []).filter((p) => p.in === "query").map((p) => p.name));
      for (const name of c.query.keys()) {
        expect(declared.has(name), `${c.method} ${tpl}: query parameter ${name} is not declared`).toBe(true);
      }
    }
  });
});
