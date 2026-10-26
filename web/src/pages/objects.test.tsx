import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ObjectView } from "../api/types";
import { expectNoA11yViolations } from "../test/axe";
import { ID, page, siteParis, srv01, swA1 } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp, type Call } from "../test/support";
import { filtersQuery, readFilters } from "./ObjectsPage";

const gone: ObjectView = { ...srv01, id: "0a000000-0000-4000-8000-0000000000b1", display: "srv-old", status: "absent", site_id: ID.site };

/** Sites for the site filter; every other list answers the objects below. */
const routes = (items: ObjectView[] = [swA1, { ...srv01, site_id: ID.site }, gone], total = items.length) => ({
  ...defaultRoutes(),
  "GET /objects": (c: Call) => (c.query.get("type") === "site" && !c.query.get("q") ? page([siteParis]) : { ...page(items), total, limit: 50, offset: Number(c.query.get("offset") ?? 0) }),
});

const listCall = (api: ReturnType<typeof mockApi>) => api.calls.filter((c) => c.path === "/objects" && c.query.get("type") !== "site").at(-1);

describe("all objects", () => {
  it("is in the menu and leads to the list", async () => {
    mockApi(routes());
    renderApp({ at: "/" });
    const user = userEvent.setup();
    const nav = await screen.findByRole("navigation", { name: /main/i });
    await user.click(within(nav).getByRole("link", { name: "All objects" }));
    expect(await screen.findByRole("heading", { level: 1, name: "All objects" })).toBeInTheDocument();
  });

  it("lists objects of every type, with their type, site, health and presence", async () => {
    const api = mockApi(routes());
    renderApp({ at: "/objects" });
    const row = (await screen.findByRole("link", { name: "srv-01" })).closest("tr") as HTMLElement;
    const q = listCall(api)?.query;
    expect(q?.has("q")).toBe(false);
    expect(q?.has("type")).toBe(false);
    expect(q?.get("sort")).toBe("display");
    expect(q?.get("limit")).toBe("50");
    expect(row.querySelector('td[data-label="Type"]')).toHaveTextContent("Server");
    expect(within(row.querySelector('td[data-label="Parent / site"]') as HTMLElement).getByRole("link", { name: "paris-1" })).toHaveAttribute("href", `/objects/${ID.site}`);
    expect(row.querySelector('td[data-label="Health"]')).toHaveTextContent("Warning");
    const old = (screen.getByRole("link", { name: "srv-old" })).closest("tr") as HTMLElement;
    expect(old.querySelector('td[data-label="Health"]')).toHaveTextContent("Absent");
    expect(screen.getByRole("status")).toHaveTextContent("3 objects");
  });

  it("names the parent of a component, in the column where a device shows its site", async () => {
    const cpu: ObjectView = {
      ...srv01,
      id: "0a000000-0000-4000-8000-0000000000c1",
      type: "cpu",
      display: "Proc 1 · Intel Xeon E5-2695 v4 @ 2.10GHz",
      attributes: { name: "Proc 1" },
      parent_id: ID.srv01,
      parent: { id: ID.srv01, type: "device", display: "srv-01" },
      site_id: ID.site,
    };
    mockApi(routes([{ ...swA1 }, cpu]));
    renderApp({ at: "/objects" });
    const row = (await screen.findByRole("link", { name: cpu.display })).closest("tr") as HTMLElement;
    const cell = row.querySelector('td[data-label="Parent / site"]') as HTMLElement;
    expect(within(cell).getByRole("link", { name: "srv-01" })).toHaveAttribute("href", `/objects/${ID.srv01}`);
    expect(within(cell).queryByRole("link", { name: "paris-1" })).toBeNull();
    // A device without parent keeps its site.
    const dev = screen.getByRole("link", { name: "sw-a1" }).closest("tr") as HTMLElement;
    expect(within(dev.querySelector('td[data-label="Parent / site"]') as HTMLElement).getByRole("link", { name: "paris-1" })).toBeInTheDocument();
  });

  it("turns the filters of the address into a TQL query", async () => {
    const api = mockApi(routes());
    renderApp({ at: `/objects?q=vendor:dell&type=device&site=${ID.site}&health=warning&presence=all&sort=-last_seen,display&offset=50` });
    await screen.findByRole("link", { name: "srv-01" });
    const q = listCall(api)?.query;
    expect(q?.get("q")).toBe(`site:${ID.site} health:warning status:active,absent (vendor:dell)`);
    expect(q?.get("type")).toBe("device");
    expect(q?.get("sort")).toBe("-last_seen,display");
    expect(q?.get("offset")).toBe("50");
    expect(screen.getByRole("combobox", { name: "Site" })).toHaveValue(ID.site);
    expect(screen.getByRole("combobox", { name: "Presence" })).toHaveValue("all");
    expect(screen.getByRole("link", { name: "Clear filters" })).toHaveAttribute("href", "/objects");
  });

  it("the form moves to a new address and asks again", async () => {
    const api = mockApi(routes());
    renderApp({ at: "/objects" });
    const user = userEvent.setup();
    await screen.findByRole("link", { name: "srv-01" });
    await user.selectOptions(screen.getByRole("combobox", { name: "Type" }), "device");
    await user.selectOptions(screen.getByRole("combobox", { name: "Site" }), ID.site);
    await user.selectOptions(screen.getByRole("combobox", { name: "Presence" }), "absent");
    await user.click(screen.getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(listCall(api)?.query.get("q")).toBe(`site:${ID.site} status:absent`));
    expect(listCall(api)?.query.get("type")).toBe("device");
  });

  it("pages through a long list", async () => {
    mockApi(routes([swA1], 120));
    renderApp({ at: "/objects?type=device&offset=50" });
    const pager = await screen.findByRole("navigation", { name: "Pagination" });
    expect(within(pager).getByRole("link", { name: "Next" })).toHaveAttribute("href", "/objects?type=device&offset=100");
  });

  it("says when the query is not understood", async () => {
    mockApi({ ...routes(), "GET /objects": (c: Call) => (c.query.get("type") === "site" ? page([siteParis]) : problem(400, "bad-query", "unexpected ')'")) });
    renderApp({ at: "/objects?q=a)" });
    expect(await screen.findByText("The query was not understood")).toBeInTheDocument();
  });

  it("says when there is none", async () => {
    mockApi(routes([]));
    renderApp({ at: "/objects?health=critical" });
    expect(await screen.findByText(/No object matches/)).toBeInTheDocument();
  });

  it("has no accessibility violation", async () => {
    mockApi(routes());
    renderApp({ at: "/objects" });
    await screen.findByRole("link", { name: "srv-01" });
    await new Promise((r) => setTimeout(r, 60));
    await expectNoA11yViolations();
  });
});

describe("filtersQuery", () => {
  const base = readFilters(new URLSearchParams());

  it("asks for present objects of the whole inventory by default", () => {
    expect(base).toEqual({ text: "", type: "", site: "", health: "", presence: "active", sort: "display" });
    expect(filtersQuery(base)).toBe("");
  });

  it("groups the reader's text so that an OR stays inside the other filters", () => {
    expect(filtersQuery({ ...base, health: "critical", text: "vendor:dell OR vendor:hpe" })).toBe("health:critical (vendor:dell OR vendor:hpe)");
  });

  it("quotes a site that needs it", () => {
    expect(filtersQuery({ ...base, site: 'paris "nord"' })).toBe('site:"paris \\"nord\\""');
  });

  it("ignores an unknown presence or sort", () => {
    const f = readFilters(new URLSearchParams("presence=gone&sort=drop"));
    expect(f.presence).toBe("active");
    expect(f.sort).toBe("display");
  });
});
