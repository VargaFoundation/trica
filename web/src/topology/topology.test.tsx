import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { RelationLink } from "../api/types";
import { expectNoA11yViolations } from "../test/axe";
import { page, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp, type Call, type Handler } from "../test/support";
import { A1E1, A1E2, CABLE1, DEVICES, T, end, rel, sampleContext, sampleLinks } from "./fixtures";
import { graphEnv } from "./graph";
import { MAX_PAGES, readAll } from "./useTopology";

beforeEach(() => {
  graphEnv.headless = true;
  graphEnv.layout = "preset";
});
afterEach(() => {
  graphEnv.headless = false;
  graphEnv.layout = "elk";
});

const obj = (id: string, type: string, display: string, extra: Record<string, unknown> = {}) => ({
  id,
  type,
  display,
  status: "active",
  attributes: {},
  created_at: "2026-03-01T08:00:00Z",
  observed: true,
  ...extra,
});

interface Setup {
  links?: RelationLink[];
  context?: RelationLink[];
  truncated?: boolean;
  /** The VLAN objects "GET /objects?type=vlan" returns. */
  vlans?: unknown[];
}

function routes({ links = sampleLinks(), context = sampleContext(), truncated, vlans = [] }: Setup = {}): Record<string, Handler> {
  return {
    ...defaultRoutes(READER),
    "GET /relations": (c: Call) => {
      const types = (c.query.get("type") ?? "").split(",");
      const mine = types.includes("cabled_to") ? links : context;
      return { ...page(mine, { limit: 1000 }), truncated };
    },
    "GET /objects": (c: Call) => {
      switch (c.query.get("type")) {
        case "device":
          return page(DEVICES.map((d) => obj(d.id, "device", d.name, { site_id: d.siteId, attributes: { role: d.role } })), { limit: 1000 });
        case "site":
          return page([obj(T.paris, "site", "paris-1"), obj(T.lyon, "site", "lyon-1")], { limit: 1000 });
        case "vlan":
          return page(vlans, { limit: 1000 });
        default:
          return page([], { limit: 1000 });
      }
    },
  };
}

async function open(setup: Setup = {}, at = "/topology") {
  const api = mockApi(routes(setup));
  renderApp({ at });
  await screen.findByRole("heading", { level: 1, name: "Topology" });
  await screen.findByRole("table");
  return api;
}

const tableRows = () => within(screen.getByRole("table")).getAllByRole("row").slice(1);

describe("the topology page", () => {
  it("lists every link, once, with how well it is known", async () => {
    await open();
    expect(tableRows()).toHaveLength(4);
    const classes = tableRows().map((r) => r.getAttribute("data-class")).sort();
    expect(classes).toEqual(["declared", "deduced", "deduced", "observed"]);
    expect(screen.getByText("4 links · 4 devices")).toBeDefined();
  });

  it("says the level and the evidence of a deduced link in words", async () => {
    await open();
    const high = tableRows().find((r) => within(r).queryByText("eno1"));
    expect(high && within(high).getByText("deduced, high · both ends see each other (LLDP)")).toBeDefined();
    const low = tableRows().find((r) => within(r).queryByText("sw-l1"));
    expect(low && within(low).getByText("deduced, low · forwarding table (MAC)")).toBeDefined();
  });

  it("shows nothing for the level of a declared or an observed link", async () => {
    await open();
    for (const r of tableRows().filter((r) => r.getAttribute("data-class") !== "deduced")) {
      expect(within(r).getAllByText("—").length).toBeGreaterThan(0);
      expect(r.textContent).not.toMatch(/deduced, /);
    }
  });

  it("links every device, port and cable to its page", async () => {
    await open();
    const cable = tableRows().find((r) => r.getAttribute("data-class") === "declared");
    if (!cable) throw new Error("no declared row");
    const hrefs = within(cable)
      .getAllByRole("link")
      .map((a) => a.getAttribute("href"));
    expect(hrefs).toContain(`/objects/${T.swA1}`);
    expect(hrefs).toContain(`/objects/${T.a1e1}`);
    expect(hrefs).toContain(`/objects/${T.swA2}`);
    expect(hrefs).toContain(`/objects/${T.cable1}`);
    expect(within(cable).getByText("C-0001")).toBeDefined();
  });

  it("opens a device from the table", async () => {
    await open();
    const user = userEvent.setup();
    const row = tableRows().find((r) => within(r).queryByText("eno1"));
    if (!row) throw new Error("no row");
    await user.click(within(row).getByRole("link", { name: "srv-01" }));
    expect(await screen.findByRole("heading", { level: 1 })).toBeDefined();
  });

  it("only reads: it never sends anything but GET", async () => {
    const api = await open();
    expect(api.calls.length).toBeGreaterThan(0);
    expect(api.calls.every((c) => c.method === "GET")).toBe(true);
    expect(api.unmatched.filter((u) => u.includes("/relations") || u.includes("/objects?"))).toEqual([]);
  });

  it("asks for the three kinds of link and the two kinds of context, and names no object one by one", async () => {
    const api = await open();
    const types = api.calls.filter((c) => c.path === "/relations").map((c) => c.query.get("type"));
    expect(types.sort()).toEqual(["cabled_to,connects,neighbor_of,uplinked_to", "mounted_in,member_of"]);
    // The table links carry their names: no request per device or port.
    expect(api.calls.filter((c) => /^\/objects\/[^/]+$/.test(c.path) && !c.path.endsWith("/me")).length).toBe(0);
  });

  it("describes the drawing for a screen reader and points to the table", async () => {
    await open();
    const fig = screen.getByRole("img");
    expect(fig.getAttribute("aria-label")).toBe("Topology: 4 devices, 4 links drawn. The table below lists the same links.");
    expect(fig.getAttribute("data-nodes")).toBe("4");
    expect(fig.getAttribute("data-edges")).toBe("4");
    expect(screen.getByText(/for keyboard and screen-reader use/)).toBeDefined();
  });

  it("has a legend naming the three kinds of line", async () => {
    await open();
    const legend = screen.getByRole("list", { name: "Legend" });
    expect(within(legend).getByText(/Declared: a documented cable \(solid line\)/)).toBeDefined();
    expect(within(legend).getByText(/Observed: .*\(dotted line\)/)).toBeDefined();
    expect(within(legend).getByText(/Deduced: .*\(dashed line\)/)).toBeDefined();
  });

  it("is in the main navigation", async () => {
    await open();
    const nav = screen.getByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Topology" }).getAttribute("href")).toBe("/topology");
  });
});

describe("filters", () => {
  it("narrows to a site, keeping a link that leaves it", async () => {
    await open();
    const user = userEvent.setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "Site" }), "lyon-1");
    await waitFor(() => expect(tableRows()).toHaveLength(1));
    expect(within(tableRows()[0] as HTMLElement).getByText("sw-l1")).toBeDefined();
    expect(screen.getByText("1 link · 2 devices")).toBeDefined();
  });

  it("narrows to a rack and to a VLAN", async () => {
    await open();
    const user = userEvent.setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "Rack" }), "lyon-1");
    await waitFor(() => expect(tableRows()).toHaveLength(1));
    await user.selectOptions(screen.getByRole("combobox", { name: "Rack" }), "");
    await user.selectOptions(screen.getByRole("combobox", { name: "VLAN" }), "VLAN 10");
    await waitFor(() => expect(tableRows()).toHaveLength(1));
    expect(within(tableRows()[0] as HTMLElement).getByText("eno1")).toBeDefined();
  });

  it("offers only what the links have: a site, a rack, a VLAN that no link touches is not listed", async () => {
    await open();
    const options = (name: string) => within(screen.getByRole("combobox", { name })).getAllByRole("option").map((o) => o.textContent);
    expect(options("Site")).toEqual(["All", "lyon-1", "paris-1"]);
    expect(options("Rack")).toEqual(["All", "lyon-1", "paris-1"]);
    expect(options("VLAN")).toEqual(["All", "VLAN 10", "VLAN 20"]);
  });

  it("tells two VLANs of the same VID apart in the filter", async () => {
    const twin = "p-vlan-10-lab";
    await open({
      context: [...sampleContext(), rel("member_of", A1E1, end(twin, "vlan", "VLAN 10"), { confidence: "declared" })],
      vlans: [
        obj(T.vlan10, "vlan", "VLAN 10", { attributes: { vid: 10, name: "MGMT", domain: "default" } }),
        obj(twin, "vlan", "VLAN 10", { attributes: { vid: 10, name: "MGMT", domain: "lab" } }),
        obj(T.vlan20, "vlan", "VLAN 20", { attributes: { vid: 20, name: "", domain: "default" } }),
      ],
    });
    const options = within(screen.getByRole("combobox", { name: "VLAN" })).getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["All", "VLAN 10 MGMT (default)", "VLAN 10 MGMT (lab)", "VLAN 20"]);
  });

  it("hides a class when its box is cleared, and does not let the last one be cleared", async () => {
    await open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("checkbox", { name: "Deduced" }));
    await waitFor(() => expect(tableRows()).toHaveLength(2));
    expect(tableRows().every((r) => r.getAttribute("data-class") !== "deduced")).toBe(true);
    await user.click(screen.getByRole("checkbox", { name: "Observed" }));
    await waitFor(() => expect(tableRows()).toHaveLength(1));
    await user.click(screen.getByRole("checkbox", { name: "Declared" }));
    expect(tableRows()).toHaveLength(1);
    expect((screen.getByRole("checkbox", { name: "Declared" }) as HTMLInputElement).checked).toBe(true);
  });

  it("is in the address, so a filtered view can be shared", async () => {
    await open({}, `/topology?site=${T.lyon}&classes=deduced`);
    expect(tableRows()).toHaveLength(1);
    expect((screen.getByRole("combobox", { name: "Site" }) as HTMLSelectElement).value).toBe(T.lyon);
    expect((screen.getByRole("checkbox", { name: "Declared" }) as HTMLInputElement).checked).toBe(false);
    expect((screen.getByRole("checkbox", { name: "Deduced" }) as HTMLInputElement).checked).toBe(true);
  });

  it("ignores an address that names something the data does not have", async () => {
    await open({}, "/topology?site=nowhere&vlan=nothing&classes=bogus");
    expect(tableRows()).toHaveLength(4);
  });

  it("says when nothing matches", async () => {
    await open();
    const user = userEvent.setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "VLAN" }), "VLAN 10");
    await user.click(screen.getByRole("checkbox", { name: "Deduced" }));
    expect(await screen.findByText("No link matches these filters.")).toBeDefined();
    expect(screen.queryByRole("table")).toBeNull();
  });
});

describe("what the page says it did not draw", () => {
  it("says when there is no link at all", async () => {
    mockApi(routes({ links: [], context: [] }));
    renderApp({ at: "/topology" });
    expect(await screen.findByText("No link between ports is recorded yet.")).toBeDefined();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("says when the inventory holds more than the page read", async () => {
    await open({ truncated: true });
    expect(screen.getByText("The inventory holds more than this page reads: the picture is partial.")).toBeDefined();
  });

  it("does not say it when nothing was cut", async () => {
    await open();
    expect(screen.queryByText(/the picture is partial/)).toBeNull();
  });

  it("counts a cable that does not have two ports", async () => {
    const links = [...sampleLinks(), rel("connects", { ...CABLE1, id: "p-cable-2", display: "C-0002" }, A1E2, { attributes: { end: "a" } })];
    await open({ links });
    expect(screen.getByText("1 cable does not have exactly two ports and is not shown.")).toBeDefined();
    expect(tableRows()).toHaveLength(4);
  });

  it("counts a link inside one device: in the table, not in the drawing", async () => {
    const links = [...sampleLinks(), rel("cabled_to", A1E1, A1E2, { confidence: "observed" })];
    await open({ links });
    expect(screen.getByText(/1 link joins two ports of the same device/)).toBeDefined();
    expect(tableRows()).toHaveLength(5);
    expect(screen.getByRole("img").getAttribute("data-edges")).toBe("4");
  });

  it("counts a link that does not join two ports", async () => {
    const links = [...sampleLinks(), rel("cabled_to", A1E1, { id: T.srv01, type: "device", display: "srv-01" })];
    await open({ links });
    expect(screen.getByText("1 link does not join two ports and is not shown.")).toBeDefined();
  });
});

describe("failure", () => {
  it("says so, and retries", async () => {
    const r = routes();
    let fail = true;
    const inner = r["GET /relations"] as (c: Call) => unknown;
    r["GET /relations"] = (c: Call) => (fail ? problem(500, "internal", "database down") : inner(c));
    mockApi(r);
    renderApp({ at: "/topology" });
    expect(await screen.findByRole("alert")).toBeDefined();
    fail = false;
    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("table")).toBeDefined();
  });
});

describe("accessibility", () => {
  it("has no violation in English", async () => {
    await open();
    await expectNoA11yViolations();
  });

  it("has no violation in French, and speaks French", async () => {
    mockApi(routes());
    renderApp({ at: "/topology", locale: "fr" });
    await screen.findByRole("table");
    expect(screen.getByRole("heading", { level: 1, name: "Topologie" })).toBeDefined();
    expect(screen.getByText("4 liens · 4 équipements")).toBeDefined();
    await expectNoA11yViolations();
  });
});

describe("reading every page", () => {
  const pageOf = (n: number, from: number, total: number, truncated = false) => ({
    total,
    limit: 1000,
    offset: from,
    truncated,
    items: Array.from({ length: n }, (_, i) => from + i),
  });

  it("follows the offsets until the total is reached", async () => {
    const seen: number[] = [];
    const r = await readAll((offset) => {
      seen.push(offset);
      return Promise.resolve(pageOf(Math.min(1000, 2500 - offset), offset, 2500));
    });
    expect(seen).toEqual([0, 1000, 2000]);
    expect(r.items).toHaveLength(2500);
    expect(r.truncated).toBe(false);
  });

  it("stops on an empty page", async () => {
    const r = await readAll((offset) => Promise.resolve(offset === 0 ? pageOf(3, 0, 10) : pageOf(0, offset, 10)));
    expect(r.items).toHaveLength(3);
  });

  it("says it stopped at the page cap", async () => {
    let calls = 0;
    const r = await readAll((offset) => {
      calls++;
      return Promise.resolve(pageOf(1000, offset, 1_000_000));
    });
    // Ten pages of a thousand: the figure is the documented bound (docs/reference/ui.md), not a free parameter.
    expect(MAX_PAGES).toBe(10);
    expect(calls).toBe(10);
    expect(r.items).toHaveLength(10_000);
    expect(r.truncated).toBe(true);
  });

  it("passes on that the server stopped at its own bound, even though the pages ended", async () => {
    const r = await readAll((offset) => Promise.resolve(pageOf(offset === 0 ? 5 : 0, offset, 5, true)));
    expect(r.truncated).toBe(true);
  });
});
