import { screen, within } from "@testing-library/react";
import type { ObjectView, RelationLink } from "../api/types";
import { page, searchSw } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp } from "../test/support";
import { groupByEnvironment, type InstanceRow } from "./AppInstances";

const obj = (id: string, type: string, display: string, parent_id?: string, attributes: Record<string, unknown> = {}): ObjectView => ({
  id,
  type,
  display,
  status: "active",
  attributes,
  parent_id,
  created_at: "2026-10-01T08:00:00Z",
  last_seen: "2026-10-26T06:00:00Z",
  observed: true,
});

const APP = "00000000-0000-4000-8000-0000000000a1";
const COMP = "00000000-0000-4000-8000-0000000000c1";
const app = obj(APP, "application", "orion");
const comp = obj(COMP, "component", "app", APP);
const dev = obj("00000000-0000-4000-8000-0000000000d1", "component_instance", "orion/app/dev/orion", COMP, { version: "deploy@3572e14", health: "ok", sync_status: "Synced" });
const prod = obj("00000000-0000-4000-8000-0000000000d2", "component_instance", "orion/app/prod/orion", COMP, { version: "deploy@aaaaaaa", health: "critical", sync_status: "OutOfSync" });
const loose = obj("00000000-0000-4000-8000-0000000000d3", "component_instance", "orion/app/-/-", COMP, {});

const link = (id: number, from: ObjectView, env: string): RelationLink => ({
  id,
  type: "deployed_in",
  from_id: from.id,
  to_id: `env-${env}`,
  from: { id: from.id, type: from.type, display: from.display },
  to: { id: `env-${env}`, type: "environment", display: env },
  source: "kubernetes:build",
  confidence: "observed",
  valid_from: "2026-10-26T06:00:00Z",
});

function routes() {
  return {
    ...defaultRoutes(),
    [`GET /objects/${APP}`]: app,
    [`GET /objects/${APP}/relations`]: page([]),
    [`GET /objects/${APP}/timeline`]: page([]),
    [`GET /objects/${APP}/provenance`]: { object_id: APP, attributes: [] },
    [`GET /objects/${APP}/faces`]: { faces: [], parts: [], truncated: false },
    "GET /search": (c: { query: URLSearchParams }) => {
      const q = c.query.get("q") ?? "";
      const items = q === `parent:"${APP}"` ? [comp] : q === `parent:"${COMP}"` ? [dev, prod, loose] : [];
      return { ...searchSw, total: items.length, items };
    },
    "GET /relations": (c: { query: URLSearchParams }) =>
      c.query.get("type") === "deployed_in" ? page([link(1, dev, "dev"), link(2, prod, "prod")]) : page([]),
  };
}

describe("an application's instances by environment", () => {
  it("groups the instances under the environment they are deployed in, the unknown last", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${APP}` });
    const devTable = await screen.findByRole("table", { name: "Instances of orion in dev" });
    const row = within(devTable).getByRole("row", { name: /orion\/app\/dev\/orion/ });
    expect(within(row).getByText("deploy@3572e14")).toBeInTheDocument();
    expect(within(row).getByText("Synced")).toBeInTheDocument();
    const prodTable = screen.getByRole("table", { name: "Instances of orion in prod" });
    expect(within(prodTable).getAllByRole("row")[1]).toHaveAttribute("data-health", "critical");
    const unknown = screen.getByRole("table", { name: "Instances of orion in Environment unknown" });
    expect(within(unknown).getByRole("link", { name: "orion/app/-/-" })).toBeInTheDocument();
    const order = [...document.querySelectorAll("table[data-environment]")].map((t) => t.getAttribute("data-environment"));
    expect(order).toEqual(["dev", "prod", ""]);
  });

  it("says so when the application has no instance", async () => {
    mockApi({ ...routes(), "GET /search": { ...searchSw, total: 0, items: [] } });
    renderApp({ at: `/objects/${APP}` });
    expect(await screen.findByText("No instance of this application is recorded yet.")).toBeInTheDocument();
  });

  it("is not shown on other objects", async () => {
    mockApi({
      ...routes(),
      [`GET /objects/${COMP}`]: comp,
      [`GET /objects/${COMP}/relations`]: page([]),
      [`GET /objects/${COMP}/timeline`]: page([]),
      [`GET /objects/${COMP}/provenance`]: { object_id: COMP, attributes: [] },
      [`GET /objects/${COMP}/faces`]: { faces: [], parts: [], truncated: false },
    });
    renderApp({ at: `/objects/${COMP}` });
    expect(await screen.findByRole("heading", { level: 1, name: "app" })).toBeInTheDocument();
    expect(screen.queryByText("Instances by environment")).not.toBeInTheDocument();
  });
});

describe("groupByEnvironment", () => {
  it("sorts the environments, then the components, and puts the unknown last", () => {
    const rows: InstanceRow[] = [
      { instance: dev, component: comp },
      { instance: prod, component: comp },
      { instance: loose, component: comp },
    ];
    const g = groupByEnvironment(rows, new Map([[prod.id, "a-first"], [dev.id, "b-second"]]));
    expect(g.map(([e]) => e)).toEqual(["a-first", "b-second", ""]);
  });
});
